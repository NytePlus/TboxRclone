package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SpoolReader supports sequential backup consumers and concurrent multipart reads.
type SpoolReader interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// Spool owns payload persistence, validated reopening and release. Prepare must
// leave recoverable content on success, even if subsequent journal saving fails.
// Release must never remove caller-owned source files.
type Spool interface {
	Prepare(context.Context, *Store, *Record, io.Reader, int64, int64) error
	Open(*Store, *Record) (SpoolReader, error)
	Release(*Store, *Record) error
}

func spoolFor(r *Record) (Spool, error) {
	switch r.SpoolKind {
	case "", "durable-file":
		return DurableFileSpool{}, nil
	case "source-file":
		return SourceFileSpool{}, nil
	default:
		return nil, errors.New("unknown spool kind")
	}
}

// DurableFileSpool preserves a complete, fsynced <operation-id>.data copy.
type DurableFileSpool struct{}

func (DurableFileSpool) Prepare(ctx context.Context, s *Store, r *Record, in io.Reader, size, max int64) error {
	file := filepath.Join(s.Dir, r.ID+".data")
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(file)
		}
	}()
	reserved := max
	if size >= 0 {
		reserved = size
	}
	if e = s.reserveSpool(ctx, f, reserved); e != nil {
		f.Close()
		return e
	}
	var output io.Writer = f
	if s.MaxSpoolBytes != 0 {
		output = &boundedSpool{f: f, remaining: reserved}
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(output, h), io.LimitReader(&contextReader{ctx, in}, max+1))
	if e == nil && n > max {
		e = errors.New("upload exceeds configured spool limit")
	}
	if e == nil && size >= 0 && n != size {
		e = fmt.Errorf("input length mismatch: expected %d received %d", size, n)
	}
	if e == nil {
		e = f.Truncate(n)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	r.Size = n
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	if e = s.syncDirectory(s.Dir); e != nil {
		return e
	}
	// Save may publish the record before a later fsync fails. Its spool must
	// survive any ambiguous publication outcome, even if this leaves an orphan.
	keep = true
	return nil
}

// Data verifies the spool's hash before returning a reader positioned at byte zero.
func (DurableFileSpool) Open(s *Store, r *Record) (SpoolReader, error) {
	if r.SpoolReleased {
		return nil, errors.New("committed spool explicitly released")
	}
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

func (DurableFileSpool) Release(s *Store, r *Record) error {
	err := os.Remove(filepath.Join(s.Dir, r.ID+".data"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.syncDirectory(s.Dir)
}
