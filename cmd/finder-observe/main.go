// finder-observe independently samples cloud content during a real Finder copy.
// It performs reads only and does not drive or replace the UI operation.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nyte/TboxRclone/internal/sjtu/defaults"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
)

func main() {
	target := flag.String("path", "", "isolated cloud path")
	expected := flag.String("sha256", "", "expected complete SHA256")
	size := flag.Int64("size", -1, "expected bytes")
	flag.Parse()
	if !strings.HasPrefix(*target, "codex-api-lab/") || len(*expected) != 64 || *size < 0 {
		fmt.Fprintln(os.Stderr, "invalid fixture")
		os.Exit(2)
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile("/secrets/space.json")
	if err == nil {
		err = json.Unmarshal(b, &ids)
	}
	if err != nil {
		panic(err)
	}
	c, err := smh.New(defaults.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		panic(err)
	}
	if err = c.UseUserToken("/secrets/user-token", "1"); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	enc := json.NewEncoder(os.Stdout)
	absent, complete, invalid, failures := 0, 0, 0, 0
	for ctx.Err() == nil {
		row := map[string]any{"at": time.Now().UTC(), "path": *target}
		item, e := c.Info(ctx, *target)
		switch {
		case smh.IsStatus(e, 404):
			row["status"] = "absent"
			absent++
		case e != nil:
			row["status"] = "read_error"
			failures++
		default:
			r, e := c.Open(ctx, *target, item, 0, -1)
			if e != nil {
				row["status"] = "read_error"
				failures++
			} else {
				h := sha256.New()
				n, e := io.Copy(h, r)
				ce := r.Close()
				digest := hex.EncodeToString(h.Sum(nil))
				row["bytes"] = n
				row["sha256"] = digest
				if e != nil || ce != nil {
					row["status"] = "read_error"
					failures++
				} else if n == *size && digest == *expected {
					row["status"] = "complete"
					complete++
				} else {
					row["status"] = "invalid"
					invalid++
				}
			}
		}
		_ = enc.Encode(row)
		if complete >= 2 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	pass := absent > 0 && complete >= 2 && invalid == 0 && failures == 0
	_ = enc.Encode(map[string]any{"summary": true, "pass": pass, "absent": absent, "complete": complete, "invalid": invalid, "read_errors": failures, "limit": "finite independent observations, not a formal atomicity proof"})
	if !pass {
		os.Exit(1)
	}
}
