package smh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Open verifies Range and the captured ETag before exposing any bytes.
func (c *Client) Open(ctx context.Context, p string, item Item, start, length int64) (io.ReadCloser, error) {
	if start < 0 || length < -1 {
		return nil, ErrProtocol
	}
	address, err := c.address(ctx, "file", p, nil)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, ErrProtocol
	}
	if item.ETag == "" {
		return nil, errors.New("missing content validator; cannot guarantee stable read")
	}
	etag := item.ETag
	if strings.HasPrefix(etag, "W/") {
		return nil, ErrProtocol
	}
	if !strings.HasPrefix(etag, "\"") {
		etag = strconv.Quote(etag)
	}
	req.Header.Set("If-Match", etag)
	ranged := start != 0 || length >= 0
	end := int64(item.Size) - 1
	if length >= 0 && length < int64(item.Size)-start {
		end = start + length - 1
	}
	if ranged {
		if start >= int64(item.Size) || length == 0 {
			return nil, &Error{Status: 416}
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, safeTransportError(ctx, err)
	}
	c.rejectExpiredToken(req, resp)
	// Only content reads follow signed HTTPS redirects. Never forward cookies,
	// authorization, Referer, or control-plane query parameters to that host.
	for redirects := 0; resp.StatusCode >= 300 && resp.StatusCode <= 399; redirects++ {
		location, locationErr := resp.Location()
		resp.Body.Close()
		if redirects >= 4 || locationErr != nil || location.Scheme != "https" || location.User != nil || location.Fragment != "" {
			return nil, errors.New("invalid or excessive content redirect")
		}
		next, requestErr := http.NewRequestWithContext(ctx, "GET", location.String(), nil)
		if requestErr != nil {
			return nil, ErrProtocol
		}
		next.Header.Set("If-Match", req.Header.Get("If-Match"))
		if ranged {
			next.Header.Set("Range", req.Header.Get("Range"))
		}
		resp, err = c.HTTP.Do(next)
		if err != nil {
			return nil, safeTransportError(ctx, err)
		}
	}
	fail := func(e error) (io.ReadCloser, error) { resp.Body.Close(); return nil, e }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(&Error{Status: resp.StatusCode})
	}
	if strings.Trim(resp.Header.Get("ETag"), "\"") != strings.Trim(item.ETag, "\"") {
		return fail(errors.New("content validator changed or missing"))
	}
	expected := int64(item.Size)
	if ranged {
		expected = end - start + 1
		if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, item.Size) {
			return fail(errors.New("incorrect range response"))
		}
	} else if resp.StatusCode != 200 {
		return fail(ErrProtocol)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expected {
		return fail(errors.New("incorrect content length"))
	}
	return &checkedReader{ReadCloser: resp.Body, left: expected}, nil
}

type checkedReader struct {
	io.ReadCloser
	left int64
	done bool
}

func (r *checkedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.done {
		return 0, io.EOF
	}
	if r.left == 0 {
		var b [1]byte
		n, e := r.ReadCloser.Read(b[:])
		if n > 0 {
			r.done = true
			return 0, errors.New("excess response bytes")
		}
		if e == io.EOF {
			r.done = true
		}
		return 0, e
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, e := r.ReadCloser.Read(p)
	r.left -= int64(n)
	if e == io.EOF && r.left > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, e
}

// PutData streams a spool file to the signed endpoint without sending the SMH token.
func (c *Client) PutData(ctx context.Context, u Upload, r io.Reader, size int64) error {
	_, err := c.putData(ctx, u, r, size)
	return err
}

func (c *Client) putData(ctx context.Context, u Upload, r io.Reader, size int64) (string, error) {
	endpoint := u.Domain
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "", errors.New("invalid signed upload endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint+u.Path, r)
	if err != nil {
		return "", ErrProtocol
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	req.GetBody = nil
	for k, v := range u.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", safeTransportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		e := &Error{Status: resp.StatusCode}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", errors.Join(ErrSignedUploadAuthorization, e)
		}
		return "", e
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
	return resp.Header.Get("ETag"), err
}

// Reauthentication is deferred to the next caller; never replay the failed request.
func (c *Client) rejectExpiredToken(req *http.Request, resp *http.Response) {
	if (resp.StatusCode == 401 || resp.StatusCode == 403) && c.invalidateToken != nil {
		c.invalidateToken(req.URL.Query().Get("access_token"))
	}
}
