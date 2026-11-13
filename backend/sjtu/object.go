package sjtu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/nyte/TboxRclone/internal/sjtu/transfer"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/hash"
)

func (o *Object) Fs() fs.Info    { return o.Backend }
func (o *Object) Remote() string { return o.RemotePath }
func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.RemotePath
}
func (o *Object) Size() int64                                     { return int64(o.Item.Size) }
func (o *Object) ModTime(context.Context) time.Time               { return o.Item.Modified }
func (o *Object) Storable() bool                                  { return true }
func (o *Object) Hash(context.Context, hash.Type) (string, error) { return "", hash.ErrUnsupported }
func (o *Object) SetModTime(context.Context, time.Time) error     { return fs.ErrorCantSetModTime }

// Open rejects unsupported mandatory options and invalid or unbound ranges.
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	start, length := int64(0), int64(-1)
	for _, opt := range options {
		switch v := opt.(type) {
		case *fs.RangeOption:
			start, length = v.Decode(o.Size())
		case *fs.SeekOption:
			start = v.Offset
		default:
			if opt.Mandatory() {
				return nil, fmt.Errorf("unsupported open option %T", opt)
			}
		}
	}
	p, e := o.Backend.FullPath(o.RemotePath)
	if e != nil {
		return nil, e
	}
	release, e := o.Backend.AcquireFile(p, false)
	if e != nil {
		return nil, fserrors.NoRetryError(e)
	}
	if e = journal.CheckPending(o.Backend.Opt.StateDir, o.Backend.Client.Endpoint+"/"+o.Backend.Client.Library+"/"+o.Backend.Client.Space, p); e != nil {
		release()
		return nil, fserrors.NoRetryError(e)
	}
	r, e := o.Backend.Client.Open(ctx, p, o.Item, start, length)
	if e != nil {
		release()
		return nil, e
	}
	return &ownedReader{ReadCloser: r, release: release}, nil
}

// Update implements durable uploads and opt-in sequential overwrite in the lab.
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	f := o.Backend
	if e := f.WriteAllowed(o.RemotePath); e != nil {
		return e
	}
	p, e := f.FullPath(o.RemotePath)
	if e != nil {
		return e
	}
	// Acquire before waiting for the journal or accepting input. A competing
	// write must fail now, never wait and become an unintended later overwrite.
	release, e := f.AcquireFile(p, true)
	if e != nil {
		return fserrors.NoRetryError(e)
	}
	defer release()
	session, e := f.BeginMutation(ctx)
	if e != nil {
		return fserrors.NoRetryError(e)
	}
	defer session.Close()
	s := session.Store
	scope := session.Scope
	r, e := s.Prepare(ctx, scope, p, in, src.Size(), int64(f.Opt.MaxUpload))
	if e != nil {
		if errors.Is(e, journal.ErrQuota) || errors.Is(e, syscall.ENOSPC) || errors.Is(e, syscall.EDQUOT) {
			return fserrors.NoRetryError(httpStatusError{status: 507, err: e})
		}
		return fserrors.NoRetryError(e)
	}
	fail := func(err error) error {
		return fserrors.NoRetryError(fmt.Errorf("operation %s (%s), local data retained: %w", r.ID, r.State, err))
	}
	if previous, infoErr := f.Client.Info(ctx, p); infoErr == nil {
		if previous.Type == "dir" {
			return fail(fs.ErrorIsDir)
		}
		// SMH resolves path lookups case-insensitively. A request for case.bin
		// may therefore return the existing Case.bin. Treat that as a visible
		// name collision instead of silently overwriting an object whose exact
		// name differs and leaving the VFS cache with two impossible entries.
		actualName := previous.Name
		if actualName == "" && len(previous.Path) != 0 {
			actualName = previous.Path[len(previous.Path)-1]
		}
		requestedName := path.Base(p)
		caseVariant := ""
		if actualName != "" && actualName != requestedName {
			caseVariant = actualName
		}
		// Info may echo the spelling from the requested path even when SMH
		// resolved a differently-cased object. The parent listing is the source
		// of truth for the stored leaf name.
		siblings, listErr := f.Client.List(ctx, path.Dir(p))
		if listErr != nil {
			return fail(fmt.Errorf("cannot verify exact overwrite target name: %w", listErr))
		}
		exact := false
		for _, sibling := range siblings {
			if sibling.Name == requestedName {
				exact = true
				break
			}
			if strings.EqualFold(sibling.Name, requestedName) {
				caseVariant = sibling.Name
			}
		}
		if caseVariant != "" {
			// Keep the record create-only so the SMH API's native "ask"
			// strategy returns its duplicate-name conflict. Do not translate a
			// case-only collision into the overwrite strategy.
			r.Overwrite = false
		} else if !exact {
			return fail(errors.New("overwrite target missing from parent listing"))
		} else if !f.Opt.LabOverwrite {
			return fail(errors.New("overwrite requires explicit lab_overwrite and the single-controlled-client contract"))
		} else if previous.ETag == "" {
			return fail(errors.New("overwrite target has no content validator"))
		} else {
			r.Overwrite, r.OldETag, r.OldSize = true, previous.ETag, int64(previous.Size)
			if e = s.Save(r); e != nil {
				return fail(e)
			}
		}
	} else if !smh.IsStatus(infoErr, 404) {
		return fail(infoErr)
	}
	parent := path.Dir(o.RemotePath)
	if parent == "." {
		parent = ""
	}
	if e = f.Mkdir(ctx, parent); e != nil {
		return fail(e)
	}
	if e = transfer.StartWithRecovery(ctx, s, f.Client, r); e != nil {
		if smh.IsStatus(e, 409) {
			return fail(errors.Join(os.ErrExist, e))
		}
		return fail(e)
	}
	o.Item, e = f.Client.Info(ctx, p)
	if e != nil {
		return fail(e)
	}
	return nil
}

var _ fs.Fs = (*Fs)(nil)
var _ fs.Object = (*Object)(nil)
var _ fs.PutStreamer = (*Fs)(nil)

type httpStatusError struct {
	status int
	err    error
}

func (e httpStatusError) Error() string   { return e.err.Error() }
func (e httpStatusError) Unwrap() error   { return e.err }
func (e httpStatusError) HTTPStatus() int { return e.status }
