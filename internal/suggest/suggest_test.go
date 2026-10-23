package suggest

import (
	"reflect"
	"testing"
)

func texts(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}

func TestTrieTopKOrderedByWeight(t *testing.T) {
	tr := NewTrie(3)
	for w, c := range map[string]float64{"python": 50, "pyramid": 5, "pytest": 30, "pylint": 20, "pyflakes": 10, "java": 99} {
		tr.Insert(w, c)
	}
	got := texts(tr.Complete("py"))
	if want := []string{"python", "pytest", "pylint"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := texts(tr.Complete("pyt")); !reflect.DeepEqual(got, []string{"python", "pytest"}) {
		t.Fatalf("got %v", got)
	}
	if tr.Complete("zzz") != nil {
		t.Fatal("unknown prefix")
	}
}

func TestInsertAccumulatesAndReorders(t *testing.T) {
	tr := NewTrie(2)
	tr.Insert("alpha", 1)
	tr.Insert("alpine", 2)
	tr.Insert("alps", 3)
	// alpha fell out of the top-2 for prefix "al"; boosting it must bring it back.
	tr.Insert("alpha", 10)
	if got := texts(tr.Complete("al")); !reflect.DeepEqual(got, []string{"alpha", "alps"}) {
		t.Fatalf("got %v", got)
	}
	if tr.Len() != 3 {
		t.Fatalf("len = %d", tr.Len())
	}
}

func TestCaseWhitespaceAndUnicode(t *testing.T) {
	tr := NewTrie(5)
	tr.Insert("  Café   Au Lait ", 1)
	if got := texts(tr.Complete("CAFÉ a")); !reflect.DeepEqual(got, []string{"café au lait"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSuggesterCombinesPhrasesAndWords(t *testing.T) {
	s := New(5)
	s.AddPhrase("list comprehensions", 10)
	s.AddPhrase("list methods", 2)
	s.AddPhrase("dictionary views", 3)
	s.AddWord("comprehension", 40)
	s.AddWord("compare", 90)
	s.AddWord("compile", 20)
	s.AddWord("list", 100)

	if got := s.Suggest("list"); got[0] != "list comprehensions" || got[1] != "list methods" {
		t.Fatalf("phrases first: %v", got)
	}
	got := s.Suggest("list comp")
	want := []string{"list comprehensions", "list compare", "list comprehension", "list compile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := s.Suggest("list "); !reflect.DeepEqual(got, []string{"list comprehensions", "list methods"}) {
		t.Fatalf("trailing space: %v", got)
	}
	if s.Suggest("   ") != nil {
		t.Fatal("blank input")
	}
	// Past queries make a phrase popular.
	for i := 0; i < 9; i++ {
		s.Observe("list methods")
	}
	if got := s.Suggest("lis"); got[0] != "list methods" {
		t.Fatalf("observed query should rank first: %v", got)
	}
}
