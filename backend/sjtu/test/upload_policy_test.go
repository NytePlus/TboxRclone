package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
)

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
				if puts == 0 {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(smh.Item{Type: kind})
			}))
			defer server.Close()
			f.Client.Endpoint = server.URL
			f.Client.HTTP = server.Client()
			err := f.Mkdir(context.Background(), "child")
			if kind == "dir" && err != nil {
				t.Fatal(err)
			}
			if kind == "file" && !errors.Is(err, fs.ErrorIsFile) {
				t.Fatalf("file conflict accepted: %v", err)
			}
			if puts != 1 || infoCalls != 4 {
				t.Fatalf("puts=%d info=%d", puts, infoCalls)
			}
		})
	}
}

func TestSequentialOverwriteAndLostResponse(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprintf("drop=%t", drop), func(t *testing.T) {
			f, sim := newSimulator(t, drop)
			f.Opt.LabOverwrite = true
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
			s, err := journal.Open(f.Opt.StateDir)
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
			f.Opt.LabOverwrite = true
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
			s, err := journal.Open(f.Opt.StateDir)
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
			f.Client.Endpoint = server.URL
			f.Client.HTTP = server.Client()
			s, err := journal.Open(f.Opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			record, err := s.Prepare(context.Background(), f.Client.Endpoint+"/l/s", f.BackendRoot+"/file", strings.NewReader("pending bytes"), 13, 20)
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
			s, err = journal.Open(f.Opt.StateDir)
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

func TestAggregateQuotaRejectsBeforeUploadInitialization(t *testing.T) {
	f, sim := newSimulator(t, false)
	f.Opt.MaxSpool = 3
	src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
	if _, err := f.Put(context.Background(), strings.NewReader("safe"), src); err == nil {
		t.Fatal("accepted over-budget upload")
	} else {
		var status interface{ HTTPStatus() int }
		if !errors.As(err, &status) || status.HTTPStatus() != http.StatusInsufficientStorage {
			t.Fatalf("quota did not expose 507: %v", err)
		}
	}
	if sim.init != 0 || sim.confirm != 0 {
		t.Fatal("quota failure mutated cloud", sim.init, sim.confirm)
	}
	f.Opt.MaxSpool = 4
	if _, err := f.Put(context.Background(), strings.NewReader("safe"), src); err != nil {
		t.Fatal(err)
	}
}

func TestPendingUploadExposesLockedWithoutReinitializing(t *testing.T) {
	for _, state := range []string{"Prepared", "InitSent", "Uploading", "CommitSent", "Unknown"} {
		t.Run(state, func(t *testing.T) {
			f, sim := newSimulator(t, false)
			ctx := context.Background()
			s, err := journal.Open(f.Opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Prepare(ctx, f.Client.Endpoint+"/l/s", f.BackendRoot+"/file", strings.NewReader("retained"), 8, 20)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			r.State = state
			if err = s.Save(r); err != nil {
				s.Close()
				t.Fatal(err)
			}
			s.Close()
			fresh := *f
			src := object.NewStaticObjectInfo("file", time.Now(), 3, true, nil, &fresh)
			_, err = fresh.Put(ctx, strings.NewReader("new"), src)
			var status interface{ HTTPStatus() int }
			if !errors.Is(err, journal.ErrPending) || !errors.As(err, &status) || status.HTTPStatus() != http.StatusLocked {
				t.Fatalf("pending upload must expose423, not an opaque405/permission failure: %v", err)
			}
			if sim.init != 0 || sim.confirm != 0 {
				t.Fatal("pending retry mutated cloud")
			}
			s, err = journal.Open(f.Opt.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			records, err := s.Records()
			if err != nil || len(records) != 1 || records[0].State != state {
				t.Fatal(records, err)
			}
			data, err := s.Data(&records[0])
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			bytes, err := io.ReadAll(data)
			if err != nil || string(bytes) != "retained" {
				t.Fatal("retained input changed", err)
			}
		})
	}
}
