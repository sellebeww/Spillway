package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestEndpoints(t *testing.T) {
	ok := PingFunc(func(context.Context) error { return nil })
	bad := PingFunc(func(context.Context) error { return errors.New("down") })

	cases := []struct {
		name string
		deps map[string]Pinger
		path string
		want int
	}{
		{"healthz ignores deps", map[string]Pinger{"db": bad}, "/healthz", 200},
		{"readyz all up", map[string]Pinger{"db": ok, "redis": ok}, "/readyz", 200},
		{"readyz one down", map[string]Pinger{"db": ok, "redis": bad}, "/readyz", 503},
		{"metrics", nil, "/metrics", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewRouter(prometheus.NewRegistry(), c.deps).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, c.path, nil))
			if rec.Code != c.want {
				t.Fatalf("got %d want %d", rec.Code, c.want)
			}
		})
	}
}
