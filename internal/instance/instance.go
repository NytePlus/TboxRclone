// Package instance excludes independent service and recovery processes sharing
// a durable state directory. Ownership lasts until process exit, including while
// rclone is shutting down handles or running its unordered at-exit callbacks.
package instance

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var ErrInUse = errors.New("state directory belongs to another service or recovery process")

var owners = struct {
	sync.Mutex
	files map[string]*os.File
}{files: make(map[string]*os.File)}

// Claim permits multiple Fs objects within this process but excludes all other
// participating processes. The lock file must never be unlinked on release:
// replacing its inode would allow two simultaneous owners. The kernel releases
// the lock on normal exit, SIGKILL, or crash; the durable operation records remain.
// This does not exclude another machine or a different state directory.
func Claim(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("state directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("state directory must be private (0700)")
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	owners.Lock()
	defer owners.Unlock()
	if owners.files[canonical] != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(canonical, "instance.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return errors.New("instance lock must be a private regular file")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrInUse
		}
		return err
	}
	owners.files[canonical] = f
	return nil
}
