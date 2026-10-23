// Package server exposes the search engine over HTTP: /search, /suggest,
// /stats, an admin /crawl endpoint and the embedded web UI.
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/crawler"
	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
	"github.com/Alkarim786/search-engine-from-scratch/internal/pipeline"
	"github.com/Alkarim786/search-engine-from-scratch/internal/search"
	"github.com/Alkarim786/search-engine-from-scratch/internal/snippet"
	"github.com/Alkarim786/search-engine-from-scratch/internal/suggest"
	"github.com/Alkarim786/search-engine-from-scratch/web"
)

// Config for the server. All fields have env-var defaults in cmd/server.
type Config struct {
	IndexDir   string        // directory holding the built index
	DataDir    string        // scratch space for admin crawls
	AdminToken string        // bearer token for POST /crawl; empty disables the endpoint
	QueryLog   string        // optional file where successful queries are appended
	CrawlDelay time.Duration // per-host delay for admin crawls
	Logf       func(string, ...any)
}

type state struct {
	engine *search.Engine
	sugg   *suggest.Suggester
}

// Server is an http.Handler provider. Safe for concurrent use.
type Server struct {
	cfg Config

	mu sync.RWMutex // guards st; held for reading while a query runs
	st *state

	crawlMu     sync.Mutex
	crawlStatus CrawlStatus

	logMu sync.Mutex
}

// CrawlStatus is reported by GET /crawl/status.
type CrawlStatus struct {
	State    string          `json:"state"` // idle | crawling | indexing | done | failed
	Started  time.Time       `json:"started,omitempty"`
	Finished time.Time       `json:"finished,omitempty"`
	Error    string          `json:"error,omitempty"`
	Crawl    *crawler.Stats  `json:"crawl,omitempty"`
	Index    *pipeline.Stats `json:"index,omitempty"`
}

// New creates a server and loads the index if one exists.
func New(cfg Config) (*Server, error) {
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.CrawlDelay <= 0 {
		cfg.CrawlDelay = time.Second
	}
	s := &Server{cfg: cfg, crawlStatus: CrawlStatus{State: "idle"}}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "meta.json")); err == nil {
		st, err := s.loadState(cfg.IndexDir)
		if err != nil {
			return nil, err
		}
		s.st = st
	} else {
		cfg.Logf("no index at %s yet; /search will return 503 until one is built", cfg.IndexDir)
	}
	return s, nil
}

// Close releases the index files.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		err := s.st.engine.Index().Close()
		s.st = nil
		return err
	}
	return nil
}

func (s *Server) loadState(dir string) (*state, error) {
	ix, err := index.Open(dir)
	if err != nil {
		return nil, err
	}
	sg := suggest.New(8)
	n := float64(ix.NumDocs())
	for id := 0; id < ix.NumDocs(); id++ {
		d := ix.Doc(uint32(id))
		if t := cleanTitle(d.Title); t != "" {
			sg.AddPhrase(t, 1+n*ix.PageRank(uint32(id)))
		}
	}
	if v, err := ix.Vocab(); err == nil {
		for _, e := range v {
			sg.AddWord(e.W, float64(e.C))
		}
	}
	if s.cfg.QueryLog != "" {
		if f, err := os.Open(s.cfg.QueryLog); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				sg.Observe(sc.Text())
			}
			f.Close()
		}
	}
	return &state{engine: search.New(ix), sugg: sg}, nil
}

// cleanTitle drops site-name suffixes such as " — Python 3.15 documentation".
func cleanTitle(t string) string {
	for _, sep := range []string{" — ", " | ", " – ", " · "} {
		if i := strings.Index(t, sep); i > 0 {
			t = t[:i]
		}
	}
	t = strings.TrimSpace(t)
	if len(t) > 80 {
		return ""
	}
	return t
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /suggest", s.handleSuggest)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /crawl", s.handleCrawl)
	mux.HandleFunc("GET /crawl/status", s.handleCrawlStatus)
	mux.Handle("GET /", http.FileServerFS(web.Files))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ResultJSON is one entry of the /search response.
type ResultJSON struct {
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Snippet  string  `json:"snippet"`
	Score    float64 `json:"score"`
	BM25     float64 `json:"bm25"`
	PageRank float64 `json:"pagerank"`
}

// SearchResponse is the /search response body.
type SearchResponse struct {
	Query   string       `json:"query"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	Size    int          `json:"size"`
	TookMS  float64      `json:"took_ms"`
	Results []ResultJSON `json:"results"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpErr(w, 400, "missing q")
		return
	}
	if len(q) > 300 {
		httpErr(w, 400, "query too long")
		return
	}
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	size := atoiDefault(r.URL.Query().Get("size"), 10)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 50 {
		size = 10
	}
	opts := search.DefaultOptions()
	if v := r.URL.Query().Get("pr"); v != "" { // experiment knob: weight of the PageRank prior
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 100 {
			httpErr(w, 400, "bad pr")
			return
		}
		opts.PRWeight = f
	}
	opts.RequireAll = r.URL.Query().Get("mode") == "and"

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st == nil {
		httpErr(w, 503, "no index yet: run the indexer or POST /crawl")
		return
	}
	res, err := s.st.engine.Search(q, opts, (page-1)*size, size)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := SearchResponse{Query: q, Total: res.Total, Page: page, Size: size, TookMS: float64(res.Took.Microseconds()) / 1000, Results: []ResultJSON{}}
	ix := s.st.engine.Index()
	for _, h := range res.Hits {
		text, _ := ix.Text(h.DocID)
		out.Results = append(out.Results, ResultJSON{
			URL: h.URL, Title: h.Title, Snippet: snippet.Make(text, res.Terms, 35),
			Score: h.Score, BM25: h.BM25, PageRank: h.PageRank,
		})
	}
	if res.Total > 0 && page == 1 && len(q) <= 80 {
		s.st.sugg.Observe(strings.ToLower(q))
		s.appendQueryLog(strings.ToLower(q))
	}
	writeJSON(w, 200, out)
}

