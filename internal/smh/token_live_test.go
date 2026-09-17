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
// the first token must outlive its advertised 1800-second lease before checking
// that the original token is rejected and the same Client still reads normally.
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
	old, err := New(c.Endpoint, ids.Library, ids.Space, "")
	if err != nil {
		t.Fatal(err)
	}
	old.Token = func(context.Context) (string, error) { return original, nil }
	_, err = old.Info(ctx, path)
	if !IsStatus(err, 401) && !IsStatus(err, 403) {
		t.Fatalf("original token was not rejected as expired: %v", err)
	}
	t.Logf("original token rejected; same client completed %d successful reads across real expiry", reads)
}
