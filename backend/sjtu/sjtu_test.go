package sjtu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
)

type simulator struct {
	mu            sync.Mutex
	bytes         []byte
	published     bool
	init, confirm int
	drop          bool
	api, data     *httptest.Server
}

func newSimulator(t *testing.T, drop bool) (*Fs, *simulator) {
	t.Helper()
	sim := &simulator{drop: drop}
	sim.data = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sim.mu.Lock()
		defer sim.mu.Unlock()
		if r.URL.Query().Get("access_token") != "" {
			t.Error("SMH credential sent to data plane")
		}
		sim.bytes, _ = io.ReadAll(r.Body)
		w.Header().Set("ETag", `"part1"`)
		w.WriteHeader(200)
	}))
	t.Cleanup(sim.data.Close)
	sim.api = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sim.mu.Lock()
		defer sim.mu.Unlock()
		p := r.URL.Path
		if r.Method == "GET" && r.URL.Query().Get("upload") == "1" {
			if r.URL.Query().Get("no_upload_part_info") != "1" {
				w.WriteHeader(400)
				io.WriteString(w, `{"code":"ParamInvalid","message":"uploadPartInfo is unsupported under current deployment"}`)
				return
			}
			json.NewEncoder(w).Encode(smh.UploadStatus{Confirmed: sim.published, Path: []string{"codex-api-lab", "run", "file"}})
			return
		}
		if strings.Contains(p, "/directory/") {
			if strings.HasSuffix(p, "/file") {
				if !sim.published {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: "file", Size: smh.Int64(len(sim.bytes)), ETag: "v1", Path: []string{"codex-api-lab", "run", "file"}})
			} else {
				json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
			}
			return
		}
		if r.Method == "POST" && r.URL.Query().Has("multipart") {
			sim.init++
			if r.URL.Query().Get("conflict_resolution_strategy") != "ask" {
				t.Error("unsafe strategy")
			}
			json.NewEncoder(w).Encode(smh.Upload{Key: "K", UploadID: "upload", Domain: sim.data.URL, Path: "/data", Parts: map[string]smh.PartSignature{"1": {Headers: map[string]string{"x-fixture": "part"}}}})
			return
		}
		if r.Method == "POST" {
			sim.confirm++
			sim.published = true
			if sim.drop {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			json.NewEncoder(w).Encode(smh.Item{Type: "file", Path: []string{"codex-api-lab", "run", "file"}, Size: smh.Int64(len(sim.bytes)), ETag: "v1"})
			return
		}
		if r.Method == "GET" {
			w.Header().Set("ETag", "\"v1\"")
			w.Write(sim.bytes)
			return
		}
		w.WriteHeader(405)
	}))
	t.Cleanup(sim.api.Close)
	c := &smh.Client{Endpoint: sim.api.URL, Library: "l", Space: "s", Token: func(context.Context) (string, error) { return "secret", nil }, HTTP: sim.api.Client()}
	f := &Fs{name: "test", root: "codex-api-lab/run", opt: Options{Endpoint: sim.api.URL, Library: "l", Space: "s", StateDir: filepath.Join(t.TempDir(), "state"), LabWrites: true, MaxUpload: 64 << 20}, c: c}
	f.features = (&fs.Features{}).Fill(context.Background(), f)
	return f, sim
}
func TestUnitU04U07UploadAndIndependentRead(t *testing.T) {
	for _, payload := range []string{"", "hello 中文"} {
		t.Run(payload, func(t *testing.T) {
			f, sim := newSimulator(t, false)
			src := object.NewStaticObjectInfo("file", time.Now(), int64(len(payload)), true, nil, f)
			o, e := f.Put(context.Background(), strings.NewReader(payload), src)
			if e != nil {
				t.Fatal(e)
			}
			if o.Size() != int64(len(payload)) || sim.confirm != 1 {
				t.Fatal(o, sim.confirm)
			}
			s, e := journal.Open(f.opt.StateDir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			records, e := s.Records()
			if e != nil || len(records) != 1 || records[0].State != "Committed" {
				t.Fatal(records, e)
			}
		})
	}
}
func TestUnitU05LostCommitReconcileNoReplay(t *testing.T) {
	f, sim := newSimulator(t, true)
	src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
	if _, e := f.Put(context.Background(), strings.NewReader("safe"), src); e == nil {
		t.Fatal("lost response reported success")
	}
	if _, e := f.Put(context.Background(), strings.NewReader("safe"), src); e == nil {
		t.Fatal("replayed unknown upload")
	}
	if sim.init != 1 || sim.confirm != 1 {
		t.Fatal("mutation replay", sim.init, sim.confirm)
	}
	s, e := journal.Open(f.opt.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 || records[0].State != "Unknown" {
		t.Fatal(records, e)
	}
	if e = recovery.Reconcile(context.Background(), s, f.c, &records[0]); e != nil {
		t.Fatal(e)
	}
	if records[0].State != "Committed" || sim.confirm != 1 {
		t.Fatal(records, sim.confirm)
	}
}
func TestUnitU09ConflictRetainsInput(t *testing.T) {
	f, sim := newSimulator(t, false)
	sim.published = true
	sim.bytes = []byte("old")
	src := object.NewStaticObjectInfo("file", time.Now(), 3, true, nil, f)
	if _, e := f.Put(context.Background(), strings.NewReader("new"), src); e == nil {
		t.Fatal("overwrite allowed")
	}
	if sim.init != 0 || string(sim.bytes) != "old" {
		t.Fatal("old version modified")
	}
	s, e := journal.Open(f.opt.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	data, e := s.Data(&records[0])
	if e != nil {
		t.Fatal(e)
	}
	defer data.Close()
	b, _ := io.ReadAll(data)
	if string(b) != "new" {
		t.Fatal("input not preserved")
	}
}
func TestUnitU04InvalidStreamNeverInitializesRemote(t *testing.T) {
	f, sim := newSimulator(t, false)
	src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
	if _, e := f.Put(context.Background(), strings.NewReader("bad"), src); e == nil {
		t.Fatal("short stream accepted")
	}
	if sim.init != 0 || sim.confirm != 0 {
		t.Fatal("invalid input reached remote")
	}
}
func TestUnitU09ReconcilePreservesConflict(t *testing.T) {
	f, sim := newSimulator(t, true)
	src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
	f.Put(context.Background(), strings.NewReader("safe"), src)
	sim.mu.Lock()
	sim.bytes = []byte("evil")
	sim.mu.Unlock()
	s, e := journal.Open(f.opt.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	if e = recovery.Reconcile(context.Background(), s, f.c, &records[0]); e == nil {
		t.Fatal("concurrent replacement accepted")
	}
	if records[0].State != "Unknown" {
		t.Fatal(records[0].State)
	}
	data, e := s.Data(&records[0])
	if e != nil {
		t.Fatal(e)
	}
	data.Close()
}
