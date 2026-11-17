// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package simulate generates signed telemetry for virtual devices (sensors) and sends it over
// HTTP or MQTT. It is used by cmd/simulator and by integration tests; payloads are built with
// the same protocol package the server verifies with.
package simulate

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/sellebeww/spillway/internal/ingest/protocol"
)

// Device is one virtual sensor.
type Device struct {
	Key      string  `json:"device"`
	SecretHx string  `json:"secret"`    // 64 hex characters, as printed by `spillway seed-dev`
	OffsetCm float64 `json:"offset_cm"` // sensor head elevation above datum; default 500
	secret   []byte
}

// LoadDevices reads a JSON array of devices.
func LoadDevices(path string) ([]Device, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read devices file: %w", err)
	}
	var ds []Device
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("parse devices file: %w", err)
	}
	for i := range ds {
		if err := ds[i].prepare(); err != nil {
			return nil, fmt.Errorf("device %q: %w", ds[i].Key, err)
		}
	}
	return ds, nil
}

func (d *Device) prepare() error {
	s, err := hex.DecodeString(d.SecretHx)
	if err != nil || len(s) != 32 {
		return errors.New("secret must be 64 hex characters")
	}
	d.secret = s
	if d.OffsetCm == 0 {
		d.OffsetCm = 500
	}
	return nil
}

// NewDevice builds a device from a key and hex secret.
func NewDevice(key, secretHex string, offsetCm float64) (Device, error) {
	d := Device{Key: key, SecretHx: secretHex, OffsetCm: offsetCm}
	return d, d.prepare()
}

// Scenario names a water level pattern.
type Scenario string

// Scenarios.
const (
	Steady    Scenario = "steady"
	FloodWave Scenario = "flood-wave"
	// SpikeNoise is a calm river with bursts of two absurd readings every 17 rounds (a sensor
	// picking up echoes): quality scoring must keep them from raising alerts.
	SpikeNoise Scenario = "spike-noise"
	// Flatline freezes the sensor on one exact value after a fifth of the run.
	Flatline Scenario = "flatline"
	// Dropout stops sending for a fifth of the run (the gap shows as a frame counter jump).
	Dropout Scenario = "dropout"
	// LateBurst delivers a stretch of readings ten minutes late, as a buffering gateway would.
	LateBurst Scenario = "late-burst"
)

// ValidScenario reports whether name is a known scenario.
func ValidScenario(name string) bool {
	switch Scenario(name) {
	case Steady, FloodWave, SpikeNoise, Flatline, Dropout, LateBurst:
		return true
	}
	return false
}

// Skip reports whether the device sends nothing at this tick (Dropout).
func Skip(s Scenario, tick, total int) bool {
	return s == Dropout && total > 0 && tick >= total*2/5 && tick < total*3/5
}

// Age is how old the timestamp of this tick's message is (LateBurst).
func Age(s Scenario, tick, total int) time.Duration {
	if s == LateBurst && total > 0 && tick >= total/2 && tick < total*7/10 {
		return 10 * time.Minute
	}
	return 0
}

// Level returns the water level (cm above datum) of device index idx at tick of total ticks.
// steady: a calm level with small deterministic ripples. flood-wave: calm, a smooth rise to a
// peak, a plateau, then a slower recession back to the base level.
func Level(s Scenario, idx, tick, total int) float64 {
	base := 60 + 5*float64(idx%7)
	ripple := 0.4 * math.Sin(float64(tick)/3+float64(idx))
	switch s {
	case SpikeNoise:
		if m := tick % 17; m == 5 || m == 6 {
			return base + 150
		}
		return base + ripple
	case Flatline:
		if total > 0 && tick >= total/5 {
			return Level(Steady, idx, total/5, total)
		}
		return base + ripple
	}
	if s != FloodWave || total <= 0 {
		return base + ripple
	}
	peak := base + 140
	x := float64(tick) / float64(total)
	smooth := func(t float64) float64 { t = math.Max(0, math.Min(1, t)); return t * t * (3 - 2*t) }
	switch {
	case x < 0.2:
		return base + ripple
	case x < 0.5:
		return base + (peak-base)*smooth((x-0.2)/0.3) + ripple
	case x < 0.6:
		return peak + ripple
	default:
		return peak - (peak-base)*smooth((x-0.6)/0.4) + ripple
	}
}

