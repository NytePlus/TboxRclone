// Package faultproxy provides an HTTPS fault injector for isolated cloud experiments.
// The CA is ephemeral and must be trusted explicitly by the experimental client.
package faultproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rule injects a fault on the Nth matching request. Slow-network rules may repeat.
// Host includes the port.
// ActionKey matches a query key without persisting its value. PartNumber may
// match the non-secret multipart index; events still omit all query values.
type Rule struct {
	Repeat           bool     `json:"repeat,omitempty"`
	DelayMS          int      `json:"delay_ms,omitempty"`
	BytesPerSecond   int64    `json:"bytes_per_second,omitempty"`
	ID               string   `json:"id"`
	Host             string   `json:"host"`
	Method           string   `json:"method"`
	PathPrefix       string   `json:"path_prefix"`
	ActionKey        string   `json:"action_key,omitempty"`
	PartNumber       int      `json:"part_number,omitempty"`
	MinContentLength int64    `json:"min_content_length,omitempty"`
	AfterAny         []string `json:"after_any,omitempty"`
	Nth              int      `json:"nth"`
	Action           string   `json:"action"`
	Bytes            int64    `json:"bytes,omitempty"`
	Status           int      `json:"status,omitempty"`
	Code             string   `json:"code,omitempty"`
}

