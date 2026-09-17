package sjtu

import (
	"context"
	"sync"
)

type directoryEnsureKey struct{ scope, path string }
type directoryEnsure struct {
	done chan struct{}
	err  error
}

var directoryEnsures = struct {
	sync.Mutex
	active map[directoryEnsureKey]*directoryEnsure
}{active: make(map[directoryEnsureKey]*directoryEnsure)}

// Sibling uploads can need the same missing ancestor. Join only that identical
// idempotent ensure, never another kind of write or a failed/unknown mutation.
// The owner keeps the usual path lease; deletes and moves still fail fast.
func (f *Fs) mkdirComponent(ctx context.Context, current string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := directoryEnsureKey{f.c.Endpoint + "/" + f.c.Library + "/" + f.c.Space, current}
	directoryEnsures.Lock()
	if active, ok := directoryEnsures.active[key]; ok {
		directoryEnsures.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-active.done:
			return active.err
		}
	}
	active := &directoryEnsure{done: make(chan struct{})}
	directoryEnsures.active[key] = active
	directoryEnsures.Unlock()
	err := f.mkdirComponentOnce(ctx, current)
	directoryEnsures.Lock()
	active.err = err
	delete(directoryEnsures.active, key)
	close(active.done)
	directoryEnsures.Unlock()
	return err
}
