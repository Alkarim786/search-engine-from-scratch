// Command eval measures ranking quality (precision@k, MRR) and query latency
// of an index against hand-labelled queries.
//
// A query file is a JSON array of {"q": "...", "relevant": ["<regexp>", ...]}.
// A result counts as relevant if its URL matches any of the regular expressions.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
	"github.com/Alkarim786/search-engine-from-scratch/internal/search"
	"github.com/Alkarim786/search-engine-from-scratch/internal/server"
)

type labelled struct {
	Q        string   `json:"q"`
	Relevant []string `json:"relevant"`
	res      []*regexp.Regexp
}

type config struct {
	Name string
	Opts search.Options
}

// Metrics averaged over queries.
type Metrics struct {
	Config    string  `json:"config"`
	P10       float64 `json:"precision_at_10"`
	P3        float64 `json:"precision_at_3"`
	MRR       float64 `json:"mrr"`
	Success1  float64 `json:"success_at_1"`
	Success3  float64 `json:"success_at_3"`
	Success10 float64 `json:"success_at_10"`
}

// Latency summarises query timings in microseconds.
type Latency struct {
	Label   string  `json:"label"`
	Queries int     `json:"queries"`
	P50us   float64 `json:"p50_us"`
	P95us   float64 `json:"p95_us"`
	P99us   float64 `json:"p99_us"`
	MeanUs  float64 `json:"mean_us"`
}

