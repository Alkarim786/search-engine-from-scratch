package dedup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
)

func article(n int, seed string) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s sentence number %d talks about topic %d and related subject %d. ", seed, i, i*7%13, i*3%11)
	}
	return sb.String()
}

func TestExactAndNear(t *testing.T) {
	d := New()
	base := article(60, "alpha")
	if k := d.Check(base, tokenize.Words(base)); k != Unique {
		t.Fatalf("first = %v", k)
	}
	if k := d.Check(base, tokenize.Words(base)); k != Exact {
		t.Fatalf("same text = %v", k)
	}
	near := base + " Updated on Tuesday."
	if k := d.Check(near, tokenize.Words(near)); k != Near {
		t.Fatalf("near copy = %v", k)
	}
	other := article(60, "beta")
	if k := d.Check(other, tokenize.Words(other)); k != Unique {
		t.Fatalf("different article = %v", k)
	}
}

func TestShortPagesOnlyExact(t *testing.T) {
	d := New()
	a, b := "hello world", "hello world again"
	if d.Check(a, tokenize.Words(a)) != Unique || d.Check(b, tokenize.Words(b)) != Unique {
		t.Fatal("short distinct pages must both be unique")
	}
	if d.Check(a, tokenize.Words(a)) != Exact {
		t.Fatal("identical short page is an exact dup")
	}
}

func TestSimHashDistance(t *testing.T) {
	a := tokenize.Words(article(50, "gamma"))
	b := append(append([]string{}, a...), "extra", "words")
	c := tokenize.Words(article(50, "delta"))
	if Distance(SimHash(a), SimHash(b)) > MaxDistance {
		t.Error("near copies should be close")
	}
	if Distance(SimHash(a), SimHash(c)) <= MaxDistance {
		t.Error("different docs should be far")
	}
}
