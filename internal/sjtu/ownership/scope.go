package ownership

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/nyte/TboxRclone/internal/durablefile"
)

// ClaimScope binds a cloud space to one state directory in a shared ownership
// registry. All participating processes and containers must use the same registry.
// The binding persists after exit so changing state_dir cannot hide pending work.
// It is deliberately not removed automatically; migration requires an explicit
// offline procedure after resolving pending operations.
func ClaimScope(registry, state, scope string) error {
	if registry == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		registry = filepath.Join(base, "TboxRclone", "owners")
	}
	if !filepath.IsAbs(registry) || scope == "" {
		return errors.New("absolute ownership directory and cloud scope required")
	}
	if err := Claim(state); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(state)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(scope))
	slot := filepath.Join(registry, hex.EncodeToString(digest[:]))
	if err := Claim(slot); err != nil {
		return err
	}
	binding := filepath.Join(slot, "state-path")
	// Publishing with O_EXCL is safe under the process lease. A crash before
	// fsync may leave a partial binding: fail closed rather than adopt a new store.
	f, err := os.OpenFile(binding, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		st, e := os.Lstat(binding)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			return errors.New("ownership binding must be a private regular file")
		}
		data, e := os.ReadFile(binding)
		if e != nil {
			return e
		}
		if string(data) != canonical+"\n" {
			return errors.New("cloud space is bound to another state directory; resolve pending operations before offline migration")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err = durablefile.WriteSyncClose(f, []byte(canonical+"\n")); err != nil {
		return err
	}
	return durablefile.SyncDir(slot)
}
