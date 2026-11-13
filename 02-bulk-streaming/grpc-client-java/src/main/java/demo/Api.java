package demo;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.function.Supplier;

/**
 * The HTTP API that load tests (k6) and humans call. Identical in every Java client. Only the
 * {@link Bulk} implementation differs (gRPC streams vs the native bulk Region API).
 *
 * <pre>
 *   GET  /                                       endpoint list
 *   GET  /health                                 200 once the GemFire connection is up
 *   GET  /metrics                                Prometheus metrics
 *   GET  /demo                                   result of the last demo run
 *   POST /demo                                   run the narrated demo; 200 = all checks passed
 *   POST /bulk/put?start=&count=&batch=&size=    write keys start..start+count-1
 *   POST /bulk/get?start=&count=&batch=          read them back (entries = how many were found)
 *   POST /bulk/remove?start=&count=&batch=       remove them
 *   GET  /bulk/keyset                            every key in the client's region
 *   POST /bulk/reset                             remove everything in the client's region
 *
 *   put/get/remove also take mode=stream (default) | unary, and inflight=N (unary only)
 * </pre>
 *
 * {@code batch} is the chunk size on both sides: entries per stream message for gRPC, entries per
 * putAll/getAll/removeAll call for native. {@code mode=unary} does the same work without bulk
 * calls, one Put/Get/Remove per entry (native: region.put/get/remove), with {@code inflight}
 * calls at a time (1 = a plain loop). It's the baseline that shows what streaming buys. Every /bulk response carries {@code X-GemFire-Micros}
 * (the time spent in GemFire calls alone) and {@code X-Entries} (entries moved). k6 records both.
 */
final class Api {

  /** One client's implementation of the bulk operations. */
  interface Bulk {
    /** Writes keys start..start+count-1 with {@code size}-byte values, {@code batch} per chunk. */
    Result put(int start, int count, int batch, int size) throws Exception;

    /** Reads those keys back. {@link Result#entries()} is the number found. */
    Result get(int start, int count, int batch) throws Exception;

    /** Removes those keys. */
    Result remove(int start, int count, int batch) throws Exception;

    /** Writes the same keys as {@link #put}, one unary call per entry, {@code inflight} at a time. */
    Result unaryPut(int start, int count, int size, int inflight) throws Exception;

    /** Reads them one unary call per key. {@link Result#entries()} is the number found. */
    Result unaryGet(int start, int count, int inflight) throws Exception;

    /** Removes them one unary call per key. */
    Result unaryRemove(int start, int count, int inflight) throws Exception;

    /** Fetches every key in the region. {@link Result#entries()} is the number of keys. */
    Result keySet() throws Exception;

    /** Removes everything in the region. Returns the number of entries removed. */
    long reset() throws Exception;

    /** Maps a failure to a short label: a gRPC status code or an exception class name. */
    String errorCode(Throwable t);
  }

  /**
   * What one bulk call did: entries moved, and the number of messages (gRPC: stream messages
   * sent for put/get/remove, received for keyset) or round trips (native: Region API calls;
   * unary mode: one call per entry).
   */
  record Result(long entries, long messages) {}

  /** URL segment → RPC name (used as the `op` metric label in every client). */
  static final Map<String, String> OPS = new LinkedHashMap<>();

  /** Streaming RPC → the unary RPC that does the same work one entry at a time (mode=unary). */
  static final Map<String, String> UNARY = new LinkedHashMap<>();

  static {
    OPS.put("put", "PutStream");
    OPS.put("get", "GetStream");
    OPS.put("remove", "RemoveStream");
    OPS.put("keyset", "KeySetStream");
    UNARY.put("PutStream", "Put");
    UNARY.put("GetStream", "Get");
    UNARY.put("RemoveStream", "Remove");
  }

  /** Every `op` label value: the 4 streams and the 3 unary RPCs. */
  static List<String> allOps() {
    List<String> all = new ArrayList<>(OPS.values());
    all.addAll(UNARY.values());
    return all;
  }

  static final int MAX_INFLIGHT = 256;

  static final int MAX_PAYLOAD_BYTES = 16 * 1024 * 1024;
  static final int MAX_COUNT = 5_000_000;
  /** Chunks are also capped at this many bytes of values, whatever {@code batch} says. */
  static final int MAX_CHUNK_BYTES = 1 << 20;

  private final Config cfg;
  private final Metrics metrics;
  private final Bulk bulk;
  private final Supplier<Checks.Report> demo;
  private volatile Checks.Report lastDemo;
  private HttpServer server;

