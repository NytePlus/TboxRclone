package sjtu

import (
	"errors"
	"io"
	"strings"
	"sync"
)

// ErrPathBusy is a conflict, not an invitation to queue a later overwrite.
var ErrPathBusy = errors.New("path busy: conflicting file access")

type accessKey struct{ scope, path string }
type accessCount struct {
	readers int
	writer  bool
	tree    bool
}

// Shared across Fs instances: remote names and state-directory choices must not
// bypass in-process ownership. Directory mutations additionally own descendants.
// This table does not replace process-instance or durable journal protection.
var fileAccess = struct {
	sync.Mutex
	paths map[accessKey]accessCount
}{paths: make(map[accessKey]accessCount)}

func (f *Fs) acquireFile(p string, write bool) (func(), error) { return f.acquirePath(p, write, false) }
func (f *Fs) acquirePath(p string, write, tree bool) (func(), error) {
	key := accessKey{f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space, p}
	fileAccess.Lock()
	for active, count := range fileAccess.paths {
		if active.scope != key.scope {
			continue
		}
		overlap := active.path == p || (count.tree && strings.HasPrefix(p, active.path+"/")) || (tree && strings.HasPrefix(active.path, p+"/"))
		if overlap && (count.writer || (write && count.readers != 0)) {
			fileAccess.Unlock()
			return nil, ErrPathBusy
		}
	}
	n := fileAccess.paths[key]
	if write {
		n.writer = true
		n.tree = tree
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
				n.tree = false
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
