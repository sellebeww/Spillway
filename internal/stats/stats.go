// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package stats provides small statistics helpers shared by quality and rules.
package stats

import (
	"math"
	"slices"
)

// Median returns the median of v without modifying it. Empty input gives 0.
func Median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// MAD returns the median absolute deviation of v (unscaled). Empty input gives 0.
func MAD(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := Median(v)
	dev := make([]float64, len(v))
	for i, x := range v {
		dev[i] = math.Abs(x - m)
	}
	return Median(dev)
}

// OLS fits y = intercept + slope*x by ordinary least squares. Inputs are
// centred first, which keeps epoch-sized x values numerically stable.
// ok is false (and the results are zero) when there are fewer than two points,
// the lengths differ, x has no spread, or anything is not finite.
func OLS(x, y []float64) (slope, intercept float64, ok bool) {
	n := len(x)
	if n < 2 || len(y) != n {
		return 0, 0, false
	}
	var sx, sy float64
	for i := range x {
		if !finite(x[i]) || !finite(y[i]) {
			return 0, 0, false
		}
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var sxx, sxy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxx += dx * dx
		sxy += dx * dy
	}
	if sxx == 0 || !finite(sxx) || !finite(sxy) {
		return 0, 0, false
	}
	slope = sxy / sxx
	intercept = my - slope*mx
	if !finite(slope) || !finite(intercept) {
		return 0, 0, false
	}
	return slope, intercept, true
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
