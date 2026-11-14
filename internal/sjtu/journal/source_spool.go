package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// SourceIdentity is persisted in the journal, never the source's content.
// Size and SHA-256 live in the enclosing Record, shared with remote verification.
type SourceIdentity struct {
	Path   string    `json:"path"`
	Device uint64    `json:"device"`
	Inode  uint64    `json:"inode"`
	Mtime  time.Time `json:"mtime"`
}

// SourceFileSpool borrows an immutable local file. Release drops the reference
// only; deleting a caller-owned source is deliberately not part of this contract.
type SourceFileSpool struct{ Path string }

func sourceIdentity(path string, st os.FileInfo) (*SourceIdentity, error) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() {
		return nil, errors.New("source must be a regular file with filesystem identity")
	}
	return &SourceIdentity{Path: path, Device: uint64(sys.Dev), Inode: uint64(sys.Ino), Mtime: st.ModTime()}, nil
}

// Nonblocking open prevents a replacement FIFO from hanging recovery. NOFOLLOW
// rejects final-component symlinks atomically; validate checks the opened inode
// and the current pathname before any content is consumed.
func openSource(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func (sp SourceFileSpool) Prepare(ctx context.Context, s *Store, r *Record, _ io.Reader, _, max int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sp.Path == "" {
		return errors.New("source path is empty")
	}
	path, err := filepath.Abs(sp.Path)
	if err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	identity, err := sourceIdentity(path, st)
	if err != nil {
		return err
	}
	if st.Size() > max {
		return errors.New("upload exceeds configured spool limit")
	}
	r.SpoolKind, r.Source, r.Size = "source-file", identity, st.Size()
	f, err := openSource(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := &sourceReader{File: f, record: *r}
	if err = reader.validate(); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, &contextReader{ctx, io.NewSectionReader(reader, 0, r.Size)})
	if err != nil {
		return err
	}
	if n != r.Size {
		return errors.New("source length changed")
	}
	if err = reader.validate(); err != nil {
		return err
	}
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	// A second validated pass also checks the recorded digest before publication.
	verified, err := sp.Open(s, r)
	if err != nil {
		return err
	}
	return verified.Close()
}

func (SourceFileSpool) Open(_ *Store, r *Record) (SpoolReader, error) {
	if r.SpoolReleased {
		return nil, errors.New("committed spool explicitly released")
	}
	if r.Source == nil || !filepath.IsAbs(r.Source.Path) {
		return nil, errors.New("invalid source spool identity")
	}
	f, err := openSource(r.Source.Path)
	if err != nil {
		return nil, err
	}
	reader := &sourceReader{File: f, record: *r}
	fail := func(err error) (SpoolReader, error) { f.Close(); return nil, err }
	if err = reader.validate(); err != nil {
		return fail(err)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.NewSectionReader(reader, 0, r.Size))
	if err != nil {
		return fail(err)
	}
	if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return fail(errors.New("source checksum mismatch"))
	}
	if err = reader.validate(); err != nil {
		return fail(err)
	}
	return reader, nil
}

func (SourceFileSpool) Release(_ *Store, _ *Record) error { return nil }

type sourceReader struct {
	File   *os.File
	record Record
}

func (f *sourceReader) validate() error {
	expected := f.record.Source
	for _, stat := range []func() (os.FileInfo, error){f.File.Stat, func() (os.FileInfo, error) { return os.Lstat(expected.Path) }} {
		st, err := stat()
		if err != nil {
			return err
		}
		id, err := sourceIdentity(expected.Path, st)
		if err != nil {
			return err
		}
		if id.Device != expected.Device || id.Inode != expected.Inode || !id.Mtime.Equal(expected.Mtime) || st.Size() != f.record.Size {
			return errors.New("source identity or content changed")
		}
	}
	return nil
}

func (f *sourceReader) ReadAt(p []byte, off int64) (int, error) {
	if err := f.validate(); err != nil {
		return 0, err
	}
	n, err := f.File.ReadAt(p, off)
	if check := f.validate(); check != nil {
		return 0, check
	}
	return n, err
}
func (f *sourceReader) Read(p []byte) (int, error) {
	if err := f.validate(); err != nil {
		return 0, err
	}
	n, err := f.File.Read(p)
	if check := f.validate(); check != nil {
		return 0, check
	}
	return n, err
}

func (f *sourceReader) Close() error { return f.File.Close() }
func (f *sourceReader) Seek(offset int64, whence int) (int64, error) {
	return f.File.Seek(offset, whence)
}
