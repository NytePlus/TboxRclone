package sjtu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
)

func TestRmdirOwnsSubtreeAndRetainsUnknownReservations(t *testing.T) {
	for _, mode := range []string{"empty", "nonempty", "lost_response", "unknown", "pending_child"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			f.opt.LabDelete = true
			entered, proceed := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(proceed) }) }
			defer unblock()
			deleted := false
			deletes := 0
			puts := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
					if r.URL.Query().Get("directory_only") != "1" || r.URL.Query().Get("permanent") != "0" || !strings.Contains(r.URL.Path, "/directory/") {
						t.Error("unsafe directory delete")
					}
					records, err := (&journal.Store{Dir: f.opt.StateDir}).Records()
					if err != nil || len(records) != 1 || records[0].Kind != "rmdir" || records[0].State != "DeleteSent" {
						t.Error("missing durable rmdir intent")
					}
					if mode == "unknown" {
						w.WriteHeader(503)
						return
					}
					deleted = true
					if mode == "lost_response" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					io.WriteString(w, `{"recycledItemId":123}`)
					return
				}
				if r.Method == "PUT" {
					puts++
					w.WriteHeader(201)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/dir") && r.URL.Query().Get("info") == "" {
					close(entered)
					<-proceed
					if mode == "nonempty" {
						io.WriteString(w, `{"contents":[{"name":"child","type":"file","size":3,"eTag":"old"}]}`)
					} else {
						io.WriteString(w, `{"contents":[]}`)
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, "/child") || deleted {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			if mode == "pending_child" {
				s, err := journal.Open(f.opt.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Prepare(context.Background(), f.c.Endpoint+"/l/s", f.root+"/dir/child", strings.NewReader("safe"), 4, 4)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Rmdir(context.Background(), "dir"); !errors.Is(err, journal.ErrPending) {
					t.Fatal("pending child ignored", err)
				}
				if deletes != 0 {
					t.Fatal("pending directory deleted")
				}
				return
			}
			done := make(chan error, 1)
			go func() { done <- f.Rmdir(context.Background(), "dir") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("list did not start")
			}
			for _, dir := range []string{"dir", "dir/child"} {
				if err := f.Mkdir(context.Background(), dir); !errors.Is(err, ErrPathBusy) {
					t.Fatalf("concurrent mkdir %s: %v", dir, err)
				}
			}
			src := object.NewStaticObjectInfo("dir/child", time.Now(), 4, true, nil, f)
			if _, err := f.Put(context.Background(), strings.NewReader("safe"), src); !errors.Is(err, ErrPathBusy) {
				t.Fatal("child upload not rejected", err)
			}
			unblock()
			err := <-done
			if mode == "nonempty" {
				if !errors.Is(err, fs.ErrorDirectoryNotEmpty) || deletes != 0 {
					t.Fatal("nonempty directory deleted", err, deletes)
				}
			} else if mode == "unknown" {
				if err == nil {
					t.Fatal("unknown delete accepted")
				}
				if err = f.Mkdir(context.Background(), "dir/child"); !errors.Is(err, journal.ErrPending) {
					t.Fatal("durable subtree reservation bypassed", err)
				}
				if _, err = f.Put(context.Background(), strings.NewReader("safe"), src); !errors.Is(err, journal.ErrPending) {
					t.Fatal("child upload bypassed pending delete", err)
				}
			} else if err != nil || !deleted || deletes != 1 {
				t.Fatal("empty delete failed", err, deletes)
			}
			if puts != 0 {
				t.Fatal("concurrent directory creation escaped", puts)
			}
		})
	}
}
