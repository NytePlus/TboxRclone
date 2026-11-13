package smh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestTransportClassificationDoesNotRetainSecrets(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"timeout", context.DeadlineExceeded},
		{"dns", &net.DNSError{Name: "SECRET", Server: "SECRET", Err: "SECRET"}},
		{"tls", &tls.CertificateVerificationError{Err: errors.New("SECRET")}},
		{"connection refused", syscall.ECONNREFUSED},
		{"connection reset", syscall.ECONNRESET},
		{"network unreachable", syscall.ENETUNREACH},
		{"host unreachable", syscall.EHOSTUNREACH},
		{"unexpected EOF", io.ErrUnexpectedEOF},
		{"EOF", io.EOF},
		{"failure", errors.New("SECRET")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, wrapped := range []bool{false, true} {
				err := tc.err
				if wrapped {
					err = &url.Error{Op: "SECRET", URL: "https://SECRET/?access_token=SECRET", Err: fmt.Errorf("SECRET: %w", err)}
				}
				got := safeTransportError(context.Background(), err)
				if !errors.Is(got, ErrTransport) || !strings.Contains(got.Error(), "SMH transport "+tc.name) {
					t.Fatalf("classification %q: %v", tc.name, got)
				}
				for e := got; e != nil; e = errors.Unwrap(e) {
					if strings.Contains(fmt.Sprintf("%+v %#v", e, e), "SECRET") {
						t.Fatal("sanitized error retains secret-bearing cause")
					}
				}
			}
		})
	}
}

func TestTransportCancellationKeepsContextIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := safeTransportError(ctx, errors.New("SECRET")); got != context.Canceled {
		t.Fatalf("cancellation changed: %v", got)
	}
}

type failingTransport struct{ calls int }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, &net.DNSError{Name: "SECRET", Err: "SECRET"}
}

func TestClassifiedMutationFailureStillRequiresReconcile(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "POST", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			transport := &failingTransport{}
			c := &Client{Endpoint: "https://example.invalid", Library: "lib", Space: "space", Token: func(context.Context) (string, error) { return "SECRET", nil }, HTTP: &http.Client{Transport: transport}}
			err := c.JSON(context.Background(), method, "file", "test", nil, nil, nil)
			if transport.calls != 1 || !errors.Is(err, ErrTransport) || errors.Is(err, ErrUnknown) != (method != "GET") || !strings.Contains(err.Error(), "transport dns") || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("calls=%d err=%v", transport.calls, err)
			}
		})
	}
}
