package smh

import (
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Multipart starts the deployed SJTU part-map protocol, not the newer SDK protocol.
func (c *Client) Multipart(ctx context.Context, path string, size int64, last int) (Upload, error) {
	return c.MultipartStrategy(ctx, path, size, last, "ask")
}

// MultipartStrategy only permits explicit create or exclusive-writer overwrite.
func (c *Client) MultipartStrategy(ctx context.Context, path string, size int64, last int, strategy string) (Upload, error) {
	var u Upload
	if size < 0 || last < 1 || last > 50 || (strategy != "ask" && strategy != "overwrite") {
		return u, ErrProtocol
	}
	err := c.JSON(ctx, "POST", "file", path, url.Values{"multipart": {"1"}, "filesize": {strconv.FormatInt(size, 10)}, "conflict_resolution_strategy": {strategy}}, map[string]any{"partNumberRange": partNumbers(1, last)}, &u)
	return u, err
}

// Renew requests ephemeral signatures for an existing session; it never creates one.
func (c *Client) Renew(ctx context.Context, key string, first, last int) (Upload, error) {
	var u Upload
	if first < 1 || last < first || last > 10000 || last-first >= 50 {
		return u, ErrProtocol
	}
	err := c.JSON(ctx, "POST", "file", key, url.Values{"renew": {"1"}}, map[string]any{"partNumberRange": partNumbers(first, last)}, &u)
	return u, err
}

func (c *Client) UploadState(ctx context.Context, key string) (UploadStatus, error) {
	var status UploadStatus
	err := c.JSON(ctx, "GET", "file", key, url.Values{"upload": {"1"}, "no_upload_part_info": {"1"}}, nil, &status)
	return status, err
}

// PutPart replaces only the numbered part in the existing unpublished upload.
// The caller must use identical immutable bytes when retrying this operation.
func (c *Client) PutPart(ctx context.Context, u Upload, number int, r io.Reader, size int64) (string, error) {
	signature, ok := u.Parts[strconv.Itoa(number)]
	if !ok || len(signature.Headers) == 0 || u.UploadID == "" || number < 1 {
		return "", ErrProtocol
	}
	path, err := url.Parse(u.Path)
	if err != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" || path.RawQuery != "" || !strings.HasPrefix(u.Path, "/") {
		return "", ErrProtocol
	}
	path.RawQuery = url.Values{"uploadId": {u.UploadID}, "partNumber": {strconv.Itoa(number)}}.Encode()
	u.Path = path.String()
	u.Headers = signature.Headers
	etag, err := c.putData(ctx, u, r, size)
	if err == nil && (etag == "" || strings.HasPrefix(etag, "W/")) {
		return "", ErrProtocol
	}
	return etag, err
}

func partNumbers(first, last int) []int {
	numbers := make([]int, 0, last-first+1)
	for n := first; n <= last; n++ {
		numbers = append(numbers, n)
	}
	return numbers
}
