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
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
)

func TestMoveIntentBackupAndNoReplay(t *testing.T) {
	for _, mode := range []string{"ack", "drop_response", "source_recreated", "wrong_target", "denied", "existing_target", "overwrite", "operations_overwrite"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			f.opt.LabMove = true
			f.opt.LabOverwrite = mode == "overwrite" || mode == "operations_overwrite"
			f.features.MoveOverwrites = true
			sourceExists := true
			targetExists := mode == "existing_target" || f.opt.LabOverwrite
			moves := 0
			payload := "source contents"
			target := "old target"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					moves++
					expected := "ask"
					if f.opt.LabOverwrite {
						expected = "overwrite"
					}
					if r.URL.Query().Get("conflict_resolution_strategy") != expected {
						t.Error("unexpected overwrite")
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if body["from"] != f.root+"/source" {
						t.Error("wrong source")
					}
					records, err := (&journal.Store{Dir: f.opt.StateDir}).Records()
					if err != nil || len(records) != 1 || records[0].State != "MoveSent" || records[0].SourcePath != body["from"] {
						t.Error("missing durable move intent")
					}
					if mode == "denied" {
						w.WriteHeader(403)
						return
					}
					sourceExists = false
					targetExists = true
					target = payload
					if mode == "source_recreated" {
						sourceExists = true
					}
					if mode == "wrong_target" {
						target = "wrong contents!"
					}
					if mode == "drop_response" || mode == "source_recreated" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					io.WriteString(w, `{}`)
					return
				}
				isSource := strings.HasSuffix(r.URL.Path, "/source")
				isTarget := strings.HasSuffix(r.URL.Path, "/target")
				if (!sourceExists && isSource) || (!targetExists && isTarget) {
					w.WriteHeader(404)
					return
				}
				bytes := payload
				if isTarget {
					bytes = target
				}
				if strings.Contains(r.URL.Path, "/directory/") {
					if !isSource && !isTarget {
						io.WriteString(w, `{"type":"dir"}`)
						return
					}
					json.NewEncoder(w).Encode(smh.Item{Type: "file", ETag: "v1", Size: smh.Int64(len(bytes))})
					return
				}
				w.Header().Set("ETag", `"v1"`)
				io.WriteString(w, bytes)
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			src, err := f.NewObject(context.Background(), "source")
			if err != nil {
				t.Fatal(err)
			}
			move := f.Move
			if mode == "operations_overwrite" {
				move = func(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
					dst, e := f.NewObject(ctx, remote)
					if e != nil {
						return nil, e
					}
					return operations.Move(ctx, f, dst, remote, src)
				}
			}
			dst, err := move(context.Background(), src, "target")
			if mode == "ack" || mode == "drop_response" || f.opt.LabOverwrite {
				if err != nil || dst.Remote() != "target" || sourceExists || moves != 1 {
					t.Fatal(dst, err, moves)
				}
			} else if err == nil {
				t.Fatal("conflict reported success")
			}
			if mode == "existing_target" {
				if moves != 0 || target != "old target" || !sourceExists {
					t.Fatal("existing target changed")
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
			data, err := s.Data(&record)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(data)
			data.Close()
			if err != nil || string(b) != payload {
				t.Fatal("source backup lost")
			}
			if mode == "ack" || mode == "drop_response" || f.opt.LabOverwrite {
				if record.State != "Committed" {
					t.Fatal(record.State)
				}
				sourceExists = true
				target = "later object"
				if err = recovery.Reconcile(context.Background(), s, f.c, &record); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, p := range []string{record.Path, record.SourcePath} {
					if err = s.Pending(record.Scope, p); !errors.Is(err, journal.ErrPending) {
						t.Fatal("path reservation lost", p, err)
					}
				}
				if err = recovery.Reconcile(context.Background(), s, f.c, &record); err == nil {
					t.Fatal("ambiguous move accepted")
				}
			}
			if moves != 1 {
				t.Fatal("move replayed", moves)
			}
		})
	}
}

func TestPathPairAcquisitionIsAtomic(t *testing.T) {
	f, _ := newSimulator(t, false)
	source, target := f.root+"/source", f.root+"/target"
	busy, err := f.acquireFile(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer busy()
	if release, err := f.acquirePaths(accessRequest{source, true, false}, accessRequest{target, true, false}); !errors.Is(err, ErrPathBusy) {
		if release != nil {
			release()
		}
		t.Fatal(err)
	}
	free, err := f.acquireFile(source, true)
	if err != nil {
		t.Fatal("failed pair retained source", err)
	}
	free()
	busy()
	pair, err := f.acquirePaths(accessRequest{source, true, false}, accessRequest{target, true, false})
	if err != nil {
		t.Fatal(err)
	}
	defer pair()
	for _, p := range []string{source, target} {
		if release, err := f.acquireFile(p, false); !errors.Is(err, ErrPathBusy) {
			if release != nil {
				release()
			}
			t.Fatal("pair not protected", err)
		}
	}
	pair()
	pair()
	for _, p := range []string{source, target} {
		release, err := f.acquireFile(p, true)
		if err != nil {
			t.Fatal("pair leaked", err)
		}
		release()
	}
}

func TestPreparedMoveReconcileDoesNotTouchCloud(t *testing.T) {
	f, _ := newSimulator(t, false)
	s, err := journal.Open(f.opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scope := f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space
	r, err := s.PrepareMove(context.Background(), scope, f.root+"/source", f.root+"/target", strings.NewReader("backup"), 6, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Any cloud request would fail.
	if err := recovery.Reconcile(ctx, s, f.c, r); err != nil {
		t.Fatal(err)
	}
	if r.State != "Aborted" {
		t.Fatal(r.State)
	}
	for _, p := range []string{r.SourcePath, r.Path} {
		if err := s.Pending(scope, p); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.Data(r)
	if err != nil {
		t.Fatal(err)
	}
	data.Close()
	if err := recovery.Reconcile(ctx, s, f.c, r); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledDirectoryMoveDoesNotFallBackToPartialMoves(t *testing.T) {
	f, _ := newSimulator(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := operations.DirMove(ctx, f, "source-dir", "target-dir")
	if err == nil || !strings.Contains(err.Error(), "whole-tree") {
		t.Fatal(err)
	}
}
