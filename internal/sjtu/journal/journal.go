// Package journal preserves complete upload bytes and ambiguous outcomes across crashes.
package journal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nyte/TboxRclone/internal/durablefile"
)

// Record is the durable upload state; it contains no access token or signed URL.
type Record struct {
	SpoolKind     string          `json:"spool_kind,omitempty"`
	Source        *SourceIdentity `json:"spool_source,omitempty"`
	SpoolReleased bool            `json:"spool_released,omitempty"`
	ID            string          `json:"id"`
	Kind          string          `json:"kind,omitempty"`
	SourcePath    string          `json:"source_path,omitempty"`
	RecycledID    string          `json:"recycled_item_id,omitempty"`
	Scope         string          `json:"scope"`
	WriteRoot     string          `json:"write_root,omitempty"`
	Path          string          `json:"path"`
	Size          int64           `json:"size"`
	SHA256        string          `json:"sha256"`
	State         string          `json:"state"`
	ConfirmKey    string          `json:"confirm_key,omitempty"`
	OldCAS        string          `json:"old_cas,omitempty"`
	Overwrite     bool            `json:"overwrite,omitempty"`
	OldETag       string          `json:"old_etag,omitempty"`
	OldSize       int64           `json:"old_size,omitempty"`
	UploadID      string          `json:"upload_id,omitempty"`
	UploadPath    string          `json:"upload_path,omitempty"`
	PartSize      int64           `json:"part_size,omitempty"`
	Parts         []Part          `json:"parts,omitempty"`
}

// Part records immutable spool identity and acknowledged remote content.
type Part struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	ETag   string `json:"etag,omitempty"`
}

// Store holds a journal lock. Recovery uses an exclusive lock; backend operations
// may use shared locks while the service owns all relevant paths exclusively.
type Store struct {
	// MaxSpoolBytes limits all .data files including completed and orphan data.
	// Zero is unlimited for standalone callers. Set before preparing operations.
	MaxSpoolBytes int64
	// WriteRoot is copied into every new record so recovery retains the same
	// remote boundary after the creating Fs no longer exists.
	WriteRoot     string
	Dir           string
	lock          *os.File
	exclusive     bool
	syncDirectory func(string) error
}

// ErrBusy indicates an existing owner of the store lock.
var ErrBusy = errors.New("state store busy; retry after active transfer completes")

// ErrPending identifies a durable reservation that survives process exit.
var ErrPending = errors.New("path has an unresolved operation")

// CheckPending reads atomically published records without taking the transfer
// lock. This lets unrelated reads continue while a different file is uploading.
// It is used under the backend's in-process path ownership; it does not replace
// service-instance exclusion or authorize a concurrent external writer.
func CheckPending(dir, scope, p string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("state directory must be absolute")
	}
	st, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil // A fresh store has never accepted an upload.
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("state directory must be a private directory (0700)")
	}
	return (&Store{Dir: dir}).Pending(scope, p)
}

// OpenContext waits for an available store or context cancellation.
func OpenContext(ctx context.Context, dir string) (*Store, error) {
	return openContext(ctx, dir, syscall.LOCK_EX)
}

// OpenConcurrentContext permits disjoint backend operations to publish separate
// records concurrently. The caller MUST hold process-lifetime instance ownership
// and all affected path leases, including subtrees for directory mutations.
// A record has exactly one writer. Standalone recovery must still use Open.
func OpenConcurrentContext(ctx context.Context, dir string) (*Store, error) {
	return openContext(ctx, dir, syscall.LOCK_SH)
}

