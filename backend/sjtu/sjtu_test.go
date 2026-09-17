package sjtu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
				json.NewEncoder(w).Encode(smh.Item{Type: "file", Size: smh.Int64(len(sim.bytes)), ETag: "v1", Path: []string{"codex-api-lab", "run", "file"}})
			} else {
				json.NewEncoder(w).Encode(smh.Item{Type: "dir"})
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
	f := &Fs{name: "test", root: "codex-api-lab/run", opt: Options{Enc: defaultEncoding, Endpoint: sim.api.URL, Library: "l", Space: "s", StateDir: filepath.Join(t.TempDir(), "state"), LabWrites: true, MaxUpload: 64 << 20}, c: c}
	f.features = (&fs.Features{NoDirMoveFallback: true}).Fill(context.Background(), f)
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
	s, e := journal.Open(f.opt.StateDir)
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
	sim.replaceOnConfirm = []byte("evil")
	if _, err := f.Put(context.Background(), strings.NewReader("safe"), src); err == nil {
		t.Fatal("concurrent replacement accepted during automatic reconciliation")
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

func TestMkdirConcurrentCreation(t *testing.T) {
	for _, kind := range []string{"dir", "file"} {
		t.Run(kind, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			infoCalls, puts := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/child") {
					io.WriteString(w, `{"type":"dir"}`)
					return
				}
				if r.Method == "PUT" {
					puts++
					w.WriteHeader(409)
					return
				}
				infoCalls++
				if infoCalls <= 2 {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: kind})
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			err := f.Mkdir(context.Background(), "child")
			if kind == "dir" && err != nil {
				t.Fatal(err)
			}
			if kind == "file" && !errors.Is(err, fs.ErrorIsFile) {
				t.Fatalf("file conflict accepted: %v", err)
			}
			if puts != 1 || infoCalls != 3 {
				t.Fatalf("puts=%d info=%d", puts, infoCalls)
			}
		})
	}
}

func TestSequentialOverwriteAndLostResponse(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprintf("drop=%t", drop), func(t *testing.T) {
			f, sim := newSimulator(t, drop)
			f.opt.LabOverwrite = true
			sim.overwrite = true
			sim.published = true
			sim.bytes = []byte{} // Finder/webdavfs first creates an empty file.
			src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
			o, err := f.Put(context.Background(), strings.NewReader("new!"), src)
			if err != nil {
				t.Fatal(err)
			}
			if sim.init != 1 || sim.confirm != 1 || string(sim.bytes) != "new!" {
				t.Fatalf("init=%d confirm=%d bytes=%q", sim.init, sim.confirm, sim.bytes)
			}
			reader, err := o.Open(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(b) != "new!" {
				t.Fatalf("reopen %q %v", b, err)
			}
			s, err := journal.Open(f.opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			records, err := s.Records()
			if err != nil || len(records) != 1 || records[0].State != "Committed" || !records[0].Overwrite || records[0].OldETag != "v1" {
				t.Fatalf("intent/result %v %v", records, err)
			}
		})
	}
}

func TestOverwriteFailureBeforeConfirmPreservesOld(t *testing.T) {
	for _, mode := range []string{"part_failure", "cancel", "target_changed"} {
		t.Run(mode, func(t *testing.T) {
			f, sim := newSimulator(t, false)
			f.opt.LabOverwrite = true
			sim.overwrite = true
			sim.published = true
			sim.bytes = []byte("old")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := "old"
			switch mode {
			case "part_failure":
				sim.failPart = true
			case "cancel":
				sim.cancelAfterPart = cancel
			case "target_changed":
				sim.changeDuringUpload = []byte("external")
				expected = "external"
			}
			src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
			if _, err := f.Put(ctx, strings.NewReader("new!"), src); err == nil {
				t.Fatal("failure reported success")
			}
			if sim.confirm != 0 || string(sim.bytes) != expected {
				t.Fatalf("old replaced: %q confirm=%d", sim.bytes, sim.confirm)
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
			data, err := s.Data(&records[0])
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			b, err := io.ReadAll(data)
			if err != nil || string(b) != "new!" {
				t.Fatalf("new data lost %q: %v", b, err)
			}
		})
	}
}

func TestMkdirCannotReplaceDurablyReservedFile(t *testing.T) {
	for _, target := range []string{"file", "file/child"} {
		t.Run(target, func(t *testing.T) {
			f, _ := newSimulator(t, false)
			puts := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
					io.WriteString(w, `{}`)
					return
				}
				if strings.Contains(r.URL.Path, "/file") {
					w.WriteHeader(404)
					return
				}
				io.WriteString(w, `{"type":"dir"}`)
			}))
			defer server.Close()
			f.c.Endpoint = server.URL
			f.c.HTTP = server.Client()
			s, err := journal.Open(f.opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			record, err := s.Prepare(context.Background(), f.c.Endpoint+"/l/s", f.root+"/file", strings.NewReader("pending bytes"), 13, 20)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			record.State = "Unknown"
			if err = s.Save(record); err != nil {
				s.Close()
				t.Fatal(err)
			}
			s.Close()
			fresh := *f
			if err = fresh.Mkdir(context.Background(), target); !errors.Is(err, journal.ErrPending) {
				t.Fatalf("pending path accepted: %v", err)
			}
			if puts != 0 {
				t.Fatal("directory mutation reached server", puts)
			}
			s, err = journal.Open(f.opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			data, err := s.Data(record)
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			bytes, err := io.ReadAll(data)
			if err != nil || string(bytes) != "pending bytes" {
				t.Fatalf("pending data lost: %q %v", bytes, err)
			}
		})
	}
}
