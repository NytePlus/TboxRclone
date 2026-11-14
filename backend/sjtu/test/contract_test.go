package test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/backend/sjtu"
	"github.com/rclone/rclone/fstest/fstests"
)

type contractNode struct {
	path     string
	dir      bool
	data     []byte
	etag     string
	modified time.Time
}

type contractUpload struct {
	path, strategy, id string
	parts              map[int][]byte
	confirmed          bool
}

type contractSMH struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	nodes   map[string]*contractNode
	uploads map[string]*contractUpload
	serial  atomic.Uint64
}

type loopbackOnlyTransport struct {
	host string
	next http.RoundTripper
}

func (t loopbackOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != t.host {
		return nil, fmt.Errorf("local contract test rejected non-loopback HTTP host %q", r.URL.Host)
	}
	return t.next.RoundTrip(r)
}

func newContractSMH(t *testing.T) *contractSMH {
	t.Helper()
	s := &contractSMH{t: t, nodes: map[string]*contractNode{}, uploads: map[string]*contractUpload{}}
	s.putDir("codex-api-lab")
	s.putDir("codex-api-lab/local-contract")
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.server.Close)
	return s
}

func key(p string) string { return strings.ToLower(strings.Trim(p, "/")) }

func (s *contractSMH) putDir(p string) {
	s.nodes[key(p)] = &contractNode{path: p, dir: true, modified: time.Now().UTC()}
}

func (s *contractSMH) item(n *contractNode) map[string]any {
	typ := "file"
	if n.dir {
		typ = "dir"
	}
	name := path.Base(n.path)
	if n.path == "" {
		name = ""
	}
	return map[string]any{
		"name": name, "path": strings.Split(n.path, "/"), "type": typ,
		"size": len(n.data), "eTag": n.etag, "inode": "local-" + key(n.path),
		"modificationTime": n.modified,
	}
}

func (s *contractSMH) apiPath(r *http.Request) (kind, p string, ok bool) {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(r.URL.Path, prefix) || r.URL.Query().Get("access_token") != "local-test-token" {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, prefix), "/", 4)
	if len(parts) < 3 || parts[1] != "local-library" || parts[2] != "local-space" {
		return "", "", false
	}
	if len(parts) == 4 {
		p = strings.Trim(parts[3], "/")
	}
	return parts[0], p, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (s *contractSMH) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/data/") {
		s.putPart(w, r)
		return
	}
	kind, p, ok := s.apiPath(r)
	if !ok {
		s.t.Errorf("contract test escaped local SMH API: %s %s", r.Method, r.URL.Redacted())
		http.Error(w, "local contract endpoint only", http.StatusBadGateway)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handleAPI(w, r, kind, p)
}

func (s *contractSMH) handleAPI(w http.ResponseWriter, r *http.Request, kind, p string) {
	if kind == "directory" {
		s.handleDirectory(w, r, p)
		return
	}
	if kind != "file" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("multipart"):
		s.beginUpload(w, r, p)
	case r.Method == http.MethodPost && q.Has("renew"):
		s.renewUpload(w, r, p)
	case r.Method == http.MethodPost && q.Has("confirm"):
		s.confirmUpload(w, p)
	case r.Method == http.MethodGet && q.Has("upload"):
		s.uploadState(w, p)
	case r.Method == http.MethodGet:
		s.getFile(w, r, p)
	case r.Method == http.MethodPut:
		s.move(w, r, p, false)
	case r.Method == http.MethodDelete && q.Has("upload"):
		delete(s.uploads, p)
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodDelete:
		s.deleteNode(w, p, false)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "MethodNotAllowed"})
	}
}

