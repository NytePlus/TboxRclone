package faultproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIgnoreRangeOnlyConsumesRangedRead(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixture"`)
		if r.Header.Get("Range") == "" {
			if r.Header.Get("If-Match") != "" {
				t.Error("ignored range kept conditional header")
			}
			io.WriteString(w, "0123456789")
			return
		}
		if r.Header.Get("Range") != "bytes=2-4" || r.Header.Get("If-Match") != `"fixture"` {
			t.Error("unaffected range request changed")
		}
		w.Header().Set("Content-Range", "bytes 2-4/10")
		w.WriteHeader(206)
		io.WriteString(w, "234")
	}))
	defer origin.Close()
	p, client := setup(t, origin)
	rule := Rule{ID: "ignore-range", Host: strings.TrimPrefix(origin.URL, "https://"), Method: "GET", PathPrefix: "/", Nth: 1, Action: "ignore_range"}
	if err := p.SetRules([]Rule{rule}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"0123456789", "0123456789", "234"} {
		req, _ := http.NewRequest("GET", origin.URL, nil)
		if i != 0 {
			req.Header.Set("Range", "bytes=2-4")
			req.Header.Set("If-Match", `"fixture"`)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		status := 200
		if i == 2 {
			status = 206
		}
		if err != nil || string(body) != want || response.StatusCode != status {
			t.Fatalf("step %d: status=%d body=%q err=%v", i, response.StatusCode, body, err)
		}
	}
	hits := 0
	for _, event := range p.Events(0) {
		if event.Phase == "range_ignored" {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("expected one ignored range, got %d", hits)
	}
	rule.Method = "PUT"
	if err := p.SetRules([]Rule{rule}); err == nil {
		t.Fatal("mutation accepted for ignore_range")
	}
}
