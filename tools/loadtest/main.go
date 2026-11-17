// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Command loadtest drives N simulated devices through MQTT against a running production compose
// stack (docs/DEPLOY.md) in three phases (baseline, 10x burst, recovery), samples Prometheus
// metrics and container resources while it runs, and proves no reading was lost by comparing
// the unique messages the broker acknowledged with the rows in the database.
//
//	go run ./tools/loadtest -devices-file load-devices.json -devices 1000 -rate-multiplier 10 -out loadtest-result.json
//
// Everything it prints is measured; nothing is extrapolated. See docs/LOADTEST.md.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sellebeww/spillway/internal/simulate"
)

type cfg struct {
	devicesFile, mqttURL, metricsURL, envFile, composeFile, out string
	devices, mqttConns                                          int
	interval, baseline, burst, recovery                         time.Duration
	multiplier, burstFactor                                     float64
	dupRate, lateRate, oooRate                                  float64
}

type phaseResult struct {
	Name           string          `json:"name"`
	TargetRate     float64         `json:"target_msg_per_s"`
	Report         json.RawMessage `json:"simulator_report"`
	StoredDelta    float64         `json:"stored_delta"`
	DuplicateDelta float64         `json:"duplicates_delta"`
	RejectedDelta  float64         `json:"rejected_delta"`
	LateDelta      float64         `json:"late_delta"`
	IngestMsPerS   float64         `json:"stored_per_s_over_phase"`
	E2EP50ms       float64         `json:"ingest_e2e_p50_ms"`
	E2EP95ms       float64         `json:"ingest_e2e_p95_ms"`
	E2EP99ms       float64         `json:"ingest_e2e_p99_ms"`
	MaxQueueDepth  float64         `json:"max_queue_depth"`
	Backpressure   float64         `json:"backpressure_waits_delta"`
	DBErrors       float64         `json:"db_errors_delta"`
	Resources      map[string]peak `json:"resources_peak"`
}

type peak struct {
	CPUPercent float64 `json:"cpu_percent_max"`
	CPUAvg     float64 `json:"cpu_percent_avg"`
	MemMiB     float64 `json:"mem_mib_max"`
}

