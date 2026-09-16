// Tbox-state lists pending uploads and reconciles submitted commits without replaying writes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
)

func run() error {
	dir := flag.String("state-dir", "", "private durable state directory")
	endpoint := flag.String("endpoint", "https://pan.sjtu.edu.cn", "SMH origin")
	library := flag.String("library-id", "", "library ID")
	space := flag.String("space-id", "", "space ID")
	token := flag.String("token-file", "", "access token file")
	id := flag.String("reconcile", "", "operation ID to reconcile with read-only requests")
	flag.Parse()
	s, e := journal.Open(*dir)
	if e != nil {
		return e
	}
	defer s.Close()
	records, e := s.Records()
	if e != nil {
		return e
	}
	if *id == "" {
		type summary struct {
			ID, State, Path, SHA256 string
			Size                    int64
		}
		out := []summary{}
		for _, r := range records {
			out = append(out, summary{r.ID, r.State, r.Path, r.SHA256, r.Size})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	c, e := smh.New(*endpoint, *library, *space, *token)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	for _, r := range records {
		if r.ID == *id {
			if e = recovery.Reconcile(ctx, s, c, &r); e != nil {
				return e
			}
			fmt.Printf("%s %s; local spool retained\n", r.ID, r.State)
			return nil
		}
	}
	return fmt.Errorf("operation ID not found")
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
