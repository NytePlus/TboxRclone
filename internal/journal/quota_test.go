package journal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestQuotaRetainsCompletedAndOrphanData(t *testing.T) {
	s := store(t)
	s.MaxSpoolBytes = 10
	r, err := s.Prepare(context.Background(), "s", "first", strings.NewReader("1234"), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	r.State = "Committed"
	if err = s.Save(r); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir, "crash.data"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Prepare(context.Background(), "s", "second", strings.NewReader("12"), 2, 8)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("wanted quota error: %v", err)
	}
	f, err := s.Data(r)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, err := os.ReadFile(filepath.Join(s.Dir, "crash.data"))
	if err != nil || string(b) != "12345" {
		t.Fatal("orphan altered", err)
	}
	// Even when already over budget, no recovery data may be discarded.
	s.MaxSpoolBytes = 8
	_, err = s.PrepareDeletion(context.Background(), "s", "delete", "delete")
	if !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
}

func TestQuotaConcurrentReservationAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	a, err := OpenConcurrentContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.MaxSpoolBytes = 8
	b, err := OpenConcurrentContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.MaxSpoolBytes = 8
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { _, err := a.Prepare(context.Background(), "s", "first", reader, -1, 8); done <- err }()
	// Pipe Write completing proves the first reservation exists before the rival.
	if _, err = writer.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	_, err = b.Prepare(context.Background(), "s", "second", strings.NewReader("x"), 1, 8)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("parallel reservation escaped budget: %v", err)
	}
	writer.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	// Unknown input releases unused reservation after EOF.
	if _, err = b.Prepare(context.Background(), "s", "third", strings.NewReader("abcd"), 4, 8); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaFailedInputReleasesOnlyUnpublishedSpool(t *testing.T) {
	s := store(t)
	s.MaxSpoolBytes = 4
	_, err := s.Prepare(context.Background(), "s", "short", strings.NewReader("ab"), 4, 4)
	if err == nil {
		t.Fatal("accepted short input")
	}
	_, err = s.Prepare(context.Background(), "s", "long", strings.NewReader("abcde"), 4, 8)
	if err == nil {
		t.Fatal("accepted excess input")
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "*.data"))
	if len(files) != 0 {
		t.Fatal(files)
	}
	if _, err = s.Prepare(context.Background(), "s", "valid", strings.NewReader("abcd"), 4, 4); err != nil {
		t.Fatal(err)
	}
}

// The child dies while a real input stream is incomplete, after a prefix has
// reached its reserved spool. No test cleanup runs in that process.
func TestQuotaProcessDeath(t *testing.T) {
	if os.Getenv("TBOX_QUOTA_CRASH_CHILD") == "1" {
		s, err := Open(os.Getenv("TBOX_QUOTA_CRASH_DIR"))
		if err != nil {
			t.Fatal(err)
		}
		s.MaxSpoolBytes = 12
		_, err = s.Prepare(context.Background(), "s", "interrupted", &quotaCrashReader{}, -1, 8)
		t.Fatalf("unexpected completion: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.MaxSpoolBytes = 12
	old, err := s.Prepare(context.Background(), "s", "durable", strings.NewReader("safe"), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestQuotaProcessDeath$")
	cmd.Env = append(os.Environ(), "TBOX_QUOTA_CRASH_CHILD=1", "TBOX_QUOTA_CRASH_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "PREFIX_WRITTEN\n" {
			t.Fatalf("child not ready: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not reach crash point")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	reaped = true
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("child was not killed: %v", err)
	}
	if status, ok := exited.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatal("expected SIGKILL", exited)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.MaxSpoolBytes = 12
	records, err := s.Records()
	if err != nil || len(records) != 1 || records[0].ID != old.ID {
		t.Fatal("unexpected journal publication", records, err)
	}
	f, err := s.Data(old)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	files, err := filepath.Glob(filepath.Join(dir, "*.data"))
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	for _, file := range files {
		if filepath.Base(file) == old.ID+".data" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil || len(data) != 8 || string(data[:4]) != "part" {
			t.Fatalf("crash spool damaged: %q %v", data, err)
		}
	}
	_, err = s.Prepare(context.Background(), "s", "after-restart", strings.NewReader("x"), 1, 8)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("restart reused crash reservation: %v", err)
	}
}

type quotaCrashReader struct{ sent bool }

func (r *quotaCrashReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "part"), nil
	}
	fmt.Println("PREFIX_WRITTEN")
	for {
		time.Sleep(time.Hour)
	}
}

func TestQuotaLockWaitHonorsCancellation(t *testing.T) {
	s := store(t)
	s.MaxSpoolBytes = 8
	lock, err := os.OpenFile(filepath.Join(s.Dir, "quota.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = s.Prepare(ctx, "s", "cancel", strings.NewReader("data"), 4, 8)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "*.data"))
	if len(files) != 0 {
		t.Fatal("unpublished spool leaked", files)
	}
}

func TestPruneCommittedRetainsReceiptAndIsIdempotent(t *testing.T) {
	s := store(t)
	r, err := s.Prepare(context.Background(), "scope", "done", strings.NewReader("durable"), 7, 8)
	if err != nil {
		t.Fatal(err)
	}
	r.State = "Committed"
	if err = s.Save(r); err != nil {
		t.Fatal(err)
	}
	if err = s.PruneCommitted(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Dir, r.ID+".data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool still exists: %v", err)
	}
	records, err := s.Records()
	if err != nil || len(records) != 1 || !records[0].SpoolReleased || records[0].State != "Committed" {
		t.Fatalf("receipt was not retained: %#v %v", records, err)
	}
	if _, err = s.Data(&records[0]); err == nil {
		t.Fatal("released spool was readable")
	}
	if err = s.PruneCommitted(context.Background(), r.ID); err != nil {
		t.Fatalf("retry after completed prune: %v", err)
	}
}

func TestPruneCommittedRejectsUnresolvedAndSharedStore(t *testing.T) {
	s := store(t)
	r, err := s.Prepare(context.Background(), "scope", "pending", strings.NewReader("bytes"), 5, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PruneCommitted(context.Background(), r.ID); err == nil {
		t.Fatal("pruned unresolved record")
	}
	dir := s.Dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	shared, err := OpenConcurrentContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	if err = shared.PruneCommitted(context.Background(), r.ID); err == nil {
		t.Fatal("pruned through shared store")
	}
}
