package index

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Posting is one document's occurrence list for a term.
type Posting struct {
	Doc uint32
	TF  uint32
	// First is the term's earliest position in the document (always set; cheap
	// way to test "appears in the title" without materialising Pos).
	First uint32
	Pos   []uint32 // ascending word positions; nil when decoded without positions
}

type dictEntry struct {
	df  uint32
	off int64
	n   int
}

// Index is a read-only view of an index directory. It is safe for concurrent use.
type Index struct {
	dir  string
	Meta Meta
	dict map[string]dictEntry
	docs []DocMeta
	pr   []float64
	post *os.File
	text *os.File
}

// Open loads the dictionary and document table into memory and keeps the
// postings and text files open for random access.
func Open(dir string) (*Index, error) {
	ix := &Index{dir: dir, dict: map[string]dictEntry{}}
	if err := readJSON(filepath.Join(dir, "meta.json"), &ix.Meta); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	if ix.Meta.Version != Version {
		return nil, fmt.Errorf("index: unsupported version %d", ix.Meta.Version)
	}
	db, err := os.ReadFile(filepath.Join(dir, "dict.bin"))
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(db)
	for r.Len() > 0 {
		l, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		term := make([]byte, l)
		if _, err := io.ReadFull(r, term); err != nil {
			return nil, err
		}
		df, e1 := binary.ReadUvarint(r)
		off, e2 := binary.ReadUvarint(r)
		n, e3 := binary.ReadUvarint(r)
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, err
		}
		ix.dict[string(term)] = dictEntry{df: uint32(df), off: int64(off), n: int(n)}
	}
	f, err := os.Open(filepath.Join(dir, "docs.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var d DocMeta
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			return nil, err
		}
		ix.docs = append(ix.docs, d)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(ix.docs) != ix.Meta.NumDocs {
		return nil, fmt.Errorf("index: docs.jsonl has %d docs, meta says %d", len(ix.docs), ix.Meta.NumDocs)
	}
	if err := readJSON(filepath.Join(dir, "pagerank.json"), &ix.pr); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if ix.pr != nil && len(ix.pr) != len(ix.docs) {
		return nil, fmt.Errorf("index: pagerank.json has %d scores for %d docs", len(ix.pr), len(ix.docs))
	}
	if ix.post, err = os.Open(filepath.Join(dir, "postings.bin")); err != nil {
		return nil, err
	}
	if ix.text, err = os.Open(filepath.Join(dir, "text.bin")); err != nil {
		ix.post.Close()
		return nil, err
	}
	return ix, nil
}

// Close releases the open files.
func (ix *Index) Close() error { return errors.Join(ix.post.Close(), ix.text.Close()) }

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

// NumDocs is the number of indexed documents.
func (ix *Index) NumDocs() int { return len(ix.docs) }

// Doc returns the metadata of a document.
func (ix *Index) Doc(id uint32) DocMeta { return ix.docs[id] }

// PageRank returns the stored PageRank of a document, or 0 if none was computed.
func (ix *Index) PageRank(id uint32) float64 {
	if ix.pr == nil {
		return 0
	}
	return ix.pr[id]
}

// HasPageRank reports whether pagerank.json was loaded.
func (ix *Index) HasPageRank() bool { return ix.pr != nil }

// DF returns the document frequency of an index term (0 if unknown).
func (ix *Index) DF(term string) int { return int(ix.dict[term].df) }

// Terms returns every indexed term (unsorted).
func (ix *Index) Terms() []string {
	out := make([]string, 0, len(ix.dict))
	for t := range ix.dict {
		out = append(out, t)
	}
	return out
}

// Postings reads and decodes the posting list of term. Positions are decoded
// only when withPositions is true.
func (ix *Index) Postings(term string, withPositions bool) ([]Posting, error) {
	e, ok := ix.dict[term]
	if !ok {
		return nil, nil
	}
	buf := make([]byte, e.n)
	if _, err := ix.post.ReadAt(buf, e.off); err != nil && !(errors.Is(err, io.EOF) && len(buf) == e.n) {
		return nil, err
	}
	return DecodePostings(buf, withPositions)
}

// DecodePostings parses one encoded posting list.
func DecodePostings(buf []byte, withPositions bool) ([]Posting, error) {
	r := bytes.NewReader(buf)
	df, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	out := make([]Posting, 0, df)
	var doc uint32
	for i := uint64(0); i < df; i++ {
		delta, e1 := binary.ReadUvarint(r)
		tf, e2 := binary.ReadUvarint(r)
		if err := errors.Join(e1, e2); err != nil {
			return nil, fmt.Errorf("index: corrupt postings: %w", err)
		}
		doc += uint32(delta)
		p := Posting{Doc: doc, TF: uint32(tf)}
		if withPositions {
			p.Pos = make([]uint32, tf)
		}
		var pos uint32
		for j := uint64(0); j < tf; j++ {
			d, err := binary.ReadUvarint(r)
			if err != nil {
				return nil, fmt.Errorf("index: corrupt postings: %w", err)
			}
			pos += uint32(d)
			if j == 0 {
				p.First = pos
			}
			if withPositions {
				p.Pos[j] = pos
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Text returns the stored plain text of a document (for snippets).
func (ix *Index) Text(id uint32) (string, error) {
	d := ix.docs[id]
	buf := make([]byte, d.TextLen)
	if _, err := ix.text.ReadAt(buf, d.TextOff); err != nil && !(errors.Is(err, io.EOF) && len(buf) == d.TextLen) {
		return "", err
	}
	zr, err := gzip.NewReader(bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	return string(b), err
}

// Size reports the on-disk size in bytes of each index file.
func (ix *Index) Size() map[string]int64 {
	out := map[string]int64{}
	for _, n := range []string{"dict.bin", "postings.bin", "docs.jsonl", "text.bin", "vocab.json", "pagerank.json", "meta.json"} {
		if st, err := os.Stat(filepath.Join(ix.dir, n)); err == nil {
			out[n] = st.Size()
		}
	}
	return out
}

// VocabEntry is a surface word and its corpus frequency.
type VocabEntry struct {
	W string `json:"w"`
	C int    `json:"c"`
}

// Vocab loads vocab.json (surface words for autocomplete).
func (ix *Index) Vocab() ([]VocabEntry, error) {
	var v []VocabEntry
	err := readJSON(filepath.Join(ix.dir, "vocab.json"), &v)
	return v, err
}

// Dir is the directory the index was opened from.
func (ix *Index) Dir() string { return ix.dir }
