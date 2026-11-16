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
 * In-app metrics, served at /metrics by {@link BulkController}. Names and labels are identical in every
 * client of every example so the Grafana dashboards compare them directly.
 *
 * <ul>
 *   <li>{@code demo_op_latency_seconds{client,example,region,op,code}} times one /bulk call's
 *       GemFire work (one gRPC stream, or the Region API calls that do the same job; in unary
 *       mode, all of the per-entry calls together, with op = Put/Get/Remove), not the HTTP
 *       request around it. Failures are timed too, with the gRPC status code or exception class as
 *       {@code code} ("OK" on success). k6 measures the end-to-end request separately.
 *   <li>{@code demo_entries_total{client,example,region,op}} counts the entries those calls moved
 *       (for GetStream, the entries found), so entries/s compares the two clients' throughput.
 *   <li>{@code demo_rpc_duration_seconds{client,example,region,method,code}} times every gRPC call
 *       the client makes, the demo's included. It's recorded by a gRPC client interceptor, so the
 *       native client never has any.
 *   <li>{@code demo_stream_messages_total{client,example,method,direction}} counts the messages
 *       the gRPC client sent and received on each kind of call (also from the interceptor).
 * </ul>
 */
@Component
final class DemoMetrics {

  /**
   * 50µs .. 60s: the same fine-grained low end as 01, plus the seconds a large bulk call can take.
   */
  private static final double[] LATENCY_BUCKETS = {
    0.00005, 0.0001, 0.00015, 0.0002, 0.0003, 0.0004, 0.0005, 0.00075, 0.001, 0.0015, 0.002,
    0.003, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5, 10, 30, 60
  };

  private final String client;
  private final String example;
  private final String region;
  private final Histogram latency;
  private final Counter entries;
  private final Histogram rpcs;
  private final Counter messages;
  private final Counter checks;

  DemoMetrics(Settings cfg) {
    this.client = cfg.client();
    this.example = cfg.example();
    this.region = cfg.region();
    JvmMetrics.builder().register();
    latency =
        Histogram.builder()
            .name("demo_op_latency_seconds")
            .help("Latency of one /bulk call's GemFire work, by result code (excludes HTTP)")
            .labelNames("client", "example", "region", "op", "code")
            .classicUpperBounds(LATENCY_BUCKETS)
            .classicOnly()
            .register();
    entries =
        Counter.builder()
            .name("demo_entries")
            .help("Entries moved by /bulk calls (GetStream: entries found)")
            .labelNames("client", "example", "region", "op")
            .register();
    rpcs =
        Histogram.builder()
            .name("demo_rpc_duration_seconds")
            .help("Duration of each gRPC call the client made, by method and status code")
            .labelNames("client", "example", "region", "method", "code")
            .classicUpperBounds(LATENCY_BUCKETS)
            .classicOnly()
            .register();
    messages =
        Counter.builder()
            .name("demo_stream_messages")
            .help("gRPC messages the client sent and received, by method")
            .labelNames("client", "example", "method", "direction")
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
    for (String op : Bulk.allOps()) {
      latency.labelValues(client, example, region, op, "OK");
      entries.labelValues(client, example, region, op);
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

  /** One /bulk call: code is "OK", a gRPC status code, or an exception class name. */
  void recordOp(String op, String code, long nanos) {
    latency.labelValues(client, example, region, op, code).observe(nanos / 1e9);
  }

  /** Entries one successful /bulk call moved. */
  void recordEntries(String op, long n) {
    entries.labelValues(client, example, region, op).inc(n);
  }

  /** Pre-creates the OK series of every gRPC method in these regions (see the constructor). */
  void initRpcs(Iterable<String> methods, String... regions) {
    for (String method : methods) {
      for (String r : regions) rpcs.labelValues(client, example, r, method, "OK");
      messages.labelValues(client, example, method, "sent");
      messages.labelValues(client, example, method, "received");
    }
  }

  /** One gRPC call (recorded by the gRPC client's interceptor). */
  void recordRpc(String method, String rpcRegion, String code, long nanos) {
    rpcs.labelValues(client, example, rpcRegion, method, code).observe(nanos / 1e9);
  }

  /** One gRPC message sent or received (direction "sent" | "received"). */
  void recordMessage(String method, String direction) {
    messages.labelValues(client, example, method, direction).inc();
  }

  void recordCheck(boolean passed) {
    checks.labelValues(client, example, passed ? "pass" : "fail").inc();
  }
}
