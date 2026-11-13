package transfer

import (
	"context"
	"errors"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
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
		if errors.Is(err, errSessionExpired) {
			if e := resetExpiredSession(c, r); e != nil {
				return errors.Join(e, err)
			}
			err = Start(ctx, s, c, r)
		} else {
			err = Resume(ctx, s, c, r)
		}
	}
	return err
}

func transient(err error) bool {
	if errors.Is(err, smh.ErrTransport) || errors.Is(err, smh.ErrSignedUploadAuthorization) || errors.Is(err, errSessionExpired) {
		return true
	}
	for _, status := range []int{408, 429, 500, 502, 503, 504} {
		if smh.IsStatus(err, status) {
			return true
		}
	}
	return false
}

// resetExpiredSession is safe only before confirmation: Uploading proves no
// confirm request was recorded. The immutable spool remains the source of truth.
// Do not save Prepared: until Start atomically publishes InitSent, a crash must
// leave the old Uploading record recoverable for another expiry check/rebuild.
func resetExpiredSession(c *smh.Client, r *journal.Record) error {
	if r.State != "Uploading" || r.ConfirmKey == "" || r.UploadID == "" || r.UploadPath == "" {
		return errors.New("only an initialized unconfirmed upload may be rebuilt")
	}
	if err := bound(c, r); err != nil {
		return err
	}
	r.State = "Prepared"
	r.ConfirmKey = ""
	r.UploadID = ""
	r.UploadPath = ""
	r.PartSize = 0
	r.Parts = nil
	return nil
}
