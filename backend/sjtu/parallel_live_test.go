package sjtu

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/transfer"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
)

type parallelFileKey struct{}

type parallelTransport struct {
	cancelFirst                                   context.CancelFunc
	cancelOnce                                    sync.Once
	cancelledAcks, initializations, confirmations int
	cancelledPart                                 int
	base                                          http.RoundTripper
	mu                                            sync.Mutex
	seen                                          map[string]bool
	active                                        map[string]int
	gate                                          chan struct{}
	maximum                                       int
}

func (p *parallelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	if r.Method == "POST" && r.URL.Query().Has("multipart") {
		p.initializations++
	}
	if r.Method == "POST" && r.URL.Query().Has("confirm") {
		p.confirmations++
	}
	p.mu.Unlock()
	id := r.URL.Query().Get("uploadId")
	if r.Method != "PUT" || id == "" {
		return p.base.RoundTrip(r)
	}
	p.mu.Lock()
	if !p.seen[id] {
		p.seen[id] = true
		if len(p.seen) == 2 {
			close(p.gate)
		}
	}
	p.active[id]++
	if len(p.active) > p.maximum {
		p.maximum = len(p.active)
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.active[id]--
		if p.active[id] == 0 {
			delete(p.active, id)
		}
	}()
	// Hold data requests until two different sessions reach this boundary.
	// A serial backend cannot pass by rapidly executing its goroutines in turn.
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-p.gate:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-timer.C:
		return nil, errors.New("distinct sessions did not overlap")
	}
	resp, err := p.base.RoundTrip(r)
	if p.cancelFirst != nil && r.Context().Value(parallelFileKey{}) == 0 && err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		interrupted := false
		p.cancelOnce.Do(func() {
			p.mu.Lock()
			p.cancelledAcks++
			p.cancelledPart, _ = strconv.Atoi(r.URL.Query().Get("partNumber"))
			p.mu.Unlock()
			interrupted = true
			p.cancelFirst()
		})
		if interrupted {
			resp.Body.Close()
			return nil, context.Canceled
		}
	}
	return resp, err
}

