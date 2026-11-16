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
//
//   - demo_op_latency_seconds{client,example,region,op,code} times one /crud operation's GemFire
//     work, not the HTTP request around it. Failures are timed too, with the gRPC status code as
//     code ("OK" on success). k6 measures the end-to-end request separately.
//   - demo_calls_total{client,example,user,region,code} counts every gRPC call by the user whose
//     token it carried (alice, bob, carol, none), the region and the status code: OK,
//     PERMISSION_DENIED or UNAUTHENTICATED show who got in.
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
	rpcs            *prometheus.HistogramVec
	calls           *prometheus.CounterVec
	checks          *prometheus.CounterVec
}

func newMetrics(cfg Config) *Metrics {
	m := &Metrics{
		client:  cfg.Client,
		example: cfg.Example,
		region:  cfg.Region,
		latency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_op_latency_seconds",
			Help:    "Latency of one /crud operation's GemFire work, by result code (excludes HTTP)",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "region", "op", "code"}),
		rpcs: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_rpc_duration_seconds",
			Help:    "Duration of each gRPC call the client made, by method and status code",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "region", "method", "code"}),
		calls: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_calls_total",
			Help: "gRPC calls by the user whose token they carried, the region, and the status code",
		}, []string{"client", "example", "user", "region", "code"}),
		checks: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_checks_total",
			Help: "Demo assertions by result",
		}, []string{"client", "example", "result"}),
	}
	// Pre-create the series so the dashboard shows 0 instead of "no data". For counters and
	// histograms this also matters for increase(): Prometheus can't see a series' first
	// observation, so a series that first appears already at 1 would count as 0.
	for _, o := range ops {
		m.latency.WithLabelValues(m.client, m.example, m.region, o.rpc, "OK")
	}
	for _, u := range userNames {
		for _, r := range []string{"orders", "customers"} {
			m.calls.WithLabelValues(m.client, m.example, u, r, "OK")
		}
	}
	m.checks.WithLabelValues(m.client, m.example, "pass")
	m.checks.WithLabelValues(m.client, m.example, "fail")
	return m
}

func (m *Metrics) handler() http.Handler { return promhttp.Handler() }

// recordOp records one /crud operation. code is "OK" or the gRPC status code name.
func (m *Metrics) recordOp(op, code string, d time.Duration) {
	m.latency.WithLabelValues(m.client, m.example, m.region, op, code).Observe(d.Seconds())
}

// initRPCs pre-creates the OK series of every gRPC method in these regions (see newMetrics).
func (m *Metrics) initRPCs(methods []string, regions ...string) {
	for _, method := range methods {
		for _, r := range regions {
			m.rpcs.WithLabelValues(m.client, m.example, r, method, "OK")
		}
	}
}

// recordRPC records one gRPC call (from the interceptors).
func (m *Metrics) recordRPC(method, region, code string, d time.Duration) {
	m.rpcs.WithLabelValues(m.client, m.example, region, method, code).Observe(d.Seconds())
}

// recordCall counts one gRPC call by the acting user, region and status code.
func (m *Metrics) recordCall(user, region, code string) {
	m.calls.WithLabelValues(m.client, m.example, user, region, code).Inc()
}

func (m *Metrics) recordCheck(passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.checks.WithLabelValues(m.client, m.example, result).Inc()
}
