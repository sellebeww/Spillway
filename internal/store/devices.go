// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/calibration"
	"github.com/sellebeww/spillway/internal/domain"
)

var deviceKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const deviceCols = `id, org_id, site_id, device_key AS key, name, expected_interval_s, low_batt_v, last_seen_at, last_status, created_at`

// deviceAAD binds an encrypted secret to its device row.
func deviceAAD(id uuid.UUID) []byte { return []byte("spillway/device-secret/v1/" + id.String()) }

// RegisterDevice creates a device with a fresh random secret. The plaintext secret (hex) is
// returned exactly once and is never stored; only its AES-GCM encryption is.
func (o *OrgStore) RegisterDevice(ctx context.Context, c *auth.Cipher, in DeviceInput) (Device, string, error) {
	if in.ExpectedIntervalS == 0 {
		in.ExpectedIntervalS = 60
	}
	if in.LowBattV == 0 {
		in.LowBattV = 3.3
	}
	if !deviceKeyRe.MatchString(in.Key) || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 ||
		in.ExpectedIntervalS < 1 || in.ExpectedIntervalS > 86400 || in.LowBattV <= 0 || in.LowBattV > 20 {
		return Device{}, "", fmt.Errorf("%w: device fields", ErrInvalid)
	}
	id := uuid.New()
	raw, secretHex, err := auth.GenerateDeviceSecret()
	if err != nil {
		return Device{}, "", err
	}
	enc, err := c.Encrypt(raw, deviceAAD(id))
	if err != nil {
		return Device{}, "", fmt.Errorf("encrypt device secret: %w", err)
	}
	var dev Device
	err = o.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		dev, err = fetchOne[Device](ctx, o, tx, `INSERT INTO devices (org_id, id, site_id, device_key, name, secret_enc, expected_interval_s, low_batt_v)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+deviceCols,
			id, in.SiteID, in.Key, in.Name, enc, in.ExpectedIntervalS, in.LowBattV)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "device.register", "device", id, nil, dev) // Device has no secret field
	})
	if err != nil {
		return Device{}, "", err
	}
	return dev, secretHex, nil
}

// GetDevice returns one device of this organization.
func (o *OrgStore) GetDevice(ctx context.Context, id uuid.UUID) (Device, error) {
	return fetchOne[Device](ctx, o, o.s.pool, `SELECT `+deviceCols+` FROM devices WHERE org_id = $1 AND id = $2`, id)
}

// ListDevices returns the devices of this organization, optionally of one site.
func (o *OrgStore) ListDevices(ctx context.Context, siteID *uuid.UUID) ([]Device, error) {
	return fetchAll[Device](ctx, o, o.s.pool, `SELECT `+deviceCols+` FROM devices
		WHERE org_id = $1 AND ($2::uuid IS NULL OR site_id = $2) ORDER BY name, id`, siteID)
}

// UpdateDeviceStatus records the newest reading time and connectivity status.
// Runtime state, not configuration, so it is not audited.
func (o *OrgStore) UpdateDeviceStatus(ctx context.Context, id uuid.UUID, lastSeen time.Time, status domain.DeviceStatus) error {
	tag, err := o.exec(ctx, o.s.pool, `UPDATE devices SET last_seen_at = GREATEST(last_seen_at, $3), last_status = $4
		WHERE org_id = $1 AND id = $2`, id, lastSeen, string(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeviceAuth is what ingest needs to authenticate a message.
type DeviceAuth struct {
	ID                uuid.UUID `db:"id"`
	OrgID             uuid.UUID `db:"org_id"`
	SiteID            uuid.UUID `db:"site_id"`
	SecretEnc         []byte    `db:"secret_enc"`
	ExpectedIntervalS int       `db:"expected_interval_s"`
	LowBattV          float64   `db:"low_batt_v"`
}

// Secret decrypts the device secret. Only the ingest path should call this.
func (a DeviceAuth) Secret(c *auth.Cipher) ([]byte, error) {
	s, err := c.Decrypt(a.SecretEnc, deviceAAD(a.ID))
	if err != nil {
		return nil, fmt.Errorf("device secret: %w", err)
	}
	return s, nil
}

// DeviceAuthByKey finds a device by its key before the organization is known (the key is
// globally unique). It is the only tenant-table read that is not organization-scoped.
func (s *Store) DeviceAuthByKey(ctx context.Context, key string) (DeviceAuth, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: authenticates a device by key before its organization is known
		SELECT id, org_id, site_id, secret_enc, expected_interval_s, low_batt_v FROM devices WHERE device_key = $1`, key)
	if err != nil {
		return DeviceAuth{}, mapErr(err)
	}
	a, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[DeviceAuth])
	return a, mapErr(err)
}

