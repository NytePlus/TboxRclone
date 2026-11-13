// Package verifyutil shares acceptance-test mechanics, not acceptance decisions.
package verifyutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Validate requires an HTTP loopback origin and a clean fixture base.
func Validate(raw, base string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || base == "" || path.Clean(base) != base || strings.Contains(base, "..") || strings.Contains(base, "\\") {
		return nil, errors.New("unsafe origin or base")
	}
	return u, nil
}

// FreshCollection rejects any fixture not newly created by this request.
func FreshCollection(client *http.Client, origin *url.URL, base string) error {
	parent := *origin
	parent.Path = strings.TrimRight(origin.Path, "/") + "/" + base + "/"
	req, _ := http.NewRequest("MKCOL", parent.String(), nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("fixture base is not fresh: MKCOL returned %d", resp.StatusCode)
	}
	return nil
}

// WaitSettled polls until a resource is absent or published. The caller decides success.
func WaitSettled(client *http.Client, origin *url.URL, remote string) (int, bool, error) {
	target := *origin
	target.Path = strings.TrimRight(origin.Path, "/") + "/" + remote
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	last := 0
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
		resp, err := client.Do(req)
		if err == nil {
			last = resp.StatusCode
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if last == http.StatusNotFound {
				return last, false, nil
			}
			if last >= 200 && last < 300 {
				return last, true, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, false, fmt.Errorf("HEAD did not settle: last=%d", last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// WriteJSON emits an indented JSON report with a trailing newline.
func WriteJSON(dst io.Writer, value any) error {
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// LabOrigin also requires explicit authorization for fixture writes.
func LabOrigin(raw, base string, allowWrites bool) (*url.URL, error) {
	u, err := Validate(raw, base)
	if err != nil || !allowWrites {
		return nil, errors.New("a loopback origin, clean -base, and -allow-lab-writes are required")
	}
	return u, nil
}
