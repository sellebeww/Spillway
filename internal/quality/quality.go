// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package quality scores data quality per reading and per device (docs/SPEC.md section 5.3).
// Everything here is pure: time comes from the readings and arguments, never the system clock.
package quality

import (
	"math"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/stats"
)

// madToSigma scales the MAD to a standard deviation estimate for normal data.
const madToSigma = 1.4826

// Component scores per flag (section 5.3). The point score is the minimum of them.
const (
	scoreOutOfRange = 0.0
	scoreSpike      = 0.2
	scoreFlatline   = 0.3
	scoreClockSkew  = 0.5
	scoreLowBattery = 0.9
)

// Config holds the tunable parameters; DefaultConfig gives the documented defaults.
type Config struct {
	LowBattV         float64 // below this voltage the reading is flagged LOW_BATTERY
	FlatlineN        int     // identical consecutive raw values that make a flatline
	HampelWindow     int     // number of recent in-range values kept
	HampelMinHistory int     // history needed before spikes can be flagged
	HampelK          float64 // threshold multiplier
	MADFloorCm       float64 // lower bound for the MAD, in cm
}

// DefaultConfig returns the defaults from docs/SPEC.md section 5.3.
func DefaultConfig() Config {
	return Config{LowBattV: 3.3, FlatlineN: 20, HampelWindow: 7, HampelMinHistory: 5, HampelK: 3, MADFloorCm: 0.5}
}

// Result is the quality verdict for one reading.
type Result struct {
	Flags []domain.QualityFlag
	Score float64
}

// Input is one historical reading with the calibration that applied to it.
type Input struct {
	Reading domain.Reading
	Cal     domain.Calibration
}

// Evaluator is the causal per-device quality state. Feed readings in processing
// order. It is not safe for concurrent use; one goroutine owns one device.
type Evaluator struct {
	cfg     Config
	window  []float64 // last HampelWindow in-range distances, cm
	flatRun int       // consecutive identical raw_mm
	lastRaw int
	hasLast bool
	sid     string // current session id
	haveSID bool
	synced  bool // the session's first reading had a trustworthy clock
}

// NewEvaluator returns an empty evaluator.
func NewEvaluator(cfg Config) *Evaluator { return &Evaluator{cfg: cfg} }

// Rebuild returns the evaluator state after replaying history in order, identical
// to the state a live evaluator would have. Supply at least the last
// max(FlatlineN, HampelWindow) readings plus the first reading of the current session.
func Rebuild(cfg Config, history []Input) *Evaluator {
	e := NewEvaluator(cfg)
	for _, h := range history {
		e.Evaluate(h.Reading, h.Cal)
	}
	return e
}

// Evaluate scores r against the calibration that applies to it and updates the state.
func (e *Evaluator) Evaluate(r domain.Reading, cal domain.Calibration) Result {
	off := r.ReceivedAt.Sub(r.TS) // positive: the reading is old

	// The first reading of a session decides whether the device clock is trustworthy.
	// A trustworthy clock makes a later large offset a late (buffered) message, not skew.
	if !e.haveSID || r.SID != e.sid {
		e.sid, e.haveSID = r.SID, true
		e.synced = off >= -domain.ClockSkewAfter && off <= domain.ClockSkewAfter
	}
	skew := off < -domain.ClockSkewAfter || (off > domain.ClockSkewAfter && !e.synced)
	late := !skew && off > domain.LateAfter

	outOfRange := r.RawMM < cal.MinValidMM || r.RawMM > cal.MaxValidMM

	if e.hasLast && r.RawMM == e.lastRaw {
		e.flatRun++
	} else {
		e.flatRun = 1
	}
	e.lastRaw, e.hasLast = r.RawMM, true
	flat := e.flatRun >= e.cfg.FlatlineN

	spike := false
	if !outOfRange {
		cm := float64(r.RawMM) / 10
		spike = e.isSpike(cm)
		e.window = append(e.window, cm)
		if len(e.window) > e.cfg.HampelWindow {
			e.window = e.window[len(e.window)-e.cfg.HampelWindow:]
		}
	}

	low := r.BattV != nil && *r.BattV < e.cfg.LowBattV

	score := 1.0
	var flags []domain.QualityFlag
	add := func(cond bool, f domain.QualityFlag, s float64) {
		if cond {
			flags = append(flags, f)
			score = math.Min(score, s)
		}
	}
	add(outOfRange, domain.FlagOutOfRange, scoreOutOfRange)
	add(spike, domain.FlagSpike, scoreSpike)
	add(flat, domain.FlagFlatline, scoreFlatline)
	add(skew, domain.FlagClockSkew, scoreClockSkew)
	add(low, domain.FlagLowBattery, scoreLowBattery)
	add(late, domain.FlagLate, 1.0) // informational only
	if len(flags) == 0 {
		flags = []domain.QualityFlag{domain.FlagOK}
	}
	return Result{Flags: flags, Score: score}
}

// isSpike is a causal Hampel test of cm against the recent in-range window.
// The window deliberately includes earlier spikes: a persistent level change then
// stops being flagged once the window has adapted instead of locking in forever.
func (e *Evaluator) isSpike(cm float64) bool {
	if len(e.window) < e.cfg.HampelMinHistory {
		return false
	}
	med := stats.Median(e.window)
	mad := math.Max(stats.MAD(e.window), e.cfg.MADFloorCm)
	return math.Abs(cm-med) > e.cfg.HampelK*madToSigma*mad
}