func (s *contractSMH) handleDirectory(w http.ResponseWriter, r *http.Request, p string) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && q.Has("info"):
		n := s.nodes[key(p)]
		if n == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
			return
		}
		writeJSON(w, http.StatusOK, s.item(n))
	case r.Method == http.MethodGet:
		n := s.nodes[key(p)]
		if n == nil || !n.dir {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
			return
		}
		prefix := strings.Trim(p, "/")
		if prefix != "" {
			prefix += "/"
		}
		contents := make([]map[string]any, 0)
		for _, child := range s.nodes {
			if strings.HasPrefix(child.path, prefix) && !strings.Contains(strings.TrimPrefix(child.path, prefix), "/") {
				contents = append(contents, s.item(child))
			}
		}
		sort.Slice(contents, func(i, j int) bool { return contents[i]["name"].(string) < contents[j]["name"].(string) })
		writeJSON(w, http.StatusOK, map[string]any{"contents": contents, "nextMarker": ""})
	case r.Method == http.MethodPut:
		var body struct {
			From string `json:"from"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.From != "" {
			s.moveDecoded(w, body.From, p, true, r.URL.Query().Get("conflict_resolution_strategy"))
			return
		}
		if _, exists := s.nodes[key(p)]; exists {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "AlreadyExists"})
			return
		}
		s.putDir(p)
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodDelete:
		s.deleteNode(w, p, true)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "MethodNotAllowed"})
	}
}

func (s *contractSMH) beginUpload(w http.ResponseWriter, r *http.Request, p string) {
	strategy := r.URL.Query().Get("conflict_resolution_strategy")
	if old := s.nodes[key(p)]; old != nil && strategy != "overwrite" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "AlreadyExists"})
		return
	}
	id := fmt.Sprintf("upload-%d", s.serial.Add(1))
	u := &contractUpload{path: p, strategy: strategy, id: id, parts: map[int][]byte{}}
	s.uploads[id] = u
	s.writeUpload(w, u, requestedParts(r))
}

func requestedParts(r *http.Request) []int {
	var body struct {
		PartNumberRange []int `json:"partNumberRange"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.PartNumberRange
}

func (s *contractSMH) writeUpload(w http.ResponseWriter, u *contractUpload, numbers []int) {
	parts := map[string]any{}
	for _, number := range numbers {
		parts[strconv.Itoa(number)] = map[string]any{"headers": map[string]string{"x-local-contract": "1"}}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain": s.server.URL, "path": "/data/" + u.id, "confirmKey": u.id,
		"uploadId": u.id, "parts": parts,
	})
}

func (s *contractSMH) renewUpload(w http.ResponseWriter, r *http.Request, id string) {
	u := s.uploads[id]
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	s.writeUpload(w, u, requestedParts(r))
}

func (s *contractSMH) putPart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/data/")
	number, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.uploads[id]
	if u == nil || number < 1 || r.Header.Get("x-local-contract") != "1" {
		http.Error(w, "unknown upload", http.StatusNotFound)
		return
	}
	u.parts[number] = b
	w.Header().Set("ETag", fmt.Sprintf("\"part-%d\"", number))
	w.WriteHeader(http.StatusOK)
}

func (s *contractSMH) confirmUpload(w http.ResponseWriter, id string) {
	u := s.uploads[id]
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	if s.nodes[key(u.path)] != nil && u.strategy != "overwrite" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "AlreadyExists"})
		return
	}
	numbers := make([]int, 0, len(u.parts))
	for number := range u.parts {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	var data []byte
	for _, number := range numbers {
		data = append(data, u.parts[number]...)
	}
	hash := sha256.Sum256(data)
	n := &contractNode{path: u.path, data: data, etag: hex.EncodeToString(hash[:]), modified: time.Now().UTC()}
	s.nodes[key(u.path)] = n
	u.confirmed = true
	writeJSON(w, http.StatusOK, s.item(n))
}

