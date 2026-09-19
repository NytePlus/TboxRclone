// Package webdavguard serializes conflicting WebDAV requests before they
// reach rclone. It is the HTTP-facing part of TboxRclone's single-writer
// contract; durable path reservations remain the backend's responsibility.
package webdavguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Guard rejects overlapping request claims instead of queueing them.
type Guard struct {
	base      string
	next      http.Handler
	stageZero bool
	stateDir  string

	mu               sync.Mutex
	nextID           uint64
	active           map[uint64][]claim
	pending          map[string]*zeroPending
	syntheticUnlocks map[string]string
}

type zeroPending struct {
	Path     string `json:"path"`
	Locks    int    `json:"locks"`
	Unlocks  int    `json:"unlocks"`
	Token    string `json:"token,omitempty"`
	LockBody string `json:"lock_body,omitempty"`
}

var errZeroPending = errors.New("zero-byte creation is already pending")

type claim struct {
	path        string
	write, tree bool
}

// New constructs a guard for a WebDAV handler mounted at basePath.
func New(basePath string, next http.Handler) (*Guard, error) {
	if next == nil {
		return nil, errors.New("WebDAV guard requires an upstream handler")
	}
	base, err := cleanBase(basePath)
	if err != nil {
		return nil, err
	}
	return &Guard{base: base, next: next, active: make(map[uint64][]claim), pending: make(map[string]*zeroPending), syntheticUnlocks: make(map[string]string)}, nil
}

// NewStagingProxy enables the macOS webdavfs zero-byte creation barrier. The
// state directory is durable so a process restart does not publish or forget a
// placeholder silently.
func NewStagingProxy(basePath string, upstream *url.URL, stateDir string) (*Guard, error) {
	g, err := NewProxy(basePath, upstream)
	if err != nil {
		return nil, err
	}
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return nil, errors.New("zero PUT staging requires an absolute state directory")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	g.stageZero, g.stateDir = true, stateDir
	if err := g.loadPending(); err != nil {
		return nil, err
	}
	return g, nil
}

// NewProxy constructs a guard which forwards accepted requests to upstream.
func NewProxy(basePath string, upstream *url.URL) (*Guard, error) {
	if upstream == nil || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" {
		return nil, errors.New("WebDAV guard requires a plain HTTP(S) upstream origin")
	}
	if upstream.Path != "" && upstream.Path != "/" {
		return nil, errors.New("WebDAV guard upstream must not contain a path")
	}
	return New(basePath, httputil.NewSingleHostReverseProxy(upstream))
}

func cleanBase(value string) (string, error) {
	if value == "" || value == "/" {
		return "", nil
	}
	if strings.ContainsAny(value, "?#") {
		return "", errors.New("WebDAV base path must not contain a query or fragment")
	}
	cleaned := path.Clean("/" + strings.Trim(value, "/"))
	if cleaned == "/" {
		return "", nil
	}
	return cleaned, nil
}

func (g *Guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.stageZero {
		if handled := g.handleZeroBoundary(w, r); handled {
			return
		}
	}
	claims, status := g.claims(r)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if len(claims) == 0 {
		g.next.ServeHTTP(w, r)
		return
	}
	release, ok := g.acquire(claims)
	if !ok {
		http.Error(w, http.StatusText(http.StatusLocked), http.StatusLocked)
		return
	}
	defer release()
	g.next.ServeHTTP(w, r)
}

func (g *Guard) pendingPath(p string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pending[p]
	return ok
}

