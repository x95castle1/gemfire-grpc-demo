package demo;

import io.prometheus.metrics.core.metrics.Counter;
import io.prometheus.metrics.core.metrics.Histogram;
import io.prometheus.metrics.exporter.httpserver.MetricsHandler;
import io.prometheus.metrics.instrumentation.jvm.JvmMetrics;

/**
 * In-app metrics, served at /metrics by {@link Api}. Names and labels are identical in every
 * client of every example so the Grafana dashboards compare them directly.
 *
 * <p>{@code demo_op_latency_seconds} times only the GemFire call (gRPC RPC or Region API call),
 * not the HTTP request around it. k6 measures the end-to-end request separately.
 */
final class Metrics {

  /** 50µs .. 2.5s: fine-grained at the low end where unary cache ops live. */
  private static final double[] LATENCY_BUCKETS = {
    0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
    0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5
  };

  private final String client;
  private final String example;
  private final Histogram latency;
  private final Counter errors;
  private final Counter checks;

  Metrics(Config cfg) {
    this.client = cfg.client();
    this.example = cfg.example();
    JvmMetrics.builder().register();
    latency =
        Histogram.builder()
            .name("demo_op_latency_seconds")
            .help("Latency of one GemFire call made by the client (excludes HTTP)")
            .labelNames("client", "example", "op")
            .classicUpperBounds(LATENCY_BUCKETS)
            .classicOnly()
            .register();
    errors =
        Counter.builder()
            .name("demo_errors")
            .help("Failed GemFire calls by status code / exception type")
            .labelNames("client", "example", "op", "code")
            .register();
    checks =
        Counter.builder()
            .name("demo_checks")
            .help("Demo assertions by result")
            .labelNames("client", "example", "result")
            .register();
    // Pre-create the series so the dashboard shows 0 instead of "no data".
    checks.labelValues(client, example, "pass");
    checks.labelValues(client, example, "fail");
  }

  MetricsHandler handler() {
    return new MetricsHandler();
  }

  void recordLatency(String op, long nanos) {
    latency.labelValues(client, example, op).observe(nanos / 1e9);
  }

  void recordError(String op, String code) {
    errors.labelValues(client, example, op, code).inc();
  }

  void recordCheck(boolean passed) {
    checks.labelValues(client, example, passed ? "pass" : "fail").inc();
  }
}
