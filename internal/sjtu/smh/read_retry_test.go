package smh

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type readRetryTransport func(*http.Request) (*http.Response, error)

func (f readRetryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJSONReadRetryBoundaries(t *testing.T) {
	for _, mode := range []string{"recover", "exhaust", "mutation", "forbidden", "protocol", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c := &Client{Endpoint: "https://example.invalid", Library: "l", Space: "s", Token: func(context.Context) (string, error) { return "token", nil }}
			c.HTTP = &http.Client{Transport: readRetryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "cancel" {
					cancel()
					return nil, io.EOF
				}
				if mode == "mutation" || mode == "exhaust" || mode == "recover" && calls == 1 {
					return nil, io.EOF
				}
				code, body := 200, `{"type":"dir"}`
				if mode == "forbidden" {
					code = 403
				}
				if mode == "protocol" {
					body = `broken`
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})}
			method := http.MethodGet
			if mode == "mutation" {
				method = http.MethodPost
			}
			var item Item
			err := c.JSON(ctx, method, "directory", "path", nil, nil, &item)
			expected := 1
			switch mode {
			case "recover":
				expected = 2
				if err != nil || item.Type != "dir" {
					t.Fatalf("%+v %v", item, err)
				}
			case "exhaust":
				expected = 3
				if !errors.Is(err, ErrTransport) {
					t.Fatal(err)
				}
			case "mutation":
				if !errors.Is(err, ErrUnknown) {
					t.Fatal(err)
				}
			case "forbidden":
				if !IsStatus(err, 403) {
					t.Fatal(err)
				}
			case "protocol":
				if !errors.Is(err, ErrProtocol) {
					t.Fatal(err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if calls != expected {
				t.Fatalf("got %d requests, want %d", calls, expected)
			}
		})
	}
}
