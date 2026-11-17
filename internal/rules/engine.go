// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package rules evaluates alert rules over a rolling per-device window (docs/SPEC.md section 5.4).
// The engine is pure: Process returns decisions and has no side effects beyond its own state.
package rules

import (
	"fmt"
	"slices"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/stats"
)

const (
	// windowKeep bounds memory; rate of rise needs only the last 10 minutes.
	windowKeep    = 15 * time.Minute
	minKeptPoints = 3
	// rateHysteresis is the fixed 20 percent hysteresis of RATE_OF_RISE.
	rateHysteresis = 0.8
)

// Point is a quality-scored reading as the rule engine sees it.
type Point struct {
	TS         time.Time // reading time
	ReceivedAt time.Time // server arrival time; drives lateness and cooldowns
	LevelCm    float64
	Score      float64
}

// DecisionKind is what the engine decided about an alert.
type DecisionKind string

// Decision kinds.
const (
	Opened      DecisionKind = "OPENED"
	Escalated   DecisionKind = "ESCALATED"
	Deescalated DecisionKind = "DEESCALATED"
	Closed      DecisionKind = "CLOSED"
	Reminder    DecisionKind = "REMINDER"
)

// Decision is one notification-worthy state change or reminder.
type Decision struct {
	Kind          DecisionKind
	RuleID        string
	Severity      domain.Severity // severity after the decision (0 for Closed)
	Previous      domain.Severity
	At            time.Time // server time of the triggering reading
	TS            time.Time // reading time
	LevelCm       float64
	RateCmPerHour *float64 // RATE_OF_RISE rules only
}

// Engine holds the rule state of one device. Replay readings in arrival order
// (see Rebuild) and the state equals the live state. Not safe for concurrent use.
type Engine struct {
	minQuality float64
	rules      []*ruleState
	window     []Point // sorted by TS, unique TS
	lastValid  time.Time
	hasValid   bool
	lastRecv   time.Time // arrival time of the newest accepted point
}

type ruleState struct {
	rule       domain.Rule
	cur        domain.Severity // 0 means no open alert
	lastNotify time.Time
	rates      []float64 // last two RATE_OF_RISE evaluations (cm/h)
}

// NewEngine validates the rules and returns an empty engine.
func NewEngine(rs []domain.Rule, minQuality float64) (*Engine, error) {
	if minQuality < 0 || minQuality > 1 {
		return nil, fmt.Errorf("min quality %v outside [0,1]", minQuality)
	}
	e := &Engine{minQuality: minQuality}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		e.rules = append(e.rules, &ruleState{rule: r})
	}
	return e, nil
}

// Rebuild replays history (in arrival order) into a fresh engine and discards the
// decisions, so no notification is repeated after a restart.
func Rebuild(rs []domain.Rule, minQuality float64, history []Point) (*Engine, error) {
	e, err := NewEngine(rs, minQuality)
	if err != nil {
		return nil, err
	}
	for _, p := range history {
		e.Process(p)
	}
	return e, nil
}

// Process feeds one reading and returns the resulting decisions (usually none).
func (e *Engine) Process(p Point) []Decision {
	if p.Score < e.minQuality {
		return nil // stored elsewhere, never used by rules
	}
	if age := p.ReceivedAt.Sub(p.TS); age > domain.LateAfter {
		// Late data is only evaluated when it continues the newest valid reading.
		if age > domain.MaxLateAge || !e.hasValid || absDur(p.TS.Sub(e.lastValid)) > domain.LateRuleWindow {
			return nil
		}
	}
	if !e.insert(p) {
		return nil
	}
	if p.ReceivedAt.After(e.lastRecv) {
		e.lastRecv = p.ReceivedAt
	}
	var out []Decision
	for _, rs := range e.rules {
		if d, ok := e.evaluate(rs, p); ok {
			out = append(out, d)
		}
	}
	return out
}

// insert adds p to the window in timestamp order; it reports false for a duplicate timestamp.
func (e *Engine) insert(p Point) bool {
	i, found := slices.BinarySearchFunc(e.window, p, func(a, b Point) int { return a.TS.Compare(b.TS) })
	if found {
		return false
	}
	e.window = slices.Insert(e.window, i, p)
	if !e.hasValid || p.TS.After(e.lastValid) {
		e.lastValid, e.hasValid = p.TS, true
	}
	newest := e.window[len(e.window)-1].TS
	for len(e.window) > minKeptPoints && newest.Sub(e.window[0].TS) > windowKeep {
		e.window = e.window[1:]
	}
	return true
}

