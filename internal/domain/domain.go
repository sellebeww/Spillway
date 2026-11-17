// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package domain holds the pure core types and constants of Spillway.
// It imports nothing outside the standard library and never reads the system clock.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// Timing constants from docs/SPEC.md section 5.1.
const (
	LateAfter      = 2 * time.Minute  // server time - ts above this marks a reading late
	MaxLateAge     = 24 * time.Hour   // older readings are rejected at ingest
	ClockSkewAfter = 10 * time.Minute // |ts - server time| above this may be clock skew
	LateRuleWindow = 5 * time.Minute  // late readings only drive rules this close to the newest valid one
)

// Defaults from docs/SPEC.md sections 5.2 and 5.4.
const (
	DefaultMinValidMM   = 250
	DefaultMaxValidMM   = 4500
	DefaultMinQuality   = 0.5
	DefaultHysteresisCm = 5.0
	DefaultCooldown     = 30 * time.Minute
)

// Clock is injected wherever "now" is needed, so core logic stays deterministic.
type Clock interface {
	Now() time.Time
}

// Reading is one sensor measurement as received by the server.
type Reading struct {
	DeviceKey  string
	SID        string // random per-boot session id
	FC         uint32 // frame counter, increasing within a session
	TS         time.Time
	ReceivedAt time.Time
	RawMM      int      // distance from sensor to water surface, mm
	BattV      *float64 // optional battery voltage
}

// Calibration is one immutable version of a device's datum calibration.
type Calibration struct {
	Version    int
	OffsetCm   float64 // elevation of the sensor head above datum zero, cm
	MinValidMM int
	MaxValidMM int
	ValidFrom  time.Time
	Note       string
	CreatedBy  string
}

// QualityFlag labels a reading with a data quality finding.
type QualityFlag string

// Quality flags from docs/SPEC.md section 5.3.
const (
	FlagOK         QualityFlag = "OK"
	FlagOutOfRange QualityFlag = "OUT_OF_RANGE"
	FlagSpike      QualityFlag = "SPIKE"
	FlagFlatline   QualityFlag = "FLATLINE"
	FlagClockSkew  QualityFlag = "CLOCK_SKEW"
	FlagLowBattery QualityFlag = "LOW_BATTERY"
	FlagLate       QualityFlag = "LATE"
)

// Severity is an alert level.
type Severity int

// Severities; the numeric values are stable and stored in the database.
const (
	SeverityNone    Severity = 0
	SeverityWaspada Severity = 1
	SeveritySiaga   Severity = 2
	SeverityAwas    Severity = 3
)

// String returns the Indonesian level name.
func (s Severity) String() string {
	switch s {
	case SeverityWaspada:
		return "WASPADA"
	case SeveritySiaga:
		return "SIAGA"
	case SeverityAwas:
		return "AWAS"
	default:
		return "NONE"
	}
}

// Valid reports whether s is one of the three alert levels.
func (s Severity) Valid() bool { return s >= SeverityWaspada && s <= SeverityAwas }

// RuleKind is the type of an alert rule. Only these two exist (scope lock).
type RuleKind string

// Rule kinds.
const (
	KindThreshold  RuleKind = "THRESHOLD"
	KindRateOfRise RuleKind = "RATE_OF_RISE"
)

// Tier maps a trigger value to a severity: level_cm for THRESHOLD, cm/hour for RATE_OF_RISE.
type Tier struct {
	Value    float64
	Severity Severity
}

// Rule is an alert rule definition.
type Rule struct {
	ID           string
	Kind         RuleKind
	Tiers        []Tier
	HysteresisCm float64 // THRESHOLD only; RATE_OF_RISE uses a fixed 20 percent
	Cooldown     time.Duration
}

// NewThresholdRule returns a THRESHOLD rule with default hysteresis and cooldown.
func NewThresholdRule(id string, tiers []Tier) Rule {
	return Rule{ID: id, Kind: KindThreshold, Tiers: tiers, HysteresisCm: DefaultHysteresisCm, Cooldown: DefaultCooldown}
}

// NewRateOfRiseRule returns a RATE_OF_RISE rule with the default cooldown.
func NewRateOfRiseRule(id string, tiers []Tier) Rule {
	return Rule{ID: id, Kind: KindRateOfRise, Tiers: tiers, Cooldown: DefaultCooldown}
}

// Validate checks that the rule is well formed: known kind, at least one tier,
// strictly ascending values and severities, non-negative parameters.
func (r Rule) Validate() error {
	if r.ID == "" {
		return errors.New("rule id is empty")
	}
	if r.Kind != KindThreshold && r.Kind != KindRateOfRise {
		return fmt.Errorf("rule %s: unknown kind %q", r.ID, r.Kind)
	}
	if len(r.Tiers) == 0 {
		return fmt.Errorf("rule %s: no tiers", r.ID)
	}
	if r.HysteresisCm < 0 || r.Cooldown < 0 {
		return fmt.Errorf("rule %s: negative hysteresis or cooldown", r.ID)
	}
	for i, t := range r.Tiers {
		if !t.Severity.Valid() {
			return fmt.Errorf("rule %s: tier %d has invalid severity %d", r.ID, i, int(t.Severity))
		}
		if i > 0 && (t.Value <= r.Tiers[i-1].Value || t.Severity <= r.Tiers[i-1].Severity) {
			return fmt.Errorf("rule %s: tiers must ascend strictly in value and severity", r.ID)
		}
	}
	return nil
}

// AlertEvent is the persisted lifecycle of one alert; at most one is open per rule.
type AlertEvent struct {
	ID             string
	RuleID         string
	Severity       Severity
	OpenedAt       time.Time
	ClosedAt       *time.Time
	LastNotifiedAt time.Time
}

// DeviceStatus is the connectivity state of a device.
type DeviceStatus string

// Device statuses.
const (
	StatusOnline  DeviceStatus = "ONLINE"
	StatusDelayed DeviceStatus = "DELAYED"
	StatusOffline DeviceStatus = "OFFLINE"
)
