package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func htmlPage(title, body string, links ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<html><head><title>%s</title></head><body><p>%s</p>", title, body)
	for _, l := range links {
		fmt.Fprintf(&sb, `<a href="%s">link</a>`, l)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

// site is a tiny local web site that records every request it receives.
type site struct {
	*httptest.Server
	mu    sync.Mutex
	hits  map[string][]time.Time
	agent map[string]string
}

func newSite(t *testing.T, robots string, pages map[string]string, external string) *site {
	t.Helper()
	s := &site{hits: map[string][]time.Time{}, agent: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if robots == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, robots)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		if r.URL.Path == "/to-external" {
			http.Redirect(w, r, external, http.StatusFound)
			return
		}
		if r.URL.Path == "/image.png" {
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "not html")
			return
		}
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *site) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[r.URL.Path] = append(s.hits[r.URL.Path], time.Now())
	s.agent[r.URL.Path] = r.Header.Get("User-Agent")
}

func (s *site) hostOf() string { u, _ := url.Parse(s.URL); return u.Host }

func urls(m *MemorySink) []string {
	var out []string
	for _, p := range m.Pages {
		out = append(out, p.URL)
	}
	sort.Strings(out)
	return out
}

func TestCrawlFollowsLinksDedupsAndRespectsRobots(t *testing.T) {
	other := newSite(t, "", map[string]string{"/": htmlPage("Elsewhere", "external site")}, "")
	pages := map[string]string{
		"/":               htmlPage("Home", "welcome home", "/a", "/b#frag", "/a?utm_source=x", "/private/secret", "/image.png", "/missing", other.URL+"/"),
		"/a":              htmlPage("A", "page a is about apples", "/", "/b", "/redirect"),
		"/b":              htmlPage("B", "page b is about bananas", "/c"),
		"/c":              htmlPage("C", "page c is about cherries"),
		"/private/secret": htmlPage("Secret", "must not be fetched"),
	}
	s := newSite(t, "User-agent: *\nDisallow: /private/\n", pages, other.URL+"/")
	sink := &MemorySink{}
	st, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, Sink: sink, Workers: 4, MaxPages: 50})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{s.URL + "/", s.URL + "/a", s.URL + "/b", s.URL + "/c"}
	if got := urls(sink); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stored %v\nwant   %v", got, want)
	}
	if len(s.hits["/private/secret"]) != 0 {
		t.Error("robots.txt disallowed path was fetched")
	}
	if len(other.hits["/"]) != 0 {
		t.Error("crawler left the allowlisted host")
	}
	if n := len(s.hits["/b"]); n != 1 {
		t.Errorf("/b fetched %d times (fragment/redirect must dedup)", n)
	}
	if st.RobotsBlocked != 1 {
		t.Errorf("robots blocked = %d", st.RobotsBlocked)
	}
	if len(s.hits["/image.png"]) != 0 || st.HTTPErrors != 1 { // image skipped by extension; /missing is a 404
		t.Errorf("stats = %+v", st)
	}
	if ua := s.agent["/a"]; !strings.Contains(ua, "SearchEngineFromScratchBot") || !strings.Contains(ua, "github.com") {
		t.Errorf("user agent %q does not identify the bot", ua)
	}
	// The stored link graph keeps in-scope canonical links only.
	for _, p := range sink.Pages {
		if p.URL == s.URL+"/" {
			if len(p.Links) != 4 { // /a, /b, /private/secret (robots-blocked), /missing (404)
				t.Errorf("home links = %v", p.Links)
			}
		}
	}
}