// Event records timing and phase without URLs, bodies, query values or credentials.
type Event struct {
	Sequence uint64    `json:"sequence"`
	Request  uint64    `json:"request"`
	At       time.Time `json:"at"`
	Phase    string    `json:"phase"`
	Rule     string    `json:"rule,omitempty"`
	Host     string    `json:"host,omitempty"`
	Method   string    `json:"method,omitempty"`
	Status   int       `json:"status,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
}

// Proxy intercepts CONNECT requests only to explicitly allowed authorities.
type Proxy struct {
	mu        sync.Mutex
	allowed   map[string]bool
	rules     []Rule
	hits      map[string]int
	events    []Event
	sequence  uint64
	requests  atomic.Uint64
	pending   map[uint64]chan bool
	conns     map[net.Conn]bool
	closed    bool
	ca        *x509.Certificate
	key       *ecdsa.PrivateKey
	caPEM     []byte
	transport http.RoundTripper
	timeout   time.Duration
	slots     chan struct{}
}

// New constructs an injector. The transport must verify upstream TLS certificates.
// Nil transport uses normal system roots and deliberately does not use another proxy.
func New(authorities []string, transport http.RoundTripper) (*Proxy, error) {
	allowed := map[string]bool{}
	for _, host := range authorities {
		h, port, e := net.SplitHostPort(host)
		portNumber, portErr := strconv.Atoi(port)
		if e != nil || h == "" || portErr != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsAny(h, "/\\@?#") {
			return nil, errors.New("allowlist entries must be host:port")
		}
		allowed[strings.ToLower(host)] = true
	}
	if len(allowed) == 0 {
		return nil, errors.New("empty upstream allowlist")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, e
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "TboxRclone ephemeral fault proxy"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		return nil, e
	}
	ca, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, e
	}
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		t.DisableCompression = true
		transport = t
	}
	return &Proxy{allowed: allowed, hits: map[string]int{}, pending: map[uint64]chan bool{}, conns: map[net.Conn]bool{}, ca: ca, key: key, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), transport: transport, timeout: 2 * time.Minute, slots: make(chan struct{}, 64)}, nil
}

// CAPEM returns the public CA certificate, never the private key.
func (p *Proxy) CAPEM() []byte { return append([]byte(nil), p.caPEM...) }

// Events returns a bounded event snapshot; sequence gaps indicate expired records.
func (p *Proxy) Events(after uint64) []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Event{}
	for _, e := range p.events {
		if e.Sequence > after {
			out = append(out, e)
		}
	}
	return out
}
func (p *Proxy) record(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sequence++
	e.Sequence = p.sequence
	e.At = time.Now().UTC()
	if len(p.events) == 4096 {
		copy(p.events, p.events[1:])
		p.events = p.events[:4095]
	}
	p.events = append(p.events, e)
}

// Release forwards (true) or discards (false) a held complete upstream response.
// Callers should first independently verify the upstream side effect.
func (p *Proxy) Release(request uint64, forward bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.pending[request]
	if !ok {
		return errors.New("request is not held")
	}
	delete(p.pending, request)
	ch <- forward
	return nil
}

// Close terminates active tunnels and held responses.
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for c := range p.conns {
		c.Close()
	}
	for id, ch := range p.pending {
		delete(p.pending, id)
		ch <- false
	}
	if t, ok := p.transport.(interface{ CloseIdleConnections() }); ok {
		t.CloseIdleConnections()
	}
}
func (p *Proxy) certificate(host string) (tls.Certificate, error) {
	name, _, _ := net.SplitHostPort(host)
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return tls.Certificate{}, e
	}
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: p.ca.NotBefore, NotAfter: p.ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(name); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		leaf.DNSNames = []string{name}
	}
	der, e := x509.CreateCertificate(rand.Reader, leaf, p.ca, &p.key.PublicKey, p.key)
	return tls.Certificate{Certificate: [][]byte{der, p.ca.Raw}, PrivateKey: p.key}, e
}

// ServeHTTP accepts one HTTP/1.1 exchange per TLS tunnel. This deliberate connection
// isolation is for correctness experiments, not a proxy throughput benchmark.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authority := strings.ToLower(r.Host)
	if r.Method != "CONNECT" || !p.allowed[authority] {
		http.Error(w, "target not allowed", http.StatusForbidden)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "proxy connection limit", 503)
		return
	}
	cert, e := p.certificate(authority)
	if e != nil {
		http.Error(w, "certificate failure", 500)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunnel unavailable", 500)
		return
	}
	raw, buffer, e := hijacker.Hijack()
	if e != nil {
		return
	}
	defer raw.Close()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.conns[raw] = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.conns, raw); p.mu.Unlock() }()
	raw.SetDeadline(time.Now().Add(p.timeout))
	if _, e = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
		return
	}
	if e = buffer.Flush(); e != nil {
		return
	}
	conn := tls.Server(&bufferedConn{Conn: raw, reader: buffer.Reader}, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if e = conn.Handshake(); e != nil {
		return
	}
	headerReader := &io.LimitedReader{R: conn, N: 64 << 10}
	request, e := http.ReadRequest(bufio.NewReader(headerReader))
	if e != nil {
		return
	}
	headerReader.N = 1 << 62
	defer func() { conn.Close(); request.Body.Close() }()
	// Neither absolute-form requests nor Host can escape the CONNECT allowlist.
	if request.URL.IsAbs() || !strings.EqualFold(httpsAuthority(request.Host), authority) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	request = request.WithContext(ctx)
	request.URL.Scheme = "https"
	request.URL.Host = authority
	request.RequestURI = ""
	// Reuse upstream connections for reads: forcing a fresh remote TLS session
	// for every metadata lookup overwhelms small Finder copies. Keep mutation
	// connections one-shot and never supply a replayable request body.
	request.Close = request.Method != http.MethodGet && request.Method != http.MethodHead
	request.GetBody = nil
	request.Header.Del("Connection")
	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Proxy-Connection")
	id := p.requests.Add(1)
	rule := p.match(authority, request)
	event := Event{Request: id, Host: authority, Method: request.Method}
	if rule != nil {
		event.Rule = rule.ID
	}
	emit := func(phase string) { event.Phase = phase; p.record(event) }
	emit("received")
	if rule != nil && rule.Action == "drop_request" {
		emit("dropped_before_origin")
		return
	}
	if rule != nil && rule.Action == "respond_status" {
		code := rule.Code
		if code == "" {
			code = "InjectedStatus"
		}
		body, _ := json.Marshal(map[string]string{"code": code})
		response := &http.Response{
			StatusCode:    rule.Status,
			Status:        strconv.Itoa(rule.Status) + " " + http.StatusText(rule.Status),
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)),
			Close:         true,
		}
		response.Header.Set("Content-Type", "application/json")
		response.Header.Set("Cache-Control", "no-store")
		if rule.Status == http.StatusTooManyRequests {
			response.Header.Set("Retry-After", "1")
		}
		event.Status = rule.Status
		event.Bytes = int64(len(body))
		emit("synthetic_status")
		if e = response.Write(conn); e != nil {
			emit("client_response_interrupted")
			return
		}
		emit("forwarded")
		return
	}
	if rule != nil && rule.Action == "cut_upload" {
		request.Body = &cutReader{ReadCloser: request.Body, left: rule.Bytes}
	}
	if rule != nil && rule.Action == "ignore_range" {
		request.Header.Del("Range")
		request.Header.Del("If-Match")
		emit("range_ignored")
	}
	var replayBody []byte
	if rule != nil && rule.Action == "replay_part" {
		// This external-event fixture is restricted to bounded numbered parts.
		// Never replay a control-plane mutation or an unbounded stream.
		if request.URL.Query().Get("uploadId") == "" || request.ContentLength < 0 || request.ContentLength > 8<<20 {
			emit("replay_rejected")
			return
		}
		replayBody, e = io.ReadAll(io.LimitReader(request.Body, (8<<20)+1))
		if e != nil || int64(len(replayBody)) != request.ContentLength || len(replayBody) > 8<<20 {
			emit("replay_rejected")
			return
		}
		request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(replayBody))
	}
	if rule != nil && rule.Action == "delay_request" {
		emit("delay_started")
		if waitDelay(ctx, time.Duration(rule.DelayMS)*time.Millisecond) != nil {
			emit("delay_cancelled")
			return
		}
		emit("delay_finished")
	}
	if rule != nil && rule.Action == "throttle_upload" {
		request.Body = &pacedReader{ReadCloser: request.Body, ctx: ctx, rate: rule.BytesPerSecond}
		emit("upload_throttled")
	}
	response, e := p.transport.RoundTrip(request)
	if e != nil {
		emit("origin_transport_error")
		return
	}
	defer response.Body.Close()
	event.Status = response.StatusCode
	emit("origin_headers")
	if rule != nil && rule.Action == "replay_part" {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if readErr != nil || len(body) > 1<<20 || response.StatusCode < 200 || response.StatusCode >= 300 || response.Header.Get("ETag") == "" {
			emit("replay_original_not_successful")
			return
		}
		response.Body.Close()
		emit("origin_response_complete")
		replay := request.Clone(ctx)
		replay.Body = io.NopCloser(bytes.NewReader(replayBody))
		emit("replay_sent")
		second, replayErr := p.transport.RoundTrip(replay)
		if replayErr != nil {
			emit("replay_transport_error")
			return
		}
		received, readErr := io.Copy(io.Discard, io.LimitReader(second.Body, (1<<20)+1))
		second.Body.Close()
		event.Status = second.StatusCode
		event.Bytes = int64(len(replayBody))
		if readErr != nil || received > 1<<20 || second.StatusCode < 200 || second.StatusCode >= 300 || second.Header.Get("ETag") != response.Header.Get("ETag") {
			emit("replay_not_equivalent")
			return
		}
		emit("replay_same_etag")
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		response.TransferEncoding = nil
	}
	if rule != nil && (rule.Action == "drop_response" || rule.Action == "hold_response") {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		event.Bytes = int64(len(body))
		if readErr != nil || len(body) > 1<<20 {
			emit("response_not_complete_or_over_limit")
			return
		}
		emit("origin_response_complete")
		if rule.Action == "drop_response" {
			emit("dropped_after_origin")
			return
		}
		ch := make(chan bool, 1)
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		p.pending[id] = ch
		p.mu.Unlock()
		emit("held")
		defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
		select {
		case forward := <-ch:
			if !forward {
				emit("dropped_after_release")
				return
			}
		case <-ctx.Done():
			emit("hold_timeout")
			return
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		response.TransferEncoding = nil
	}
	if rule != nil && rule.Action == "cut_download" {
		response.Body = &cutReader{ReadCloser: response.Body, left: rule.Bytes}
	}
	response.Close = true
	if e = response.Write(conn); e != nil {
		emit("client_response_interrupted")
		return
	}
	emit("forwarded")
}
