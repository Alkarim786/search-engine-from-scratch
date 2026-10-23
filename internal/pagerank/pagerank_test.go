package pagerank

import (
	"math"
	"testing"
)

func sum(x []float64) (s float64) {
	for _, v := range x {
		s += v
	}
	return
}

func near(a, b, eps float64) bool { return math.Abs(a-b) < eps }

// Two pages, A -> B, B dangling. Solving the linear system by hand with d=0.85:
//
//	a = 0.075 + 0.425 b
//	b = 0.075 + 0.85 a + 0.425 b
//
// gives b = 0.138750/0.21375 = 0.649123 and a = 0.350877.
func TestAnalyticTwoNodeDanglingGraph(t *testing.T) {
	r := Compute([][]int{{1}, {}}, Options{Tol: 1e-12})
	if !r.Converged {
		t.Fatal("did not converge")
	}
	if !near(r.Scores[0], 0.350877, 1e-5) || !near(r.Scores[1], 0.649123, 1e-5) {
		t.Fatalf("scores = %v", r.Scores)
	}
}

func TestSumsToOneAndSymmetricCycleIsUniform(t *testing.T) {
	cyc := [][]int{{1}, {2}, {3}, {0}}
	r := Compute(cyc, Options{})
	if !near(sum(r.Scores), 1, 1e-9) {
		t.Fatalf("sum = %v", sum(r.Scores))
	}
	for _, s := range r.Scores {
		if !near(s, 0.25, 1e-6) {
			t.Fatalf("cycle should be uniform: %v", r.Scores)
		}
	}
}

func TestStarGraphHubWins(t *testing.T) {
	// Everyone links to node 0; node 0 links to nobody.
	g := [][]int{{}, {0}, {0}, {0}, {0}}
	r := Compute(g, Options{})
	if !near(sum(r.Scores), 1, 1e-9) {
		t.Fatalf("sum = %v", sum(r.Scores))
	}
	for i := 1; i < 5; i++ {
		if r.Scores[0] <= r.Scores[i] {
			t.Fatalf("hub must outrank leaves: %v", r.Scores)
		}
		if !near(r.Scores[i], r.Scores[1], 1e-12) {
			t.Fatalf("leaves must tie: %v", r.Scores)
		}
	}
}

func TestIgnoresSelfLoopsAndDuplicateEdges(t *testing.T) {
	a := Compute([][]int{{1}, {0}}, Options{})
	b := Compute([][]int{{0, 1, 1, 1}, {0, 0}}, Options{})
	for i := range a.Scores {
		if !near(a.Scores[i], b.Scores[i], 1e-12) {
			t.Fatalf("%v vs %v", a.Scores, b.Scores)
		}
	}
}

func TestAllDanglingIsUniformAndEmptyGraph(t *testing.T) {
	r := Compute([][]int{{}, {}, {}}, Options{})
	for _, s := range r.Scores {
		if !near(s, 1.0/3, 1e-9) {
			t.Fatalf("%v", r.Scores)
		}
	}
	if r := Compute(nil, Options{}); len(r.Scores) != 0 || !r.Converged {
		t.Fatal("empty graph")
	}
}

func TestReportsIterationsAndConvergence(t *testing.T) {
	g := [][]int{{1, 2}, {2}, {0}, {2}}
	r := Compute(g, Options{})
	if !r.Converged || r.Iterations < 2 || r.Delta >= 1e-6 {
		t.Fatalf("%+v", r)
	}
	r = Compute(g, Options{MaxIter: 1, Tol: 1e-15})
	if r.Converged || r.Iterations != 1 {
		t.Fatalf("%+v", r)
	}
}
