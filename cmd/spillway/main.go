// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Command spillway runs the Spillway server and its maintenance subcommands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/sellebeww/spillway/internal/alerts"
	"github.com/sellebeww/spillway/internal/api"
	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/config"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/notify"
	"github.com/sellebeww/spillway/internal/observability"
	"github.com/sellebeww/spillway/internal/store"
)

func main() {
	want := map[string]int{"serve": 2, "migrate": 2, "seed-dev": 2, "sub-link": 4, "seed-load": 4, "create-org": 4}
	if len(os.Args) < 2 || want[os.Args[1]] != len(os.Args) {
		fmt.Fprintln(os.Stderr, "usage: spillway <serve|migrate|seed-dev> | spillway sub-link <org-slug> <site-slug> | spillway seed-load <devices> <out.json> | spillway create-org <slug> <name>")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx, os.Args[1], os.Args[2:]...)
	stop()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("fatal", "cmd", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args ...string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := observability.NewLogger(os.Stdout, cfg.LogLevel)

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch cmd {
	case "migrate":
		if err := store.Migrate(ctx, pool); err != nil {
			return err
		}
		if err := store.ApplyRetention(ctx, pool, cfg.RawRetentionDays); err != nil {
			return err
		}
		log.Info("migrations applied", "raw_retention_days", cfg.RawRetentionDays)
		return nil
	case "seed-dev":
		return seedDev(ctx, cfg, pool, log)
	case "sub-link":
		return subLink(ctx, cfg, pool, args[0], args[1])
	case "create-org":
		return createOrg(ctx, pool, log, args[0], args[1])
	case "seed-load":
		return seedLoad(ctx, cfg, pool, args[0], args[1])
	}

	return serve(ctx, cfg, pool, log)
}

// serve runs HTTP and MQTT ingest until ctx ends, then shuts down in an order that loses nothing:
// stop HTTP (in-flight requests finish), stop MQTT intake, drain the pipeline (acks go out as
// batches commit), and only then drop the broker connection.
func serve(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) error {
	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return errors.New("parse REDIS_URL") // not wrapped: the error text may echo credentials
	}
	rdb := redis.NewClient(ropts)
	defer func() { _ = rdb.Close() }() // nothing to flush at shutdown
	cipher, err := auth.NewCipher(cfg.SecretKeyVersion, cfg.SecretKey, cfg.OldSecretKeys)
	if err != nil {
		return fmt.Errorf("build cipher: %w", err)
	}

	reg := observability.NewRegistry()
	st := store.New(pool)
	sink := alerts.New(st, alerts.DefaultConfig(), log, nil, reg)
	if err := sink.Start(ctx); err != nil {
		return fmt.Errorf("start alerts: %w", err)
	}
	var bg sync.WaitGroup
	bgCtx, stopBG := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBG()
	if cfg.TelegramToken != "" {
		tg := &notify.Client{Token: cfg.TelegramToken, BaseURL: cfg.TelegramAPIURL}
		bot := &notify.Bot{TG: tg, St: st, Log: log}
		sender := &notify.Sender{TG: tg, St: st, PublicURL: cfg.PublicBaseURL, Log: log, M: notify.NewMetrics(reg)}
		bg.Add(2)
		go func() { defer bg.Done(); _ = bot.Run(bgCtx) }()
		go func() { defer bg.Done(); sender.Run(bgCtx, 2) }()
	} else {
		log.Warn("TELEGRAM_BOT_TOKEN not set: alerts are stored and queued but nothing is sent")
	}
	pipe := ingest.New(ingest.DefaultConfig(), ingest.Deps{
		Backend:   ingest.StoreBackend{Store: store.New(pool), Cipher: cipher},
		Dedupe:    &ingest.RedisDedupe{C: rdb, TTL: 24 * time.Hour},
		Publisher: &ingest.RedisPublisher{C: rdb},
		Sink:      sink,
		Metrics:   ingest.NewMetrics(reg),
		Log:       log,
	}, reg)

	deps := map[string]api.Pinger{
		"postgres": api.PingFunc(pool.Ping),
		"redis":    api.PingFunc(func(ctx context.Context) error { return rdb.Ping(ctx).Err() }),
	}
	var sub *ingest.Subscriber
	if cfg.MQTTURL != "" {
		// The connection must outlive the signal context: acknowledgements of drained messages are sent during shutdown.
		sub, err = ingest.StartSubscriber(context.WithoutCancel(ctx), ingest.MQTTConfig{URL: cfg.MQTTURL, ClientID: cfg.MQTTClientID, Username: cfg.MQTTUsername,
			Password: cfg.MQTTPassword, SessionExpiry: 24 * 3600}, pipe, log)
		if err != nil {
			_ = pipe.Close(context.Background())
			return err
		}
		deps["mqtt"] = api.PingFunc(func(context.Context) error { return sub.Connected() })
	}

	var ver *auth.Verifier
	if cfg.OIDCIssuer != "" {
		ver = &auth.Verifier{Issuer: cfg.OIDCIssuer, Audience: cfg.OIDCAudience, JWKSURL: cfg.OIDCJWKSURL}
	} else {
		log.Warn("OIDC_ISSUER not set: the admin API is disabled (public API and ingest still run)")
	}
	hubCtx, stopHub := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHub()
	var apiSrv *api.Server
	hub := api.NewHub(api.RedisSource{C: rdb}, func(c context.Context) (map[uuid.UUID]string, error) { return apiSrv.PublicSlugs(c) })
	apiSrv = api.NewServer(st, cipher, ver, hub, api.ServerConfig{BootstrapAdminEmail: cfg.BootstrapAdminEmail, DefaultOrg: cfg.DefaultOrgSlug,
		CORSOrigins: cfg.CORSOrigins, TrustProxy: cfg.TrustProxyHeaders, BotUsername: cfg.TelegramUsername}, log)
	bg.Add(1)
	go func() {
		defer bg.Done()
		if err := hub.Run(hubCtx); err != nil {
			log.Error("event hub stopped", "err", err)
		}
	}()

	limiter := ingest.NewLimiter(2, 10, 100000) // per device: 2 messages/s sustained, burst 10
	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouter(reg, deps, func(r chi.Router) {
			r.Post("/v1/ingest", pipe.HTTPHandler(limiter, 1024, 5*time.Second).ServeHTTP)
			apiSrv.Mount(r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("serving", "config", cfg, "mqtt", cfg.MQTTURL != "")

	var runErr error
	select {
	case err := <-errc:
		runErr = fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	// Fresh context: ctx is already cancelled, and the drain must still get its time.
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var errs []error
	stopHub() // open SSE streams would otherwise keep Shutdown waiting
	errs = append(errs, runErr, srv.Shutdown(sctx))
	if sub != nil {
		errs = append(errs, sub.StopIntake(sctx))
	}
	errs = append(errs, pipe.Close(sctx))
	errs = append(errs, sink.Close(sctx)) // after the pipeline: every committed reading is evaluated
	stopBG()
	bg.Wait()
	if sub != nil {
		errs = append(errs, sub.Close(sctx))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}

// seedDev loads the development fixture. It refuses to run outside SPILLWAY_ENV=dev.
func seedDev(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) error {
	if cfg.Env != "dev" {
		return errors.New("seed-dev only runs with SPILLWAY_ENV=dev")
	}
	cipher, err := auth.NewCipher(cfg.SecretKeyVersion, cfg.SecretKey, cfg.OldSecretKeys)
	if err != nil {
		return fmt.Errorf("build cipher: %w", err)
	}
	res, err := store.New(pool).SeedDev(ctx, cipher)
	if err != nil {
		return err
	}
	if !res.Created {
		log.Info("dev organization already exists; nothing changed", "org_id", res.OrgID)
		return nil
	}
	log.Info("dev organization created", "org_id", res.OrgID, "devices", len(res.Devices))
	// Device secrets are shown exactly once, on stdout, never through the logger.
	for _, d := range res.Devices {
		fmt.Printf("device=%s site=%s secret=%s\n", d.Key, d.SiteSlug, d.SecretHex)
	}
	return nil
}

// subLink prints a single-use Telegram deep link that subscribes a chat to a site (24 h).
func subLink(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, orgSlug, siteSlug string) error {
	if cfg.TelegramUsername == "" {
		return errors.New("TELEGRAM_BOT_USERNAME is required to build the link")
	}
	st := store.New(pool)
	org, err := st.OrgBySlug(ctx, orgSlug)
	if err != nil {
		return fmt.Errorf("organization %q: %w", orgSlug, err)
	}
	o := st.ForOrg(org.ID)
	sites, err := o.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, s := range sites {
		if s.Slug != siteSlug {
			continue
		}
		token, exp, err := o.CreateSubscriptionToken(store.WithActor(ctx, store.Actor{Label: "cli"}), s.ID, 24*time.Hour, time.Now())
		if err != nil {
			return err
		}
		fmt.Printf("https://t.me/%s?start=%s\nexpires %s (single use)\n", cfg.TelegramUsername, token, exp.Format(time.RFC3339))
		return nil
	}
	return fmt.Errorf("site %q not found in organization %q", siteSlug, orgSlug)
}

// seedLoad creates n load test devices and writes their keys and secrets to path (mode 0600) in
// the format the simulator reads. Dev-only: the file holds device secrets.
func seedLoad(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, countArg, path string) error {
	if cfg.Env != "dev" {
		return errors.New("seed-load only runs with SPILLWAY_ENV=dev (set it for this one command)")
	}
	n, err := strconv.Atoi(countArg)
	if err != nil {
		return fmt.Errorf("device count %q: %w", countArg, err)
	}
	cipher, err := auth.NewCipher(cfg.SecretKeyVersion, cfg.SecretKey, cfg.OldSecretKeys)
	if err != nil {
		return fmt.Errorf("build cipher: %w", err)
	}
	devs, err := store.New(pool).SeedLoad(ctx, cipher, n)
	if err != nil {
		return err
	}
	type entry struct {
		Device string `json:"device"`
		Secret string `json:"secret"`
	}
	entries := make([]entry, len(devs))
	for i, d := range devs {
		entries[i] = entry{d.Key, d.SecretHex}
	}
	raw, err := json.Marshal(entries) //nolint:gosec // the file is the point: simulator input, written 0600, dev only
	if err != nil {
		return fmt.Errorf("encode devices: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil { //nolint:gosec // operator-supplied path
		return fmt.Errorf("write devices file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "created %d devices; secrets written to %s\n", len(devs), path)
	return nil
}

// createOrg creates the organization a fresh deployment needs before the first admin signs in.
// Running it again for an existing slug changes nothing.
func createOrg(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, slug, name string) error {
	st := store.New(pool)
	if org, err := st.OrgBySlug(ctx, slug); err == nil {
		log.Info("organization already exists; nothing changed", "org_id", org.ID)
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	org, err := st.CreateOrg(store.WithActor(ctx, store.Actor{Label: "cli"}), slug, name)
	if err != nil {
		return err
	}
	log.Info("organization created", "org_id", org.ID, "slug", slug)
	return nil
}