func (g *Guard) handleZeroBoundary(w http.ResponseWriter, r *http.Request) bool {
	p, err := g.localPath(r.URL.Path)
	if err != nil {
		return false
	}
	if r.Method == http.MethodPut && r.ContentLength == 0 && r.Header.Get("If") == "" && r.Header.Get("If-Match") == "" && r.Header.Get("If-None-Match") == "" {
		if err := g.stage(p); err != nil {
			if errors.Is(err, errZeroPending) {
				http.Error(w, http.StatusText(http.StatusLocked), http.StatusLocked)
			} else {
				http.Error(w, err.Error(), http.StatusInsufficientStorage)
			}
			return true
		}
		w.WriteHeader(http.StatusCreated)
		return true
	}
	// Serialize token validation and receipt mutation with content publication.
	if r.Method == http.MethodPut || r.Method == "LOCK" || r.Method == "UNLOCK" {
		release, ok := g.acquire([]claim{{path: p, write: true, tree: true}})
		if !ok {
			http.Error(w, http.StatusText(http.StatusLocked), http.StatusLocked)
			return true
		}
		defer release()
	}
	g.mu.Lock()
	pending := g.pending[p]
	if pending != nil {
		snapshot := *pending
		pending = &snapshot
	}
	if r.Method == "UNLOCK" && pending == nil && g.syntheticUnlocks[p] != "" {
		if r.Header.Get("Lock-Token") != "<"+g.syntheticUnlocks[p]+">" {
			g.mu.Unlock()
			http.Error(w, "lock token mismatch", http.StatusConflict)
			return true
		}
		delete(g.syntheticUnlocks, p)
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	g.mu.Unlock()
	if pending == nil {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == "PROPFIND" {
		// The local resource exists after 201; cloud publication still waits for PUT.
		servePending(w, r)
		return true
	}
	if r.Method == http.MethodPut && (r.ContentLength != 0 || r.Header.Get("If") != "") {
		// Let the real content PUT establish the object. Remove the barrier only
		// after the upstream response confirms that request.
		forward := r.Clone(r.Context())
		forward.Header = r.Header.Clone()
		// The LOCK was synthesized by this guard, so its WebDAV If token is not
		// known to the core. Preserve entity-tag conditions, but remove only the
		// lock-token predicate before forwarding the content PUT.
		if pending.Locks > pending.Unlocks {
			if pending.Token == "" || r.Header.Get("If") != "(<"+pending.Token+">)" {
				http.Error(w, "lock token mismatch", http.StatusPreconditionFailed)
				return true
			}
			// Only this exact token-only condition is discharged here. Other
			// conditions are not silently removed.
			forward.Header.Del("If")
		}
		capture := newStatusCapture()
		g.next.ServeHTTP(capture, forward)
		if capture.status >= 200 && capture.status < 300 {
			g.mu.Lock()
			if pending.Token != "" {
				g.syntheticUnlocks[p] = pending.Token
			}
			g.mu.Unlock()
			if err := g.clear(p); err != nil {
				// The upstream object is already published, but the durable barrier
				// must remain unresolved if its receipt cannot be removed safely.
				http.Error(w, "published content has an unresolved local receipt: "+err.Error(), http.StatusInsufficientStorage)
				return true
			}
		}
		capture.commit(w)
		return true
	}
	if r.Method == "LOCK" {
		if r.ContentLength == 0 {
			g.mu.Lock()
			current := g.pending[p]
			valid := current != nil && current.Token != "" && r.Header.Get("If") == "(<"+current.Token+">)"
			var body string
			if valid {
				body = current.LockBody
			}
			g.mu.Unlock()
			if !valid {
				http.Error(w, "lock token mismatch", http.StatusPreconditionFailed)
				return true
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			_, _ = io.WriteString(w, body)
			return true
		}
		token, body, err := lockResponse(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		if err := g.grantLock(p, token, string(body)); err != nil {
			status := http.StatusInsufficientStorage
			if errors.Is(err, errZeroPending) {
				status = http.StatusLocked
			}
			http.Error(w, err.Error(), status)
			return true
		}
		w.Header().Set("Lock-Token", "<"+token+">")
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return true
	}
	if r.Method == "UNLOCK" {
		if pending.Token == "" || r.Header.Get("Lock-Token") != "<"+pending.Token+">" {
			http.Error(w, "lock token mismatch", http.StatusConflict)
			return true
		}
		// Unlock count is not a commit signal: Finder closes and reopens
		// placeholders before sending their content. Keep the durable barrier
		// until an explicit PUT, including a token-conditioned empty PUT.

		if err := g.bumpUnlock(p); err != nil {
			http.Error(w, err.Error(), http.StatusInsufficientStorage)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

type statusCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newStatusCapture() *statusCapture {
	return &statusCapture{header: make(http.Header), status: http.StatusOK}
}
func (w *statusCapture) Header() http.Header    { return w.header }
func (w *statusCapture) WriteHeader(status int) { w.status = status }
func (w *statusCapture) Write(p []byte) (int, error) {
	return w.body.Write(p)
}
func (w *statusCapture) commit(dst http.ResponseWriter) {
	for key, values := range w.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	dst.WriteHeader(w.status)
	_, _ = dst.Write(w.body.Bytes())
}

func (g *Guard) stage(p string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.pending[p]; ok {
		return errZeroPending
	}
	x := &zeroPending{Path: p}
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	if err = g.writeState(p, b); err != nil {
		// A rename may have succeeded even when syncing the directory failed.
		// Keep the in-memory reservation whenever a receipt exists so this
		// process cannot accept a conflicting request while durability is unsure.
		if _, statErr := os.Stat(g.stateFile(p)); statErr == nil {
			g.pending[p] = x
		}
		return err
	}
	g.pending[p] = x
	return nil
}
func (g *Guard) bumpUnlock(p string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	x := g.pending[p]
	if x == nil {
		return nil
	}
	candidate := *x
	candidate.Unlocks++
	candidate.Token, candidate.LockBody = "", ""
	if err := g.writeState(p, mustJSON(&candidate)); err != nil {
		return err
	}
	*x = candidate
	return nil
}
func (g *Guard) grantLock(p, token, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	x := g.pending[p]
	if x == nil || x.Locks > x.Unlocks {
		return errZeroPending
	}
	candidate := *x
	candidate.Locks++
	candidate.Token, candidate.LockBody = token, body
	if err := g.writeState(p, mustJSON(&candidate)); err != nil {
		return err
	}
	*x = candidate
	return nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func (g *Guard) clear(p string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := os.Remove(g.stateFile(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncDirectory(g.stateDir); err != nil {
		return err
	}
	delete(g.pending, p)
	return nil
}
func (g *Guard) stateFile(p string) string {
	return filepath.Join(g.stateDir, fmt.Sprintf("%x.json", []byte(p)))
}
func (g *Guard) writeState(p string, b []byte) error {
	f, err := os.CreateTemp(g.stateDir, ".zero-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, g.stateFile(p)); err != nil {
		return err
	}
	return syncDirectory(g.stateDir)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
func (g *Guard) loadPending() error {
	entries, err := os.ReadDir(g.stateDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(g.stateDir, e.Name()))
		if err != nil {
			return err
		}
		var x zeroPending
		if err = json.Unmarshal(b, &x); err != nil || x.Path == "" {
			return errors.New("invalid zero PUT state")
		}
		g.pending[x.Path] = &x
	}
	return nil
}
func (g *Guard) publishEmpty(original *http.Request, p string) error {
	req := original.Clone(original.Context())
	req.Method = http.MethodPut
	req.URL.Path = g.base + p
	if req.URL.Path == "" {
		req.URL.Path = "/"
	}
	req.URL.RawPath = ""
	req.Body = io.NopCloser(bytes.NewReader(nil))
	req.ContentLength = 0
	req.Header = original.Header.Clone()
	req.Header.Del("If")
	req.Header.Del("Lock-Token")
	rec := newCapture()
	g.next.ServeHTTP(rec, req)
	if rec.status < 200 || rec.status >= 300 {
		return fmt.Errorf("empty publication upstream status %d", rec.status)
	}
	return nil
}

type responseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapture() *responseCapture             { return &responseCapture{header: make(http.Header)} }
func (c *responseCapture) Header() http.Header { return c.header }
func (c *responseCapture) WriteHeader(s int)   { c.status = s }
func (c *responseCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.body.Write(p)
}

func (g *Guard) claims(r *http.Request) ([]claim, int) {
	source, err := g.localPath(r.URL.Path)
	if err != nil {
		return nil, http.StatusBadRequest
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, "PROPFIND":
		return []claim{{path: source, tree: r.URL.Query().Get("download") == "zip"}}, 0
	case http.MethodPut, http.MethodDelete, "MKCOL", "PROPPATCH", "LOCK", "UNLOCK":
		return []claim{{path: source, write: true, tree: true}}, 0
	case "COPY", "MOVE":
		target, status := g.destination(r)
		if status != 0 {
			return nil, status
		}
		if contains(source, target) || contains(target, source) {
			return nil, http.StatusForbidden
		}
		return []claim{
			{path: source, write: r.Method == "MOVE", tree: true},
			{path: target, write: true, tree: true},
		}, 0
	default:
		return nil, 0
	}
}

func (g *Guard) destination(r *http.Request) (string, int) {
	value := r.Header.Get("Destination")
	if value == "" {
		return "", http.StatusBadRequest
	}
	destination, err := url.Parse(value)
	if err != nil || destination.Path == "" || destination.RawQuery != "" || destination.Fragment != "" || destination.User != nil {
		return "", http.StatusBadRequest
	}
	if destination.Host != "" && !strings.EqualFold(destination.Host, r.Host) {
		return "", http.StatusBadRequest
	}
	target, err := g.localPath(destination.Path)
	if err != nil {
		return "", http.StatusBadRequest
	}
	return target, 0
}

// localPath converts public request and Destination paths into one namespace.
func (g *Guard) localPath(value string) (string, error) {
	public := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if g.base != "" {
		if public != g.base && !strings.HasPrefix(public, g.base+"/") {
			return "", errors.New("path is outside the configured WebDAV base")
		}
		public = strings.TrimPrefix(public, g.base)
		if public == "" {
			public = "/"
		}
	}
	return path.Clean("/" + strings.TrimPrefix(public, "/")), nil
}

func contains(parent, child string) bool {
	return parent == child || parent == "/" || strings.HasPrefix(child, parent+"/")
}

func conflicts(left, right claim) bool {
	overlap := left.path == right.path || (left.tree && contains(left.path, right.path)) || (right.tree && contains(right.path, left.path))
	return overlap && (left.write || right.write)
}

func (g *Guard) acquire(claims []claim) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, existing := range g.active {
		for _, left := range existing {
			for _, right := range claims {
				if conflicts(left, right) {
					return nil, false
				}
			}
		}
	}
	g.nextID++
	id := g.nextID
	g.active[id] = append([]claim(nil), claims...)
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.active, id)
			g.mu.Unlock()
		})
	}, true
}