func (s *contractSMH) uploadState(w http.ResponseWriter, id string) {
	u := s.uploads[id]
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	parts := make([]map[string]any, 0, len(u.parts))
	for number, data := range u.parts {
		parts = append(parts, map[string]any{"PartNumber": number, "Size": len(data), "ETag": fmt.Sprintf("\"part-%d\"", number)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": u.confirmed, "uploadId": u.id, "parts": parts, "path": strings.Split(u.path, "/")})
}

func (s *contractSMH) getFile(w http.ResponseWriter, r *http.Request, p string) {
	n := s.nodes[key(p)]
	if n == nil || n.dir {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	w.Header().Set("ETag", strconv.Quote(n.etag))
	if match := strings.Trim(r.Header.Get("If-Match"), "\""); match != n.etag {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"code": "PreconditionFailed"})
		return
	}
	data := n.data
	if value := r.Header.Get("Range"); value != "" {
		var start, end int
		if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= len(data) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *contractSMH) move(w http.ResponseWriter, r *http.Request, target string, directory bool) {
	var body struct {
		From string `json:"from"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.From == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "Invalid"})
		return
	}
	s.moveDecoded(w, body.From, target, directory, r.URL.Query().Get("conflict_resolution_strategy"))
}

func (s *contractSMH) moveDecoded(w http.ResponseWriter, source, target string, directory bool, strategy string) {
	n := s.nodes[key(source)]
	if n == nil || n.dir != directory {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	if s.nodes[key(target)] != nil && strategy != "overwrite" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "AlreadyExists"})
		return
	}
	if strategy == "overwrite" {
		delete(s.nodes, key(target))
	}
	changes := map[string]*contractNode{}
	for k, child := range s.nodes {
		if k == key(source) || directory && strings.HasPrefix(k, key(source)+"/") {
			newPath := target + strings.TrimPrefix(child.path, source)
			copyNode := *child
			copyNode.path = newPath
			changes[key(newPath)] = &copyNode
			delete(s.nodes, k)
		}
	}
	for k, child := range changes {
		s.nodes[k] = child
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *contractSMH) deleteNode(w http.ResponseWriter, p string, directory bool) {
	n := s.nodes[key(p)]
	if n == nil || n.dir != directory {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NotFound"})
		return
	}
	if directory {
		for k := range s.nodes {
			if strings.HasPrefix(k, key(p)+"/") {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "DirectoryNotEmpty"})
				return
			}
		}
	}
	delete(s.nodes, key(p))
	writeJSON(w, http.StatusOK, map[string]string{"recycledItemId": "local-recycle"})
}

// TestBackendContract is the mandatory upstream rclone filesystem contract.
// It never reads a real rclone remote and every control/data request terminates
// at the loopback-only TLS server above.
func TestBackendContract(t *testing.T) {
	sim := newContractSMH(t)
	oldTransport := http.DefaultTransport
	http.DefaultTransport = loopbackOnlyTransport{host: strings.TrimPrefix(sim.server.URL, "https://"), next: sim.server.Client().Transport}
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	root := t.TempDir()
	token := root + "/token"
	if err := os.WriteFile(token, []byte("local-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/state", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/state/write-root", []byte("codex-api-lab/local-contract\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const name = "TboxLocalContract"
	values := map[string]string{
		"type": "sjtu", "endpoint": sim.server.URL, "library_id": "local-library", "space_id": "local-space",
		"token_file": token, "state_dir": root + "/state", "ownership_dir": root + "/owners",
		"lab_writes": "true", "lab_overwrite": "true", "lab_delete": "true", "lab_move": "true",
		"max_spool": "1G", "max_upload": "128M",
	}
	extra := make([]fstests.ExtraConfigItem, 0, len(values))
	for k, v := range values {
		extra = append(extra, fstests.ExtraConfigItem{Name: name, Key: k, Value: v})
	}
	t.Chdir("../../../third_party/rclone")
	fstests.Run(t, &fstests.Opt{
		RemoteName:  name + ":codex-api-lab/local-contract",
		NilObject:   (*sjtu.Object)(nil),
		ExtraConfig: extra,
	})
}
