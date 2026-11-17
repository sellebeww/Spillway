package api

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

type oasOp struct {
	Summary    string                    `yaml:"summary"`
	Security   []map[string][]string     `yaml:"security"`
	Parameters []map[string]any          `yaml:"parameters"`
	Responses  map[string]map[string]any `yaml:"responses"`
}

// TestOpenAPIMatchesTheRouter is the contract test: every routed endpoint is documented, every
// documented endpoint exists, and the documentation agrees with the code on authentication.
func TestOpenAPIMatchesTheRouter(t *testing.T) {
	raw, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]oasOp `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}

	r := chi.NewRouter()
	NewServer(nil, nil, nil, nil, ServerConfig{}, nil).Mount(r)
	routed := map[string]bool{}
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != "OPTIONS" {
			routed[strings.ToLower(method)+" "+route] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	documented := map[string]bool{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			key := method + " " + path
			if path == "/v1/ingest" { // registered by the ingest pipeline, not by Mount
				continue
			}
			documented[key] = true
			if op.Summary == "" {
				t.Errorf("%s has no summary", key)
			}
			ok2xx := false
			for code := range op.Responses {
				if strings.HasPrefix(code, "2") {
					ok2xx = true
				}
			}
			if !ok2xx {
				t.Errorf("%s documents no success response", key)
			}
			public := strings.HasPrefix(path, "/v1/public/")
			switch {
			case public && len(op.Security) != 0:
				t.Errorf("%s is public but declares security", key)
			case !public && (len(op.Security) != 1 || op.Security[0]["bearerAuth"] == nil):
				t.Errorf("%s must require bearerAuth", key)
			case !public && op.Responses["401"] == nil:
				t.Errorf("%s must document 401", key)
			}
			if strings.Contains(path, "{id}") && op.Responses["404"] == nil && !strings.HasSuffix(path, "/v1/me") {
				t.Errorf("%s has an {id} and must document 404", key)
			}
			for _, name := range pathParams(path) {
				found := false
				for _, p := range op.Parameters {
					if ref, ok := p["$ref"].(string); ok && strings.EqualFold(strings.TrimPrefix(ref, "#/components/parameters/"), name) {
						found = true
					}
					if p["name"] == name {
						found = true
					}
				}
				if !found {
					t.Errorf("%s does not declare path parameter {%s}", key, name)
				}
			}
		}
	}
	var missing, extra []string
	for k := range routed {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !routed[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes missing from docs/openapi.yaml: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("documented endpoints that do not exist: %v", extra)
	}
	if len(routed) < 25 {
		t.Fatalf("only %d routes found; the walk is not working", len(routed))
	}
}

func pathParams(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, strings.Trim(seg, "{}"))
		}
	}
	return out
}
