package faultproxy

import (
	"bufio"
	"io"
	"net"
	"strings"
)

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func httpsAuthority(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), "443")
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

type cutReader struct {
	io.ReadCloser
	left int64
}

func (r *cutReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if r.left == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(b)) > r.left {
		b = b[:r.left]
	}
	n, e := r.ReadCloser.Read(b)
	r.left -= int64(n)
	return n, e
}
