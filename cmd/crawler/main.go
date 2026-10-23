// Command crawler crawls a site politely and writes pages.jsonl.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/crawler"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	seeds := flag.String("seeds", envOr("CRAWL_SEEDS", ""), "comma-separated seed URLs (env CRAWL_SEEDS)")
	allow := flag.String("allow", envOr("CRAWL_ALLOW_HOSTS", ""), "comma-separated host[:port] allowlist (default: hosts of the seeds)")
	prefix := flag.String("path-prefix", envOr("CRAWL_PATH_PREFIX", ""), "comma-separated URL path prefixes to stay inside")
	out := flag.String("out", envOr("CRAWL_OUT", "data/pages.jsonl"), "output JSONL file")
	max := flag.Int("max-pages", 1000, "stop after storing this many pages")
	depth := flag.Int("max-depth", 10, "maximum link depth from the seeds")
	workers := flag.Int("workers", 8, "concurrent fetchers")
	delay := flag.Duration("delay", time.Second, "minimum delay between requests to one host")
	ua := flag.String("user-agent", envOr("CRAWL_USER_AGENT", crawler.DefaultUserAgent), "User-Agent header")
	flag.Parse()

	if *seeds == "" {
		log.Fatal("no seeds: pass -seeds or set CRAWL_SEEDS")
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	sink, err := crawler.NewJSONLSink(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer sink.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := crawler.Config{
		Seeds: split(*seeds), AllowHosts: split(*allow), PathPrefixes: split(*prefix),
		MaxPages: *max, MaxDepth: *depth, Workers: *workers, Delay: *delay, UserAgent: *ua,
		Sink: sink, Logf: log.Printf,
	}
	st, err := crawler.Run(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	fmt.Printf("%s\n", b)
	fmt.Printf("stored %d pages in %s (%.1f pages/s) -> %s\n", st.Stored, st.Elapsed.Round(time.Millisecond), st.PagesPerSecond(), *out)
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
