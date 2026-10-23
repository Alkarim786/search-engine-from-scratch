// Package crawler is a polite, concurrent, scope-limited web crawler.
//
// Politeness: robots.txt (cached per host, Crawl-delay honoured), a minimum
// delay between requests to one host, one in-flight request per host, an
// identifying User-Agent, a body-size cap and per-request timeouts.
package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/Alkarim786/search-engine-from-scratch/internal/dedup"
	"github.com/Alkarim786/search-engine-from-scratch/internal/frontier"
	"github.com/Alkarim786/search-engine-from-scratch/internal/parse"
	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
	"github.com/Alkarim786/search-engine-from-scratch/internal/urlnorm"
)

// DefaultUserAgent identifies the bot and tells site owners where to find us.
const DefaultUserAgent = "SearchEngineFromScratchBot/1.0 (+https://github.com/Alkarim786/search-engine-from-scratch)"

// Config controls one crawl.
type Config struct {
	Seeds        []string
	AllowHosts   []string      // host[:port] allowlist; defaults to the seeds' hosts
	PathPrefixes []string      // if set, only URLs whose path starts with one of these are crawled
	MaxPages     int           // stop after this many pages are stored (0 = 1000)
	MaxDepth     int           // link depth limit from the seeds (0 = 10)
	Workers      int           // concurrent fetchers (0 = 8)
	Delay        time.Duration // minimum gap between two requests to one host
	UserAgent    string
	Timeout      time.Duration // per request (0 = 15s)
	MaxBodyBytes int64         // 0 = 2 MiB
	Client       *http.Client  // optional; tests inject one
	Sink         Sink
	Logf         func(format string, args ...any)
}

