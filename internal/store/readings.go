// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxBatch bounds one InsertReadings statement.
const maxBatch = 5000

// maxRange bounds one range query.
const maxRange = 10000

// InsertReadings stores a batch idempotently and returns how many rows were actually
// inserted (duplicates of (device, ts, sid, fc) are skipped, not errors). The device must
// belong to this organization; the check is part of the same statement.
func (o *OrgStore) InsertReadings(ctx context.Context, deviceID uuid.UUID, rows []ReadingRow) (int64, error) {
	var total int64
	for start := 0; start < len(rows); start += maxBatch {
		n, err := o.insertChunk(ctx, deviceID, rows[start:min(start+maxBatch, len(rows))])
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (o *OrgStore) insertChunk(ctx context.Context, deviceID uuid.UUID, rows []ReadingRow) (int64, error) {
	n := len(rows)
	ts, recv := make([]time.Time, n), make([]time.Time, n)
	sid, flags := make([]string, n), make([]string, n)
	fc, raw := make([]int64, n), make([]int32, n)
	level, score := make([]float64, n), make([]float64, n)
	late := make([]bool, n)
	batt := make([]*float64, n)
	for i, r := range rows {
		fs := make([]string, len(r.Flags))
		for j, f := range r.Flags {
			fs[j] = string(f)
		}
		ts[i], recv[i], sid[i], fc[i] = r.TS, r.ReceivedAt, r.SID, int64(r.FC)
		raw[i], level[i], score[i], late[i], batt[i] = int32(r.RawMM), r.LevelCm, r.Score, r.Late, r.BattV //nolint:gosec // raw_mm is validated to 0..100000
		flags[i] = strings.Join(fs, ",")
	}
	tag, err := o.exec(ctx, o.s.pool, `INSERT INTO readings (device_id, ts, sid, fc, received_at, raw_value, level_cm, quality_flags, quality_score, late, batt_v)
		SELECT $2, t.ts, t.sid, t.fc, t.received_at, t.raw, t.level, string_to_array(t.flags, ','), t.score::real, t.late, t.batt::real
		FROM unnest($3::timestamptz[], $4::text[], $5::bigint[], $6::timestamptz[], $7::int[], $8::float8[], $9::text[], $10::float8[], $11::bool[], $12::float8[])
		     AS t(ts, sid, fc, received_at, raw, level, flags, score, late, batt)
		WHERE EXISTS (SELECT 1 FROM devices WHERE id = $2 AND org_id = $1)
		ON CONFLICT (device_id, ts, sid, fc) DO NOTHING`,
		deviceID, ts, sid, fc, recv, raw, level, flags, score, late, batt)
	if err != nil {
		return 0, fmt.Errorf("insert readings: %w", err)
	}
	if tag.RowsAffected() == 0 && n > 0 {
		// Either every row was a duplicate or the device is not ours; tell them apart.
		if _, err := o.GetDevice(ctx, deviceID); err != nil {
			return 0, err
		}
	}
	return tag.RowsAffected(), nil
}

// ReadingsRange returns raw readings in [from, to), oldest first, at most limit (cap 10000).
func (o *OrgStore) ReadingsRange(ctx context.Context, deviceID uuid.UUID, from, to time.Time, limit int) ([]StoredReading, error) {
	return fetchAll[StoredReading](ctx, o, o.s.pool, `SELECT r.ts, r.received_at, r.sid, r.fc, r.raw_value, r.level_cm,
			r.quality_flags, r.quality_score, r.late, r.batt_v
		FROM readings r JOIN devices d ON d.id = r.device_id AND d.org_id = $1
		WHERE r.device_id = $2 AND r.ts >= $3 AND r.ts < $4 ORDER BY r.ts, r.sid, r.fc LIMIT $5`,
		deviceID, from, to, clampLimit(limit, 1000, maxRange))
}

// LatestReading returns the newest reading by timestamp.
func (o *OrgStore) LatestReading(ctx context.Context, deviceID uuid.UUID) (StoredReading, error) {
	return fetchOne[StoredReading](ctx, o, o.s.pool, `SELECT r.ts, r.received_at, r.sid, r.fc, r.raw_value, r.level_cm,
			r.quality_flags, r.quality_score, r.late, r.batt_v
		FROM readings r JOIN devices d ON d.id = r.device_id AND d.org_id = $1
		WHERE r.device_id = $2 ORDER BY r.ts DESC LIMIT 1`, deviceID)
}

// Fixed per-resolution queries: the view name never comes from input.
var aggregateSQL = map[Resolution]string{
	Res1m: aggSelect("readings_1m"),
	Res1h: aggSelect("readings_1h"),
	Res1d: aggSelect("readings_1d"),
}

func aggSelect(view string) string {
	return `SELECT a.bucket, a.min_level, a.avg_level, a.max_level, a.n
		FROM ` + view + ` a JOIN devices d ON d.id = a.device_id AND d.org_id = $1
		WHERE a.device_id = $2 AND a.bucket >= $3 AND a.bucket < $4 ORDER BY a.bucket LIMIT $5`
}

// Aggregates reads a continuous aggregate (1m, 1h or 1d) for buckets in [from, to).
func (o *OrgStore) Aggregates(ctx context.Context, deviceID uuid.UUID, res Resolution, from, to time.Time, limit int) ([]Aggregate, error) {
	q, ok := aggregateSQL[res]
	if !ok {
		return nil, fmt.Errorf("%w: resolution %q has no aggregate", ErrInvalid, res)
	}
	return fetchAll[Aggregate](ctx, o, o.s.pool, q, deviceID, from, to, clampLimit(limit, 1000, maxRange))
}

// ApplyRetention (re)configures the raw-readings retention policy. Aggregates are kept.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, days int) error {
	if days < 7 || days > 3650 {
		return fmt.Errorf("%w: retention days must be 7..3650", ErrInvalid)
	}
	if _, err := pool.Exec(ctx, `-- unscoped: maintenance of the retention policy
		SELECT remove_retention_policy('readings', if_exists => true)`); err != nil {
		return fmt.Errorf("remove retention policy: %w", err)
	}
	if _, err := pool.Exec(ctx, `-- unscoped: maintenance of the retention policy
		SELECT add_retention_policy('readings', make_interval(days => $1))`, days); err != nil {
		return fmt.Errorf("add retention policy: %w", err)
	}
	return nil
}

type insertedRow struct {
	DeviceID uuid.UUID `db:"device_id"`
	TS       time.Time `db:"ts"`
	SID      string    `db:"sid"`
	FC       int64     `db:"fc"`
}

// InsertReadingsBatch stores readings of several devices of this organization in one
// statement and returns exactly the keys that were newly inserted (duplicates are skipped).
// Rows of devices outside this organization are silently not inserted, so callers must
// resolve devices through DeviceAuthByKey first.
func (o *OrgStore) InsertReadingsBatch(ctx context.Context, rows []DeviceReading) ([]ReadingKey, error) {
	var out []ReadingKey
	for start := 0; start < len(rows); start += maxBatch {
		keys, err := o.insertBatchChunk(ctx, rows[start:min(start+maxBatch, len(rows))])
		if err != nil {
			return out, err
		}
		out = append(out, keys...)
	}
	return out, nil
}

func (o *OrgStore) insertBatchChunk(ctx context.Context, rows []DeviceReading) ([]ReadingKey, error) {
	n := len(rows)
	dev := make([]uuid.UUID, n)
	ts, recv := make([]time.Time, n), make([]time.Time, n)
	sid, flags := make([]string, n), make([]string, n)
	fc, raw := make([]int64, n), make([]int32, n)
	level, score := make([]float64, n), make([]float64, n)
	late := make([]bool, n)
	batt := make([]*float64, n)
	for i, r := range rows {
		fs := make([]string, len(r.Flags))
		for j, f := range r.Flags {
			fs[j] = string(f)
		}
		dev[i], ts[i], recv[i], sid[i], fc[i] = r.DeviceID, r.TS, r.ReceivedAt, r.SID, int64(r.FC)
		raw[i], level[i], score[i], late[i], batt[i] = int32(r.RawMM), r.LevelCm, r.Score, r.Late, r.BattV //nolint:gosec // raw_mm is validated to 0..100000
		flags[i] = strings.Join(fs, ",")
	}
	got, err := fetchAll[insertedRow](ctx, o, o.s.pool, `INSERT INTO readings (device_id, ts, sid, fc, received_at, raw_value, level_cm, quality_flags, quality_score, late, batt_v)
		SELECT t.device_id, t.ts, t.sid, t.fc, t.received_at, t.raw, t.level, string_to_array(t.flags, ','), t.score::real, t.late, t.batt::real
		FROM unnest($2::uuid[], $3::timestamptz[], $4::text[], $5::bigint[], $6::timestamptz[], $7::int[], $8::float8[], $9::text[], $10::float8[], $11::bool[], $12::float8[])
		     AS t(device_id, ts, sid, fc, received_at, raw, level, flags, score, late, batt)
		WHERE EXISTS (SELECT 1 FROM devices d WHERE d.id = t.device_id AND d.org_id = $1)
		ON CONFLICT (device_id, ts, sid, fc) DO NOTHING
		RETURNING device_id, ts, sid, fc`,
		dev, ts, sid, fc, recv, raw, level, flags, score, late, batt)
	if err != nil {
		return nil, fmt.Errorf("insert readings batch: %w", err)
	}
	keys := make([]ReadingKey, len(got))
	for i, g := range got {
		keys[i] = ReadingKey{DeviceID: g.DeviceID, TS: g.TS.UTC(), SID: g.SID, FC: uint32(g.FC)} //nolint:gosec // fc has a CHECK 0..4294967295
	}
	return keys, nil
}

// RecentReadings returns the newest readings of a device, oldest first, to rebuild
// per-device state after a restart.
func (o *OrgStore) RecentReadings(ctx context.Context, deviceID uuid.UUID, limit int) ([]StoredReading, error) {
	rows, err := fetchAll[StoredReading](ctx, o, o.s.pool, `SELECT r.ts, r.received_at, r.sid, r.fc, r.raw_value, r.level_cm,
			r.quality_flags, r.quality_score, r.late, r.batt_v
		FROM readings r JOIN devices d ON d.id = r.device_id AND d.org_id = $1
		WHERE r.device_id = $2 ORDER BY r.ts DESC LIMIT $3`, deviceID, clampLimit(limit, 64, 1000))
	if err != nil {
		return nil, err
	}
	slices.Reverse(rows)
	return rows, nil
}