// Payload builds a signed payload for a level. The raw distance is what the sensor would see.
func (d Device) Payload(sid string, fc uint32, ts time.Time, levelCm float64, battV *float64) protocol.Payload {
	raw := int(math.Round((d.OffsetCm - levelCm) * 10))
	raw = max(0, raw)
	p := protocol.Payload{Device: d.Key, SID: sid, FC: fc, TS: ts.Unix(), RawMM: raw}
	if battV != nil {
		v := math.Round(*battV*100) / 100 // at most 2 decimals, as the protocol requires
		p.BattV = &v
	}
	p.Sig = protocol.Sign(d.secret, p)
	return p
}

type wire struct {
	Device string   `json:"device"`
	SID    string   `json:"sid"`
	FC     uint32   `json:"fc"`
	TS     int64    `json:"ts"`
	RawMM  int      `json:"raw_mm"`
	BattV  *float64 `json:"batt_v,omitempty"`
	Sig    string   `json:"sig"`
}

// Body encodes a payload as the JSON that goes on the wire.
func Body(p protocol.Payload) []byte {
	b, _ := json.Marshal(wire{p.Device, p.SID, p.FC, p.TS, p.RawMM, p.BattV, p.Sig}) //nolint:errchkjson // plain struct, cannot fail
	return b
}

// Sender delivers one message. deviceKey selects the MQTT topic.
type Sender interface {
	Send(ctx context.Context, deviceKey string, body []byte) error
}

// HTTPSender posts to /v1/ingest.
type HTTPSender struct {
	URL    string // full URL of the ingest endpoint
	Client *http.Client
}

// Send posts the body and treats any non-200 answer as an error.
func (h HTTPSender) Send(ctx context.Context, _ string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c := h.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return nil
}

// MQTTSender publishes QoS 1 to spillway/v1/{device}/telemetry and waits for the broker's PUBACK.
type MQTTSender struct {
	cm *autopaho.ConnectionManager
}

// NewMQTTSender connects to the broker (with auto reconnect).
func NewMQTTSender(ctx context.Context, brokerURL, username, password string) (*MQTTSender, error) {
	u, err := url.Parse(brokerURL)
	if err != nil {
		return nil, fmt.Errorf("parse broker URL: %w", err)
	}
	id := make([]byte, 6)
	_, _ = crand.Read(id)
	cm, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls: []*url.URL{u}, KeepAlive: 30, ConnectUsername: username, ConnectPassword: []byte(password),
		CleanStartOnInitialConnection: true, ConnectTimeout: 10 * time.Second,
		ClientConfig: paho.ClientConfig{ClientID: "sim-" + hex.EncodeToString(id)},
	})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := cm.AwaitConnection(ctx); err != nil {
		return nil, fmt.Errorf("await connection: %w", err)
	}
	return &MQTTSender{cm: cm}, nil
}

// Send publishes one message.
func (m *MQTTSender) Send(ctx context.Context, deviceKey string, body []byte) error {
	_, err := m.cm.Publish(ctx, &paho.Publish{Topic: "spillway/v1/" + deviceKey + "/telemetry", QoS: 1, Payload: body})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

// Close disconnects.
func (m *MQTTSender) Close(ctx context.Context) error {
	err := m.cm.Disconnect(ctx)
	<-m.cm.Done()
	return err
}

// Config describes a run: every Interval each device sends one reading, for Ticks ticks.
type Config struct {
	Devices     []Device
	Scenario    Scenario
	Interval    time.Duration
	Ticks       int
	Concurrency int              // simultaneous sends, default 32
	Now         func() time.Time // timestamp source, default time.Now
	// Fault injection, each a probability in [0,1] applied per message:
	DuplicateRate  float64 // the identical message is sent a second time
	LateRate       float64 // the timestamp is 3 to 120 minutes old (accepted, flagged late)
	OutOfOrderRate float64 // the message is held back and sent after the device's next one
	Seed           uint64  // fault injection seed; 0 means random
}

// Report summarises a run. Sent counts successful transmissions including duplicate copies;
// UniqueConfirmed counts distinct messages with at least one successful transmission, which is
// the number of rows the server must have stored (messages older than 24 h excepted: none are made).
type Report struct {
	Sent, Errors     int64 // successful and failed transmissions
	Unique           int64 // distinct messages generated
	UniqueConfirmed  int64 // distinct messages confirmed at least once
	DuplicatesSent   int64 // extra copies attempted
	Late, OutOfOrder int64 // distinct messages that were made late or reordered
	Elapsed          time.Duration
	FirstError       error
	Latency          Latency // per transmission, as seen by the sender (HTTP response, MQTT PUBACK)
}

// Latency holds percentiles of the sender round trip, in milliseconds.
type Latency struct{ P50, P95, P99, Max float64 }

// Rate is the achieved transmission rate in messages per second.
func (r Report) Rate() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Sent) / r.Elapsed.Seconds()
}

