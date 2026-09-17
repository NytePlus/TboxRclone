package sjtu

import (
	"errors"
	"io"
	"strings"
	"sync"
)

// ErrPathBusy is a conflict, not an invitation to queue a later overwrite.
var ErrPathBusy = errors.New("path busy: conflicting file access")

type accessRequest struct {
	path        string
	write, tree bool
}
type accessLease struct {
	scope    string
	requests []accessRequest
}

// A lease installs all source/destination claims atomically. Distinct reader
// leases remain independent even if their path ranges overlap.
var fileAccess = struct {
	sync.Mutex
	leases map[*accessLease]bool
}{leases: make(map[*accessLease]bool)}

func (f *Fs) acquireFile(p string, write bool) (func(), error) { return f.acquirePath(p, write, false) }
func (f *Fs) acquirePath(p string, write, tree bool) (func(), error) {
	return f.acquirePaths(accessRequest{p, write, tree})
}
func (f *Fs) acquirePaths(requests ...accessRequest) (func(), error) {
	lease := &accessLease{scope: f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space, requests: append([]accessRequest(nil), requests...)}
	fileAccess.Lock()
	defer fileAccess.Unlock()
	for active := range fileAccess.leases {
		if active.scope != lease.scope {
			continue
		}
		for _, a := range active.requests {
			for _, b := range lease.requests {
				overlap := a.path == b.path || (a.tree && strings.HasPrefix(b.path, a.path+"/")) || (b.tree && strings.HasPrefix(a.path, b.path+"/"))
				if overlap && (a.write || b.write) {
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
