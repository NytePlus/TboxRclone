package journal

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
