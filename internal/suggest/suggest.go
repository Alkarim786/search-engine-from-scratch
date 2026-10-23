// Package suggest implements query autocomplete with a prefix trie that caches
// the top-k completions at every node, so a lookup is O(len(prefix) + k)
// regardless of how many entries share the prefix.
package suggest

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Entry is a completion and its weight.
type Entry struct {
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
}

type node struct {
	children map[rune]*node
	top      []Entry // best-first, at most k, all entries under this prefix
}

// Trie is a weighted prefix trie. Safe for concurrent use.
type Trie struct {
	mu      sync.RWMutex
	root    *node
	k       int
	weights map[string]float64
}

// NewTrie returns a trie that keeps the best k completions per prefix.
func NewTrie(k int) *Trie {
	if k <= 0 {
		k = 10
	}
	return &Trie{root: &node{children: map[rune]*node{}}, k: k, weights: map[string]float64{}}
}

func normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// Insert adds w to the entry's weight (creating it if needed).
func (t *Trie) Insert(text string, w float64) {
	text = normalize(text)
	if text == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	total := t.weights[text] + w
	t.weights[text] = total
	n := t.root
	for _, r := range text {
		c := n.children[r]
		if c == nil {
			c = &node{children: map[rune]*node{}}
			n.children[r] = c
		}
		n = c
		n.top = t.upsert(n.top, Entry{text, total})
	}
}

func better(a, b Entry) bool {
	if a.Weight != b.Weight {
		return a.Weight > b.Weight
	}
	return a.Text < b.Text
}

func (t *Trie) upsert(top []Entry, e Entry) []Entry {
	for i := range top {
		if top[i].Text == e.Text {
			top[i] = e
			sort.Slice(top, func(a, b int) bool { return better(top[a], top[b]) })
			return top
		}
	}
	top = append(top, e)
	sort.Slice(top, func(a, b int) bool { return better(top[a], top[b]) })
	if len(top) > t.k {
		top = top[:t.k]
	}
	return top
}

// Complete returns up to k best entries starting with prefix.
func (t *Trie) Complete(prefix string) []Entry {
	prefix = strings.TrimLeftFunc(strings.ToLower(prefix), unicode.IsSpace)
	prefix = strings.Join(strings.Fields(prefix), " ") + trailingSpace(prefix)
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.root
	for _, r := range prefix {
		n = n.children[r]
		if n == nil {
			return nil
		}
	}
	out := make([]Entry, len(n.top))
	copy(out, n.top)
	return out
}

func trailingSpace(s string) string {
	if strings.HasSuffix(s, " ") && strings.TrimSpace(s) != "" {
		return " "
	}
	return ""
}

// Len returns the number of distinct entries.
func (t *Trie) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.weights)
}

// Suggester combines a phrase trie (page titles and past queries) with a word
// trie (corpus vocabulary) to complete the query being typed.
type Suggester struct {
	phrases *Trie
	words   *Trie
	k       int
}

// New returns an empty Suggester returning up to k suggestions.
func New(k int) *Suggester {
	return &Suggester{phrases: NewTrie(k), words: NewTrie(k), k: k}
}

// AddPhrase registers a title or query with a weight.
func (s *Suggester) AddPhrase(text string, w float64) { s.phrases.Insert(text, w) }

// AddWord registers a vocabulary word with its corpus frequency.
func (s *Suggester) AddWord(w string, count float64) { s.words.Insert(w, count) }

// Observe records that users searched for q (so popular queries rise).
func (s *Suggester) Observe(q string) { s.phrases.Insert(q, 1) }

// Suggest completes the text typed so far. Phrase matches (whole titles and
// past queries beginning with the input) come first; then the last word is
// completed from the vocabulary while keeping the words before it.
func (s *Suggester) Suggest(input string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(x string) {
		if x != "" && !seen[x] && len(out) < s.k {
			seen[x] = true
			out = append(out, x)
		}
	}
	norm := strings.ToLower(strings.TrimLeftFunc(input, unicode.IsSpace))
	if strings.TrimSpace(norm) == "" {
		return nil
	}
	for _, e := range s.phrases.Complete(norm) {
		add(e.Text)
	}
	if strings.HasSuffix(norm, " ") { // the user finished a word; nothing to complete
		return out
	}
	fields := strings.Fields(norm)
	last := fields[len(fields)-1]
	head := strings.Join(fields[:len(fields)-1], " ")
	for _, e := range s.words.Complete(last) {
		if head == "" {
			add(e.Text)
		} else {
			add(head + " " + e.Text)
		}
	}
	return out
}
