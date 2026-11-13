package smh

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ignoreRangeTransport simulates a data endpoint that ignores a requested
// Range. The real response still comes from the live endpoint; only the two
// conditional request headers are removed at the external-event boundary.
type ignoreRangeTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	hits int
}

func (t *ignoreRangeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet && request.Header.Get("Range") != "" {
		clone := request.Clone(request.Context())
		clone.Header = request.Header.Clone()
		clone.Header.Del("Range")
		clone.Header.Del("If-Match")
		t.mu.Lock()
		t.hits++
		t.mu.Unlock()
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(request)
}

func TestLiveIgnoredRangeRejected(t *testing.T) {
	if os.Getenv("TBOX_LIVE_IGNORED_RANGE") != "1" {
		t.Skip("opt-in live isolated ignored-Range experiment")
	}
	var ids struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
	}
	data, err := os.ReadFile(os.Getenv("TBOX_SPACE_FILE"))
	if err != nil || json.Unmarshal(data, &ids) != nil {
		t.Fatal("private identity unavailable")
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	fixture := root + "/ignored-range-" + hex.EncodeToString(nonce) + ".bin"
	payload := bytes.Repeat([]byte("range-must-not-be-ignored:"), 512)
	upload, err := c.Multipart(ctx, fixture, int64(len(payload)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutPart(ctx, upload, 1, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if err = c.JSON(ctx, "POST", "file", upload.Key, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {"ask"}}, struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	item, err := c.Info(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	fault := &ignoreRangeTransport{base: base}
	c.HTTP.Transport = fault
	reader, err := c.Open(ctx, fixture, item, 128, 4096)
	if reader != nil {
		reader.Close()
		t.Fatal("ignored Range exposed a content stream")
	}
	fault.mu.Lock()
	hits := fault.hits
	fault.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "incorrect range response") || hits < 1 {
		t.Fatalf("ignored Range not rejected: err=%v hits=%d", err, hits)
	}
	sum := sha256.Sum256(payload)
	t.Logf("fixture=%s size=%d sha256=%x ignored_range_injected=true ignored_range_rejected=true", fixture, len(payload), sum)
}

var _ http.RoundTripper = (*ignoreRangeTransport)(nil)
