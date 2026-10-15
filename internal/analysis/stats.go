package analysis

import (
	"math"
	"sort"
)

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// quantile returns the q-quantile (0..1) of xs using linear interpolation.
// xs is sorted in place.
func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	sort.Float64s(xs)
	pos := q * float64(len(xs)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return xs[lo] + (xs[hi]-xs[lo])*(pos-float64(lo))
}

func quantile32(xs []float32, q float64) float64 {
	f := make([]float64, len(xs))
	for i, x := range xs {
		f[i] = float64(x)
	}
	return quantile(f, q)
}

// pearson returns the Pearson correlation coefficient of x and y.
func pearson(x, y []float64) float64 {
	n := len(x)
	if n < 3 || n != len(y) {
		return math.NaN()
	}
	mx, my := mean(x), mean(y)
	var sxy, sxx, syy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// ranks returns average ranks (ties share the mean rank).
func ranks(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, len(x))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

// spearman returns Spearman's rank correlation (robust to the heavy skew in
// count data such as crimes-per-neighborhood).
func spearman(x, y []float64) float64 { return pearson(ranks(x), ranks(y)) }

// strength describes |r| in plain language for non-technical stakeholders.
func strength(r float64) string {
	a := math.Abs(r)
	switch {
	case math.IsNaN(r):
		return "undetermined"
	case a >= 0.7:
		return "strong"
	case a >= 0.5:
		return "moderate-to-strong"
	case a >= 0.3:
		return "moderate"
	case a >= 0.1:
		return "weak"
	}
	return "negligible"
}

// quartileOf assigns each value to quartile 0..3 by rank (equal-count bins).
func quartileOf(vals []float64) []int {
	idx := make([]int, len(vals))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return vals[idx[a]] < vals[idx[b]] })
	q := make([]int, len(vals))
	for rank, i := range idx {
		q[i] = rank * 4 / len(vals)
	}
	return q
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return math.NaN()
	}
	return a / b
}

// minMax rescales xs to 0..1. NaN inputs (e.g. a rate with a zero
// denominator) are scored 0 rather than poisoning the whole column.
func minMax(xs []float64) []float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range xs {
		if math.IsNaN(x) {
			continue
		}
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	out := make([]float64, len(xs))
	for i, x := range xs {
		if hi > lo && !math.IsNaN(x) {
			out[i] = (x - lo) / (hi - lo)
		}
	}
	return out
}
