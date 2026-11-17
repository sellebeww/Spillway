// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package ingest turns telemetry messages (MQTT or HTTP) into stored readings:
// parse, authenticate, deduplicate, calibrate, score, batch-insert, publish.
// Delivery is at-least-once end to end; duplicates are removed by the dedupe layers
// and finally by the unique index, so the stored result is exactly-once.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/store"
)

// ErrUnknownDevice means no device has this key.
var ErrUnknownDevice = errors.New("unknown device")

// DeviceInfo is what the pipeline needs to authenticate and process a device's messages.
type DeviceInfo struct {
	ID       uuid.UUID
	OrgID    uuid.UUID
	SiteID   uuid.UUID
	Key      string
	Secret   []byte // decrypted, held in memory only
	Interval time.Duration
	LowBattV float64
}

// Backend is the pipeline's view of persistence (the real one is StoreBackend).
type Backend interface {
	LookupDevice(ctx context.Context, key string) (DeviceInfo, error)
	Calibrations(ctx context.Context, d DeviceInfo) ([]domain.Calibration, error)
	History(ctx context.Context, d DeviceInfo, n int) ([]store.StoredReading, error)
	Insert(ctx context.Context, orgID uuid.UUID, rows []store.DeviceReading) ([]store.ReadingKey, error)
	Touch(ctx context.Context, d DeviceInfo, seen time.Time) error
}

// StoreBackend implements Backend on the database.
type StoreBackend struct {
	Store  *store.Store
	Cipher *auth.Cipher
}

// LookupDevice finds and decrypts a device by key.
func (b StoreBackend) LookupDevice(ctx context.Context, key string) (DeviceInfo, error) {
	a, err := b.Store.DeviceAuthByKey(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return DeviceInfo{}, ErrUnknownDevice
	}
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("lookup device: %w", err)
	}
	secret, err := a.Secret(b.Cipher)
	if err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{ID: a.ID, OrgID: a.OrgID, SiteID: a.SiteID, Key: key, Secret: secret,
		Interval: time.Duration(a.ExpectedIntervalS) * time.Second, LowBattV: a.LowBattV}, nil
}

// Calibrations lists all calibration versions of a device.
func (b StoreBackend) Calibrations(ctx context.Context, d DeviceInfo) ([]domain.Calibration, error) {
	return b.Store.ForOrg(d.OrgID).ListCalibrations(ctx, d.ID)
}

// History returns the newest stored readings of a device, oldest first.
func (b StoreBackend) History(ctx context.Context, d DeviceInfo, n int) ([]store.StoredReading, error) {
	return b.Store.ForOrg(d.OrgID).RecentReadings(ctx, d.ID, n)
}

// Insert stores a batch for one organization and returns the newly inserted keys.
func (b StoreBackend) Insert(ctx context.Context, orgID uuid.UUID, rows []store.DeviceReading) ([]store.ReadingKey, error) {
	return b.Store.ForOrg(orgID).InsertReadingsBatch(ctx, rows)
}

// Touch records that a device was seen.
func (b StoreBackend) Touch(ctx context.Context, d DeviceInfo, seen time.Time) error {
	return b.Store.ForOrg(d.OrgID).UpdateDeviceStatus(ctx, d.ID, seen, domain.StatusOnline)
}

// Dedupe is the first dedupe layer (Redis). Seen must only be true for keys whose reading
// was committed (Mark is called after the commit), so a hit never hides unstored data.
type Dedupe interface {
	Seen(ctx context.Context, key string) (bool, error)
	Mark(ctx context.Context, keys []string) error
}

// ReadingEvent describes one newly stored reading.
type ReadingEvent struct {
	OrgID      uuid.UUID
	SiteID     uuid.UUID
	DeviceID   uuid.UUID
	DeviceKey  string
	TS         time.Time
	ReceivedAt time.Time
	RawMM      int
	LevelCm    float64
	Score      float64
	Flags      []domain.QualityFlag
	Late       bool
}

// ReadingSink receives every newly stored reading, in commit order per device, from the
// single writer goroutine: implementations must not block for long. Phase 5 plugs the rule
// engine in here.
type ReadingSink interface {
	Handle(ctx context.Context, ev ReadingEvent)
}

// Publisher fans new readings out (last value cache and pub/sub). It is best effort.
type Publisher interface {
	Publish(ctx context.Context, evs []ReadingEvent) error
}

// Outcome kinds.
type OutcomeKind int

// Outcomes of a message.
const (
	Stored    OutcomeKind = iota // committed now
	Duplicate                    // already stored earlier
	Rejected                     // permanently refused; retrying cannot help
	Failed                       // transient failure; the sender should retry (not acknowledged)
)

// Outcome is the final result of one message.
type Outcome struct {
	Kind   OutcomeKind
	Reason string
	Err    error
}

// Rejection reasons (also the metric labels).
const (
	ReasonSchema        = "schema"
	ReasonSig           = "sig"
	ReasonUnknownDevice = "unknown_device"
	ReasonTooOld        = "too_old"
	ReasonRateLimit     = "rate_limit"
	ReasonNoCalibration = "no_calibration"
	ReasonTopicMismatch = "topic_mismatch"
	ReasonDBInvalid     = "db_invalid"
)
