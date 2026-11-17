// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package calibration resolves datum calibration versions and converts raw distance to level.
package calibration

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
)

// Errors returned by this package.
var (
	ErrNoCalibration = errors.New("no calibration valid at the reading time")
	ErrBackdated     = errors.New("valid_from is before the latest active version; explicit confirmation required")
	ErrInvalid       = errors.New("invalid calibration")
)

// Resolve returns the version in force at reading time ts (not arrival time):
// the one with the latest valid_from <= ts, preferring the higher version on ties.
func Resolve(versions []domain.Calibration, ts time.Time) (domain.Calibration, error) {
	var best domain.Calibration
	found := false
	for _, v := range versions {
		if v.ValidFrom.After(ts) {
			continue
		}
		if !found || v.ValidFrom.After(best.ValidFrom) || (v.ValidFrom.Equal(best.ValidFrom) && v.Version > best.Version) {
			best, found = v, true
		}
	}
	if !found {
		return domain.Calibration{}, ErrNoCalibration
	}
	return best, nil
}

// Level converts a raw sensor distance (mm) to water level (cm above datum zero):
// level_cm = offset_cm - raw_mm/10. It may be negative.
func Level(offsetCm float64, rawMM int) float64 {
	return offsetCm - float64(rawMM)/10
}

// ValidateAppend checks that next may be appended to the append-only history.
// The version must be max+1 (1 for the first), the parameters sane, and valid_from
// must not precede the latest active version unless confirmBackdate is set.
func ValidateAppend(existing []domain.Calibration, next domain.Calibration, confirmBackdate bool) error {
	maxVersion := 0
	var latestFrom time.Time
	for _, v := range existing {
		maxVersion = max(maxVersion, v.Version)
		if v.ValidFrom.After(latestFrom) {
			latestFrom = v.ValidFrom
		}
	}
	switch {
	case next.Version != maxVersion+1:
		return fmt.Errorf("%w: version must be %d, got %d", ErrInvalid, maxVersion+1, next.Version)
	case math.IsNaN(next.OffsetCm) || math.IsInf(next.OffsetCm, 0):
		return fmt.Errorf("%w: offset_cm is not finite", ErrInvalid)
	case next.MinValidMM < 0 || next.MinValidMM >= next.MaxValidMM:
		return fmt.Errorf("%w: need 0 <= min_valid_mm < max_valid_mm", ErrInvalid)
	case next.ValidFrom.IsZero():
		return fmt.Errorf("%w: valid_from is required", ErrInvalid)
	case next.ValidFrom.Before(latestFrom) && !confirmBackdate:
		return ErrBackdated
	}
	return nil
}
