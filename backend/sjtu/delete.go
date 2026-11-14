package sjtu

import (
	"context"
	"errors"
	"net/url"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/recovery"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
)

// remove sends at most one trash deletion after persisting its intent. The
// caller's object version is checked under local ownership, not server CAS.
func (o *Object) remove(ctx context.Context) error {
	f := o.Backend
	if err := f.WriteAllowed(o.RemotePath); err != nil {
		return err
	}
	if !f.Opt.LabDelete {
		return errors.New("delete requires explicit lab_delete and the single-controlled-client contract")
	}
	p, err := f.FullPath(o.RemotePath)
	if err != nil {
		return err
	}
	release, err := f.AcquireFile(p, true)
	if err != nil {
		return err
	}
	defer release()
	session, err := f.BeginMutation(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	s := session.Store
	scope := session.Scope
	if err = s.Pending(scope, p); err != nil {
		return err
	}
	current, err := f.Client.Info(ctx, p)
	if err != nil {
		return mapped(err, fs.ErrorObjectNotFound)
	}
	if current.Type != "file" && current.Type != "image" && current.Type != "video" {
		return fs.ErrorNotAFile
	}
	if o.Item.ETag == "" || current.ETag != o.Item.ETag || current.Size != o.Item.Size {
		return errors.New("delete target changed; refresh object before deleting")
	}
	// A zero-byte spool is an intent artifact, not a backup of deleted content.
	r, err := s.PrepareDeletion(ctx, scope, p, "delete")
	if err != nil {
		return err
	}
	r.Kind = "delete"
	r.OldETag, r.OldSize = current.ETag, int64(current.Size)
	if err = f.bindWriteRoot(s, r); err != nil {
		return err
	}
	return f.deleteOnce(ctx, s, r, "file")
}

func (f *Fs) deleteOnce(ctx context.Context, s *journal.Store, r *journal.Record, kind string) error {
	r.State = "DeleteSent"
	if err := s.Save(r); err != nil {
		return err
	}
	var result struct {
		RecycledID smh.Identifier `json:"recycledItemId"`
	}
	query := url.Values{"permanent": {"0"}}
	if kind == "directory" {
		query.Set("directory_only", "1")
	}
	requestErr := f.Client.JSON(ctx, "DELETE", kind, r.Path, query, nil, &result)
	r.RecycledID = string(result.RecycledID)
	r.State = "DeleteUnknown"
	if err := s.Save(r); err != nil {
		return errors.Join(requestErr, err)
	}
	if err := recovery.Reconcile(ctx, s, f.Client, r); err != nil {
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

func (f *Fs) rmdir(ctx context.Context, dir string) error {
	if err := f.WriteAllowed(dir); err != nil {
		return err
	}
	if !f.Opt.LabDelete {
		return errors.New("rmdir requires explicit lab_delete and exclusive-writer contract")
	}
	p, err := f.FullPath(dir)
	if err != nil {
		return err
	}
	release, err := f.AcquirePath(p, true, true)
	if err != nil {
		return err
	}
	defer release()
	session, err := f.BeginMutation(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	s := session.Store
	scope := session.Scope
	if err = s.PendingSubtree(scope, p); err != nil {
		return err
	}
	info, err := f.Client.Info(ctx, p)
	if err != nil {
		return mapped(err, fs.ErrorDirNotFound)
	}
	if info.Type != "dir" {
		return fs.ErrorIsFile
	}
	children, err := f.Client.List(ctx, p)
	if err != nil {
		return err
	}
	if len(children) != 0 {
		return fs.ErrorDirectoryNotEmpty
	}
	r, err := s.PrepareDeletion(ctx, scope, p, "rmdir")
	if err != nil {
		return err
	}
	r.Kind = "rmdir"
	if err = f.bindWriteRoot(s, r); err != nil {
		return err
	}
	return f.deleteOnce(ctx, s, r, "directory")
}

// Rmdir uses exclusive subtree ownership from the emptiness check through
// confirmation. The server's directory_only flag alone does not supply safety.
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	if err := f.rmdir(ctx, dir); err != nil {
		return fserrors.NoRetryError(err)
	}
	return nil
}
