// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/domain"
)

// Sentinel errors. Callers match with errors.Is; messages never contain row values.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid input")
	// ErrUnscoped means a query tried to run without an organization filter. It is a programming error.
	ErrUnscoped = errors.New("query is not scoped to an organization")
)

// Resolution selects raw readings or one of the continuous aggregates.
type Resolution string

// Resolutions.
const (
	ResRaw Resolution = "raw"
	Res1m  Resolution = "1m"
	Res1h  Resolution = "1h"
	Res1d  Resolution = "1d"
)

// ChooseResolution picks the chart resolution for a time span (docs/SPEC.md 5.6): at most a few
// thousand points per series.
func ChooseResolution(span time.Duration) Resolution {
	switch {
	case span <= 6*time.Hour:
		return ResRaw
	case span <= 48*time.Hour:
		return Res1m
	case span <= 30*24*time.Hour:
		return Res1h
	default:
		return Res1d
	}
}

// Org is a tenant.
type Org struct {
	ID        uuid.UUID `db:"id"`
	Slug      string    `db:"slug"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
}

// SiteInput holds the editable fields of a site.
type SiteInput struct {
	Slug     string
	Name     string
	Lat, Lon float64
	TZ       string // IANA zone, default Asia/Jakarta
	IsPublic bool
}

// Site is a monitored location.
type Site struct {
	ID        uuid.UUID `db:"id" json:"id"`
	OrgID     uuid.UUID `db:"org_id" json:"-"`
	Slug      string    `db:"slug" json:"slug"`
	Name      string    `db:"name" json:"name"`
	Lat       float64   `db:"lat" json:"lat"`
	Lon       float64   `db:"lon" json:"lon"`
	TZ        string    `db:"tz" json:"tz"`
	IsPublic  bool      `db:"is_public" json:"is_public"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// DeviceInput holds what an operator provides when registering a device.
type DeviceInput struct {
	SiteID            uuid.UUID
	Key               string
	Name              string
	ExpectedIntervalS int     // default 60
	LowBattV          float64 // default 3.3
}

// Device never exposes its secret.
type Device struct {
	ID                uuid.UUID           `db:"id" json:"id"`
	OrgID             uuid.UUID           `db:"org_id" json:"-"`
	SiteID            uuid.UUID           `db:"site_id" json:"site_id"`
	Key               string              `db:"key" json:"device_key"`
	Name              string              `db:"name" json:"name"`
	ExpectedIntervalS int                 `db:"expected_interval_s" json:"expected_interval_s"`
	LowBattV          float64             `db:"low_batt_v" json:"low_batt_v"`
	LastSeenAt        *time.Time          `db:"last_seen_at" json:"last_seen_at"`
	LastStatus        domain.DeviceStatus `db:"last_status" json:"last_status"`
	CreatedAt         time.Time           `db:"created_at" json:"created_at"`
}

// ReadingRow is one reading ready for insertion.
type ReadingRow struct {
	TS         time.Time
	ReceivedAt time.Time
	SID        string
	FC         uint32
	RawMM      int
	LevelCm    float64
	Flags      []domain.QualityFlag
	Score      float64
	Late       bool
	BattV      *float64
}

// StoredReading is a raw reading as read back.
type StoredReading struct {
	TS         time.Time `db:"ts"`
	ReceivedAt time.Time `db:"received_at"`
	SID        string    `db:"sid"`
	FC         int64     `db:"fc"`
	RawMM      int       `db:"raw_value"`
	LevelCm    float64   `db:"level_cm"`
	Flags      []string  `db:"quality_flags"`
	Score      float64   `db:"quality_score"`
	Late       bool      `db:"late"`
	BattV      *float64  `db:"batt_v"`
}

// Aggregate is one bucket of a continuous aggregate.
type Aggregate struct {
	Bucket time.Time `db:"bucket"`
	Min    float64   `db:"min_level"`
	Avg    float64   `db:"avg_level"`
	Max    float64   `db:"max_level"`
	N      int64     `db:"n"`
}

// RuleInput holds the editable fields of an alert rule.
type RuleInput struct {
	SiteID       uuid.UUID
	Kind         domain.RuleKind
	Tiers        []domain.Tier
	HysteresisCm float64       // THRESHOLD only; 0 means the default
	Cooldown     time.Duration // 0 means the default (30 min)
	MinQuality   float64       // 0 means the default (0.5)
	Enabled      bool
}

// AlertRule is a stored rule.
type AlertRule struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	SiteID       uuid.UUID
	Kind         domain.RuleKind
	Tiers        []domain.Tier
	HysteresisCm float64
	Cooldown     time.Duration
	MinQuality   float64
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Domain converts the stored rule to the pure engine's rule type.
func (r AlertRule) Domain() domain.Rule {
	return domain.Rule{ID: r.ID.String(), Kind: r.Kind, Tiers: r.Tiers, HysteresisCm: r.HysteresisCm, Cooldown: r.Cooldown}
}

// AlertEvent is the lifecycle of one alert; at most one is open per rule.
type AlertEvent struct {
	ID             uuid.UUID       `db:"id" json:"id"`
	OrgID          uuid.UUID       `db:"org_id" json:"-"`
	RuleID         uuid.UUID       `db:"rule_id" json:"rule_id"`
	State          string          `db:"state" json:"state"`
	Severity       domain.Severity `db:"severity" json:"severity"`
	OpenedAt       time.Time       `db:"opened_at" json:"opened_at"`
	ClosedAt       *time.Time      `db:"closed_at" json:"closed_at"`
	PeakLevel      *float64        `db:"peak_level" json:"peak_level"`
	PeakRate       *float64        `db:"peak_rate" json:"peak_rate"`
	LastNotifiedAt *time.Time      `db:"last_notified_at" json:"last_notified_at"`
	DeviceID       *uuid.UUID      `db:"device_id" json:"device_id"`
	ReminderCount  int             `db:"reminder_count" json:"reminder_count"`
	NotifySeq      int             `db:"notify_seq"`
}

// Channel is a notification destination (Telegram chat).
type Channel struct {
	ID        uuid.UUID `db:"id"`
	OrgID     uuid.UUID `db:"org_id" json:"-"`
	Kind      string    `db:"kind"`
	ChatID    string    `db:"chat_id"`
	IsActive  bool      `db:"is_active"`
	CreatedAt time.Time `db:"created_at"`
}

// NotificationInput enqueues one outbox row. Seq is 0 except for repeated reminders.
type NotificationInput struct {
	EventID   uuid.UUID
	Kind      string
	Seq       int
	ChannelID uuid.UUID
	Payload   []byte // JSON, default {}
}

// Notification is an outbox row.
type Notification struct {
	ID            int64     `db:"id"`
	OrgID         uuid.UUID `db:"org_id" json:"-"`
	AlertEventID  uuid.UUID `db:"alert_event_id"`
	ChannelID     uuid.UUID `db:"channel_id"`
	Kind          string    `db:"kind"`
	Seq           int       `db:"seq"`
	Status        string    `db:"status"`
	Attempts      int       `db:"attempts"`
	NextAttemptAt time.Time `db:"next_attempt_at"`
	LastError     *string   `db:"last_error"`
	Payload       []byte    `db:"payload"`
}

// User is an OIDC identity inside one organization.
type User struct {
	ID          uuid.UUID `db:"id"`
	OrgID       uuid.UUID `db:"org_id" json:"-"`
	OIDCSub     string    `db:"oidc_sub"`
	Email       *string   `db:"email"`
	DisplayName string    `db:"display_name"`
	CreatedAt   time.Time `db:"created_at"`
}

// Membership grants a role: admin covers the organization, operator one site.
type Membership struct {
	ID     uuid.UUID  `db:"id"`
	OrgID  uuid.UUID  `db:"org_id" json:"-"`
	UserID uuid.UUID  `db:"user_id"`
	Role   string     `db:"role"`
	SiteID *uuid.UUID `db:"site_id"`
}

// AuditEntry is one configuration change.
type AuditEntry struct {
	ID          int64           `db:"id" json:"id"`
	At          time.Time       `db:"at" json:"at"`
	ActorUserID *uuid.UUID      `db:"actor_user_id" json:"-"`
	ActorLabel  string          `db:"actor_label" json:"actor"`
	Action      string          `db:"action" json:"action"`
	EntityType  string          `db:"entity_type" json:"entity_type"`
	EntityID    string          `db:"entity_id" json:"entity_id"`
	Before      json.RawMessage `db:"before" json:"before"`
	After       json.RawMessage `db:"after" json:"after"`
}

// DeviceReading is a reading together with the device it belongs to (multi-device batches).
type DeviceReading struct {
	DeviceID uuid.UUID
	ReadingRow
}

// ReadingKey identifies one stored reading (the unique index of the readings table).
type ReadingKey struct {
	DeviceID uuid.UUID
	TS       time.Time
	SID      string
	FC       uint32
}
