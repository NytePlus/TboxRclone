// Package recovery reconciles durable operations using reads only; it never replays a mutation.
package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
)

// Reconcile checks upload session/content or deletion absence before recording completion.
// The local spool is retained, including when remote data no longer matches.
func Reconcile(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if r.Scope != c.Endpoint+"/"+c.Library+"/"+c.Space {
		return errors.New("account or space does not match journal")
	}
	if r.Kind == "move" || r.Kind == "dirmove" {
		return reconcileMove(ctx, s, c, r)
	}
	if r.Kind == "delete" || r.Kind == "rmdir" {
		return reconcileDelete(ctx, s, c, r)
	}
	if r.Kind != "" && r.Kind != "upload" {
		return errors.New("unsupported recovery operation")
	}
	if r.State == "Committed" {
		return nil
	}
	if r.State != "CommitSent" && r.State != "Unknown" {
		return errors.New("operation has no submitted commit; retained for explicit recovery")
	}
	if r.ConfirmKey == "" {
		return errors.New("missing upload session identity")
	}
	data, e := s.Data(r)
	if e != nil {
		return e
	}
	data.Close()
	var status smh.UploadStatus
	// SJTU rejects uploadPartInfo generation, but still includes uploaded parts
	// when no_upload_part_info=1. Omitting this flag returns ParamInvalid (400).
	if e = c.JSON(ctx, "GET", "file", r.ConfirmKey, url.Values{"upload": {"1"}, "no_upload_part_info": {"1"}}, nil, &status); e != nil {
		return e
	}
	if !status.Confirmed || strings.Join(status.Path, "/") != r.Path || (r.UploadID != "" && status.UploadID != "" && status.UploadID != r.UploadID) {
		return errors.New("upload is not confirmed at expected path")
	}
	info, e := c.Info(ctx, r.Path)
	if e != nil {
		return e
	}
	if int64(info.Size) != r.Size {
		return errors.New("remote size differs; possible concurrent change")
	}
	reader, e := c.Open(ctx, r.Path, info, 0, -1)
	if e != nil {
		return e
	}
	h := sha256.New()
	n, e := io.Copy(h, reader)
	ce := reader.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return errors.New("remote content differs; local copy preserved")
	}
	// Remote verification can take arbitrarily long. The local source may have
	// changed since the initial check, even with its size and mtime preserved.
	// Revalidate through the spool contract before permitting committed cleanup.
	verified, e := s.Data(r)
	if e != nil {
		return e
	}
	if e = verified.Close(); e != nil {
		return e
	}
	old := r.State
	r.State = "Committed"
	if e = s.Save(r); e != nil {
		r.State = old
		return e
	}
	return nil
}
