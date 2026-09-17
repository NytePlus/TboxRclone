// Faultproxy provides local HTTPS fault injection with an explicitly trusted ephemeral CA.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/nyte/TboxRclone/internal/faultproxy"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func loopback(address string) bool {
	host, _, e := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	return e == nil && ip != nil && ip.IsLoopback()
}
func run() error {
	listen := flag.String("listen", "127.0.0.1:8787", "loopback proxy listener")
	control := flag.String("control", "127.0.0.1:8788", "loopback control listener")
	hosts := flag.String("allow-hosts", "", "comma-separated exact upstream host:port authorities")
	caFile := flag.String("ca-file", "", "public ephemeral CA output path")
	flag.Parse()
	if !loopback(*listen) || !loopback(*control) {
		return errors.New("proxy and control listeners must use loopback IPs")
	}
	if *caFile == "" {
		return errors.New("ca-file required")
	}
	p, e := faultproxy.New(strings.Split(*hosts, ","), nil)
	if e != nil {
		return e
	}
	defer p.Close()
	if e = os.WriteFile(*caFile, p.CAPEM(), 0600); e != nil {
		return e
	}
	proxyListener, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	defer proxyListener.Close()
	controlListener, e := net.Listen("tcp", *control)
	if e != nil {
		return e
	}
	defer controlListener.Close()
	proxyServer := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	controlServer := &http.Server{Handler: p.ControlHandler(), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	failures := make(chan error, 2)
	go func() { failures <- proxyServer.Serve(proxyListener) }()
	go func() { failures <- controlServer.Serve(controlListener) }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("Proxy %s; control %s; public CA written. No system trust changed.\n", proxyListener.Addr(), controlListener.Addr())
	select {
	case <-ctx.Done():
	case e = <-failures:
	}
	p.Close()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	proxyServer.Shutdown(shutdown)
	controlServer.Shutdown(shutdown)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
