package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// latencyBuckets are 50µs .. 2.5s, fine-grained at the low end where unary cache ops live.
// Identical to the Java clients' buckets so the dashboards compare like for like.
var latencyBuckets = []float64{
	0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
	0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5,
}

// Metrics are served at /metrics. Names and labels match every other client in every example.
// demo_op_latency_seconds times only the GemFire call (the RPC), not the HTTP request around it;
// k6 measures the end-to-end request separately. The default registry also carries go_* and
// process_* metrics, the Go counterpart of the Java clients' jvm_* metrics.
type Metrics struct {
	client, example string
	latency         *prometheus.HistogramVec
	errors          *prometheus.CounterVec
	checks          *prometheus.CounterVec
}

func newMetrics(cfg Config) *Metrics {
	m := &Metrics{
		client:  cfg.Client,
		example: cfg.Example,
		latency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_op_latency_seconds",
			Help:    "Latency of one GemFire call made by the client (excludes HTTP)",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "op"}),
		errors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_errors_total",
			Help: "Failed GemFire calls by status code / error type",
		}, []string{"client", "example", "op", "code"}),
		checks: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_checks_total",
			Help: "Demo assertions by result",
		}, []string{"client", "example", "result"}),
	}
	// Pre-create the series so the dashboard shows 0 instead of "no data".
	m.checks.WithLabelValues(m.client, m.example, "pass")
	m.checks.WithLabelValues(m.client, m.example, "fail")
	return m
}

func (m *Metrics) handler() http.Handler { return promhttp.Handler() }

func (m *Metrics) recordLatency(op string, d time.Duration) {
	m.latency.WithLabelValues(m.client, m.example, op).Observe(d.Seconds())
}

func (m *Metrics) recordError(op, code string) {
	m.errors.WithLabelValues(m.client, m.example, op, code).Inc()
}

func (m *Metrics) recordCheck(passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.checks.WithLabelValues(m.client, m.example, result).Inc()
}
