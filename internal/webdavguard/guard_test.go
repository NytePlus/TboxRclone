package webdavguard

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConflictsFailBeforeUpstream(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Hold") == "yes" {
			close(entered)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	guard, err := New("", next)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(guard)
	defer server.Close()

	first, err := http.NewRequest(http.MethodPut, server.URL+"/dir/file", strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	first.Header.Set("Hold", "yes")
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(first)
		if err == nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach upstream")
	}

	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, server.URL+"/dir/file", ""),
		mustRequest(t, http.MethodPut, server.URL+"/dir/file", "second"),
		mustRequest(t, http.MethodDelete, server.URL+"/dir", ""),
	} {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusLocked {
			t.Fatalf("%s returned %d", request.Method, response.StatusCode)
		}
	}
	response, err := http.Get(server.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.Status)
	}
	if calls.Load() != 2 {
		t.Fatalf("conflicts reached upstream: %d calls", calls.Load())
	}

	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(server.URL + "/dir/file")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.Status)
	}
}

func TestDestinationUsesPublicBaseNamespace(t *testing.T) {
	guard, err := New("/dav", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, source, destination string
		want                      int
	}{
		{"sibling", "/dav/source", "http://example.test/dav/target", http.StatusCreated},
		{"same", "/dav/source", "http://example.test/dav/source", http.StatusForbidden},
		{"descendant", "/dav/dir", "http://example.test/dav/dir/child", http.StatusForbidden},
		{"encoded descendant", "/dav/dir", "http://example.test/dav/%64ir/child", http.StatusForbidden},
		{"ancestor", "/dav/dir/child", "http://example.test/dav/dir", http.StatusForbidden},
		{"relative destination", "/dav/source", "/dav/target", http.StatusCreated},
		{"outside base", "/dav/source", "http://example.test/other", http.StatusBadRequest},
		{"source outside base", "/other", "http://example.test/dav/target", http.StatusBadRequest},
		{"other host", "/dav/source", "http://other.test/dav/target", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("COPY", "http://example.test"+test.source, nil)
			request.Header.Set("Destination", test.destination)
			response := httptest.NewRecorder()
			guard.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("got %d want %d", response.Code, test.want)
			}
		})
	}
}

func TestProxyPreservesPublicHostAndDestination(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "public.example" {
			t.Errorf("Host changed to %q", r.Host)
		}
		if got := r.Header.Get("Destination"); got != "http://public.example/dav/target" {
			t.Errorf("Destination changed to %q", got)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	origin, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewProxy("/dav", origin)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("COPY", "http://public.example/dav/source", nil)
	request.Header.Set("Destination", "http://public.example/dav/target")
	response := httptest.NewRecorder()
	guard.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code)
	}
}

func TestZeroPutBarrierDoesNotPublishOnUnlockCount(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method == http.MethodPut {
			body, err := io.ReadAll(r.Body)
			if err != nil || len(body) != 0 {
				t.Fatalf("synthetic empty PUT body: %q %v", body, err)
			}
		}
		w.WriteHeader(http.StatusCreated)
	})
	guard, err := New("", next)
	if err != nil {
		t.Fatal(err)
	}
	guard.stageZero = true
	guard.stateDir = t.TempDir()
	guard.pending = make(map[string]*zeroPending)
	server := httptest.NewServer(guard)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPut, server.URL+"/empty", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatal(response.Status)
	}
	mu.Lock()
	if len(methods) != 0 {
		t.Fatalf("placeholder reached upstream: %v", methods)
	}
	mu.Unlock()

	for i := 0; i < 2; i++ {
		request, _ := http.NewRequest("LOCK", server.URL+"/empty", strings.NewReader(`<lockinfo xmlns="DAV:"><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`))
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		token := response.Header.Get("Lock-Token")
		response.Body.Close()
		request, _ = http.NewRequest("UNLOCK", server.URL+"/empty", nil)
		request.Header.Set("Lock-Token", token)
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode >= 400 {
			t.Fatal(response.Status)
		}
		if i == 0 {
			mu.Lock()
			for _, method := range methods {
				if strings.HasPrefix(method, "PUT ") {
					t.Fatalf("published after first unlock: %v", methods)
				}
			}
			mu.Unlock()
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 0 || !guard.pendingPath("/empty") { // unlocking alone never publishes a placeholder
		t.Fatalf("unexpected upstream sequence: %v", methods)
	}
}

func TestZeroPutBarrierContentPutClearsDurableState(t *testing.T) {
	var puts int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
			if r.Header.Get("If") != "" {
				t.Fatal("synthetic lock predicate leaked to upstream")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "content" {
				t.Fatalf("wrong content: %q", body)
			}
		}
		w.WriteHeader(http.StatusCreated)
	})
	guard, err := New("", next)
	if err != nil {
		t.Fatal(err)
	}
	guard.stageZero = true
	guard.stateDir = t.TempDir()
	guard.pending = make(map[string]*zeroPending)
	server := httptest.NewServer(guard)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/file", strings.NewReader(""))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	request, _ = http.NewRequest(http.MethodPut, server.URL+"/file", strings.NewReader("content"))

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode >= 400 || puts != 1 || guard.pendingPath("/file") {
		t.Fatalf("content barrier not committed: status=%d puts=%d pending=%v", response.StatusCode, puts, guard.pendingPath("/file"))
	}
}

