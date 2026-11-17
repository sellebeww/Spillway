package main

import (
	"math"
	"strings"
	"testing"
)

func TestHistQuantilesInterpolateLikePrometheus(t *testing.T) {
	text := `# HELP x
x_bucket{le="0.001"} 0
x_bucket{le="0.002"} 50
x_bucket{le="0.004"} 100
x_bucket{le="+Inf"} 100
x_count 100
`
	after := parseProm(strings.NewReader(text))
	p50, p95, p99 := histQuantiles(map[string]float64{}, after, "x")
	if math.Abs(p50-2) > 1e-9 || math.Abs(p95-3.8) > 1e-9 || math.Abs(p99-3.96) > 1e-9 {
		t.Fatalf("p50=%v p95=%v p99=%v", p50, p95, p99)
	}
	// Deltas: observations before the phase must not count.
	before := parseProm(strings.NewReader(text))
	if a, b, c := histQuantiles(before, after, "x"); a != 0 || b != 0 || c != 0 {
		t.Fatalf("empty phase: %v %v %v", a, b, c)
	}
}

func TestParseMiB(t *testing.T) {
	for in, want := range map[string]float64{"512MiB": 512, "1.5GiB": 1536, "2048KiB": 2} {
		if got := parseMiB(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: %v", in, got)
		}
	}
}
