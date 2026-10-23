package search

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
)

type doc struct{ url, title, text string }

func build(t *testing.T, docs []doc, pr []float64) *Engine {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ix")
	b, err := index.NewBuilder(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if _, err := b.Add(d.url, d.title, d.text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if pr != nil {
		if err := index.WritePageRank(dir, pr); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return New(ix)
}

func urls(r *Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.URL)
	}
	return out
}

// Hand-computed: N=3, df=1, tf=2, dl=4, avgdl=5, k1=1.2, b=0.75
//
//	idf = ln(1 + 2.5/1.5)               = 0.9808292530
//	tf' = 2*2.2 / (2 + 1.2*(0.25+0.6))  = 4.4/3.02 = 1.4569536424
//	bm25 = idf * tf'                    = 1.4290227527
func TestBM25TermMatchesHandComputation(t *testing.T) {
	got := BM25Term(3, 1, 2, 4, 5, 1.2, 0.75)
	if math.Abs(got-1.4290227527) > 1e-9 {
		t.Fatalf("got %.10f", got)
	}
}

func TestBM25Properties(t *testing.T) {
	// Saturation: doubling tf must less than double the score.
	a, b := BM25Term(100, 10, 1, 50, 50, 1.2, 0.75), BM25Term(100, 10, 2, 50, 50, 1.2, 0.75)
	if !(b > a && b < 2*a) {
		t.Errorf("no saturation: %v %v", a, b)
	}
	// Length normalisation: same tf in a longer doc scores lower.
	if BM25Term(100, 10, 3, 200, 50, 1.2, 0.75) >= BM25Term(100, 10, 3, 50, 50, 1.2, 0.75) {
		t.Error("longer doc should score lower")
	}
	// Rarer terms (lower df) carry more weight; idf stays positive even if df > N/2.
	if BM25Term(100, 2, 1, 50, 50, 1.2, 0.75) <= BM25Term(100, 60, 1, 50, 50, 1.2, 0.75) {
		t.Error("rare term should score higher")
	}
	if BM25Term(10, 10, 1, 5, 5, 1.2, 0.75) <= 0 {
		t.Error("idf must stay positive when every doc has the term")
	}
}

func TestRankingMatchesManualBM25(t *testing.T) {
	e := build(t, []doc{
		{"u/a", "", "rust rust rust compiler"}, // dl 4, tf(rust)=3
		{"u/b", "", "rust go python java kotlin swift"},
		{"u/c", "", "python java kotlin swift scala"},
	}, nil)
	res, err := e.Search("rust", Options{PRWeight: 0}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(urls(res), []string{"u/a", "u/b"}) || res.Total != 2 {
		t.Fatalf("got %v total %d", urls(res), res.Total)
	}
	avg := float64(4+6+5) / 3
	want := BM25Term(3, 2, 3, 4, avg, 1.2, 0.75)
	if math.Abs(res.Hits[0].BM25-want) > 1e-9 {
		t.Fatalf("bm25 = %v want %v", res.Hits[0].BM25, want)
	}
}

func TestTitleBoostAndStemming(t *testing.T) {
	e := build(t, []doc{
		{"u/body", "Notes", "a long text about compilers and compiling many programs compilers"},
		{"u/title", "Compilers", "short text"},
	}, nil)
	res, _ := e.Search("compiler", Options{TitleBoost: 2, PRWeight: 0}, 0, 10)
	if len(res.Hits) != 2 {
		t.Fatalf("stemming should match compilers/compiling: %v", urls(res))
	}
	if res.Hits[0].URL != "u/title" {
		t.Fatalf("title match should win: %v", urls(res))
	}
	noBoost, _ := e.Search("compiler", Options{NoDefaults: true, K1: 1.2, B: 0.75, BM25Weight: 1}, 0, 10)
	if noBoost.Hits[0].BM25 >= res.Hits[0].BM25 {
		t.Error("boost should raise the title doc's score")
	}
}

func TestPhraseQueries(t *testing.T) {
	e := build(t, []doc{
		{"u/exact", "", "the quick brown fox jumps over the lazy dog"},
		{"u/scrambled", "", "brown dogs and quick foxes jump"},
		{"u/gap", "", "state of the art compilers"},
		{"u/nogap", "", "state art"},
	}, nil)
	r, _ := e.Search(`"quick brown fox"`, Options{PRWeight: 0}, 0, 10)
	if !reflect.DeepEqual(urls(r), []string{"u/exact"}) {
		t.Errorf("phrase: %v", urls(r))
	}
	r, _ = e.Search(`quick brown fox`, Options{PRWeight: 0}, 0, 10)
	if r.Total != 2 {
		t.Errorf("without quotes both docs match: %v", urls(r))
	}
	// Stopwords inside a phrase keep their distance: "state of the art" needs a 3-word gap.
	r, _ = e.Search(`"state of the art"`, Options{PRWeight: 0}, 0, 10)
	if !reflect.DeepEqual(urls(r), []string{"u/gap"}) {
		t.Errorf("stopword gap phrase: %v", urls(r))
	}
	// Phrase combined with a bare term filters then ranks.
	r, _ = e.Search(`"brown fox" lazy`, Options{PRWeight: 0}, 0, 10)
	if !reflect.DeepEqual(urls(r), []string{"u/exact"}) {
		t.Errorf("phrase+term: %v", urls(r))
	}
}

func TestPhraseDoesNotSpanTitleAndBody(t *testing.T) {
	e := build(t, []doc{{"u/x", "Hello World", "program output"}}, nil)
	r, _ := e.Search(`"world program"`, Options{PRWeight: 0}, 0, 10)
	if r.Total != 0 {
		t.Errorf("phrase crossed the title/body boundary: %v", urls(r))
	}
}

func TestRequireAll(t *testing.T) {
	e := build(t, []doc{
		{"u/both", "", "apples and oranges"},
		{"u/one", "", "apples only here"},
	}, nil)
	r, _ := e.Search("apples oranges", Options{PRWeight: 0}, 0, 10)
	if r.Total != 2 || r.Hits[0].URL != "u/both" {
		t.Errorf("OR: %v", urls(r))
	}
	r, _ = e.Search("apples oranges", Options{PRWeight: 0, RequireAll: true}, 0, 10)
	if !reflect.DeepEqual(urls(r), []string{"u/both"}) {
		t.Errorf("AND: %v", urls(r))
	}
}

func TestPageRankBreaksNearTies(t *testing.T) {
	docs := []doc{
		{"u/low", "", "database indexing guide"},
		{"u/high", "", "database indexing guide"},
	}
	e := build(t, docs, []float64{0.1, 0.9})
	r, _ := e.Search("database indexing", Options{PRWeight: 1}, 0, 10)
	if r.Hits[0].URL != "u/high" || r.Hits[0].Score <= r.Hits[1].Score {
		t.Errorf("PageRank should lift the better-linked page: %+v", r.Hits)
	}
	r, _ = e.Search("database indexing", Options{PRWeight: 0}, 0, 10)
	if r.Hits[0].URL != "u/low" { // identical BM25 -> deterministic tie-break on docID
		t.Errorf("tie-break: %v", urls(r))
	}
}

func TestPaginationAndEmptyQueries(t *testing.T) {
	var docs []doc
	for i := 0; i < 25; i++ {
		docs = append(docs, doc{url: string(rune('a'+i)) + "/x", text: "needle " + string(rune('a'+i))})
	}
	e := build(t, docs, nil)
	p1, _ := e.Search("needle", Options{PRWeight: 0}, 0, 10)
	p2, _ := e.Search("needle", Options{PRWeight: 0}, 10, 10)
	p3, _ := e.Search("needle", Options{PRWeight: 0}, 20, 10)
	if len(p1.Hits) != 10 || len(p2.Hits) != 10 || len(p3.Hits) != 5 || p1.Total != 25 {
		t.Fatalf("pages: %d %d %d total %d", len(p1.Hits), len(p2.Hits), len(p3.Hits), p1.Total)
	}
	seen := map[string]bool{}
	for _, p := range []*Result{p1, p2, p3} {
		for _, h := range p.Hits {
			if seen[h.URL] {
				t.Fatalf("duplicate across pages: %s", h.URL)
			}
			seen[h.URL] = true
		}
	}
	for _, q := range []string{"", "the of and", "zzzznotaword", `""`} {
		r, err := e.Search(q, Options{}, 0, 10)
		if err != nil || len(r.Hits) != 0 {
			t.Errorf("query %q: %v %v", q, r.Hits, err)
		}
	}
}

func TestParseQuery(t *testing.T) {
	pq := ParseQuery(`go "state of the art" Concurrency go`)
	if !reflect.DeepEqual(pq.Terms, []string{"state", "art", "go", "concurr"}) {
		t.Errorf("terms = %v", pq.Terms)
	}
	if len(pq.Phrases) != 1 || !reflect.DeepEqual(pq.Phrases[0], []PhraseTerm{{"state", 0}, {"art", 3}}) {
		t.Errorf("phrases = %v", pq.Phrases)
	}
	if pq := ParseQuery(`"unterminated phrase here`); len(pq.Phrases) != 1 || len(pq.Terms) != 2 {
		t.Errorf("unterminated quote: %+v", pq)
	}
}

func TestIndexPostingsRoundTrip(t *testing.T) {
	e := build(t, []doc{
		{"u/0", "Alpha", "beta beta gamma"},
		{"u/1", "", "gamma gamma"},
		{"u/2", "Gamma", "beta"},
	}, nil)
	ps, err := e.Index().Postings("gamma", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].Doc != 0 || ps[1].Doc != 1 || ps[2].Doc != 2 {
		t.Fatalf("postings = %+v", ps)
	}
	// doc 0: title "Alpha" (1 word, pos 0) + gap -> body starts at 2: beta=2,3 gamma=4
	if !reflect.DeepEqual(ps[0].Pos, []uint32{4}) || ps[0].First != 4 {
		t.Errorf("doc0 positions = %v", ps[0].Pos)
	}
	if !reflect.DeepEqual(ps[1].Pos, []uint32{1, 2}) || ps[1].TF != 2 {
		t.Errorf("doc1 positions = %v", ps[1].Pos)
	}
	if ps[2].First != 0 { // appears in the title
		t.Errorf("doc2 first = %d", ps[2].First)
	}
	if e.Index().DF("gamma") != 3 || e.Index().DF("nope") != 0 {
		t.Error("df")
	}
	txt, err := e.Index().Text(1)
	if err != nil || txt != "gamma gamma" {
		t.Errorf("text = %q %v", txt, err)
	}
}
