package smh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// personalTokens keeps short-lived credentials in memory, scoped to one Client.
// A rotated UserToken invalidates the cache; refresh cannot silently switch spaces.
type personalTokens struct {
	client     *Client
	file, org  string
	mu         sync.Mutex
	refreshing *tokenRefresh
	key        [32]byte
	value      string
	until      time.Time
	now        func() time.Time
}

type tokenRefresh struct {
	done chan struct{}
	key  [32]byte
	err  error
}

// UseUserToken enables personal-space access token renewal. The private file
// contains the existing UserToken only; interactive SSO is deliberately separate.
func (c *Client) UseUserToken(file, org string) error {
	if !filepath.IsAbs(file) || org == "" || strings.ContainsAny(org, "/\\?#") {
		return errors.New("user token file must be absolute and organization must be an identifier")
	}
	p := &personalTokens{client: c, file: file, org: org, now: time.Now}
	c.Token = p.token
	c.invalidateToken = p.invalidate
	return nil
}

func privateToken(file string) (string, error) {
	f, e := os.Open(file)
	if e != nil {
		return "", errors.New("cannot read user token file")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("user token file must be a private regular file (0600)")
	}
	b, e := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if e != nil || len(b) > 64<<10 {
		return "", errors.New("cannot read user token file")
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("empty user token file")
	}
	return token, nil
}

func (p *personalTokens) token(ctx context.Context) (string, error) {
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		user, e := privateToken(p.file)
		if e != nil {
			return "", e
		}
		key := sha256.Sum256([]byte(user))
		p.mu.Lock()
		if p.key == key && p.value != "" && p.now().Before(p.until) {
			value := p.value
			p.mu.Unlock()
			return value, nil
		}
		if pending := p.refreshing; pending != nil {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-pending.done:
				if pending.key == key && pending.err != nil {
					return "", pending.err
				}
				continue
			}
		}
		p.refreshing = &tokenRefresh{done: make(chan struct{}), key: key}
		p.mu.Unlock()
		// Measure validity from before dispatch; network latency consumes the lease.
		started := p.now()
		value, ttl, e := p.fetch(ctx, user)
		p.mu.Lock()
		if e == nil {
			margin := min(time.Minute, ttl/10)
			until := started.Add(ttl - margin)
			if !p.now().Before(until) {
				e = errors.New("refreshed access token already expired")
			} else {
				p.key = key
				p.value = value
				p.until = until
			}
		}
		if e != nil {
			p.value = ""
			p.until = time.Time{}
		}
		p.refreshing.err = e
		close(p.refreshing.done)
		p.refreshing = nil
		p.mu.Unlock()
		if e != nil {
			return "", e
		}
		return value, nil
	}
}
func (p *personalTokens) invalidate(value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// A late response for an older lease must not invalidate a newer credential.
	if p.value == value {
		p.value = ""
		p.until = time.Time{}
	}
}
func (p *personalTokens) fetch(ctx context.Context, user string) (string, time.Duration, error) {
	address := p.client.Endpoint + "/user/v1/space/" + url.PathEscape(p.org) + "/personal?" + url.Values{"user_token": {user}}.Encode()
	req, e := http.NewRequestWithContext(ctx, "POST", address, bytes.NewBufferString("{}"))
	if e != nil {
		return "", 0, ErrProtocol
	}
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = nil
	resp, e := p.client.HTTP.Do(req)
	if e != nil {
		return "", 0, safeTransportError(ctx, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", 0, &Error{resp.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if e != nil || len(b) > 64<<10 {
		return "", 0, ErrProtocol
	}
	var result struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
		Token   string `json:"accessToken"`
		Expires Int64  `json:"expiresIn"`
	}
	if json.Unmarshal(b, &result) != nil || result.Token == "" || result.Expires <= 0 || result.Expires > 86400 {
		return "", 0, ErrProtocol
	}
	if result.Library != p.client.Library || result.Space != p.client.Space {
		return "", 0, errors.New("refreshed credentials belong to a different account or space")
	}
	return result.Token, time.Duration(result.Expires) * time.Second, nil
}
