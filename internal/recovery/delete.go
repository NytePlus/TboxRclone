package recovery

import (
	"context"
	"errors"
	"strings"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

// reconcileDelete never replays a DELETE, even if the original object remains.
// Under the exclusive-writer contract a confirmed absence releases the path.
// A lost response may leave the recycle ID unknown; absence alone does not prove
// recycle retention, and callers must not report that retention as verified.
func reconcileDelete(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if !strings.HasPrefix(r.Path, "codex-api-lab/") || smh.ValidatePath(r.Path) != nil {
		return errors.New("delete recovery requires an isolated lab path")
	}
	if r.State == "Committed" {
		return nil
	}
	if r.State != "DeleteSent" && r.State != "DeleteUnknown" {
		return errors.New("delete has no submitted intent")
	}
	_, err := c.Info(ctx, r.Path)
	if !smh.IsStatus(err, 404) {
		if err != nil {
			return err
		}
		return errors.New("delete target still exists or was recreated; refusing replay")
	}
	old := r.State
	r.State = "Committed"
	if err = s.Save(r); err != nil {
		r.State = old
		return err
	}
	return nil
}