  Api(Config cfg, Metrics metrics, Bulk bulk, Supplier<Checks.Report> demo) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.bulk = bulk;
    this.demo = demo;
  }

  void start() throws IOException {
    server = HttpServer.create(new InetSocketAddress(cfg.port()), 1024);
    // A fixed pool of platform threads, the same in every client, so concurrency is identical
    // on both sides of the comparison.
    server.setExecutor(Executors.newFixedThreadPool(cfg.httpThreads()));
    server.createContext("/", this::index);
    server.createContext("/health", ex -> send(ex, 200, "text/plain", "ok"));
    server.createContext("/metrics", metrics.handler());
    server.createContext("/demo", this::demo);
    server.createContext("/bulk/", this::bulk);
    server.start();
    System.out.printf("[%s] HTTP API listening on :%d (%d threads)%n", cfg.client(), cfg.port(), cfg.httpThreads());
  }

  /** Runs the demo now (used at startup) and remembers the result for GET /demo. */
  synchronized Checks.Report runDemo() {
    lastDemo = demo.get();
    return lastDemo;
  }

  /** Chunk size for {@code batch} entries of {@code size} bytes: at most ~1 MiB of values. */
  static int chunk(int batch, int size) {
    return Math.max(1, Math.min(batch, MAX_CHUNK_BYTES / Math.max(1, size)));
  }

  // ---------------------------------------------------------------------------------------------

  private void index(HttpExchange ex) throws IOException {
    if (!ex.getRequestURI().getPath().equals("/")) {
      send(ex, 404, "application/json", "{\"error\":\"not found\"}");
      return;
    }
    String text = cfg.client() + " (" + cfg.example() + "), region " + cfg.region() + "\n\n"
        + "GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n"
        + "POST /bulk/put?start=0&count=10000&batch=500&size=" + cfg.payloadBytes() + "\n"
        + "POST /bulk/get?start=0&count=10000&batch=500\n"
        + "POST /bulk/remove?start=0&count=10000&batch=500\n"
        + "GET  /bulk/keyset\n"
        + "POST /bulk/reset\n\n"
        + "put/get/remove also take mode=stream|unary and inflight=1.." + MAX_INFLIGHT + " (unary only), e.g.\n"
        + "POST /bulk/put?count=2000&mode=unary&inflight=16\n";
    send(ex, 200, "text/plain", text);
  }

  private void demo(HttpExchange ex) throws IOException {
    if (ex.getRequestMethod().equals("POST")) {
      Checks.Report r = runDemo();
      send(ex, r.failed() == 0 ? 200 : 500, "text/plain; charset=utf-8", r.text());
    } else {
      Checks.Report r = lastDemo;
      String body = r == null ? "{\"ran\":false}"
          : String.format("{\"ran\":true,\"passed\":%d,\"failed\":%d}", r.passed(), r.failed());
      send(ex, 200, "application/json", body);
    }
  }

  private void bulk(HttpExchange ex) throws IOException {
    String segment = ex.getRequestURI().getPath().substring("/bulk/".length());
    Map<String, String> q = query(ex);
    try {
      int start = intParam(q, "start", 0, 0, Integer.MAX_VALUE - MAX_COUNT);
      int count = intParam(q, "count", 10_000, 0, MAX_COUNT);
      int batch = intParam(q, "batch", 500, 1, 100_000);
      int size = intParam(q, "size", cfg.payloadBytes(), 0, MAX_PAYLOAD_BYTES);
      String mode = q.getOrDefault("mode", "stream");
      if (!mode.equals("stream") && !mode.equals("unary")) throw new IllegalArgumentException("mode must be stream or unary");
      boolean unary = mode.equals("unary");
      int inflight = intParam(q, "inflight", 1, 1, MAX_INFLIGHT);

      if (segment.equals("reset")) {
        long t0 = System.nanoTime();
        long removed = bulk.reset();
        send(ex, 200, "application/json", String.format(
            "{\"op\":\"Reset\",\"removed\":%d,\"micros\":%d}", removed, (System.nanoTime() - t0) / 1000));
        return;
      }

      String stream = OPS.get(segment);
      if (stream == null) {
        send(ex, 404, "application/json", "{\"error\":\"unknown op: " + json(segment) + "\",\"ops\":\"GET /\"}");
        return;
      }
      // KeySetStream has no unary counterpart: no single-key RPC lists keys.
      if (unary && !UNARY.containsKey(stream)) throw new IllegalArgumentException(segment + " has no unary mode");
      String op = unary ? UNARY.get(stream) : stream;

      Result result;
      long t0 = System.nanoTime();
      try {
        result = switch (op) {
          case "PutStream" -> bulk.put(start, count, batch, size);
          case "GetStream" -> bulk.get(start, count, batch);
          case "RemoveStream" -> bulk.remove(start, count, batch);
          case "Put" -> bulk.unaryPut(start, count, size, inflight);
          case "Get" -> bulk.unaryGet(start, count, inflight);
          case "Remove" -> bulk.unaryRemove(start, count, inflight);
          default -> bulk.keySet();
        };
      } catch (Exception e) {
        String code = bulk.errorCode(e);
        metrics.recordOp(op, code, System.nanoTime() - t0);   // failures are timed too
        ex.getResponseHeaders().set("X-Error-Code", code);
        send(ex, 502, "application/json", String.format(
            "{\"op\":\"%s\",\"error\":\"%s\",\"message\":\"%s\"}", op, code, json(String.valueOf(e.getMessage()))));
        return;
      }
      long nanos = System.nanoTime() - t0;
      metrics.recordOp(op, "OK", nanos);
      metrics.recordEntries(op, result.entries());

      StringBuilder body = new StringBuilder().append("{\"op\":\"").append(op).append('"');
      body.append(",\"mode\":\"").append(mode).append('"');
      if (unary) {
        body.append(",\"inflight\":").append(inflight)
            .append(",\"start\":").append(start).append(",\"requested\":").append(count);
      } else if (!op.equals("KeySetStream")) {
        body.append(",\"start\":").append(start).append(",\"requested\":").append(count)
            .append(",\"batch\":").append(op.equals("PutStream") ? chunk(batch, size) : batch);
      }
      body.append(",\"entries\":").append(result.entries())
          .append(",\"messages\":").append(result.messages())
          .append(",\"micros\":").append(nanos / 1000).append('}');
      ex.getResponseHeaders().set("X-GemFire-Micros", Long.toString(nanos / 1000));
      ex.getResponseHeaders().set("X-Entries", Long.toString(result.entries()));
      send(ex, 200, "application/json", body.toString());
    } catch (IllegalArgumentException e) {
      send(ex, 400, "application/json", "{\"error\":\"" + json(String.valueOf(e.getMessage())) + "\"}");
    } catch (Exception e) {
      send(ex, 500, "application/json", "{\"error\":\"" + json(String.valueOf(e)) + "\"}");
    }
  }

  private static int intParam(Map<String, String> q, String name, int def, int min, int max) {
    String raw = q.get(name);
    if (raw == null || raw.isBlank()) return def;
    int v;
    try {
      v = Integer.parseInt(raw.trim());
    } catch (NumberFormatException e) {
      throw new IllegalArgumentException(name + " must be a number");
    }
    if (v < min || v > max) throw new IllegalArgumentException(name + " must be " + min + ".." + max);
    return v;
  }

  private static Map<String, String> query(HttpExchange ex) {
    Map<String, String> out = new HashMap<>();
    String raw = ex.getRequestURI().getRawQuery();
    if (raw == null) return out;
    for (String pair : raw.split("&")) {
      int i = pair.indexOf('=');
      if (i > 0) {
        out.put(URLDecoder.decode(pair.substring(0, i), StandardCharsets.UTF_8),
            URLDecoder.decode(pair.substring(i + 1), StandardCharsets.UTF_8));
      }
    }
    return out;
  }

  private static String json(String s) {
    StringBuilder sb = new StringBuilder(s.length() + 8);
    for (char c : s.toCharArray()) {
      switch (c) {
        case '"' -> sb.append("\\\"");
        case '\\' -> sb.append("\\\\");
        case '\n' -> sb.append("\\n");
        case '\r' -> sb.append("\\r");
        case '\t' -> sb.append("\\t");
        default -> {
          if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
          else sb.append(c);
        }
      }
    }
    return sb.toString();
  }

  private static void send(HttpExchange ex, int status, String type, String body) throws IOException {
    // Drain any request body so keep-alive connections can be reused.
    ex.getRequestBody().readAllBytes();
    byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
    ex.getResponseHeaders().set("Content-Type", type);
    ex.sendResponseHeaders(status, bytes.length);
    try (OutputStream os = ex.getResponseBody()) {
      os.write(bytes);
    }
  }
}
