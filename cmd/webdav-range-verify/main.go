// Webdav-range-verify checks that byte-range reads all belong to one fixed WebDAV version.
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/nyte/TboxRclone/internal/verifyutil"
)

type rangeResult struct {
	Start        int64  `json:"start"`
	End          int64  `json:"end"`
	HTTPStatus   int    `json:"http_status"`
	ContentRange string `json:"content_range"`
	SHA256       string `json:"sha256"`
	Pass         bool   `json:"pass"`
}

type report struct {
	At             string        `json:"at"`
	URL            string        `json:"url"`
	ETag           string        `json:"etag"`
	Size           int64         `json:"size"`
	FullSHA256     string        `json:"full_sha256"`
	ExpectedSHA256 string        `json:"expected_sha256"`
	Ranges         []rangeResult `json:"ranges"`
	Pass           bool          `json:"pass"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	origin := flag.String("url", "http://127.0.0.1:8686/", "loopback WebDAV origin")
	filePath := flag.String("path", "", "remote path below the WebDAV origin")
	wantHash := flag.String("sha256", "", "expected full-file SHA-256")
	flag.Parse()
	u, err := validatedURL(*origin, *filePath)
	if err != nil {
		return err
	}
	if len(*wantHash) != 64 {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	if _, err = hex.DecodeString(*wantHash); err != nil {
		return errors.New("sha256 is not hexadecimal")
	}
	client := &http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(u.String())
	if err != nil {
		return err
	}
	full, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("full GET failed: status=%d read=%v close=%v", resp.StatusCode, readErr, closeErr)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return errors.New("full GET omitted ETag")
	}
	fullHash := verifyutil.SHA256(full)
	out := report{At: time.Now().UTC().Format(time.RFC3339), URL: u.String(), ETag: etag, Size: int64(len(full)), FullSHA256: fullHash, ExpectedSHA256: strings.ToLower(*wantHash)}
	if len(full) == 0 {
		return errors.New("range fixture must not be empty")
	}
	for _, span := range spans(int64(len(full))) {
		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", span[0], span[1]))
		req.Header.Set("If-Match", etag)
		response, requestErr := client.Do(req)
		item := rangeResult{Start: span[0], End: span[1]}
		if requestErr == nil {
			item.HTTPStatus = response.StatusCode
			item.ContentRange = response.Header.Get("Content-Range")
			var got []byte
			got, requestErr = io.ReadAll(response.Body)
			response.Body.Close()
			item.SHA256 = verifyutil.SHA256(got)
			want := full[span[0] : span[1]+1]
			item.Pass = response.StatusCode == http.StatusPartialContent &&
				item.ContentRange == fmt.Sprintf("bytes %d-%d/%d", span[0], span[1], len(full)) &&
				response.Header.Get("ETag") == etag && bytes.Equal(got, want)
		}
		out.Ranges = append(out.Ranges, item)
		if requestErr != nil {
			return requestErr
		}
	}
	out.Pass = strings.EqualFold(fullHash, *wantHash)
	for _, item := range out.Ranges {
		out.Pass = out.Pass && item.Pass
	}
	if err := verifyutil.WriteJSON(os.Stdout, out); err != nil {
		return err
	}
	if !out.Pass {
		return errors.New("fixed-version range verification failed")
	}
	return nil
}

func validatedURL(origin, filePath string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("url must be a plain loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("only loopback origins are accepted")
	}
	clean := path.Clean("/" + filePath)
	if clean == "/" || strings.Contains(filePath, "\\") || strings.Contains(filePath, "..") {
		return nil, errors.New("path must name one remote file without traversal")
	}
	u.Path = strings.TrimRight(u.Path, "/") + clean
	return u, nil
}

func spans(size int64) [][2]int64 {
	width := int64(4096)
	if size < width {
		width = size
	}
	last := size - width
	middle := (size - width) / 2
	return [][2]int64{{0, width - 1}, {middle, middle + width - 1}, {last, size - 1}}
}
