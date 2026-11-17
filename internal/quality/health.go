// Copyright (c) 2026 Vassel Goleyu. MIT License.

package quality

import (
	"time"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/stats"
)

// minStale is the floor of the "DATA TERTUNDA" threshold (docs/SPEC.md section 1).
const minStale = 5 * time.Minute

// HealthScore is received_ratio x mean(point score) over a window.
// scores holds the point scores of readings accepted in the window.
func HealthScore(scores []float64, interval, window time.Duration) float64 {
	if len(scores) == 0 || interval <= 0 || window <= 0 {
		return 0
	}
	expected := max(1, float64(window)/float64(interval))
	ratio := min(1, float64(len(scores))/expected)
	var sum float64
	for _, s := range scores {
		sum += s
	}
	return ratio * sum / float64(len(scores))
}

// BattSample is one battery voltage observation.
type BattSample struct {
	TS time.Time
	V  float64
}

// BatterySlopePerDay is the OLS slope of voltage in volts per day over the
// 24 hours up to now. ok is false when fewer than two samples fall in the window.
func BatterySlopePerDay(samples []BattSample, now time.Time) (slope float64, ok bool) {
	from := now.Add(-24 * time.Hour)
	var x, y []float64
	for _, s := range samples {
		if s.TS.Before(from) || s.TS.After(now) {
			continue
		}
		x = append(x, s.TS.Sub(from).Hours()/24)
		y = append(y, s.V)
	}
	slope, _, ok = stats.OLS(x, y)
	return slope, ok
}

// Status derives the connectivity state from the age of the newest reading.
// ONLINE up to 2 x interval, DELAYED up to max(10 x interval, 15 min), then OFFLINE.
func Status(now, lastSeen time.Time, interval time.Duration) domain.DeviceStatus {
	if lastSeen.IsZero() {
		return domain.StatusOffline
	}
	age := now.Sub(lastSeen)
	switch {
	case age > max(10*interval, 15*time.Minute):
		return domain.StatusOffline
	case age > 2*interval:
		return domain.StatusDelayed
	default:
		return domain.StatusOnline
	}
}

// StaleAfter is the age beyond which public UI and messages must show "DATA TERTUNDA".
func StaleAfter(interval time.Duration) time.Duration { return max(2*interval, minStale) }

// IsStaleReading judges a reading by the older of its measurement time and its arrival time, so a
// buffered reading that has just arrived is still shown as old data (fail-visible).
func IsStaleReading(now, ts, receivedAt time.Time, interval time.Duration) bool {
	if ts.Before(receivedAt) {
		receivedAt = ts
	}
	return IsStale(now, receivedAt, interval)
}

// IsStale reports whether the newest reading is old enough to be shown as delayed.
func IsStale(now, lastSeen time.Time, interval time.Duration) bool {
	return lastSeen.IsZero() || now.Sub(lastSeen) > StaleAfter(interval)
}
