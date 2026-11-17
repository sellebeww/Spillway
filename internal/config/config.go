// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package config loads and validates runtime configuration from the environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// devSecretKey is the master key printed in .env.example (base64 MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=).
// Anyone can read it in the repository, so production refuses to start with it.
const devSecretKey = "0123456789abcdef0123456789abcdef"

// Config holds all runtime settings. Secrets are never logged: use LogValue.
type Config struct {
	HTTPAddr    string
	DatabaseURL string
	RedisURL    string
	LogLevel    slog.Level
	// MQTT subscriber (optional: empty MQTTURL runs HTTP ingest only).
	MQTTURL      string
	MQTTUsername string
	MQTTPassword string
	MQTTClientID string
	// OIDC (optional: without OIDC_ISSUER only the public API and ingest run).
	OIDCIssuer          string
	OIDCAudience        string
	OIDCJWKSURL         string
	BootstrapAdminEmail string
	DefaultOrgSlug      string
	CORSOrigins         []string
	TrustProxyHeaders   bool
	// Telegram (optional: without a token alerts are still stored and queued, nothing is sent).
	TelegramToken    string
	TelegramUsername string
	TelegramAPIURL   string
	PublicBaseURL    string
	// Env is "dev" or "production" (the default). Dev-only commands refuse to run otherwise.
	Env string
	// RawRetentionDays is how long raw readings are kept (7..3650, default 90).
	RawRetentionDays int
	// SecretKey is the 32-byte AES-GCM master key for device secrets; SecretKeyVersion
	// labels it inside stored blobs. OldSecretKeys can still decrypt (key rotation).
	SecretKey        []byte
	SecretKeyVersion uint8
	OldSecretKeys    map[uint8][]byte
}

