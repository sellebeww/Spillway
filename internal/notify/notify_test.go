package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/store"
	"github.com/sellebeww/spillway/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

const token = "123456:SECRET-TOKEN-VALUE"

type sentMsg struct {
	chat, text string
	at         time.Time
}

// fakeTG is a stand-in for the Telegram Bot API.
type fakeTG struct {
	mu       sync.Mutex
	sent     []sentMsg
	attempts map[string]int
	script   func(chat string, attempt int) (code, retryAfter int) // 0 means success
	updates  [][]Update
	offsets  []int64
	srv      *httptest.Server
}

func newFakeTG(t *testing.T) *fakeTG {
	f := &fakeTG{attempts: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTG) client() *Client { return &Client{Token: token, BaseURL: f.srv.URL} }

func (f *fakeTG) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "application/json")
	switch strings.TrimPrefix(r.URL.Path, "/bot"+token+"/") {
	case "sendMessage":
		chat, _ := body["chat_id"].(string)
		text, _ := body["text"].(string)
		f.mu.Lock()
		f.attempts[chat]++
		attempt := f.attempts[chat]
		code, retry := 0, 0
		if f.script != nil {
			code, retry = f.script(chat, attempt)
		}
		if code == 0 {
			f.sent = append(f.sent, sentMsg{chat, text, time.Now()})
		}
		f.mu.Unlock()
		if code != 0 {
			_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":%d,"description":"scripted failure","parameters":{"retry_after":%d}}`, code, retry)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	case "getUpdates":
		f.mu.Lock()
		off, _ := body["offset"].(float64)
		f.offsets = append(f.offsets, int64(off))
		var batch []Update
		if len(f.updates) > 0 {
			batch, f.updates = f.updates[0], f.updates[1:]
		}
		f.mu.Unlock()
		if len(batch) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
		out, _ := json.Marshal(batch)
		if batch == nil {
			out = []byte("[]")
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, out)
	}
}

func (f *fakeTG) messages(chat string) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMsg
	for _, m := range f.sent {
		if m.chat == chat {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeTG) total() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClientErrorsAndTokenScrubbing(t *testing.T) {
	f := newFakeTG(t)
	f.script = func(chat string, _ int) (int, int) {
		switch chat {
		case "limited":
			return 429, 7
		case "blocked":
			return 403, 0
		}
		return 0, 0
	}
	c := f.client()
	var api *APIError
	if err := c.SendMessage(t.Context(), "limited", "x"); !errors.As(err, &api) || api.Code != 429 || api.RetryAfter != 7*time.Second {
		t.Fatalf("429: %v", err)
	}
	if err := c.SendMessage(t.Context(), "blocked", "x"); !errors.As(err, &api) || api.Code != 403 {
		t.Fatalf("403: %v", err)
	}
	if err := c.SendMessage(t.Context(), "ok", "x"); err != nil {
		t.Fatal(err)
	}
	f.srv.Close()
	err := c.SendMessage(t.Context(), "ok", "x")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN-VALUE") || strings.Contains(err.Error(), token) {
		t.Fatalf("a network error must not leak the bot token: %v", err)
	}
}

