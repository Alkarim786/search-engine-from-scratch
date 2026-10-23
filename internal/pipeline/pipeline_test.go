package pipeline

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/Alkarim786/search-engine-from-scratch/internal/crawler"
	"github.com/Alkarim786/search-engine-from-scratch/internal/index"
)

func writePages(t *testing.T, path string, pages []crawler.Page) {
	t.Helper()
	sink, err := crawler.NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if err := sink.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	sink.Close()
}

func TestBuildResolvesLinkGraphAndPageRank(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	// hub is linked from everyone; "ghost" is not a crawled page and must not create an edge.
	writePages(t, a, []crawler.Page{
		{URL: "http://s/hub", Title: "Hub", Text: "central hub page", Links: []string{"http://s/ghost"}},
		{URL: "http://s/x", Title: "X", Text: "page x", Links: []string{"http://s/hub"}},
	})
	writePages(t, b, []crawler.Page{
		{URL: "http://s/y", Title: "Y", Text: "page y", Links: []string{"http://s/hub", "http://s/x"}},
		{URL: "http://s/hub", Title: "Hub again", Text: "duplicate URL in a later file is skipped"},
	})
	out := filepath.Join(dir, "ix")
	st, err := Build([]string{a, b}, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Docs != 3 || st.Edges != 3 || !st.PRConverged {
		t.Fatalf("stats = %+v", st)
	}
	ix, err := index.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var sum float64
	for i := 0; i < ix.NumDocs(); i++ {
		sum += ix.PageRank(uint32(i))
	}
	if math.Abs(sum-1) > 1e-6 {
		t.Errorf("PageRank sums to %v", sum)
	}
	hub, x, y := ix.PageRank(0), ix.PageRank(1), ix.PageRank(2)
	if !(hub > x && x > y) { // hub: in-links from x and y; x: from y; y: none
		t.Errorf("hub=%v x=%v y=%v", hub, x, y)
	}
}

func TestBuildFailsOnMissingFile(t *testing.T) {
	if _, err := Build([]string{filepath.Join(t.TempDir(), "nope.jsonl")}, t.TempDir(), nil); err == nil {
		t.Fatal("expected error")
	}
}
