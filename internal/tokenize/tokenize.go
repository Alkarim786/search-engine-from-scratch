// Package tokenize turns text into index terms: lower-cased words, stopwords
// dropped, Porter-stemmed. Positions count every word (including stopwords) so
// phrase queries can keep the original word distances.
package tokenize

import (
	"strings"
	"unicode"
)

// MaxWordLen drops absurdly long "words" (base64 blobs, minified code).
const MaxWordLen = 40

// Token is an indexable term and its word position in the source text.
type Token struct {
	Term string
	Pos  int
}

var stopwords = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, w := range strings.Fields(`a about above after again against all am an and any are as at be because been
before being below between both but by can could did do does doing down during each few for from further had has have
having he her here hers herself him himself his how i if in into is it its itself just me more most my myself no nor
not now of off on once only or other our ours ourselves out over own same she should so some such than that the their
theirs them themselves then there these they this those through to too under until up very was we were what when where
which while who whom why will with would you your yours yourself yourselves s t`) {
		m[w] = struct{}{}
	}
	return m
}()

// IsStop reports whether w (already lower-case) is a stopword.
func IsStop(w string) bool { _, ok := stopwords[w]; return ok }

// Words splits text into lower-case words (runs of letters and digits).
func Words(text string) []string {
	var out []string
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			if sb.Len() <= MaxWordLen {
				out = append(out, sb.String())
			}
			sb.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return out
}

// Normalize maps a single lower-case word to its index term ("" if it is a
// stopword).
func Normalize(w string) string {
	if IsStop(w) {
		return ""
	}
	return Stem(w)
}

// Analyze returns the indexable tokens of text. Pos is the word offset, so
// stopwords leave gaps in the position sequence.
func Analyze(text string) []Token {
	words := Words(text)
	out := make([]Token, 0, len(words))
	for i, w := range words {
		if t := Normalize(w); t != "" {
			out = append(out, Token{Term: t, Pos: i})
		}
	}
	return out
}
