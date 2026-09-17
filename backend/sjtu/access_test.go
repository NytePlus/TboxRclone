package sjtu

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/object"
)

type gatedInput struct {
	entered chan struct{}
	proceed chan struct{}
	once    sync.Once
	source  io.Reader
}

func (r *gatedInput) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.proceed })
	return r.source.Read(p)
}

type unacceptedInput struct{ t *testing.T }

func (r unacceptedInput) Read([]byte) (int, error) {
	r.t.Error("conflicting input was consumed")
	return 0, io.EOF
}

func TestFileConflictRejectedBeforeJournalWaitAndInput(t *testing.T) {
	f, _ := newSimulator(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := &gatedInput{entered: make(chan struct{}), proceed: make(chan struct{}), source: strings.NewReader("safe")}
	var release sync.Once
	unblock := func() { release.Do(func() { close(input.proceed) }) }
	defer unblock()
	src := object.NewStaticObjectInfo("file", time.Now(), 4, true, nil, f)
	done := make(chan error, 1)
	go func() { _, err := f.Put(ctx, input, src); done <- err }()
	select {
	case <-input.entered:
	case <-ctx.Done():
		t.Fatal("first upload did not enter spool")
	}
	// Same account/path through another Fs and another state directory must
	// still collide before either journal acquisition or reading request bytes.
	alias := *f
	alias.name = "another-remote"
	alias.opt.StateDir = filepath.Join(t.TempDir(), "other-state")
	_, err := alias.Put(ctx, unacceptedInput{t}, src)
	if !errors.Is(err, ErrPathBusy) || !fserrors.IsNoRetryError(err) {
		t.Fatalf("second write: %v", err)
	}
	o := &Object{f: &alias, remote: "file"}
	if r, err := o.Open(ctx); !errors.Is(err, ErrPathBusy) {
		if r != nil {
			r.Close()
		}
		t.Fatalf("read during write: %v", err)
	}
	// A different file read is independent even while the first file is spooling.
	other := &Object{f: f, remote: "other", item: smh.Item{ETag: "v1", Size: 0}}
	r, err := other.Open(ctx)
	if err != nil {
		t.Fatalf("unrelated read: %v", err)
	}
	r.Close()
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Ownership ends after successful reconciliation, permitting normal reopen.
	obj, err := f.NewObject(ctx, "file")
	if err != nil {
		t.Fatal(err)
	}
	r, err = obj.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "safe" {
		t.Fatalf("readback %q: %v", got, err)
	}
}

func TestReadersHoldOwnershipUntilLastClose(t *testing.T) {
	f, sim := newSimulator(t, false)
	sim.published = true
	sim.bytes = []byte("old")
	ctx := context.Background()
	o, err := f.NewObject(ctx, "file")
	if err != nil {
		t.Fatal(err)
	}
	a, err := o.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := o.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	src := object.NewStaticObjectInfo("file", time.Now(), 3, true, nil, f)
	for i := 0; i < 2; i++ {
		_, err = f.Put(ctx, unacceptedInput{t}, src)
		if !errors.Is(err, ErrPathBusy) {
			t.Fatalf("read/write conflict: %v", err)
		}
		a.Close() // Repeated Close must not release b's ownership.
	}
	b.Close()
	_, err = f.Put(ctx, strings.NewReader("new"), src)
	if err == nil || errors.Is(err, ErrPathBusy) {
		t.Fatalf("expected existing create-only restriction, not leaked ownership: %v", err)
	}
}

func TestFailedOpenReleasesFileOwnership(t *testing.T) {
	f, sim := newSimulator(t, false)
	sim.published = true
	sim.bytes = []byte("old")
	o := &Object{f: f, remote: "file", item: smh.Item{ETag: "different", Size: 3}}
	r, err := o.Open(context.Background())
	if err == nil {
		r.Close()
		t.Fatal("version mismatch accepted")
	}
	release, err := f.acquireFile(f.root+"/file", true)
	if err != nil {
		t.Fatalf("failed open leaked ownership: %v", err)
	}
	release()
}

// No in-memory writer exists: this models a fresh backend after an interrupted
// process, with the durable journal as the only remaining ownership evidence.
func TestPendingJournalBlocksReadAfterReopen(t *testing.T) {
	f, sim := newSimulator(t, false)
	sim.published = true
	sim.bytes = []byte("old")
	ctx := context.Background()
	s, err := journal.Open(f.opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Prepare(ctx, f.c.Endpoint+"/"+f.c.Library+"/"+f.c.Space, f.root+"/file", strings.NewReader("new"), 3, 10)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	fresh := *f
	for _, state := range []string{"Prepared", "Uploading", "CommitSent", "Unknown", "AbortSent", "AbortUnknown"} {
		s, err = journal.Open(f.opt.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		r.State = state
		err = s.Save(r)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		o, err := fresh.NewObject(ctx, "file")
		if err != nil {
			t.Fatal(err)
		}
		reader, err := o.Open(ctx)
		if reader != nil {
			reader.Close()
		}
		if !errors.Is(err, journal.ErrPending) || !fserrors.IsNoRetryError(err) {
			t.Fatalf("%s: %v", state, err)
		}
	}
	s, err = journal.Open(f.opt.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	// An explicit, durably recorded cancellation releases the reservation.
	r.State = "Aborted"
	err = s.Save(r)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	o, err := fresh.NewObject(ctx, "file")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := o.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	b, err := io.ReadAll(reader)
	if err != nil || string(b) != "old" {
		t.Fatalf("old content %q: %v", b, err)
	}
}