func (s *Server) appendQueryLog(q string) {
	if s.cfg.QueryLog == "" || strings.ContainsAny(q, "\r\n") {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	f, err := os.OpenFile(s.cfg.QueryLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, q)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st == nil {
		writeJSON(w, 200, map[string]any{"query": q, "suggestions": []string{}})
		return
	}
	sug := s.st.sugg.Suggest(q)
	if sug == nil {
		sug = []string{}
	}
	writeJSON(w, 200, map[string]any{"query": q, "suggestions": sug})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st == nil {
		writeJSON(w, 200, map[string]any{"indexed": false})
		return
	}
	ix := s.st.engine.Index()
	var total int64
	for _, n := range ix.Size() {
		total += n
	}
	writeJSON(w, 200, map[string]any{
		"indexed": true, "docs": ix.NumDocs(), "terms": ix.Meta.NumTerms, "tokens": ix.Meta.TotalTokens,
		"avg_doc_len": ix.Meta.AvgDL, "pagerank": ix.HasPageRank(), "index_bytes": total, "files": ix.Size(),
	})
}

// CrawlRequest is the POST /crawl body.
type CrawlRequest struct {
	Seeds        []string `json:"seeds"`
	MaxPages     int      `json:"maxPages"`
	PathPrefixes []string `json:"pathPrefixes"`
}

func (s *Server) handleCrawl(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminToken == "" {
		httpErr(w, 403, "admin endpoint disabled: set ADMIN_TOKEN")
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.AdminToken)) != 1 {
		httpErr(w, 401, "bad or missing bearer token")
		return
	}
	var req CrawlRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || len(req.Seeds) == 0 {
		httpErr(w, 400, `body must be {"seeds":[...],"maxPages":N}`)
		return
	}
	if req.MaxPages <= 0 || req.MaxPages > 5000 {
		req.MaxPages = 500
	}
	s.crawlMu.Lock()
	if st := s.crawlStatus.State; st == "crawling" || st == "indexing" {
		s.crawlMu.Unlock()
		httpErr(w, 409, "a crawl is already running")
		return
	}
	s.crawlStatus = CrawlStatus{State: "crawling", Started: time.Now().UTC()}
	s.crawlMu.Unlock()
	go s.runCrawl(req)
	writeJSON(w, 202, map[string]string{"status": "started"})
}

func (s *Server) setStatus(f func(*CrawlStatus)) {
	s.crawlMu.Lock()
	defer s.crawlMu.Unlock()
	f(&s.crawlStatus)
}

func (s *Server) handleCrawlStatus(w http.ResponseWriter, r *http.Request) {
	s.crawlMu.Lock()
	defer s.crawlMu.Unlock()
	writeJSON(w, 200, s.crawlStatus)
}

func (s *Server) runCrawl(req CrawlRequest) {
	fail := func(err error) {
		s.cfg.Logf("crawl failed: %v", err)
		s.setStatus(func(c *CrawlStatus) { c.State, c.Error, c.Finished = "failed", err.Error(), time.Now().UTC() })
	}
	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		fail(err)
		return
	}
	pages := filepath.Join(s.cfg.DataDir, "pages.jsonl")
	sink, err := crawler.NewJSONLSink(pages)
	if err != nil {
		fail(err)
		return
	}
	cst, err := crawler.Run(context.Background(), crawler.Config{
		Seeds: req.Seeds, PathPrefixes: req.PathPrefixes, MaxPages: req.MaxPages, Workers: 4,
		Delay: s.cfg.CrawlDelay, Sink: sink, Logf: s.cfg.Logf,
	})
	sink.Close()
	if err != nil {
		fail(err)
		return
	}
	s.setStatus(func(c *CrawlStatus) { c.State, c.Crawl = "indexing", cst })

	next := s.cfg.IndexDir + ".next"
	ist, err := pipeline.Build([]string{pages}, next, s.cfg.Logf)
	if err != nil {
		fail(err)
		return
	}
	if err := s.swap(next); err != nil {
		fail(err)
		return
	}
	s.setStatus(func(c *CrawlStatus) { c.State, c.Index, c.Finished = "done", ist, time.Now().UTC() })
}

// swap atomically (w.r.t. queries) replaces the live index directory with next.
func (s *Server) swap(next string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		if err := s.st.engine.Index().Close(); err != nil {
			return err
		}
		s.st = nil
	}
	old := s.cfg.IndexDir + ".old"
	os.RemoveAll(old)
	if _, err := os.Stat(s.cfg.IndexDir); err == nil {
		if err := os.Rename(s.cfg.IndexDir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(next, s.cfg.IndexDir); err != nil {
		return err
	}
	st, err := s.loadState(s.cfg.IndexDir)
	if err != nil {
		return err
	}
	s.st = st
	os.RemoveAll(old)
	return nil
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}

// ListenAndServe runs the server until ctx is cancelled, then shuts down gracefully.
func ListenAndServe(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			return err
		}
		if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
