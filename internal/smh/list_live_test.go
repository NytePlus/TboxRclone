package smh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveDirectoryBoundaries checks exact stable sets on both the production
// listing path and forced 50-entry pages. Only generated directories are created.
func TestLiveDirectoryBoundaries(t *testing.T) {
	if os.Getenv("TBOX_LIVE_LIST") != "1" {
		t.Skip("opt-in live 1000-directory lab fixture")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil {
		t.Fatal("cannot read private space identity")
	}
	if json.Unmarshal(b, &ids) != nil {
		t.Fatal("invalid private space identity")
	}
	root := os.Getenv("TBOX_AUTH_PATH")
	if ValidatePath(root) != nil || !strings.HasPrefix(root, "codex-api-lab/") {
		t.Fatal("isolated lab root required")
	}
	c, err := New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	path := root + "/list-boundaries-" + hex.EncodeToString(id[:])
	mkdir := func(p string) {
		t.Helper()
		if err := c.JSON(ctx, "PUT", "directory", p, url.Values{"conflict_resolution_strategy": {"ask"}}, struct{}{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	mkdir(path)
	count := 0
	for _, want := range []int{0, 1, 50, 51, 1000} {
		for count < want {
			mkdir(fmt.Sprintf("%s/dir-%04d", path, count))
			count++
		}
		check := func(items []Item) {
			t.Helper()
			if len(items) != want {
				t.Fatalf("want=%d got=%d", want, len(items))
			}
			seen := map[string]bool{}
			for _, item := range items {
				if seen[item.Name] || item.Type != "dir" {
					t.Fatalf("invalid or duplicate generated item %q", item.Name)
				}
				seen[item.Name] = true
			}
			for n := 0; n < want; n++ {
				if !seen[fmt.Sprintf("dir-%04d", n)] {
					t.Fatalf("missing item %d", n)
				}
			}
		}
		items, err := c.List(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		check(items)
		var paged []Item
		marker := ""
		seen := map[string]bool{}
		pages := 0
		for {
			var page struct {
				Contents []Item     `json:"contents"`
				Next     pageMarker `json:"nextMarker"`
			}
			if err := c.JSON(ctx, "GET", "directory", path, url.Values{"limit": {"50"}, "marker": {marker}}, nil, &page); err != nil {
				t.Fatal(err)
			}
			pages++
			if page.Contents == nil || len(page.Contents) > 50 {
				t.Fatal("invalid page")
			}
			paged = append(paged, page.Contents...)
			if page.Next == "" {
				break
			}
			if seen[string(page.Next)] || pages > 21 {
				t.Fatal("nonterminating pagination")
			}
			seen[string(page.Next)] = true
			marker = string(page.Next)
		}
		check(paged)
		t.Logf("fixture=%s entries=%d exact_set=true limit50_pages=%d", path, want, pages)
	}
}
