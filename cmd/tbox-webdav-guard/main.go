// Tbox-webdav-guard is the HTTP concurrency boundary in front of rclone's
// unmodified WebDAV server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyte/TboxRclone/internal/webdavguard"
)

func run() error {
	listen := flag.String("addr", "127.0.0.1:8686", "public WebDAV guard listen address")
	upstreamValue := flag.String("upstream", "http://127.0.0.1:8685", "private rclone WebDAV origin")
	base := flag.String("base-url", "", "public WebDAV base path shared with the origin")
	stageZero := flag.String("stage-zero-dir", "", "durable directory for macOS webdavfs zero-byte creation barriers")
	trace := flag.Bool("trace-requests", false, "log request metadata without credentials or bodies")
	flag.Parse()
	upstream, err := url.Parse(*upstreamValue)
	if err != nil {
		return err
	}
	var guard *webdavguard.Guard
	if *stageZero != "" {
		guard, err = webdavguard.NewStagingProxy(*base, upstream, *stageZero)
	} else {
		guard, err = webdavguard.NewProxy(*base, upstream)
	}
	if err != nil {
		return err
	}
	var handler http.Handler = guard
	if *trace {
		handler = traceRequests(handler)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		done <- server.Shutdown(shutdown)
	}()
	err = server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err = <-done; err != nil {
		return fmt.Errorf("shut down WebDAV guard: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
