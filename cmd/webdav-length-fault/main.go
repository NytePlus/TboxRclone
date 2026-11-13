// Webdav-length-fault sends malformed request bodies to the isolated loopback
// WebDAV service and proves that no formal object is published.
package main

import (
	"bufio"
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

	"github.com/nyte/TboxRclone/internal/verifyutil"
)

type caseResult struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	ResponseLine string `json:"response_line,omitempty"`
	HeadStatus   int    `json:"head_status"`
	Published    bool   `json:"published"`
	Pass         bool   `json:"pass"`
}

type report struct {
	At    string       `json:"at"`
	Base  string       `json:"base"`
	Cases []caseResult `json:"cases"`
	Pass  bool         `json:"pass"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	origin := flag.String("url", "http://127.0.0.1:8686/", "loopback WebDAV origin")
	base := flag.String("base", "", "fresh isolated fixture directory")
	writes := flag.Bool("allow-lab-writes", false, "send malformed writes to the isolated fixture")
	flag.Parse()
	u, err := verifyutil.LabOrigin(*origin, *base, *writes)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Minute}
	if err = verifyutil.FreshCollection(client, u, *base); err != nil {
		return err
	}
	cases := []struct {
		id, name, headers, body string
	}{
		{"short_eof", "short-eof.bin", "Content-Length: 10\r\n", "short"},
		{"excess_bytes", "excess-bytes.bin", "Transfer-Encoding: chunked\r\n", "3\r\nabcd\r\n0\r\n\r\n"},
		{"reader_failure", "reader-failure.bin", "Transfer-Encoding: chunked\r\n", "A\r\nshort"},
	}
	out := report{At: time.Now().UTC().Format(time.RFC3339), Base: *base, Pass: true}
	for _, tc := range cases {
		remote := *base + "/" + tc.name
		line, sendErr := sendMalformed(u, remote, tc.headers, tc.body)
		if sendErr != nil {
			return fmt.Errorf("%s transport setup failed: %w", tc.id, sendErr)
		}
		status, published, checkErr := verifyutil.WaitSettled(client, u, remote)
		item := caseResult{ID: tc.id, Path: remote, ResponseLine: line, HeadStatus: status, Published: published, Pass: checkErr == nil && !published && status == http.StatusNotFound}
		out.Cases = append(out.Cases, item)
		out.Pass = out.Pass && item.Pass
	}
	if err = verifyutil.WriteJSON(os.Stdout, out); err != nil {
		return err
	}
	if !out.Pass {
		return errors.New("one or more malformed inputs published a formal object")
	}
	return nil
}

func sendMalformed(origin *url.URL, remote, headers, body string) (string, error) {
	address := origin.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(origin.Hostname(), "80")
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	requestPath := strings.TrimRight(origin.EscapedPath(), "/") + "/" + strings.TrimLeft(remote, "/")
	wire := "PUT " + requestPath + " HTTP/1.1\r\nHost: " + origin.Host + "\r\n" + headers + "Connection: close\r\n\r\n" + body
	if _, err = io.WriteString(conn, wire); err != nil {
		return "", err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	line, readErr := bufio.NewReader(conn).ReadString('\n')
	if readErr != nil && line == "" {
		// A server may close a malformed request without a response. Publication
		// state, checked independently below, is the acceptance signal.
		return "connection closed without response", nil
	}
	return strings.TrimSpace(line), nil
}
