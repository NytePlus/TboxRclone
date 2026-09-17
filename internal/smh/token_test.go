package smh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func userFile(t *testing.T) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "user-token")
	if e := os.WriteFile(name, []byte("USER_SECRET"), 0600); e != nil {
		t.Fatal(e)
	}
	return name
}
func tokenResponse(w http.ResponseWriter, token, library, space string, ttl int) {
	json.NewEncoder(w).Encode(map[string]any{"libraryId": library, "spaceId": space, "accessToken": token, "expiresIn": ttl})
}
func TestTokenConcurrentRefreshAndExpiry(t *testing.T) {
	var calls atomic.Int32
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/user/v1/space/1/personal" || r.URL.Query().Get("user_token") != "USER_SECRET" {
			t.Error("wrong token request")
		}
		tokenResponse(w, "access", "lib", "space", 1800)
	})
	now := time.Now()
	p := &personalTokens{client: c, file: userFile(t), org: "1", now: func() time.Time { return now }}
	var wg sync.WaitGroup
	for n := 0; n < 32; n++ {
		wg.Go(func() {
			token, e := p.token(context.Background())
			if e != nil || token != "access" {
				t.Errorf("token refresh failed: %v", e)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("refresh not coalesced", calls.Load())
	}
	now = now.Add(29*time.Minute + time.Second)
	if _, e := p.token(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("lease not refreshed before expiry")
	}
}
func TestTokenRotationAndAccountIsolation(t *testing.T) {
	var calls atomic.Int32
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("user_token") == "ROTATED" {
			tokenResponse(w, "other-secret", "other", "space", 1800)
		} else {
			tokenResponse(w, "access", "lib", "space", 1800)
		}
	})
	name := userFile(t)
	if e := c.UseUserToken(name, "1"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Token(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(name, []byte("ROTATED"), 0600); e != nil {
		t.Fatal(e)
	}
	if token, e := c.Token(context.Background()); e == nil || token != "" || strings.Contains(e.Error(), "other-secret") {
		t.Fatal("switched account or leaked credentials")
	}
	if calls.Load() != 2 {
		t.Fatal("rotation not observed")
	}
}
func TestTokenUnauthorizedWriteNotReplayed(t *testing.T) {
	var refresh, writes atomic.Int32
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/user/") {
			refresh.Add(1)
			tokenResponse(w, "access", "lib", "space", 1800)
			return
		}
		if r.Method == "PUT" {
			writes.Add(1)
			w.WriteHeader(401)
			io.WriteString(w, "SECRET")
			return
		}
		io.WriteString(w, `{"type":"dir"}`)
	})
	if e := c.UseUserToken(userFile(t), "1"); e != nil {
		t.Fatal(e)
	}
	e := c.JSON(context.Background(), "PUT", "file", "test", nil, struct{}{}, nil)
	if !IsStatus(e, 401) || writes.Load() != 1 || refresh.Load() != 1 {
		t.Fatal("write replayed", e, writes.Load(), refresh.Load())
	}
	if _, e = c.Info(context.Background(), "test"); e != nil {
		t.Fatal(e)
	}
	if refresh.Load() != 2 || writes.Load() != 1 {
		t.Fatal("next caller did not refresh safely")
	}
}

type observedContext struct {
	context.Context
	observed chan struct{}
}

func (c observedContext) Done() <-chan struct{} {
	select {
	case c.observed <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestTokenWaitCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		tokenResponse(w, "access", "lib", "space", 1800)
	})
	if e := c.UseUserToken(userFile(t), "1"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := c.Token(context.Background()); done <- e }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan struct{}, 1)
	waiter := make(chan error, 1)
	go func() { _, e := c.Token(observedContext{ctx, observed}); waiter <- e }()
	<-observed // The second caller is waiting on the in-flight refresh.
	cancel()
	if e := <-waiter; !errors.Is(e, context.Canceled) {
		t.Fatal("waiter ignored cancellation", e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestTokenRejectsInvalidLeaseAndPrivateFile(t *testing.T) {
	for _, body := range []string{`{}`, `{"libraryId":"lib","spaceId":"space","accessToken":"secret","expiresIn":0}`, `{"libraryId":"lib","spaceId":"space","accessToken":"secret","expiresIn":999999999}`, `{"libraryId":"lib","spaceId":"wrong","accessToken":"secret","expiresIn":1800}`} {
		t.Run(body, func(t *testing.T) {
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if e := c.UseUserToken(userFile(t), "1"); e != nil {
				t.Fatal(e)
			}
			token, e := c.Token(context.Background())
			if e == nil || token != "" || strings.Contains(e.Error(), "secret") {
				t.Fatal("bad lease accepted or leaked")
			}
		})
	}
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unsafe credential file reached network") })
	name := userFile(t)
	if e := os.Chmod(name, 0644); e != nil {
		t.Fatal(e)
	}
	if e := c.UseUserToken(name, "1"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Token(context.Background()); e == nil {
		t.Fatal("public credential file accepted")
	}
}
