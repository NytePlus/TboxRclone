// Package smh implements the SJTU SMH control plane without replaying mutations.
package smh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrUnknown means a mutation may have executed and must not be replayed.
var ErrUnknown = errors.New("remote outcome unknown; preserve local data and reconcile")

// ErrTransport identifies sanitized network failures without exposing signed URLs.
var ErrTransport = errors.New("SMH transport failure")

// ErrProtocol means the response cannot establish the promised result.
var ErrProtocol = errors.New("invalid or unsupported SMH response")

// Error retains status without leaking credentials, URLs, or response bodies.
type Error struct{ Status int }

func (e *Error) Error() string { return fmt.Sprintf("SMH HTTP %d", e.Status) }

// IsStatus matches a sanitized HTTP error.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// Int64 decodes integer numbers and decimal strings without float conversion.
type Int64 int64

func (n *Int64) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return ErrProtocol
	}
	*n = Int64(v)
	return nil
}

// Item is a file or directory; ETag is an opaque validator, not an MD5 hash.
type Item struct {
	Name     string    `json:"name"`
	Path     []string  `json:"path"`
	Type     string    `json:"type"`
	Size     Int64     `json:"size"`
	ETag     string    `json:"eTag"`
	CAS      string    `json:"contentCas"`
	Inode    string    `json:"inode"`
	Modified time.Time `json:"modificationTime"`
}

// Upload contains short-lived data-plane credentials; never persist or log it.
type Upload struct {
	Domain   string                   `json:"domain"`
	Path     string                   `json:"path"`
	Headers  map[string]string        `json:"headers"`
	Key      string                   `json:"confirmKey"`
	UploadID string                   `json:"uploadId"`
	Parts    map[string]PartSignature `json:"parts"`
}

type PartSignature struct {
	Headers map[string]string `json:"headers"`
}

type UploadedPart struct {
	Number int    `json:"PartNumber"`
	Size   Int64  `json:"Size"`
	ETag   string `json:"ETag"`
}

// UploadStatus identifies the final path after confirmation.
type UploadStatus struct {
	Confirmed bool           `json:"confirmed"`
	UploadID  string         `json:"uploadId"`
	Parts     []UploadedPart `json:"parts"`
	Path      []string       `json:"path"`
}

// Client is bound to a single library and space. Token is read for every request.
type Client struct {
	Endpoint, Library, Space string
	Token                    func(context.Context) (string, error)
	HTTP                     *http.Client
	invalidateToken          func(string)
}

// New constructs a client with bounded requests and no automatic redirects.
func New(endpoint, library, space, tokenFile string) (*Client, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("endpoint must be an HTTPS origin")
	}
	if library == "" || space == "" || strings.ContainsAny(library+space, "/\\?#") {
		return nil, errors.New("library_id and space_id are required identifiers")
	}
	return &Client{Endpoint: strings.TrimRight(endpoint, "/"), Library: library, Space: space,
		HTTP: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Token: func(ctx context.Context) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			b, err := os.ReadFile(tokenFile)
			if err != nil {
				return "", errors.New("cannot read access token file")
			}
			s := strings.TrimSpace(string(b))
			if s == "" {
				return "", errors.New("empty access token file")
			}
			return s, nil
		}}, nil
}

// ValidatePath refuses traversal and ambiguous separators, preserving Unicode exactly.
func ValidatePath(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsAny(p, "\x00\\") {
		return errors.New("invalid path")
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." {
			return errors.New("invalid path segment")
		}
	}
	return nil
}
func escape(p string) string {
	a := strings.Split(p, "/")
	for i := range a {
		a[i] = url.PathEscape(a[i])
	}
	return strings.Join(a, "/")
}
func (c *Client) address(ctx context.Context, kind, p string, q url.Values) (string, error) {
	if err := ValidatePath(p); err != nil {
		return "", err
	}
	token, err := c.Token(ctx)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	for k, v := range q {
		values[k] = append([]string(nil), v...)
	}
	values.Set("access_token", token)
	return c.Endpoint + "/api/v1/" + kind + "/" + escape(c.Library) + "/" + escape(c.Space) + "/" + escape(p) + "?" + values.Encode(), nil
}
func safeTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var u *url.Error
	if errors.As(err, &u) {
		if u.Timeout() {
			return fmt.Errorf("SMH transport timeout: %w", ErrTransport)
		}
		return ErrTransport
	}
	return ErrTransport
}

