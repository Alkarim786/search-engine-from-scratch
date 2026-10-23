// Package snippet picks the best passage of a document for a query and
// highlights the matching words.
package snippet

import (
	"html"
	"strings"
	"unicode"

	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
)

// MaxScanWords bounds the work per result: only the first MaxScanWords words
// of a document are searched for a passage (very long pages otherwise
// dominate query latency).
const MaxScanWords = 6000

// Make returns an HTML-safe snippet of at most maxWords words with matching
// words wrapped in <mark>. terms are stemmed query terms.
func Make(text string, terms []string, maxWords int) string {
	fields := strings.Fields(text)
	if len(fields) > MaxScanWords {
		fields = fields[:MaxScanWords]
	}
	if len(fields) == 0 {
		return ""
	}
	if maxWords <= 0 {
		maxWords = 30
	}
	m := matcher{want: make(map[string]int, len(terms)), cache: map[string]int{}}
	for i, t := range terms {
		m.want[t] = i
	}
	// For each field, the query-term index it matches (or -1).
	match := make([]int, len(fields))
	var hits []int
	for i, f := range fields {
		match[i] = -1
		forEachWord(f, func(w string) bool {
			if idx, ok := m.match(w); ok {
				match[i] = idx
				hits = append(hits, i)
				return true
			}
			return false
		})
	}
	start := 0
	if len(hits) > 0 {
		best, bestScore := hits[0], -1
		for _, h := range hits {
			s := h - 3
			if s < 0 {
				s = 0
			}
			end := s + maxWords
			if end > len(fields) {
				end = len(fields)
			}
			distinct := map[int]bool{}
			count := 0
			for j := s; j < end; j++ {
				if match[j] >= 0 {
					distinct[match[j]] = true
					count++
				}
			}
			if score := len(distinct)*100 + count; score > bestScore {
				best, bestScore = s, score
			}
		}
		start = best
	}
	end := start + maxWords
	if end > len(fields) {
		end = len(fields)
	}
	var sb strings.Builder
	if start > 0 {
		sb.WriteString("… ")
	}
	for i := start; i < end; i++ {
		if i > start {
			sb.WriteByte(' ')
		}
		if match[i] < 0 {
			sb.WriteString(html.EscapeString(fields[i]))
			continue
		}
		for _, seg := range segments(fields[i]) {
			if isWordRune([]rune(seg)[0]) {
				if _, ok := m.match(seg); ok {
					sb.WriteString("<mark>" + html.EscapeString(seg) + "</mark>")
					continue
				}
			}
			sb.WriteString(html.EscapeString(seg))
		}
	}
	if end < len(fields) {
		sb.WriteString(" …")
	}
	return sb.String()
}

// segments splits f into alternating runs of word and non-word runes, so
// "str.join(x)" becomes str . join ( x ).
func segments(f string) []string {
	var out []string
	var cur []rune
	var inWord bool
	for _, r := range f {
		w := isWordRune(r)
		if len(cur) > 0 && w != inWord {
			out = append(out, string(cur))
			cur = cur[:0]
		}
		inWord = w
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// matcher maps surface words to the index of the query term they stem to.
// Stemming dominates snippet cost on long pages, so results are cached per
// distinct word (documents repeat the same words constantly).
type matcher struct {
	want  map[string]int
	cache map[string]int // lower-cased word -> term index, -1 = no match
}

// match reports which query term the word w (a run of letters/digits) stems to.
func (m *matcher) match(w string) (int, bool) {
	w = strings.ToLower(w) // no allocation when w is already lower case
	idx, ok := m.cache[w]
	if !ok {
		idx = -1
		if !tokenize.IsStop(w) {
			if i, found := m.want[tokenize.Normalize(w)]; found {
				idx = i
			}
		}
		m.cache[w] = idx
	}
	return idx, idx >= 0
}

// forEachWord calls fn for every maximal run of letters/digits in f, without
// allocating, until fn returns true.
func forEachWord(f string, fn func(string) bool) {
	start := -1
	for i, r := range f {
		if isWordRune(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			if fn(f[start:i]) {
				return
			}
			start = -1
		}
	}
	if start >= 0 {
		fn(f[start:])
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
