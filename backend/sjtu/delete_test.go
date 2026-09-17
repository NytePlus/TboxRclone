package sjtu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/transfer"
	"github.com/rclone/rclone/fs/fserrors"
)

func TestDurableDeleteNeverReplays(t *testing.T) {
	for _, mode := range []string{"ack", "ack_numeric", "drop_response", "recreated", "denied", "read_failure", "stale_object", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			f.opt.LabDelete = mode != "disabled"
			exists := true
			deletes := 0
			etag := "old"
			failRead := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
					if r.URL.Query().Get("permanent") != "0" || r.URL.Query().Has("upload") {
						t.Error("not a trash file deletion")
					}
					// Verify the crash boundary at the instant the mutation reaches server.
					var record journal.Record
					records, err := (&journal.Store{Dir: f.opt.StateDir}).Records()
					if err != nil || len(records) != 1 {
						t.Error("missing durable intent", err)
					} else {
						record = records[0]
					}
					if record.Kind != "delete" || record.State != "DeleteSent" || record.OldETag != "old" {
						t.Error("invalid durable intent", record.State)
					}
					if mode == "denied" {
						w.WriteHeader(403)
						return
					}
					exists = false
					if mode == "read_failure" {
						failRead = true
					}
					if mode == "recreated" {
						exists = true
						etag = "new"
					}
					if mode == "drop_response" || mode == "recreated" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					if mode == "ack_numeric" {
						json.NewEncoder(w).Encode(map[string]int64{"recycledItemId": 9007199254740993})
					} else {
						json.NewEncoder(w).Encode(map[string]string{"recycledItemId": "fixture-trash-id"})
					}
					return
				}
				if r.Method != "GET" || !strings.Contains(r.URL.Path, "/directory/") {
					t.Error("unexpected request", r.Method)
					w.WriteHeader(500)
					return
				}
				if failRead {
					w.WriteHeader(503)
					return
				}
				if !exists {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: "file", Size: 3, ETag: etag})
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			obj, err := f.NewObject(context.Background(), "file")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "stale_object" {
				etag = "new"
			}
			err = obj.Remove(context.Background())
			if mode == "ack" || mode == "ack_numeric" || mode == "drop_response" {
				if err != nil || exists || deletes != 1 {
					t.Fatalf("delete result: exists=%t calls=%d err=%v", exists, deletes, err)
				}
			} else if err == nil || !fserrors.IsNoRetryError(err) {
				t.Fatal("failure not surfaced", err)
			}
			if mode == "disabled" || mode == "stale_object" {
				if deletes != 0 {
					t.Fatal("forbidden deletion sent")
				}
				return
			}
			s, err := journal.Open(f.opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			records, err := s.Records()
			if err != nil || len(records) != 1 {
				t.Fatal(records, err)
			}
			record := records[0]
			if record.Kind != "delete" {
				t.Fatal("kind lost")
			}
			if mode == "ack_numeric" && record.RecycledID != "9007199254740993" {
				t.Fatal("numeric recycle ID lost precision", record.RecycledID)
			}
			if mode == "ack" && record.RecycledID != "fixture-trash-id" {
				t.Fatal("trash receipt lost")
			}
			if mode == "ack" || mode == "ack_numeric" || mode == "drop_response" {
				if record.State != "Committed" {
					t.Fatal(record.State)
				}
			} else {
				if record.State != "DeleteUnknown" {
					t.Fatal(record.State)
				}
				if err = s.Pending(record.Scope, record.Path); !errors.Is(err, journal.ErrPending) {
					t.Fatal("reservation lost", err)
				}
				if err = recovery.Reconcile(context.Background(), s, f.c, &record); err == nil {
					t.Fatal("unresolved deletion accepted")
				}
				if mode == "read_failure" {
					failRead = false
					if err = recovery.Reconcile(context.Background(), s, f.c, &record); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = transfer.Resume(context.Background(), s, f.c, &record); err == nil {
				t.Fatal("delete interpreted as upload")
			}
			if err = transfer.Abort(context.Background(), s, f.c, &record); err == nil {
				t.Fatal("delete interpreted as upload cancellation")
			}
			if deletes != 1 {
				t.Fatal("delete replayed", deletes)
			}
			// A terminal deletion can be queried again without touching a later file.
			if record.State == "Committed" {
				exists = true
				etag = "new"
				if err = recovery.Reconcile(context.Background(), s, f.c, &record); err != nil {
					t.Fatal(err)
				}
				if !exists || deletes != 1 {
					t.Fatal("later object removed")
				}
			}
			data, err := s.Data(&record)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(data)
			data.Close()
			if err != nil || len(b) != 0 {
				t.Fatal("intent artifact corrupted")
			}
		})
	}
}
