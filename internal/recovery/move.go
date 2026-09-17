package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

func reconcileMove(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	for _, p := range []string{r.Path, r.SourcePath} {
		if !strings.HasPrefix(p, "codex-api-lab/") || smh.ValidatePath(p) != nil {
			return errors.New("move recovery requires isolated source and destination")
		}
	}
	if r.Path == r.SourcePath {
		return errors.New("move source equals destination")
	}
	if r.State == "Committed" || r.State == "Aborted" {
		return nil
	}
	if r.State == "Prepared" {
		// No mutation was sent: MoveSent must be durable before the request.
		// Retain the backup while releasing the two unused reservations.
		r.State = "Aborted"
		if err := s.Save(r); err != nil {
			r.State = "Prepared"
			return err
		}
		return nil
	}
	if r.State != "MoveSent" && r.State != "MoveUnknown" {
		return errors.New("move has no submitted intent")
	}
	data, err := s.Data(r)
	if err != nil {
		return err
	}
	data.Close()
	if _, err = c.Info(ctx, r.SourcePath); !smh.IsStatus(err, 404) {
		if err != nil {
			return err
		}
		return errors.New("move source still exists or was recreated; refusing replay")
	}
	info, err := c.Info(ctx, r.Path)
	if err != nil {
		return err
	}
	if info.Type == "dir" || int64(info.Size) != r.Size {
		return errors.New("move destination differs; source backup retained")
	}
	reader, err := c.Open(ctx, r.Path, info, 0, -1)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, reader)
	closeErr := reader.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return errors.New("move destination content differs; backup retained")
	}
	old := r.State
	r.State = "Committed"
	if err = s.Save(r); err != nil {
		r.State = old
		return err
	}
	return nil
}
