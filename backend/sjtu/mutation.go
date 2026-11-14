package sjtu

import (
	"context"
	"sync"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
)

// MutationSession owns the journal and capacity slot, but not path claims or
// operation-specific pending checks and recovery state transitions.
type MutationSession struct {
	Store   *journal.Store
	Scope   string
	release func()
	once    sync.Once
	err     error
}

func (f *Fs) BeginMutation(ctx context.Context) (*MutationSession, error) {
	release, err := f.AcquireMutation(ctx)
	if err != nil {
		return nil, err
	}
	store, err := journal.OpenConcurrentContext(ctx, f.Opt.StateDir)
	if err != nil {
		release()
		return nil, err
	}
	store.MaxSpoolBytes = int64(f.Opt.MaxSpool)
	writeRoot, err := f.writeRoot()
	if err != nil {
		store.Close()
		release()
		return nil, err
	}
	store.WriteRoot = writeRoot
	return &MutationSession{Store: store, Scope: f.Client.Endpoint + "/" + f.Client.Library + "/" + f.Client.Space, release: release}, nil
}

func (s *MutationSession) Close() error {
	s.once.Do(func() { s.err = s.Store.Close(); s.release() })
	return s.err
}
