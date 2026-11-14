// Upload-benchmark measures direct SMH transfer with durable staging and full
// confirmation/readback. It is a protocol comparison, never Finder acceptance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/defaults"
	"github.com/nyte/TboxRclone/internal/sjtu/journal"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"github.com/nyte/TboxRclone/internal/sjtu/transfer"
	"github.com/nyte/TboxRclone/internal/verifyutil"
)

type counter struct {
	base    http.RoundTripper
	mu      sync.Mutex
	counts  map[string]int
	started time.Time
	timings []requestTiming
}

type requestTiming struct {
	Method            string  `json:"method"`
	Kind              string  `json:"kind"`
	Start             float64 `json:"start_seconds"`
	Headers           float64 `json:"headers_seconds"`
	Total             float64 `json:"total_seconds"`
	NewConnections    int     `json:"new_connections"`
	ReusedConnections int     `json:"reused_connections"`
}

type timedBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *timedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	return err
}

func (c *counter) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	kind := "metadata"
	if strings.HasPrefix(r.URL.Hostname(), "s3pan") {
		kind = "content"
	} else if r.URL.Query().Has("confirm") {
		kind = "confirm"
	} else if r.URL.Query().Has("multipart") || r.URL.Query().Has("upload") {
		kind = "upload-control"
	}
	c.mu.Lock()
	c.counts[r.Method]++
	origin := c.started
	c.mu.Unlock()
	newConnections, reusedConnections := 0, 0
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if info.Reused {
				reusedConnections++
			} else {
				newConnections++
			}
		},
	}))
	response, err := c.base.RoundTrip(r)
	headers := time.Since(started).Seconds()
	done := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timings = append(c.timings, requestTiming{Method: r.Method, Kind: kind, Start: started.Sub(origin).Seconds(), Headers: headers, Total: time.Since(started).Seconds(), NewConnections: newConnections, ReusedConnections: reusedConnections})
	}
	if err != nil {
		done()
		return response, err
	}
	response.Body = &timedBody{ReadCloser: response.Body, done: done}
	return response, nil
}
func run() error {
	source := flag.String("source", "", "existing regular fixture")
	root := flag.String("root", "", "fresh isolated cloud directory")
	state := flag.String("state", "", "fresh local journal directory")
	output := flag.String("output", "", "fresh evidence JSON")
	space := flag.String("space-file", "/secrets/space.json", "space identifiers")
	token := flag.String("token-file", "/secrets/user-token", "user token file")
	flag.Parse()
	if !strings.HasPrefix(*root, "codex-api-lab/") || smh.ValidatePath(*root) != nil || !filepath.IsAbs(*state) || *output == "" {
		return errors.New("fresh isolated root, absolute state and evidence output required")
	}
	if _, err := os.Lstat(*state); !errors.Is(err, os.ErrNotExist) {
		return errors.New("state directory already exists or cannot be checked")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output already exists or cannot be checked")
	}
	st, err := os.Lstat(*source)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > 2<<30 {
		return errors.New("fixture must be a regular file of 1 byte..2 GiB")
	}
	input, err := os.Open(*source)
	if err != nil {
		return err
	}
	defer input.Close()
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(*space)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &ids); err != nil {
		return err
	}
	c, err := smh.New(defaults.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		return err
	}
	if err = c.UseUserToken(*token, "1"); err != nil {
		return err
	}
	meter := &counter{base: http.DefaultTransport, counts: map[string]int{}, started: time.Now()}
	c.HTTP.Transport = meter
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err = c.Info(ctx, *root); !smh.IsStatus(err, 404) {
		return errors.New("cloud root exists or cannot be checked")
	}
	if err = c.JSON(ctx, "PUT", "directory", *root, url.Values{"conflict_resolution_strategy": {"ask"}}, struct{}{}, nil); err != nil {
		return err
	}
	store, err := journal.Open(*state)
	if err != nil {
		return err
	}
	defer store.Close()
	store.WriteRoot = *root
	scope := c.Endpoint + "/" + c.Library + "/" + c.Space
	started := time.Now()
	record, err := store.Prepare(ctx, scope, *root+"/payload.bin", input, st.Size(), 2<<30)
	staged := time.Now()
	if err != nil {
		return err
	}
	meter.mu.Lock()
	meter.counts = map[string]int{}
	meter.timings = nil
	meter.started = staged
	meter.mu.Unlock()
	err = transfer.StartWithRecovery(ctx, store, c, record)
	finished := time.Now()
	report := map[string]any{"scope": "direct SMH transfer engine, no rclone/WebDAV; includes confirm and independent full readback", "source": *source, "remote_path": record.Path, "bytes": record.Size, "sha256": record.SHA256, "operation_id": record.ID, "state": record.State, "started_at": started.UTC().Format(time.RFC3339Nano), "staging_seconds": staged.Sub(started).Seconds(), "transfer_and_verification_seconds": finished.Sub(staged).Seconds(), "total_seconds": finished.Sub(started).Seconds(), "bytes_per_second": float64(record.Size) / finished.Sub(started).Seconds(), "request_counts": meter.counts, "pass": err == nil && record.State == "Committed", "source_retained": true}
	report["request_timings"] = meter.timings
	if err != nil {
		report["error"] = err.Error()
	}
	encoded, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e = verifyutil.WriteEvidence(*output, append(encoded, '\n')); e != nil {
		return e
	}
	fmt.Println(string(encoded))
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
