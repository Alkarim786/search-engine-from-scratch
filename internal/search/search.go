// Package search ranks documents for a query: BM25 text relevance (with a
// title boost) blended with a PageRank prior, plus exact phrase matching
// using the positional postings.
package search

import (
	"container/heap"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
)

// Options tune ranking. Zero value = DefaultOptions via Normalize.
type Options struct {
	K1         float64 // term-frequency saturation (default 1.2)
	B          float64 // length normalisation (default 0.75); set NoDefaults to use 0
	TitleBoost float64 // extra multiplier on a term's score when it appears in the title (default 0.5)
	BM25Weight float64 // w1 (default 1)
	PRWeight   float64 // w2: weight of ln(1 + N*PageRank) (default 0.5)
	RequireAll bool    // AND semantics: every query term must appear
	NoDefaults bool    // use the numbers exactly as given (for experiments)
}

// DefaultOptions are the ranking parameters used by the server.
func DefaultOptions() Options {
	return Options{K1: 1.2, B: 0.75, TitleBoost: 0.5, BM25Weight: 1, PRWeight: 0.5}
}

func (o Options) normalize() Options {
	if o.NoDefaults {
		return o
	}
	d := DefaultOptions()
	if o.K1 == 0 {
		o.K1 = d.K1
	}
	if o.B == 0 {
		o.B = d.B
	}
	if o.BM25Weight == 0 {
		o.BM25Weight = d.BM25Weight
	}
	return o
}

// Hit is one ranked result.
type Hit struct {
	DocID    uint32
	URL      string
	Title    string
	Score    float64 // final blended score
	BM25     float64
	PageRank float64
}

// Result of a query.
type Result struct {
	Hits    []Hit // the requested page
	Total   int   // number of matching documents
	Terms   []string
	Phrases [][]string
	Took    time.Duration
}

// Engine answers queries against one index.
type Engine struct {
	ix *index.Index
}

// New wraps an open index.
func New(ix *index.Index) *Engine { return &Engine{ix: ix} }

// Index returns the underlying index.
func (e *Engine) Index() *index.Index { return e.ix }

// BM25Term is the BM25 contribution of one term in one document (Lucene's
// non-negative idf variant):
//
//	idf = ln(1 + (N - df + 0.5) / (df + 0.5))
//	tf' = tf (k1+1) / (tf + k1 (1 - b + b dl/avgdl))
func BM25Term(n, df int, tf, dl, avgdl, k1, b float64) float64 {
	idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
	return idf * tf * (k1 + 1) / (tf + k1*(1-b+b*dl/avgdl))
}

// ParsedQuery is a query split into scoring terms and phrase constraints.
type ParsedQuery struct {
	Terms   []string       // unique stemmed terms used for scoring
	Phrases [][]PhraseTerm // each must match as an exact (stopword-gap-aware) phrase
}

// PhraseTerm is a stemmed word and its offset inside the phrase (counting
// stopwords, so "state of the art" requires "state" and "art" 3 apart).
type PhraseTerm struct {
	Term   string
	Offset int
}

// ParseQuery understands bare words and "quoted phrases".
func ParseQuery(q string) ParsedQuery {
	var pq ParsedQuery
	seen := map[string]bool{}
	addTerm := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			pq.Terms = append(pq.Terms, t)
		}
	}
	var bare strings.Builder
	rest := q
	for {
		i := strings.IndexByte(rest, '"')
		if i < 0 {
			bare.WriteString(rest)
			break
		}
		bare.WriteString(rest[:i])
		bare.WriteByte(' ')
		j := strings.IndexByte(rest[i+1:], '"')
		var phrase string
		if j < 0 {
			phrase, rest = rest[i+1:], ""
		} else {
			phrase, rest = rest[i+1:i+1+j], rest[i+2+j:]
		}
		var pt []PhraseTerm
		for _, tk := range tokenize.Analyze(phrase) {
			pt = append(pt, PhraseTerm{tk.Term, tk.Pos})
			addTerm(tk.Term)
		}
		if len(pt) > 0 {
			base := pt[0].Offset
			for k := range pt {
				pt[k].Offset -= base
			}
			if len(pt) == 1 {
				continue // a one-word "phrase" is just a term
			}
			pq.Phrases = append(pq.Phrases, pt)
		}
		if rest == "" {
			break
		}
	}
	for _, tk := range tokenize.Analyze(bare.String()) {
		addTerm(tk.Term)
	}
	return pq
}

type scored struct {
	id    uint32
	bm25  float64
	final float64
}

type minHeap []scored

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].final != h[j].final {
		return h[i].final < h[j].final
	}
	return h[i].id > h[j].id // on ties the lower docID ranks higher
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)   { *h = append(*h, x.(scored)) }
func (h *minHeap) Pop() any {
	o := *h
	n := len(o)
	x := o[n-1]
	*h = o[:n-1]
	return x
}

