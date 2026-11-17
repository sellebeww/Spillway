// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/domain"
)

// DevicesPerLoadSite is how many load test devices share a site (and so a rule set).
const DevicesPerLoadSite = 10

// SeedLoad creates the load test fixture: organization "load" with n devices spread over private
// sites (DevicesPerLoadSite each), a calibration per device and a threshold rule per site, so
// ingest does the same work per message as in production. It refuses to run twice because the
// device secrets are shown only once.
func (s *Store) SeedLoad(ctx context.Context, c *auth.Cipher, n int) ([]SeededDevice, error) {
	if n < 1 || n > 100000 {
		return nil, fmt.Errorf("%w: device count must be 1..100000", ErrInvalid)
	}
	ctx = WithActor(ctx, Actor{Label: "seed-load"})
	if _, err := s.OrgBySlug(ctx, "load"); err == nil {
		return nil, fmt.Errorf("%w: organization \"load\" already exists (drop the database volume to start over)", ErrConflict)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	org, err := s.CreateOrg(ctx, "load", "Spillway Load Test")
	if err != nil {
		return nil, err
	}
	o := s.ForOrg(org.ID)
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var out []SeededDevice
	for first := 0; first < n; first += DevicesPerLoadSite {
		slug := fmt.Sprintf("load-site-%05d", first/DevicesPerLoadSite)
		site, err := o.CreateSite(ctx, SiteInput{Slug: slug, Name: slug, Lat: -6.2 + float64(first%1000)/10000, Lon: 106.8})
		if err != nil {
			return nil, fmt.Errorf("seed site %s: %w", slug, err)
		}
		if _, err := o.CreateRule(ctx, RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Enabled: true, Tiers: []domain.Tier{
			{Value: 100, Severity: domain.SeverityWaspada}, {Value: 150, Severity: domain.SeveritySiaga}, {Value: 200, Severity: domain.SeverityAwas},
		}}); err != nil {
			return nil, fmt.Errorf("seed rule %s: %w", slug, err)
		}
		for i := first; i < min(first+DevicesPerLoadSite, n); i++ {
			key := fmt.Sprintf("load-%05d", i)
			dev, secret, err := o.RegisterDevice(ctx, c, DeviceInput{SiteID: site.ID, Key: key, Name: "Load " + key, ExpectedIntervalS: 30})
			if err != nil {
				return nil, fmt.Errorf("seed device %s: %w", key, err)
			}
			if _, err := o.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: validFrom, Note: "load test"}, false); err != nil {
				return nil, fmt.Errorf("seed calibration %s: %w", key, err)
			}
			out = append(out, SeededDevice{Key: key, SiteSlug: slug, SecretHex: secret})
		}
	}
	return out, nil
}
