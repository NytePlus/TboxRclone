package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAutomaticRecoveryLostAcknowledgement(t *testing.T) {
	for _, phase := range []string{"part", "confirm"} {
		t.Run(phase, func(t *testing.T) {
			m, c, s, r, _ := fixture(t)
			defer s.Close()
			m.dropPart = phase == "part"
			m.dropConfirm = phase == "confirm"
			waits := 0
			err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if r.State != "Committed" || m.init != 1 || m.confirm != 1 || waits != 1 {
				t.Fatalf("state=%s init=%d confirm=%d waits=%d", r.State, m.init, m.confirm, waits)
			}
			if phase == "part" && m.puts[2] != 2 {
				t.Fatal("lost part not repaired in original session")
			}
		})
	}
}

type retryTransport func(*http.Request) (*http.Response, error)

func (f retryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAutomaticRecoveryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		wantWaits int
	}{
		{"persistent outage", 503, 3}, {"permission denied", 403, 0}, {"rate limited", 429, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, s, r, _ := fixture(t)
			defer s.Close()
			transport := c.HTTP.Transport
			c.HTTP.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/data" && req.Method == "PUT" {
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
				}
				return transport.RoundTrip(req)
			})
			waits := 0
			err := startWithRecovery(context.Background(), s, c, r, func(ctx context.Context, d time.Duration) error {
				if d != time.Second<<waits {
					t.Errorf("unexpected backoff %s", d)
				}
				waits++
				return nil
			})
			if err == nil || waits != tc.wantWaits || m.init != 1 || m.confirm != 0 || r.State != "Uploading" {
				t.Fatalf("err=%v waits=%d init=%d confirm=%d state=%s", err, waits, m.init, m.confirm, r.State)
			}
			f, err := s.Data(r)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		})
	}
	t.Run("initialization response lost", func(t *testing.T) {
		m, c, s, r, _ := fixture(t)
		defer s.Close()
		transport := c.HTTP.Transport
		c.HTTP.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
			resp, err := transport.RoundTrip(req)
			if req.URL.Query().Has("multipart") && err == nil {
				resp.Body.Close()
				return nil, errors.New("injected response loss")
			}
			return resp, err
		})
		waits := 0
		err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
		if err == nil || waits != 0 || m.init != 1 || m.confirm != 0 || r.State != "InitSent" {
			t.Fatalf("err=%v waits=%d init=%d state=%s", err, waits, m.init, r.State)
		}
	})
	t.Run("cancel during backoff", func(t *testing.T) {
		m, c, s, r, _ := fixture(t)
		defer s.Close()
		m.dropPart = true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := startWithRecovery(ctx, s, c, r, func(context.Context, time.Duration) error { cancel(); return ctx.Err() })
		if !errors.Is(err, context.Canceled) || m.renew != 0 || m.confirm != 0 {
			t.Fatalf("err=%v renew=%d confirm=%d", err, m.renew, m.confirm)
		}
	})
}
