package smh

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveReadVersionChange mutates only its own randomly named lab fixture to
// simulate an independent writer between range reads. Files are retained.
func TestLiveReadVersionChange(t *testing.T) {
	if os.Getenv("TBOX_LIVE_READ_CHANGE") != "1" {
		t.Skip("opt-in live isolated fixture overwrite experiment")
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	path := root + "/read-change-" + hex.EncodeToString(id[:]) + ".bin"
	old := bytes.Repeat([]byte("old-version:"), 1024)
	next := bytes.Repeat([]byte("new-version:"), 1024)
	publish := func(data []byte, strategy string) {
		t.Helper()
		u, err := c.Multipart(ctx, path, int64(len(data)), 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.PutPart(ctx, u, 1, bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
		if err = c.JSON(ctx, "POST", "file", u.Key, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {strategy}}, struct{}{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	read := func(item Item, start, length int64) []byte {
		t.Helper()
		r, err := c.Open(ctx, path, item, start, length)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil {
			t.Fatal("read failed", err, closeErr)
		}
		return data
	}
	publish(old, "ask")
	before, err := c.Info(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(before, 0, 4096), old[:4096]) {
		t.Fatal("initial range mismatch")
	}
	publish(next, "overwrite")
	after, err := c.Info(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if before.ETag == after.ETag {
		t.Fatal("changed bytes did not change validator")
	}
	r, err := c.Open(ctx, path, before, 4096, 4096)
	if r != nil {
		r.Close()
		t.Fatal("stale object exposed a content stream")
	}
	if err == nil || (!IsStatus(err, 412) && !strings.Contains(err.Error(), "content validator changed")) {
		t.Fatalf("did not establish validator rejection: %v", err)
	}
	if !bytes.Equal(read(after, 0, -1), next) {
		t.Fatal("fresh full read mismatch")
	}
	if !bytes.Equal(read(after, 4096, 4096), next[4096:8192]) {
		t.Fatal("fresh range mismatch")
	}
	t.Logf("fixture=%s old_sha256=%x new_sha256=%x stale_range_rejected=true fresh_reads_match=true", path, sha256.Sum256(old), sha256.Sum256(next))
}
