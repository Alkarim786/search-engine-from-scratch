// Command indexer builds the search index (and PageRank) from crawler output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Alkarim786/search-engine-from-scratch/internal/pipeline"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	in := flag.String("in", envOr("CRAWL_OUT", "data/pages.jsonl"), "comma-separated pages.jsonl files")
	out := flag.String("out", envOr("INDEX_DIR", "data/index"), "index output directory")
	flag.Parse()

	if err := os.RemoveAll(*out); err != nil {
		log.Fatal(err)
	}
	st, err := pipeline.Build(strings.Split(*in, ","), *out, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	fmt.Println(string(b))
	fmt.Printf("index written to %s (%.1f MB)\n", *out, float64(st.IndexBytes)/1e6)
}