// JSON is the machine-readable final report.
func (r Report) JSON() ([]byte, error) {
	first := ""
	if r.FirstError != nil {
		first = r.FirstError.Error()
	}
	return json.MarshalIndent(struct {
		Attempted       int64   `json:"attempted"`
		Confirmed       int64   `json:"confirmed"`
		Errors          int64   `json:"errors"`
		Unique          int64   `json:"unique"`
		UniqueConfirmed int64   `json:"unique_confirmed"`
		DuplicatesSent  int64   `json:"duplicates_sent"`
		Late            int64   `json:"late"`
		OutOfOrder      int64   `json:"out_of_order"`
		ElapsedS        float64 `json:"elapsed_s"`
		RateMsgPerS     float64 `json:"rate_msg_per_s"`
		Latency         Latency `json:"sender_latency_ms"`
		FirstError      string  `json:"first_error,omitempty"`
	}{r.Sent + r.Errors, r.Sent, r.Errors, r.Unique, r.UniqueConfirmed, r.DuplicatesSent, r.Late, r.OutOfOrder,
		r.Elapsed.Seconds(), r.Rate(), r.Latency, first}, "", "  ")
}

// MarshalJSON keeps the field names stable in the report.
func (l Latency) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]float64{"p50": l.P50, "p95": l.P95, "p99": l.P99, "max": l.Max})
}

// hist is a lock-free log-scale latency histogram (1.2x buckets from 50 microseconds).
type hist struct{ b [96]atomic.Int64 }

func bucketUpper(i int) float64 { return 0.05 * math.Pow(1.2, float64(i+1)) } // milliseconds

func (h *hist) add(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	i := 0
	if ms > 0.05 {
		i = min(len(h.b)-1, int(math.Log(ms/0.05)/math.Log(1.2)))
	}
	h.b[i].Add(1)
}

func (h *hist) latency() Latency {
	var counts [96]int64
	var total int64
	last := 0
	for i := range h.b {
		counts[i] = h.b[i].Load()
		total += counts[i]
		if counts[i] > 0 {
			last = i
		}
	}
	q := func(p float64) float64 {
		if total == 0 {
			return 0
		}
		rank := int64(math.Ceil(p * float64(total)))
		var acc int64
		for i, c := range counts {
			if acc += c; acc >= rank {
				return bucketUpper(i)
			}
		}
		return bucketUpper(last)
	}
	return Latency{P50: q(0.50), P95: q(0.95), P99: q(0.99), Max: bucketUpper(last)}
}

// job is one distinct message and how many copies of it go on the wire.
type job struct {
	device string
	body   []byte
	copies int
}

