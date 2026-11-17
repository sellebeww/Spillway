package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/calibration"
	"github.com/sellebeww/spillway/internal/domain"
)

// TestOrganizationIsolation fills organization A, then drives EVERY repository method of
// organization B against A's identifiers. Reads must find nothing, writes must fail, and
// A's data must be byte-for-byte unchanged afterwards.
func TestOrganizationIsolation(t *testing.T) {
	s := requireDB(t)
	ctx := context.Background()
	a, b := newOrg(t, s), newOrg(t, s)
	fa := newFixture(t, a)
	now := time.Now()

	// --- organization A's data -------------------------------------------------------------
	if _, err := a.AddCalibration(ctx, fa.Device.ID, domain.Calibration{OffsetCm: 500, ValidFrom: base}, false); err != nil {
		t.Fatal(err)
	}
	if n, err := a.InsertReadings(ctx, fa.Device.ID, genRows(base, 10, "s", 0)); err != nil || n != 10 {
		t.Fatal(n, err)
	}
	ev, ch := newEventAndChannel(t, a, fa)
	rules, _ := a.ListRules(ctx, &fa.Site.ID)
	ruleID := rules[0].ID
	if err := a.Subscribe(ctx, fa.Site.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.EnqueueNotification(ctx, NotificationInput{EventID: ev.ID, Kind: "OPENED", ChannelID: ch.ID}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	claimed, _ := s.ClaimDueNotifications(ctx, now.Add(time.Minute), 100, time.Nanosecond)
	var notifID int64
	for _, n := range claimed {
		if n.OrgID == a.OrgID() {
			notifID = n.ID
		}
	}
	if notifID == 0 {
		t.Fatal("could not obtain A's notification id")
	}
	user, _ := a.UpsertUser(ctx, "alice", nil, "Alice")
	mem, err := a.SetMembership(ctx, user.ID, RoleOperator, &fa.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetMembership(ctx, user.ID, RoleAdmin, nil); err != nil { // so B's admin attempt would hit the unique index
		t.Fatal(err)
	}
	token, _, _ := a.CreateSubscriptionToken(ctx, fa.Site.ID, time.Hour, now)
	cipher := testCipher(t)

	snapshot := func(o *OrgStore) map[string]any {
		site, _ := o.GetSite(ctx, fa.Site.ID)
		dev, _ := o.GetDevice(ctx, fa.Device.ID)
		cals, _ := o.ListCalibrations(ctx, fa.Device.ID)
		rs, _ := o.ReadingsRange(ctx, fa.Device.ID, base.Add(-time.Hour), base.Add(24*time.Hour), 100)
		rule, _ := o.GetRule(ctx, ruleID)
		evs, _ := o.ListEvents(ctx, ruleID, 10)
		chn, _ := o.GetChannelByChat(ctx, ch.Kind, ch.ChatID)
		subs, _ := o.ListSubscribers(ctx, fa.Site.ID)
		n, _ := o.GetNotification(ctx, notifID)
		ms, _ := o.ListMemberships(ctx, user.ID)
		aud, _ := o.ListAudit(ctx, 1000)
		return map[string]any{"site": site, "dev": dev, "cals": cals, "readings": rs, "rule": rule, "events": evs,
			"channel": chn, "subs": subs, "notif": n, "memberships": ms, "audit": len(aud)}
	}
	before := snapshot(a)

	// --- organization B attacks ------------------------------------------------------------
	notFound := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: succeeded across organizations", name)
		} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, calibration.ErrNoCalibration) {
			t.Errorf("%s: expected not-found, got %v", name, err)
		}
	}
	empty := func(name string, n int, err error) {
		t.Helper()
		if err != nil || n != 0 {
			t.Errorf("%s: returned %d rows (err %v), want none", name, n, err)
		}
	}
	siteB, err := b.CreateSite(ctx, SiteInput{Slug: "b-site", Name: "B", Lat: 1, Lon: 1})
	if err != nil {
		t.Fatal(err)
	}
	devB, _, err := b.RegisterDevice(ctx, cipher, DeviceInput{SiteID: siteB.ID, Key: "b-device", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	chB, _ := b.UpsertChannel(ctx, "telegram", "b-chat")
	ruleB, _ := b.CreateRule(ctx, RuleInput{SiteID: siteB.ID, Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 1, Severity: 1}}, Enabled: true})
	evB, _ := b.OpenEvent(ctx, ruleB.ID, 1, now, 1, nil)
	userB, _ := b.UpsertUser(ctx, "bob", nil, "Bob")
	rowsA := genRows(base.Add(time.Hour), 3, "evil", 0)
	dayRange := func() (time.Time, time.Time) { return base.Add(-time.Hour), base.Add(time.Hour) }
	from, to := dayRange()

	// reads
	_, err = b.GetSite(ctx, fa.Site.ID)
	notFound("GetSite", err)
	sites, err := b.ListSites(ctx)
	if err != nil || len(sites) != 1 || sites[0].ID != siteB.ID {
		t.Errorf("ListSites leaks: %v %v", sites, err)
	}
	_, err = b.GetDevice(ctx, fa.Device.ID)
	notFound("GetDevice", err)
	devs, err := b.ListDevices(ctx, nil)
	if err != nil || len(devs) != 1 || devs[0].ID != devB.ID {
		t.Errorf("ListDevices leaks: %v %v", devs, err)
	}
	devs, err = b.ListDevices(ctx, &fa.Site.ID)
	empty("ListDevices(A's site)", len(devs), err)
	cals, err := b.ListCalibrations(ctx, fa.Device.ID)
	empty("ListCalibrations", len(cals), err)
	_, err = b.ResolveCalibration(ctx, fa.Device.ID, now)
	notFound("ResolveCalibration", err)
	rd, err := b.ReadingsRange(ctx, fa.Device.ID, from, to.Add(24*time.Hour), 100)
	empty("ReadingsRange", len(rd), err)
	_, err = b.LatestReading(ctx, fa.Device.ID)
	notFound("LatestReading", err)
	for _, res := range []Resolution{Res1m, Res1h, Res1d} {
		ag, err := b.Aggregates(ctx, fa.Device.ID, res, from, to.Add(48*time.Hour), 100)
		empty("Aggregates "+string(res), len(ag), err)
	}
	_, err = b.GetRule(ctx, ruleID)
	notFound("GetRule", err)
	rl, err := b.ListRules(ctx, &fa.Site.ID)
	empty("ListRules(A's site)", len(rl), err)
	_, err = b.OpenEventForRule(ctx, ruleID)
	notFound("OpenEventForRule", err)
	el, err := b.ListEvents(ctx, ruleID, 10)
	empty("ListEvents", len(el), err)
	_, err = b.GetChannelByChat(ctx, ch.Kind, ch.ChatID)
	notFound("GetChannelByChat", err)
	sub, err := b.ListSubscribers(ctx, fa.Site.ID)
	empty("ListSubscribers", len(sub), err)
	cs, err := b.ListChannelSites(ctx, ch.ID)
	empty("ListChannelSites", len(cs), err)
	_, err = b.GetNotification(ctx, notifID)
	notFound("GetNotification", err)
	ml, err := b.ListMemberships(ctx, user.ID)
	empty("ListMemberships", len(ml), err)
	audit, err := b.ListAudit(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range audit {
		if e.EntityID == fa.Site.ID.String() || e.EntityID == fa.Device.ID.String() {
			t.Errorf("ListAudit leaks A's entry %s", e.Action)
		}
	}

	// writes
	_, err = b.UpdateSite(ctx, fa.Site.ID, SiteInput{Slug: "hacked", Name: "Hacked", Lat: 0, Lon: 0})
	notFound("UpdateSite", err)
	_, _, err = b.RegisterDevice(ctx, cipher, DeviceInput{SiteID: fa.Site.ID, Key: "b-in-a-site", Name: "x"})
	notFound("RegisterDevice into A's site", err)
	notFound("UpdateDeviceStatus", b.UpdateDeviceStatus(ctx, fa.Device.ID, now, domain.StatusOnline))
	_, err = b.AddCalibration(ctx, fa.Device.ID, domain.Calibration{OffsetCm: 1, ValidFrom: base.Add(24 * time.Hour)}, true)
	notFound("AddCalibration", err)
	n, err := b.InsertReadings(ctx, fa.Device.ID, rowsA)
	if n != 0 {
		t.Errorf("InsertReadings wrote %d rows into A's device", n)
	}
	notFound("InsertReadings", err)
	_, err = b.CreateRule(ctx, RuleInput{SiteID: fa.Site.ID, Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 1, Severity: 1}}})
	notFound("CreateRule on A's site", err)
	_, err = b.UpdateRule(ctx, ruleID, RuleInput{SiteID: fa.Site.ID, Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 1, Severity: 1}}})
	notFound("UpdateRule", err)
	_, err = b.OpenEvent(ctx, ruleID, 1, now, 1, nil)
	notFound("OpenEvent on A's rule", err)
	_, err = b.UpdateEvent(ctx, ev.ID, 3, 999, nil, now)
	notFound("UpdateEvent", err)
	_, err = b.CloseEvent(ctx, ev.ID, now)
	notFound("CloseEvent", err)
	notFound("DeactivateChannel", b.DeactivateChannel(ctx, ch.ID))
	notFound("Subscribe(A site, B channel)", b.Subscribe(ctx, fa.Site.ID, chB.ID))
	notFound("Subscribe(B site, A channel)", b.Subscribe(ctx, siteB.ID, ch.ID))
	notFound("Unsubscribe", b.Unsubscribe(ctx, fa.Site.ID, ch.ID))
	_, _, err = b.CreateSubscriptionToken(ctx, fa.Site.ID, time.Hour, now)
	notFound("CreateSubscriptionToken for A's site", err)
	_, err = b.EnqueueNotification(ctx, NotificationInput{EventID: ev.ID, Kind: "CLOSED", ChannelID: chB.ID})
	notFound("EnqueueNotification for A's event", err)
	_, err = b.EnqueueNotification(ctx, NotificationInput{EventID: evB.ID, Kind: "CLOSED", ChannelID: ch.ID})
	notFound("EnqueueNotification to A's channel", err)
	notFound("MarkNotificationSent", b.MarkNotificationSent(ctx, notifID, now))
	notFound("MarkNotificationRetry", b.MarkNotificationRetry(ctx, notifID, now, "x"))
	notFound("MarkNotificationFailed", b.MarkNotificationFailed(ctx, notifID, "x"))
	_, err = b.SetMembership(ctx, user.ID, RoleAdmin, nil)
	notFound("SetMembership for A's user (A's admin row exists)", err)
	_, err = b.SetMembership(ctx, user.ID, RoleOperator, &fa.Site.ID)
	notFound("SetMembership duplicating A's operator row", err)
	notFound("Subscribe duplicating A's subscription", b.Subscribe(ctx, fa.Site.ID, ch.ID))
	_, err = b.EnqueueNotification(ctx, NotificationInput{EventID: ev.ID, Kind: "OPENED", ChannelID: ch.ID})
	notFound("EnqueueNotification duplicating A's outbox row", err)
	_, err = b.SetMembership(ctx, userB.ID, RoleOperator, &fa.Site.ID)
	notFound("SetMembership onto A's site", err)
	notFound("RemoveMembership", b.RemoveMembership(ctx, mem.ID))

	// --- A is untouched --------------------------------------------------------------------
	after := snapshot(a)
	for k, v := range before {
		if !reflect.DeepEqual(v, after[k]) {
			t.Errorf("organization A changed (%s):\n before %+v\n after  %+v", k, v, after[k])
		}
	}
	if n := countRows(t, `SELECT count(*) FROM readings WHERE device_id = $1`, fa.Device.ID); n != 10 {
		t.Errorf("A's device has %d readings, want 10", n)
	}

	// Bootstrap lookups work by design, and stay usable only for the owner of the secret.
	if _, _, err := s.RedeemSubscriptionToken(ctx, token, now.Add(time.Second)); err != nil {
		t.Errorf("legitimate redeem failed: %v", err)
	}
	if got, err := s.DeviceAuthByKey(ctx, fa.Device.Key); err != nil || got.OrgID != a.OrgID() {
		t.Errorf("device auth resolved to the wrong organization: %v %v", got.OrgID, err)
	}
}
