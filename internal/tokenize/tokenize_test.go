package tokenize

import (
	"reflect"
	"strings"
	"testing"
)

func TestStemVectors(t *testing.T) {
	// Vectors from Porter's paper and the reference vocabulary.
	cases := map[string]string{
		"caresses": "caress", "ponies": "poni", "ties": "ti", "caress": "caress", "cats": "cat",
		"feed": "feed", "agreed": "agre", "plastered": "plaster", "bled": "bled", "motoring": "motor",
		"sing": "sing", "conflated": "conflat", "troubled": "troubl", "sized": "size", "hopping": "hop",
		"tanned": "tan", "falling": "fall", "hissing": "hiss", "fizzed": "fizz", "failing": "fail",
		"filing": "file", "happy": "happi", "sky": "sky", "relational": "relat", "conditional": "condit",
		"rational": "ration", "valenci": "valenc", "digitizer": "digit", "operator": "oper",
		"feudalism": "feudal", "decisiveness": "decis", "hopefulness": "hope", "callousness": "callous",
		"formaliti": "formal", "sensitiviti": "sensit", "sensibiliti": "sensibl", "triplicate": "triplic",
		"formative": "form", "formalize": "formal", "electriciti": "electr", "electrical": "electr",
		"hopeful": "hope", "goodness": "good", "revival": "reviv", "allowance": "allow",
		"inference": "infer", "airliner": "airlin", "gyroscopic": "gyroscop", "adjustable": "adjust",
		"defensible": "defens", "irritant": "irrit", "replacement": "replac", "adjustment": "adjust",
		"dependent": "depend", "adoption": "adopt", "homologou": "homolog", "communism": "commun",
		"activate": "activ", "angulariti": "angular", "homologous": "homolog", "effective": "effect",
		"bowdlerize": "bowdler", "probate": "probat", "rate": "rate", "cease": "ceas",
		"controll": "control", "roll": "roll", "running": "run", "generalizations": "gener",
	}
	for in, want := range cases {
		if got := Stem(in); got != want {
			t.Errorf("Stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStemLeavesNonAlphaAlone(t *testing.T) {
	for _, w := range []string{"py3", "42", "é", "a", "is"} {
		if got := Stem(w); got != w {
			t.Errorf("Stem(%q) = %q", w, got)
		}
	}
}

func TestAnalyzeKeepsStopwordGaps(t *testing.T) {
	got := Analyze("The quick brown foxes, jumping over the lazy dog!")
	want := []Token{{"quick", 1}, {"brown", 2}, {"fox", 3}, {"jump", 4}, {"lazi", 7}, {"dog", 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestWordsSplitsPunctuationAndCode(t *testing.T) {
	got := Words("str.join(list_of_Strings) -> naïve café 3.14")
	want := []string{"str", "join", "list", "of", "strings", "naïve", "café", "3", "14"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOverlongWordsDropped(t *testing.T) {
	long := strings.Repeat("x", 100)
	if got := Words(long + " ok"); !reflect.DeepEqual(got, []string{"ok"}) {
		t.Fatalf("got %v", got)
	}
}
