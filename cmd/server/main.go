// Command server serves the search API and web UI.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/server"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	delay, err := time.ParseDuration(envOr("CRAWL_DELAY", "1s"))
	if err != nil {
		log.Fatalf("bad CRAWL_DELAY: %v", err)
	}
	cfg := server.Config{
		IndexDir:   envOr("INDEX_DIR", "data/index"),
		DataDir:    envOr("DATA_DIR", "data"),
		AdminToken: os.Getenv("ADMIN_TOKEN"),
		QueryLog:   os.Getenv("QUERY_LOG"),
		CrawlDelay: delay,
		Logf:       log.Printf,
	}
	s, err := server.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	addr := envOr("ADDR", ":8080")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("listening on %s (index %s)", addr, cfg.IndexDir)
	if err := server.ListenAndServe(ctx, addr, s.Handler()); err != nil {
		log.Fatal(err)
	}
}