type calRow struct {
	Version    int       `db:"version"`
	OffsetCm   float64   `db:"offset_cm"`
	MinValidMM int       `db:"min_valid_mm"`
	MaxValidMM int       `db:"max_valid_mm"`
	ValidFrom  time.Time `db:"valid_from"`
	Note       string    `db:"note"`
	CreatedBy  *string   `db:"created_by"`
}

func (r calRow) domain() domain.Calibration {
	c := domain.Calibration{Version: r.Version, OffsetCm: r.OffsetCm, MinValidMM: r.MinValidMM, MaxValidMM: r.MaxValidMM, ValidFrom: r.ValidFrom, Note: r.Note}
	if r.CreatedBy != nil {
		c.CreatedBy = *r.CreatedBy
	}
	return c
}

const calCols = `version, offset_cm, min_valid_mm, max_valid_mm, valid_from, note, created_by::text AS created_by`

// ListCalibrations returns all versions of a device, oldest version first.
func (o *OrgStore) ListCalibrations(ctx context.Context, deviceID uuid.UUID) ([]domain.Calibration, error) {
	rows, err := fetchAll[calRow](ctx, o, o.s.pool, `SELECT `+calCols+` FROM calibrations
		WHERE org_id = $1 AND device_id = $2 ORDER BY version`, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Calibration, len(rows))
	for i, r := range rows {
		out[i] = r.domain()
	}
	return out, nil
}

// AddCalibration appends a new calibration version (append-only; a database trigger refuses
// updates and deletes). Version 0 means "next". The device row is locked so concurrent
// additions get consecutive versions. Backdating needs confirmBackdate (docs/SPEC.md 5.2).
func (o *OrgStore) AddCalibration(ctx context.Context, deviceID uuid.UUID, c domain.Calibration, confirmBackdate bool) (domain.Calibration, error) {
	if c.MinValidMM == 0 && c.MaxValidMM == 0 {
		c.MinValidMM, c.MaxValidMM = domain.DefaultMinValidMM, domain.DefaultMaxValidMM
	}
	var added domain.Calibration
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := fetchOne[struct {
			ID uuid.UUID `db:"id"`
		}](ctx, o, tx, `SELECT id FROM devices WHERE org_id = $1 AND id = $2 FOR UPDATE`, deviceID); err != nil {
			return err
		}
		rows, err := fetchAll[calRow](ctx, o, tx, `SELECT `+calCols+` FROM calibrations WHERE org_id = $1 AND device_id = $2`, deviceID)
		if err != nil {
			return err
		}
		existing := make([]domain.Calibration, len(rows))
		maxV := 0
		for i, r := range rows {
			existing[i] = r.domain()
			maxV = max(maxV, r.Version)
		}
		if c.Version == 0 {
			c.Version = maxV + 1
		}
		if err := calibration.ValidateAppend(existing, c, confirmBackdate); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		actor := actorFrom(ctx)
		got, err := fetchOne[calRow](ctx, o, tx, `INSERT INTO calibrations (org_id, device_id, version, offset_cm, min_valid_mm, max_valid_mm, valid_from, note, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+calCols,
			deviceID, c.Version, c.OffsetCm, c.MinValidMM, c.MaxValidMM, c.ValidFrom, c.Note, actor.UserID)
		if err != nil {
			return err
		}
		added = got.domain()
		return o.audit(ctx, tx, "calibration.add", "device", deviceID, nil,
			map[string]any{"calibration": added, "backdate_confirmed": confirmBackdate})
	})
	return added, err
}

// ResolveCalibration selects the version in force at reading time ts, in SQL
// (latest valid_from <= ts, higher version on ties); it matches calibration.Resolve.
func (o *OrgStore) ResolveCalibration(ctx context.Context, deviceID uuid.UUID, ts time.Time) (domain.Calibration, error) {
	r, err := fetchOne[calRow](ctx, o, o.s.pool, `SELECT `+calCols+` FROM calibrations
		WHERE org_id = $1 AND device_id = $2 AND valid_from <= $3 ORDER BY valid_from DESC, version DESC LIMIT 1`, deviceID, ts)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.Calibration{}, calibration.ErrNoCalibration
		}
		return domain.Calibration{}, err
	}
	return r.domain(), nil
}
