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
	"unicode/utf8"
)

// ErrUnknown means a mutation may have executed and must not be replayed.
var ErrUnknown = errors.New("remote outcome unknown; preserve local data and reconcile")

// ErrTransport identifies sanitized network failures without exposing signed URLs.
var ErrTransport = errors.New("SMH transport failure")

// ErrSignedUploadAuthorization means an ephemeral data-plane signature was
// rejected. Callers may request fresh signatures for the same immutable part;
// it does not classify control-plane 401/403 responses as retryable.
var ErrSignedUploadAuthorization = errors.New("signed upload authorization rejected")

// ErrProtocol means the response cannot establish the promised result.
var ErrProtocol = errors.New("invalid or unsupported SMH response")

// Error retains status and a bounded server code without leaking credentials,
// URLs, free-form messages, or response bodies.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return fmt.Sprintf("SMH HTTP %d", e.Status) }

// HTTPStatus lets protocol adapters preserve a sanitized upstream status
// without depending on this package or exposing the response body.
func (e *Error) HTTPStatus() int {
	if e.Code == "QuotaLimitReached" {
		return http.StatusInsufficientStorage
	}
	return e.Status
}

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
	if !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\") {
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
	return classifyTransportError(err)
}

// JSON bounds retries of failed read-only transport requests. Mutations and
// explicit HTTP/protocol errors are never automatically retried.
func (c *Client) JSON(ctx context.Context, method, kind, p string, q url.Values, in, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.jsonOnce(ctx, method, kind, p, q, in, out)
		if (method != http.MethodGet && method != http.MethodHead) ||
			!errors.Is(err, ErrTransport) || attempt >= 2 || ctx.Err() != nil {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) jsonOnce(ctx context.Context, method, kind, p string, q url.Values, in, out any) error {
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
		e := &Error{Status: resp.StatusCode, Code: readErrorCode(resp.Body)}
		if mutation && (resp.StatusCode >= 500 || resp.StatusCode == 408) {
			return errors.Join(ErrUnknown, e)
		}
		return e
	}
	if resp.StatusCode == 202 {
		return errors.Join(ErrUnknown, errors.New("asynchronous task requires reconciliation"))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil {
		e := safeTransportError(ctx, err)
		if mutation {
			return errors.Join(ErrUnknown, e)
		}
		return e
	}
	if len(b) > 4<<20 {
		return unknownProtocol(mutation)
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

func readErrorCode(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, 65537))
	if err != nil || len(b) > 65536 {
		return ""
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(b, &envelope) != nil || len(envelope.Code) > 64 {
		return ""
	}
	for _, r := range envelope.Code {
		if r < 'A' || r > 'Z' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return envelope.Code
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

// pageMarker preserves opaque string cursors and exact nonnegative integer cursors.
// The deployed service returns JSON numbers for paginated directory offsets.
type pageMarker string

func (m *pageMarker) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*m = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var value string
		if err := json.Unmarshal(b, &value); err != nil {
			return ErrProtocol
		}
		*m = pageMarker(value)
		return nil
	}
	if len(b) == 0 || !json.Valid(b) {
		return ErrProtocol
	}
	for _, ch := range b {
		if ch < '0' || ch > '9' {
			return ErrProtocol
		}
	}
	*m = pageMarker(string(b))
	return nil
}

// List walks marker pages, failing on repeated names or markers instead of losing entries.
func (c *Client) List(ctx context.Context, p string) ([]Item, error) {
	var result []Item
	seen := map[string]bool{}
	markers := map[string]bool{}
	marker := ""
	for page := 0; page < 100000; page++ {
		var data struct {
			Contents []Item     `json:"contents"`
			Next     pageMarker `json:"nextMarker"`
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
		if markers[string(data.Next)] {
			return nil, errors.New("repeated directory cursor")
		}
		markers[string(data.Next)] = true
		marker = string(data.Next)
	}
	return nil, errors.New("directory page limit exceeded")
}
