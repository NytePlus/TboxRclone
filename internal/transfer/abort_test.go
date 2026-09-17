package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

type abortFixture struct {
	deleted, confirmed, drop, race, otherPath, otherIdentity, targetExists bool
	eraseConfirmedSession                                                  bool
	calls                                                                  int
}

func setupAbort(t *testing.T) (*abortFixture, *smh.Client, *journal.Store, *journal.Record) {
	t.Helper()
	f := &abortFixture{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if req.Method == "DELETE" {
			if req.URL.Path != "/api/v1/file/l/s/K" || q.Get("upload") != "1" {
				t.Error("attempted formal file deletion")
				w.WriteHeader(400)
				return
			}
			f.calls++
			if f.eraseConfirmedSession {
				f.confirmed = true
				f.deleted = true
				w.WriteHeader(204)
				return
			}
			if f.race {
				f.confirmed = true
				w.WriteHeader(409)
				return
			}
			f.deleted = true
			if f.drop {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(204)
			return
		}
		if q.Get("upload") == "1" {
			if f.deleted {
				w.WriteHeader(404)
				return
			}
			status := smh.UploadStatus{Path: []string{"codex-api-lab", "run", "file"}, UploadID: "upload", Confirmed: f.confirmed}
			if f.otherPath {
				status.Path = []string{"elsewhere"}
			}
			if f.otherIdentity {
				status.UploadID = "different"
			}
			if f.confirmed {
				status.UploadID = ""
			}
			json.NewEncoder(w).Encode(status)
			return
		}
		if strings.Contains(req.URL.Path, "/directory/") {
			if !f.confirmed && !f.targetExists {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(smh.Item{Type: "file", Size: 4, ETag: "v1"})
			return
		}
		if req.Method == "GET" && f.confirmed {
			w.Header().Set("ETag", `"v1"`)
			io.WriteString(w, "safe")
			return
		}
		t.Error("unexpected request")
		w.WriteHeader(500)
	}))
	t.Cleanup(server.Close)
	c := &smh.Client{Endpoint: server.URL, Library: "l", Space: "s", HTTP: server.Client(), Token: func(context.Context) (string, error) { return "secret", nil }}
	s, e := journal.Open(filepath.Join(t.TempDir(), "state"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	r, e := s.Prepare(context.Background(), c.Endpoint+"/l/s", "codex-api-lab/run/file", strings.NewReader("safe"), 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	r.State = "Uploading"
	r.ConfirmKey = "K"
	r.UploadID = "upload"
	if e = s.Save(r); e != nil {
		t.Fatal(e)
	}
	return f, c, s, r
}
func TestAbortSessionIsIdempotentAndRetainsSpool(t *testing.T) {
	f, c, s, r := setupAbort(t)
	for range 2 {
		if e := Abort(context.Background(), s, c, r); e != nil {
			t.Fatal(e)
		}
	}
	if r.State != "Aborted" || f.calls != 1 {
		t.Fatal(r.State, f.calls)
	}
	data, e := s.Data(r)
	if e != nil {
		t.Fatal(e)
	}
	defer data.Close()
	b, _ := io.ReadAll(data)
	if string(b) != "safe" {
		t.Fatal("lost spool")
	}
}
func TestAbortLostResponseReconcilesWithoutDeleteReplay(t *testing.T) {
	f, c, s, r := setupAbort(t)
	f.drop = true
	if e := Abort(context.Background(), s, c, r); e == nil || r.State != "AbortUnknown" {
		t.Fatal(e, r.State)
	}
	records, e := s.Records()
	if e != nil {
		t.Fatal(e)
	}
	r = &records[0]
	if e = Abort(context.Background(), s, c, r); e != nil {
		t.Fatal(e)
	}
	if f.calls != 1 || r.State != "Aborted" {
		t.Fatal(f.calls, r.State)
	}
}
func TestAbortConfirmRaceNeverDeletesFormalFile(t *testing.T) {
	f, c, s, r := setupAbort(t)
	f.race = true
	if e := Abort(context.Background(), s, c, r); e == nil || r.State != "AbortUnknown" {
		t.Fatal(e, r.State)
	}
	if e := Abort(context.Background(), s, c, r); !errors.Is(e, ErrAlreadyCommitted) {
		t.Fatal(e)
	}
	if r.State != "Committed" || f.calls != 1 || f.deleted {
		t.Fatal("unsafe cancellation", r.State, f.calls)
	}
}
func TestAbortRefusesIdentityAndStateMismatch(t *testing.T) {
	for _, mode := range []string{"scope", "path", "identity", "unknown init", "submitted confirm", "committed"} {
		t.Run(mode, func(t *testing.T) {
			f, c, s, r := setupAbort(t)
			switch mode {
			case "scope":
				r.Scope = "other"
			case "path":
				f.otherPath = true
			case "identity":
				f.otherIdentity = true
			case "unknown init":
				r.State = "InitSent"
			case "submitted confirm":
				r.State = "CommitSent"
			case "committed":
				r.State = "Committed"
			}
			if e := Abort(context.Background(), s, c, r); e == nil {
				t.Fatal("unsafe cancellation accepted")
			}
			if f.calls != 0 {
				t.Fatal("sent destructive request")
			}
		})
	}
}
func TestAbortMissingSessionWithTargetRemainsUnknown(t *testing.T) {
	f, c, s, r := setupAbort(t)
	f.targetExists = true
	if e := Abort(context.Background(), s, c, r); e == nil || r.State != "AbortUnknown" {
		t.Fatal(e, r.State)
	}
	if e := Abort(context.Background(), s, c, r); e == nil || f.calls != 1 {
		t.Fatal("retried deletion or claimed cancellation", e, f.calls)
	}
}
func TestAbortPreparedIsLocalOnly(t *testing.T) {
	f, c, s, r := setupAbort(t)
	r.State = "Prepared"
	r.ConfirmKey = ""
	r.UploadID = ""
	if e := Abort(context.Background(), s, c, r); e != nil {
		t.Fatal(e)
	}
	if r.State != "Aborted" || f.calls != 0 {
		t.Fatal(r.State, f.calls)
	}
}

func TestAbortErasedSessionWithPublishedFileStaysUnknown(t *testing.T) {
	f, c, s, r := setupAbort(t)
	f.eraseConfirmedSession = true
	if e := Abort(context.Background(), s, c, r); e == nil || r.State != "AbortUnknown" {
		t.Fatal(e, r.State)
	}
	if e := Abort(context.Background(), s, c, r); e == nil || r.State != "AbortUnknown" || f.calls != 1 {
		t.Fatal("guessed abort success or replayed deletion", e, r.State, f.calls)
	}
	data, e := s.Data(r)
	if e != nil {
		t.Fatal(e)
	}
	data.Close()
}

func TestOverwriteAbortRequiresPreviousVersion(t *testing.T) {
	for _, mode := range []string{"old_unchanged", "changed", "missing", "lost_response"} {
		t.Run(mode, func(t *testing.T) {
			f, c, s, r := setupAbort(t)
			r.Overwrite = true
			r.OldETag = "v1"
			r.OldSize = 4
			f.targetExists = mode != "missing"
			if mode == "changed" {
				r.OldETag = "previous"
			}
			if mode == "lost_response" {
				f.drop = true
			}
			if err := s.Save(r); err != nil {
				t.Fatal(err)
			}
			err := Abort(context.Background(), s, c, r)
			if mode == "lost_response" {
				if err == nil || r.State != "AbortUnknown" {
					t.Fatal(err, r.State)
				}
				records, e := s.Records()
				if e != nil {
					t.Fatal(e)
				}
				r = &records[0]
				err = Abort(context.Background(), s, c, r)
			}
			if mode == "old_unchanged" || mode == "lost_response" {
				if err != nil || r.State != "Aborted" {
					t.Fatal(err, r.State)
				}
			} else if err == nil || r.State != "AbortUnknown" {
				t.Fatal("changed/missing previous file accepted", err, r.State)
			}
			if f.calls != 1 {
				t.Fatal("abort replayed", f.calls)
			}
			data, e := s.Data(r)
			if e != nil {
				t.Fatal(e)
			}
			data.Close()
		})
	}
}
