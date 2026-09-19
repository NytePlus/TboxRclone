package journal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// PruneCommitted releases one completed operation's local content, retaining its
// journal receipt. It never prunes cancelled, ambiguous or orphan data. The
// durable marker precedes unlink, so retry after interruption is idempotent.
// It requires an exclusive store: callers must stop active service operations.
func (s *Store) PruneCommitted(ctx context.Context, id string) error {
	if !s.exclusive || s.lock == nil {
		return errors.New("spool pruning requires exclusive store ownership")
	}
	if !validID(id) {
		return errors.New("invalid operation ID")
	}
	records, err := s.Records()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.ID != id {
			continue
		}
		if r.State != "Committed" {
			return errors.New("only committed spool data can be released")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !r.SpoolReleased {
			// Corrupt or unexpectedly missing content needs diagnosis, not cleanup.
			f, err := s.Data(&r)
			if err != nil {
				return err
			}
			if err = f.Close(); err != nil {
				return err
			}
			r.SpoolReleased = true
			if err = s.Save(&r); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		name := filepath.Join(s.Dir, id+".data")
		err = os.Remove(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return s.syncDirectory(s.Dir)
	}
	return errors.New("operation ID not found")
}