func TestPoliteDelayBetweenRequests(t *testing.T) {
	pages := map[string]string{
		"/":  htmlPage("Home", "home", "/1", "/2", "/3"),
		"/1": htmlPage("One", "unique one one"),
		"/2": htmlPage("Two", "unique two two"),
		"/3": htmlPage("Three", "unique three three"),
	}
	s := newSite(t, "User-agent: *\nCrawl-delay: 0.1\n", pages, "")
	sink := &MemorySink{}
	_, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, Sink: sink, Workers: 8, Delay: 0, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.Pages) != 4 {
		t.Fatalf("stored %d", len(sink.Pages))
	}
	var all []time.Time
	for p, ts := range s.hits {
		if p != "/robots.txt" {
			all = append(all, ts...)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Before(all[j]) })
	for i := 1; i < len(all); i++ {
		if gap := all[i].Sub(all[i-1]); gap < 90*time.Millisecond {
			t.Errorf("requests %d and %d only %v apart; Crawl-delay is 100ms", i-1, i, gap)
		}
	}
}

func TestMaxPagesAndDepthAndPrefix(t *testing.T) {
	pages := map[string]string{
		"/":       htmlPage("Home", "home page", "/docs/1", "/blog/1"),
		"/docs/1": htmlPage("Doc1", "doc one", "/docs/2"),
		"/docs/2": htmlPage("Doc2", "doc two", "/docs/3"),
		"/docs/3": htmlPage("Doc3", "doc three"),
		"/blog/1": htmlPage("Blog1", "blog one"),
	}
	s := newSite(t, "", pages, "")

	sink := &MemorySink{}
	if _, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/docs/1"}, PathPrefixes: []string{"/docs/"}, Sink: sink, MaxPages: 50}); err != nil {
		t.Fatal(err)
	}
	if len(sink.Pages) != 3 {
		t.Errorf("prefix crawl stored %v", urls(sink))
	}

	sink = &MemorySink{}
	if _, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, MaxDepth: 1, Sink: sink, MaxPages: 50}); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.Pages); got != 3 { // home + depth-1 pages (/docs/1, /blog/1)
		t.Errorf("depth-limited crawl stored %v", urls(sink))
	}

	sink = &MemorySink{}
	st, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, Sink: sink, MaxPages: 2, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.Pages) != 2 || st.Stored != 2 {
		t.Errorf("max-pages crawl stored %d", len(sink.Pages))
	}
}

func TestDuplicateContentIsDropped(t *testing.T) {
	long := strings.Repeat("the same long article text repeats here with many words to exceed the simhash threshold. ", 8)
	pages := map[string]string{
		"/":      htmlPage("Home", "home", "/copy1", "/copy2", "/other"),
		"/copy1": htmlPage("Article", long),
		"/copy2": htmlPage("Article mirror", long),
		"/other": htmlPage("Other", "something completely different"),
	}
	s := newSite(t, "", pages, "")
	sink := &MemorySink{}
	st, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, Sink: sink, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.Pages) != 3 || st.ExactDups+st.NearDups != 1 {
		t.Errorf("stored %v, stats %+v", urls(sink), st)
	}
}

func TestRedirectOutOfScopeIsNotFollowed(t *testing.T) {
	other := newSite(t, "", map[string]string{"/": htmlPage("Elsewhere", "external")}, "")
	pages := map[string]string{"/": htmlPage("Home", "home", "/to-external")}
	s := newSite(t, "", pages, other.URL+"/")
	sink := &MemorySink{}
	if _, err := Run(context.Background(), Config{Seeds: []string{s.URL + "/"}, Sink: sink, MaxPages: 10}); err != nil {
		t.Fatal(err)
	}
	if len(other.hits["/"]) != 0 {
		t.Error("redirect escaped the allowlist")
	}
}

func TestRobots5xxMeansDisallow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.Error(w, "boom", 503)
			return
		}
		fmt.Fprint(w, htmlPage("x", "x"))
	}))
	defer srv.Close()
	sink := &MemorySink{}
	st, _ := Run(context.Background(), Config{Seeds: []string{srv.URL + "/"}, Sink: sink})
	if len(sink.Pages) != 0 || st.RobotsBlocked != 1 {
		t.Errorf("stored %d, stats %+v", len(sink.Pages), st)
	}
}
