// Package sjtu registers the experimental SJTU cloud drive backend.
package sjtu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/transfer"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/hash"
)

func init() {
	fs.Register(&fs.RegInfo{Name: "sjtu", Description: "SJTU cloud drive (experimental)", NewFs: NewFs, Options: []fs.Option{
		{Name: "endpoint", Default: "https://pan.sjtu.edu.cn", Help: "SMH HTTPS origin."},
		{Name: "library_id", Required: true, Help: "Library ID from personal space credentials."},
		{Name: "space_id", Required: true, Help: "Space ID from personal space credentials."},
		{Name: "token_file", Help: "Absolute path to a private access token file; reread on each request."},
		{Name: "user_token_file", Help: "Private UserToken file for automatic personal-space token refresh; takes precedence over token_file."},
		{Name: "organization_id", Default: "1", Help: "Organization ID for personal-space token refresh."},
		{Name: "state_dir", Required: true, Help: "Absolute path to a private durable upload journal directory."},
		{Name: "lab_writes", Default: false, Help: "Enable experimental create-only writes under codex-api-lab; not a release safety guarantee."},
		{Name: "max_upload", Default: fs.SizeSuffix(64 << 20), Help: "Maximum durable upload spool size. All files use resumable multipart, including empty files."},
	}})
}

// Options configures one account and the persistent journal.
type Options struct {
	Endpoint      string        `config:"endpoint"`
	Library       string        `config:"library_id"`
	Space         string        `config:"space_id"`
	TokenFile     string        `config:"token_file"`
	UserTokenFile string        `config:"user_token_file"`
	Organization  string        `config:"organization_id"`
	StateDir      string        `config:"state_dir"`
	LabWrites     bool          `config:"lab_writes"`
	MaxUpload     fs.SizeSuffix `config:"max_upload"`
}

// Fs represents one SJTU directory.
type Fs struct {
	name, root string
	opt        Options
	c          *smh.Client
	features   *fs.Features
}

// Object captures metadata for a specific remote content version.
type Object struct {
	f      *Fs
	remote string
	item   smh.Item
}

// NewFs creates a backend. Destructive capabilities remain disabled until validated.
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := Options{Endpoint: "https://pan.sjtu.edu.cn", MaxUpload: 64 << 20, Organization: "1"}
	if err := configstruct.Set(m, &opt); err != nil {
		return nil, err
	}
	root = strings.Trim(root, "/")
	if err := smh.ValidatePath(root); err != nil {
		return nil, err
	}
	if opt.MaxUpload <= 0 || opt.MaxUpload > 1<<40 {
		return nil, errors.New("max_upload must be between 1 byte and 1 TiB")
	}
	if opt.LabWrites && !strings.HasPrefix(root, "codex-api-lab/") {
		return nil, errors.New("lab_writes requires an isolated codex-api-lab/<run-id> root")
	}
	c, err := smh.New(opt.Endpoint, opt.Library, opt.Space, opt.TokenFile)
	if err != nil {
		return nil, err
	}
	if opt.UserTokenFile != "" {
		if err = c.UseUserToken(opt.UserTokenFile, opt.Organization); err != nil {
			return nil, err
		}
	} else if opt.TokenFile == "" {
		return nil, errors.New("token_file or user_token_file is required")
	}
	f := &Fs{name: name, root: root, opt: opt, c: c}
	f.features = (&fs.Features{CanHaveEmptyDirectories: true}).Fill(ctx, f)
	if root != "" {
		i, e := c.Info(ctx, root)
		if e != nil && !smh.IsStatus(e, 404) {
			return nil, e
		}
		if e == nil && i.Type != "dir" {
			f.root = path.Dir(root)
			if f.root == "." {
				f.root = ""
			}
			return f, fs.ErrorIsFile
		}
	}
	return f, nil
}
func (f *Fs) Name() string             { return f.name }
func (f *Fs) Root() string             { return f.root }
func (f *Fs) String() string           { return "SJTU cloud drive " + f.root }
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }
func (f *Fs) Hashes() hash.Set         { return hash.NewHashSet() }
func (f *Fs) Features() *fs.Features   { return f.features }
func (f *Fs) full(p string) (string, error) {
	if e := smh.ValidatePath(p); e != nil {
		return "", e
	}
	if p == "" {
		return f.root, nil
	}
	if f.root == "" {
		return p, nil
	}
	return f.root + "/" + p, nil
}
func mapped(err, missing error) error {
	if smh.IsStatus(err, 404) {
		return missing
	}
	if smh.IsStatus(err, 401) || smh.IsStatus(err, 403) {
		return errors.Join(fs.ErrorPermissionDenied, err)
	}
	return err
}

