package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/smh"
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
			if phase == "part" && m.puts[2] != 1 {
				t.Fatal("server-listed part was retransmitted")
			}
		})
	}
}

func TestAutomaticRecoveryRenewsExpiredPartSignature(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	m.expireSignature = true
	waits := 0
	err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
	if err != nil || r.State != "Committed" || waits != 1 || m.init != 1 || m.renew != 1 || m.confirm != 1 {
		t.Fatalf("err=%v state=%s waits=%d init=%d renew=%d confirm=%d", err, r.State, waits, m.init, m.renew, m.confirm)
	}
	total := 0
	for _, puts := range m.puts {
		total += puts
	}
	if total != 4 {
		t.Fatalf("PUT count=%d, want three parts plus only the rejected part", total)
	}
}

func TestAutomaticRecoveryRebuildsExpiredUnconfirmedSession(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	m.dropPart = true
	m.expireSession = true
	waits := 0
	err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
	if err != nil || r.State != "Committed" || waits != 2 || m.init != 2 || m.confirm != 1 {
		t.Fatalf("err=%v state=%s waits=%d init=%d confirm=%d", err, r.State, waits, m.init, m.confirm)
	}
}

func TestExpiredSessionResetKeepsOldDurableRecordUntilReinitialization(t *testing.T) {
	_, c, s, r, _ := fixture(t)
	defer s.Close()
	r.State = "Uploading"
	r.ConfirmKey = "old-key"
	r.UploadID = "old-upload"
	r.UploadPath = "/old-path"
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := resetExpiredSession(c, r); err != nil || r.State != "Prepared" {
		t.Fatalf("err=%v state=%s", err, r.State)
	}
	records, err := s.Records()
	if err != nil || len(records) != 1 {
		t.Fatal(err)
	}
	if records[0].State != "Uploading" || records[0].ConfirmKey != "old-key" || records[0].UploadID != "old-upload" {
		t.Fatalf("durable recovery point changed early: %+v", records[0])
	}
}

func TestAutomaticRecoveryDoesNotRetryControlPlanePermissionFailure(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	m.dropPart = true
	transport := c.HTTP.Transport
	c.HTTP.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Has("upload") {
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
		}
		return transport.RoundTrip(req)
	})
	waits := 0
	err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
	if !smh.IsStatus(err, http.StatusForbidden) || waits != 1 || m.init != 1 || m.renew != 0 || m.confirm != 0 || r.State != "Uploading" {
		t.Fatalf("err=%v waits=%d init=%d renew=%d confirm=%d state=%s", err, waits, m.init, m.renew, m.confirm, r.State)
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
		{"persistent outage", 503, 3}, {"expired signed authorization", 403, 3}, {"rate limited", 429, 3},
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

type interruptedBody struct{ io.ReadCloser }

func (b interruptedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestAutomaticRecoveryInterruptedConfirmBody(t *testing.T) {
	m, c, s, r, _ := fixture(t)
	defer s.Close()
	transport := c.HTTP.Transport
	c.HTTP.Transport = retryTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := transport.RoundTrip(req)
		if err == nil && req.URL.Query().Has("confirm") {
			resp.Body = interruptedBody{resp.Body}
		}
		return resp, err
	})
	waits := 0
	err := startWithRecovery(context.Background(), s, c, r, func(context.Context, time.Duration) error { waits++; return nil })
	if err != nil || waits != 1 || m.init != 1 || m.confirm != 1 || r.State != "Committed" {
		t.Fatalf("err=%v waits=%d init=%d confirm=%d state=%s", err, waits, m.init, m.confirm, r.State)
	}
}
