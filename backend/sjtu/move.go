package sjtu

import (
	"context"
	"errors"
	"net/url"
	"path"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
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
	if err := f.writeAllowed(remote); err != nil {
		return nil, err
	}
	if !f.opt.LabMove {
		return nil, fs.ErrorCantMove
	}
	old, ok := src.(*Object)
	if !ok {
		return nil, fs.ErrorCantMove
	}
	if err := old.f.writeAllowed(old.remote); err != nil {
		return nil, err
	}
	scope := f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space
	if scope != old.f.c.Endpoint+"/"+old.f.c.Library+"/"+old.f.c.Space || f.opt.StateDir != old.f.opt.StateDir {
		return nil, fs.ErrorCantMove
	}
	source, err := old.f.full(old.remote)
	if err != nil {
		return nil, err
	}
	target, err := f.full(remote)
	if err != nil {
		return nil, err
	}
	release, err := f.acquirePaths(accessRequest{source, true, false}, accessRequest{target, true, false})
	if err != nil {
		return nil, err
	}
	defer release()
	releaseSlot, err := f.acquireMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()
	s, err := journal.OpenConcurrentContext(ctx, f.opt.StateDir)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	s.MaxSpoolBytes = int64(f.opt.MaxSpool)
	for _, p := range []string{source, target} {
		if err = s.PendingSubtree(scope, p); err != nil {
			return nil, err
		}
	}
	current, err := f.c.Info(ctx, source)
	if err != nil {
		return nil, mapped(err, fs.ErrorObjectNotFound)
	}
	if current.Type == "dir" || old.item.ETag == "" || current.ETag != old.item.ETag || current.Size != old.item.Size {
		return nil, errors.New("move source changed")
	}
	if source == target {
		return src, nil
	}
	strategy := "ask"
	if dest, e := f.c.Info(ctx, target); e == nil {
		if dest.Type == "dir" {
			return nil, fs.ErrorIsDir
		}
		if !f.opt.LabOverwrite {
			return nil, errors.New("move target exists; lab_overwrite is disabled")
		}
		strategy = "overwrite"
	} else if !smh.IsStatus(e, 404) {
		return nil, e
	}
	// Preserve the complete source before submitting a potentially ambiguous move.
	reader, err := f.c.Open(ctx, source, current, 0, -1)
	if err != nil {
		return nil, err
	}
	r, err := s.PrepareMove(ctx, scope, source, target, reader, int64(current.Size), int64(f.opt.MaxUpload), strategy == "overwrite")
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
	requestErr := f.c.JSON(ctx, "PUT", "file", target, url.Values{"conflict_resolution_strategy": {strategy}}, map[string]string{"from": source}, nil)
	r.State = "MoveUnknown"
	if err = s.Save(r); err != nil {
		return nil, errors.Join(requestErr, err)
	}
	if err = recovery.Reconcile(ctx, s, f.c, r); err != nil {
		return nil, errors.Join(requestErr, err)
	}
	return f.NewObject(ctx, remote)
}