// Page is one stored document (one line of pages.jsonl).
type Page struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	Links     []string  `json:"links,omitempty"` // canonical, in-scope outgoing links
	Depth     int       `json:"depth"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Sink receives stored pages. Implementations must be safe for concurrent use.
type Sink interface{ Write(Page) error }

// Stats summarises a crawl.
type Stats struct {
	Stored        int64         `json:"stored"`
	Fetched       int64         `json:"fetched"`
	RobotsBlocked int64         `json:"robots_blocked"`
	ExactDups     int64         `json:"exact_duplicates"`
	NearDups      int64         `json:"near_duplicates"`
	NonHTML       int64         `json:"non_html"`
	HTTPErrors    int64         `json:"http_errors"`
	NetErrors     int64         `json:"network_errors"`
	NoIndex       int64         `json:"noindex"`
	Bytes         int64         `json:"bytes"`
	URLsSeen      int           `json:"urls_seen"`
	Elapsed       time.Duration `json:"elapsed_ns"`
}

// PagesPerSecond is stored pages divided by wall-clock time.
func (s Stats) PagesPerSecond() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return float64(s.Stored) / s.Elapsed.Seconds()
}

type crawler struct {
	cfg      Config
	fr       *frontier.Frontier
	robots   *robotsCache
	dd       *dedup.Detector
	client   *http.Client
	allow    map[string]bool
	stats    Stats
	stored   atomic.Int64
	storeMu  sync.Mutex
	cancel   context.CancelFunc
	logEvery int64
}

var skipExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true, ".webp": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".tar": true,
	".css": true, ".js": true, ".json": true, ".xml": true, ".woff": true, ".woff2": true, ".ttf": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".exe": true, ".dmg": true, ".msi": true,
	".txt": true, ".epub": true, ".inv": true,
}

// Run crawls until the frontier is exhausted, MaxPages pages are stored or ctx
// is cancelled.
func Run(ctx context.Context, cfg Config) (*Stats, error) {
	if len(cfg.Seeds) == 0 {
		return nil, errors.New("crawler: no seeds")
	}
	if cfg.Sink == nil {
		return nil, errors.New("crawler: no sink")
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 1000
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 10
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 2 << 20
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &crawler{cfg: cfg, fr: frontier.New(), dd: dedup.New(), allow: map[string]bool{}, cancel: cancel, logEvery: 100}
	for _, h := range cfg.AllowHosts {
		c.allow[strings.ToLower(h)] = true
	}
	c.client = cfg.Client
	if c.client == nil {
		c.client = &http.Client{Timeout: cfg.Timeout}
	}
	// Redirects are not followed automatically: the target is re-queued so it
	// goes through the seen-set, scope check, robots.txt and the host delay.
	cl := *c.client
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.client = &cl
	c.robots = newRobotsCache(c.client, cfg.UserAgent)

	for _, s := range cfg.Seeds {
		cu, err := urlnorm.Canonical(s, nil)
		if err != nil {
			return nil, fmt.Errorf("bad seed %q: %w", s, err)
		}
		if len(cfg.AllowHosts) == 0 {
			u, _ := url.Parse(cu)
			c.allow[u.Host] = true
		}
		c.fr.Add(cu, 0)
	}

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, ok := c.fr.Next(ctx)
				if !ok {
					return
				}
				delay := c.process(ctx, it)
				c.fr.Done(it.Host, delay)
			}
		}()
	}
	wg.Wait()
	c.stats.Elapsed = time.Since(start)
	c.stats.Stored = c.stored.Load()
	c.stats.URLsSeen = c.fr.Seen()
	return &c.stats, nil
}

func (c *crawler) inScope(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if !c.allow[strings.ToLower(u.Host)] {
		return false
	}
	if len(c.cfg.PathPrefixes) > 0 {
		ok := false
		for _, p := range c.cfg.PathPrefixes {
			if strings.HasPrefix(u.Path, p) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return !skipExt[strings.ToLower(path.Ext(u.Path))]
}

// process fetches one URL and returns the delay to apply before the next
// request to the same host.
func (c *crawler) process(ctx context.Context, it frontier.Item) time.Duration {
	delay := c.cfg.Delay
	u, err := url.Parse(it.URL)
	if err != nil {
		return delay
	}
	rules := c.robots.get(ctx, u)
	if d := rules.CrawlDelay(); d > delay {
		delay = d
	}
	if !rules.Allowed(u.RequestURI()) {
		atomic.AddInt64(&c.stats.RobotsBlocked, 1)
		return 0
	}
	if int(c.stored.Load()) >= c.cfg.MaxPages {
		return 0
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.URL, nil)
	if err != nil {
		return delay
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			atomic.AddInt64(&c.stats.NetErrors, 1)
			c.cfg.Logf("fetch error %s: %v", it.URL, err)
		}
		return delay
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if cu, err := urlnorm.Canonical(resp.Header.Get("Location"), u); err == nil {
			if lu, _ := url.Parse(cu); c.inScope(lu) {
				c.fr.Add(cu, it.Depth)
			}
		}
		return delay
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		atomic.AddInt64(&c.stats.HTTPErrors, 1)
		return delay
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "html") {
		atomic.AddInt64(&c.stats.NonHTML, 1)
		return delay
	}
	final, finalURL := u, it.URL
	body := &countingReader{r: io.LimitReader(resp.Body, c.cfg.MaxBodyBytes)}
	r, err := charset.NewReader(body, ct)
	if err != nil {
		r = body
	}
	pg, err := parse.Parse(final, r)
	atomic.AddInt64(&c.stats.Bytes, body.n)
	if err != nil {
		atomic.AddInt64(&c.stats.NetErrors, 1)
		return delay
	}
	atomic.AddInt64(&c.stats.Fetched, 1)
	if pg.NoIndex {
		atomic.AddInt64(&c.stats.NoIndex, 1)
	}

	words := tokenize.Words(pg.Text)
	switch c.dd.Check(pg.Text, words) {
	case dedup.Exact:
		atomic.AddInt64(&c.stats.ExactDups, 1)
		return delay
	case dedup.Near:
		atomic.AddInt64(&c.stats.NearDups, 1)
		return delay
	}

	var links []string
	if !pg.NoFollow {
		seen := map[string]bool{}
		for _, l := range pg.Links {
			cl, err := urlnorm.Canonical(l, nil)
			if err != nil || seen[cl] {
				continue
			}
			lu, _ := url.Parse(cl)
			if !c.inScope(lu) {
				continue
			}
			seen[cl] = true
			links = append(links, cl)
		}
	}

	if !pg.NoIndex {
		c.storeMu.Lock()
		if int(c.stored.Load()) >= c.cfg.MaxPages {
			c.storeMu.Unlock()
			return delay
		}
		err := c.cfg.Sink.Write(Page{URL: finalURL, Title: pg.Title, Text: pg.Text, Links: links, Depth: it.Depth, FetchedAt: time.Now().UTC()})
		if err == nil {
			n := c.stored.Add(1)
			if n%c.logEvery == 0 {
				c.cfg.Logf("stored %d pages (queue %d)", n, c.fr.Pending())
			}
			if int(n) >= c.cfg.MaxPages {
				c.cancel()
			}
		} else {
			c.cfg.Logf("sink error: %v", err)
		}
		c.storeMu.Unlock()
	}
	if it.Depth < c.cfg.MaxDepth {
		for _, l := range links {
			c.fr.Add(l, it.Depth+1)
		}
	}
	return delay
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// JSONLSink appends pages to a file, one JSON object per line.
type JSONLSink struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewJSONLSink creates (truncating) the file at path.
func NewJSONLSink(path string) (*JSONLSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &JSONLSink{f: f, enc: json.NewEncoder(f)}, nil
}

// Write implements Sink.
func (s *JSONLSink) Write(p Page) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(p)
}

// Close flushes and closes the file.
func (s *JSONLSink) Close() error { return s.f.Close() }

// MemorySink collects pages in memory (tests).
type MemorySink struct {
	mu    sync.Mutex
	Pages []Page
}

// Write implements Sink.
func (m *MemorySink) Write(p Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Pages = append(m.Pages, p)
	return nil
}
