// Davcheck exercises a live loopback WebDAV service using isolated generated fixtures.
// It is a protocol check, not a replacement for Finder acceptance.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type result struct {
	Case     string `json:"case"`
	Expected int    `json:"expected_http"`
	Actual   int    `json:"actual_http"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail,omitempty"`
}
type report struct {
	At      string   `json:"at"`
	Scope   string   `json:"scope"`
	Fixture string   `json:"fixture"`
	Results []result `json:"results"`
}

func run() error {
	endpoint := flag.String("url", "http://127.0.0.1:8686/", "loopback WebDAV origin serving the isolated lab root")
	writes := flag.Bool("allow-lab-writes", false, "create generated fixtures in the configured isolated lab root")
	flag.Parse()
	if !*writes {
		return errors.New("explicit -allow-lab-writes is required; configure the server with an isolated lab root first")
	}
	u, e := url.Parse(*endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("url must be a loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("only loopback IP addresses are accepted")
	}
	base := strings.TrimRight(*endpoint, "/") + "/"
	var id [8]byte
	if _, e = rand.Read(id[:]); e != nil {
		return e
	}
	prefix := "davcheck-" + hex.EncodeToString(id[:]) + "/"
	out := report{At: time.Now().UTC().Format(time.RFC3339), Scope: "live WebDAV protocol; not Finder acceptance", Fixture: prefix}
	client := http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	passed := true
	request := func(name, method, path string, payload []byte, headers map[string]string, want int) (http.Header, []byte) {
		item := result{Case: name, Expected: want}
		req, err := http.NewRequest(method, base+path, bytes.NewReader(payload))
		if err == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
		var h http.Header
		var b []byte
		if err == nil {
			var response *http.Response
			response, err = client.Do(req)
			if err == nil {
				item.Actual = response.StatusCode
				h = response.Header
				b, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
				response.Body.Close()
				if len(b) > 1<<20 {
					err = errors.New("response too large")
				}
			}
		}
		item.Pass = err == nil && item.Actual == want
		if err != nil {
			item.Detail = "request or response failed"
		}
		if !item.Pass {
			passed = false
		}
		out.Results = append(out.Results, item)
		return h, b
	}
	fact := func(ok bool, detail string) {
		if !ok {
			passed = false
			last := &out.Results[len(out.Results)-1]
			last.Pass = false
			last.Detail = detail
		}
	}
	payload := []byte("TboxRclone WebDAV deterministic protocol fixture\n")
	request("options", "OPTIONS", "", nil, nil, 200)
	request("mkcol_new", "MKCOL", prefix, nil, nil, 201)
	request("mkcol_existing", "MKCOL", prefix, nil, nil, 405)
	file := prefix + "file.txt"
	request("put_new", "PUT", file, payload, nil, 201)
	h, b := request("get_content", "GET", file, nil, nil, 200)
	fact(bytes.Equal(b, payload), "content differs")
	etag := h.Get("ETag")
	fact(etag != "", "missing ETag")
	h, b = request("head", "HEAD", file, nil, nil, 200)
	fact(len(b) == 0 && h.Get("Content-Length") == fmt.Sprint(len(payload)), "incorrect HEAD length")
	h, b = request("range", "GET", file, nil, map[string]string{"Range": "bytes=2-8"}, 206)
	fact(bytes.Equal(b, payload[2:9]) && h.Get("Content-Range") == fmt.Sprintf("bytes 2-8/%d", len(payload)), "incorrect range")
	request("range_unsatisfiable", "GET", file, nil, map[string]string{"Range": "bytes=9999-"}, 416)
	request("if_match_wrong_get", "GET", file, nil, map[string]string{"If-Match": `"not-the-current-etag"`}, 412)
	request("if_none_match_get", "GET", file, nil, map[string]string{"If-None-Match": etag}, 304)
	request("if_none_match_put", "PUT", file, []byte("must not replace"), map[string]string{"If-None-Match": "*"}, 412)
	_, b = request("failed_put_preserves_content", "GET", file, nil, nil, 200)
	fact(bytes.Equal(b, payload), "failed PUT changed original content")
	_, b = request("propfind", "PROPFIND", prefix, nil, map[string]string{"Depth": "1"}, 207)
	fact(bytes.Contains(b, []byte("file.txt")), "listing omitted file")
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if e = encoder.Encode(out); e != nil {
		return e
	}
	if !passed {
		return errors.New("WebDAV protocol checks failed; fixtures retained")
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
