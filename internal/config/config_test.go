package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

var goodKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

//nolint:gosec // dummy credentials in a test fixture
func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{
		"DATABASE_URL":        "postgres://u:p@localhost:5432/db",
		"REDIS_URL":           "redis://localhost:6379/0",
		"SPILLWAY_SECRET_KEY": goodKey,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != "127.0.0.1:8080" || c.LogLevel != slog.LevelInfo || len(c.SecretKey) != 32 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

// The master key in .env.example is public. Production must refuse it; dev may use it.
//
//nolint:gosec // the published development key, on purpose
func TestProductionRefusesThePublishedDevelopmentKey(t *testing.T) {
	const devKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // .env.example
	base := map[string]string{"DATABASE_URL": "postgres://u@localhost/db", "REDIS_URL": "redis://localhost:6379/0", "SPILLWAY_SECRET_KEY": devKey}
	_, err := Load(env(base)) // SPILLWAY_ENV defaults to production
	if err == nil || !strings.Contains(err.Error(), "SPILLWAY_SECRET_KEY") || strings.Contains(err.Error(), devKey) {
		t.Fatalf("production must refuse the development key without echoing it, got %v", err)
	}
	base["SPILLWAY_ENV"] = "dev"
	if _, err := Load(env(base)); err != nil {
		t.Fatalf("dev may use the development key: %v", err)
	}
}

//nolint:gosec // dummy credentials in a test fixture
func TestLoadReportsAllErrorsWithoutLeakingValues(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL":        "mysql://user:hunter2@h/db",
		"SPILLWAY_LOG_LEVEL":  "loud",
		"SPILLWAY_SECRET_KEY": "c2hvcnQ=",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"DATABASE_URL", "REDIS_URL", "SPILLWAY_LOG_LEVEL", "SPILLWAY_SECRET_KEY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %s in %q", want, msg)
		}
	}
	if strings.Contains(msg, "hunter2") {
		t.Error("error leaks credentials")
	}
}

func TestLoadPhase3Settings(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":        "postgres://u@localhost/db",
		"REDIS_URL":           "redis://localhost:6379/0",
		"SPILLWAY_SECRET_KEY": goodKey,
	}
	with := func(kv ...string) func(string) string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return env(m)
	}

	c, err := Load(with())
	if err != nil || c.Env != "production" || c.RawRetentionDays != 90 || c.SecretKeyVersion != 1 || c.OldSecretKeys != nil {
		t.Fatalf("defaults wrong: %+v err=%v", c, err)
	}
	c, err = Load(with("SPILLWAY_ENV", "dev", "SPILLWAY_RAW_RETENTION_DAYS", "30", "SPILLWAY_SECRET_KEY_VERSION", "2",
		"SPILLWAY_SECRET_KEY_OLD", "1:"+goodKey))
	if err != nil || c.Env != "dev" || c.RawRetentionDays != 30 || c.SecretKeyVersion != 2 || len(c.OldSecretKeys[1]) != 32 {
		t.Fatalf("overrides wrong: %+v err=%v", c, err)
	}

	for name, kv := range map[string][]string{
		"bad env":              {"SPILLWAY_ENV", "staging"},
		"retention too small":  {"SPILLWAY_RAW_RETENTION_DAYS", "3"},
		"retention not number": {"SPILLWAY_RAW_RETENTION_DAYS", "abc"},
		"version zero":         {"SPILLWAY_SECRET_KEY_VERSION", "0"},
		"old key same version": {"SPILLWAY_SECRET_KEY_OLD", "1:" + goodKey},
		"old key malformed":    {"SPILLWAY_SECRET_KEY_OLD", "2:c2hvcnQ="},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(with(kv...)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadTelegramSettings(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://u@localhost/db", "REDIS_URL": "redis://localhost:6379/0", "SPILLWAY_SECRET_KEY": goodKey}
	with := func(kv ...string) func(string) string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return env(m)
	}
	c, err := Load(with())
	if err != nil || c.TelegramToken != "" || c.TelegramAPIURL != "https://api.telegram.org" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = Load(with("TELEGRAM_BOT_TOKEN", "1:abc", "TELEGRAM_BOT_USERNAME", "@spillway_bot", "PUBLIC_BASE_URL", "https://spillway.example/"))
	if err != nil || c.TelegramUsername != "spillway_bot" || c.PublicBaseURL != "https://spillway.example" {
		t.Fatalf("telegram: %+v %v", c, err)
	}
	if _, err := Load(with("TELEGRAM_BOT_TOKEN", "1:abc")); err == nil {
		t.Fatal("a token without a username must be refused (deep links need it)")
	}
	if _, err := Load(with("PUBLIC_BASE_URL", "ftp://x")); err == nil {
		t.Fatal("bad public URL")
	}
	if strings.Contains(fmt.Sprint(Config{TelegramToken: "1:SECRETVALUE"}.LogValue()), "SECRETVALUE") {
		t.Fatal("the bot token must never be logged")
	}
}
