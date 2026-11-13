package journal

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func store(t *testing.T) *Store {
	t.Helper()
	d := filepath.Join(t.TempDir(), "state")
	s, e := Open(d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestUnitU04ExactLength(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		size, max  int64
		ok         bool
	}{{"empty", "", 0, 10, true}, {"known", "abc", 3, 10, true}, {"unknown", "abc", -1, 10, true}, {"short", "ab", 3, 10, false}, {"excess", "abcd", 3, 10, false}, {"limit", "abcd", -1, 3, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			r, e := s.Prepare(context.Background(), "scope", "x", strings.NewReader(tc.data), tc.size, tc.max)
			if (e == nil) != tc.ok {
				t.Fatalf("%v", e)
			}
			if e == nil {
				f, e := s.Data(r)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				b, e := io.ReadAll(f)
				if e != nil || string(b) != tc.data {
					t.Fatal(string(b), e)
				}
			}
			records, e := s.Records()
			if e != nil {
				t.Fatal(e)
			}
			if !tc.ok && len(records) != 0 {
				t.Fatal("failed stream has a record")
			}
		})
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, errors.New("input failure") }
func TestUnitU04ReaderFailure(t *testing.T) {
	s := store(t)
	_, e := s.Prepare(context.Background(), "s", "x", badReader{}, -1, 10)
	if e == nil {
		t.Fatal("accepted failed reader")
	}
}
func TestUnitU07RecoveryAndUnknown(t *testing.T) {
	s := store(t)
	r, e := s.Prepare(context.Background(), "s", "x", strings.NewReader("safe"), 4, 10)
	if e != nil {
		t.Fatal(e)
	}
	r.State = "Unknown"
	r.ConfirmKey = "K"
	if e = s.Save(r); e != nil {
		t.Fatal(e)
	}
	s.Close()
	reopened, e := Open(s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	records, e := reopened.Records()
	if e != nil || len(records) != 1 || records[0].State != "Unknown" {
		t.Fatal(records, e)
	}
	if e = reopened.Pending("s", "x"); e == nil {
		t.Fatal("unknown replay allowed")
	}
	f, e := reopened.Data(&records[0])
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if e = reopened.Pending("other account", "x"); e != nil {
		t.Fatal("account isolation", e)
	}
}
func TestUnitU07SpoolCorruption(t *testing.T) {
	s := store(t)
	r, e := s.Prepare(context.Background(), "s", "x", strings.NewReader("safe"), 4, 10)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Dir, r.ID+".data"), []byte("evil"), 0600); e != nil {
		t.Fatal(e)
	}
	if f, e := s.Data(r); e == nil {
		f.Close()
		t.Fatal("corruption ignored")
	}
}
func TestUnitU09ProcessLock(t *testing.T) {
	s := store(t)
	other, e := Open(s.Dir)
	if e == nil {
		other.Close()
		t.Fatal("concurrent access allowed")
	}
}
func TestUnitU07CancelledInput(t *testing.T) {
	s := store(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := s.Prepare(ctx, "s", "x", strings.NewReader("a"), 1, 10)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestPrepareDirectorySyncFailurePreservesSpool(t *testing.T) {
	s := store(t)
	failure := errors.New("injected directory fsync failure")
	calls := 0
	s.syncDirectory = func(dir string) error {
		calls++
		if calls == 2 {
			return failure
		}
		return syncDir(dir)
	}
	_, err := s.Prepare(context.Background(), "scope", "file", strings.NewReader("durable bytes"), 13, 100)
	if !errors.Is(err, failure) {
		t.Fatalf("expected durability error, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.Records()
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	f, err := reopened.Data(&records[0])
	if err != nil {
		t.Fatalf("published record lost its complete spool: %v", err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "durable bytes" {
		t.Fatalf("data=%q err=%v", b, err)
	}
	if reopened.Pending("scope", "file") == nil {
		t.Fatal("unresolved preparation allowed blind retry")
	}
}

func TestOpenContextWaitsAndCancels(t *testing.T) {
	s := store(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if next, err := OpenContext(ctx, s.Dir); !errors.Is(err, context.DeadlineExceeded) {
		if next != nil {
			next.Close()
		}
		t.Fatalf("expected cancellation while occupied: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenContext(context.Background(), s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := OpenContext(canceled, s.Dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: %v", err)
	}
}

func TestPendingSnapshotDuringUnrelatedTransfer(t *testing.T) {
	s := store(t)
	r, err := s.Prepare(context.Background(), "scope", "pending", strings.NewReader("safe"), 4, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the store lock held: unrelated reads must not wait for network I/O.
	if err := CheckPending(s.Dir, "scope", "other"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPending(s.Dir, "other-account", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPending(s.Dir, "scope", "pending"); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	r.State = "Committed"
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := CheckPending(s.Dir, "scope", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPending(s.Dir, "scope", "other"); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}

func TestMoveFirstPublicationKeepsIdentity(t *testing.T) {
	s := store(t)
	calls := 0
	s.syncDirectory = func(string) error {
		calls++
		if calls == 2 {
			return errors.New("record directory sync failed")
		}
		return nil
	}
	_, err := s.PrepareMove(context.Background(), "scope", "source", "target", strings.NewReader("backup"), 6, 10, true)
	if err == nil {
		t.Fatal("expected publication failure")
	}
	records, err := s.Records()
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	r := records[0]
	if r.Kind != "move" || r.SourcePath != "source" || r.Path != "target" || r.State != "Prepared" || !r.Overwrite {
		t.Fatalf("unsafe first record: %+v", r)
	}
	for _, p := range []string{"source", "target"} {
		if err := s.Pending("scope", p); !errors.Is(err, ErrPending) {
			t.Fatal(p, err)
		}
	}
	data, err := s.Data(&r)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	b, err := io.ReadAll(data)
	if err != nil || string(b) != "backup" {
		t.Fatal("backup lost", err)
	}
}

func TestMutationFirstPublicationKeepsKindAndSubtrees(t *testing.T) {
	for _, kind := range []string{"delete", "rmdir", "dirmove"} {
		t.Run(kind, func(t *testing.T) {
			s := store(t)
			calls := 0
			s.syncDirectory = func(string) error {
				calls++
				if calls == 2 {
					return errors.New("publication sync failed")
				}
				return nil
			}
			var err error
			if kind == "dirmove" {
				_, err = s.PrepareDirectoryMove(context.Background(), "scope", "source", "target", strings.NewReader("backup"), 10)
			} else {
				_, err = s.PrepareDeletion(context.Background(), "scope", "target", kind)
			}
			if err == nil {
				t.Fatal("missing injected failure")
			}
			records, err := s.Records()
			if err != nil || len(records) != 1 {
				t.Fatal(records, err)
			}
			r := records[0]
			if r.Kind != kind || r.State != "Prepared" {
				t.Fatal("misclassified durable operation", r)
			}
			if kind == "dirmove" {
				for _, p := range []string{"source", "source/child", "target", "target/child"} {
					if !errors.Is(s.Pending("scope", p), ErrPending) {
						t.Fatal("missing subtree reservation", p)
					}
				}
				if err = s.PendingSubtree("scope", "source"); !errors.Is(err, ErrPending) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestConcurrentStoresExcludeRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := OpenConcurrentContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenConcurrentContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if recovery, err := Open(dir); !errors.Is(err, ErrBusy) {
		if recovery != nil {
			recovery.Close()
		}
		t.Fatalf("exclusive recovery not excluded: %v", err)
	}
	first.Close()
	if recovery, err := Open(dir); !errors.Is(err, ErrBusy) {
		if recovery != nil {
			recovery.Close()
		}
		t.Fatalf("remaining shared holder not protected: %v", err)
	}
	second.Close()
	exclusive, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if shared, err := OpenConcurrentContext(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		if shared != nil {
			shared.Close()
		}
		t.Fatalf("exclusive recovery bypassed: %v", err)
	}
}
