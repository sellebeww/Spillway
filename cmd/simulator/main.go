// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Command simulator sends signed telemetry for virtual sensors to a Spillway server, over
// HTTP or MQTT, so ingest can be exercised without hardware.
//
//	simulator -devices-file devices.json -mqtt tcp://127.0.0.1:1883 -scenario flood-wave -interval 30s -duration 10m
//	simulator -devices-file devices.json -devices 1000 -mqtt tcp://127.0.0.1:1883 -interval 30s -rate-multiplier 30 -duration 2m \
//	    -duplicate-rate 0.05 -late-rate 0.02 -out-of-order-rate 0.03 -report report.json
//
// devices.json is [{"device":"dev-001","secret":"<64 hex>"}]; `spillway seed-dev` and
// `spillway seed-load` print the secrets. Broker credentials come from MQTT_USERNAME and MQTT_PASSWORD.
// -rate-multiplier compresses time: rounds are sent every interval/multiplier, so -duration is wall
// clock and the simulated time is duration x multiplier. The JSON report goes to -report (or stdout with "-").
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sellebeww/spillway/internal/simulate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simulator:", err)
		os.Exit(1)
	}
}

func run() error {
	devicesFile := flag.String("devices-file", "", "JSON file with device keys and secrets (required)")
	count := flag.Int("devices", 0, "use only the first N devices of the file (0 = all)")
	httpURL := flag.String("http", "", "ingest endpoint, e.g. http://127.0.0.1:8080/v1/ingest")
	mqttURL := flag.String("mqtt", "", "broker URL, e.g. tcp://127.0.0.1:1883")
	mqttConns := flag.Int("mqtt-conns", 8, "MQTT connections to spread devices over")
	scenario := flag.String("scenario", "steady", "steady | flood-wave | spike-noise | flatline | dropout | late-burst")
	interval := flag.Duration("interval", time.Second, "time between rounds (every device sends once per round)")
	duration := flag.Duration("duration", time.Minute, "total wall-clock run time")
	multiplier := flag.Float64("rate-multiplier", 1, "divides the interval: 30 turns a 30s interval into 1s")
	dupRate := flag.Float64("duplicate-rate", 0, "fraction of messages sent twice (0..1)")
	lateRate := flag.Float64("late-rate", 0, "fraction of messages with a timestamp 3 to 120 minutes old (0..1)")
	oooRate := flag.Float64("out-of-order-rate", 0, "fraction of messages delivered after the device's next one (0..1)")
	seed := flag.Uint64("seed", 0, "fault injection seed (0 = random)")
	concurrency := flag.Int("concurrency", 256, "simultaneous sends")
	report := flag.String("report", "", `write the JSON report to this file ("-" for stdout)`)
	flag.Parse()

	if *devicesFile == "" || (*httpURL == "") == (*mqttURL == "") {
		return fmt.Errorf("need -devices-file and exactly one of -http or -mqtt")
	}
	if !simulate.ValidScenario(*scenario) {
		return fmt.Errorf("unknown scenario %q", *scenario)
	}
	if *multiplier <= 0 || *interval <= 0 {
		return fmt.Errorf("need interval > 0 and rate-multiplier > 0")
	}
	for name, v := range map[string]float64{"duplicate-rate": *dupRate, "late-rate": *lateRate, "out-of-order-rate": *oooRate} {
		if v < 0 || v > 1 {
			return fmt.Errorf("%s must be within 0..1", name)
		}
	}
	eff := time.Duration(float64(*interval) / *multiplier)
	if eff <= 0 || *duration < eff {
		return fmt.Errorf("need 0 < interval/rate-multiplier <= duration")
	}
	devs, err := simulate.LoadDevices(*devicesFile)
	if err != nil {
		return err
	}
	if *count < 0 || *count > len(devs) {
		return fmt.Errorf("-devices %d: the file has %d devices", *count, len(devs))
	}
	if *count > 0 {
		devs = devs[:*count]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var sender simulate.Sender
	if *mqttURL != "" {
		pool, err := simulate.NewMQTTPool(ctx, *mqttURL, os.Getenv("MQTT_USERNAME"), os.Getenv("MQTT_PASSWORD"), *mqttConns)
		if err != nil {
			return err
		}
		defer func() {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = pool.Close(cctx)
		}()
		sender = pool
	} else {
		sender = simulate.HTTPSender{URL: *httpURL, Client: nil}
	}

	ticks := int(*duration / eff)
	fmt.Fprintf(os.Stderr, "sending %d rounds x %d devices (%s, every %v = %v / %gx)\n", ticks, len(devs), *scenario, eff, *interval, *multiplier)
	rep := simulate.Run(ctx, simulate.Config{Devices: devs, Scenario: simulate.Scenario(*scenario), Interval: eff, Ticks: ticks,
		Concurrency: *concurrency, DuplicateRate: *dupRate, LateRate: *lateRate, OutOfOrderRate: *oooRate, Seed: *seed}, sender)
	fmt.Fprintf(os.Stderr, "unique=%d confirmed=%d errors=%d elapsed=%v rate=%.1f msg/s\n", rep.Unique, rep.UniqueConfirmed, rep.Errors,
		rep.Elapsed.Round(time.Millisecond), rep.Rate())
	if *report != "" {
		js, err := rep.JSON()
		if err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
		if *report == "-" {
			fmt.Println(string(js))
		} else if err := os.WriteFile(*report, append(js, '\n'), 0o600); err != nil { //nolint:gosec // operator-supplied path
			return fmt.Errorf("write report: %w", err)
		}
	}
	if rep.FirstError != nil {
		fmt.Fprintf(os.Stderr, "first error: %v\n", rep.FirstError)
		return fmt.Errorf("%d transmissions failed", rep.Errors)
	}
	return nil
}
