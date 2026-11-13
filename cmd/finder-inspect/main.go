// finder-inspect independently inventories a test directory through the cloud API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/nyte/TboxRclone/internal/sjtu/smh"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	spaceFile := flag.String("space-file", "/secrets/space.json", "SMH library and space JSON")
	tokenFile := flag.String("token-file", "/secrets/user-token", "SMH user token file")
	flag.Parse()
	if flag.NArg() != 1 || !strings.HasPrefix(flag.Arg(0), "codex-api-lab/") {
		return fmt.Errorf("isolated cloud directory required")
	}
	root := flag.Arg(0)
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, e := os.ReadFile(*spaceFile)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &ids); e != nil {
		return e
	}
	c, e := smh.New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if e != nil {
		return e
	}
	if e = c.UseUserToken(*tokenFile, "1"); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files := map[string]any{}
	dirs := []string{}
	var walk func(string) error
	walk = func(rel string) error {
		p := root
		if rel != "" {
			p += "/" + rel
		}
		items, e := c.List(ctx, p)
		if e != nil {
			return e
		}
		for _, item := range items {
			n := item.Name
			if rel != "" {
				n = rel + "/" + n
			}
			if item.Type == "dir" {
				dirs = append(dirs, n)
				if e = walk(n); e != nil {
					return e
				}
				continue
			}
			info, e := c.Info(ctx, root+"/"+n)
			if e != nil {
				return e
			}
			r, e := c.Open(ctx, root+"/"+n, info, 0, -1)
			if e != nil {
				return e
			}
			h := sha256.New()
			size, e := io.Copy(h, r)
			ce := r.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			files[n] = map[string]any{"size": size, "sha256": hex.EncodeToString(h.Sum(nil))}
		}
		return nil
	}
	if e = walk(""); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"root": root, "directories": dirs, "files": files, "method": "direct cloud full reads, bypass WebDAV"})
}
