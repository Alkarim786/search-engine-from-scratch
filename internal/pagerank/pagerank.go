// Package pagerank computes PageRank by power iteration on a link graph.
//
// PageRank is the stationary distribution of a random surfer who follows a
// random outgoing link with probability d and teleports to a uniformly random
// page with probability 1-d. Pages with no outgoing links ("dangling") are
// treated as linking to every page, which keeps the transition matrix
// stochastic so the scores sum to 1.
package pagerank

import "math"

// Options configure Compute. Zero values select the defaults.
type Options struct {
	Damping float64 // default 0.85
	Tol     float64 // L1 convergence threshold, default 1e-6
	MaxIter int     // default 200
}

// Result of a computation.
type Result struct {
	Scores     []float64
	Iterations int
	Delta      float64 // final L1 change between iterations
	Converged  bool
}

// Compute returns the PageRank of an n-node graph given as adjacency lists
// (out[i] = targets of node i). Duplicate edges and self-loops are ignored.
func Compute(out [][]int, o Options) Result {
	n := len(out)
	if o.Damping == 0 {
		o.Damping = 0.85
	}
	if o.Tol == 0 {
		o.Tol = 1e-6
	}
	if o.MaxIter == 0 {
		o.MaxIter = 200
	}
	if n == 0 {
		return Result{Converged: true}
	}
	// Clean the graph once: drop self-loops and duplicate edges.
	adj := make([][]int, n)
	seen := make([]int, n)
	for i := range seen {
		seen[i] = -1
	}
	for i, ts := range out {
		for _, t := range ts {
			if t == i || t < 0 || t >= n || seen[t] == i {
				continue
			}
			seen[t] = i
			adj[i] = append(adj[i], t)
		}
	}
	d := o.Damping
	cur := make([]float64, n)
	next := make([]float64, n)
	for i := range cur {
		cur[i] = 1 / float64(n)
	}
	res := Result{}
	for it := 1; it <= o.MaxIter; it++ {
		var dangling float64
		for i := range next {
			next[i] = 0
		}
		for i, ts := range adj {
			if len(ts) == 0 {
				dangling += cur[i]
				continue
			}
			share := cur[i] / float64(len(ts))
			for _, t := range ts {
				next[t] += share
			}
		}
		base := (1-d)/float64(n) + d*dangling/float64(n)
		var delta float64
		for i := range next {
			next[i] = base + d*next[i]
			delta += math.Abs(next[i] - cur[i])
		}
		cur, next = next, cur
		res.Iterations, res.Delta = it, delta
		if delta < o.Tol {
			res.Converged = true
			break
		}
	}
	res.Scores = cur
	return res
}
