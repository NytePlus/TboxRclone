package faultproxy

import (
	"context"
	"io"
	"time"
)

func waitDelay(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// pacedReader limits each matched request, not aggregate proxy throughput.
// It caps read chunks at 32KiB and charges elapsed wall time from the first
// read, including upstream backpressure, without buffering the entire upload.
type pacedReader struct {
	io.ReadCloser
	ctx     context.Context
	rate    int64
	started time.Time
	sent    int64
}

func (r *pacedReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.started.IsZero() {
		r.started = time.Now()
	}
	if len(b) > 32<<10 {
		b = b[:32<<10]
	}
	n, err := r.ReadCloser.Read(b)
	r.sent += int64(n)
	due := time.Duration(float64(r.sent) / float64(r.rate) * float64(time.Second))
	if waitErr := waitDelay(r.ctx, due-time.Since(r.started)); waitErr != nil {
		return 0, waitErr
	}
	return n, err
}