func TestLiveDistinctUploads(t *testing.T) {
	if os.Getenv("TBOX_LIVE_PARALLEL") != "1" {
		t.Skip("opt-in isolated real-cloud parallel upload; stop service first")
	}
	root := os.Getenv("TBOX_AUTH_PATH")
	if smh.ValidatePath(root) != nil || !strings.HasPrefix(root, "codex-api-lab/") {
		t.Fatal("isolated lab root required")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil || json.Unmarshal(b, &ids) != nil {
		t.Fatal("private identity unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fsys, err := NewFs(ctx, "parallel-live", root, configmap.Simple{"library_id": ids.Library, "space_id": ids.Space, "user_token_file": os.Getenv("TBOX_USER_TOKEN_FILE"), "state_dir": os.Getenv("TBOX_STATE_DIR"), "ownership_dir": os.Getenv("TBOX_OWNERSHIP_DIR"), "lab_writes": "true"})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	defer direct.CloseIdleConnections()
	transport := &parallelTransport{base: direct, seen: map[string]bool{}, active: map[string]int{}, gate: make(chan struct{})}
	f.c.HTTP.Transport = transport
	independent, err := smh.New(f.c.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	independent.HTTP.Transport = direct
	if err = independent.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 6)
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	prefix := "parallel-" + hex.EncodeToString(nonce)
	newParent := os.Getenv("TBOX_PARALLEL_NEW_PARENT") == "1"
	if !newParent {
		if err = f.Mkdir(ctx, prefix); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		Path    string  `json:"path"`
		Size    int     `json:"size"`
		SHA256  string  `json:"sha256"`
		Seconds float64 `json:"seconds"`
	}
	payloads := [][]byte{bytes.Repeat([]byte("parallel-A-"), 900000), bytes.Repeat([]byte("parallel-B-"), 900000)}
	results := make([]result, 2)
	cancelOne := os.Getenv("TBOX_PARALLEL_CANCEL_ONE") == "1"
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	if cancelOne {
		transport.cancelFirst = cancelFirst
	}
	type outcome struct {
		index int
		err   error
	}
	errs := make(chan outcome, 2)
	start := time.Now()
	for i := range 2 {
		go func(i int) {
			name := prefix + "/" + []string{"one.bin", "two.bin"}[i]
			before := time.Now()
			payload := payloads[i]
			src := object.NewStaticObjectInfo(name, before, int64(len(payload)), true, nil, f)
			uploadCtx := ctx
			if i == 0 {
				uploadCtx = firstCtx
			}
			uploadCtx = context.WithValue(uploadCtx, parallelFileKey{}, i)
			_, err := f.Put(uploadCtx, bytes.NewReader(payload), src)
			sum := sha256.Sum256(payload)
			results[i] = result{Path: root + "/" + name, Size: len(payload), SHA256: hex.EncodeToString(sum[:]), Seconds: time.Since(before).Seconds()}
			errs <- outcome{i, err}
		}(i)
	}
	var uploadErr error
	for range 2 {
		result := <-errs
		if cancelOne && result.index == 0 {
			if !errors.Is(result.err, context.Canceled) {
				uploadErr = errors.Join(uploadErr, errors.New("first upload did not cancel as expected"), result.err)
			}
		} else {
			uploadErr = errors.Join(uploadErr, result.err)
		}
	}
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	elapsed := time.Since(start).Seconds()
	verify := func(want result) {
		item, err := independent.Info(ctx, want.Path)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := independent.Open(ctx, want.Path, item, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		n, err := io.Copy(h, reader)
		reader.Close()
		if err != nil || n != int64(want.Size) || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
			t.Fatal("independent content mismatch", want.Path)
		}
	}

	if cancelOne {
		verify(results[1])
	}
	transport.mu.Lock()
	maximum, sessions, cancelledAcks := transport.maximum, len(transport.seen), transport.cancelledAcks
	transport.mu.Unlock()
	if sessions != 2 || maximum < 2 {
		t.Fatal("no distinct session overlap", sessions, maximum)
	}
	if cancelOne {
		if cancelledAcks != 1 {
			t.Fatal("no completed-origin part was cancelled")
		}
		if _, err := independent.Info(ctx, results[0].Path); !smh.IsStatus(err, 404) {
			t.Fatalf("cancelled upload published unexpectedly: %v", err)
		}
		src := object.NewStaticObjectInfo(prefix+"/one.bin", time.Now(), 1, true, nil, f)
		if _, err := f.Put(ctx, strings.NewReader("x"), src); !errors.Is(err, journal.ErrPending) {
			t.Fatalf("cancelled path not reserved: %v", err)
		}
	}
	store, err := journal.Open(f.opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	records, err := store.Records()
	if err != nil {
		t.Fatal(err)
	}
	committed := 0
	statesBeforeRecovery := map[string]string{}
	for _, r := range records {
		if strings.HasPrefix(r.Path, root+"/"+prefix+"/") {
			statesBeforeRecovery[r.Path] = r.State
			if cancelOne && r.Path == results[0].Path {
				if r.State != "Uploading" {
					t.Fatal("wrong cancelled state", r.State)
				}
				status, err := independent.UploadState(ctx, r.ConfirmKey)
				if err != nil {
					t.Fatal(err)
				}
				transport.mu.Lock()
				cancelledPart := transport.cancelledPart
				transport.mu.Unlock()
				found := false
				for _, part := range status.Parts {
					if part.Number == cancelledPart && part.ETag != "" && cancelledPart > 0 && cancelledPart <= len(r.Parts) && int64(part.Size) == r.Parts[cancelledPart-1].Size {
						found = true
					}
				}
				if !found || status.UploadID != r.UploadID || status.Confirmed {
					t.Fatal("cancelled response has no matching independently verified remote part")
				}
				oldKey, oldID := r.ConfirmKey, r.UploadID
				if err := transfer.Resume(ctx, store, f.c, &r); err != nil {
					t.Fatal(err)
				}
				if r.ConfirmKey != oldKey || r.UploadID != oldID {
					t.Fatal("resume replaced session")
				}
			}
			if r.State != "Committed" {
				t.Fatal(r.State)
			}
			data, err := store.Data(&r)
			if err != nil {
				t.Fatal(err)
			}
			data.Close()
			committed++
		}
	}
	if committed != 2 {
		t.Fatal("wrong journal count", committed)
	}
	for _, want := range results {
		verify(want)
	}
	transport.mu.Lock()
	initializations, confirmations := transport.initializations, transport.confirmations
	transport.mu.Unlock()
	if initializations != 2 || confirmations != 2 {
		t.Fatal("reinitialized or replayed confirm", initializations, confirmations)
	}
	report := map[string]any{"cancel_one": cancelOne, "cancelled_origin_success_responses": cancelledAcks, "states_before_recovery": statesBeforeRecovery, "initializations": initializations, "confirmations": confirmations, "status": "PASS", "scope": "real backend with synchronized data-request entry; not Finder acceptance or an uninstrumented throughput benchmark", "fixture": prefix, "parent_initially_missing": newParent, "files": results, "elapsed_seconds": elapsed, "distinct_sessions": sessions, "max_overlapping_sessions": maximum, "committed_records": committed}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("TBOX_PARALLEL_REPORT"), append(output, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
