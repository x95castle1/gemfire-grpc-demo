package demo;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.function.Supplier;

/**
 * The HTTP API that load tests (k6) and humans call. Identical in every Java client. Only the
 * {@link Crud} implementation differs (gRPC vs native).
 *
 * <pre>
 *   GET  /                          endpoint list
 *   GET  /health                    200 once the GemFire connection is up
 *   GET  /metrics                   Prometheus metrics
 *   GET  /demo                      result of the last demo run
 *   POST /demo                      run the narrated demo; 200 = all checks passed
 *   POST /crud/preload?count=&size= write count keys (bulk) so reads hit existing entries
 *   GET  /crud/{op}?key=&size=      read ops:  get, contains-key, entry-count
 *   POST /crud/{op}?key=&size=      write ops: put, get-and-put, remove, remove-if, ...
 * </pre>
 *
 * Every /crud response carries {@code X-GemFire-Micros}: the time spent in the GemFire call
 * alone. k6 records it next to its own end-to-end timing.
 */
final class Api {

  /** One client's implementation of the 13 unary operations. */
  interface Crud {
    /**
     * Runs one operation on {@code key} with a {@code payloadBytes} value (for writes) and
     * returns its result: value size in bytes (or null when absent), a boolean, or a count.
     */
    Object execute(String op, String key, int payloadBytes) throws Exception;

    /** Writes keys 0..count-1 with a bulk API. Returns the number written. */
    int preload(int count, int payloadBytes) throws Exception;

    /** Maps a failure to a short label: a gRPC status code or an exception class name. */
    String errorCode(Throwable t);
  }

  /** URL segment → RPC name (used as the `op` metric label in every client). */
  static final Map<String, String> OPS = new LinkedHashMap<>();

  /** RPC name → JSON field name of its result (absent for Put). */
  static final Map<String, String> RESULT_FIELD = new HashMap<>();

  static {
    op("get", "Get", "valueBytes");
    op("put", "Put", null);
    op("get-and-put", "GetAndPut", "previousBytes");
    op("remove", "Remove", "removed");
    op("remove-if", "RemoveIf", "removed");
    op("get-and-remove", "GetAndRemove", "removedBytes");
    op("put-if-absent", "PutIfAbsent", "inserted");
    op("put-if-absent-and-get", "PutIfAbsentAndGet", "valueBytes");
    op("replace", "Replace", "replaced");
    op("replace-if", "ReplaceIf", "replaced");
    op("get-and-replace", "GetAndReplace", "previousBytes");
    op("contains-key", "ContainsKey", "contains");
    op("entry-count", "EntryCount", "size");
  }

  private static void op(String path, String rpc, String field) {
    OPS.put(path, rpc);
    if (field != null) RESULT_FIELD.put(rpc, field);
  }

  private static final int MAX_PAYLOAD_BYTES = 16 * 1024 * 1024;

  private final Config cfg;
  private final Metrics metrics;
  private final Crud crud;
  private final Supplier<Checks.Report> demo;
  private volatile Checks.Report lastDemo;
  private HttpServer server;

  Api(Config cfg, Metrics metrics, Crud crud, Supplier<Checks.Report> demo) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.crud = crud;
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
    server.createContext("/crud/", this::crud);
    server.start();
    System.out.printf("[%s] HTTP API listening on :%d (%d threads)%n", cfg.client(), cfg.port(), cfg.httpThreads());
  }

  /** Runs the demo now (used at startup) and remembers the result for GET /demo. */
  synchronized Checks.Report runDemo() {
    lastDemo = demo.get();
    return lastDemo;
  }

  // ---------------------------------------------------------------------------------------------

  private void index(HttpExchange ex) throws IOException {
    if (!ex.getRequestURI().getPath().equals("/")) {
      send(ex, 404, "application/json", "{\"error\":\"not found\"}");
      return;
    }
    StringBuilder sb = new StringBuilder();
    sb.append(cfg.client()).append(" (").append(cfg.example()).append(")\n\n");
    sb.append("GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n");
    sb.append("POST /crud/preload?count=10000&size=").append(cfg.payloadBytes()).append('\n');
    for (Map.Entry<String, String> e : OPS.entrySet()) {
      boolean read = isRead(e.getValue());
      sb.append(read ? "GET  " : "POST ").append("/crud/").append(e.getKey());
      sb.append(e.getValue().equals("EntryCount") ? "" : "?key=42").append('\n');
    }
    send(ex, 200, "text/plain", sb.toString());
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

  private void crud(HttpExchange ex) throws IOException {
    String segment = ex.getRequestURI().getPath().substring("/crud/".length());
    Map<String, String> q = query(ex);
    try {
      int size = q.containsKey("size") ? Integer.parseInt(q.get("size")) : cfg.payloadBytes();
      if (size < 0 || size > MAX_PAYLOAD_BYTES) throw new IllegalArgumentException("size must be 0.." + MAX_PAYLOAD_BYTES);

      if (segment.equals("preload")) {
        int count = Integer.parseInt(q.getOrDefault("count", "10000"));
        long start = System.nanoTime();
        int written = crud.preload(count, size);
        long micros = (System.nanoTime() - start) / 1000;
        ex.getResponseHeaders().set("X-GemFire-Micros", Long.toString(micros));
        send(ex, 200, "application/json", String.format("{\"op\":\"Preload\",\"written\":%d,\"micros\":%d}", written, micros));
        return;
      }

      String op = OPS.get(segment);
      if (op == null) {
        send(ex, 404, "application/json", "{\"error\":\"unknown op: " + json(segment) + "\",\"ops\":\"GET /\"}");
        return;
      }
      String key = q.get("key");
      if (key == null && !op.equals("EntryCount")) throw new IllegalArgumentException("key is required");

      Object result;
      long start = System.nanoTime();
      try {
        result = crud.execute(op, key == null ? null : cfg.keyPrefix() + key, size);
      } catch (Exception e) {
        String code = crud.errorCode(e);
        metrics.recordError(op, code);
        ex.getResponseHeaders().set("X-Error-Code", code);
        send(ex, 502, "application/json", String.format(
            "{\"op\":\"%s\",\"error\":\"%s\",\"message\":\"%s\"}", op, code, json(String.valueOf(e.getMessage()))));
        return;
      }
      long nanos = System.nanoTime() - start;
      metrics.recordLatency(op, nanos);

      StringBuilder body = new StringBuilder().append("{\"op\":\"").append(op).append('"');
      if (key != null) body.append(",\"key\":\"").append(json(key)).append('"');
      String field = RESULT_FIELD.get(op);
      if (field != null) body.append(",\"").append(field).append("\":").append(result);
      body.append(",\"micros\":").append(nanos / 1000).append('}');
      ex.getResponseHeaders().set("X-GemFire-Micros", Long.toString(nanos / 1000));
      send(ex, 200, "application/json", body.toString());
    } catch (IllegalArgumentException e) {
      send(ex, 400, "application/json", "{\"error\":\"" + json(String.valueOf(e.getMessage())) + "\"}");
    } catch (Exception e) {
      send(ex, 500, "application/json", "{\"error\":\"" + json(String.valueOf(e)) + "\"}");
    }
  }

  static boolean isRead(String op) {
    return op.equals("Get") || op.equals("ContainsKey") || op.equals("EntryCount");
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
