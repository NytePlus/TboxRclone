package faultproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowNetworkPreservesBodiesAndRepeatsWithoutReplay(t *testing.T) {
	payload := strings.Repeat("x", 128<<10)
	for _, action := range []string{"delay_request", "throttle_upload"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != payload || r.Header.Get("X-Fixture") != "preserved" {
					t.Error("request altered by slow-network fixture")
				}
				w.Header().Set("ETag", `"same"`)
				io.WriteString(w, "receipt")
			}))
			defer origin.Close()
			p, client := setup(t, origin)
			rule := Rule{ID: action, Host: strings.TrimPrefix(origin.URL, "https://"), Method: "PUT", PathPrefix: "/", Nth: 2, Repeat: true, Action: action}
			minimum := 80 * time.Millisecond
			phase := "delay_finished"
			if action == "delay_request" {
				rule.DelayMS = 100
			} else {
				rule.BytesPerSecond = 1 << 20
				minimum = 110 * time.Millisecond
				phase = "upload_throttled"
			}
			if err := p.SetRules([]Rule{rule}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				req, _ := http.NewRequest("PUT", origin.URL+"/part", strings.NewReader(payload))
				req.GetBody = nil
				req.Header.Set("X-Fixture", "preserved")
				start := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || string(body) != "receipt" || resp.Header.Get("ETag") != `"same"` || resp.StatusCode != 200 {
					t.Fatal("response altered")
				}
				if i >= 1 && time.Since(start) < minimum {
					t.Fatal("slow-network rule did not delay transfer")
				}
			}
			if calls.Load() != 3 {
				t.Fatal("mutation replayed", calls.Load())
			}
			matches := 0
			for _, e := range p.Events(0) {
				if e.Phase == phase {
					matches++
				}
			}
			if matches != 2 {
				t.Fatal("Nth/repeat mismatch", matches)
			}
			if err := p.SetRules(nil); err != nil {
				t.Fatal(err)
			}
			if p.match(rule.Host, mustRequest(t, origin.URL+"/part")) != nil {
				t.Fatal("cleared rule still active")
			}
		})
	}
}

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	r, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPacedReaderCancellationAndZeroRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := &pacedReader{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("a", 32768))), ctx: ctx, rate: 1024}
	if n, err := r.Read(nil); n != 0 || err != nil || !r.started.IsZero() {
		t.Fatal("zero read consumed budget")
	}
	start := time.Now()
	if n, err := r.Read(make([]byte, 65536)); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(n, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation waited for full pacing interval")
	}
	if r.sent != 32768 {
		t.Fatal("read not bounded", r.sent)
	}
}

func TestRejectInvalidSlowNetworkRules(t *testing.T) {
	p, err := New([]string{"example.test:443"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	base := Rule{ID: "slow", Host: "example.test:443", Method: "PUT", PathPrefix: "/", Nth: 1}
	for _, mutate := range []func(*Rule){
		func(r *Rule) { r.Action = "drop_response"; r.Repeat = true },
		func(r *Rule) { r.Action = "observe"; r.DelayMS = 10 },
		func(r *Rule) { r.Action = "delay_request"; r.DelayMS = 10001 },
		func(r *Rule) { r.Action = "delay_request"; r.DelayMS = 10; r.BytesPerSecond = 1024 },
		func(r *Rule) { r.Action = "throttle_upload"; r.BytesPerSecond = 1023 },
		func(r *Rule) { r.Action = "throttle_upload"; r.BytesPerSecond = 1024; r.Method = "POST" },
		func(r *Rule) { r.Action = "throttle_upload"; r.BytesPerSecond = 1024; r.Status = 429 },
	} {
		r := base
		mutate(&r)
		if p.SetRules([]Rule{r}) == nil {
			t.Fatal("invalid rule accepted", r.Action)
		}
	}
}
