// Package dedup detects duplicate page content: exact duplicates by SHA-256
// and near duplicates by 64-bit SimHash over word 3-shingles.
package dedup

import (
	"crypto/sha256"
	"hash/fnv"
	"math/bits"
	"strings"
	"sync"
)

// MinWordsForSimHash: very short pages produce unreliable fingerprints, so
// only the exact-hash check applies to them.
const MinWordsForSimHash = 40

// MaxDistance is the Hamming distance at or below which two SimHashes are
// treated as near-duplicates. With 4 bands of 16 bits, any pair within
// distance 3 shares at least one band exactly (pigeonhole), so lookups are exact.
const MaxDistance = 3

// SimHash fingerprints words using 3-word shingles.
func SimHash(words []string) uint64 {
	var v [64]int
	n := 3
	if len(words) < n {
		n = len(words)
	}
	for i := 0; i+n <= len(words); i++ {
		h := fnv.New64a()
		h.Write([]byte(strings.Join(words[i:i+n], " ")))
		x := h.Sum64()
		for b := 0; b < 64; b++ {
			if x&(1<<uint(b)) != 0 {
				v[b]++
			} else {
				v[b]--
			}
		}
	}
	var out uint64
	for b := 0; b < 64; b++ {
		if v[b] > 0 {
			out |= 1 << uint(b)
		}
	}
	return out
}

// Distance is the Hamming distance between two fingerprints.
func Distance(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// Detector remembers fingerprints and reports duplicates. Safe for concurrent use.
type Detector struct {
	mu     sync.Mutex
	exact  map[[32]byte]struct{}
	bands  [4]map[uint16][]uint64
	nWords int
}

// New returns an empty Detector.
func New() *Detector {
	d := &Detector{exact: map[[32]byte]struct{}{}}
	for i := range d.bands {
		d.bands[i] = map[uint16][]uint64{}
	}
	return d
}

// Kind of duplicate found.
type Kind int

const (
	Unique Kind = iota
	Exact
	Near
)

// Check records the content and reports whether it duplicates earlier content.
// Duplicates are not recorded.
func (d *Detector) Check(text string, words []string) Kind {
	sum := sha256.Sum256([]byte(text))
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.exact[sum]; ok {
		return Exact
	}
	useSim := len(words) >= MinWordsForSimHash
	var sim uint64
	if useSim {
		sim = SimHash(words)
		for i := 0; i < 4; i++ {
			for _, other := range d.bands[i][uint16(sim>>(16*uint(i)))] {
				if Distance(sim, other) <= MaxDistance {
					return Near
				}
			}
		}
	}
	d.exact[sum] = struct{}{}
	if useSim {
		for i := 0; i < 4; i++ {
			k := uint16(sim >> (16 * uint(i)))
			d.bands[i][k] = append(d.bands[i][k], sim)
		}
	}
	return Unique
}
