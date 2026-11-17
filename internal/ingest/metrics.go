// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import "github.com/prometheus/client_golang/prometheus"

// Metrics are the ingest Prometheus metrics. Labels never contain device keys or payload data.
type Metrics struct {
	Received     *prometheus.CounterVec // source
	Rejected     *prometheus.CounterVec // reason
	Duplicates   *prometheus.CounterVec // layer: memory, redis, database
	Stored       prometheus.Counter
	Late         prometheus.Counter
	Flags        *prometheus.CounterVec // flag
	Backpressure prometheus.Counter     // submissions that had to wait for queue space
	DBErrors     prometheus.Counter
	RedisErrors  prometheus.Counter
	PublishErrs  prometheus.Counter
	BatchSize    prometheus.Histogram
	InsertSecs   prometheus.Histogram
	E2ESecs      prometheus.Histogram // received_at to commit
}

// NewMetrics registers the metrics on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Received:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_ingest_received_total", Help: "Messages received by source."}, []string{"source"}),
		Rejected:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_ingest_rejected_total", Help: "Messages rejected by reason."}, []string{"reason"}),
		Duplicates:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_ingest_duplicates_total", Help: "Duplicate messages by the layer that caught them."}, []string{"layer"}),
		Stored:       prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_stored_total", Help: "Readings newly committed."}),
		Late:         prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_late_total", Help: "Stored readings flagged late."}),
		Flags:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_ingest_quality_flags_total", Help: "Stored readings by quality flag."}, []string{"flag"}),
		Backpressure: prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_backpressure_waits_total", Help: "Submissions that waited because a shard queue was full."}),
		DBErrors:     prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_db_errors_total", Help: "Failed insert attempts."}),
		RedisErrors:  prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_redis_errors_total", Help: "Redis errors (the pipeline fails open)."}),
		PublishErrs:  prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_ingest_publish_errors_total", Help: "Failed last-value/pub-sub publications."}),
		BatchSize:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "spillway_ingest_batch_size", Help: "Rows per insert batch.", Buckets: []float64{1, 5, 10, 50, 100, 250, 500, 1000}}),
		InsertSecs:   prometheus.NewHistogram(prometheus.HistogramOpts{Name: "spillway_ingest_insert_seconds", Help: "Database insert latency per statement.", Buckets: prometheus.ExponentialBuckets(0.001, 2, 14)}),
		E2ESecs:      prometheus.NewHistogram(prometheus.HistogramOpts{Name: "spillway_ingest_e2e_seconds", Help: "Latency from receipt to commit.", Buckets: prometheus.ExponentialBuckets(0.001, 2, 16)}),
	}
	reg.MustRegister(m.Received, m.Rejected, m.Duplicates, m.Stored, m.Late, m.Flags, m.Backpressure,
		m.DBErrors, m.RedisErrors, m.PublishErrs, m.BatchSize, m.InsertSecs, m.E2ESecs)
	return m
}
