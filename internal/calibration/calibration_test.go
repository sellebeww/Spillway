package calibration

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ver(v int, offset float64, validFrom time.Time) domain.Calibration {
	return domain.Calibration{Version: v, OffsetCm: offset, MinValidMM: 250, MaxValidMM: 4500, ValidFrom: validFrom}
}

func TestResolve(t *testing.T) {
	// Deliberately unordered input.
	versions := []domain.Calibration{
		ver(3, 700, t0.Add(2*time.Hour)),
		ver(1, 500, t0),
		ver(2, 600, t0.Add(time.Hour)),
	}
	cases := []struct {
		name    string
		ts      time.Time
		want    int
		wantErr error
	}{
		{"exactly at first valid_from", t0, 1, nil},
		{"one ns before first", t0.Add(-time.Nanosecond), 0, ErrNoCalibration},
		{"one ns before v2 boundary", t0.Add(time.Hour - time.Nanosecond), 1, nil},
		{"exactly at v2 boundary", t0.Add(time.Hour), 2, nil},
		// A late reading taken before v3 existed keeps the old version even if it arrives after v3 was created.
		{"late reading uses old version", t0.Add(90 * time.Minute), 2, nil},
		{"after last", t0.Add(24 * time.Hour), 3, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(versions, c.ts)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if err == nil && got.Version != c.want {
				t.Fatalf("version = %d, want %d", got.Version, c.want)
			}
		})
	}
	if _, err := Resolve(nil, t0); !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("empty list: %v", err)
	}
}

func TestResolveSameValidFromPrefersHigherVersion(t *testing.T) {
	got, err := Resolve([]domain.Calibration{ver(2, 2, t0), ver(1, 1, t0)}, t0)
	if err != nil || got.Version != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLevel(t *testing.T) {
	cases := []struct {
		name   string
		offset float64
		raw    int
		want   float64
	}{
		{"typical", 500, 1234, 376.6},
		{"water at sensor distance equals offset", 300, 0, 300},
		{"negative level below datum", 100, 2000, -100},
		{"zero", 123.4, 1234, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Level(c.offset, c.raw); math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("Level = %v, want %v", got, c.want)
			}
		})
	}
}

func TestValidateAppend(t *testing.T) {
	existing := []domain.Calibration{ver(1, 500, t0), ver(2, 510, t0.Add(time.Hour))}
	later := t0.Add(2 * time.Hour)
	cases := []struct {
		name    string
		existed []domain.Calibration
		next    domain.Calibration
		confirm bool
		wantErr error
	}{
		{"first version", nil, ver(1, 500, t0), false, nil},
		{"first version must be 1", nil, ver(2, 500, t0), false, ErrInvalid},
		{"next version, later valid_from", existing, ver(3, 520, later), false, nil},
		{"equal valid_from allowed", existing, ver(3, 520, t0.Add(time.Hour)), false, nil},
		{"version gap rejected", existing, ver(4, 520, later), false, ErrInvalid},
		{"version reuse rejected", existing, ver(2, 520, later), false, ErrInvalid},
		{"backdated rejected", existing, ver(3, 520, t0.Add(30*time.Minute)), false, ErrBackdated},
		{"backdated accepted with confirmation", existing, ver(3, 520, t0.Add(30*time.Minute)), true, nil},
		{"min >= max", existing, domain.Calibration{Version: 3, OffsetCm: 1, MinValidMM: 500, MaxValidMM: 500, ValidFrom: later}, false, ErrInvalid},
		{"negative min", existing, domain.Calibration{Version: 3, OffsetCm: 1, MinValidMM: -1, MaxValidMM: 500, ValidFrom: later}, false, ErrInvalid},
		{"NaN offset", existing, ver(3, math.NaN(), later), false, ErrInvalid},
		{"zero valid_from", existing, ver(3, 1, time.Time{}), true, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateAppend(c.existed, c.next, c.confirm); !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}
