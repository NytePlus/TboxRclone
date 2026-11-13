package sjtu

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/nyte/TboxRclone/internal/pathclaims"
)

// ErrPathBusy is a conflict, not an invitation to queue a later overwrite.
var ErrPathBusy = errors.New("path busy: conflicting file access")

type AccessRequest struct {
	Path        string
	Write, Tree bool
}
type accessLease struct {
	scope    string
	requests []AccessRequest
}

// A lease installs all source/destination claims atomically. Distinct reader
// leases remain independent even if their path ranges overlap.
var fileAccess = struct {
	sync.Mutex
	leases map[*accessLease]bool
}{leases: make(map[*accessLease]bool)}

func (f *Fs) AcquireFile(p string, write bool) (func(), error) { return f.AcquirePath(p, write, false) }
func (f *Fs) AcquirePath(p string, write, tree bool) (func(), error) {
	return f.AcquirePaths(AccessRequest{p, write, tree})
}
func (f *Fs) AcquirePaths(requests ...AccessRequest) (func(), error) {
	lease := &accessLease{scope: f.Client.Endpoint + "/" + f.Client.Library + "/" + f.Client.Space, requests: append([]AccessRequest(nil), requests...)}
	fileAccess.Lock()
	defer fileAccess.Unlock()
	for active := range fileAccess.leases {
		if active.scope != lease.scope {
			continue
		}
		for _, a := range active.requests {
			for _, b := range lease.requests {
				if pathclaims.Conflict("/"+a.Path, a.Write, a.Tree, "/"+b.Path, b.Write, b.Tree) {
					return nil, ErrPathBusy
				}
			}
		}
	}
	fileAccess.leases[lease] = true
	var once sync.Once
	return func() {
		once.Do(func() { fileAccess.Lock(); defer fileAccess.Unlock(); delete(fileAccess.leases, lease) })
	}, nil
}

type ownedReader struct {
	io.ReadCloser
	release func()
	once    sync.Once
	err     error
}

func (r *ownedReader) Close() error {
	r.once.Do(func() {
		r.err = r.ReadCloser.Close()
		r.release()
	})
	return r.err
}

// Bound complete mutation lifetimes (spool/backup through reconciliation), not
// individual parts. Four uploads each use at most four data-plane workers.
const MaxActiveMutations = 4

var mutationSlots sync.Map

func (f *Fs) AcquireMutation(ctx context.Context) (func(), error) {
	scope := f.Client.Endpoint + "/" + f.Client.Library + "/" + f.Client.Space
	value, _ := mutationSlots.LoadOrStore(scope, make(chan struct{}, MaxActiveMutations))
	slots := value.(chan struct{})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
