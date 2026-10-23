// Package index builds and reads the on-disk positional inverted index.
//
// Directory layout (all files immutable once written):
//
//	meta.json      corpus statistics (doc count, average length, ...)
//	dict.bin       sorted term dictionary: term, doc frequency, postings offset/length
//	postings.bin   per term: uvarint(df) then, per doc, uvarint(docID delta),
//	               uvarint(tf), then tf uvarint position deltas
//	docs.jsonl     one metadata line per document (url, title, length, text offset)
//	text.bin       gzip-compressed document text, one blob per document (for snippets)
//	vocab.json     surface-word counts for autocomplete
//	pagerank.json  optional: PageRank score per docID
package index

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Alkarim786/search-engine-from-scratch/internal/tokenize"
)

// Version of the on-disk format.
const Version = 1

// DocMeta is the per-document metadata kept in memory at query time.
type DocMeta struct {
	ID       uint32 `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Len      int    `json:"len"`       // number of indexed tokens (stopwords excluded)
	TitleLen int    `json:"title_len"` // positions < TitleLen belong to the title
	TextOff  int64  `json:"text_off"`
	TextLen  int    `json:"text_len"`
}

// Meta holds corpus-level statistics.
type Meta struct {
	Version     int     `json:"version"`
	NumDocs     int     `json:"num_docs"`
	NumTerms    int     `json:"num_terms"`
	TotalTokens int64   `json:"total_tokens"`
	AvgDL       float64 `json:"avg_dl"`
}

type termBuf struct {
	df   uint32
	last uint32 // last docID appended (delta base)
	buf  []byte
}

// Builder accumulates documents and writes the index. Documents must be added
// from a single goroutine.
type Builder struct {
	dir      string
	terms    map[string]*termBuf
	docs     []DocMeta
	text     *os.File
	textW    *bufio.Writer
	textOff  int64
	total    int64
	vocab    map[string]int
	scratch  [binary.MaxVarintLen64]byte
	byTerm   map[string][]uint32
	gzBuf    bytes.Buffer
	finished bool
}

// NewBuilder creates dir and starts a new index there.
func NewBuilder(dir string) (*Builder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(dir, "text.bin"))
	if err != nil {
		return nil, err
	}
	return &Builder{
		dir: dir, terms: map[string]*termBuf{}, text: f, textW: bufio.NewWriterSize(f, 1<<20),
		vocab: map[string]int{}, byTerm: map[string][]uint32{},
	}, nil
}

func (b *Builder) putUvarint(dst []byte, v uint64) []byte {
	n := binary.PutUvarint(b.scratch[:], v)
	return append(dst, b.scratch[:n]...)
}

// Add indexes one document and returns its docID. Title tokens occupy the
// first positions; body tokens follow after a one-word gap.
func (b *Builder) Add(url, title, text string) (uint32, error) {
	id := uint32(len(b.docs))
	titleWords := len(tokenize.Words(title))
	toks := tokenize.Analyze(title)
	for _, t := range tokenize.Analyze(text) {
		toks = append(toks, tokenize.Token{Term: t.Term, Pos: t.Pos + titleWords + 1})
	}
	for k := range b.byTerm {
		delete(b.byTerm, k)
	}
	for _, t := range toks {
		b.byTerm[t.Term] = append(b.byTerm[t.Term], uint32(t.Pos))
	}
	for term, positions := range b.byTerm {
		tb := b.terms[term]
		if tb == nil {
			tb = &termBuf{}
			b.terms[term] = tb
		}
		tb.buf = b.putUvarint(tb.buf, uint64(id-tb.last))
		tb.buf = b.putUvarint(tb.buf, uint64(len(positions)))
		prev := uint32(0)
		for _, p := range positions {
			tb.buf = b.putUvarint(tb.buf, uint64(p-prev))
			prev = p
		}
		tb.df++
		tb.last = id
	}
	for _, w := range tokenize.Words(text) {
		if len(w) >= 3 && !tokenize.IsStop(w) && isASCIILetters(w) {
			b.vocab[w]++
		}
	}

	b.gzBuf.Reset()
	zw := gzip.NewWriter(&b.gzBuf)
	if _, err := zw.Write([]byte(text)); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	n, err := b.textW.Write(b.gzBuf.Bytes())
	if err != nil {
		return 0, err
	}
	b.docs = append(b.docs, DocMeta{ID: id, URL: url, Title: title, Len: len(toks), TitleLen: titleWords, TextOff: b.textOff, TextLen: n})
	b.textOff += int64(n)
	b.total += int64(len(toks))
	return id, nil
}

func isASCIILetters(w string) bool {
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return true
}

// Abort releases the Builder's open file after a failed build. It is a no-op
// once Finish has run.
func (b *Builder) Abort() {
	if !b.finished {
		b.finished = true
		b.text.Close()
	}
}

// NumDocs returns the number of documents added so far.
func (b *Builder) NumDocs() int { return len(b.docs) }

// DocURL returns the URL of an already-added document.
func (b *Builder) DocURL(id uint32) string { return b.docs[id].URL }

// maxVocab bounds vocab.json; the long tail is not useful for autocomplete.
const maxVocab = 50000

// Finish writes the dictionary, postings and metadata. The Builder must not be
// used afterwards.
func (b *Builder) Finish() (*Meta, error) {
	if b.finished {
		return nil, fmt.Errorf("index: Finish called twice")
	}
	b.finished = true
	if err := b.textW.Flush(); err != nil {
		return nil, err
	}
	if err := b.text.Close(); err != nil {
		return nil, err
	}
	terms := make([]string, 0, len(b.terms))
	for t := range b.terms {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	pf, err := os.Create(filepath.Join(b.dir, "postings.bin"))
	if err != nil {
		return nil, err
	}
	pw := bufio.NewWriterSize(pf, 1<<20)
	df, err := os.Create(filepath.Join(b.dir, "dict.bin"))
	if err != nil {
		return nil, err
	}
	dw := bufio.NewWriterSize(df, 1<<20)
	var off uint64
	var tmp []byte
	for _, t := range terms {
		tb := b.terms[t]
		tmp = b.putUvarint(tmp[:0], uint64(tb.df))
		tmp = append(tmp, tb.buf...)
		if _, err := pw.Write(tmp); err != nil {
			return nil, err
		}
		var rec []byte
		rec = b.putUvarint(rec, uint64(len(t)))
		rec = append(rec, t...)
		rec = b.putUvarint(rec, uint64(tb.df))
		rec = b.putUvarint(rec, off)
		rec = b.putUvarint(rec, uint64(len(tmp)))
		if _, err := dw.Write(rec); err != nil {
			return nil, err
		}
		off += uint64(len(tmp))
	}
	for _, w := range []*bufio.Writer{pw, dw} {
		if err := w.Flush(); err != nil {
			return nil, err
		}
	}
	if err := pf.Close(); err != nil {
		return nil, err
	}
	if err := df.Close(); err != nil {
		return nil, err
	}

	// docs.jsonl
	docF, err := os.Create(filepath.Join(b.dir, "docs.jsonl"))
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(docF)
	for _, d := range b.docs {
		if err := enc.Encode(d); err != nil {
			return nil, err
		}
	}
	if err := docF.Close(); err != nil {
		return nil, err
	}

	// vocab.json: top surface words by frequency.
	type wc struct {
		W string `json:"w"`
		C int    `json:"c"`
	}
	vs := make([]wc, 0, len(b.vocab))
	for w, c := range b.vocab {
		vs = append(vs, wc{w, c})
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].C != vs[j].C {
			return vs[i].C > vs[j].C
		}
		return vs[i].W < vs[j].W
	})
	if len(vs) > maxVocab {
		vs = vs[:maxVocab]
	}
	if err := writeJSON(filepath.Join(b.dir, "vocab.json"), vs); err != nil {
		return nil, err
	}

	m := &Meta{Version: Version, NumDocs: len(b.docs), NumTerms: len(terms), TotalTokens: b.total}
	if m.NumDocs > 0 {
		m.AvgDL = float64(b.total) / float64(m.NumDocs)
	}
	if err := writeJSON(filepath.Join(b.dir, "meta.json"), m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(v); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WritePageRank stores PageRank scores (indexed by docID) in dir.
func WritePageRank(dir string, scores []float64) error {
	return writeJSON(filepath.Join(dir, "pagerank.json"), scores)
}
