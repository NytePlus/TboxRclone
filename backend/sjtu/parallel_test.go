package sjtu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/rclone/rclone/fs/object"
)

func TestDistinctUploadsReachDataPlaneTogether(t *testing.T) {
	for _, newParent := range []bool{false, true} {
		name := "existing_parent"
		if newParent {
			name = "new_parent"
		}
		t.Run(name, func(t *testing.T) { testDistinctUploads(t, newParent) })
	}
}

func testDistinctUploads(t *testing.T, newParent bool) {
	f, _ := newSimulator(t, false)
	if newParent {
		f.root += "/new-parent"
	}
	parentExists := !newParent
	parentCreates := 0
	type state struct {
		data      []byte
		published bool
	}
	var mu sync.Mutex
	files := map[string]*state{"one": {}, "two": {}}
	reached := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.HasPrefix(r.URL.Path, "/data/") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			reached <- name
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			mu.Lock()
			files[name].data = body
			mu.Unlock()
			w.Header().Set("ETag", `"part"`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		name = strings.TrimPrefix(name, "K-")
		item, exists := files[name]
		if strings.Contains(r.URL.Path, "/directory/") {
			if name == "new-parent" {
				if r.Method == "PUT" {
					parentExists = true
					parentCreates++
					w.WriteHeader(201)
					return
				}
				if !parentExists {
					w.WriteHeader(404)
					return
				}
			}
			if !exists {
				json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
				return
			}
			if !item.published {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(smh.Item{Type: "file", Size: smh.Int64(len(item.data)), ETag: "v1"})
			return
		}
		if !exists {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" && r.URL.Query().Get("upload") == "1" {
			json.NewEncoder(w).Encode(smh.UploadStatus{Confirmed: item.published, UploadID: name, Path: strings.Split(f.root+"/"+name, "/")})
			return
		}
		if r.Method == "POST" && r.URL.Query().Has("multipart") {
			json.NewEncoder(w).Encode(smh.Upload{Key: "K-" + name, UploadID: name, Domain: server.URL, Path: "/data/" + name, Parts: map[string]smh.PartSignature{"1": {Headers: map[string]string{"x-fixture": "part"}}}})
			return
		}
		if r.Method == "POST" {
			item.published = true
			json.NewEncoder(w).Encode(smh.Item{Type: "file", Path: strings.Split(f.root+"/"+name, "/"), Size: smh.Int64(len(item.data)), ETag: "v1"})
			return
		}
		if r.Method == "GET" {
			w.Header().Set("ETag", `"v1"`)
			w.Write(item.data)
			return
		}
		w.WriteHeader(405)
	}))
	defer server.Close()
	// Unblock before server shutdown on any assertion failure.
	defer unblock()
	f.c.Endpoint = server.URL
	f.c.HTTP = server.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			data := "distinct payload " + name
			src := object.NewStaticObjectInfo(name, time.Now(), int64(len(data)), true, nil, f)
			_, err := f.Put(ctx, strings.NewReader(data), src)
			done <- err
		}(name)
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case name := <-reached:
			seen[name] = true
		case err := <-done:
			t.Fatalf("upload ended before overlap: %v", err)
		case <-ctx.Done():
			unblock()
			<-done
			<-done
			t.Fatal("different files serialized before data plane")
		}
	}
	// Same-file access remains fail-fast while both independent uploads run.
	src := object.NewStaticObjectInfo("one", time.Now(), 1, true, nil, f)
	if _, err := f.Put(ctx, strings.NewReader("x"), src); err == nil {
		t.Fatal("conflicting upload accepted")
	}
	unblock()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if newParent && parentCreates != 1 {
		t.Fatalf("parent creates: %d", parentCreates)
	}
	for name, item := range files {
		if !item.published || string(item.data) != "distinct payload "+name {
			t.Fatal("wrong independent content", name)
		}
	}
	s, err := journal.Open(f.opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, err := s.Records()
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	for _, r := range records {
		if r.State != "Committed" {
			t.Fatal(r.State)
		}
		data, err := s.Data(&r)
		if err != nil {
			t.Fatal(err)
		}
		data.Close()
	}
}

func TestMutationCapacityAndCancellation(t *testing.T) {
	f, _ := newSimulator(t, false)
	releases := make([]func(), 0, maxActiveMutations)
	for range maxActiveMutations {
		release, err := f.acquireMutation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
		defer release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := f.acquireMutation(ctx); err == nil {
		release()
		t.Fatal("capacity exceeded")
	}
	releases[0]()
	releases[0]()
	release, err := f.acquireMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
