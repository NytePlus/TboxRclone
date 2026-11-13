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

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/ownership"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/rclone/rclone/backend/overview"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/encoder"
)

const DefaultEncoding = encoder.Display | encoder.EncodeWin | encoder.EncodeBackSlash | encoder.EncodeInvalidUtf8

func init() {
	fs.Register(&fs.RegInfo{Name: "sjtu", Description: "SJTU cloud drive (experimental)", NewFs: NewFs,
		Overview: &overview.BackendConfig{
			Backend: "sjtu", Name: "SJTU cloud drive (experimental)",
			IntegrationTests: "Incomplete; destructive operations disabled",
			DataIntegrity:    "Experimental; conditional overwrite not established",
		}, Options: []fs.Option{
			{Name: "encoding", Default: DefaultEncoding, Advanced: true, Help: "Reversible cloud filename encoding. Keep fixed for an existing managed root."},
			{Name: "endpoint", Default: "https://pan.sjtu.edu.cn", Help: "SMH HTTPS origin."},
			{Name: "library_id", Required: true, Help: "Library ID from personal space credentials."},
			{Name: "space_id", Required: true, Help: "Space ID from personal space credentials."},
			{Name: "token_file", Help: "Absolute path to a private access token file; reread on each request."},
			{Name: "user_token_file", Help: "Private UserToken file for automatic personal-space token refresh; takes precedence over token_file."},
			{Name: "organization_id", Default: "1", Help: "Organization ID for personal-space token refresh."},
			{Name: "state_dir", Required: true, Help: "Absolute path to a private durable upload journal directory."},
			{Name: "ownership_dir", Help: "Shared private cloud-space ownership registry; defaults to the user configuration directory. All service and recovery processes must share it."},
			{Name: "lab_writes", Default: false, Help: "Enable experimental create-only writes under codex-api-lab; not a release safety guarantee."},
			{Name: "lab_overwrite", Default: false, Help: "Experimental sequential overwrite under the single-controlled-client contract; requires lab_writes. No external writers allowed."},
			{Name: "lab_move", Default: false, Help: "Experimental journaled file and directory moves under the single-controlled-client contract; requires lab_writes."},
			{Name: "lab_delete", Default: false, Help: "Experimental trash deletion under the single-controlled-client contract; requires lab_writes."},
			{Name: "max_spool", Default: fs.SizeSuffix(4 << 30), Help: "Aggregate logical spool budget including completed, pending and orphan data. Unknown-length inputs reserve max_upload. Does not reserve physical disk blocks."},
			{Name: "max_upload", Default: fs.SizeSuffix(64 << 20), Help: "Maximum durable spool size, including complete directory-move tar backups. All uploaded files use resumable multipart, including empty files."},
		}})
}

// Options configures one account and the persistent journal.
type Options struct {
	Enc           encoder.MultiEncoder `config:"encoding"`
	Endpoint      string               `config:"endpoint"`
	Library       string               `config:"library_id"`
	Space         string               `config:"space_id"`
	TokenFile     string               `config:"token_file"`
	UserTokenFile string               `config:"user_token_file"`
	Organization  string               `config:"organization_id"`
	StateDir      string               `config:"state_dir"`
	OwnershipDir  string               `config:"ownership_dir"`
	LabWrites     bool                 `config:"lab_writes"`
	LabOverwrite  bool                 `config:"lab_overwrite"`
	LabDelete     bool                 `config:"lab_delete"`
	LabMove       bool                 `config:"lab_move"`
	MaxSpool      fs.SizeSuffix        `config:"max_spool"`
	MaxUpload     fs.SizeSuffix        `config:"max_upload"`
}

// Fs represents one SJTU directory.
type Fs struct {
	BackendName, BackendRoot string
	Opt                      Options
	Client                   *smh.Client
	FeatureSet               *fs.Features
}

// Object captures metadata for a specific remote content version.
type Object struct {
	Backend    *Fs
	RemotePath string
	Item       smh.Item
}

// NewFs creates a backend. Destructive capabilities remain disabled until validated.
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := Options{Enc: DefaultEncoding, Endpoint: "https://pan.sjtu.edu.cn", MaxUpload: 64 << 20, MaxSpool: 4 << 30, Organization: "1"}
	if err := configstruct.Set(m, &opt); err != nil {
		return nil, err
	}
	root = strings.Trim(root, "/")
	if err := validateStandardPath(root, opt.Enc); err != nil {
		return nil, err
	}
	if opt.MaxUpload <= 0 || opt.MaxUpload > 1<<40 {
		return nil, errors.New("max_upload must be between 1 byte and 1 TiB")
	}
	if opt.MaxSpool <= 0 || opt.MaxSpool > 1<<50 {
		return nil, errors.New("max_spool must be between 1 byte and 1 PiB")
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
	// Hold for the process lifetime, not an individual Fs: VFS handles and
	// rclone shutdown callbacks may outlive that object's cached lifetime.
	if err = ownership.ClaimScope(opt.OwnershipDir, opt.StateDir, c.Endpoint+"/"+c.Library+"/"+c.Space); err != nil {
		return nil, err
	}
	f := &Fs{BackendName: name, BackendRoot: root, Opt: opt, Client: c}
	// SMH resolves names case-insensitively. Advertising the opposite lets the
	// VFS cache hold both Case.bin and case.bin even though the second upload
	// replaces the first cloud object. That hides the collision from clients
	// such as Finder and leaves a stale entry in the mounted directory.
	f.FeatureSet = (&fs.Features{CaseInsensitive: true, CanHaveEmptyDirectories: true, MoveOverwrites: opt.LabMove, NoDirMoveFallback: true}).Fill(ctx, f)
	if !opt.LabMove {
		f.FeatureSet.Move = nil
	}
	if root != "" {
		i, e := c.Info(ctx, opt.Enc.FromStandardPath(root))
		if e != nil && !smh.IsStatus(e, 404) {
			return nil, e
		}
		if e == nil && i.Type != "dir" {
			f.BackendRoot = path.Dir(root)
			if f.BackendRoot == "." {
				f.BackendRoot = ""
			}
			return f, fs.ErrorIsFile
		}
	}
	return f, nil
}
func (f *Fs) Name() string             { return f.BackendName }
func (f *Fs) Root() string             { return f.BackendRoot }
func (f *Fs) String() string           { return "SJTU cloud drive " + f.BackendRoot }
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }
func (f *Fs) Hashes() hash.Set         { return hash.NewHashSet() }
func (f *Fs) Features() *fs.Features   { return f.FeatureSet }

