package sjtu

import (
	"errors"
	"io"
	"sync"
)

// ErrPathBusy is a conflict, not an invitation to queue a later overwrite.
var ErrPathBusy = errors.New("path busy: conflicting file access")

type accessKey struct{ scope, path string }
type accessCount struct {
	readers int
	writer  bool
}

// Shared across Fs instances: remote names and state-directory choices must not
// bypass in-process file ownership. Service-instance and subtree ownership are
// separate requirements; this table does not claim to implement either.
var fileAccess = struct {
	sync.Mutex
	paths map[accessKey]accessCount
}{paths: make(map[accessKey]accessCount)}

func (f *Fs) acquireFile(p string, write bool) (func(), error) {
	key := accessKey{f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space, p}
	fileAccess.Lock()
	n := fileAccess.paths[key]
	if n.writer || (write && n.readers != 0) {
		fileAccess.Unlock()
		return nil, ErrPathBusy
	}
	if write {
		n.writer = true
	} else {
		n.readers++
	}
	fileAccess.paths[key] = n
	fileAccess.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			fileAccess.Lock()
			defer fileAccess.Unlock()
			n := fileAccess.paths[key]
			if write {
				n.writer = false
			} else {
				n.readers--
			}
			if !n.writer && n.readers == 0 {
				delete(fileAccess.paths, key)
			} else {
				fileAccess.paths[key] = n
			}
		})
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
