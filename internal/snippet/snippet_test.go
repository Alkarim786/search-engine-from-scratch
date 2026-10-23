package snippet

import (
	"strings"
	"testing"

	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
)

func stems(ws ...string) []string {
	var out []string
	for _, w := range ws {
		out = append(out, tokenize.Normalize(w))
	}
	return out
}

func TestHighlightsStemmedMatchesAndKeepsPunctuation(t *testing.T) {
	got := Make("Dictionaries are indexed by keys, and (keys) must be hashable.", stems("key", "dictionary"), 30)
	for _, want := range []string{"<mark>Dictionaries</mark>", "<mark>keys</mark>,", "(<mark>keys</mark>)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestPicksWindowWithMostDistinctTerms(t *testing.T) {
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 20)
	text := "alpha " + filler + "alpha beta gamma are together here " + filler
	got := Make(text, stems("alpha", "beta", "gamma"), 10)
	if !strings.Contains(got, "<mark>beta</mark>") || !strings.Contains(got, "<mark>gamma</mark>") {
		t.Errorf("should centre on the cluster: %q", got)
	}
	if !strings.HasPrefix(got, "… ") || !strings.HasSuffix(got, " …") {
		t.Errorf("ellipses missing: %q", got)
	}
}

func TestEscapesHTMLAndHandlesNoMatch(t *testing.T) {
	got := Make("<script>alert(1)</script> hello", stems("hello"), 30)
	if strings.Contains(got, "<script>") || !strings.Contains(got, "&lt;script&gt;") || !strings.Contains(got, "<mark>hello</mark>") {
		t.Errorf("unsafe or wrong: %q", got)
	}
	if got := Make("nothing relevant here at all", stems("zebra"), 3); got != "nothing relevant here …" {
		t.Errorf("no-match snippet = %q", got)
	}
	if Make("", stems("x"), 5) != "" {
		t.Error("empty text")
	}
}