func main() {
	idx := flag.String("index", "data/index", "index directory")
	qf := flag.String("queries", "eval/queries.json", "labelled queries")
	out := flag.String("out", "bench/results.json", "results JSON")
	reps := flag.Int("reps", 20, "repetitions of every query for latency")
	verbose := flag.Bool("v", false, "print the rank of the first relevant result per query (default ranking)")
	flag.Parse()

	raw, err := os.ReadFile(*qf)
	if err != nil {
		log.Fatal(err)
	}
	var qs []labelled
	if err := json.Unmarshal(raw, &qs); err != nil {
		log.Fatal(err)
	}
	for i := range qs {
		for _, p := range qs[i].Relevant {
			qs[i].res = append(qs[i].res, regexp.MustCompile(p))
		}
	}
	ix, err := index.Open(*idx)
	if err != nil {
		log.Fatal(err)
	}
	defer ix.Close()
	eng := search.New(ix)

	base := search.Options{K1: 1.2, B: 0.75, BM25Weight: 1, NoDefaults: true}
	cfgs := []config{{"BM25 (no title boost, no PageRank)", base}}
	tb := base
	tb.TitleBoost = 0.5
	cfgs = append(cfgs, config{"BM25 + title boost 0.5", tb})
	for _, w := range []float64{0.25, 0.5, 1, 2, 4} {
		o := tb
		o.PRWeight = w
		cfgs = append(cfgs, config{fmt.Sprintf("BM25 + title boost + PageRank w2=%.2g", w), o})
	}
	and := tb
	and.PRWeight = 0.5
	and.RequireAll = true
	cfgs = append(cfgs, config{"same as w2=0.5 but all terms required (AND)", and})

	var all []Metrics
	fmt.Printf("%d labelled queries, %d docs\n\n", len(qs), ix.NumDocs())
	fmt.Println("| Configuration | P@10 | P@3 | MRR | Success@1 | Success@3 | Success@10 |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, c := range cfgs {
		m := Metrics{Config: c.Name}
		for _, q := range qs {
			r, err := eng.Search(q.Q, c.Opts, 0, 10)
			if err != nil {
				log.Fatal(err)
			}
			rel := make([]bool, len(r.Hits))
			for i, h := range r.Hits {
				for _, re := range q.res {
					if re.MatchString(h.URL) {
						rel[i] = true
						break
					}
				}
			}
			m.P10 += frac(rel, 10)
			m.P3 += frac(rel, 3)
			for i, ok := range rel {
				if ok {
					m.MRR += 1 / float64(i+1)
					break
				}
			}
			m.Success1 += b2f(anyIn(rel, 1))
			m.Success3 += b2f(anyIn(rel, 3))
			m.Success10 += b2f(anyIn(rel, 10))
		}
		n := float64(len(qs))
		m.P10, m.P3, m.MRR, m.Success1, m.Success3, m.Success10 = m.P10/n, m.P3/n, m.MRR/n, m.Success1/n, m.Success3/n, m.Success10/n
		all = append(all, m)
		fmt.Printf("| %s | %.3f | %.3f | %.3f | %.2f | %.2f | %.2f |\n", m.Config, m.P10, m.P3, m.MRR, m.Success1, m.Success3, m.Success10)
	}

	if *verbose {
		fmt.Println("\nper-query rank of first relevant result (server default ranking):")
		for _, q := range qs {
			r, _ := eng.Search(q.Q, search.DefaultOptions(), 0, 10)
			rank := 0
			for i, h := range r.Hits {
				for _, re := range q.res {
					if rank == 0 && re.MatchString(h.URL) {
						rank = i + 1
					}
				}
			}
			top := ""
			if len(r.Hits) > 0 {
				top = r.Hits[0].URL
			}
			fmt.Printf("  rank %2d  %-42q top1: %s\n", rank, q.Q, top)
		}
	}

	// Latency: engine only, then end to end over loopback HTTP (incl. snippets + JSON).
	def := search.DefaultOptions()
	var eng1 []float64
	for r := 0; r < *reps; r++ {
		for _, q := range qs {
			t := time.Now()
			if _, err := eng.Search(q.Q, def, 0, 10); err != nil {
				log.Fatal(err)
			}
			eng1 = append(eng1, float64(time.Since(t).Nanoseconds())/1e3)
		}
	}
	lat := []Latency{summarize("engine only (BM25+PageRank, top 10)", eng1)}

	srv, err := server.New(server.Config{IndexDir: *idx, DataDir: os.TempDir(), Logf: func(string, ...any) {}})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	var e2e []float64
	for r := 0; r < *reps; r++ {
		for _, q := range qs {
			u := ts.URL + "/search?q=" + urlQuery(q.Q)
			t := time.Now()
			resp, err := http.Get(u)
			if err != nil {
				log.Fatal(err)
			}
			buf := make([]byte, 1<<16)
			for {
				_, err := resp.Body.Read(buf)
				if err != nil {
					break
				}
			}
			resp.Body.Close()
			e2e = append(e2e, float64(time.Since(t).Nanoseconds())/1e3)
		}
	}
	lat = append(lat, summarize("HTTP GET /search on loopback (snippets + JSON)", e2e))
	fmt.Println("\n| Latency | queries | p50 | p95 | p99 | mean |")
	fmt.Println("|---|---|---|---|---|---|")
	for _, l := range lat {
		fmt.Printf("| %s | %d | %.2f ms | %.2f ms | %.2f ms | %.2f ms |\n", l.Label, l.Queries, l.P50us/1e3, l.P95us/1e3, l.P99us/1e3, l.MeanUs/1e3)
	}

	sizes := ix.Size()
	var total int64
	for _, n := range sizes {
		total += n
	}
	fmt.Printf("\nindex: %d docs, %d terms, %d tokens, %.2f MB on disk %v\n", ix.NumDocs(), ix.Meta.NumTerms, ix.Meta.TotalTokens, float64(total)/1e6, sizes)

	res := map[string]any{"queries": len(qs), "docs": ix.NumDocs(), "terms": ix.Meta.NumTerms, "tokens": ix.Meta.TotalTokens,
		"index_bytes": total, "index_files": sizes, "quality": all, "latency": lat}
	if err := os.MkdirAll("bench", 0o755); err == nil {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func urlQuery(q string) string { return url.QueryEscape(q) }

func frac(rel []bool, k int) float64 {
	n := 0
	for i, ok := range rel {
		if i < k && ok {
			n++
		}
	}
	return float64(n) / float64(k)
}

func anyIn(rel []bool, k int) bool {
	for i, ok := range rel {
		if i < k && ok {
			return true
		}
	}
	return false
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func summarize(label string, us []float64) Latency {
	sort.Float64s(us)
	pct := func(p float64) float64 { return us[int(float64(len(us)-1)*p)] }
	var sum float64
	for _, v := range us {
		sum += v
	}
	return Latency{Label: label, Queries: len(us), P50us: pct(0.5), P95us: pct(0.95), P99us: pct(0.99), MeanUs: sum / float64(len(us))}
}
