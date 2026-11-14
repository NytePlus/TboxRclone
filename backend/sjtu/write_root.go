package sjtu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyte/TboxRclone/internal/durablefile"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
)

const writeRootFile = "write-root"

// claimWriteRoot binds a state directory to the first explicit remote root
// that enables writes. rclone may later construct descendant or account-root
// Fs values internally; they inherit this boundary instead of widening it.
func claimWriteRoot(stateDir, candidate string) (string, error) {
	if !filepath.IsAbs(stateDir) {
		return "", errors.New("state directory must be absolute")
	}
	if candidate != "" && smh.ValidatePath(candidate) != nil {
		return "", errors.New("invalid configured remote write root")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return "", err
	}
	st, err := os.Lstat(stateDir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("state directory must be a private directory (0700)")
	}
	name := filepath.Join(stateDir, writeRootFile)
	for {
		data, readErr := os.ReadFile(name)
		if readErr == nil {
			root := strings.TrimSpace(string(data))
			if !smh.PathWithinRoot(root, root) {
				return "", errors.New("invalid persisted remote write root")
			}
			if candidate != "" && !smh.PathWithinRoot(root, candidate) && !smh.PathWithinRoot(candidate, root) {
				return "", errors.New("configured remote path is outside the state directory write root")
			}
			return root, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return "", readErr
		}
		if candidate == "" {
			return "", errors.New("writes require an explicit non-root remote path")
		}
		file, createErr := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(createErr, os.ErrExist) {
			continue
		}
		if createErr != nil {
			return "", createErr
		}
		_, writeErr := file.WriteString(candidate + "\n")
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr == nil {
			writeErr = durablefile.SyncDir(stateDir)
		}
		if writeErr != nil {
			return "", writeErr
		}
		return candidate, nil
	}
}
