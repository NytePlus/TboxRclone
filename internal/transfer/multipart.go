// Package transfer implements durable multipart uploads with explicit intent.
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
)

// PartSize is the 4 MiB part layout observed on the SJTU deployment.
const PartSize int64 = 4 << 20
const workers = 4
const batchSize = 50

func bound(c *smh.Client, r *journal.Record) error {
	if r.Kind != "" && r.Kind != "upload" {
		return errors.New("operation is not an upload")
	}
	if r.Scope != c.Endpoint+"/"+c.Library+"/"+c.Space {
		return errors.New("account or space does not match journal")
	}
	if !strings.HasPrefix(r.Path, "codex-api-lab/") || smh.ValidatePath(r.Path) != nil {
		return errors.New("multipart writes require an isolated lab path")
	}
	if r.Overwrite && (r.OldETag == "" || r.OldSize < 0) {
		return errors.New("overwrite requires recorded previous content identity")
	}
	return nil
}

func strategy(r *journal.Record) string {
	if r.Overwrite {
		return "overwrite"
	}
	return "ask"
}

// Start initializes exactly once, after persisting the spool and intent.
func Start(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := bound(c, r); err != nil {
		return err
	}
	if r.State != "Prepared" || r.Size < 0 {
		return errors.New("multipart requires a prepared upload")
	}
	data, err := s.Data(r)
	if err != nil {
		return err
	}
	defer data.Close()
	count := max(int64(1), (r.Size+PartSize-1)/PartSize)
	if count > 10000 {
		return errors.New("multipart part count exceeds supported limit")
	}
	r.PartSize = PartSize
	r.Parts = nil
	for n := 1; n <= int(count); n++ {
		size := min(PartSize, r.Size-int64(n-1)*PartSize)
		h := sha256.New()
		if _, err = io.Copy(h, io.NewSectionReader(data, int64(n-1)*PartSize, size)); err != nil {
			return err
		}
		r.Parts = append(r.Parts, journal.Part{Number: n, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r.State = "InitSent"
	if err = s.Save(r); err != nil {
		return err
	}
	u, err := c.MultipartStrategy(ctx, r.Path, r.Size, min(int(count), batchSize), strategy(r))
	if err != nil {
		return err
	}
	if u.Key == "" || u.UploadID == "" || u.Path == "" {
		return smh.ErrProtocol
	}
	r.ConfirmKey = u.Key
	r.UploadID = u.UploadID
	r.UploadPath = u.Path
	r.State = "Uploading"
	if err = s.Save(r); err != nil {
		return err
	}
	return uploadAndCommit(ctx, s, c, r, data, &u)
}

// Resume never reinitializes an upload or replays a submitted confirmation.
// A lost part acknowledgement is repaired by sending the same numbered bytes.
func Resume(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record) error {
	if err := bound(c, r); err != nil {
		return err
	}
	if r.State == "CommitSent" || r.State == "Unknown" {
		return recovery.Reconcile(ctx, s, c, r)
	}
	if r.State != "Uploading" || r.UploadID == "" || r.ConfirmKey == "" || r.UploadPath == "" || r.PartSize != PartSize || r.Size < 0 {
		return errors.New("operation is not a resumable multipart upload")
	}
	data, err := s.Data(r)
	if err != nil {
		return err
	}
	defer data.Close()
	count := max(int64(1), (r.Size+PartSize-1)/PartSize)
	if count > 10000 || len(r.Parts) != int(count) {
		return smh.ErrProtocol
	}
	for i, p := range r.Parts {
		size := min(PartSize, r.Size-int64(i)*PartSize)
		h := sha256.New()
		if _, err = io.Copy(h, io.NewSectionReader(data, int64(i)*PartSize, size)); err != nil {
			return err
		}
		if p.Number != i+1 || p.Size != size || p.SHA256 != hex.EncodeToString(h.Sum(nil)) {
			return errors.New("part journal does not match immutable spool")
		}
	}
	status, err := c.UploadState(ctx, r.ConfirmKey)
	if err != nil {
		return err
	}
	if status.UploadID != r.UploadID || strings.Join(status.Path, "/") != r.Path || status.Confirmed {
		return errors.New("upload session identity or publication state changed")
	}
	seen := map[int]bool{}
	for _, p := range status.Parts {
		if p.Number < 1 || p.Number > len(r.Parts) || seen[p.Number] {
			return smh.ErrProtocol
		}
		seen[p.Number] = true
		local := r.Parts[p.Number-1]
		if int64(p.Size) != local.Size || p.ETag == "" || (local.ETag != "" && local.ETag != p.ETag) {
			return errors.New("remote part differs from journal; spool retained")
		}
	}
	for _, p := range r.Parts {
		if p.ETag != "" && !seen[p.Number] {
			return errors.New("acknowledged remote part disappeared")
		}
	}
	return uploadAndCommit(ctx, s, c, r, data, nil)
}

func uploadAndCommit(ctx context.Context, s *journal.Store, c *smh.Client, r *journal.Record, data io.ReaderAt, initial *smh.Upload) error {
	var err error
	for first := 1; first <= len(r.Parts); first += batchSize {
		last := min(first+batchSize-1, len(r.Parts))
		pending := []int{}
		for n := first; n <= last; n++ {
			if r.Parts[n-1].ETag == "" {
				pending = append(pending, n)
			}
		}
		if len(pending) == 0 {
			continue
		}
		var u smh.Upload
		if first == 1 && initial != nil {
			u = *initial
		} else {
			var e error
			u, e = c.Renew(ctx, r.ConfirmKey, first, last)
			if e != nil {
				return e
			}
		}
		if u.Key != r.ConfirmKey || u.UploadID != r.UploadID || u.Path != r.UploadPath {
			return errors.New("renewal changed upload identity")
		}
		for _, n := range pending {
			if len(u.Parts[strconv.Itoa(n)].Headers) == 0 {
				return smh.ErrProtocol
			}
		}
		groupCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error
		jobs := make(chan int)
		for w := 0; w < min(workers, len(pending)); w++ {
			wg.Go(func() {
				for n := range jobs {
					if groupCtx.Err() != nil {
						continue
					}
					part := r.Parts[n-1]
					tag, e := c.PutPart(groupCtx, u, n, io.NewSectionReader(data, int64(n-1)*PartSize, part.Size), part.Size)
					mu.Lock()
					if e == nil {
						r.Parts[n-1].ETag = tag
						e = s.Save(r)
					}
					if e != nil && firstErr == nil {
						firstErr = e
						cancel()
					}
					mu.Unlock()
				}
			})
		}
		for _, n := range pending {
			select {
			case jobs <- n:
			case <-groupCtx.Done():
			}
		}
		close(jobs)
		wg.Wait()
		cancel()
		if firstErr != nil {
			return firstErr
		}
		if err = ctx.Err(); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if r.Overwrite {
		// Defensive change detection under the exclusive-writer contract. This
		// read is not a server CAS and cannot protect against external writers.
		previous, e := c.Info(ctx, r.Path)
		if e != nil {
			return e
		}
		if previous.ETag != r.OldETag || int64(previous.Size) != r.OldSize || previous.Type == "dir" {
			return errors.New("overwrite target changed; old and pending data retained")
		}
	}
	r.State = "CommitSent"
	if err = s.Save(r); err != nil {
		return err
	}
	var confirmed smh.Item
	err = c.JSON(ctx, "POST", "file", r.ConfirmKey, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {strategy(r)}}, struct{}{}, &confirmed)
	if err != nil {
		r.State = "Unknown"
		return errors.Join(err, s.Save(r))
	}
	if strings.Join(confirmed.Path, "/") != r.Path || int64(confirmed.Size) != r.Size {
		return smh.ErrUnknown
	}
	return recovery.Reconcile(ctx, s, c, r)
}
