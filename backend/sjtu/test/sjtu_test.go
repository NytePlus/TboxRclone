package test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/recovery"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
)

type simulator struct {
	mu                 sync.Mutex
	bytes              []byte
	staged             []byte
	overwrite          bool
	failPart           bool
	cancelAfterPart    context.CancelFunc
	changeDuringUpload []byte
	published          bool
	init, confirm      int
	drop               bool
	blockReconcile     bool
	replaceOnConfirm   []byte
	publishedName      string
	infoEchoRequested  bool
	api, data          *httptest.Server
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
		if sim.failPart {
			w.WriteHeader(403)
			return
		}
		sim.staged, _ = io.ReadAll(r.Body)
		if sim.changeDuringUpload != nil {
			sim.bytes = sim.changeDuringUpload
		}
		if sim.cancelAfterPart != nil {
			sim.cancelAfterPart()
		}
		w.Header().Set("ETag", `"part1"`)
		w.WriteHeader(200)
	}))
	t.Cleanup(sim.data.Close)
	sim.api = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sim.mu.Lock()
		defer sim.mu.Unlock()
		p := r.URL.Path
		if r.Method == "GET" && r.URL.Query().Get("upload") == "1" {
			if sim.blockReconcile {
				w.WriteHeader(503)
				return
			}
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
				name := sim.publishedName
				if name == "" {
					name = "file"
				}
				infoName := name
				if sim.infoEchoRequested {
					infoName = "file"
				}
				json.NewEncoder(w).Encode(smh.Item{Name: infoName, Type: "file", Size: smh.Int64(len(sim.bytes)), ETag: "v1", Path: []string{"codex-api-lab", "run", infoName}})
			} else {
				if r.URL.Query().Get("info") == "1" {
					json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
				} else {
					contents := []smh.Item{}
					if sim.published {
						name := sim.publishedName
						if name == "" {
							name = "file"
						}
						contents = append(contents, smh.Item{Name: name, Type: "file", Size: smh.Int64(len(sim.bytes)), ETag: "v1", Path: []string{"codex-api-lab", "run", name}})
					}
					json.NewEncoder(w).Encode(struct {
						Contents []smh.Item `json:"contents"`
						Next     string     `json:"nextMarker"`
					}{Contents: contents})
				}
			}
			return
		}
		if r.Method == "POST" && r.URL.Query().Has("multipart") {
			sim.init++
			want := "ask"
			if sim.overwrite {
				want = "overwrite"
			}
			if r.URL.Query().Get("conflict_resolution_strategy") != want {
				t.Error("unsafe strategy")
			}
			if sim.published && !sim.overwrite {
				w.WriteHeader(409)
				return
			}
			json.NewEncoder(w).Encode(smh.Upload{Key: "K", UploadID: "upload", Domain: sim.data.URL, Path: "/data", Parts: map[string]smh.PartSignature{"1": {Headers: map[string]string{"x-fixture": "part"}}}})
			return
		}
		if r.Method == "POST" {
			sim.confirm++
			want := "ask"
			if sim.overwrite {
				want = "overwrite"
			}
			if r.URL.Query().Get("conflict_resolution_strategy") != want {
				t.Error("incorrect confirm strategy")
			}
			sim.bytes = sim.staged
			sim.published = true
			if sim.replaceOnConfirm != nil {
				sim.bytes = sim.replaceOnConfirm
			}
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
	f := &Fs{BackendName: "test", BackendRoot: "codex-api-lab/run", WriteBoundary: "codex-api-lab/run", Opt: Options{Enc: DefaultEncoding, Endpoint: sim.api.URL, Library: "l", Space: "s", StateDir: filepath.Join(t.TempDir(), "state"), LabWrites: true, MaxUpload: 64 << 20}, Client: c}
	f.FeatureSet = (&fs.Features{CaseInsensitive: true, NoDirMoveFallback: true}).Fill(context.Background(), f)
	return f, sim
}

func TestFeaturesDeclareCloudNameCaseInsensitivity(t *testing.T) {
	f, _ := newSimulator(t, false)
	if !f.Features().CaseInsensitive {
		t.Fatal("SMH name matching must be advertised as case-insensitive")
	}
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
			s, e := journal.Open(f.Opt.StateDir)
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
	sim.blockReconcile = true
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
	s, e := journal.Open(f.Opt.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 || records[0].State != "Unknown" {
		t.Fatal(records, e)
	}
	sim.mu.Lock()
	sim.blockReconcile = false
	sim.mu.Unlock()
	if e = recovery.Reconcile(context.Background(), s, f.Client, &records[0]); e != nil {
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
	s, e := journal.Open(f.Opt.StateDir)
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

func TestCaseOnlyCollisionRetainsInput(t *testing.T) {
	f, sim := newSimulator(t, false)
	f.Opt.LabOverwrite = true
	sim.published = true
	sim.publishedName = "File"
	sim.infoEchoRequested = true
	sim.bytes = []byte("old")
	src := object.NewStaticObjectInfo("file", time.Now(), 3, true, nil, f)
	if _, err := f.Put(context.Background(), strings.NewReader("new"), src); err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("case-only collision was not rejected: %v", err)
	}
	if sim.init != 1 || sim.confirm != 0 || string(sim.bytes) != "old" {
		t.Fatalf("collision mutated remote: init=%d confirm=%d bytes=%q", sim.init, sim.confirm, sim.bytes)
	}
	s, err := journal.Open(f.Opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, err := s.Records()
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	if records[0].State != "Rejected" {
		t.Fatalf("duplicate response left ambiguous state: %s", records[0].State)
	}
	if err := s.Pending(records[0].Scope, records[0].Path); err != nil {
		t.Fatalf("definitive duplicate response kept path pending: %v", err)
	}
	data, err := s.Data(&records[0])
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	got, _ := io.ReadAll(data)
	if string(got) != "new" {
		t.Fatalf("retained input = %q", got)
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
	sim.replaceOnConfirm = []byte("evil")
	if _, err := f.Put(context.Background(), strings.NewReader("safe"), src); err == nil {
		t.Fatal("concurrent replacement accepted during automatic reconciliation")
	}
	s, e := journal.Open(f.Opt.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	if e = recovery.Reconcile(context.Background(), s, f.Client, &records[0]); e == nil {
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
