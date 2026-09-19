package journal

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrQuota means accepting another spool would exceed the logical byte budget.
var ErrQuota = errors.New("aggregate spool capacity exhausted; existing recovery data retained")

// reserveSpool accounts reservations using the data files themselves. A crash
// leaves its reservation on disk, so restart cannot silently reuse those bytes.
// Truncate reserves logical capacity, not physical filesystem blocks.
func (s *Store) reserveSpool(ctx context.Context, f *os.File, size int64) error {
	if s.MaxSpoolBytes == 0 {
		return nil
	}
	if s.MaxSpoolBytes < 0 {
		return ErrQuota
	}
	lock, err := os.OpenFile(filepath.Join(s.Dir, "quota.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	remaining := s.MaxSpoolBytes
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".data") {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("unexpected non-regular spool")
		}
		if info.Size() > remaining {
			return ErrQuota
		}
		remaining -= info.Size()
	}
	if size > remaining {
		return ErrQuota
	}
	return f.Truncate(size)
}

// boundedSpool prevents an incorrectly declared stream from growing beyond its
// reservation before length validation runs. Failed unpublished data is removed.
type boundedSpool struct {
	f         *os.File
	remaining int64
}

func (w *boundedSpool) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("input exceeds reserved spool length")
	}
	n, err := w.f.Write(p)
	w.remaining -= int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
