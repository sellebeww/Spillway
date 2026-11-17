package stats

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

func TestMedianAndMAD(t *testing.T) {
	cases := []struct {
		name        string
		in          []float64
		median, mad float64
	}{
		{"empty", nil, 0, 0},
		{"single", []float64{4}, 4, 0},
		{"odd", []float64{3, 1, 2}, 2, 1},
		{"even", []float64{1, 2, 3, 4}, 2.5, 1},
		{"all equal gives MAD 0", []float64{5, 5, 5, 5, 5}, 5, 0},
		{"outlier barely moves MAD", []float64{10, 10, 11, 9, 1000}, 10, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			orig := append([]float64(nil), c.in...)
			if got := Median(c.in); got != c.median {
				t.Errorf("Median = %v, want %v", got, c.median)
			}
			if got := MAD(c.in); got != c.mad {
				t.Errorf("MAD = %v, want %v", got, c.mad)
			}
			for i := range orig {
				if orig[i] != c.in[i] {
					t.Fatal("input slice was mutated")
				}
			}
		})
	}
}

func TestOLSKnownLine(t *testing.T) {
	x := []float64{0, 60, 120, 180}
	y := []float64{10, 11, 12, 13} // 1 per 60 s
	slope, intercept, ok := OLS(x, y)
	if !ok || math.Abs(slope-1.0/60) > 1e-12 || math.Abs(intercept-10) > 1e-9 {
		t.Fatalf("got slope=%v intercept=%v ok=%v", slope, intercept, ok)
	}
}

func TestOLSDegenerateInputsAreNotOK(t *testing.T) {
	cases := []struct {
		name string
		x, y []float64
	}{
		{"empty", nil, nil},
		{"one point", []float64{1}, []float64{1}},
		{"length mismatch", []float64{1, 2}, []float64{1}},
		{"constant x", []float64{5, 5, 5}, []float64{1, 2, 3}},
		{"NaN input", []float64{1, 2}, []float64{math.NaN(), 1}},
		{"Inf input", []float64{1, math.Inf(1)}, []float64{1, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, ok := OLS(c.x, c.y); ok {
				t.Fatal("expected ok=false")
			}
		})
	}
}

// refSlope computes the OLS slope exactly with rational arithmetic.
func refSlope(x, y []float64) (*big.Rat, bool) {
	n := big.NewRat(int64(len(x)), 1)
	sx, sy := new(big.Rat), new(big.Rat)
	for i := range x {
		sx.Add(sx, new(big.Rat).SetFloat64(x[i]))
		sy.Add(sy, new(big.Rat).SetFloat64(y[i]))
	}
	mx, my := new(big.Rat).Quo(sx, n), new(big.Rat).Quo(sy, n)
	num, den := new(big.Rat), new(big.Rat)
	for i := range x {
		dx := new(big.Rat).Sub(new(big.Rat).SetFloat64(x[i]), mx)
		dy := new(big.Rat).Sub(new(big.Rat).SetFloat64(y[i]), my)
		num.Add(num, new(big.Rat).Mul(dx, dy))
		den.Add(den, new(big.Rat).Mul(dx, dx))
	}
	if den.Sign() == 0 {
		return nil, false
	}
	return new(big.Rat).Quo(num, den), true
}

// Property test: fixed seed, 300 random cases, compared with an exact reference.
func TestOLSMatchesExactReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 4242)) //nolint:gosec // deterministic seed is the point
	for i := 0; i < 300; i++ {
		n := 2 + rng.IntN(40)
		x, y := make([]float64, n), make([]float64, n)
		cur := 1.7e9 * rng.Float64() // epoch-like offsets stress numerical stability
		for j := range x {
			if rng.IntN(4) != 0 {
				cur += float64(rng.IntN(120))
			}
			x[j] = cur
			y[j] = (rng.Float64() - 0.5) * 2000
		}
		want, wantOK := refSlope(x, y)
		got, _, ok := OLS(x, y)
		if ok != wantOK {
			t.Fatalf("case %d: ok=%v want %v", i, ok, wantOK)
		}
		if !ok {
			continue
		}
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("case %d: non-finite slope %v", i, got)
		}
		w, _ := want.Float64()
		if math.Abs(got-w) > 1e-9*math.Max(1, math.Abs(w)) {
			t.Fatalf("case %d: slope %v, exact %v", i, got, w)
		}
	}
}

func TestOLSNeverNaNOnExtremes(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7)) //nolint:gosec // deterministic seed is the point
	pool := []float64{0, 1, -1, 1e-300, 1e300, -1e300, math.MaxFloat64, math.SmallestNonzeroFloat64, math.NaN(), math.Inf(1), math.Inf(-1)}
	for i := 0; i < 300; i++ {
		n := rng.IntN(6)
		x, y := make([]float64, n), make([]float64, n)
		for j := range x {
			x[j], y[j] = pool[rng.IntN(len(pool))], pool[rng.IntN(len(pool))]
		}
		s, ic, ok := OLS(x, y)
		if ok && (math.IsNaN(s) || math.IsInf(s, 0) || math.IsNaN(ic) || math.IsInf(ic, 0)) {
			t.Fatalf("case %d: x=%v y=%v produced %v %v", i, x, y, s, ic)
		}
	}
}
