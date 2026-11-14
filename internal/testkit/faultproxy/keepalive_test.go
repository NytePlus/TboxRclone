package faultproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstreamReadConnectionsAreReused(t *testing.T) {
	type observation struct {
		address string
		close   bool
	}
	seen := make(chan observation, 4)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		seen <- observation{r.RemoteAddr, r.Close}
		io.WriteString(w, "ok")
	}))
	defer origin.Close()
	_, client := setup(t, origin)
	rows := []observation{}
	for _, method := range []string{"GET", "GET", "POST", "GET"} {
		req, err := http.NewRequest(method, origin.URL, strings.NewReader("payload"))
		if err != nil {
			t.Fatal(err)
		}
		req.GetBody = nil
		req.Close = true // A one-shot client tunnel need not close the upstream socket.
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "ok" {
			t.Fatalf("body=%q err=%v", body, err)
		}
		rows = append(rows, <-seen)
	}
	if rows[0].close || rows[1].close || rows[3].close || !rows[2].close {
		t.Fatalf("wrong keepalive policy: %+v", rows)
	}
	if rows[0].address != rows[1].address {
		t.Fatalf("reads opened separate origin connections: %+v", rows)
	}
	if rows[2].address == rows[3].address {
		t.Fatalf("mutation connection reused: %+v", rows)
	}
	if len(seen) != 0 {
		t.Fatal("unexpected replay")
	}
}
