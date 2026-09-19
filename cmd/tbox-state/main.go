// Tbox-state inspects, resumes, reconciles, or explicitly cancels recorded uploads.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nyte/TboxRclone/internal/instance"
	"github.com/nyte/TboxRclone/internal/journal"
	"github.com/nyte/TboxRclone/internal/recovery"
	"github.com/nyte/TboxRclone/internal/smh"
	"github.com/nyte/TboxRclone/internal/transfer"
)

func run() error {
	dir := flag.String("state-dir", "", "private durable state directory")
	ownership := flag.String("ownership-dir", os.Getenv("RCLONE_SJTU_OWNERSHIP_DIR"), "shared cloud-space ownership registry (same as service; defaults to RCLONE_SJTU_OWNERSHIP_DIR)")
	endpoint := flag.String("endpoint", "https://pan.sjtu.edu.cn", "SMH origin")
	library := flag.String("library-id", "", "library ID")
	space := flag.String("space-id", "", "space ID")
	token := flag.String("token-file", "", "access token file")
	userToken := flag.String("user-token-file", "", "private UserToken file for personal-space token refresh")
	org := flag.String("organization-id", "1", "personal-space organization ID")
	id := flag.String("reconcile", "", "operation ID to reconcile with read-only requests")
	resume := flag.String("resume", "", "resume an existing isolated multipart upload; never reinitialize or repeat confirmation")
	abort := flag.String("abort", "", "cancel the recorded unpublished multipart session; retain local bytes")
	prune := flag.String("prune-committed", "", "explicitly release one committed operation spool; retain journal receipt; stop service first")
	flag.Parse()
	actions := 0
	for _, value := range []string{*id, *resume, *abort, *prune} {
		if value != "" {
			actions++
		}
	}
	if actions > 1 {
		return fmt.Errorf("choose one of reconcile, resume, abort, or prune-committed")
	}
	if actions != 0 && *prune == "" {
		c, err := smh.New(*endpoint, *library, *space, *token)
		if err != nil {
			return err
		}
		if err = instance.ClaimScope(*ownership, *dir, c.Endpoint+"/"+c.Library+"/"+c.Space); err != nil {
			return err
		}
	}
	s, e := journal.Open(*dir)
	if e != nil {
		return e
	}
	defer s.Close()
	if *prune != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if e := s.PruneCommitted(ctx, *prune); e != nil {
			return e
		}
		fmt.Printf("%s committed spool released; journal receipt retained\n", *prune)
		return nil
	}
	records, e := s.Records()
	if e != nil {
		return e
	}
	if actions == 0 {
		type summary struct {
			ID, State, Path, SHA256, Kind, SourcePath string
			Size                                      int64
			SpoolReleased                             bool
		}
		out := []summary{}
		for _, r := range records {
			out = append(out, summary{ID: r.ID, State: r.State, Path: r.Path, SHA256: r.SHA256, Kind: r.Kind, SourcePath: r.SourcePath, Size: r.Size, SpoolReleased: r.SpoolReleased})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	c, e := smh.New(*endpoint, *library, *space, *token)
	if e != nil {
		return e
	}
	if *userToken != "" {
		if e = c.UseUserToken(*userToken, *org); e != nil {
			return e
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	for _, r := range records {
		if r.ID == *id || r.ID == *resume || r.ID == *abort {
			if *abort != "" {
				e = transfer.Abort(ctx, s, c, &r)
			} else if *resume != "" {
				e = transfer.Resume(ctx, s, c, &r)
			} else {
				e = recovery.Reconcile(ctx, s, c, &r)
			}
			if e != nil {
				return e
			}
			if r.Kind == "dirmove" {
				fmt.Printf("%s %s; complete directory content tar backup retained\n", r.ID, r.State)
			} else if r.Kind == "delete" || r.Kind == "rmdir" {
				fmt.Printf("%s %s; deletion intent retained (not a content backup); recycle receipt present: %t\n", r.ID, r.State, r.RecycledID != "")
			} else {
				fmt.Printf("%s %s; local spool retained\n", r.ID, r.State)
			}
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
