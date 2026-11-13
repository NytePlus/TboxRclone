// Package durablefile provides low-level durable publication primitives.
package durablefile

import (
	"os"
	"path/filepath"
)

// SyncDir persists directory changes and reports sync or close failures.
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// WriteSyncClose closes f even if writing or syncing fails, preserving the first error.
func WriteSyncClose(f *os.File, data []byte) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Replace publishes a private temporary file by rename. A directory sync error
// can occur after publication; callers must treat that outcome as uncertain.
// syncDir is supplied to allow the owning store's durability fault injection.
func Replace(target, pattern string, data []byte, syncDir func(string) error) error {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = WriteSyncClose(f, data); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return err
	}
	return syncDir(dir)
}
