package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMutationSessionReleasesSlotOnOpenFailure(t *testing.T) {
	f, _ := newSimulator(t, false)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	state := f.Opt.StateDir
	f.Opt.StateDir = filepath.Join(blocked, "state")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < MaxActiveMutations+1; i++ {
		if session, err := f.BeginMutation(ctx); err == nil {
			session.Close()
			t.Fatal("opened invalid state directory")
		}
	}
	if ctx.Err() != nil {
		t.Fatal("failed journal opens leaked mutation slots")
	}
	f.Opt.StateDir = state
	session, err := f.BeginMutation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if session.Store.MaxSpoolBytes != int64(f.Opt.MaxSpool) {
		t.Fatal("spool limit lost")
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if err = session.Close(); err != nil {
		t.Fatal("second close:", err)
	}
	// All slots must be available after cleanup, including repeated Close.
	for i := 0; i < MaxActiveMutations; i++ {
		release, err := f.AcquireMutation(ctx)
		if err != nil {
			t.Fatal("session leaked capacity:", err)
		}
		defer release()
	}
}

func TestRootSubtreeClaimConflictsWithRelativeChild(t *testing.T) {
	f, _ := newSimulator(t, false)
	release, err := f.AcquirePath("", true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if releaseChild, err := f.AcquireFile("child", false); err == nil {
		releaseChild()
		t.Fatal("root subtree did not protect its child")
	}
}