func openContext(ctx context.Context, dir string, mode int) (*Store, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := openMode(dir, mode)
		if !errors.Is(err, ErrBusy) {
			return s, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Open takes a nonblocking process lock; callers must Close it.
func Open(dir string) (*Store, error) { return openMode(dir, syscall.LOCK_EX) }

func openMode(dir string, mode int) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("state directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("state directory must be a private directory (0700)")
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &Store{Dir: dir, lock: f, exclusive: mode == syscall.LOCK_EX, syncDirectory: syncDir}, nil
}

// Close releases the process lock.
func (s *Store) Close() error  { return s.lock.Close() }
func syncDir(dir string) error { return durablefile.SyncDir(dir) }
func validID(id string) bool   { b, e := hex.DecodeString(id); return e == nil && len(b) == 16 }

// Save atomically replaces a record and fsyncs its parent directory.
func (s *Store) Save(r *Record) error {
	if !validID(r.ID) {
		return errors.New("invalid operation ID")
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	return durablefile.Replace(filepath.Join(s.Dir, r.ID+".json"), ".record-", b, s.syncDirectory)
}

// Records lists all operations; malformed entries fail closed.
func (s *Store) Records() ([]Record, error) {
	names, e := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	if e != nil {
		return nil, e
	}
	var records []Record
	for _, name := range names {
		b, e := os.ReadFile(name)
		if e != nil {
			return nil, e
		}
		var r Record
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, e
		}
		if !validID(r.ID) || filepath.Base(name) != r.ID+".json" {
			return nil, errors.New("invalid journal record")
		}
		records = append(records, r)
	}
	return records, nil
}

// Pending prevents a later invocation from blindly replaying an unresolved write.
func (s *Store) Pending(scope, p string) error { return s.pending(scope, p, false) }

// PendingSubtree prevents a directory operation from crossing unresolved children.
func (s *Store) PendingSubtree(scope, p string) error { return s.pending(scope, p, true) }
func (s *Store) pending(scope, p string, tree bool) error {
	records, e := s.Records()
	if e != nil {
		return e
	}
	for _, r := range records {
		overlap := false
		for _, reserved := range []string{r.Path, r.SourcePath} {
			if reserved == "" {
				continue
			}
			if reserved == p || (tree && strings.HasPrefix(reserved, p+"/")) || ((r.Kind == "rmdir" || r.Kind == "dirmove") && strings.HasPrefix(p, reserved+"/")) {
				overlap = true
			}
		}
		if r.Scope == scope && overlap && r.State != "Committed" && r.State != "Aborted" && r.State != "Rejected" {
			return fmt.Errorf("%w: operation %s is %s; reconcile before retrying", ErrPending, r.ID, r.State)
		}
	}
	return nil
}

// Prepare persists exact input bytes before any remote operation.
func (s *Store) Prepare(ctx context.Context, scope, p string, in io.Reader, size, max int64) (*Record, error) {
	return s.prepare(ctx, scope, p, in, size, max, "", "", false)
}

// PrepareMove publishes the operation identity with the first durable record.
// A crash must never turn a move backup into a resumable upload.
func (s *Store) PrepareMove(ctx context.Context, scope, source, target string, in io.Reader, size, max int64, overwrite bool) (*Record, error) {
	if source == "" || source == target {
		return nil, errors.New("move requires distinct source and destination")
	}
	if err := s.Pending(scope, source); err != nil {
		return nil, err
	}
	return s.prepare(ctx, scope, target, in, size, max, "move", source, overwrite)
}

// PrepareDirectoryMove records both subtree reservations with the first backup publication.
func (s *Store) PrepareDirectoryMove(ctx context.Context, scope, source, target string, in io.Reader, max int64) (*Record, error) {
	if source == "" || target == "" || source == target || strings.HasPrefix(source, target+"/") || strings.HasPrefix(target, source+"/") {
		return nil, errors.New("directory move requires disjoint source and destination")
	}
	for _, p := range []string{source, target} {
		if err := s.PendingSubtree(scope, p); err != nil {
			return nil, err
		}
	}
	return s.prepare(ctx, scope, target, in, -1, max, "dirmove", source, false)
}

// PrepareDeletion publishes the deletion kind before any recovery-visible record.
func (s *Store) PrepareDeletion(ctx context.Context, scope, p, kind string) (*Record, error) {
	if kind != "delete" && kind != "rmdir" {
		return nil, errors.New("invalid deletion kind")
	}
	return s.prepare(ctx, scope, p, strings.NewReader(""), 0, 0, kind, "", false)
}

func (s *Store) prepare(ctx context.Context, scope, p string, in io.Reader, size, max int64, kind, source string, overwrite bool) (*Record, error) {
	return s.prepareWith(ctx, scope, p, in, size, max, kind, source, overwrite, DurableFileSpool{})
}

// PrepareSource records an existing local file without copying its bytes. The
// caller must keep the source unchanged until independent remote verification.
func (s *Store) PrepareSource(ctx context.Context, scope, p, source string, max int64) (*Record, error) {
	return s.prepareWith(ctx, scope, p, nil, -1, max, "upload", "", false, SourceFileSpool{Path: source})
}

func (s *Store) prepareWith(ctx context.Context, scope, p string, in io.Reader, size, max int64, kind, source string, overwrite bool, spool Spool) (*Record, error) {
	if max < 0 || size > max {
		return nil, errors.New("upload exceeds configured spool limit")
	}
	if e := s.Pending(scope, p); e != nil {
		return nil, e
	}
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return nil, e
	}
	r := &Record{ID: hex.EncodeToString(id[:]), Scope: scope, WriteRoot: s.WriteRoot, Path: p, State: "Prepared", Kind: kind, SourcePath: source, Overwrite: overwrite}
	if err := spool.Prepare(ctx, s, r, in, size, max); err != nil {
		return nil, err
	}
	if err := s.Save(r); err != nil {
		return nil, err
	}
	return r, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

// Data validates content before opening a spool through its storage contract.
func (s *Store) Data(r *Record) (SpoolReader, error) {
	if r.SpoolReleased {
		return nil, errors.New("committed spool explicitly released")
	}
	if !validID(r.ID) {
		return nil, errors.New("invalid operation ID")
	}
	spool, err := spoolFor(r)
	if err != nil {
		return nil, err
	}
	return spool.Open(s, r)
}