// Load reads configuration through getenv (os.Getenv in production) and
// reports every problem at once.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	c := Config{
		// Loopback by default; containers opt in to 0.0.0.0 explicitly.
		HTTPAddr:    get("SPILLWAY_HTTP_ADDR", "127.0.0.1:8080"),
		DatabaseURL: get("DATABASE_URL", ""),
		RedisURL:    get("REDIS_URL", ""),
	}

	if err := requireURL("DATABASE_URL", c.DatabaseURL, "postgres", "postgresql"); err != nil {
		errs = append(errs, err)
	}
	if err := requireURL("REDIS_URL", c.RedisURL, "redis", "rediss"); err != nil {
		errs = append(errs, err)
	}

	if err := c.LogLevel.UnmarshalText([]byte(get("SPILLWAY_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("SPILLWAY_LOG_LEVEL: %w", err))
	}

	key, err := base64.StdEncoding.DecodeString(getenv("SPILLWAY_SECRET_KEY"))
	switch {
	case err != nil:
		errs = append(errs, errors.New("SPILLWAY_SECRET_KEY: must be base64 (generate with: openssl rand -base64 32)"))
	case len(key) != 32:
		errs = append(errs, fmt.Errorf("SPILLWAY_SECRET_KEY: must decode to 32 bytes, got %d", len(key)))
	default:
		c.SecretKey = key
	}

	c.MQTTURL = get("MQTT_URL", "")
	if c.MQTTURL != "" {
		if err := requireURL("MQTT_URL", c.MQTTURL, "tcp", "mqtt", "tls", "mqtts", "ssl"); err != nil {
			errs = append(errs, err)
		}
	}
	c.MQTTUsername = get("MQTT_USERNAME", "")
	c.MQTTPassword = get("MQTT_PASSWORD", "")
	c.MQTTClientID = get("SPILLWAY_MQTT_CLIENT_ID", "spillway-ingest")

	c.OIDCIssuer = strings.TrimRight(get("OIDC_ISSUER", ""), "/")
	c.OIDCAudience = get("OIDC_AUDIENCE", "spillway-api")
	c.OIDCJWKSURL = get("OIDC_JWKS_URL", "")
	if c.OIDCIssuer != "" {
		if err := requireURL("OIDC_ISSUER", c.OIDCIssuer, "http", "https"); err != nil {
			errs = append(errs, err)
		}
		if c.OIDCJWKSURL == "" {
			c.OIDCJWKSURL = c.OIDCIssuer + "/protocol/openid-connect/certs"
		}
	}
	c.BootstrapAdminEmail = get("BOOTSTRAP_ADMIN_EMAIL", "")
	c.DefaultOrgSlug = get("DEFAULT_ORG_SLUG", "")
	for _, o := range strings.Split(get("CORS_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	c.TrustProxyHeaders = get("TRUST_PROXY_HEADERS", "false") == "true"

	c.TelegramToken = get("TELEGRAM_BOT_TOKEN", "")
	c.TelegramUsername = strings.TrimPrefix(get("TELEGRAM_BOT_USERNAME", ""), "@")
	c.TelegramAPIURL = get("TELEGRAM_API_URL", "https://api.telegram.org")
	c.PublicBaseURL = strings.TrimRight(get("PUBLIC_BASE_URL", ""), "/")
	if c.TelegramToken != "" && c.TelegramUsername == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_USERNAME: required when TELEGRAM_BOT_TOKEN is set (used for deep links)"))
	}
	if c.PublicBaseURL != "" {
		if err := requireURL("PUBLIC_BASE_URL", c.PublicBaseURL, "http", "https"); err != nil {
			errs = append(errs, err)
		}
	}

	c.Env = get("SPILLWAY_ENV", "production")
	if c.Env != "dev" && c.Env != "production" {
		errs = append(errs, errors.New("SPILLWAY_ENV: must be dev or production"))
	}
	if c.Env == "production" && string(c.SecretKey) == devSecretKey {
		errs = append(errs, errors.New("SPILLWAY_SECRET_KEY: this is the published development key from .env.example; generate your own (openssl rand -base64 32)"))
	}

	days, err := strconv.Atoi(get("SPILLWAY_RAW_RETENTION_DAYS", "90"))
	if err != nil || days < 7 || days > 3650 {
		errs = append(errs, errors.New("SPILLWAY_RAW_RETENTION_DAYS: must be an integer between 7 and 3650"))
	}
	c.RawRetentionDays = days

	ver, err := strconv.ParseUint(get("SPILLWAY_SECRET_KEY_VERSION", "1"), 10, 8)
	if err != nil || ver == 0 {
		errs = append(errs, errors.New("SPILLWAY_SECRET_KEY_VERSION: must be an integer between 1 and 255"))
	}
	c.SecretKeyVersion = uint8(ver) //nolint:gosec // bounded by ParseUint bitSize 8
	old, err := parseOldKeys(getenv("SPILLWAY_SECRET_KEY_OLD"), c.SecretKeyVersion)
	if err != nil {
		errs = append(errs, err)
	}
	c.OldSecretKeys = old

	return c, errors.Join(errs...)
}

// parseOldKeys reads "version:base64,version:base64". Values are never echoed in errors.
func parseOldKeys(raw string, current uint8) (map[uint8][]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[uint8][]byte{}
	for _, item := range strings.Split(raw, ",") {
		vs, b64, ok := strings.Cut(strings.TrimSpace(item), ":")
		v, err := strconv.ParseUint(vs, 10, 8)
		key, kerr := base64.StdEncoding.DecodeString(b64)
		if !ok || err != nil || v == 0 || kerr != nil || len(key) != 32 || uint8(v) == current { //nolint:gosec // bounded by bitSize 8
			return nil, errors.New("SPILLWAY_SECRET_KEY_OLD: expected version:base64(32 bytes) pairs with versions distinct from the current one")
		}
		out[uint8(v)] = key //nolint:gosec // bounded by bitSize 8
	}
	return out, nil
}

func requireURL(name, raw string, schemes ...string) error {
	if raw == "" {
		return fmt.Errorf("%s: required", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: invalid URL", name) // never echo the value, it holds credentials
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("%s: scheme must be one of %v", name, schemes)
}

// LogValue redacts everything sensitive when a Config is passed to slog.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("log_level", c.LogLevel.String()),
	)
}
