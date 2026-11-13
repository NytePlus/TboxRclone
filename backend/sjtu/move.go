package sjtu

import (
	"context"
	"errors"
	"net/url"
	"path"

	"github.com/nyte/TboxRclone/internal/sjtu/recovery"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
)

// Move preserves a source backup and reconciles a single server-side request.
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	o, err := f.move(ctx, src, remote)
	if err != nil {
		return nil, fserrors.NoRetryError(err)
	}
	return o, nil
}
func (f *Fs) move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	if err := f.WriteAllowed(remote); err != nil {
		return nil, err
	}
	if !f.Opt.LabMove {
		return nil, fs.ErrorCantMove
	}
	old, ok := src.(*Object)
	if !ok {
		return nil, fs.ErrorCantMove
	}
	if err := old.Backend.WriteAllowed(old.RemotePath); err != nil {
		return nil, err
	}
	scope := f.Client.Endpoint + "/" + f.Client.Library + "/" + f.Client.Space
	if scope != old.Backend.Client.Endpoint+"/"+old.Backend.Client.Library+"/"+old.Backend.Client.Space || f.Opt.StateDir != old.Backend.Opt.StateDir {
		return nil, fs.ErrorCantMove
	}
	source, err := old.Backend.FullPath(old.RemotePath)
	if err != nil {
		return nil, err
	}
	target, err := f.FullPath(remote)
	if err != nil {
		return nil, err
	}
	release, err := f.AcquirePaths(AccessRequest{source, true, false}, AccessRequest{target, true, false})
	if err != nil {
		return nil, err
	}
	defer release()
	session, err := f.BeginMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	s := session.Store
	for _, p := range []string{source, target} {
		if err = s.PendingSubtree(scope, p); err != nil {
			return nil, err
		}
	}
	current, err := f.Client.Info(ctx, source)
	if err != nil {
		return nil, mapped(err, fs.ErrorObjectNotFound)
	}
	if current.Type == "dir" || old.Item.ETag == "" || current.ETag != old.Item.ETag || current.Size != old.Item.Size {
		return nil, errors.New("move source changed")
	}
	if source == target {
		return src, nil
	}
	strategy := "ask"
	if dest, e := f.Client.Info(ctx, target); e == nil {
		if dest.Type == "dir" {
			return nil, fs.ErrorIsDir
		}
		if !f.Opt.LabOverwrite {
			return nil, errors.New("move target exists; lab_overwrite is disabled")
		}
		strategy = "overwrite"
	} else if !smh.IsStatus(e, 404) {
		return nil, e
	}
	// Preserve the complete source before submitting a potentially ambiguous move.
	reader, err := f.Client.Open(ctx, source, current, 0, -1)
	if err != nil {
		return nil, err
	}
	r, err := s.PrepareMove(ctx, scope, source, target, reader, int64(current.Size), int64(f.Opt.MaxUpload), strategy == "overwrite")
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	r.Kind = "move"
	r.SourcePath = source
	r.OldETag = current.ETag
	r.OldSize = int64(current.Size)
	if err = s.Save(r); err != nil {
		return nil, err
	}
	parent := path.Dir(remote)
	if parent == "." {
		parent = ""
	}
	if err = f.Mkdir(ctx, parent); err != nil {
		return nil, err
	}
	r.State = "MoveSent"
	if err = s.Save(r); err != nil {
		return nil, err
	}
	requestErr := f.Client.JSON(ctx, "PUT", "file", target, url.Values{"conflict_resolution_strategy": {strategy}}, map[string]string{"from": source}, nil)
	r.State = "MoveUnknown"
	if err = s.Save(r); err != nil {
		return nil, errors.Join(requestErr, err)
	}
	if err = recovery.Reconcile(ctx, s, f.Client, r); err != nil {
		return nil, errors.Join(requestErr, err)
	}
	return f.NewObject(ctx, remote)
}
