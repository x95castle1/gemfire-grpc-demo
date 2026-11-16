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
//   - demo_calls_total{client,example,channel,server,security,code} counts every gRPC call by the
//     channel it went out on (direct or xds), the server that answered (from the call's peer
//     address) and whether the connection was TLS (security: tls or insecure).
//   - demo_server_cert_expiry_seconds{client,example,channel,server} is the Unix time the
//     certificate the server presented expires.
//   - demo_client_cert_expiry_seconds{client,example} is when the client's own certificate expires.
//   - demo_op_latency_seconds{client,example,channel,region,op,code} times one /crud operation's GemFire
//     work, not the HTTP request around it. Failures are timed too, with the gRPC status code as
//     code ("OK" on success). k6 measures the end-to-end request separately.
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
	certExpiry      *prometheus.GaugeVec
	// clientCertExpiry is when the client's own certificate expires (Unix time).
	clientCertExpiry *prometheus.GaugeVec
	checks           *prometheus.CounterVec
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
			Help: "gRPC calls by channel (direct or xds) and the server that answered",
		}, []string{"client", "example", "channel", "server", "security", "code"}),
		certExpiry: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "demo_server_cert_expiry_seconds",
			Help: "When the certificate the server presented expires (Unix time)",
		}, []string{"client", "example", "channel", "server"}),
		clientCertExpiry: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "demo_client_cert_expiry_seconds",
			Help: "When the certificate this client presents expires (Unix time)",
		}, []string{"client", "example"}),
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
		for _, srv := range []string{"server1", "server2"} {
			m.calls.WithLabelValues(m.client, m.example, ch, srv, "tls", "OK")
		}
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
func (m *Metrics) recordRPC(channel, method, region string, info CallInfo, code string, d time.Duration) {
	m.rpcs.WithLabelValues(m.client, m.example, channel, region, method, code).Observe(d.Seconds())
	m.calls.WithLabelValues(m.client, m.example, channel, info.Server, info.Security, code).Inc()
	if info.Cert != nil {
		m.certExpiry.WithLabelValues(m.client, m.example, channel, info.Server).Set(float64(info.Cert.NotAfter.Unix()))
	}
}

func (m *Metrics) recordCheck(passed bool) {
	result := "fail"
	if passed {
		result = "pass"
	}
	m.checks.WithLabelValues(m.client, m.example, result).Inc()
}
