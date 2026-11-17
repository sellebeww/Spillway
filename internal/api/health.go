// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package api exposes the HTTP surface of Spillway.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Pinger is a dependency that can report whether it is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingFunc adapts a function to Pinger.
type PingFunc func(ctx context.Context) error

// Ping implements Pinger.
func (f PingFunc) Ping(ctx context.Context) error { return f(ctx) }

// NewRouter wires /healthz (liveness), /readyz (dependencies) and /metrics.
func NewRouter(reg *prometheus.Registry, deps map[string]Pinger, mount ...func(chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		var failed []error
		for name, d := range deps {
			if err := d.Ping(ctx); err != nil {
				failed = append(failed, errors.New(name))
			}
		}
		if len(failed) > 0 {
			http.Error(w, "not ready: "+errors.Join(failed...).Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready"))
	})
	r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	for _, m := range mount {
		m(r)
	}
	return r
}
