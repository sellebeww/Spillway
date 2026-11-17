// Copyright (c) 2026 Vassel Goleyu. MIT License.

package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + rel) //nolint:gosec // fixed repo-relative paths chosen by this test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Both public names (the app and the sign-in service) must send HSTS; Caddy does not add it by itself.
func TestCaddyfileSendsHSTSOnEverySite(t *testing.T) {
	cf := repoFile(t, "deploy/caddy/Caddyfile")
	sites := len(regexp.MustCompile(`(?m)^\{\$PUBLIC_SCHEME\}://`).FindAllString(cf, -1))
	hsts := len(regexp.MustCompile(`(?m)^\s+header >?Strict-Transport-Security "max-age=\d{7,}"`).FindAllString(cf, -1))
	if sites != 2 || hsts != sites {
		t.Fatalf("%d site blocks but %d HSTS headers", sites, hsts)
	}
}

// A fresh clone must be able to follow docs/DEPLOY.md: the example file has to be tracked (.gitignore
// once hid it, which also broke the CI smoke job) and must list every variable compose requires.
func TestProdEnvExampleIsTrackedAndComplete(t *testing.T) {
	if !strings.Contains(repoFile(t, ".gitignore"), "!deploy/compose/.env.prod.example") {
		t.Fatal(".gitignore must un-ignore deploy/compose/.env.prod.example")
	}
	example := repoFile(t, "deploy/compose/.env.prod.example")
	for _, m := range regexp.MustCompile(`\$\{(\w+):\?`).FindAllStringSubmatch(repoFile(t, "deploy/compose/docker-compose.prod.yml"), -1) {
		if !regexp.MustCompile(`(?m)^` + m[1] + `=`).MatchString(example) {
			t.Errorf("compose requires %s but .env.prod.example does not define it", m[1])
		}
	}
	if strings.Contains(example, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY") {
		t.Error(".env.prod.example must not carry the development master key")
	}
}

// mosquitto_passwd -c fails on an existing file and a restarted container keeps its filesystem, so the
// entrypoint must remove the old file first. deploy/scripts/smoke.sh proves it for real (step 5b).
func TestBrokerEntrypointSurvivesARestart(t *testing.T) {
	sh := repoFile(t, "deploy/mosquitto/entrypoint.prod.sh")
	rm, create := strings.Index(sh, "rm -f /mosquitto/passwd"), strings.Index(sh, "mosquitto_passwd -b -c")
	if rm < 0 || create < 0 || rm > create {
		t.Fatal("entrypoint.prod.sh must `rm -f /mosquitto/passwd` before `mosquitto_passwd -c`")
	}
}

// Development credentials and the development realm must never be part of the production stack.
func TestProdStackCarriesNoDevelopmentCredentials(t *testing.T) {
	for _, f := range []string{"deploy/compose/docker-compose.prod.yml", "deploy/keycloak/realm-prod.json", "deploy/mosquitto/mosquitto.prod.conf", "deploy/mosquitto/entrypoint.prod.sh", "deploy/caddy/Caddyfile"} {
		body := repoFile(t, f)
		for _, bad := range []string{"realm-dev", "dev-admin-password", "dev-operator-password", "-dev-password", "spillway-dev"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains development credential or realm %q", f, bad)
			}
		}
	}
}
