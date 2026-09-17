package sjtu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/smh"
)

func TestConcurrentEnsureDirectory(t *testing.T) {
	for _, mode := range []string{"success", "join_cancel", "response_loss"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var created atomic.Bool
			var mutations atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/new-parent") {
					json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
					return
				}
				if r.Method == "PUT" {
					if mutations.Add(1) == 1 {
						close(entered)
					}
					<-release
					created.Store(true)
					if mode == "response_loss" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					w.WriteHeader(201)
					return
				}
				if !created.Load() {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
			}))
			defer server.Close()
			defer unblock()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			first := make(chan error, 1)
			go func() { first <- f.Mkdir(ctx, "new-parent") }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first create not reached")
			}
			// The active initializer may be joined, but deleting its subtree must fail.
			f.opt.LabDelete = true
			if err := f.Rmdir(ctx, "new-parent"); !errors.Is(err, ErrPathBusy) {
				t.Fatalf("delete did not conflict: %v", err)
			}
			joinCtx, joinCancel := context.WithCancel(ctx)
			defer joinCancel()
			second := make(chan error, 1)
			go func() { second <- f.Mkdir(joinCtx, "new-parent") }()
			if mode == "join_cancel" {
				// A waiting sibling may cancel without cancelling the original mkdir.
				select {
				case err := <-second:
					t.Fatalf("sibling rejected before cancellation: %v", err)
				case <-time.After(40 * time.Millisecond):
				}
				joinCancel()
				if err := <-second; !errors.Is(err, context.Canceled) {
					t.Fatalf("wrong cancellation: %v", err)
				}
			} else {
				select {
				case err := <-second:
					t.Fatalf("sibling rejected instead of sharing create: %v", err)
				case <-time.After(40 * time.Millisecond):
				}
			}
			unblock()
			firstErr := <-first
			if mode == "response_loss" {
				if !errors.Is(firstErr, smh.ErrUnknown) {
					t.Fatalf("missing ambiguous mutation result: %v", firstErr)
				}
			} else if firstErr != nil {
				t.Fatal(firstErr)
			}
			if mode != "join_cancel" {
				err := <-second
				if (err != nil) != (mode == "response_loss") {
					t.Fatalf("different shared result: %v", err)
				}
			}
			if mutations.Load() != 1 {
				t.Fatalf("create replayed: %d", mutations.Load())
			}
		})
	}
}
