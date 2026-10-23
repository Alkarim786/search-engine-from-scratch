package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
)

func quiet(string, ...any) {}

func buildIndex(t *testing.T, dir string, docs [][3]string) {
	t.Helper()
	b, err := index.NewBuilder(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if _, err := b.Add(d[0], d[1], d[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := index.WritePageRank(dir, make([]float64, len(docs))); err != nil {
		t.Fatal(err)
	}
}

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	cfg.Logf = quiet
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); s.Close() })
	return s, ts
}

func getJSON(t *testing.T, u string, v any) int {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", u, err)
	}
	return resp.StatusCode
}

func TestSearchSuggestStats(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ix")
	var docs [][3]string
	for i := 0; i < 12; i++ {
		docs = append(docs, [3]string{fmt.Sprintf("http://x/%d", i), fmt.Sprintf("Python Tutorial %d — Docs", i), "learn python programming with lists and <b>dictionaries</b>"})
	}
	docs = append(docs, [3]string{"http://x/other", "Cooking", "pasta sauce recipes"})
	buildIndex(t, dir, docs)
	_, ts := newTestServer(t, Config{IndexDir: dir, DataDir: t.TempDir()})

	var sr SearchResponse
	if code := getJSON(t, ts.URL+"/search?q=dictionary+python&size=5", &sr); code != 200 {
		t.Fatalf("status %d", code)
	}
	if sr.Total != 12 || len(sr.Results) != 5 || sr.Page != 1 {
		t.Fatalf("resp = %+v", sr)
	}
	if sn := sr.Results[0].Snippet; !strings.Contains(sn, "<mark>dictionaries</mark>") || strings.Contains(sn, "<b>") {
		t.Errorf("snippet must highlight and escape: %q", sn)
	}
	var p3 SearchResponse
	getJSON(t, ts.URL+"/search?q=python&size=5&page=3", &p3)
	if len(p3.Results) != 2 {
		t.Errorf("page 3 has %d results, want 2", len(p3.Results))
	}
	var phrase SearchResponse
	getJSON(t, ts.URL+"/search?q="+url.QueryEscape(`"pasta sauce"`), &phrase)
	if phrase.Total != 1 || phrase.Results[0].URL != "http://x/other" {
		t.Errorf("phrase search = %+v", phrase)
	}

	var e map[string]string
	if code := getJSON(t, ts.URL+"/search", &e); code != 400 {
		t.Errorf("missing q -> %d", code)
	}
	if code := getJSON(t, ts.URL+"/search?q=x&pr=abc", &e); code != 400 {
		t.Errorf("bad pr -> %d", code)
	}

	// The queries above were logged as popular, and titles feed autocomplete.
	var sg struct{ Suggestions []string }
	getJSON(t, ts.URL+"/suggest?q=python+tu", &sg)
	if len(sg.Suggestions) == 0 || !strings.HasPrefix(sg.Suggestions[0], "python tutorial") {
		t.Errorf("suggestions = %v", sg.Suggestions)
	}
	getJSON(t, ts.URL+"/suggest?q=dict", &sg)
	if !strings.Contains(strings.Join(sg.Suggestions, "|"), "dictionaries") {
		t.Errorf("word completion = %v", sg.Suggestions)
	}

	var st map[string]any
	getJSON(t, ts.URL+"/stats", &st)
	if st["indexed"] != true || st["docs"].(float64) != 13 {
		t.Errorf("stats = %v", st)
	}

	resp, _ := http.Get(ts.URL + "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("Search Engine From Scratch")) {
		t.Error("UI not served")
	}
}

func TestNoIndexYet(t *testing.T) {
	_, ts := newTestServer(t, Config{IndexDir: filepath.Join(t.TempDir(), "none"), DataDir: t.TempDir()})
	var e map[string]string
	if code := getJSON(t, ts.URL+"/search?q=x", &e); code != 503 {
		t.Errorf("status %d", code)
	}
	var st map[string]any
	getJSON(t, ts.URL+"/stats", &st)
	if st["indexed"] != false {
		t.Errorf("stats = %v", st)
	}
}

func postCrawl(t *testing.T, ts *httptest.Server, token string, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/crawl", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestCrawlEndpointAuth(t *testing.T) {
	_, ts := newTestServer(t, Config{IndexDir: filepath.Join(t.TempDir(), "ix"), DataDir: t.TempDir()})
	if code, _ := postCrawl(t, ts, "", `{"seeds":["http://x"]}`); code != 403 {
		t.Errorf("no ADMIN_TOKEN configured -> %d, want 403", code)
	}
	_, ts2 := newTestServer(t, Config{IndexDir: filepath.Join(t.TempDir(), "ix"), DataDir: t.TempDir(), AdminToken: "s3cret"})
	if code, _ := postCrawl(t, ts2, "wrong", `{"seeds":["http://x"]}`); code != 401 {
		t.Errorf("wrong token -> %d, want 401", code)
	}
	if code, _ := postCrawl(t, ts2, "s3cret", `{"seeds":[]}`); code != 400 {
		t.Errorf("empty seeds -> %d, want 400", code)
	}
}

func TestAdminCrawlBuildsAndSwapsIndex(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<title>Home</title><p>welcome to the zebra sanctuary</p><a href="/a">a</a><a href="/b">b</a>`)
		case "/a":
			fmt.Fprint(w, `<title>Stripes</title><p>zebra stripes are unique like fingerprints</p><a href="/">home</a>`)
		case "/b":
			fmt.Fprint(w, `<title>Diet</title><p>zebras eat grass and the savanna is their home</p><a href="/a">a</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	dir := filepath.Join(t.TempDir(), "ix")
	buildIndex(t, dir, [][3]string{{"http://old/1", "Old", "an unrelated old document about llamas"}})
	_, ts := newTestServer(t, Config{IndexDir: dir, DataDir: t.TempDir(), AdminToken: "tok", CrawlDelay: time.Millisecond})

	var before SearchResponse
	getJSON(t, ts.URL+"/search?q=zebra", &before)
	if before.Total != 0 {
		t.Fatal("zebra should not be indexed yet")
	}
	code, body := postCrawl(t, ts, "tok", fmt.Sprintf(`{"seeds":[%q],"maxPages":10}`, site.URL+"/"))
	if code != 202 {
		t.Fatalf("crawl start -> %d %s", code, body)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var cs CrawlStatus
		getJSON(t, ts.URL+"/crawl/status", &cs)
		if cs.State == "done" {
			if cs.Crawl.Stored != 3 || cs.Index.Docs != 3 || cs.Index.Edges != 4 {
				t.Errorf("status = %+v / %+v", cs.Crawl, cs.Index)
			}
			break
		}
		if cs.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("crawl did not finish: %+v", cs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var after SearchResponse
	getJSON(t, ts.URL+"/search?q=zebra", &after)
	if after.Total != 3 {
		t.Fatalf("after swap: %+v", after)
	}
	var gone SearchResponse
	getJSON(t, ts.URL+"/search?q=llamas", &gone)
	if gone.Total != 0 {
		t.Error("old index should be replaced")
	}
}