// Search runs q and returns results offset..offset+limit of the ranking.
func (e *Engine) Search(q string, opts Options, offset, limit int) (*Result, error) {
	start := time.Now()
	opts = opts.normalize()
	if limit <= 0 {
		limit = 10
	}
	pq := ParseQuery(q)
	res := &Result{Terms: pq.Terms}
	for _, p := range pq.Phrases {
		var ts []string
		for _, t := range p {
			ts = append(ts, t.Term)
		}
		res.Phrases = append(res.Phrases, ts)
	}
	if len(pq.Terms) == 0 {
		res.Took = time.Since(start)
		return res, nil
	}

	n := e.ix.NumDocs()
	avgdl := e.ix.Meta.AvgDL
	acc := map[uint32]float64{}
	matched := map[uint32]int{} // number of distinct query terms per doc
	needPos := len(pq.Phrases) > 0
	phrasePostings := map[string][]index.Posting{}
	for _, term := range pq.Terms {
		ps, err := e.ix.Postings(term, needPos)
		if err != nil {
			return nil, err
		}
		if needPos {
			phrasePostings[term] = ps
		}
		df := len(ps)
		for _, p := range ps {
			d := e.ix.Doc(p.Doc)
			s := BM25Term(n, df, float64(p.TF), float64(d.Len), avgdl, opts.K1, opts.B)
			if opts.TitleBoost > 0 && inTitle(p, d) {
				s *= 1 + opts.TitleBoost
			}
			acc[p.Doc] += s
			matched[p.Doc]++
		}
	}

	if opts.RequireAll {
		for id, c := range matched {
			if c < len(pq.Terms) {
				delete(acc, id)
			}
		}
	}
	for _, phrase := range pq.Phrases {
		ok, err := e.phraseDocs(phrase, phrasePostings)
		if err != nil {
			return nil, err
		}
		for id := range acc {
			if !ok[id] {
				delete(acc, id)
			}
		}
	}
	res.Total = len(acc)

	want := offset + limit
	h := &minHeap{}
	for id, s := range acc {
		final := opts.BM25Weight * s
		if opts.PRWeight != 0 && e.ix.HasPageRank() {
			final += opts.PRWeight * math.Log(1+float64(n)*e.ix.PageRank(id))
		}
		item := scored{id: id, bm25: s, final: final}
		if h.Len() < want {
			heap.Push(h, item)
		} else if less((*h)[0], item) {
			(*h)[0] = item
			heap.Fix(h, 0)
		}
	}
	top := make([]scored, h.Len())
	copy(top, *h)
	sort.Slice(top, func(i, j int) bool { return less(top[j], top[i]) })
	if offset > len(top) {
		offset = len(top)
	}
	top = top[offset:]
	for _, s := range top {
		d := e.ix.Doc(s.id)
		res.Hits = append(res.Hits, Hit{DocID: s.id, URL: d.URL, Title: d.Title, Score: s.final, BM25: s.bm25, PageRank: e.ix.PageRank(s.id)})
	}
	res.Took = time.Since(start)
	return res, nil
}

// less reports whether a ranks strictly below b.
func less(a, b scored) bool {
	if a.final != b.final {
		return a.final < b.final
	}
	return a.id > b.id
}

// inTitle reports whether the term occurs in the document's title: title
// tokens occupy the lowest positions, so the earliest position decides.
func inTitle(p index.Posting, d index.DocMeta) bool {
	return d.TitleLen > 0 && int(p.First) < d.TitleLen
}

var errNoPositions = errors.New("search: postings decoded without positions")

// phraseDocs returns the set of documents containing the phrase.
func (e *Engine) phraseDocs(phrase []PhraseTerm, postings map[string][]index.Posting) (map[uint32]bool, error) {
	out := map[uint32]bool{}
	first := postings[phrase[0].Term]
	lists := make([]map[uint32][]uint32, len(phrase))
	for i, pt := range phrase {
		m := map[uint32][]uint32{}
		for _, p := range postings[pt.Term] {
			if p.Pos == nil && p.TF > 0 {
				return nil, errNoPositions
			}
			m[p.Doc] = p.Pos
		}
		lists[i] = m
	}
	for _, p := range first {
		ok := true
		for i := 1; i < len(phrase) && ok; i++ {
			if _, has := lists[i][p.Doc]; !has {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if phraseAt(p.Doc, phrase, lists) {
			out[p.Doc] = true
		}
	}
	return out, nil
}

func phraseAt(doc uint32, phrase []PhraseTerm, lists []map[uint32][]uint32) bool {
	for _, start := range lists[0][doc] {
		match := true
		for i := 1; i < len(phrase); i++ {
			target := start + uint32(phrase[i].Offset)
			ps := lists[i][doc]
			j := sort.Search(len(ps), func(k int) bool { return ps[k] >= target })
			if j == len(ps) || ps[j] != target {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
