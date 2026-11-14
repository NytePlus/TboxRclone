package journal

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpoolProcessReader(t *testing.T) {
	dir := os.Getenv("TBOX_SPOOL_CONTRACT_DIR")
	if dir == "" {
		return
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, err := s.Records()
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	f, err := s.Data(&records[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "abcdefgh" {
		t.Fatal(string(b), err)
	}
}

func TestSpoolContract(t *testing.T) {
	for _, kind := range []string{"durable", "source"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			source := filepath.Join(t.TempDir(), "input")
			if err = os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
				t.Fatal(err)
			}
			var r *Record
			if kind == "source" {
				r, err = s.PrepareSource(ctx, "scope", "path", source, 8)
			} else {
				r, err = s.Prepare(ctx, "scope", "path", bytes.NewBufferString("abcdefgh"), 8, 8)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "source" {
				if _, err = os.Stat(filepath.Join(s.Dir, r.ID+".data")); !os.IsNotExist(err) {
					t.Fatal("source was copied", err)
				}
			}
			f, err := s.Data(r)
			if err != nil {
				t.Fatal(err)
			}
			b := make([]byte, 3)
			n, err := f.ReadAt(b, 2)
			if err != nil || n != 3 || string(b) != "cde" {
				t.Fatal(n, string(b), err)
			}
			f.Close()
			if err = s.PruneCommitted(ctx, r.ID); err == nil {
				t.Fatal("released before commit")
			}
			dir := s.Dir
			s.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSpoolProcessReader$")
			cmd.Env = append(os.Environ(), "TBOX_SPOOL_CONTRACT_DIR="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fresh process: %v %s", err, out)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			records, err := s.Records()
			if err != nil {
				t.Fatal(err)
			}
			r = &records[0]
			r.State = "Committed"
			if err = s.Save(r); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err = s.PruneCommitted(ctx, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			records, err = s.Records()
			if err != nil {
				t.Fatal(err)
			}
			if f, err = s.Data(&records[0]); err == nil {
				f.Close()
				t.Fatal("released spool reopened")
			}
			if b, err := os.ReadFile(source); err != nil || string(b) != "abcdefgh" {
				t.Fatal("caller source deleted", err)
			}
		})
	}
}

func TestSourceChangesFailClosed(t *testing.T) {
	for _, change := range []string{"missing", "replacement", "size", "mtime", "digest", "symlink", "fifo", "directory"} {
		t.Run(change, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			source := filepath.Join(t.TempDir(), "source")
			if err = os.WriteFile(source, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			r, err := s.PrepareSource(context.Background(), "s", "p", source, 100)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing":
				err = os.Remove(source)
			case "replacement", "symlink", "fifo", "directory":
				err = os.Rename(source, source+".old")
				if err != nil {
					t.Fatal(err)
				}
				if change == "fifo" {
					err = syscall.Mkfifo(source, 0600)
				} else if change == "directory" {
					err = os.Mkdir(source, 0700)
				} else if change == "symlink" {
					err = os.Symlink(source+".old", source)
				} else {
					err = os.WriteFile(source, []byte("original"), 0600)
				}
			case "size":
				err = os.WriteFile(source, []byte("longer contents"), 0600)
			case "mtime":
				err = os.Chtimes(source, time.Now(), r.Source.Mtime.Add(time.Second))
			case "digest":
				err = os.WriteFile(source, []byte("modified"), 0600)
				if err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(source, r.Source.Mtime, r.Source.Mtime)
			}
			if err != nil {
				t.Fatal(err)
			}
			if f, err := s.Data(r); err == nil {
				f.Close()
				t.Fatal("changed source accepted")
			}
		})
	}
}

func TestSourceOpenReaderRejectsReplacement(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "source")
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.PrepareSource(context.Background(), "s", "p", path, 100)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Data(r)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := f.ReadAt(make([]byte, 8), 0); n != 0 || err == nil {
		t.Fatal("open reader consumed replaced source", n, err)
	}
	if n, err := f.Read(make([]byte, 8)); n != 0 || err == nil {
		t.Fatal("sequential reader consumed replaced source", n, err)
	}
}