// JSON issues one control-plane request. Mutations are never automatically retried.
func (c *Client) JSON(ctx context.Context, method, kind, p string, q url.Values, in, out any) error {
	address, err := c.address(ctx, kind, p, q)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return errors.New("invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	// GetBody would permit net/http to replay a request after a connection failure.
	req.GetBody = nil
	mutation := method != "GET" && method != "HEAD"
	resp, err := c.HTTP.Do(req)
	if err != nil {
		e := safeTransportError(ctx, err)
		if mutation {
			return errors.Join(ErrUnknown, e)
		}
		return e
	}
	defer resp.Body.Close()
	c.rejectExpiredToken(req, resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &Error{resp.StatusCode}
		if mutation && (resp.StatusCode >= 500 || resp.StatusCode == 408) {
			return errors.Join(ErrUnknown, e)
		}
		return e
	}
	if resp.StatusCode == 202 {
		return errors.Join(ErrUnknown, errors.New("asynchronous task requires reconciliation"))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(b) > 4<<20 {
		if mutation {
			return errors.Join(ErrUnknown, ErrProtocol)
		}
		return ErrProtocol
	}
	if len(b) == 0 {
		if out != nil {
			if mutation {
				return errors.Join(ErrUnknown, ErrProtocol)
			}
			return ErrProtocol
		}
		return nil
	}
	var envelope struct {
		Code   json.RawMessage `json:"code"`
		Task   json.RawMessage `json:"taskId"`
		Status json.RawMessage `json:"status"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return unknownProtocol(mutation)
	}
	if len(envelope.Task) > 0 && string(envelope.Task) != "null" {
		return errors.Join(ErrUnknown, errors.New("asynchronous task not complete"))
	}
	for _, v := range []json.RawMessage{envelope.Code, envelope.Status} {
		if len(v) > 0 && string(v) != "0" && string(v) != "\"0\"" && string(v) != "null" {
			return unknownProtocol(mutation)
		}
	}
	if out != nil && json.Unmarshal(b, out) != nil {
		return unknownProtocol(mutation)
	}
	return nil
}
func unknownProtocol(mutation bool) error {
	if mutation {
		return errors.Join(ErrUnknown, ErrProtocol)
	}
	return ErrProtocol
}

// Info returns file or directory metadata.
func (c *Client) Info(ctx context.Context, p string) (Item, error) {
	var i Item
	err := c.JSON(ctx, "GET", "directory", p, url.Values{"info": {"1"}, "with_content_cas": {"1"}, "with_inode": {"1"}}, nil, &i)
	if err == nil && i.Type == "" {
		err = ErrProtocol
	}
	return i, err
}

// List walks marker pages, failing on repeated names or markers instead of losing entries.
func (c *Client) List(ctx context.Context, p string) ([]Item, error) {
	var result []Item
	seen := map[string]bool{}
	markers := map[string]bool{}
	marker := ""
	for page := 0; page < 100000; page++ {
		var data struct {
			Contents []Item `json:"contents"`
			Next     string `json:"nextMarker"`
		}
		err := c.JSON(ctx, "GET", "directory", p, url.Values{"limit": {"1000"}, "marker": {marker}, "with_content_cas": {"1"}, "with_inode": {"1"}}, nil, &data)
		if err != nil {
			return nil, err
		}
		if data.Contents == nil {
			return nil, ErrProtocol
		}
		for _, i := range data.Contents {
			if i.Name == "" || strings.Contains(i.Name, "/") || ValidatePath(i.Name) != nil || seen[i.Name] || i.Type == "" {
				return nil, errors.New("directory changed or returned invalid entries; restart listing")
			}
			seen[i.Name] = true
			result = append(result, i)
		}
		if data.Next == "" {
			return result, nil
		}
		if markers[data.Next] {
			return nil, errors.New("repeated directory cursor")
		}
		markers[data.Next] = true
		marker = data.Next
	}
	return nil, errors.New("directory page limit exceeded")
}

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
			return nil, &Error{416}
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
		return fail(&Error{resp.StatusCode})
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
		return "", &Error{resp.StatusCode}
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
