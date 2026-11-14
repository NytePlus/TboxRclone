package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/rclone/rclone/fs"
)

func TestExistingDirectoryLookupBounded(t *testing.T) {
	for _, mode := range []string{"directory", "file", "forbidden", "pending_ancestor", "active_ancestor"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/a/b/c") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				switch mode {
				case "forbidden":
					w.WriteHeader(403)
				case "file":
					io.WriteString(w, `{"type":"file"}`)
				default:
					io.WriteString(w, `{"type":"dir"}`)
				}
			}))
			defer server.Close()
			f.Client.Endpoint, f.Client.HTTP = server.URL, server.Client()
			if mode == "pending_ancestor" {
				s, err := journal.Open(f.Opt.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Prepare(context.Background(), server.URL+"/l/s", f.BackendRoot+"/a", strings.NewReader("pending"), 7, 20)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "active_ancestor" {
				release, err := f.AcquireFile("codex-api-lab", true)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			err := f.Mkdir(context.Background(), "a/b/c")
			switch mode {
			case "directory":
				if err != nil {
					t.Fatal(err)
				}
			case "file":
				if !errors.Is(err, fs.ErrorIsFile) {
					t.Fatal(err)
				}
			case "pending_ancestor":
				if !errors.Is(err, journal.ErrPending) {
					t.Fatal(err)
				}
			default:
				if err == nil {
					t.Fatal("unsafe lookup accepted")
				}
			}
			expected := 1
			if mode == "pending_ancestor" || mode == "active_ancestor" {
				expected = 0
			}
			if calls != expected {
				t.Fatalf("want %d cloud requests, got %d", expected, calls)
			}
		})
	}
}
