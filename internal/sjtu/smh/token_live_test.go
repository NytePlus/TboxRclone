package smh

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveTokenExpiry uses the real clock and deployment. It is opt-in because
// the client must cross the advertised 1800-second lease while proving that it
// rotated credentials and continued to read normally.
func TestLiveTokenExpiry(t *testing.T) {
	if os.Getenv("TBOX_AUTH_SOAK") != "1" {
		t.Skip("set TBOX_AUTH_SOAK=1 with private live credentials for a 31-minute read-only check")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	b, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil {
		t.Fatal("cannot read private space identity file")
	}
	if json.Unmarshal(b, &ids) != nil {
		t.Fatal("invalid private space identity file")
	}
	c, err := New("https://pan.sjtu.edu.cn", ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.UseUserToken(os.Getenv("TBOX_USER_TOKEN_FILE"), "1"); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("TBOX_AUTH_PATH")
	if ValidatePath(path) != nil || !strings.HasPrefix(path, "codex-api-lab/") {
		t.Fatal("explicit valid lab path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 33*time.Minute)
	defer cancel()
	start := time.Now()
	original, err := c.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous := original
	changes := 0
	reads := 0
	for {
		item, err := c.Info(ctx, path)
		if err != nil {
			t.Fatalf("read %d failed: %v", reads+1, err)
		}
		if item.Type != "dir" {
			t.Fatal("expected isolated directory")
		}
		current, err := c.Token(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if current != previous {
			changes++
			previous = current
			t.Logf("token changed after %s", time.Since(start).Round(time.Second))
		}
		reads++
		t.Logf("elapsed=%s successful_reads=%d token_changes=%d", time.Since(start).Round(time.Second), reads, changes)
		if time.Since(start) >= 31*time.Minute {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(30 * time.Second):
		}
	}
	if changes < 1 {
		t.Fatal("no observed token refresh")
	}
	current, err := c.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current == original {
		t.Fatal("client reused the original token beyond its advertised lease")
	}
	t.Logf("client rotated credentials and completed %d successful reads across the advertised expiry", reads)
}
