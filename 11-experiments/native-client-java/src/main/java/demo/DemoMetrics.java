package demo;

import io.prometheus.metrics.core.metrics.Counter;
import io.prometheus.metrics.core.metrics.Histogram;
import io.prometheus.metrics.expositionformats.ExpositionFormatWriter;
import io.prometheus.metrics.expositionformats.ExpositionFormats;
import io.prometheus.metrics.instrumentation.jvm.JvmMetrics;
import io.prometheus.metrics.model.registry.PrometheusRegistry;
import java.io.IOException;
import java.io.OutputStream;
import org.springframework.stereotype.Component;

/**
 * In-app metrics, served at /metrics by {@link CrudController}. Names and labels are identical in every
 * client of every example so the Grafana dashboards compare them directly.
 *
 * <ul>
 *   <li>{@code demo_op_latency_seconds{client,example,region,op,code}} times one /crud operation's
 *       GemFire work (a gRPC RPC, or the Region API call(s) that do the same job), not the HTTP
 *       request around it. Failures are timed too, with the gRPC status code or exception class as
 *       {@code code} ("OK" on success). k6 measures the end-to-end request separately.
 *   <li>{@code demo_rpc_duration_seconds{client,example,region,method,code}} times every gRPC call
 *       the client makes, streaming ones and the demo's included. It's recorded by a gRPC client
 *       interceptor, so the native client never has any.
 * </ul>
 */
@Component
final class DemoMetrics {

  /** 50µs .. 2.5s: fine-grained at the low end where unary cache ops live. */
  private static final double[] LATENCY_BUCKETS = {
    0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
    0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5
  };

  private final String client;
  private final String example;
  private final String[] regions;
  private final Histogram latency;
  private final Histogram rpcs;
  private final Counter checks;

  DemoMetrics(Settings cfg) {
    this.client = cfg.client();
    this.example = cfg.example();
    this.regions = new String[] {cfg.region(), cfg.replicateRegion()};
    JvmMetrics.builder().register();
    latency =
        Histogram.builder()
            .name("demo_op_latency_seconds")
            .help("Latency of one /crud operation's GemFire work, by result code (excludes HTTP)")
            .labelNames("client", "example", "region", "op", "code")
            .classicUpperBounds(LATENCY_BUCKETS)
            .classicOnly()
            .register();
    rpcs =
        Histogram.builder()
            .name("demo_rpc_duration_seconds")
            .help("Duration of each gRPC call the client made, by method and status code")
            .labelNames("client", "example", "region", "method", "code")
            .classicUpperBounds(LATENCY_BUCKETS)
            .classicOnly()
            .register();
    checks =
        Counter.builder()
            .name("demo_checks")
            .help("Demo assertions by result")
            .labelNames("client", "example", "result")
            .register();
    // Pre-create the series so the dashboard shows 0 instead of "no data". For counters and
    // histograms this also matters for increase(): Prometheus can't see a series' first
    // observation, so a series that first appears already at 1 would count as 0.
    for (String r : regions) {
      for (String op : Crud.OPS.values()) latency.labelValues(client, example, r, op, "OK");
    }
    checks.labelValues(client, example, "pass");
    checks.labelValues(client, example, "fail");
  }

  private final ExpositionFormats formats = ExpositionFormats.init();

  /**
   * Writes every metric to {@code out} in the format the scraper asked for (its Accept header),
   * the same negotiation the Prometheus client's own HTTP server does. Returns the content type.
   */
  String scrape(String accept, OutputStream out) throws IOException {
    ExpositionFormatWriter writer = formats.findWriter(accept);
    writer.write(out, PrometheusRegistry.defaultRegistry.scrape());
    return writer.getContentType();
  }

  /** One /crud operation: code is "OK", a gRPC status code, or an exception class name. */
  void recordOp(String region, String op, String code, long nanos) {
    latency.labelValues(client, example, region, op, code).observe(nanos / 1e9);
  }

  /** Pre-creates the OK series of every gRPC method in these regions (see the constructor). */
  void initRpcs(Iterable<String> methods, String... regions) {
    for (String method : methods) {
      for (String r : regions) rpcs.labelValues(client, example, r, method, "OK");
    }
  }

  /** One gRPC call (recorded by the gRPC client's interceptor). */
  void recordRpc(String method, String rpcRegion, String code, long nanos) {
    rpcs.labelValues(client, example, rpcRegion, method, code).observe(nanos / 1e9);
  }


  void recordCheck(boolean passed) {
    checks.labelValues(client, example, passed ? "pass" : "fail").inc();
  }
}
