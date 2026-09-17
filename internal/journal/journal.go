// Package journal preserves complete upload bytes and ambiguous outcomes across crashes.
package journal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Record is the durable upload state; it contains no access token or signed URL.
type Record struct {
	ID         string `json:"id"`
	Scope      string `json:"scope"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	State      string `json:"state"`
	ConfirmKey string `json:"confirm_key,omitempty"`
	OldCAS     string `json:"old_cas,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	OldETag    string `json:"old_etag,omitempty"`
	OldSize    int64  `json:"old_size,omitempty"`
	UploadID   string `json:"upload_id,omitempty"`
	UploadPath string `json:"upload_path,omitempty"`
	PartSize   int64  `json:"part_size,omitempty"`
	Parts      []Part `json:"parts,omitempty"`
}

// Part records immutable spool identity and acknowledged remote content.
type Part struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	ETag   string `json:"etag,omitempty"`
}

// Store serializes access to the journal directory across processes.
type Store struct {
	Dir           string
	lock          *os.File
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
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := Open(dir)
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
func Open(dir string) (*Store, error) {
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
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &Store{Dir: dir, lock: f, syncDirectory: syncDir}, nil
}

// Close releases the process lock.
func (s *Store) Close() error { return s.lock.Close() }
func syncDir(dir string) error {
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func validID(id string) bool { b, e := hex.DecodeString(id); return e == nil && len(b) == 16 }

// Save atomically replaces a record and fsyncs its parent directory.
func (s *Store) Save(r *Record) error {
	if !validID(r.ID) {
		return errors.New("invalid operation ID")
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.Dir, ".record-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, filepath.Join(s.Dir, r.ID+".json")); e != nil {
		return e
	}
	return s.syncDirectory(s.Dir)
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
func (s *Store) Pending(scope, p string) error {
	records, e := s.Records()
	if e != nil {
		return e
	}
	for _, r := range records {
		if r.Scope == scope && r.Path == p && r.State != "Committed" && r.State != "Aborted" {
			return fmt.Errorf("%w: operation %s is %s; reconcile before retrying", ErrPending, r.ID, r.State)
		}
	}
	return nil
}

// Prepare persists exact input bytes before any remote operation.
func (s *Store) Prepare(ctx context.Context, scope, p string, in io.Reader, size, max int64) (*Record, error) {
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
	r := &Record{ID: hex.EncodeToString(id[:]), Scope: scope, Path: p, State: "Prepared"}
	file := filepath.Join(s.Dir, r.ID+".data")
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(file)
		}
	}()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(&contextReader{ctx, in}, max+1))
	if e == nil && n > max {
		e = errors.New("upload exceeds configured spool limit")
	}
	if e == nil && size >= 0 && n != size {
		e = fmt.Errorf("input length mismatch: expected %d received %d", size, n)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return nil, e
	}
	r.Size = n
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	if e = s.syncDirectory(s.Dir); e != nil {
		return nil, e
	}
	// Save may publish the record before a later fsync fails. Its spool must
	// survive any ambiguous publication outcome, even if this leaves an orphan.
	keep = true
	if e = s.Save(r); e != nil {
		return nil, e
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

// Data verifies the spool's hash before returning a reader positioned at byte zero.
func (s *Store) Data(r *Record) (*os.File, error) {
	if !validID(r.ID) {
		return nil, errors.New("invalid operation ID")
	}
	f, e := os.Open(filepath.Join(s.Dir, r.ID+".data"))
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil || n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		f.Close()
		return nil, errors.New("spool checksum mismatch")
	}
	if _, e = f.Seek(0, 0); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
