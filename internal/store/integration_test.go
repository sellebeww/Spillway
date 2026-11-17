package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/calibration"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/rules"
	"github.com/sellebeww/spillway/migrations"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func genRows(start time.Time, n int, sid string, fcStart int) []ReadingRow {
	out := make([]ReadingRow, n)
	for i := range out {
		ts := start.Add(time.Duration(i) * time.Minute)
		out[i] = ReadingRow{TS: ts, ReceivedAt: ts, SID: sid, FC: uint32(fcStart + i), //nolint:gosec // small test counter
			RawMM: 2000, LevelCm: 50 + float64(i%10), Flags: []domain.QualityFlag{domain.FlagOK}, Score: 1}
	}
	return out
}

func TestMigrationsUpAndDown(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `DROP DATABASE IF EXISTS migtest WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `CREATE DATABASE migtest`); err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(ctx, fmt.Sprintf(testBaseURL, "migtest"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate from scratch: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate must be a no-op: %v", err)
	}
	count := func(sql string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tbl := range []string{"organizations", "sites", "devices", "calibrations", "readings", "alert_rules", "alert_events",
		"channels", "subscriptions", "subscription_tokens", "notifications_outbox", "users", "memberships", "audit_log",
		"readings_1m", "readings_1h", "readings_1d"} {
		if count(`SELECT count(*) FROM pg_class WHERE relname = $1`, tbl) == 0 {
			t.Errorf("missing relation %s", tbl)
		}
	}
	if count(`SELECT count(*) FROM timescaledb_information.hypertables WHERE hypertable_name = 'readings'`) != 1 {
		t.Error("readings is not a hypertable")
	}
	if count(`SELECT count(*) FROM timescaledb_information.continuous_aggregates WHERE materialized_only = false`) != 3 {
		t.Error("all three continuous aggregates must have real-time aggregation on")
	}
	if count(`SELECT count(*) FROM timescaledb_information.jobs WHERE proc_name = 'policy_compression' AND config->>'compress_after' = '7 days'`) != 1 {
		t.Error("compression policy after 7 days missing")
	}
	if count(`SELECT count(*) FROM timescaledb_information.jobs WHERE proc_name = 'policy_retention' AND config->>'drop_after' = '90 days'`) != 1 {
		t.Error("default 90 day retention policy missing")
	}
	if count(`SELECT count(*) FROM timescaledb_information.jobs WHERE proc_name = 'policy_refresh_continuous_aggregate'`) != 3 {
		t.Error("refresh policies missing")
	}
	// Every organization-owned table has an org_id column.
	if n := count(`SELECT count(*) FROM information_schema.tables t WHERE table_schema = 'public' AND table_name IN
		('sites','devices','calibrations','alert_rules','alert_events','channels','subscriptions','subscription_tokens',
		 'notifications_outbox','users','memberships','audit_log')
		AND NOT EXISTS (SELECT 1 FROM information_schema.columns c WHERE c.table_name = t.table_name AND c.column_name = 'org_id')`); n != 0 {
		t.Errorf("%d business tables lack org_id", n)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	goose.SetBaseFS(migrations.FS)
	if err := goose.DownToContext(ctx, db, ".", 0); err != nil {
		t.Fatalf("migrate down to 0: %v", err)
	}
	if n := count(`SELECT count(*) FROM pg_class WHERE relname IN ('readings','readings_1m','sites','schema_info')`); n != 0 {
		t.Errorf("%d relations survived the down migrations", n)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate up again after down: %v", err)
	}
}

func TestRetentionIsConfigurable(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	pool, err := NewPool(ctx, fmt.Sprintf(testBaseURL, "migtest"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil { // ensure the schema exists even if run alone
		t.Fatal(err)
	}
	if err := ApplyRetention(ctx, pool, 30); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM timescaledb_information.jobs WHERE proc_name = 'policy_retention' AND config->>'drop_after' = '30 days'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("30 day policy not applied: n=%d err=%v", n, err)
	}
	if err := ApplyRetention(ctx, pool, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retention below 7 days must be refused, got %v", err)
	}
}

func TestUniqueIndexRejectsDuplicates(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	dev := uuid.New()
	ts := time.Now().UTC().Truncate(time.Second)
	const ins = `INSERT INTO readings (device_id, ts, sid, fc, received_at, raw_value, level_cm, quality_flags, quality_score, late)
		VALUES ($1, $2, $3, $4, $2, 1000, 1.5, '{OK}', 1, false)`
	if _, err := testPool.Exec(ctx, ins, dev, ts, "s", 1); err != nil {
		t.Fatal(err)
	}
	_, err := testPool.Exec(ctx, ins, dev, ts, "s", 1)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("duplicate must violate the unique index, got %v", err)
	}
	for name, args := range map[string][]any{"other fc": {dev, ts, "s", 2}, "other sid": {dev, ts, "t", 1}, "other device": {uuid.New(), ts, "s", 1}} {
		if _, err := testPool.Exec(ctx, ins, args...); err != nil {
			t.Errorf("%s must be allowed: %v", name, err)
		}
	}
}

func TestInsertReadingsIsIdempotent(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	rows := genRows(base, 100, "s1", 0)

	if n, err := o.InsertReadings(ctx, fx.Device.ID, rows); err != nil || n != 100 {
		t.Fatalf("first insert: n=%d err=%v", n, err)
	}
	if n, err := o.InsertReadings(ctx, fx.Device.ID, rows); err != nil || n != 0 {
		t.Fatalf("replay must insert nothing: n=%d err=%v", n, err)
	}
	overlap := append(slices.Clone(rows[50:]), genRows(base.Add(100*time.Minute), 50, "s1", 100)...)
	if n, err := o.InsertReadings(ctx, fx.Device.ID, overlap); err != nil || n != 50 {
		t.Fatalf("overlapping batch must report only the 50 new rows: n=%d err=%v", n, err)
	}
	if n := countRows(t, `SELECT count(*) FROM readings WHERE device_id = $1`, fx.Device.ID); n != 150 {
		t.Fatalf("stored %d rows, want 150", n)
	}
	if n, err := o.InsertReadings(ctx, fx.Device.ID, []ReadingRow{rows[0], rows[0]}); err != nil || n != 0 {
		t.Fatalf("duplicates inside a batch: n=%d err=%v", n, err)
	}
	if n, err := o.InsertReadings(ctx, fx.Device.ID, nil); err != nil || n != 0 {
		t.Fatalf("empty batch: n=%d err=%v", n, err)
	}
	if _, err := o.InsertReadings(ctx, uuid.New(), rows[:1]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
	big := genRows(base.Add(48*time.Hour), maxBatch+1, "big", 0)
	if n, err := o.InsertReadings(ctx, fx.Device.ID, big); err != nil || n != maxBatch+1 {
		t.Fatalf("batch larger than one statement: n=%d err=%v", n, err)
	}
}

func TestReadingsRoundTrip(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	batt := 3.2
	rows := genRows(base, 5, "s", 0)
	rows[0].Flags = []domain.QualityFlag{domain.FlagSpike, domain.FlagLowBattery}
	rows[0].Score, rows[0].Late, rows[0].BattV = 0.2, true, &batt
	if _, err := o.InsertReadings(ctx, fx.Device.ID, rows); err != nil {
		t.Fatal(err)
	}
	got, err := o.ReadingsRange(ctx, fx.Device.ID, base, base.Add(time.Hour), 0)
	if err != nil || len(got) != 5 {
		t.Fatalf("range: %d rows, %v", len(got), err)
	}
	if !got[0].TS.Equal(base) || !slices.Equal(got[0].Flags, []string{"SPIKE", "LOW_BATTERY"}) || !got[0].Late ||
		got[0].BattV == nil || math.Abs(*got[0].BattV-3.2) > 1e-6 || math.Abs(got[0].Score-0.2) > 1e-6 || got[0].RawMM != 2000 {
		t.Fatalf("first row did not round-trip: %+v", got[0])
	}
	if got[1].BattV != nil || got[1].Late {
		t.Fatalf("null battery / late=false lost: %+v", got[1])
	}
	if lim, _ := o.ReadingsRange(ctx, fx.Device.ID, base, base.Add(time.Hour), 2); len(lim) != 2 {
		t.Fatalf("limit ignored: %d", len(lim))
	}
	if half, _ := o.ReadingsRange(ctx, fx.Device.ID, base, base.Add(2*time.Minute), 0); len(half) != 2 {
		t.Fatalf("[from,to) must exclude to: %d", len(half))
	}
	last, err := o.LatestReading(ctx, fx.Device.ID)
	if err != nil || !last.TS.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("latest: %+v %v", last, err)
	}
	if _, err := o.Aggregates(ctx, fx.Device.ID, ResRaw, base, base.Add(time.Hour), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("raw is not an aggregate: %v", err)
	}
}

type bucketAgg struct {
	min, max, sum float64
	n             int64
}

func expectedAgg(rows []ReadingRow, size time.Duration) map[time.Time]*bucketAgg {
	out := map[time.Time]*bucketAgg{}
	for _, r := range rows {
		if r.Score < 0.5 {
			continue
		}
		b := r.TS.UTC().Truncate(size)
		a := out[b]
		if a == nil {
			a = &bucketAgg{min: math.Inf(1), max: math.Inf(-1)}
			out[b] = a
		}
		a.min, a.max, a.sum, a.n = math.Min(a.min, r.LevelCm), math.Max(a.max, r.LevelCm), a.sum+r.LevelCm, a.n+1
	}
	return out
}

func checkAggregates(t *testing.T, label string, o *OrgStore, dev uuid.UUID, rows []ReadingRow) {
	t.Helper()
	for res, size := range map[Resolution]time.Duration{Res1m: time.Minute, Res1h: time.Hour, Res1d: 24 * time.Hour} {
		want := expectedAgg(rows, size)
		got, err := o.Aggregates(context.Background(), dev, res, base, base.Add(24*time.Hour), 0)
		if err != nil {
			t.Fatalf("%s %s: %v", label, res, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s %s: %d buckets, want %d", label, res, len(got), len(want))
		}
		for _, g := range got {
			w := want[g.Bucket.UTC()]
			if w == nil || g.N != w.n || math.Abs(g.Min-w.min) > 1e-9 || math.Abs(g.Max-w.max) > 1e-9 || math.Abs(g.Avg-w.sum/float64(w.n)) > 1e-9 {
				t.Fatalf("%s %s bucket %v: got %+v, want %+v", label, res, g.Bucket, g, w)
			}
		}
	}
}

func TestContinuousAggregatesMatchManualCalculation(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)

	rows := genRows(base, 180, "agg", 0)
	for i := range rows {
		rows[i].LevelCm = 100 + 10*math.Sin(float64(i)/10)
		switch {
		case i%7 == 3:
			rows[i].Score = 0.3 // below min_quality: must be excluded
		case i%11 == 5:
			rows[i].Score = 0.5 // exactly min_quality: must be included
		}
	}
	if _, err := o.InsertReadings(ctx, fx.Device.ID, rows); err != nil {
		t.Fatal(err)
	}

	// Real-time aggregation: correct before anything has been materialized.
	checkAggregates(t, "real-time", o, fx.Device.ID, rows)

	refresh := func() {
		for _, v := range []string{"readings_1m", "readings_1h", "readings_1d"} {
			if _, err := testPool.Exec(ctx, fmt.Sprintf(`CALL refresh_continuous_aggregate('%s', '2025-12-31', '2026-01-03')`, v)); err != nil {
				t.Fatalf("refresh %s: %v", v, err)
			}
		}
	}
	refresh()
	checkAggregates(t, "materialized", o, fx.Device.ID, rows)

	// A late reading inside an already materialized bucket is picked up by the next refresh.
	late := genRows(base.Add(30*time.Minute), 1, "late", 9999)
	late[0].LevelCm = 150
	if n, err := o.InsertReadings(ctx, fx.Device.ID, late); err != nil || n != 1 {
		t.Fatalf("late insert: %d %v", n, err)
	}
	rows = append(rows, late...)
	refresh()
	checkAggregates(t, "after late data", o, fx.Device.ID, rows)

	if span := ChooseResolution(24 * time.Hour); span != Res1m {
		t.Fatalf("24h chart should use 1m, got %s", span)
	}
}

func TestCalibrationSelectionAndAppendOnly(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	dev := fx.Device.ID
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }

	for i, h := range []int{0, 1, 2} {
		c, err := o.AddCalibration(ctx, dev, domain.Calibration{OffsetCm: 500 + float64(i)*10, ValidFrom: at(h), Note: "v"}, false)
		if err != nil || c.Version != i+1 || c.MinValidMM != 250 || c.MaxValidMM != 4500 {
			t.Fatalf("add v%d: %+v %v", i+1, c, err)
		}
	}
	if _, err := o.AddCalibration(ctx, dev, domain.Calibration{OffsetCm: 1, ValidFrom: at(0).Add(30 * time.Minute)}, false); !errors.Is(err, ErrInvalid) || !errors.Is(err, calibration.ErrBackdated) {
		t.Fatalf("backdating without confirmation must be refused: %v", err)
	}
	if c, err := o.AddCalibration(ctx, dev, domain.Calibration{OffsetCm: 1, ValidFrom: at(0).Add(30 * time.Minute)}, true); err != nil || c.Version != 4 {
		t.Fatalf("confirmed backdate: %+v %v", c, err)
	}
	if _, err := o.AddCalibration(ctx, dev, domain.Calibration{Version: 9, OffsetCm: 1, ValidFrom: at(5)}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("version gap: %v", err)
	}
	if _, err := o.AddCalibration(ctx, dev, domain.Calibration{OffsetCm: 1, ValidFrom: at(5), MinValidMM: 900, MaxValidMM: 900}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("min >= max: %v", err)
	}

	all, err := o.ListCalibrations(ctx, dev)
	if err != nil || len(all) != 4 {
		t.Fatalf("list: %d %v", len(all), err)
	}
	// SQL selection must agree with the pure Resolve at every interesting instant.
	for _, ts := range []time.Time{at(0), at(0).Add(-time.Nanosecond), at(0).Add(30 * time.Minute), at(0).Add(45 * time.Minute),
		at(1).Add(-time.Nanosecond), at(1), at(2), at(10)} {
		want, wantErr := calibration.Resolve(all, ts)
		got, gotErr := o.ResolveCalibration(ctx, dev, ts)
		if !errors.Is(gotErr, wantErr) || (wantErr == nil && got.Version != want.Version) {
			t.Errorf("at %v: SQL gives v%d (%v), Go gives v%d (%v)", ts, got.Version, gotErr, want.Version, wantErr)
		}
	}

	// Append-only is enforced by the database itself.
	for _, stmt := range []string{`UPDATE calibrations SET note = 'x' WHERE device_id = $1`, `DELETE FROM calibrations WHERE device_id = $1`} {
		if _, err := testPool.Exec(ctx, stmt, dev); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q must be refused, got %v", stmt, err)
		}
	}

	// Concurrent additions get consecutive, unique versions.
	fx2 := newFixture(t, o)
	var wg sync.WaitGroup
	versions := make([]int, 6)
	for i := range versions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := o.AddCalibration(ctx, fx2.Device.ID, domain.Calibration{OffsetCm: float64(i), ValidFrom: base}, false)
			if err != nil {
				t.Error(err)
			}
			versions[i] = c.Version
		}()
	}
	wg.Wait()
	slices.Sort(versions)
	if !slices.Equal(versions, []int{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("concurrent versions: %v", versions)
	}
}

func TestDeviceSecretIsEncryptedAndBound(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	cipher := testCipher(t)
	o := newOrg(t, s)
	fx := newFixture(t, o)

	var enc []byte
	if err := testPool.QueryRow(ctx, `SELECT secret_enc FROM devices WHERE id = $1`, fx.Device.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc[0] != 1 || strings.Contains(string(enc), fx.Secret) {
		t.Fatalf("stored secret must be a version-1 blob without the plaintext")
	}
	a, err := s.DeviceAuthByKey(ctx, fx.Device.Key)
	if err != nil || a.ID != fx.Device.ID || a.OrgID != o.OrgID() {
		t.Fatalf("auth lookup: %+v %v", a, err)
	}
	raw, err := a.Secret(cipher)
	if err != nil || fmt.Sprintf("%x", raw) != fx.Secret {
		t.Fatalf("secret did not round-trip: %v", err)
	}
	wrong, _ := auth.NewCipher(1, make([]byte, 32), nil)
	if _, err := a.Secret(wrong); !errors.Is(err, auth.ErrDecrypt) {
		t.Fatalf("wrong key must fail, got %v", err)
	}

	// A secret copied onto another device row does not decrypt (AAD = device id).
	other := newFixture(t, o)
	if _, err := testPool.Exec(ctx, `UPDATE devices SET secret_enc = $2 WHERE id = $1`, other.Device.ID, enc); err != nil {
		t.Fatal(err)
	}
	b, _ := s.DeviceAuthByKey(ctx, other.Device.Key)
	if _, err := b.Secret(cipher); !errors.Is(err, auth.ErrDecrypt) {
		t.Fatalf("a swapped secret must not decrypt, got %v", err)
	}

	if js, _ := json.Marshal(fx.Device); strings.Contains(string(js), fx.Secret) || strings.Contains(strings.ToLower(string(js)), "secret") {
		t.Fatal("Device must not expose the secret")
	}
	if _, err := s.DeviceAuthByKey(ctx, "no-such-device"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	// Keys are globally unique, also across organizations (they form the MQTT topic).
	o2 := newOrg(t, s)
	site2, _ := o2.CreateSite(ctx, SiteInput{Slug: "x-site", Name: "X", Lat: 1, Lon: 1})
	if _, _, err := o2.RegisterDevice(ctx, cipher, DeviceInput{SiteID: site2.ID, Key: fx.Device.Key, Name: "dup"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key across orgs: %v", err)
	}
	for _, bad := range []DeviceInput{{SiteID: fx.Site.ID, Key: "has space", Name: "n"}, {SiteID: fx.Site.ID, Key: "ok-key", Name: " "},
		{SiteID: fx.Site.ID, Key: "ok-key", Name: "n", ExpectedIntervalS: 999999}} {
		if _, _, err := o.RegisterDevice(ctx, cipher, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestAuditLog(t *testing.T) {
	s := requireDB(t)
	cipher := testCipher(t)
	o := newOrg(t, s)
	user, err := o.UpsertUser(context.Background(), "sub-1", nil, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), Actor{UserID: &user.ID, Label: "alice"})

	site, err := o.CreateSite(ctx, SiteInput{Slug: "audit-site", Name: "Audit", Lat: 1, Lon: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.UpdateSite(ctx, site.ID, SiteInput{Slug: "audit-site", Name: "Renamed", Lat: 1, Lon: 2, IsPublic: true}); err != nil {
		t.Fatal(err)
	}
	dev, secret, err := o.RegisterDevice(ctx, cipher, DeviceInput{SiteID: site.ID, Key: "audit-dev", Name: "D"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: base}, false); err != nil {
		t.Fatal(err)
	}
	rule, err := o.CreateRule(ctx, RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Enabled: true, Tiers: []domain.Tier{{Value: 100, Severity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.UpdateRule(ctx, rule.ID, RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Enabled: false, Tiers: []domain.Tier{{Value: 120, Severity: 1}}}); err != nil {
		t.Fatal(err)
	}
	ch, _ := o.UpsertChannel(ctx, "telegram", "12345")
	_ = o.Subscribe(ctx, site.ID, ch.ID)
	_ = o.DeactivateChannel(ctx, ch.ID)
	m, _ := o.SetMembership(ctx, user.ID, RoleOperator, &site.ID)
	_ = o.RemoveMembership(ctx, m.ID)

	entries, err := o.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
		if e.ActorLabel != "alice" && e.Action != "org.create" {
			t.Errorf("%s: actor %q", e.Action, e.ActorLabel)
		}
		blob := strings.ToLower(string(e.Before) + string(e.After))
		if strings.Contains(blob, secret) || strings.Contains(blob, "secret") {
			t.Errorf("%s: audit entry contains secret material", e.Action)
		}
	}
	slices.Reverse(actions)
	want := []string{"org.create", "site.create", "site.update", "device.register", "calibration.add", "rule.create", "rule.update",
		"channel.upsert", "subscription.add", "channel.deactivate", "membership.add", "membership.remove"}
	if !slices.Equal(actions, want) {
		t.Fatalf("audit actions:\n got %v\nwant %v", actions, want)
	}
	var upd AuditEntry
	for _, e := range entries {
		if e.Action == "site.update" {
			upd = e
		}
	}
	if !strings.Contains(string(upd.Before), `"Audit"`) || !strings.Contains(string(upd.After), `"Renamed"`) || upd.ActorUserID == nil || *upd.ActorUserID != user.ID {
		t.Fatalf("update must record before, after and the actor: %s | %s", upd.Before, upd.After)
	}

	// A failed change leaves no audit row (same transaction).
	before := len(entries)
	if _, err := o.CreateSite(ctx, SiteInput{Slug: "audit-site", Name: "Dup", Lat: 1, Lon: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
	if after, _ := o.ListAudit(ctx, 100); len(after) != before {
		t.Fatalf("a failed change was audited: %d -> %d", before, len(after))
	}

	for _, stmt := range []string{`UPDATE audit_log SET action = 'x' WHERE org_id = $1`, `DELETE FROM audit_log WHERE org_id = $1`} {
		if _, err := testPool.Exec(context.Background(), stmt, o.OrgID()); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q must be refused, got %v", stmt, err)
		}
	}
}

func TestRulesAndEvents(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	tiers := []domain.Tier{{Value: 100, Severity: 1}, {Value: 150, Severity: 2}}

	rule, err := o.CreateRule(ctx, RuleInput{SiteID: fx.Site.ID, Kind: domain.KindThreshold, Tiers: tiers, Enabled: true})
	if err != nil || rule.Cooldown != 30*time.Minute || rule.MinQuality != 0.5 || rule.HysteresisCm != 5 {
		t.Fatalf("defaults: %+v %v", rule, err)
	}
	if _, err := rules.NewEngine([]domain.Rule{rule.Domain()}, rule.MinQuality); err != nil {
		t.Fatalf("a stored rule must load into the engine: %v", err)
	}
	ror, err := o.CreateRule(ctx, RuleInput{SiteID: fx.Site.ID, Kind: domain.KindRateOfRise, Tiers: []domain.Tier{{Value: 30, Severity: 1}}, Enabled: true})
	if err != nil || ror.HysteresisCm != 0 {
		t.Fatalf("rate of rise: %+v %v", ror, err)
	}
	if list, _ := o.ListRules(ctx, &fx.Site.ID); len(list) != 2 {
		t.Fatalf("list by site: %d", len(list))
	}
	if _, err := o.CreateRule(ctx, RuleInput{SiteID: fx.Site.ID, Kind: domain.KindThreshold, Tiers: nil}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no tiers: %v", err)
	}
	upd, err := o.UpdateRule(ctx, rule.ID, RuleInput{SiteID: fx.Site.ID, Kind: domain.KindThreshold, Tiers: tiers[:1], Enabled: false, Cooldown: time.Hour, MinQuality: 0.7})
	if err != nil || upd.Enabled || upd.Cooldown != time.Hour || upd.MinQuality < 0.69 || len(upd.Tiers) != 1 {
		t.Fatalf("update: %+v %v", upd, err)
	}

	ev, err := o.OpenEvent(ctx, rule.ID, domain.SeverityWaspada, base, 110, nil)
	if err != nil || ev.State != "OPEN" || ev.Severity != domain.SeverityWaspada {
		t.Fatalf("open: %+v %v", ev, err)
	}
	if _, err := o.OpenEvent(ctx, rule.ID, domain.SeverityWaspada, base, 111, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second open event for one rule must conflict, got %v", err)
	}
	rate := 42.0
	if ev, err = o.UpdateEvent(ctx, ev.ID, domain.SeveritySiaga, 160, &rate, base.Add(time.Minute)); err != nil || ev.Severity != domain.SeveritySiaga || *ev.PeakLevel != 160 || *ev.PeakRate != 42 {
		t.Fatalf("escalate: %+v %v", ev, err)
	}
	if ev, err = o.UpdateEvent(ctx, ev.ID, domain.SeverityWaspada, 120, nil, base.Add(2*time.Minute)); err != nil || ev.Severity != domain.SeverityWaspada || *ev.PeakLevel != 160 || *ev.PeakRate != 42 {
		t.Fatalf("de-escalate must keep the peaks: %+v %v", ev, err)
	}
	if got, err := o.OpenEventForRule(ctx, rule.ID); err != nil || got.ID != ev.ID {
		t.Fatalf("open lookup: %v", err)
	}
	if closed, err := o.CloseEvent(ctx, ev.ID, base.Add(time.Hour)); err != nil || closed.State != "CLOSED" || closed.ClosedAt == nil {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, err := o.CloseEvent(ctx, ev.ID, base.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closing twice: %v", err)
	}
	if _, err := o.OpenEventForRule(ctx, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no open event after close: %v", err)
	}
	if _, err := o.OpenEvent(ctx, rule.ID, domain.SeverityAwas, base.Add(2*time.Hour), 210, nil); err != nil {
		t.Fatalf("a new event may open after the previous closed: %v", err)
	}
	if list, _ := o.ListEvents(ctx, rule.ID, 10); len(list) != 2 || list[0].State != "OPEN" {
		t.Fatalf("events newest first: %+v", list)
	}
}

func newEventAndChannel(t *testing.T, o *OrgStore, fx fixture) (AlertEvent, Channel) {
	t.Helper()
	ctx := context.Background()
	rule, err := o.CreateRule(ctx, RuleInput{SiteID: fx.Site.ID, Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 100, Severity: 1}}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := o.OpenEvent(ctx, rule.ID, domain.SeverityWaspada, base, 101, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := o.UpsertChannel(ctx, "telegram", uuid.NewString()[:12])
	if err != nil {
		t.Fatal(err)
	}
	return ev, ch
}

func TestOutboxIsIdempotentAndLeased(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	ev, ch := newEventAndChannel(t, o, fx)

	in := NotificationInput{EventID: ev.ID, Kind: "OPENED", ChannelID: ch.ID, Payload: []byte(`{"level_cm":101}`)}
	if ok, err := o.EnqueueNotification(ctx, in); err != nil || !ok {
		t.Fatalf("first enqueue: %v %v", ok, err)
	}
	if ok, err := o.EnqueueNotification(ctx, in); err != nil || ok {
		t.Fatalf("the same (event, kind, channel) must not enqueue twice: %v %v", ok, err)
	}
	rem := NotificationInput{EventID: ev.ID, Kind: "REMINDER", Seq: 1, ChannelID: ch.ID}
	if ok, _ := o.EnqueueNotification(ctx, rem); !ok {
		t.Fatal("a reminder with a new seq is a new notification")
	}
	rem.Seq = 2
	if ok, _ := o.EnqueueNotification(ctx, rem); !ok {
		t.Fatal("reminder seq 2")
	}
	if _, err := o.EnqueueNotification(ctx, NotificationInput{EventID: ev.ID, Kind: "BOGUS", ChannelID: ch.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}

	now := time.Now().Add(time.Minute) // the rows were created "now" in database time
	claimed, err := s.ClaimDueNotifications(ctx, now, 10, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mine := map[int64]Notification{}
	for _, n := range claimed {
		if n.OrgID == o.OrgID() {
			mine[n.ID] = n
			if n.Attempts != 1 {
				t.Errorf("attempts after first claim: %d", n.Attempts)
			}
		}
	}
	if len(mine) != 3 {
		t.Fatalf("claimed %d of my 3 notifications", len(mine))
	}
	again, _ := s.ClaimDueNotifications(ctx, now, 100, 5*time.Minute)
	for _, n := range again {
		if _, dup := mine[n.ID]; dup {
			t.Fatalf("notification %d was claimed twice inside its lease", n.ID)
		}
	}
	later, _ := s.ClaimDueNotifications(ctx, now.Add(10*time.Minute), 100, 5*time.Minute)
	reclaimed := 0
	for _, n := range later {
		if _, ok := mine[n.ID]; ok {
			reclaimed++
			if n.Attempts != 2 {
				t.Errorf("attempts after reclaim: %d", n.Attempts)
			}
		}
	}
	if reclaimed != 3 {
		t.Fatalf("expired leases must be claimable again: %d", reclaimed)
	}

	var ids []int64
	for id := range mine {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if err := o.MarkNotificationSent(ctx, ids[0], now); err != nil {
		t.Fatal(err)
	}
	if err := o.MarkNotificationRetry(ctx, ids[1], now.Add(time.Hour), strings.Repeat("e", 900)); err != nil {
		t.Fatal(err)
	}
	if err := o.MarkNotificationFailed(ctx, ids[2], "blocked by user"); err != nil {
		t.Fatal(err)
	}
	if n, _ := o.GetNotification(ctx, ids[0]); n.Status != "SENT" {
		t.Fatalf("sent: %+v", n)
	}
	if n, _ := o.GetNotification(ctx, ids[1]); n.Status != "PENDING" || n.LastError == nil || len(*n.LastError) != 500 {
		t.Fatalf("retry must keep PENDING and truncate the error: %+v", n)
	}
	if n, _ := o.GetNotification(ctx, ids[2]); n.Status != "FAILED" {
		t.Fatalf("failed: %+v", n)
	}
	if err := o.MarkNotificationSent(ctx, ids[0], now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a finished notification cannot be finished again: %v", err)
	}
}

func TestConcurrentWorkersNeverShareAClaim(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	ev, ch := newEventAndChannel(t, o, fx)
	for i := 1; i <= 20; i++ {
		if ok, err := o.EnqueueNotification(ctx, NotificationInput{EventID: ev.ID, Kind: "REMINDER", Seq: i, ChannelID: ch.ID}); err != nil || !ok {
			t.Fatal(err)
		}
	}
	now := time.Now().Add(time.Minute)
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 6 {
				got, err := s.ClaimDueNotifications(ctx, now, 5, time.Hour)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				for _, n := range got {
					if n.OrgID == o.OrgID() {
						seen[n.ID]++
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct of 20", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("notification %d claimed %d times", id, n)
		}
	}
}

func TestSubscriptionTokens(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	now := time.Now()

	token, exp, err := o.CreateSubscriptionToken(ctx, fx.Site.ID, time.Hour, now)
	if err != nil || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("create: %v", err)
	}
	if n := countRows(t, `SELECT count(*) FROM subscription_tokens WHERE position(convert_to($1, 'UTF8') in token_hash) > 0`, token); n != 0 {
		t.Fatal("the plaintext token must not be stored")
	}
	org, site, err := s.RedeemSubscriptionToken(ctx, token, now.Add(time.Minute))
	if err != nil || org != o.OrgID() || site != fx.Site.ID {
		t.Fatalf("redeem: %v %v %v", org, site, err)
	}
	if _, _, err := s.RedeemSubscriptionToken(ctx, token, now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tokens are single use: %v", err)
	}
	expired, _, _ := o.CreateSubscriptionToken(ctx, fx.Site.ID, time.Hour, now)
	if _, _, err := s.RedeemSubscriptionToken(ctx, expired, now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token: %v", err)
	}
	if _, _, err := s.RedeemSubscriptionToken(ctx, "not-a-token", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, _, err := o.CreateSubscriptionToken(ctx, fx.Site.ID, 0, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero ttl: %v", err)
	}
}

func TestChannelsAndSubscriptions(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	ch, err := o.UpsertChannel(ctx, "telegram", "777")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := o.UpsertChannel(ctx, "telegram", "777"); again.ID != ch.ID {
		t.Fatal("upsert must be idempotent")
	}
	if err := o.Subscribe(ctx, fx.Site.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := o.Subscribe(ctx, fx.Site.ID, ch.ID); err != nil {
		t.Fatalf("subscribing twice is fine: %v", err)
	}
	subs, _ := o.ListSubscribers(ctx, fx.Site.ID)
	sites, _ := o.ListChannelSites(ctx, ch.ID)
	if len(subs) != 1 || len(sites) != 1 || sites[0] != fx.Site.ID {
		t.Fatalf("subscribers=%d sites=%d", len(subs), len(sites))
	}
	if err := o.DeactivateChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if subs, _ := o.ListSubscribers(ctx, fx.Site.ID); len(subs) != 0 {
		t.Fatal("deactivated channels receive nothing")
	}
	if re, _ := o.UpsertChannel(ctx, "telegram", "777"); !re.IsActive {
		t.Fatal("upsert reactivates")
	}
	if err := o.Unsubscribe(ctx, fx.Site.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := o.Unsubscribe(ctx, fx.Site.ID, ch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second unsubscribe: %v", err)
	}
}

func TestUsersAndMemberships(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	email := "a@example.org"
	u, err := o.UpsertUser(ctx, "sub-x", &email, "A")
	if err != nil {
		t.Fatal(err)
	}
	if u2, _ := o.UpsertUser(ctx, "sub-x", nil, "Renamed"); u2.ID != u.ID || u2.DisplayName != "Renamed" {
		t.Fatal("upsert by subject must update in place")
	}
	if _, err := o.SetMembership(ctx, u.ID, RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.SetMembership(ctx, u.ID, RoleAdmin, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("one admin membership per user: %v", err)
	}
	if _, err := o.SetMembership(ctx, u.ID, RoleOperator, &fx.Site.ID); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func() error{
		"admin with a site":     func() error { _, e := o.SetMembership(ctx, u.ID, RoleAdmin, &fx.Site.ID); return e },
		"operator without":      func() error { _, e := o.SetMembership(ctx, u.ID, RoleOperator, nil); return e },
		"unknown role":          func() error { _, e := o.SetMembership(ctx, u.ID, "root", nil); return e },
		"operator unknown site": func() error { x := uuid.New(); _, e := o.SetMembership(ctx, u.ID, RoleOperator, &x); return e },
	} {
		if err := f(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if ms, _ := o.ListMemberships(ctx, u.ID); len(ms) != 2 {
		t.Fatalf("memberships: %d", len(ms))
	}
}

func TestDeviceStatusUpdate(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	o := newOrg(t, s)
	fx := newFixture(t, o)
	if fx.Device.LastStatus != domain.StatusOffline || fx.Device.LastSeenAt != nil {
		t.Fatalf("new devices start OFFLINE: %+v", fx.Device)
	}
	if err := o.UpdateDeviceStatus(ctx, fx.Device.ID, base.Add(time.Hour), domain.StatusOnline); err != nil {
		t.Fatal(err)
	}
	if err := o.UpdateDeviceStatus(ctx, fx.Device.ID, base, domain.StatusDelayed); err != nil { // older timestamp must not move last_seen back
		t.Fatal(err)
	}
	d, _ := o.GetDevice(ctx, fx.Device.ID)
	if d.LastSeenAt == nil || !d.LastSeenAt.Equal(base.Add(time.Hour)) || d.LastStatus != domain.StatusDelayed {
		t.Fatalf("status: %+v", d)
	}
	if err := o.UpdateDeviceStatus(ctx, fx.Device.ID, base, "BROKEN"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid status: %v", err)
	}
	if err := o.UpdateDeviceStatus(ctx, uuid.New(), base, domain.StatusOnline); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
	if list, _ := o.ListDevices(ctx, nil); len(list) != 1 {
		t.Fatalf("list all: %d", len(list))
	}
}

func TestSeedDevIsIdempotent(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	cipher := testCipher(t)
	res, err := s.SeedDev(ctx, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || len(res.Devices) != 3 {
		t.Fatalf("first run: %+v", res)
	}
	o := s.ForOrg(uuid.MustParse(res.OrgID))
	sites, _ := o.ListSites(ctx)
	devs, _ := o.ListDevices(ctx, nil)
	ruleList, _ := o.ListRules(ctx, nil)
	if len(sites) != 2 || len(devs) != 3 || len(ruleList) != 4 {
		t.Fatalf("sites=%d devices=%d rules=%d", len(sites), len(devs), len(ruleList))
	}
	for _, d := range devs {
		cals, _ := o.ListCalibrations(ctx, d.ID)
		if len(cals) != 1 || cals[0].Version != 1 {
			t.Errorf("device %s calibrations: %+v", d.Key, cals)
		}
	}
	for _, sd := range res.Devices {
		a, err := s.DeviceAuthByKey(ctx, sd.Key)
		if err != nil {
			t.Fatal(err)
		}
		if raw, err := a.Secret(cipher); err != nil || fmt.Sprintf("%x", raw) != sd.SecretHex {
			t.Errorf("%s: printed secret does not match the stored one", sd.Key)
		}
	}
	again, err := s.SeedDev(ctx, cipher)
	if err != nil || again.Created || len(again.Devices) != 0 {
		t.Fatalf("second run must change nothing: %+v %v", again, err)
	}
	if devs2, _ := o.ListDevices(ctx, nil); len(devs2) != 3 {
		t.Fatal("second run duplicated devices")
	}
	// The seeded rules are valid input for the pure engine.
	for _, r := range ruleList {
		if _, err := rules.NewEngine([]domain.Rule{r.Domain()}, r.MinQuality); err != nil {
			t.Errorf("seeded rule rejected by the engine: %v", err)
		}
	}
}

func TestSeedLoadCreatesWorkingDevicesOnce(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	cipher := testCipher(t)
	devs, err := s.SeedLoad(ctx, cipher, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 25 {
		t.Fatalf("devices: %d", len(devs))
	}
	org, err := s.OrgBySlug(ctx, "load")
	if err != nil {
		t.Fatal(err)
	}
	o := s.ForOrg(org.ID)
	sites, _ := o.ListSites(ctx)
	rules, _ := o.ListRules(ctx, nil)
	if len(sites) != 3 || len(rules) != 3 { // 25 devices at 10 per site
		t.Fatalf("sites=%d rules=%d", len(sites), len(rules))
	}
	last := devs[len(devs)-1]
	a, err := s.DeviceAuthByKey(ctx, last.Key)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := a.Secret(cipher); err != nil || fmt.Sprintf("%x", raw) != last.SecretHex {
		t.Error("printed secret does not match the stored one")
	}
	if _, err := s.SeedLoad(ctx, cipher, 25); !errors.Is(err, ErrConflict) {
		t.Errorf("a second run must refuse (secrets are shown once), got %v", err)
	}
	if _, err := s.SeedLoad(ctx, cipher, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("zero devices: %v", err)
	}
}