// Run sends Ticks rounds of readings and reports. A zero Interval sends as fast as possible.
func Run(ctx context.Context, cfg Config, s Sender) Report {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 32
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = rand.Uint64() //nolint:gosec // fault injection, not security
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // fault injection, not security
	sids := make([]string, len(cfg.Devices))
	for i := range sids {
		b := make([]byte, 4)
		_, _ = crand.Read(b)
		sids[i] = "sim" + hex.EncodeToString(b)
	}
	var sent, errs, unique, confirmed, dups, late, ooo atomic.Int64
	var lat hist
	var firstErr error
	var errOnce sync.Once
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	var tick *time.Ticker
	if cfg.Interval > 0 {
		tick = time.NewTicker(cfg.Interval)
		defer tick.Stop()
	}
	// dispatch sends the jobs in order on one goroutine, so a device's reordering is deterministic.
	dispatch := func(jobs ...job) bool {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return false
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			for _, j := range jobs {
				ok := false
				for c := 0; c < j.copies; c++ {
					t0 := time.Now()
					err := s.Send(ctx, j.device, j.body)
					lat.add(time.Since(t0))
					if err != nil {
						errs.Add(1)
						errOnce.Do(func() { firstErr = err })
						continue
					}
					sent.Add(1)
					ok = true
				}
				if ok {
					confirmed.Add(1)
				}
			}
		}()
		return true
	}
	held := make([]*job, len(cfg.Devices))
rounds:
	for t := 0; t < cfg.Ticks; t++ {
		if tick != nil && t > 0 {
			select {
			case <-tick.C:
			case <-ctx.Done():
				break rounds
			}
		}
		for i := range cfg.Devices {
			d := cfg.Devices[i]
			batt := 3.9 - 0.4*float64(t)/float64(max(cfg.Ticks, 1))
			if Skip(cfg.Scenario, t, cfg.Ticks) {
				continue
			}
			age := Age(cfg.Scenario, t, cfg.Ticks)
			if cfg.LateRate > 0 && rng.Float64() < cfg.LateRate {
				age = 3*time.Minute + time.Duration(rng.Int64N(int64(117*time.Minute)))
				late.Add(1)
			}
			p := d.Payload(sids[i], uint32(t+1), cfg.Now().Add(-age), Level(cfg.Scenario, i, t, cfg.Ticks), &batt) //nolint:gosec // tick count is small
			j := job{device: d.Key, body: Body(p), copies: 1}
			if cfg.DuplicateRate > 0 && rng.Float64() < cfg.DuplicateRate {
				j.copies = 2
				dups.Add(1)
			}
			unique.Add(1)
			if held[i] == nil && cfg.OutOfOrderRate > 0 && rng.Float64() < cfg.OutOfOrderRate {
				held[i] = &j // goes out after this device's next message
				ooo.Add(1)
				continue
			}
			batch := []job{j}
			if held[i] != nil {
				batch = append(batch, *held[i])
				held[i] = nil
			}
			if !dispatch(batch...) {
				break rounds
			}
		}
	}
	for i := range held { // a message still held at the end goes out last
		if held[i] != nil && ctx.Err() == nil {
			dispatch(*held[i])
		}
	}
	wg.Wait()
	return Report{Sent: sent.Load(), Errors: errs.Load(), Unique: unique.Load(), UniqueConfirmed: confirmed.Load(), DuplicatesSent: dups.Load(),
		Late: late.Load(), OutOfOrder: ooo.Load(), Elapsed: time.Since(start), FirstError: firstErr, Latency: lat.latency()}
}

// MQTTPool spreads devices over several broker connections, because one connection carries only
// a bounded number of unacknowledged QoS 1 messages. A device always uses the same connection.
type MQTTPool []*MQTTSender

// NewMQTTPool opens n connections.
func NewMQTTPool(ctx context.Context, brokerURL, username, password string, n int) (MQTTPool, error) {
	pool := make(MQTTPool, 0, n)
	for range max(n, 1) {
		m, err := NewMQTTSender(ctx, brokerURL, username, password)
		if err != nil {
			_ = pool.Close(context.WithoutCancel(ctx))
			return nil, err
		}
		pool = append(pool, m)
	}
	return pool, nil
}

// Send publishes through the connection that owns deviceKey.
func (p MQTTPool) Send(ctx context.Context, deviceKey string, body []byte) error {
	h := fnv.New32a()
	_, _ = h.Write([]byte(deviceKey))
	return p[int(h.Sum32()%uint32(len(p)))].Send(ctx, deviceKey, body) //nolint:gosec // len is small
}

// Close disconnects every connection.
func (p MQTTPool) Close(ctx context.Context) error {
	var errs []error
	for _, m := range p {
		errs = append(errs, m.Close(ctx))
	}
	return errors.Join(errs...)
}
