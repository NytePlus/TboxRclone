package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
)

type tracedBody struct {
	io.ReadCloser
	bytes int64
}

func (b *tracedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.bytes += int64(n)
	return n, e
}

type tracedResponse struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *tracedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *tracedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *tracedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, e
}
func (w *tracedResponse) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Opt-in metadata only: no query strings, credentials, lock tokens or bodies.
func traceRequests(next http.Handler) http.Handler {
	var sequence atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sequence.Add(1)
		pathID := fmt.Sprintf("%x", sha256.Sum256([]byte(r.URL.Path)))
		body := &tracedBody{ReadCloser: r.Body}
		r = r.Clone(r.Context())
		r.Body = body
		response := &tracedResponse{ResponseWriter: w}
		log.Printf("dav_request id=%d method=%s path_sha256=%s content_length=%d has_if=%t", id, r.Method, pathID, r.ContentLength, r.Header.Get("If") != "")
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = 200
		}
		log.Printf("dav_response id=%d status=%d request_bytes=%d response_bytes=%d", id, status, body.bytes, response.bytes)
	})
}
