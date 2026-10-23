package crawler

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Alkarim786/search-engine-from-scratch/internal/robots"
)

// robotsCache fetches and caches robots.txt once per scheme+host.
// Missing (4xx) -> allow everything; server error or network failure -> treat
// the site as disallowed for this crawl (the conservative reading of RFC 9309).
type robotsCache struct {
	client *http.Client
	agent  string
	mu     sync.Mutex
	cache  map[string]*robots.Rules
}

func newRobotsCache(c *http.Client, agent string) *robotsCache {
	return &robotsCache{client: c, agent: agent, cache: map[string]*robots.Rules{}}
}

func (rc *robotsCache) get(ctx context.Context, u *url.URL) *robots.Rules {
	key := u.Scheme + "://" + u.Host
	rc.mu.Lock()
	defer rc.mu.Unlock() // serialises the (rare) first fetch per host; later hits are map reads
	if r, ok := rc.cache[key]; ok {
		return r
	}
	r := rc.fetch(ctx, key)
	rc.cache[key] = r
	return r
}

func (rc *robotsCache) fetch(ctx context.Context, origin string) *robots.Rules {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return robots.DisallowAll()
	}
	req.Header.Set("User-Agent", rc.agent)
	resp, err := rc.client.Do(req)
	if err != nil {
		return robots.DisallowAll()
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return robots.DisallowAll()
	case resp.StatusCode >= 400:
		return robots.AllowAll()
	case resp.StatusCode != http.StatusOK:
		return robots.AllowAll()
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10)) // RFC 9309: parse at least 500 KiB
	return robots.Parse(string(b), productToken(rc.agent))
}

// productToken returns "SearchEngineFromScratchBot" from "SearchEngineFromScratchBot/1.0 (+...)".
func productToken(ua string) string {
	if i := strings.IndexAny(ua, "/ "); i > 0 {
		return ua[:i]
	}
	return ua
}
