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

// SeededDevice is a device created by SeedDev with its one-time secret.
type SeededDevice struct {
	Key       string
	SiteSlug  string
	SecretHex string
}

// SeedResult describes what SeedDev did.
type SeedResult struct {
	Created bool // false: the dev organization already existed and nothing was changed
	OrgID   string
	Devices []SeededDevice
}

// SeedDev creates the development fixture: one organization, two sites, three devices with
// calibrations, and example alert rules. It is idempotent. The example threshold values are
// illustrative only; real values must come from the local authorities.
func (s *Store) SeedDev(ctx context.Context, c *auth.Cipher) (SeedResult, error) {
	ctx = WithActor(ctx, Actor{Label: "seed-dev"})
	if org, err := s.OrgBySlug(ctx, "dev"); err == nil {
		return SeedResult{OrgID: org.ID.String()}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return SeedResult{}, err
	}
	org, err := s.CreateOrg(ctx, "dev", "Spillway Dev")
	if err != nil {
		return SeedResult{}, err
	}
	o := s.ForOrg(org.ID)
	res := SeedResult{Created: true, OrgID: org.ID.String()}

	sites := []struct {
		in      SiteInput
		devices []string
	}{
		{SiteInput{Slug: "kali-contoh", Name: "Kali Contoh", Lat: -6.2088, Lon: 106.8456, IsPublic: true}, []string{"dev-001", "dev-002"}},
		{SiteInput{Slug: "sungai-demo", Name: "Sungai Demo", Lat: -6.9175, Lon: 107.6191, IsPublic: false}, []string{"dev-003"}},
	}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // covers every dev reading
	for _, sd := range sites {
		site, err := o.CreateSite(ctx, sd.in)
		if err != nil {
			return res, fmt.Errorf("seed site %s: %w", sd.in.Slug, err)
		}
		for _, key := range sd.devices {
			dev, secret, err := o.RegisterDevice(ctx, c, DeviceInput{SiteID: site.ID, Key: key, Name: "Sensor " + key})
			if err != nil {
				return res, fmt.Errorf("seed device %s: %w", key, err)
			}
			if _, err := o.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: validFrom, Note: "dev seed"}, false); err != nil {
				return res, fmt.Errorf("seed calibration %s: %w", key, err)
			}
			res.Devices = append(res.Devices, SeededDevice{Key: key, SiteSlug: sd.in.Slug, SecretHex: secret})
		}
		if _, err := o.CreateRule(ctx, RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Enabled: true, Tiers: []domain.Tier{
			{Value: 100, Severity: domain.SeverityWaspada}, {Value: 150, Severity: domain.SeveritySiaga}, {Value: 200, Severity: domain.SeverityAwas},
		}}); err != nil {
			return res, fmt.Errorf("seed threshold rule %s: %w", sd.in.Slug, err)
		}
		if _, err := o.CreateRule(ctx, RuleInput{SiteID: site.ID, Kind: domain.KindRateOfRise, Enabled: true, Tiers: []domain.Tier{
			{Value: 30, Severity: domain.SeverityWaspada}, {Value: 60, Severity: domain.SeveritySiaga}, {Value: 120, Severity: domain.SeverityAwas},
		}}); err != nil {
			return res, fmt.Errorf("seed rate-of-rise rule %s: %w", sd.in.Slug, err)
		}
	}
	return res, nil
}
