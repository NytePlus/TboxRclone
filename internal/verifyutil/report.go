package verifyutil

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyte/TboxRclone/internal/durablefile"
)

// SHA256 returns the lowercase hexadecimal content digest.
func SHA256(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// WriteEvidence creates a new JSON evidence file; existing evidence is never overwritten.
func WriteEvidence(output string, data []byte) error {
	evidenceRoot, pathErr := filepath.Abs("evidence")
	target, targetErr := filepath.Abs(output)
	if pathErr != nil || targetErr != nil || !strings.HasPrefix(target, evidenceRoot+string(os.PathSeparator)) || filepath.Ext(target) != ".json" {
		return errors.New("output must be a JSON path below evidence")
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	return durablefile.WriteSyncClose(f, data)
}
