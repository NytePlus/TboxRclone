package faultproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUploadFaultSkipsFinderPlaceholderAndMetadata(t *testing.T) {
	sizes := make(chan int64, 4)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		sizes <- n
		io.WriteString(w, "ok")
	}))
	defer origin.Close()
	p, c := setup(t, origin)
	host := strings.TrimPrefix(origin.URL, "https://")
	r := Rule{ID: "large-part", Host: host, Method: http.MethodPut, PathPrefix: "/",
		PartNumber: 1, MinContentLength: 65536, Nth: 1, Action: "drop_request"}
	if err := p.SetRules([]Rule{r}); err != nil {
		t.Fatal(err)
	}
	for i, size := range []int{0, 4096, 65536, 65536} {
		req, _ := http.NewRequest(http.MethodPut, origin.URL+"?partNumber=1", strings.NewReader(strings.Repeat("a", size)))
		req.GetBody = nil
		resp, err := c.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		if (err != nil) != (i == 2) {
			t.Fatalf("request %d size %d: %v", i, size, err)
		}
	}
	if len(sizes) != 3 {
		t.Fatal("unexpected origin calls", len(sizes))
	}
	for _, want := range []int64{0, 4096, 65536} {
		if got := <-sizes; got != want {
			t.Fatal(got, want)
		}
	}
	r.MinContentLength = -1
	if err := p.SetRules([]Rule{r}); err == nil {
		t.Fatal("negative length accepted")
	}
}

func TestReplayPartUsesIdenticalRequestAndRejectsChangedReceipt(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			var calls atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				b, err := io.ReadAll(r.Body)
				if err != nil || string(b) != "immutable-part" || r.URL.RawQuery != "partNumber=2&uploadId=private-upload" || r.Header.Get("Authorization") != "private-signature" {
					t.Errorf("replayed request differs")
				}
				tag := `"same"`
				if mismatch && n == 2 {
					tag = `"changed"`
				}
				w.Header().Set("ETag", tag)
				w.WriteHeader(200)
			}))
			defer origin.Close()
			p, client := setup(t, origin)
			r := Rule{ID: "replay", Host: strings.TrimPrefix(origin.URL, "https://"), Method: "PUT", PathPrefix: "/", PartNumber: 2, Nth: 1, Action: "replay_part"}
			if err := p.SetRules([]Rule{r}); err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("PUT", origin.URL+"/?partNumber=2&uploadId=private-upload", strings.NewReader("immutable-part"))
			req.Header.Set("Authorization", "private-signature")
			req.GetBody = nil
			resp, err := client.Do(req)
			if mismatch {
				if err == nil {
					resp.Body.Close()
					t.Fatal("changed replay receipt accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 || resp.Header.Get("ETag") != `"same"` {
					t.Fatal("original response changed")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("expected exactly two origin requests", calls.Load())
			}
			encoded, _ := json.Marshal(p.Events(0))
			if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "immutable-part") {
				t.Fatal("sensitive data in events")
			}
			phase := "replay_same_etag"
			if mismatch {
				phase = "replay_not_equivalent"
			}
			if !strings.Contains(string(encoded), phase) {
				t.Fatal("missing replay outcome", string(encoded))
			}
			r.Method = "POST"
			if err := p.SetRules([]Rule{r}); err == nil {
				t.Fatal("control-plane replay accepted")
			}
		})
	}
}

func TestPrerequisiteSkipsPlaceholderWithoutConsumingFault(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer origin.Close()
	p, client := setup(t, origin)
	host := strings.TrimPrefix(origin.URL, "https://")
	rules := []Rule{
		{ID: "part", Host: host, Method: "PUT", PathPrefix: "/", PartNumber: 2, Nth: 1, Action: "respond_status", Status: 403},
		{ID: "expiry", Host: host, Method: "GET", PathPrefix: "/", ActionKey: "upload", Nth: 1, Action: "respond_status", Status: 404, AfterAny: []string{"part"}},
	}
	if err := p.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	for i, step := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/?upload=1", 200}, {"GET", "/?upload=1", 200},
		{"PUT", "/?partNumber=2", 403}, {"GET", "/?upload=1", 404}, {"GET", "/?upload=1", 200},
	} {
		req, _ := http.NewRequest(step.method, origin.URL+step.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != step.status {
			t.Fatalf("step %d: got %d want %d", i, resp.StatusCode, step.status)
		}
	}
	rules[1].AfterAny = []string{"missing"}
	if err := p.SetRules(rules); err == nil {
		t.Fatal("unknown prerequisite accepted")
	}
}