func TestRenderAlertContent(t *testing.T) {
	rate := 45.04
	p := Payload{Kind: "ESCALATED", Severity: 2, Previous: 1, SiteName: "Kali <b>&Contoh", SiteSlug: "kali-contoh", SitePublic: true,
		TZ: "Asia/Jakarta", LevelCm: 112.46, RateCmPerHour: &rate, TS: time.Date(2026, 11, 16, 4, 5, 0, 0, time.UTC).Unix()}
	got := RenderAlert(p, "https://spillway.example/")
	for _, want := range []string{"NAIK ke <b>SIAGA</b>", "sebelumnya WASPADA", "Kali &lt;b&gt;&amp;Contoh", "112,5 cm", "45,0 cm/jam",
		"16-11-2026 11:05 WIB", "https://spillway.example/s/kali-contoh", "BUKAN sistem peringatan dini resmi"} {
		if !strings.Contains(got, want) {
			t.Errorf("message lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<b>&Contoh") {
		t.Error("site name was not HTML-escaped")
	}
	p.SitePublic, p.RateCmPerHour = false, nil
	if got = RenderAlert(p, "https://spillway.example"); strings.Contains(got, "https://") || strings.Contains(got, "cm/jam") {
		t.Errorf("a private site has no link and no rate line:\n%s", got)
	}
	for _, kind := range []string{"OPENED", "DEESCALATED", "CLOSED", "REMINDER"} {
		p.Kind = kind
		if m := RenderAlert(p, ""); !strings.Contains(m, SafetyLine) || len(m) < 60 {
			t.Errorf("%s: %q", kind, m)
		}
	}
}

// Fail-visible: an alert that sat in the outbox (Telegram down, 429) and is delivered long after its
// reading must say so prominently instead of reading like a live report.
func TestDelayNoteMarksLateDeliveries(t *testing.T) {
	read := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	p := Payload{Kind: "OPENED", Severity: 1, SiteName: "Kali", TZ: "Asia/Jakarta", LevelCm: 100, TS: read.Unix()}
	if n := DelayNote(p, read.Add(5*time.Minute)); n != "" {
		t.Fatalf("5 minutes is within the freshness window, got %q", n)
	}
	n := DelayNote(p, read.Add(47*time.Minute))
	if !strings.Contains(n, "DATA TERTUNDA") || !strings.Contains(n, "47 menit") {
		t.Fatalf("a 47 minute old alert must be marked, got %q", n)
	}
	if n := DelayNote(p, read.Add(-time.Hour)); n != "" {
		t.Fatalf("a reading from the future (clock skew) is not a delay, got %q", n)
	}
}

type tenant struct {
	o        *store.OrgStore
	st       *store.Store
	site     store.Site
	dev      store.Device
	rule     store.AlertRule
	channels map[string]store.Channel
}

func newTenant(t *testing.T, chats ...string) *tenant {
	t.Helper()
	db := testutil.SharedDB(t)
	// docs/SPEC.md 6.10: no leaked goroutines. Whatever the test starts (bot polling, sender workers) must be gone
	// at its end; the shared pool and containers predate this snapshot.
	noLeaks := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, noLeaks) })
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	org, err := db.Store.CreateOrg(ctx, "n-"+tag, "Org")
	if err != nil {
		t.Fatal(err)
	}
	o := db.Store.ForOrg(org.ID)
	site, err := o.CreateSite(ctx, store.SiteInput{Slug: "kali-" + tag, Name: "Kali Contoh", Lat: -6, Lon: 106, IsPublic: true})
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := auth.NewCipher(1, make([]byte, 32), nil)
	dev, _, err := o.RegisterDevice(ctx, cipher, store.DeviceInput{SiteID: site.ID, Key: "d-" + tag, Name: "Sensor 1"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := o.CreateRule(ctx, store.RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Enabled: true,
		Tiers: []domain.Tier{{Value: 100, Severity: 1}, {Value: 150, Severity: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	tn := &tenant{o: o, st: db.Store, site: site, dev: dev, rule: rule, channels: map[string]store.Channel{}}
	for _, c := range chats {
		ch, err := o.UpsertChannel(ctx, "telegram", c)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Subscribe(ctx, site.ID, ch.ID); err != nil {
			t.Fatal(err)
		}
		tn.channels[c] = ch
	}
	return tn
}

func (tn *tenant) decide(t *testing.T, kind string, sev int, at time.Time) store.DecisionResult {
	t.Helper()
	p, _ := json.Marshal(Payload{Kind: kind, Severity: sev, SiteName: tn.site.Name, SiteSlug: tn.site.Slug, SitePublic: true, TZ: "Asia/Jakarta", LevelCm: 120, TS: at.Unix()})
	res, err := tn.o.ApplyDecision(context.Background(), store.DecisionInput{RuleID: tn.rule.ID, DeviceID: tn.dev.ID, Kind: kind,
		Severity: domain.Severity(sev), At: at, LevelCm: 120, Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (tn *tenant) outbox(t *testing.T, status string) int {
	t.Helper()
	var n int
	err := testutil.SharedDB(t).Pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications_outbox WHERE org_id = $1 AND status = $2`, tn.o.OrgID(), status).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBotSubscribeStatusStopFlow(t *testing.T) {
	tn := newTenant(t)
	f := newFakeTG(t)
	now := time.Now()
	bot := &Bot{TG: f.client(), St: tn.st, Now: func() time.Time { return now }}
	ctx := context.Background()
	say := func(text string) string {
		t.Helper()
		before := len(f.messages("777"))
		var m Message
		m.Text, m.Chat.ID = text, 777
		if err := bot.HandleUpdate(ctx, Update{UpdateID: 1, Message: &m}); err != nil {
			t.Fatal(err)
		}
		msgs := f.messages("777")
		if len(msgs) != before+1 {
			t.Fatalf("expected one reply to %q, got %d", text, len(msgs)-before)
		}
		return msgs[len(msgs)-1].text
	}

	if got := say("/start"); !strings.Contains(got, "/start &lt;token&gt;") {
		t.Fatalf("help: %s", got)
	}
	if got := say("/status"); !strings.Contains(got, "belum berlangganan") {
		t.Fatalf("status before subscribing: %s", got)
	}
	tok, _, err := tn.o.CreateSubscriptionToken(ctx, tn.site.ID, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := say("/start " + tok); !strings.Contains(got, "Kali Contoh") || !strings.Contains(got, SafetyLine) {
		t.Fatalf("subscribe: %s", got)
	}
	if subs, _ := tn.o.ListSubscribers(ctx, tn.site.ID); len(subs) != 1 || subs[0].ChatID != "777" {
		t.Fatalf("subscribers: %+v", subs)
	}
	if got := say("/start " + tok); !strings.Contains(got, "tidak valid") {
		t.Fatalf("a deep link is single use: %s", got)
	}
	if got := say("/start bogus-token"); !strings.Contains(got, "tidak valid") {
		t.Fatalf("bogus token: %s", got)
	}

	if got := say("/status"); !strings.Contains(got, "belum ada data") || !strings.Contains(got, "DATA TERTUNDA") {
		t.Fatalf("no data yet must read as stale: %s", got)
	}
	row := func(recv time.Time, level float64) []store.ReadingRow {
		return []store.ReadingRow{{TS: recv, ReceivedAt: recv, SID: "s", FC: uint32(recv.Unix() % 100000), RawMM: 3000, LevelCm: level, //nolint:gosec // test value
			Flags: []domain.QualityFlag{domain.FlagOK}, Score: 1}}
	}
	if _, err := tn.o.InsertReadings(ctx, tn.dev.ID, row(now.Add(-2*time.Hour), 88.5)); err != nil {
		t.Fatal(err)
	}
	if got := say("/status"); !strings.Contains(got, "DATA TERTUNDA") || !strings.Contains(got, "88,5 cm") {
		t.Fatalf("a 2 hour old reading must be flagged: %s", got)
	}
	if _, err := tn.o.InsertReadings(ctx, tn.dev.ID, row(now.Add(-30*time.Second), 91.2)); err != nil {
		t.Fatal(err)
	}
	if got := say("/status"); strings.Contains(got, "DATA TERTUNDA") || !strings.Contains(got, "91,2 cm") || !strings.Contains(got, "Kali Contoh") {
		t.Fatalf("fresh status: %s", got)
	}

	if got := say("/stop"); !strings.Contains(got, "tidak lagi menerima") {
		t.Fatalf("stop: %s", got)
	}
	if subs, _ := tn.o.ListSubscribers(ctx, tn.site.ID); len(subs) != 0 {
		t.Fatal("stopped chats must receive nothing")
	}
	if ch, _ := tn.o.GetChannelByChat(ctx, "telegram", "777"); ch.IsActive {
		t.Fatal("channel must be deactivated")
	}
	before := f.total()
	if err := bot.HandleUpdate(ctx, Update{Message: &Message{Text: "halo semua"}}); err != nil || f.total() != before {
		t.Fatal("chatter must be ignored")
	}
}

func TestBotLongPollingPersistsItsOffset(t *testing.T) {
	tn := newTenant(t)
	f := newFakeTG(t)
	var m Message
	m.Text = "/status"
	m.Chat.ID = 4242
	f.updates = [][]Update{{{UpdateID: 10, Message: &m}, {UpdateID: 11, Message: &m}}}
	name := "test-" + uuid.NewString()[:8]
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
		defer cancel()
		_ = (&Bot{TG: f.client(), St: tn.st, Name: name, PollSeconds: 1}).Run(ctx)
	}
	run()
	if got := len(f.messages("4242")); got != 2 {
		t.Fatalf("both commands answered once, got %d", got)
	}
	if off, _ := tn.st.BotOffset(context.Background(), name); off != 12 {
		t.Fatalf("persisted offset %d, want 12", off)
	}
	f.mu.Lock()
	f.offsets = nil
	f.mu.Unlock()
	run() // restart
	f.mu.Lock()
	first := f.offsets[0]
	f.mu.Unlock()
	if first != 12 || len(f.messages("4242")) != 2 {
		t.Fatalf("after a restart the first poll must resume at offset 12 (was %d) without replaying commands", first)
	}
}

func newSender(tn *tenant, f *fakeTG) *Sender {
	return &Sender{TG: f.client(), St: tn.st, PublicURL: "https://spillway.example", ChatEvery: 20 * time.Millisecond,
		GlobalRate: time.Millisecond, Poll: 20 * time.Millisecond, Lease: 30 * time.Second}
}

func runSender(s *Sender, workers int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx, workers) }()
	return func() { cancel(); <-done }
}

func TestSenderDeliversEachNotificationExactlyOnce(t *testing.T) {
	tn := newTenant(t, "chat-a", "chat-b")
	f := newFakeTG(t)
	now := time.Now()
	if res := tn.decide(t, "OPENED", 1, now); !res.Applied || res.Enqueued != 2 {
		t.Fatalf("one outbox row per subscribed channel: %+v", res)
	}
	if res := tn.decide(t, "OPENED", 1, now); res.Applied || res.Enqueued != 0 {
		t.Fatalf("re-applying the same decision must not enqueue again: %+v", res)
	}
	stop := runSender(newSender(tn, f), 4) // four workers race for the same rows
	eventually(t, 10*time.Second, "both messages", func() bool { return f.total() == 2 })
	time.Sleep(300 * time.Millisecond)
	stop()
	for _, chat := range []string{"chat-a", "chat-b"} {
		ms := f.messages(chat)
		if len(ms) != 1 || !strings.Contains(ms[0].text, "PERINGATAN <b>WASPADA</b>") || !strings.Contains(ms[0].text, SafetyLine) ||
			!strings.Contains(ms[0].text, "https://spillway.example/s/") {
			t.Fatalf("%s: %+v", chat, ms)
		}
	}
	if tn.outbox(t, "SENT") != 2 || tn.outbox(t, "PENDING") != 0 {
		t.Fatal("rows must be SENT")
	}

	// A restart after delivery sends nothing again.
	stop = runSender(newSender(tn, f), 2)
	time.Sleep(300 * time.Millisecond)
	stop()
	if f.total() != 2 {
		t.Fatalf("a restart re-sent delivered notifications: %d", f.total())
	}

	// A worker that claimed rows and died before sending: the rows return after the lease, once.
	tn.decide(t, "REMINDER", 1, now.Add(31*time.Minute))
	crashed, err := tn.st.ClaimDueNotifications(context.Background(), time.Now().Add(time.Second), 10, 700*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	mine := 0
	for _, c := range crashed {
		if c.OrgID == tn.o.OrgID() {
			mine++
		}
	}
	if mine != 2 {
		t.Fatalf("simulated crashed worker claimed %d rows", mine)
	}
	s := newSender(tn, f)
	s.Now = func() time.Time { return time.Now().Add(time.Second) } // the sender's clock runs a second ahead of the claim
	stop = runSender(s, 3)
	time.Sleep(200 * time.Millisecond)
	if f.total() != 2 {
		t.Fatalf("rows under a live lease must not be sent: %d", f.total())
	}
	eventually(t, 10*time.Second, "reminders after the lease expires", func() bool { return f.total() == 4 })
	time.Sleep(300 * time.Millisecond)
	stop()
	if f.total() != 4 || len(f.messages("chat-a")) != 2 {
		t.Fatalf("each reminder exactly once: %d", f.total())
	}
}

func TestSenderDeactivatesBlockedChannelsAndRetriesRateLimits(t *testing.T) {
	tn := newTenant(t, "slow-chat", "blocked-chat", "fine-chat")
	f := newFakeTG(t)
	f.script = func(chat string, attempt int) (int, int) {
		switch {
		case chat == "slow-chat" && attempt == 1:
			return 429, 1
		case chat == "blocked-chat":
			return 403, 0
		}
		return 0, 0
	}
	tn.decide(t, "OPENED", 2, time.Now())
	stop := runSender(newSender(tn, f), 2)
	eventually(t, 20*time.Second, "all outcomes", func() bool {
		return len(f.messages("slow-chat")) == 1 && len(f.messages("fine-chat")) == 1 && tn.outbox(t, "PENDING") == 0
	})
	stop()

	f.mu.Lock()
	slowAttempts, blockedAttempts := f.attempts["slow-chat"], f.attempts["blocked-chat"]
	f.mu.Unlock()
	if slowAttempts != 2 {
		t.Fatalf("the 429 chat should be tried twice (once refused, once delivered), got %d", slowAttempts)
	}
	if blockedAttempts != 1 {
		t.Fatalf("a 403 must not be retried, got %d attempts", blockedAttempts)
	}
	if ch, _ := tn.o.GetChannelByChat(context.Background(), "telegram", "blocked-chat"); ch.IsActive {
		t.Fatal("a 403 (bot blocked) must deactivate the channel")
	}
	if tn.outbox(t, "FAILED") != 1 || tn.outbox(t, "SENT") != 2 {
		t.Fatalf("outbox: failed=%d sent=%d", tn.outbox(t, "FAILED"), tn.outbox(t, "SENT"))
	}
	if res := tn.decide(t, "DEESCALATED", 1, time.Now()); res.Enqueued != 2 {
		t.Fatalf("only active channels are scheduled: %+v", res)
	}
}

func TestSenderRetryAfterIsRespected(t *testing.T) {
	tn := newTenant(t, "limited")
	f := newFakeTG(t)
	var mu sync.Mutex
	var firstAt, secondAt time.Time
	f.script = func(_ string, attempt int) (int, int) {
		mu.Lock()
		defer mu.Unlock()
		if attempt == 1 {
			firstAt = time.Now()
			return 429, 2
		}
		secondAt = time.Now()
		return 0, 0
	}
	tn.decide(t, "OPENED", 1, time.Now())
	stop := runSender(newSender(tn, f), 1)
	eventually(t, 20*time.Second, "delivery after the 429", func() bool { return f.total() == 1 })
	stop()
	mu.Lock()
	defer mu.Unlock()
	if gap := secondAt.Sub(firstAt); gap < 2*time.Second {
		t.Fatalf("retry came after %v, Telegram asked for 2 s", gap)
	}
}

func TestSenderPacesMessagesPerChat(t *testing.T) {
	tn := newTenant(t, "paced")
	f := newFakeTG(t)
	now := time.Now()
	tn.decide(t, "OPENED", 1, now)
	tn.decide(t, "ESCALATED", 2, now.Add(time.Minute))
	tn.decide(t, "DEESCALATED", 1, now.Add(2*time.Minute))
	s := newSender(tn, f)
	s.ChatEvery = 200 * time.Millisecond
	stop := runSender(s, 3)
	eventually(t, 10*time.Second, "three messages", func() bool { return f.total() == 3 })
	stop()
	ms := f.messages("paced")
	for i := 1; i < len(ms); i++ {
		if gap := ms[i].at.Sub(ms[i-1].at); gap < 180*time.Millisecond {
			t.Fatalf("messages %d and %d only %v apart; the per-chat limit is 200 ms here", i-1, i, gap)
		}
	}
}

func TestApplyDecisionIsAtomic(t *testing.T) {
	tn := newTenant(t, "chat-x")
	ctx := context.Background()
	_, err := tn.o.ApplyDecision(ctx, store.DecisionInput{RuleID: tn.rule.ID, DeviceID: tn.dev.ID, Kind: "BOGUS", Severity: 1, At: time.Now()})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := tn.o.OpenEventForDevice(ctx, tn.rule.ID, tn.dev.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused decision left an alert event behind")
	}
	res := tn.decide(t, "OPENED", 1, time.Now())
	ev, err := tn.o.OpenEventForDevice(ctx, tn.rule.ID, tn.dev.ID)
	if err != nil || ev.ID != res.EventID || res.Enqueued != 1 || tn.outbox(t, "PENDING") != 1 {
		t.Fatalf("alert and notification must exist together: %+v %v", res, err)
	}
	tn.decide(t, "REMINDER", 1, time.Now().Add(time.Hour))
	tn.decide(t, "REMINDER", 1, time.Now().Add(2*time.Hour))
	if ev, _ = tn.o.OpenEventForDevice(ctx, tn.rule.ID, tn.dev.ID); ev.ReminderCount != 2 || tn.outbox(t, "PENDING") != 3 {
		t.Fatalf("each reminder is its own notification: count=%d pending=%d", ev.ReminderCount, tn.outbox(t, "PENDING"))
	}
	tn.decide(t, "CLOSED", 0, time.Now().Add(3*time.Hour))
	if _, err := tn.o.OpenEventForDevice(ctx, tn.rule.ID, tn.dev.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("closed")
	}
	if res := tn.decide(t, "CLOSED", 0, time.Now()); res.Applied || res.Enqueued != 0 {
		t.Fatalf("closing twice is a no-op: %+v", res)
	}
}

func TestRevokeSubscriptionTokens(t *testing.T) {
	tn := newTenant(t)
	ctx := context.Background()
	tok, _, _ := tn.o.CreateSubscriptionToken(ctx, tn.site.ID, time.Hour, time.Now())
	if n, err := tn.o.RevokeSubscriptionTokens(ctx, tn.site.ID); err != nil || n != 1 {
		t.Fatalf("revoked %d: %v", n, err)
	}
	if _, _, err := tn.st.RedeemSubscriptionToken(ctx, tok, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a revoked token must not redeem: %v", err)
	}
}