func TestZeroPutBarrierRejectsDuplicateCreation(t *testing.T) {
	guard, err := New("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	if err != nil {
		t.Fatal(err)
	}
	guard.stageZero = true
	guard.stateDir = t.TempDir()
	guard.pending = make(map[string]*zeroPending)
	server := httptest.NewServer(guard)
	defer server.Close()
	for i, want := range []int{http.StatusCreated, http.StatusLocked} {
		request, _ := http.NewRequest(http.MethodPut, server.URL+"/duplicate", strings.NewReader(""))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("attempt %d: got %d want %d", i, response.StatusCode, want)
		}
	}
}

func TestZeroPutBarrierReloadsPendingStateAfterRestart(t *testing.T) {
	var upstreamPuts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			upstreamPuts.Add(1)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	origin, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()

	first, err := NewStagingProxy("", origin, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	server1 := httptest.NewServer(first)
	request, _ := http.NewRequest(http.MethodPut, server1.URL+"/survives-restart", strings.NewReader(""))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	server1.Close()
	if upstreamPuts.Load() != 0 {
		t.Fatal("placeholder was published before restart")
	}

	second, err := NewStagingProxy("", origin, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	server2 := httptest.NewServer(second)
	defer server2.Close()
	request, _ = http.NewRequest(http.MethodGet, server2.URL+"/survives-restart", nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reloaded pending path returned %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPut, server2.URL+"/survives-restart", strings.NewReader(""))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusLocked {
		t.Fatalf("duplicate after restart returned %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPut, server2.URL+"/survives-restart", strings.NewReader("live"))

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode >= 300 || upstreamPuts.Load() != 1 {
		t.Fatalf("reloaded barrier did not publish content: status=%d puts=%d", response.StatusCode, upstreamPuts.Load())
	}
}

func TestZeroPutBarrierKeepsReceiptWhenClearFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	origin, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	guard, err := NewStagingProxy("", origin, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(guard)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/receipt", strings.NewReader(""))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	stateFile := guard.stateFile("/receipt")
	if err := os.Remove(stateFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stateFile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateFile, "keep"), []byte("receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateFile)
	request, _ = http.NewRequest(http.MethodPut, server.URL+"/receipt", strings.NewReader("published"))

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusInsufficientStorage || !guard.pendingPath("/receipt") {
		t.Fatalf("clear failure was not retained: status=%d pending=%v", response.StatusCode, guard.pendingPath("/receipt"))
	}
}

func TestReadersShareAndMoveClaimsBothTrees(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Hold") == "yes" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	guard, err := New("", next)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(guard)
	defer server.Close()

	for range 2 {
		request := mustRequest(t, http.MethodGet, server.URL+"/same", "")
		request.Header.Set("Hold", "yes")
		go func() {
			response, _ := http.DefaultClient.Do(request)
			if response != nil {
				response.Body.Close()
			}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("reader was serialized")
		}
	}
	write := mustRequest(t, http.MethodPut, server.URL+"/same", "x")
	response, err := http.DefaultClient.Do(write)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusLocked {
		t.Fatal(response.Status)
	}
	close(release)

	move := httptest.NewRequest("MOVE", "http://example.test/source", nil)
	move.Header.Set("Destination", "http://example.test/target")
	claims, status := guard.claims(move)
	if status != 0 || len(claims) != 2 || !claims[0].write || !claims[1].write {
		t.Fatal(claims, status)
	}
}

func TestDifferentFileWritesMayRunInParallel(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.URL.Path
		<-release
		w.WriteHeader(http.StatusCreated)
	})
	guard, err := New("", next)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(guard)
	defer server.Close()

	results := make(chan error, 2)
	for _, name := range []string{"/one", "/two"} {
		name := name
		go func() {
			request, _ := http.NewRequest(http.MethodPut, server.URL+name, strings.NewReader("data"))
			response, requestErr := http.DefaultClient.Do(request)
			if response != nil {
				response.Body.Close()
				if response.StatusCode != http.StatusCreated {
					requestErr = fmt.Errorf("%s returned %d", name, response.StatusCode)
				}
			}
			results <- requestErr
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-entered:
			seen[name] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("different file writes were serialized: entered=%v", seen)
		}
	}
	if !seen["/one"] || !seen["/two"] {
		t.Fatalf("unexpected paths reached upstream: %v", seen)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func mustRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	return request
}