// evaluate computes the target severities for one rule and applies the state machine.
func (e *Engine) evaluate(rs *ruleState, p Point) (Decision, bool) {
	var up, hold domain.Severity
	canLower := false
	var rate *float64

	switch rs.rule.Kind {
	case domain.KindThreshold:
		last := e.window[max(0, len(e.window)-3):]
		up = highest(rs.rule.Tiers, func(t domain.Tier) bool { // 2 of the last 3 at or above the tier
			n := 0
			for _, q := range last {
				if q.LevelCm >= t.Value {
					n++
				}
			}
			return n >= 2
		})
		peak := last[0].LevelCm
		for _, q := range last {
			peak = max(peak, q.LevelCm)
		}
		hold = highest(rs.rule.Tiers, func(t domain.Tier) bool { return peak >= t.Value-rs.rule.HysteresisCm })
		canLower = len(last) == 3 // lowering needs 3 consecutive readings
	case domain.KindRateOfRise:
		if r, ok := riseRate(e.window); ok {
			rs.rates = append(rs.rates, r)
			if len(rs.rates) > 2 {
				rs.rates = rs.rates[len(rs.rates)-2:]
			}
			rate = &r
			if len(rs.rates) == 2 { // 2 consecutive evaluations confirm both rising and falling
				lo, hi := min(rs.rates[0], rs.rates[1]), max(rs.rates[0], rs.rates[1])
				up = highest(rs.rule.Tiers, func(t domain.Tier) bool { return lo >= t.Value })
				hold = highest(rs.rule.Tiers, func(t domain.Tier) bool { return hi >= rateHysteresis*t.Value })
				canLower = true
			}
		}
	}

	d := Decision{RuleID: rs.rule.ID, At: p.ReceivedAt, TS: p.TS, LevelCm: p.LevelCm, RateCmPerHour: rate}
	switch {
	case up > rs.cur:
		d.Previous, d.Severity = rs.cur, up
		d.Kind = Escalated
		if rs.cur == domain.SeverityNone {
			d.Kind = Opened
		}
	case rs.cur > domain.SeverityNone && canLower && hold < rs.cur:
		d.Previous, d.Severity = rs.cur, hold
		d.Kind = Deescalated
		if hold == domain.SeverityNone {
			d.Kind = Closed
		}
	case rs.cur > domain.SeverityNone && p.ReceivedAt.Sub(rs.lastNotify) >= rs.rule.Cooldown:
		d.Previous, d.Severity, d.Kind = rs.cur, rs.cur, Reminder
	default:
		return Decision{}, false
	}
	rs.cur = d.Severity
	rs.lastNotify = p.ReceivedAt // every notification restarts the cooldown
	return d, true
}

// riseRate is the OLS slope (cm/hour) over the points within 10 minutes of the newest one.
// It needs at least 4 points spanning at least 5 minutes.
func riseRate(window []Point) (float64, bool) {
	if len(window) == 0 {
		return 0, false
	}
	newest := window[len(window)-1].TS
	from, _ := slices.BinarySearchFunc(window, newest.Add(-10*time.Minute), func(p Point, t time.Time) int { return p.TS.Compare(t) })
	pts := window[from:]
	if len(pts) < 4 || pts[len(pts)-1].TS.Sub(pts[0].TS) < 5*time.Minute {
		return 0, false
	}
	x, y := make([]float64, len(pts)), make([]float64, len(pts))
	for i, p := range pts {
		x[i], y[i] = p.TS.Sub(pts[0].TS).Seconds(), p.LevelCm
	}
	slope, _, ok := stats.OLS(x, y)
	return slope * 3600, ok
}

// highest returns the severity of the highest tier satisfying ok, or SeverityNone.
func highest(tiers []domain.Tier, ok func(domain.Tier) bool) domain.Severity {
	for i := len(tiers) - 1; i >= 0; i-- {
		if ok(tiers[i]) {
			return tiers[i].Severity
		}
	}
	return domain.SeverityNone
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// State returns the open severity (0 if none) and the last notification time of a rule.
func (e *Engine) State(ruleID string) (domain.Severity, time.Time) {
	for _, rs := range e.rules {
		if rs.rule.ID == ruleID {
			return rs.cur, rs.lastNotify
		}
	}
	return domain.SeverityNone, time.Time{}
}

// Adopt makes the engine continue an alert that is open in the database but no longer visible in
// the replayed window (older than the replay horizon). It never lowers or replaces a state the
// replay derived itself, and only moves the cooldown clock forward.
func (e *Engine) Adopt(ruleID string, sev domain.Severity, lastNotify time.Time) {
	for _, rs := range e.rules {
		if rs.rule.ID != ruleID {
			continue
		}
		if rs.cur == domain.SeverityNone {
			rs.cur = sev
		}
		if lastNotify.After(rs.lastNotify) {
			rs.lastNotify = lastNotify
		}
	}
}

// Remind returns reminders that are due at now without waiting for the next reading, but only
// while the data is fresh (the newest accepted reading arrived within maxAge). Stale data never
// produces reminders: the alert stays open and the UI shows "DATA TERTUNDA" instead.
func (e *Engine) Remind(now time.Time, maxAge time.Duration) []Decision {
	if len(e.window) == 0 || now.Sub(e.lastRecv) > maxAge {
		return nil
	}
	newest := e.window[len(e.window)-1]
	var out []Decision
	for _, rs := range e.rules {
		if rs.cur == domain.SeverityNone || now.Sub(rs.lastNotify) < rs.rule.Cooldown {
			continue
		}
		rs.lastNotify = now
		out = append(out, Decision{Kind: Reminder, RuleID: rs.rule.ID, Severity: rs.cur, Previous: rs.cur, At: now, TS: newest.TS, LevelCm: newest.LevelCm})
	}
	return out
}
