package faultproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyte/TboxRclone/internal/smh"
)

func setup(t *testing.T, origin *httptest.Server, others ...*httptest.Server) (*Proxy, *http.Client) {
	t.Helper()
	hosts := []string{strings.TrimPrefix(origin.URL, "https://")}
	for _, s := range others {
		hosts = append(hosts, strings.TrimPrefix(s.URL, "https://"))
	}
	p, e := New(hosts, origin.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(p)
	t.Cleanup(func() { p.Close(); server.Close() })
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(p.CAPEM())
	address, _ := url.Parse(server.URL)
	transport := &http.Transport{Proxy: http.ProxyURL(address), TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	return p, &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func rule(t *testing.T, p *Proxy, origin *httptest.Server, action string, nth int, n int64) {
	t.Helper()
	e := p.SetRules([]Rule{{ID: "injection", Host: strings.TrimPrefix(origin.URL, "https://"), Method: "POST", PathPrefix: "/", Nth: nth, Action: action, Bytes: n}})
	if e != nil {
		t.Fatal(e)
	}
}
func send(client *http.Client, address string, body string) error {
	req, _ := http.NewRequest("POST", address, strings.NewReader(body))
	req.GetBody = nil
	response, e := client.Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	_, e = io.ReadAll(response.Body)
	return e
}
func held(t *testing.T, p *Proxy) Event {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, e := range p.Events(0) {
			if e.Phase == "held" {
				return e
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("response was not held")
		case <-ticker.C:
		}
	}
}
func TestHoldConfirmUntilIndependentFactCheck(t *testing.T) {
	var commits atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			commits.Add(1)
		}
		io.WriteString(w, `{"committed":true}`)
	}))
	defer origin.Close()
	p, client := setup(t, origin)
	rule(t, p, origin, "hold_response", 1, 0)
	c := &smh.Client{Endpoint: origin.URL, Library: "library", Space: "space", Token: func(context.Context) (string, error) { return "NEVER_LOG_THIS", nil }, HTTP: client}
	done := make(chan error, 1)
	go func() {
		done <- c.JSON(context.Background(), "POST", "file", "confirm-key", url.Values{"confirm": {"1"}}, struct{}{}, nil)
	}()
	event := held(t, p)
	if commits.Load() != 1 {
		t.Fatal("held before origin commit")
	}
	// This independent connection bypasses the proxy and observes server state.
	response, e := origin.Client().Get(origin.URL + "/fact-check")
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || string(b) != `{"committed":true}` {
		t.Fatal(string(b), e)
	}
	select {
	case <-done:
		t.Fatal("client acknowledged before fact check/release")
	default:
	}
	if e = p.Release(event.Request, false); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e == nil {
		t.Fatal("lost response reported success")
	}
	if commits.Load() != 1 {
		t.Fatal("write replayed")
	}
	evidence, _ := json.Marshal(p.Events(0))
	for _, secret := range []string{"NEVER_LOG_THIS", "confirm-key", "access_token"} {
		if bytes.Contains(evidence, []byte(secret)) {
			t.Fatal("secret in evidence")
		}
	}
}
func TestHoldReleaseForwardsResponse(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "safe") }))
	defer origin.Close()
	p, c := setup(t, origin)
	rule(t, p, origin, "hold_response", 1, 0)
	done := make(chan error, 1)
	go func() { done <- send(c, origin.URL, "data") }()
	event := held(t, p)
	if e := p.Release(event.Request, true); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestBeforeAndAfterOriginAreDifferent(t *testing.T) {
	for _, action := range []string{"drop_request", "drop_response"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "ok") }))
			defer origin.Close()
			p, c := setup(t, origin)
			rule(t, p, origin, action, 1, 0)
			if e := send(c, origin.URL, "data"); e == nil {
				t.Fatal("fault not delivered")
			}
			want := int32(1)
			if action == "drop_request" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatal("wrong fault phase", calls.Load())
			}
			if e := send(c, origin.URL, "data"); e != nil {
				t.Fatal("one-shot rule repeated", e)
			}
			if calls.Load() != want+1 {
				t.Fatal(calls.Load())
			}
		})
	}
}
func TestSignedDataPlaneUsesSameFaultProxy(t *testing.T) {
	var uploaded atomic.Int64
	data := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("access_token") {
			t.Error("token reached data plane")
		}
		n, _ := io.Copy(io.Discard, r.Body)
		uploaded.Add(n)
		w.WriteHeader(200)
	}))
	defer data.Close()
	control := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(smh.Upload{Domain: data.URL, Path: "/object?signature=SIGNATURE_SECRET", Key: "K"})
	}))
	defer control.Close()
	p, client := setup(t, control, data)
	host := strings.TrimPrefix(data.URL, "https://")
	if e := p.SetRules([]Rule{{ID: "data-loss", Host: host, Method: "PUT", PathPrefix: "/object", Nth: 1, Action: "drop_response"}}); e != nil {
		t.Fatal(e)
	}
	c := &smh.Client{Endpoint: control.URL, Library: "l", Space: "s", Token: func(context.Context) (string, error) { return "CONTROL_SECRET", nil }, HTTP: client}
	var upload smh.Upload
	if e := c.JSON(context.Background(), "PUT", "file", "x", nil, struct{}{}, &upload); e != nil {
		t.Fatal(e)
	}
	if e := c.PutData(context.Background(), upload, strings.NewReader("complete bytes"), 14); e == nil {
		t.Fatal("data response loss not injected")
	}
	if uploaded.Load() != 14 {
		t.Fatal("request did not completely reach data plane", uploaded.Load())
	}
	hosts := map[string]bool{}
	for _, e := range p.Events(0) {
		hosts[e.Host] = true
	}
	if len(hosts) != 2 {
		t.Fatal("only one plane covered")
	}
	b, _ := json.Marshal(p.Events(0))
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("signed URL leaked")
	}
}
func TestCutUploadAndDownload(t *testing.T) {
	for _, action := range []string{"cut_upload", "cut_download"} {
		t.Run(action, func(t *testing.T) {
			var received atomic.Int64
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n, _ := io.Copy(io.Discard, r.Body)
				received.Add(n)
				w.Header().Set("Content-Length", "1048576")
				w.Write(bytes.Repeat([]byte{'x'}, 1<<20))
			}))
			defer origin.Close()
			p, c := setup(t, origin)
			rule(t, p, origin, action, 1, 4096)
			if e := send(c, origin.URL, strings.Repeat("y", 1<<20)); e == nil {
				t.Fatal("cut not detected")
			}
			if action == "cut_upload" && received.Load() > 4096 {
				t.Fatal("too many upload bytes reached origin")
			}
			if action == "cut_download" && received.Load() != 1<<20 {
				t.Fatal("upload not complete before download fault")
			}
		})
	}
}
func TestNthRuleAndAuthorityIsolation(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "ok") }))
	defer origin.Close()
	p, c := setup(t, origin)
	rule(t, p, origin, "drop_request", 2, 0)
	if e := send(c, origin.URL, "a"); e != nil {
		t.Fatal(e)
	}
	if e := send(c, origin.URL, "b"); e == nil {
		t.Fatal("Nth ignored")
	}
	if e := send(c, origin.URL, "c"); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	req, _ := http.NewRequest("POST", origin.URL, strings.NewReader("d"))
	req.Host = "different.example:443"
	if response, e := c.Do(req); e == nil {
		response.Body.Close()
		t.Fatal("Host escaped authority check")
	}
	if calls.Load() != 2 {
		t.Fatal("cross-authority request forwarded")
	}
}
func TestRejectUnallowlistedCONNECT(t *testing.T) {
	p, e := New([]string{"allowed.example:443"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	r := httptest.NewRequest("CONNECT", "http://denied.example:443", nil)
	r.Host = "denied.example:443"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestControlReleaseAndShutdown(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer origin.Close()
	p, c := setup(t, origin)
	rule(t, p, origin, "hold_response", 1, 0)
	done := make(chan error, 1)
	go func() { done <- send(c, origin.URL, "a") }()
	held(t, p)
	if e := p.SetRules(nil); e == nil {
		t.Fatal("held response lost by reconfiguration")
	}
	p.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("shutdown gave success")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close held tunnel")
	}
}

func TestDefaultHTTPSAuthority(t *testing.T) {
	for input, want := range map[string]string{"pan.sjtu.edu.cn": "pan.sjtu.edu.cn:443", "pan.sjtu.edu.cn:443": "pan.sjtu.edu.cn:443", "[::1]": "[::1]:443", "[::1]:9443": "[::1]:9443"} {
		if got := httpsAuthority(input); got != want {
			t.Fatalf("%q => %q, want %q", input, got, want)
		}
	}
}
