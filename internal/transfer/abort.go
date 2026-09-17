package transfer

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
)

// ErrAlreadyCommitted means cancellation cannot revoke an existing publication.
var ErrAlreadyCommitted = errors.New("upload already committed; no formal file was deleted")

// Abort cancels only the recorded upload session. It never deletes a file path.
// An explicit repeated call may cancel the same still-active session again;
// a missing session is accepted only after durable abort intent and a path check.
func Abort(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if err := bound(c, r); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch r.State {
	case "Aborted":
		return nil
	case "Committed":
		return ErrAlreadyCommitted
	case "CommitSent", "Unknown":
		if err := recovery.Reconcile(ctx, s, c, r); err != nil {
			return err
		}
		return ErrAlreadyCommitted
	case "Prepared", "Uploading", "AbortSent", "AbortUnknown":
	default:
		return errors.New("cannot cancel an upload without a known session outcome")
	}
	data, err := s.Data(r)
	if err != nil {
		return err
	}
	data.Close()
	if r.State == "Prepared" {
		if r.ConfirmKey != "" || r.UploadID != "" {
			return errors.New("prepared operation unexpectedly has a remote session")
		}
		return setState(s, r, "Aborted")
	}
	if r.ConfirmKey == "" || r.UploadID == "" {
		return errors.New("missing multipart session identity")
	}
	status, err := c.UploadState(ctx, r.ConfirmKey)
	if err != nil {
		if smh.IsStatus(err, 404) && (r.State == "AbortSent" || r.State == "AbortUnknown") {
			return finishAbort(ctx, s, c, r)
		}
		return err
	}
	if strings.Join(status.Path, "/") != r.Path {
		return errors.New("upload path changed; cancellation refused")
	}
	if status.Confirmed {
		return committedDuringAbort(ctx, s, c, r)
	}
	if status.UploadID != r.UploadID {
		return errors.New("upload identity changed; cancellation refused")
	}
	if err = setState(s, r, "AbortSent"); err != nil {
		return err
	}
	err = c.JSON(ctx, "DELETE", "file", r.ConfirmKey, url.Values{"upload": {"1"}}, nil, nil)
	if err != nil {
		return errors.Join(err, setState(s, r, "AbortUnknown"))
	}
	status, err = c.UploadState(ctx, r.ConfirmKey)
	if smh.IsStatus(err, 404) {
		return finishAbort(ctx, s, c, r)
	}
	if err == nil && status.Confirmed && strings.Join(status.Path, "/") == r.Path {
		return committedDuringAbort(ctx, s, c, r)
	}
	if err == nil {
		err = errors.New("upload session still exists after cancellation")
	}
	return errors.Join(err, setState(s, r, "AbortUnknown"))
}

func setState(s *journal.Store, r *journal.Record, state string) error {
	previous := r.State
	r.State = state
	if err := s.Save(r); err != nil {
		r.State = previous
		return err
	}
	return nil
}
func finishAbort(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	previous, err := c.Info(ctx, r.Path)
	if smh.IsStatus(err, 404) && !r.Overwrite {
		return setState(s, r, "Aborted")
	}
	if err == nil && r.Overwrite && previous.Type != "dir" && previous.ETag == r.OldETag && int64(previous.Size) == r.OldSize {
		return setState(s, r, "Aborted")
	}
	if err == nil {
		err = errors.New("target exists after session disappeared; local data retained for investigation")
	}
	return errors.Join(err, setState(s, r, "AbortUnknown"))
}
func committedDuringAbort(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if err := setState(s, r, "Unknown"); err != nil {
		return err
	}
	if err := recovery.Reconcile(ctx, s, c, r); err != nil {
		return err
	}
	return ErrAlreadyCommitted
}