type result struct {
	Started       time.Time     `json:"started"`
	Devices       int           `json:"devices"`
	IntervalS     float64       `json:"interval_s"`
	Multiplier    float64       `json:"rate_multiplier"`
	BurstFactor   float64       `json:"burst_factor"`
	Faults        string        `json:"fault_injection"`
	Phases        []phaseResult `json:"phases"`
	UniqueSent    int64         `json:"unique_messages_acknowledged_by_broker"`
	RowsInDB      int64         `json:"rows_in_db_for_load_org"`
	Lost          int64         `json:"lost"`
	DBSizeBefore  int64         `json:"db_bytes_before"`
	DBSizeAfter   int64         `json:"db_bytes_after"`
	DrainSeconds  float64       `json:"drain_seconds_after_last_phase"`
	ReadingsTotal int64         `json:"readings_row_count_total"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func run() error {
	var c cfg
	flag.StringVar(&c.devicesFile, "devices-file", "load-devices.json", "devices JSON from `spillway seed-load`")
	flag.IntVar(&c.devices, "devices", 1000, "devices to use")
	flag.StringVar(&c.mqttURL, "mqtt", "tcp://127.0.0.1:1883", "broker URL")
	flag.IntVar(&c.mqttConns, "mqtt-conns", 16, "MQTT connections")
	flag.StringVar(&c.metricsURL, "metrics", "http://127.0.0.1:8080/metrics", "Prometheus endpoint of spillway")
	flag.StringVar(&c.envFile, "env-file", "deploy/compose/.env.prod", "compose env file")
	flag.StringVar(&c.composeFile, "compose-file", "deploy/compose/docker-compose.prod.yml", "compose file")
	flag.DurationVar(&c.interval, "interval", 30*time.Second, "device reporting interval being simulated")
	flag.Float64Var(&c.multiplier, "rate-multiplier", 1, "time compression of the baseline phase")
	flag.DurationVar(&c.baseline, "baseline", 3*time.Minute, "baseline phase, wall clock")
	flag.DurationVar(&c.burst, "burst", 60*time.Second, "burst phase, wall clock")
	flag.Float64Var(&c.burstFactor, "burst-factor", 10, "burst rate as a multiple of the baseline rate")
	flag.DurationVar(&c.recovery, "recovery", 2*time.Minute, "recovery phase at baseline rate")
	flag.Float64Var(&c.dupRate, "duplicate-rate", 0.02, "duplicate fraction")
	flag.Float64Var(&c.lateRate, "late-rate", 0.01, "late fraction")
	flag.Float64Var(&c.oooRate, "out-of-order-rate", 0.02, "out-of-order fraction")
	flag.StringVar(&c.out, "out", "loadtest-result.json", "result JSON")
	flag.Parse()

	devs, err := simulate.LoadDevices(c.devicesFile)
	if err != nil {
		return err
	}
	if c.devices > len(devs) {
		return fmt.Errorf("-devices %d but the file has %d", c.devices, len(devs))
	}
	devs = devs[:c.devices]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := simulate.NewMQTTPool(ctx, c.mqttURL, os.Getenv("MQTT_USERNAME"), os.Getenv("MQTT_PASSWORD"), c.mqttConns)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close(context.Background()) }()

	res := result{Started: time.Now().UTC(), Devices: c.devices, IntervalS: c.interval.Seconds(), Multiplier: c.multiplier, BurstFactor: c.burstFactor,
		Faults: fmt.Sprintf("duplicate=%.2f late=%.2f out-of-order=%.2f", c.dupRate, c.lateRate, c.oooRate)}
	rowsBefore, _ := c.sqlInt(ctx, "SELECT count(*) FROM readings WHERE device_id IN (SELECT id FROM devices WHERE device_key LIKE 'load-%')")
	res.DBSizeBefore, _ = c.sqlInt(ctx, "SELECT pg_database_size('spillway')")

	phases := []struct {
		name string
		mult float64
		dur  time.Duration
	}{{"baseline", c.multiplier, c.baseline}, {"burst", c.multiplier * c.burstFactor, c.burst}, {"recovery", c.multiplier, c.recovery}}
	for _, p := range phases {
		pr, uniq, err := c.phase(ctx, devs, pool, p.name, p.mult, p.dur)
		if err != nil {
			return err
		}
		res.UniqueSent += uniq
		res.Phases = append(res.Phases, pr)
		fmt.Fprintf(os.Stderr, "phase %s done: stored +%.0f, p99 %.1f ms, max queue %.0f\n", p.name, pr.StoredDelta, pr.E2EP99ms, pr.MaxQueueDepth)
	}

	// Drain: wait until the stored counter stops moving and the queue is empty.
	t0 := time.Now()
	var lastStored float64 = -1
	for stable := 0; stable < 3; {
		time.Sleep(2 * time.Second)
		m, err := scrape(c.metricsURL)
		if err != nil {
			return err
		}
		if m["spillway_ingest_stored_total"] == lastStored && m["spillway_ingest_queue_depth"] == 0 {
			stable++
		} else {
			stable = 0
		}
		lastStored = m["spillway_ingest_stored_total"]
		if time.Since(t0) > 10*time.Minute {
			return fmt.Errorf("pipeline did not drain within 10 minutes")
		}
	}
	res.DrainSeconds = time.Since(t0).Seconds() - 6
	rowsAfter, err := c.sqlInt(ctx, "SELECT count(*) FROM readings WHERE device_id IN (SELECT id FROM devices WHERE device_key LIKE 'load-%')")
	if err != nil {
		return err
	}
	res.RowsInDB = rowsAfter - rowsBefore
	res.Lost = res.UniqueSent - res.RowsInDB
	res.DBSizeAfter, _ = c.sqlInt(ctx, "SELECT pg_database_size('spillway')")
	res.ReadingsTotal = rowsAfter

	raw, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(c.out, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Println(string(raw))
	if res.Lost != 0 {
		return fmt.Errorf("LOSS DETECTED: %d unique messages acknowledged but not in the database", res.Lost)
	}
	return nil
}

func (c cfg) phase(ctx context.Context, devs []simulate.Device, s simulate.Sender, name string, mult float64, dur time.Duration) (phaseResult, int64, error) {
	eff := time.Duration(float64(c.interval) / mult)
	ticks := int(dur / eff)
	pr := phaseResult{Name: name, TargetRate: float64(len(devs)) * mult / c.interval.Seconds()}
	before, err := scrape(c.metricsURL)
	if err != nil {
		return pr, 0, err
	}
	sctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var maxQ float64
	samples := map[string][]peakSample{}
	var mu sync.Mutex
	wg.Add(1)
	go func() { // sample every second until the phase ends
		defer wg.Done()
		for tk := time.NewTicker(time.Second); ; {
			select {
			case <-sctx.Done():
				tk.Stop()
				return
			case <-tk.C:
				if m, err := scrape(c.metricsURL); err == nil {
					mu.Lock()
					maxQ = math.Max(maxQ, m["spillway_ingest_queue_depth"])
					mu.Unlock()
				}
				for svc, v := range c.dockerStats(sctx) {
					mu.Lock()
					samples[svc] = append(samples[svc], v)
					mu.Unlock()
				}
			}
		}
	}()
	fmt.Fprintf(os.Stderr, "phase %s: %d devices, every %v, %d rounds, target %.0f msg/s\n", name, len(devs), eff, ticks, pr.TargetRate)
	rep := simulate.Run(ctx, simulate.Config{Devices: devs, Scenario: simulate.Steady, Interval: eff, Ticks: ticks, Concurrency: 1024,
		DuplicateRate: c.dupRate, LateRate: c.lateRate, OutOfOrderRate: c.oooRate}, s)
	// Let in-flight work finish so the phase delta covers it, still sampling.
	time.Sleep(3 * time.Second)
	cancel()
	wg.Wait()
	after, err := scrape(c.metricsURL)
	if err != nil {
		return pr, 0, err
	}
	pr.StoredDelta = after["spillway_ingest_stored_total"] - before["spillway_ingest_stored_total"]
	pr.RejectedDelta = sumPrefix(after, "spillway_ingest_rejected_total") - sumPrefix(before, "spillway_ingest_rejected_total")
	pr.DuplicateDelta = sumPrefix(after, "spillway_ingest_duplicates_total") - sumPrefix(before, "spillway_ingest_duplicates_total")
	pr.LateDelta = after["spillway_ingest_late_total"] - before["spillway_ingest_late_total"]
	pr.Backpressure = after["spillway_ingest_backpressure_waits_total"] - before["spillway_ingest_backpressure_waits_total"]
	pr.DBErrors = after["spillway_ingest_db_errors_total"] - before["spillway_ingest_db_errors_total"]
	pr.IngestMsPerS = pr.StoredDelta / rep.Elapsed.Seconds()
	pr.E2EP50ms, pr.E2EP95ms, pr.E2EP99ms = histQuantiles(before, after, "spillway_ingest_e2e_seconds")
	pr.MaxQueueDepth = maxQ
	pr.Resources = map[string]peak{}
	for svc, ss := range samples {
		var p peak
		for _, s := range ss {
			p.CPUPercent = math.Max(p.CPUPercent, s.cpu)
			p.CPUAvg += s.cpu / float64(len(ss))
			p.MemMiB = math.Max(p.MemMiB, s.mem)
		}
		pr.Resources[svc] = p
	}
	pr.Report, _ = rep.JSON()
	return pr, rep.UniqueConfirmed, nil
}

type peakSample struct{ cpu, mem float64 }

// dockerStats returns CPU percent and memory MiB of the key services (one call, all containers).
func (c cfg) dockerStats(ctx context.Context) map[string]peakSample {
	out := map[string]peakSample{}
	ids, err := exec.CommandContext(ctx, "docker", "compose", "--env-file", c.envFile, "-f", c.composeFile, "ps", "-q", "spillway", "postgres", "mosquitto", "redis").Output() //nolint:gosec // operator-supplied paths
	if err != nil {
		return out
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}} {{.CPUPerc}} {{.MemUsage}}"}, strings.Fields(string(ids))...)
	raw, err := exec.CommandContext(ctx, "docker", args...).Output() //nolint:gosec // ids come from docker
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		svc := f[0]
		if i := strings.Index(svc, "-"); i >= 0 {
			svc = strings.TrimSuffix(strings.TrimPrefix(svc[i+1:], ""), "-1")
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(f[1], "%"), 64)
		out[svc] = peakSample{cpu: cpu, mem: parseMiB(f[2])}
	}
	return out
}

func parseMiB(s string) float64 {
	// Longest suffix first: "B" also ends "MiB".
	for _, u := range []struct {
		suffix string
		mul    float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"B", 1.0 / 1048576}} {
		if strings.HasSuffix(s, u.suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			return v * u.mul
		}
	}
	return 0
}

func (c cfg) sqlInt(ctx context.Context, q string) (int64, error) {
	out, err := exec.CommandContext(ctx, "docker", "compose", "--env-file", c.envFile, "-f", c.composeFile, "exec", "-T", "postgres", //nolint:gosec // operator-supplied paths
		"psql", "-U", "spillway", "-d", "spillway", "-Atc", q).Output()
	if err != nil {
		return 0, fmt.Errorf("psql: %w", err)
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

// scrape parses the Prometheus text format into name{labels} -> value.
func scrape(url string) (map[string]float64, error) {
	cl := http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get(url) //nolint:noctx // short sampling call
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return parseProm(resp.Body), nil
}

func parseProm(r io.Reader) map[string]float64 {
	m := map[string]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(line[i+1:], 64); err == nil {
			m[line[:i]] = v
		}
	}
	return m
}

func sumPrefix(m map[string]float64, name string) float64 {
	var s float64
	for k, v := range m {
		if strings.HasPrefix(k, name) {
			s += v
		}
	}
	return s
}

// histQuantiles estimates p50/p95/p99 (milliseconds) of the observations between two scrapes
// of a Prometheus histogram, by linear interpolation inside the bucket (as histogram_quantile does).
func histQuantiles(before, after map[string]float64, name string) (p50, p95, p99 float64) {
	type bkt struct{ le, n float64 }
	var bs []bkt
	for k, v := range after {
		if !strings.HasPrefix(k, name+"_bucket{le=\"") {
			continue
		}
		le := strings.TrimSuffix(strings.TrimPrefix(k, name+"_bucket{le=\""), "\"}")
		f := math.Inf(1)
		if le != "+Inf" {
			f, _ = strconv.ParseFloat(le, 64)
		}
		bs = append(bs, bkt{f, v - before[k]})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
	if len(bs) == 0 || bs[len(bs)-1].n == 0 {
		return 0, 0, 0
	}
	total := bs[len(bs)-1].n
	q := func(p float64) float64 {
		rank := p * total
		prevLE, prevN := 0.0, 0.0
		for _, b := range bs {
			if b.n >= rank {
				if math.IsInf(b.le, 1) {
					return prevLE * 1000
				}
				if b.n == prevN {
					return b.le * 1000
				}
				return (prevLE + (b.le-prevLE)*(rank-prevN)/(b.n-prevN)) * 1000
			}
			prevLE, prevN = b.le, b.n
		}
		return prevLE * 1000
	}
	return q(0.50), q(0.95), q(0.99)
}
