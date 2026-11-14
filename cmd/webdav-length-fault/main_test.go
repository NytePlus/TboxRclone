package main

import (
	"github.com/nyte/TboxRclone/internal/verifyutil"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestControlRejectsDeniedOrCorruptWrites(t *testing.T) {
	for _, mode := range []string{"good", "denied", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			var stored string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					if mode == "denied" {
						w.WriteHeader(403)
						return
					}
					b, _ := io.ReadAll(r.Body)
					stored = string(b)
					w.WriteHeader(201)
					return
				}
				if mode == "corrupt" {
					io.WriteString(w, "wrong")
					return
				}
				io.WriteString(w, stored)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			if err := verifyControl(srv.Client(), u, "fixture"); (err == nil) != (mode == "good") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
		})
	}
}

func TestMalformedResponseTimeoutIsNotRejection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
		<-done
	}()
	u, _ := url.Parse("http://" + listener.Addr().String())
	_, err = sendMalformedWithin(u, "fixture/short", "Content-Length: 10\r\n", "short", 100*time.Millisecond)
	if err == nil {
		t.Fatal("timeout was accepted as rejection")
	}
	for _, line := range []string{"HTTP/1.1 201 Created", "HTTP/1.1 200 OK", "garbage"} {
		if rejectedResponse(line) {
			t.Fatalf("accepted %q", line)
		}
	}
	if !rejectedResponse("HTTP/1.1 400 Bad Request") || !rejectedResponse("connection closed without response") {
		t.Fatal("legitimate rejection not recognized")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	if _, err := verifyutil.Validate("http://127.0.0.1:8686/", "system-test-length-fault"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ origin, base string }{
		{"https://127.0.0.1/", "safe"},
		{"http://example.com/", "safe"},
		{"http://127.0.0.1/", "../escape"},
	} {
		if _, err := verifyutil.Validate(tc.origin, tc.base); err == nil {
			t.Fatalf("accepted unsafe input %#v", tc)
		}
	}
}
