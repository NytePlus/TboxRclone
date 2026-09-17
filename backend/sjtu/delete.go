package sjtu

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
)

// remove sends at most one trash deletion after persisting its intent. The
// caller's object version is checked under local ownership, not server CAS.
func (o *Object) remove(ctx context.Context) error {
	f := o.f
	if err := f.writeAllowed(); err != nil {
		return err
	}
	if !f.opt.LabDelete {
		return errors.New("delete requires explicit lab_delete and the single-controlled-client contract")
	}
	p, err := f.full(o.remote)
	if err != nil {
		return err
	}
	release, err := f.acquireFile(p, true)
	if err != nil {
		return err
	}
	defer release()
	s, err := journal.OpenContext(ctx, f.opt.StateDir)
	if err != nil {
		return err
	}
	defer s.Close()
	scope := f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space
	if err = s.Pending(scope, p); err != nil {
		return err
	}
	current, err := f.c.Info(ctx, p)
	if err != nil {
		return mapped(err, fs.ErrorObjectNotFound)
	}
	if current.Type != "file" && current.Type != "image" && current.Type != "video" {
		return fs.ErrorNotAFile
	}
	if o.item.ETag == "" || current.ETag != o.item.ETag || current.Size != o.item.Size {
		return errors.New("delete target changed; refresh object before deleting")
	}
	// A zero-byte spool is an intent artifact, not a backup of deleted content.
	r, err := s.Prepare(ctx, scope, p, strings.NewReader(""), 0, 0)
	if err != nil {
		return err
	}
	r.Kind, r.State = "delete", "DeleteSent"
	r.OldETag, r.OldSize = current.ETag, int64(current.Size)
	if err = s.Save(r); err != nil {
		return err
	}
	var result struct {
		RecycledID string `json:"recycledItemId"`
	}
	requestErr := f.c.JSON(ctx, "DELETE", "file", p, url.Values{"permanent": {"0"}}, nil, &result)
	r.RecycledID = result.RecycledID
	r.State = "DeleteUnknown"
	if err = s.Save(r); err != nil {
		return errors.Join(requestErr, err)
	}
	if err = recovery.Reconcile(ctx, s, f.c, r); err != nil {
		return errors.Join(requestErr, err)
	}
	return nil
}

// Remove requires explicit lab deletion and suppresses rclone mutation retries.
func (o *Object) Remove(ctx context.Context) error {
	if err := o.remove(ctx); err != nil {
		return fserrors.NoRetryError(err)
	}
	return nil
}
