package sjtu

import (
	"context"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/treebackup"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
)

// DirMove submits one whole-tree move after preserving a complete source backup.
// NoDirMoveFallback preserves standard destination errors without decomposing
// the move; other errors suppress mutation retries.
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	if err := f.dirMove(ctx, src, srcRemote, dstRemote); err != nil {
		if err == fs.ErrorDirExists {
			return err
		}
		return fserrors.NoRetryError(err)
	}
	return nil
}
func (f *Fs) dirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	if err := f.writeAllowed(dstRemote); err != nil {
		return err
	}
	if !f.opt.LabMove {
		return errors.New("directory move requires lab_move; whole-tree fallback is disabled")
	}
	old, ok := src.(*Fs)
	if !ok {
		return errors.New("directory move requires the same backend")
	}
	if err := old.writeAllowed(srcRemote); err != nil {
		return err
	}
	scope := f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space
	if scope != old.c.Endpoint+"/"+old.c.Library+"/"+old.c.Space || f.opt.StateDir != old.opt.StateDir {
		return errors.New("directory move requires the same space and state directory")
	}
	source, err := old.full(srcRemote)
	if err != nil {
		return err
	}
	target, err := f.full(dstRemote)
	if err != nil {
		return err
	}
	if strings.HasPrefix(source, target+"/") || strings.HasPrefix(target, source+"/") {
		return errors.New("directory move source and destination overlap")
	}
	release, err := f.acquirePaths(accessRequest{source, true, true}, accessRequest{target, true, true})
	if err != nil {
		return err
	}
	defer release()
	releaseSlot, err := f.acquireMutation(ctx)
	if err != nil {
		return err
	}
	defer releaseSlot()
	s, err := journal.OpenConcurrentContext(ctx, f.opt.StateDir)
	if err != nil {
		return err
	}
	defer s.Close()
	s.MaxSpoolBytes = int64(f.opt.MaxSpool)
	for _, p := range []string{source, target} {
		if err = s.PendingSubtree(scope, p); err != nil {
			return err
		}
	}
	info, err := f.c.Info(ctx, source)
	if err != nil {
		return mapped(err, fs.ErrorDirNotFound)
	}
	if info.Type != "dir" {
		return fs.ErrorIsFile
	}
	if source == target {
		return fs.ErrorDirExists
	}
	if _, err = f.c.Info(ctx, target); err == nil {
		return fs.ErrorDirExists
	} else if !smh.IsStatus(err, 404) {
		return err
	}
	backupCtx, cancel := context.WithCancel(ctx)
	input, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := treebackup.Write(backupCtx, f.c, source, output)
		output.CloseWithError(err)
		done <- err
	}()
	r, err := s.PrepareDirectoryMove(ctx, scope, source, target, input, int64(f.opt.MaxUpload))
	input.CloseWithError(err)
	cancel()
	backupErr := <-done
	if err != nil {
		return err
	}
	if backupErr != nil {
		return backupErr
	}
	// Ensure only cloud ancestors: dstRemote may be empty when moving to the
	// Fs root, which is already exclusively reserved by this operation.
	if err = f.mkdirCloudPath(ctx, path.Dir(target)); err != nil {
		return err
	}
	r.State = "MoveSent"
	if err = s.Save(r); err != nil {
		return err
	}
	requestErr := f.c.JSON(ctx, "PUT", "directory", target, url.Values{"conflict_resolution_strategy": {"ask"}}, map[string]string{"from": source}, nil)
	r.State = "MoveUnknown"
	if err = s.Save(r); err != nil {
		return errors.Join(requestErr, err)
	}
	if err = recovery.Reconcile(ctx, s, f.c, r); err != nil {
		return errors.Join(requestErr, err)
	}
	return nil
}
