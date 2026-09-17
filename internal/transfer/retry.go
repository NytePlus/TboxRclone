package transfer

import (
	"context"
	"errors"
	"time"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/smh"
)

// StartWithRecovery initializes once, then makes at most three recovery attempts
// for transient failures. Submitted confirmations are only reconciled by reads;
// an ambiguous initialization requires explicit investigation and is never retried.
func StartWithRecovery(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	return startWithRecovery(ctx, s, c, r, func(ctx context.Context, delay time.Duration) error {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
}

func startWithRecovery(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record, wait func(context.Context, time.Duration) error) error {
	err := Start(ctx, s, c, r)
	for attempt := 0; err != nil && attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		if r.State != "Uploading" && r.State != "CommitSent" && r.State != "Unknown" {
			return err
		}
		if !transient(err) {
			return err
		}
		if e := wait(ctx, time.Second<<attempt); e != nil {
			return errors.Join(e, err)
		}
		err = Resume(ctx, s, c, r)
	}
	return err
}

func transient(err error) bool {
	if errors.Is(err, smh.ErrTransport) {
		return true
	}
	for _, status := range []int{408, 429, 500, 502, 503, 504} {
		if smh.IsStatus(err, status) {
			return true
		}
	}
	return false
}
