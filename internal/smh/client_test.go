package smh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func clientAt(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	return &Client{Endpoint: s.URL, Library: "lib", Space: "space", Token: func(context.Context) (string, error) { return "SECRET", nil }, HTTP: s.Client()}
}
func TestUnitU01Paths(t *testing.T) {
	names := []string{"中文/😀%#?+\"", "a/e\u0301", "a/é", "a/A", "a/a"}
	for _, p := range names {
		t.Run(p, func(t *testing.T) {
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/directory/lib/space/"+p {
					t.Errorf("path %q", r.URL.Path)
				}
				if r.URL.Query().Get("access_token") != "SECRET" {
					t.Error("token")
				}
				io.WriteString(w, `{"type":"file","size":"9007199254740993"}`)
			})
			i, e := c.Info(context.Background(), p)
			if e != nil || i.Size != 9007199254740993 {
				t.Fatalf("%+v %v", i, e)
			}
		})
	}
	for _, p := range []string{"../x", "a//b", "/a", "a/./b", "a/../b", "a\\b", "a\x00b"} {
		if ValidatePath(p) == nil {
			t.Errorf("accepted %q", p)
		}
	}
}
func TestUnitU02IntegerPrecision(t *testing.T) {
	for _, s := range []string{`"9223372036854775807"`, `9223372036854775807`} {
		var n Int64
		if json.Unmarshal([]byte(s), &n) != nil || n != 9223372036854775807 {
			t.Fatal(s)
		}
	}
	for _, s := range []string{`-1`, `1.5`, `1e3`, `"18446744073709551615"`, `null`} {
		var n Int64
		if json.Unmarshal([]byte(s), &n) == nil {
			t.Fatal(s)
		}
	}
}
func TestUnitU02FailureIsNotSuccess(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{200, `{"code":"Denied"}`}, {200, `{"taskId":"task"}`}, {202, `{}`}, {200, `{`}, {200, ``}, {403, `SECRET`}, {503, `SECRET`}} {
		t.Run(tc.body+http.StatusText(tc.status), func(t *testing.T) {
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) })
			_, e := c.Info(context.Background(), "x")
			if e == nil || strings.Contains(e.Error(), "SECRET") {
				t.Fatalf("error %v", e)
			}
		})
	}
}
func TestUnitU05MutationLostResponseNeverReplayed(t *testing.T) {
	var calls atomic.Int32
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			conn.Close()
		}
	})
	e := c.JSON(context.Background(), "POST", "file", "K", url.Values{"confirm": {"1"}}, struct{}{}, nil)
	if !errors.Is(e, ErrUnknown) || calls.Load() != 1 || strings.Contains(e.Error(), "SECRET") {
		t.Fatalf("%v calls=%d", e, calls.Load())
	}
}
func TestUnitU05MutationServerFailureUnknown(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	e := c.JSON(context.Background(), "DELETE", "file", "x", nil, nil, nil)
	if !errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
}
func TestUnitU06Ranges(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		cr, tag, body string
		ok            bool
	}{
		{"correct", 206, "bytes 2-4/6", "\"v1\"", "cde", true},
		{"ignored", 200, "", "\"v1\"", "abcdef", false},
		{"wrong offsets", 206, "bytes 1-3/6", "\"v1\"", "bcd", false},
		{"changed", 206, "bytes 2-4/6", "\"v2\"", "cde", false},
		{"missing validator", 206, "bytes 2-4/6", "", "cde", false},
		{"unsatisfiable", 416, "", "", "", false},
		{"short", 206, "bytes 2-4/6", "\"v1\"", "cd", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("If-Match") != "\"v1\"" || r.Header.Get("Range") != "bytes=2-4" {
					t.Error("request headers")
				}
				w.Header().Set("ETag", tc.tag)
				w.Header().Set("Content-Range", tc.cr)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			r, e := c.Open(context.Background(), "x", Item{Size: 6, ETag: "v1"}, 2, 3)
			if r != nil {
				_, e = io.ReadAll(r)
				r.Close()
			}
			if (e == nil) != tc.ok {
				t.Fatalf("%v", e)
			}
		})
	}
}
func TestUnitU06Cancellation(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request despite cancellation") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := c.Info(ctx, "x")
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestUnitU06StreamLength(t *testing.T) {
	for _, tc := range []struct {
		s  string
		n  int64
		ok bool
	}{{"", 0, true}, {"abc", 3, true}, {"ab", 3, false}, {"abcd", 3, false}} {
		r := &checkedReader{ReadCloser: io.NopCloser(strings.NewReader(tc.s)), left: tc.n}
		_, e := io.ReadAll(r)
		if (e == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
}
func TestUnitU08Pagination(t *testing.T) {
	for _, mode := range []string{"normal", "repeat cursor", "duplicate entry", "missing contents", "empty intermediate"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n > 4 {
					t.Error("unbounded pagination")
					w.WriteHeader(500)
					return
				}
				if mode == "missing contents" {
					io.WriteString(w, `{}`)
					return
				}
				if n == 1 {
					io.WriteString(w, `{"contents":[{"name":"one","type":"file"}],"nextMarker":"m"}`)
					return
				}
				switch mode {
				case "repeat cursor":
					io.WriteString(w, `{"contents":[],"nextMarker":"m"}`)
				case "duplicate entry":
					io.WriteString(w, `{"contents":[{"name":"one","type":"file"}]}`)
				case "empty intermediate":
					if n == 2 {
						io.WriteString(w, `{"contents":[],"nextMarker":"n"}`)
					} else {
						io.WriteString(w, `{"contents":[]}`)
					}
				default:
					io.WriteString(w, `{"contents":[{"name":"two","type":"file"}]}`)
				}
			})
			items, e := c.List(context.Background(), "")
			good := mode == "normal" || mode == "empty intermediate"
			if (e == nil) != good {
				t.Fatalf("%v", e)
			}
			if mode == "normal" && len(items) != 2 {
				t.Fatal(items)
			}
		})
	}
}
func TestUnitU03NoRedirectCredentialLeak(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	c, e := New(source.URL, "l", "s", "unused")
	if e != nil {
		t.Fatal(e)
	}
	c.Token = func(context.Context) (string, error) { return "SECRET", nil }
	c.HTTP.Transport = source.Client().Transport
	_, e = c.Info(context.Background(), "x")
	if !IsStatus(e, 302) || targetCalls.Load() != 0 {
		t.Fatalf("%v %d", e, targetCalls.Load())
	}
}