// List returns all immediate children or an explicit pagination error.
func (f *Fs) List(ctx context.Context, dir string) (fs.DirEntries, error) {
	p, e := f.full(dir)
	if e != nil {
		return nil, e
	}
	items, e := f.c.List(ctx, p)
	if e != nil {
		return nil, mapped(e, fs.ErrorDirNotFound)
	}
	out := make(fs.DirEntries, 0, len(items))
	for _, i := range items {
		remote := path.Join(dir, i.Name)
		if i.Type == "dir" {
			out = append(out, fs.NewDir(remote, i.Modified).SetID(i.Inode))
		} else if i.Type == "file" || i.Type == "image" || i.Type == "video" {
			out = append(out, &Object{f, remote, i})
		} else {
			return nil, fmt.Errorf("unsupported entry type %q", i.Type)
		}
	}
	return out, nil
}

// NewObject captures the current content validator.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	p, e := f.full(remote)
	if e != nil {
		return nil, e
	}
	i, e := f.c.Info(ctx, p)
	if e != nil {
		return nil, mapped(e, fs.ErrorObjectNotFound)
	}
	if i.Type == "dir" {
		return nil, fs.ErrorIsDir
	}
	if i.Type != "file" && i.Type != "image" && i.Type != "video" {
		return nil, fs.ErrorNotAFile
	}
	return &Object{f, remote, i}, nil
}
func (f *Fs) writeAllowed() error {
	if !f.opt.LabWrites {
		return fs.ErrorPermissionDenied
	}
	if !strings.HasPrefix(f.root, "codex-api-lab/") {
		return errors.New("write root is outside the isolated lab")
	}
	return nil
}

// Mkdir creates missing ancestors without overwriting existing files.
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	if e := f.writeAllowed(); e != nil {
		return e
	}
	p, e := f.full(dir)
	if e != nil {
		return e
	}
	parts := strings.Split(p, "/")
	for n := 1; n <= len(parts); n++ {
		current := strings.Join(parts[:n], "/")
		i, e := f.c.Info(ctx, current)
		if e == nil {
			if i.Type != "dir" {
				return fs.ErrorIsFile
			}
			continue
		}
		if !smh.IsStatus(e, 404) {
			return mapped(e, fs.ErrorDirNotFound)
		}
		e = f.c.JSON(ctx, "PUT", "directory", current, url.Values{"conflict_resolution_strategy": {"ask"}}, struct{}{}, nil)
		if e != nil {
			return fserrors.NoRetryError(e)
		}
	}
	return nil
}

// Rmdir refuses an unproven non-empty-directory deletion contract.
func (f *Fs) Rmdir(context.Context, string) error {
	return fserrors.NoRetryError(errors.New("safe atomic empty-directory deletion is not verified"))
}

// Put preserves input before starting an upload and refuses unsafe overwrite.
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	o := &Object{f: f, remote: src.Remote()}
	if e := o.Update(ctx, in, src, options...); e != nil {
		return nil, e
	}
	return o, nil
}

// PutStream spools an unknown-length stream before publishing it.
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}
func (o *Object) Fs() fs.Info                                     { return o.f }
func (o *Object) Remote() string                                  { return o.remote }
func (o *Object) String() string                                  { return o.remote }
func (o *Object) Size() int64                                     { return int64(o.item.Size) }
func (o *Object) ModTime(context.Context) time.Time               { return o.item.Modified }
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
	p, e := o.f.full(o.remote)
	if e != nil {
		return nil, e
	}
	return o.f.c.Open(ctx, p, o.item, start, length)
}

// Remove refuses path-based deletion until object-conditional semantics are verified.
func (o *Object) Remove(context.Context) error {
	return fserrors.NoRetryError(errors.New("object-conditional deletion is not verified"))
}

// Update implements durable, create-only uploads for isolated experiments.
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	f := o.f
	if e := f.writeAllowed(); e != nil {
		return e
	}
	p, e := f.full(o.remote)
	if e != nil {
		return e
	}
	s, e := journal.Open(f.opt.StateDir)
	if e != nil {
		return fserrors.NoRetryError(e)
	}
	defer s.Close()
	scope := f.opt.Endpoint + "/" + f.opt.Library + "/" + f.opt.Space
	r, e := s.Prepare(ctx, scope, p, in, src.Size(), int64(f.opt.MaxUpload))
	if e != nil {
		return fserrors.NoRetryError(e)
	}
	fail := func(err error) error {
		return fserrors.NoRetryError(fmt.Errorf("operation %s (%s), local data retained: %w", r.ID, r.State, err))
	}
	if _, e = f.c.Info(ctx, p); e == nil {
		return fail(errors.New("overwrite requires verified compare-and-swap semantics"))
	} else if !smh.IsStatus(e, 404) {
		return fail(e)
	}
	parent := path.Dir(o.remote)
	if parent == "." {
		parent = ""
	}
	if e = f.Mkdir(ctx, parent); e != nil {
		return fail(e)
	}
	if e = transfer.Start(ctx, s, f.c, r); e != nil {
		return fail(e)
	}
	o.item, e = f.c.Info(ctx, p)
	if e != nil {
		return fail(e)
	}
	return nil
}

var _ fs.Fs = (*Fs)(nil)
var _ fs.Object = (*Object)(nil)
var _ fs.PutStreamer = (*Fs)(nil)