// Validate before joining: path.Join would silently erase traversal and empty segments.
func validateStandardPath(p string, enc encoder.MultiEncoder) error {
	if p != "" {
		for _, part := range strings.Split(p, "/") {
			if part == "" || part == "." || part == ".." {
				return errors.New("invalid path segment")
			}
		}
	}
	return smh.ValidatePath(enc.FromStandardPath(p))
}
func (f *Fs) FullPath(p string) (string, error) {
	if err := validateStandardPath(p, f.Opt.Enc); err != nil {
		return "", err
	}
	if err := validateStandardPath(f.BackendRoot, f.Opt.Enc); err != nil {
		return "", err
	}
	joined := f.BackendRoot
	if p != "" {
		if joined != "" {
			joined += "/"
		}
		joined += p
	}
	return f.Opt.Enc.FromStandardPath(joined), nil
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
	p, e := f.FullPath(dir)
	if e != nil {
		return nil, e
	}
	release, e := f.AcquireFile(p, false)
	if e != nil {
		return nil, e
	}
	defer release()
	if e = journal.CheckPending(f.Opt.StateDir, f.Client.Endpoint+"/"+f.Client.Library+"/"+f.Client.Space, p); e != nil {
		return nil, e
	}
	items, e := f.Client.List(ctx, p)
	if e != nil {
		return nil, mapped(e, fs.ErrorDirNotFound)
	}
	out := make(fs.DirEntries, 0, len(items))
	for _, i := range items {
		remote := path.Join(dir, f.Opt.Enc.ToStandardName(i.Name))
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
	p, e := f.FullPath(remote)
	if e != nil {
		return nil, e
	}
	i, e := f.Client.Info(ctx, p)
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
func (f *Fs) WriteAllowed(remote string) error {
	if !f.Opt.LabWrites {
		return fs.ErrorPermissionDenied
	}
	FullPath, err := f.FullPath(remote)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(FullPath, "codex-api-lab/") || FullPath == "codex-api-lab/" {
		return errors.New("write root is outside the isolated lab")
	}
	return nil
}

// Mkdir creates missing ancestors without overwriting existing files.
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	if e := f.WriteAllowed(dir); e != nil {
		return e
	}
	p, e := f.FullPath(dir)
	if e != nil {
		return e
	}
	return f.mkdirCloudPath(ctx, p)
}

// mkdirCloudPath accepts an already encoded cloud path.
func (f *Fs) mkdirCloudPath(ctx context.Context, p string) error {
	if p == "" || p == "." {
		return nil
	}
	parts := strings.Split(p, "/")
	for n := 1; n <= len(parts); n++ {
		if err := f.mkdirComponent(ctx, strings.Join(parts[:n], "/")); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fs) mkdirComponentOnce(ctx context.Context, current string) error {
	scope := f.Client.Endpoint + "/" + f.Client.Library + "/" + f.Client.Space
	check := func() (bool, error) {
		if err := journal.CheckPending(f.Opt.StateDir, scope, current); err != nil {
			return false, fserrors.NoRetryError(err)
		}
		item, err := f.Client.Info(ctx, current)
		if err == nil {
			if item.Type != "dir" {
				return false, fs.ErrorIsFile
			}
			return true, nil
		}
		if !smh.IsStatus(err, 404) {
			return false, mapped(err, fs.ErrorDirNotFound)
		}
		return false, nil
	}
	// Existing directories need no exclusive ownership: sibling uploads may
	// independently ensure their shared ancestors without becoming conflicting.
	releaseRead, err := f.AcquireFile(current, false)
	if err != nil {
		return fserrors.NoRetryError(err)
	}
	exists, err := check()
	releaseRead()
	if err != nil || exists {
		return err
	}
	release, err := f.AcquireFile(current, true)
	if err != nil {
		return fserrors.NoRetryError(err)
	}
	defer release()
	// The path may have become reserved or been created since the first check.
	if exists, err := check(); err != nil || exists {
		return err
	}
	err = f.Client.JSON(ctx, "PUT", "directory", current, url.Values{"conflict_resolution_strategy": {"ask"}}, struct{}{}, nil)
	if smh.IsStatus(err, 409) {
		existing, checkErr := f.Client.Info(ctx, current)
		if checkErr == nil {
			if existing.Type == "dir" {
				return nil
			}
			return fs.ErrorIsFile
		}
		return fserrors.NoRetryError(errors.Join(err, checkErr))
	}
	if err != nil {
		return fserrors.NoRetryError(err)
	}
	return nil
}

// Put preserves input before starting a create or explicitly enabled overwrite.
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	o := &Object{Backend: f, RemotePath: src.Remote()}
	if e := o.Update(ctx, in, src, options...); e != nil {
		return nil, e
	}
	return o, nil
}

// PutStream spools an unknown-length stream before publishing it.
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}
