// Package pipeline turns crawler output (pages.jsonl) into a searchable index:
// it assigns docIDs, builds the inverted index, resolves the link graph among
// crawled pages and computes PageRank.
package pipeline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/crawler"
	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
	"github.com/Alkarim786/search-engine-from-scratch/internal/pagerank"
)

// Stats describes a build.
type Stats struct {
	Docs         int           `json:"docs"`
	Terms        int           `json:"terms"`
	Tokens       int64         `json:"tokens"`
	Edges        int           `json:"edges"`
	PRIterations int           `json:"pagerank_iterations"`
	PRConverged  bool          `json:"pagerank_converged"`
	IndexTime    time.Duration `json:"index_time_ns"`
	PageRankTime time.Duration `json:"pagerank_time_ns"`
	IndexBytes   int64         `json:"index_bytes"`
}

// Build reads one or more JSONL page files and writes an index to outDir.
// Documents with a URL already seen in an earlier file or line are skipped.
func Build(pagePaths []string, outDir string, logf func(string, ...any)) (*Stats, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	start := time.Now()
	b, err := index.NewBuilder(outDir)
	if err != nil {
		return nil, err
	}
	defer b.Abort() // closes the text file if we bail out before Finish
	byURL := map[string]uint32{}
	var links [][]string
	for _, path := range pagePaths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		r := bufio.NewReaderSize(f, 1<<20)
		dec := json.NewDecoder(r)
		for {
			var p crawler.Page
			if err := dec.Decode(&p); err == io.EOF {
				break
			} else if err != nil {
				f.Close()
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if _, dup := byURL[p.URL]; dup {
				continue
			}
			id, err := b.Add(p.URL, p.Title, p.Text)
			if err != nil {
				f.Close()
				return nil, err
			}
			byURL[p.URL] = id
			links = append(links, p.Links)
			if (id+1)%500 == 0 {
				logf("indexed %d documents", id+1)
			}
		}
		f.Close()
	}
	meta, err := b.Finish()
	if err != nil {
		return nil, err
	}
	st := &Stats{Docs: meta.NumDocs, Terms: meta.NumTerms, Tokens: meta.TotalTokens, IndexTime: time.Since(start)}

	prStart := time.Now()
	graph := make([][]int, len(links))
	for i, ls := range links {
		for _, l := range ls {
			if j, ok := byURL[l]; ok {
				graph[i] = append(graph[i], int(j))
				st.Edges++
			}
		}
	}
	res := pagerank.Compute(graph, pagerank.Options{})
	st.PRIterations, st.PRConverged, st.PageRankTime = res.Iterations, res.Converged, time.Since(prStart)
	if err := index.WritePageRank(outDir, res.Scores); err != nil {
		return nil, err
	}
	ix, err := index.Open(outDir)
	if err != nil {
		return nil, err
	}
	for _, sz := range ix.Size() {
		st.IndexBytes += sz
	}
	ix.Close()
	logf("built index: %d docs, %d terms, %d link edges, PageRank %d iterations", st.Docs, st.Terms, st.Edges, st.PRIterations)
	return st, nil
}
