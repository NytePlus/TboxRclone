// read-change-verify exercises pinned reads across an isolated external replacement.
// It creates a fresh fixture, replaces only that fixture, and never deletes it.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/smh"
)

type readResult struct {
	Status int    `json:"status"`
	ETag   string `json:"etag"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Error  string `json:"error,omitempty"`
}

func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", "", "isolated existing cloud root served by WebDAV")
	name := flag.String("name", "", "fresh fixture basename")
	origin := flag.String("origin", "http://127.0.0.1:8686", "local test WebDAV origin")
	flag.Parse()
	u, err := url.Parse(*origin)
	if err != nil || u.Scheme != "http" || (u.Host != "127.0.0.1:8686" && u.Host != "host.docker.internal:8686") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("requires local isolated WebDAV origin")
	}
	if !strings.HasPrefix(*root, "codex-api-lab/") || smh.ValidatePath(*root) != nil || *name == "" || strings.ContainsAny(*name, "/\\?#%") || smh.ValidatePath(*name) != nil {
		return errors.New("requires isolated root and fresh plain basename")
	}
	httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()
	address := *origin + "/" + url.PathEscape(*name)
	health, _ := http.NewRequest("OPTIONS", *origin+"/", nil)
	response, err := httpClient.Do(health)
	if err != nil {
		return errors.New("WebDAV unreachable; no fixture created")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("WebDAV not healthy; no fixture created")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile("/secrets/space.json")
	if err != nil {
		return errors.New("private identity unavailable")
	}
	if err = json.Unmarshal(b, &ids); err != nil {
		return errors.New("invalid private identity")
	}
	c, err := smh.New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if err != nil {
		return err
	}
	if err = c.UseUserToken("/secrets/user-token", "1"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	remote := *root + "/" + *name
	if _, err = c.Info(ctx, remote); !smh.IsStatus(err, 404) {
		return errors.New("fixture must not exist")
	}
	a, b := bytes.Repeat([]byte("version-A-read-fixture\n"), 1024), bytes.Repeat([]byte("version-B-read-fixture\n"), 1024)
	report := map[string]any{"scope": "protocol supplement; native Finder acceptance required", "remote": remote, "source_a_sha256": hash(a), "source_b_sha256": hash(b), "size": len(a), "pass": false}
	defer func() { json.NewEncoder(os.Stdout).Encode(report) }()
	// Prepare through WebDAV so its directory cache knows this new fixture.
	// Only the replacement below is an out-of-band external event.
	setupRequest, _ := http.NewRequestWithContext(ctx, "PUT", address, bytes.NewReader(a))
	setupResponse, err := httpClient.Do(setupRequest)
	if err != nil {
		return errors.New("fixture WebDAV setup failed")
	}
	io.Copy(io.Discard, io.LimitReader(setupResponse.Body, 4096))
	setupResponse.Body.Close()
	report["setup_status"] = setupResponse.StatusCode
	if setupResponse.StatusCode != 201 {
		return errors.New("fixture setup was not a fresh create")
	}
	old, err := c.Info(ctx, remote)
	if err != nil {
		return err
	}
	if err = verifyCloud(ctx, c, remote, old, a); err != nil {
		return err
	}
	first, data := read(ctx, httpClient, address, "", "")
	report["initial_full_read"] = first
	if first.Error != "" || first.Status != 200 || first.ETag == "" || !bytes.Equal(data, a) {
		return errors.New("WebDAV origin does not expose fixture A")
	}
	prefix, data := read(ctx, httpClient, address, "bytes=0-1023", first.ETag)
	report["first_range"] = prefix
	if prefix.Error != "" || prefix.Status != 206 || !bytes.Equal(data, a[:1024]) {
		return errors.New("initial pinned range failed")
	}
	// Explicit external event: replace only the newly created fixture through
	// the independent cloud API, preserving size to test identity, not length.
	current, err := c.Info(ctx, remote)
	if err != nil || current.ETag != old.ETag {
		return errors.New("fixture changed before controlled replacement")
	}
	if err = publish(ctx, c, remote, b, "overwrite"); err != nil {
		return err
	}
	changed, err := c.Info(ctx, remote)
	if err != nil {
		return err
	}
	if changed.ETag == old.ETag {
		return errors.New("replacement did not change content identity")
	}
	if err = verifyCloud(ctx, c, remote, changed, b); err != nil {
		return err
	}
	report["cloud_replacement_verified"] = true
	reader, pinnedErr := c.Open(ctx, remote, old, 1024, 1024)
	if reader != nil {
		reader.Close()
		return errors.New("stale cloud identity exposed a reader")
	}
	if pinnedErr == nil {
		return errors.New("stale cloud identity was not rejected")
	}
	report["cloud_stale_identity_rejected"] = pinnedErr.Error()
	second, data := read(ctx, httpClient, address, "bytes=1024-2047", first.ETag)
	report["second_range_after_replacement"] = second
	// Either the captured old version or an explicit failure is safe. Never
	// combine a prefix from A with bytes from B under the old validator.
	safe := second.Status >= 400 ||
		(second.Error != "" && bytes.HasPrefix(a[1024:2048], data)) ||
		(second.Status == 206 && second.ETag == first.ETag && bytes.Equal(data, a[1024:2048]))
	if !safe {
		return errors.New("pinned read mixed versions")
	}
	last, data := read(ctx, httpClient, address, "", "")
	report["reopen"] = last
	if last.Error != "" || last.Status != 200 || !bytes.Equal(data, b) {
		return errors.New("reopen did not return complete replacement")
	}
	report["pass"] = true
	return nil
}

func publish(ctx context.Context, c *smh.Client, p string, data []byte, strategy string) error {
	u, err := c.MultipartStrategy(ctx, p, int64(len(data)), 1, strategy)
	if err != nil {
		return err
	}
	if _, err = c.PutPart(ctx, u, 1, bytes.NewReader(data), int64(len(data))); err != nil {
		return err
	}
	return c.JSON(ctx, "POST", "file", u.Key, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {strategy}}, struct{}{}, nil)
}

func verifyCloud(ctx context.Context, c *smh.Client, p string, item smh.Item, want []byte) error {
	r, err := c.Open(ctx, p, item, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, int64(len(want)+1)))
	if err != nil || !bytes.Equal(b, want) {
		return errors.New("independent cloud bytes differ")
	}
	return nil
}

func read(ctx context.Context, client *http.Client, address, span, etag string) (readResult, []byte) {
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	if span != "" {
		req.Header.Set("Range", span)
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return readResult{Error: "transport failure"}, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	r := readResult{Status: resp.StatusCode, ETag: resp.Header.Get("ETag"), Bytes: len(body), SHA256: hash(body)}
	if err != nil {
		r.Error = err.Error()
	}
	if resp.ContentLength >= 0 && int64(len(body)) != resp.ContentLength {
		r.Error = "response length mismatch"
	}
	return r, body
}