func TestUnitU06SignedRedirectKeepsRangeWithoutControlCredentials(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("credential crossed origin")
		}
		if r.Header.Get("Range") != "bytes=1-2" || r.Header.Get("If-Match") != "\"v1\"" {
			t.Error("lost content preconditions")
		}
		w.Header().Set("ETag", "\"v1\"")
		w.Header().Set("Content-Range", "bytes 1-2/4")
		w.WriteHeader(206)
		io.WriteString(w, "bc")
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/data?signature=signed", 302)
	}))
	defer source.Close()
	c, e := New(source.URL, "l", "s", "unused")
	if e != nil {
		t.Fatal(e)
	}
	c.Token = func(context.Context) (string, error) { return "SECRET", nil }
	c.HTTP.Transport = source.Client().Transport
	r, e := c.Open(context.Background(), "x", Item{Size: 4, ETag: "v1"}, 1, 2)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	b, e := io.ReadAll(r)
	if e != nil || string(b) != "bc" {
		t.Fatal(string(b), e)
	}
}
func TestUnitU06WeakValidatorRejected(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { t.Error("weak validator sent") })
	if _, e := c.Open(context.Background(), "x", Item{Size: 1, ETag: `W/"v1"`}, 0, -1); e == nil {
		t.Fatal("weak validator accepted")
	}
}
