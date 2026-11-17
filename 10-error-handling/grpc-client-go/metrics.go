package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// latencyBuckets are 50µs .. 2.5s as in every other client, plus 4 s .. 60 s: here a call can
// wait for a connection (waitforready) or retry for seconds, and that wait is the point.
var latencyBuckets = []float64{
	0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
	0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5,
	4, 6, 8, 10, 12.5, 15, 17.5, 20, 22.5, 25, 27.5, 30, 45, 60,
}

// Metrics are served at /metrics.
//
//   - demo_calls_total{client,example,channel,code} counts every gRPC call by the channel it went
//     out on (failfast, retry, waitforready; the demo's unreachable/* channels) and its final
//     status code, once per call however many attempts it took.
//   - demo_call_attempts_total{client,example,channel} counts attempts: a retried call counts
//     each one. Attempts ÷ calls is the retries' cost.
//   - demo_op_latency_seconds{client,example,channel,region,op,code} times one /crud operation's
//     GemFire work, failures included, with the gRPC status code as code ("OK" on success).
//   - demo_rpc_duration_seconds{client,example,channel,region,method,code} times every gRPC call
//     the client makes, via the interceptors in interceptors.go.
type Metrics struct {
	client, example string
	region          string
	latency         *prometheus.HistogramVec
	rpcs            *prometheus.HistogramVec
	calls           *prometheus.CounterVec
	attempts        *prometheus.CounterVec
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
		}, []string{"client", "example", "channel", "region", "op", "code"}),
		rpcs: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "demo_rpc_duration_seconds",
			Help:    "Duration of each gRPC call the client made, by method and status code",
			Buckets: latencyBuckets,
		}, []string{"client", "example", "channel", "region", "method", "code"}),
		calls: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_calls_total",
			Help: "gRPC calls by channel and final status code (once per call, however many attempts)",
		}, []string{"client", "example", "channel", "code"}),
		attempts: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_call_attempts_total",
			Help: "gRPC call attempts by channel: each retry is another attempt",
		}, []string{"client", "example", "channel"}),
		checks: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "demo_checks_total",
			Help: "Demo assertions by result",
		}, []string{"client", "example", "result"}),
	}
	// Pre-create the series so the dashboard shows 0 instead of "no data". For counters and
	// histograms this also matters for increase(): Prometheus can't see a series' first
	// observation, so a series that first appears already at 1 would count as 0.
	for _, ch := range channels {
		for _, o := range ops {
			m.latency.WithLabelValues(m.client, m.example, ch, m.region, o.rpc, "OK")
		}
		for _, code := range []string{"OK", "UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED",
			"INVALID_ARGUMENT", "FAILED_PRECONDITION", "UNIMPLEMENTED", "INTERNAL", "CANCELLED"} {
			m.calls.WithLabelValues(m.client, m.example, ch, code)
		}
		m.attempts.WithLabelValues(m.client, m.example, ch)
		m.attempts.WithLabelValues(m.client, m.example, "unreachable/"+ch)
		m.calls.WithLabelValues(m.client, m.example, "unreachable/"+ch, "UNAVAILABLE")
		m.calls.WithLabelValues(m.client, m.example, "unreachable/"+ch, "DEADLINE_EXCEEDED")
	}
	m.checks.WithLabelValues(m.client, m.example, "pass")
	m.checks.WithLabelValues(m.client, m.example, "fail")
	return m
}

func (m *Metrics) handler() http.Handler { return promhttp.Handler() }

// recordOp records one /crud operation. code is "OK" or the gRPC status code name.
func (m *Metrics) recordOp(channel, op, code string, d time.Duration) {
	m.latency.WithLabelValues(m.client, m.example, channel, m.region, op, code).Observe(d.Seconds())
}

// initRPCs pre-creates the OK series of every gRPC method in these regions (see newMetrics).
func (m *Metrics) initRPCs(methods []string, regions ...string) {
	for _, ch := range channels {
		for _, method := range methods {
			for _, r := range regions {
				m.rpcs.WithLabelValues(m.client, m.example, ch, r, method, "OK")
			}
		}
	}
}

// recordRPC records one gRPC call (from the interceptors).
func (m *Metrics) recordRPC(channel, method, region, code string, d time.Duration) {
	m.rpcs.WithLabelValues(m.client, m.example, channel, region, method, code).Observe(d.Seconds())
	m.calls.WithLabelValues(m.client, m.example, channel, code).Inc()
}

// recordAttempt counts one attempt of a call (from the stats handler in channels.go).
func (m *Metrics) recordAttempt(channel string) {
	m.attempts.WithLabelValues(m.client, m.example, channel).Inc()
}

func (m *Metrics) recordCheck(passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.checks.WithLabelValues(m.client, m.example, result).Inc()
}
