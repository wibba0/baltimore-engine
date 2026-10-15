package analysis

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPearsonSpearman(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	if !near(pearson(x, []float64{2, 4, 6, 8, 10}), 1) {
		t.Error("perfect positive")
	}
	if !near(pearson(x, []float64{5, 4, 3, 2, 1}), -1) {
		t.Error("perfect negative")
	}
	// Monotonic but non-linear: Spearman is exactly 1, Pearson is not.
	y := []float64{1, 4, 9, 100, 10000}
	if !near(spearman(x, y), 1) || pearson(x, y) >= 1 {
		t.Error("spearman should capture monotonic relation")
	}
}

func TestQuantile(t *testing.T) {
	if q := quantile([]float64{4, 1, 3, 2}, 0.5); !near(q, 2.5) {
		t.Errorf("median = %v", q)
	}
	if !math.IsNaN(quantile(nil, .5)) {
		t.Error("empty should be NaN")
	}
}

func TestQuartileOf(t *testing.T) {
	q := quartileOf([]float64{10, 1, 5, 7, 3, 2, 8, 4})
	counts := map[int]int{}
	for _, v := range q {
		counts[v]++
	}
	for i := 0; i < 4; i++ {
		if counts[i] != 2 {
			t.Fatalf("unequal quartiles: %v", counts)
		}
	}
	if q[1] != 0 || q[0] != 3 {
		t.Errorf("min/max misassigned: %v", q)
	}
}
