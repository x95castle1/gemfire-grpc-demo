package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// latencyBuckets are 50µs .. 60s: the same fine-grained low end as the other examples, plus room
// for queries that scan a whole region.
var latencyBuckets = []float64{
	0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
	0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5, 10, 30, 60,
}

// Metrics are served at /metrics. Names and labels match every other client in every example.
//
//   - demo_op_latency_seconds{client,example,region,op,code} times one API call's GemFire work
//     (a Get, a Put, or a whole Query stream), not the HTTP request around it. op is get, put or
//     the query's name (?name=, "Query" by default). Failures are timed too, with the gRPC status code as code
//     ("OK" on success). k6 measures the end-to-end request separately.
//   - demo_rows_total{client,example,region,op} counts the rows those queries returned.
//   - demo_stream_messages_total{client,example,method,direction} counts stream messages, e.g.
//     the QueryResponse messages a Query call received (interceptors.go).
//   - demo_rpc_duration_seconds{client,example,region,method,code} times every gRPC call the
//     client makes, streaming ones and the demo's included, via the interceptors in
//     interceptors.go.
//
// The default registry also carries go_* and process_* metrics, the Go counterpart of the Java
// clients' jvm_* metrics.
type Metrics struct {
	client, example string
	region          string
	latency         *prometheus.HistogramVec
	rows            *prometheus.CounterVec
	rpcs            *prometheus.HistogramVec
	messages        *prometheus.CounterVec
	checks          *prometheus.CounterVec
}

func newMetrics(cfg Config) *Metrics {
	m := &Metrics{
		client:  cfg.Client,
		example: cfg.Example,
		region:  cfg.Region,
		latency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_op_latency_seconds",
			Help:    "Latency of one /query call's GemFire work, by result code (excludes HTTP)",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "region", "op", "code"}),
		rows: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_rows_total",
			Help: "Rows returned by /query calls",
		}, []string{"client", "example", "region", "op"}),
		rpcs: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_rpc_duration_seconds",
			Help:    "Duration of each gRPC call the client made, by method and status code",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "region", "method", "code"}),
		messages: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_stream_messages_total",
			Help: "Stream messages sent and received, by gRPC method",
		}, []string{"client", "example", "method", "direction"}),
		checks: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_checks_total",
			Help: "Demo assertions by result",
		}, []string{"client", "example", "result"}),
	}
	// Pre-create the series so the dashboard shows 0 instead of "no data". For counters and
	// histograms this also matters for increase(): Prometheus can't see a series' first
	// observation, so a series that first appears already at 1 would count as 0.
	for _, op := range []string{"get", "put", defaultQueryName} {
		m.initOp(op)
	}
	m.checks.WithLabelValues(m.client, m.example, "pass")
	m.checks.WithLabelValues(m.client, m.example, "fail")
	return m
}

func (m *Metrics) handler() http.Handler { return promhttp.Handler() }

// initOp pre-creates one query name's series (see newMetrics).
func (m *Metrics) initOp(op string) {
	m.latency.WithLabelValues(m.client, m.example, m.region, op, "OK")
	m.rows.WithLabelValues(m.client, m.example, m.region, op)
}

// recordOp records one /query call. code is "OK" or the gRPC status code name.
func (m *Metrics) recordOp(op, code string, d time.Duration) {
	m.latency.WithLabelValues(m.client, m.example, m.region, op, code).Observe(d.Seconds())
}

// recordRows counts the rows one /query call returned.
func (m *Metrics) recordRows(op string, n int) {
	m.rows.WithLabelValues(m.client, m.example, m.region, op).Add(float64(n))
}

// initRPCs pre-creates the OK series of every gRPC method in these regions (see newMetrics).
func (m *Metrics) initRPCs(methods []string, regions ...string) {
	for _, method := range methods {
		m.messages.WithLabelValues(m.client, m.example, method, "received")
		for _, r := range regions {
			m.rpcs.WithLabelValues(m.client, m.example, r, method, "OK")
		}
	}
}

// recordRPC records one gRPC call (from the interceptors).
func (m *Metrics) recordRPC(method, region, code string, d time.Duration) {
	m.rpcs.WithLabelValues(m.client, m.example, region, method, code).Observe(d.Seconds())
}

// recordMessage counts one stream message (direction: "received").
func (m *Metrics) recordMessage(method, direction string) {
	m.messages.WithLabelValues(m.client, m.example, method, direction).Inc()
}

func (m *Metrics) recordCheck(passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.checks.WithLabelValues(m.client, m.example, result).Inc()
}
