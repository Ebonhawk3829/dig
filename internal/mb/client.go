// Package mb is a Go client for the MusicBrainz ws/2 API with a SQLite cache
// and client-side rate limiting (1 req/s).
package mb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Ebonhawk3829/dig/internal/store"
)

// Client talks to MusicBrainz (or a mirror) through a shared cache.
type Client struct {
	baseURL    string
	userAgent  string
	ttlSearch  time.Duration
	ttlDetail  time.Duration
	cache      *store.Cache
	http       *http.Client
	mu         sync.Mutex
	lastReq    time.Time
}

// New creates a MusicBrainz client.
func New(baseURL, userAgent string, ttlSearch, ttlDetail time.Duration, cache *store.Cache) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
		ttlSearch: ttlSearch,
		ttlDetail: ttlDetail,
		cache:     cache,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// get fetches path (with query) through the cache. ttl selects the cache TTL.
func (c *Client) get(ctx context.Context, path string, query url.Values, ttl time.Duration, out any) error {
	cacheKey := path + "?" + query.Encode()

	// cache lookup
	if c.cache != nil {
		hit, err := c.cache.Get("mb:"+cacheKey, out)
		if err != nil {
			return err // cache errors are not fatal for correctness; but surface for now
		}
		if hit {
			return nil
		}
	}

	// HARD CAP: 1 request per second, strictly serialized. The mutex is held
	// across the entire request/response cycle so only one MB request is ever
	// in flight, and lastReq is stamped AFTER the response arrives. This is
	// deliberately conservative — MetaBrainz blocks cloud IPs that trip their
	// limiter, and a block is far more expensive than the latency.
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := time.Until(c.lastReq.Add(time.Second)); wait > 0 {
		// sleeping while holding the lock IS the point: it serializes callers
		// so no second request can start until this one finishes (+1s gap).
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	c.lastReq = time.Now()
	if err != nil {
		return fmt.Errorf("musicbrainz %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("musicbrainz %s: HTTP %d: %s", path, resp.StatusCode, string(b))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("musicbrainz %s: decode: %w", path, err)
	}

	if c.cache != nil {
		_ = c.cache.Set("mb:"+cacheKey, out, ttl) // best-effort
	}
	return nil
}

// ErrNotFound is returned for 404s.
var ErrNotFound = fmt.Errorf("musicbrainz: not found")

// ErrRateLimited is returned when MB returns 503/429 after retries.
var ErrRateLimited = fmt.Errorf("musicbrainz is busy (rate limited) — try again shortly")

// IsRateLimited reports whether err is a rate-limit response.
func IsRateLimited(err error) bool { return err == ErrRateLimited }

// WithRetry runs fn, retrying on rate-limit responses with a short backoff
// (5s, 7s, 10s — ~22s worst case). MetaBrainz's per-IP limiter trips on
// bursts; a brief wait almost always clears it, which beats failing the page.
func WithRetry(ctx context.Context, fn func() error) error {
	waits := []time.Duration{5 * time.Second, 7 * time.Second, 10 * time.Second}
	err := fn()
	for _, w := range waits {
		if err == nil || err != ErrRateLimited {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w):
		}
		err = fn()
	}
	return err
}

// Ping checks MusicBrainz reachability with a minimal request.
func (c *Client) Ping(ctx context.Context) error {
	var out struct{}
	v := url.Values{}
	v.Set("query", "dig")
	v.Set("limit", "1")
	v.Set("fmt", "json")
	return c.get(ctx, "/artist", v, c.ttlSearch, &out)
}
