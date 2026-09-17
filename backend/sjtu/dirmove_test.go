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
	"github.com/nyte/TboxRclone/internal/treebackup"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
)

type dirMoveNode struct {
	directory bool
	data      string
}

func TestDirectoryMoveSnapshotAndNoPartialFallback(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "lost_response", "source_recreated", "wrong_content", "extra_child", "missing_empty", "denied", "existing_target", "backup_failure", "backup_limit", "pending_child", "overlap"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			f.opt.LabMove = true
			source, target := f.root+"/source", f.root+"/target"
			nodes := map[string]dirMoveNode{source: {directory: true}}
			if mode != "empty" {
				nodes[source+"/空 目录"] = dirMoveNode{directory: true}
				nodes[source+"/空 目录/child"] = dirMoveNode{data: "original contents"}
				nodes[source+"/empty"] = dirMoveNode{directory: true}
				nodes[source+"/zero"] = dirMoveNode{}
			}
			if mode == "existing_target" {
				nodes[target] = dirMoveNode{directory: true}
				nodes[target+"/keep"] = dirMoveNode{data: "keep"}
			}
			if mode == "backup_limit" {
				f.opt.MaxUpload = 1
			}
			mutations := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				kind, p, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/l/s/")
				if !ok {
					w.WriteHeader(400)
					return
				}
				if r.Method == "PUT" {
					mutations++
					if kind != "directory" || p != target || r.URL.Query().Get("conflict_resolution_strategy") != "ask" {
						t.Error("wrong mutation endpoint or strategy")
						w.WriteHeader(400)
						return
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if body["from"] != source {
						t.Error("wrong source")
					}
					records, err := (&journal.Store{Dir: f.opt.StateDir}).Records()
					if err != nil || len(records) != 1 || records[0].Kind != "dirmove" || records[0].State != "MoveSent" {
						t.Error("missing durable directory intent")
					}
					if mode == "denied" {
						w.WriteHeader(403)
						return
					}
					moved := map[string]dirMoveNode{}
					for name, node := range nodes {
						if name == source || strings.HasPrefix(name, source+"/") {
							moved[target+strings.TrimPrefix(name, source)] = node
							delete(nodes, name)
						}
					}
					for name, node := range moved {
						nodes[name] = node
					}
					switch mode {
					case "source_recreated":
						nodes[source] = dirMoveNode{directory: true}
					case "wrong_content":
						nodes[target+"/空 目录/child"] = dirMoveNode{data: "different content"}
					case "extra_child":
						nodes[target+"/extra"] = dirMoveNode{data: "unexpected"}
					case "missing_empty":
						delete(nodes, target+"/empty")
					}
					if mode == "lost_response" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					w.WriteHeader(204)
					return
				}
				node, exists := nodes[p]
				if p == f.root || p == "codex-api-lab" {
					node = dirMoveNode{directory: true}
					exists = true
				}
				if !exists {
					w.WriteHeader(404)
					return
				}
				if kind == "file" {
					if mode == "backup_failure" && strings.HasPrefix(p, source+"/") {
						w.WriteHeader(503)
						return
					}
					w.Header().Set("ETag", `"v1"`)
					io.WriteString(w, node.data)
					return
				}
				item := func(name string, n dirMoveNode) smh.Item {
					typ := "file"
					if n.directory {
						typ = "dir"
					}
					return smh.Item{Name: name, Type: typ, Size: smh.Int64(len(n.data)), ETag: "v1"}
				}
				if r.URL.Query().Has("info") {
					json.NewEncoder(w).Encode(item("", node))
					return
				}
				contents := []smh.Item{}
				for name, n := range nodes {
					if child, ok := strings.CutPrefix(name, p+"/"); ok && !strings.Contains(child, "/") {
						contents = append(contents, item(child, n))
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"contents": contents})
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			if mode == "pending_child" {
				s, err := journal.Open(f.opt.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Prepare(context.Background(), f.c.Endpoint+"/l/s", source+"/unresolved", strings.NewReader("save"), 4, 4)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			remote := "target"
			if mode == "overlap" {
				remote = "source/target"
			}
			err := operations.DirMove(context.Background(), f, "source", remote)
			success := mode == "normal" || mode == "empty" || mode == "lost_response"
			if (err == nil) != success {
				t.Fatal("unexpected outcome", err)
			}
			if mode == "existing_target" && err != fs.ErrorDirExists {
				t.Fatal("nonstandard existing-directory error", err)
			}
			beforeSend := mode == "existing_target" || mode == "backup_failure" || mode == "backup_limit" || mode == "pending_child" || mode == "overlap"
			if beforeSend {
				if mutations != 0 {
					t.Fatal("preflight failure mutated remote", mutations)
				}
				if _, ok := nodes[source]; !ok {
					t.Fatal("source lost")
				}
				if mode == "existing_target" && nodes[target+"/keep"].data != "keep" {
					t.Fatal("existing directory modified")
				}
				return
			}
			if mutations != 1 {
				t.Fatal("directory move replayed or decomposed", mutations)
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
			record := &records[0]
			data, err := s.Data(record)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := treebackup.Manifest(data)
			data.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := 5
			if mode == "empty" {
				want = 1
			}
			if len(manifest) != want {
				t.Fatal("backup incomplete", manifest)
			}
			if success {
				if record.State != "Committed" {
					t.Fatal(record.State)
				}
			} else {
				if record.State != "MoveUnknown" {
					t.Fatal(record.State)
				}
				for _, p := range []string{source, source + "/child", target, target + "/child"} {
					if !errors.Is(s.Pending(record.Scope, p), journal.ErrPending) {
						t.Fatal("subtree reservation lost", p)
					}
				}
				for _, p := range []string{"source/new", "target/new"} {
					if !errors.Is(f.Mkdir(context.Background(), p), journal.ErrPending) {
						t.Fatal("mkdir bypassed pending tree")
					}
				}
				if _, err = f.List(context.Background(), "target"); !errors.Is(err, journal.ErrPending) {
					t.Fatal("list bypassed pending tree", err)
				}
				if err = recovery.Reconcile(context.Background(), s, f.c, record); err == nil {
					t.Fatal("incomplete tree accepted")
				}
			}
			if mutations != 1 {
				t.Fatal("reconcile replayed directory move")
			}
		})
	}
}

func TestDirectoryMoveBusySubtrees(t *testing.T) {
	f, _ := newSimulator(t, false)
	f.opt.LabMove = true
	source, target := f.root+"/source", f.root+"/target"
	release, err := f.acquireFile(source+"/open-child", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.DirMove(context.Background(), f, "source", "target"); !errors.Is(err, ErrPathBusy) {
		t.Fatal(err)
	}
	release()
	pair, err := f.acquirePaths(accessRequest{source, true, true}, accessRequest{target, true, true})
	if err != nil {
		t.Fatal(err)
	}
	defer pair()
	for _, p := range []string{"source", "source/child", "target", "target/child"} {
		if _, err = f.List(context.Background(), p); !errors.Is(err, ErrPathBusy) {
			t.Fatal("list crossed active subtree", p, err)
		}
	}
	free, err := f.acquireFile(f.root+"/unrelated", true)
	if err != nil {
		t.Fatal(err)
	}
	free()
}

var _ fs.DirMover = (*Fs)(nil)
