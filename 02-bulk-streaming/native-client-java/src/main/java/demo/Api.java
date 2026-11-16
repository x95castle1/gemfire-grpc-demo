package demo;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Supplier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.boot.availability.ApplicationAvailability;
import org.springframework.boot.availability.ReadinessState;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.method.annotation.MethodArgumentTypeMismatchException;

/**
 * The HTTP API that load tests (k6) and humans call. Identical in every Java client. Only the
 * {@link Bulk} bean differs (gRPC streams vs the native bulk Region API), and it's wired up in
 * each client's {@code Application}.
 *
 * <pre>
 *   GET  /                                       endpoint list
 *   GET  /health                                 200 once the startup demo has run and the API takes traffic
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
 * calls at a time (1 = a plain loop). It's the baseline that shows what streaming buys. Every /bulk response carries
 * {@code X-GemFire-Micros} (the time spent in GemFire calls alone) and {@code X-Entries} (entries moved). k6 records both.
 */
@RestController
class Api implements ApplicationRunner {

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

  private static final MediaType TEXT = MediaType.parseMediaType("text/plain;charset=UTF-8");
  private static final Logger log = LoggerFactory.getLogger(Api.class);

  private final Config cfg;
  private final Metrics metrics;
  private final Bulk bulk;
  private final Supplier<Checks.Report> demo;
  private final ApplicationAvailability availability;
  private volatile Checks.Report lastDemo;

  Api(Config cfg, Metrics metrics, Bulk bulk, Supplier<Checks.Report> demo, ApplicationAvailability availability) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.bulk = bulk;
    this.demo = demo;
    this.availability = availability;
  }

  /**
   * Runs the startup demo. Spring Boot calls runners after Tomcat is listening but before it
   * marks the app ready, so /health stays 503 (and Docker keeps the client "starting") until the
   * demo is done.
   */
  @Override
  public void run(ApplicationArguments args) {
    log.info("{} ({}) serving region {}", cfg.client(), cfg.example(), cfg.region());
    if (cfg.demoOnStart()) runDemo();
  }

  /** Runs the demo now and remembers the result for GET /demo. */
  synchronized Checks.Report runDemo() {
    lastDemo = demo.get();
    return lastDemo;
  }

  /** Chunk size for {@code batch} entries of {@code size} bytes: at most ~1 MiB of values. */
  static int chunk(int batch, int size) {
    return Math.max(1, Math.min(batch, MAX_CHUNK_BYTES / Math.max(1, size)));
  }

  // ---------------------------------------------------------------------------------------------

  @GetMapping("/")
  ResponseEntity<String> index() {
    String text = cfg.client() + " (" + cfg.example() + "), region " + cfg.region() + "\n\n"
        + "GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n"
        + "POST /bulk/put?start=0&count=10000&batch=500&size=" + cfg.payloadBytes() + "\n"
        + "POST /bulk/get?start=0&count=10000&batch=500\n"
        + "POST /bulk/remove?start=0&count=10000&batch=500\n"
        + "GET  /bulk/keyset\n"
        + "POST /bulk/reset\n\n"
        + "put/get/remove also take mode=stream|unary and inflight=1.." + MAX_INFLIGHT + " (unary only), e.g.\n"
        + "POST /bulk/put?count=2000&mode=unary&inflight=16\n";
    return ResponseEntity.ok().contentType(TEXT).body(text);
  }

  @GetMapping("/health")
  ResponseEntity<String> health() {
    boolean ready = availability.getReadinessState() == ReadinessState.ACCEPTING_TRAFFIC;
    return ResponseEntity.status(ready ? 200 : 503).contentType(TEXT).body(ready ? "ok" : "starting");
  }

  @GetMapping("/metrics")
  ResponseEntity<byte[]> metrics(@RequestHeader(name = "Accept", required = false) String accept) throws IOException {
    ByteArrayOutputStream out = new ByteArrayOutputStream(64 * 1024);
    String type = metrics.scrape(accept, out);
    return ResponseEntity.ok().header("Content-Type", type).body(out.toByteArray());
  }

  @GetMapping("/demo")
  Map<String, Object> lastDemo() {
    Checks.Report r = lastDemo;
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("ran", r != null);
    if (r != null) {
      body.put("passed", r.passed());
      body.put("failed", r.failed());
    }
    return body;
  }

  @PostMapping("/demo")
  ResponseEntity<String> demo() {
    Checks.Report r = runDemo();
    return ResponseEntity.status(r.failed() == 0 ? 200 : 500).contentType(TEXT).body(r.text());
  }

  @RequestMapping(path = "/bulk/reset", method = {RequestMethod.GET, RequestMethod.POST})
  Map<String, Object> reset() throws Exception {
    long t0 = System.nanoTime();
    long removed = bulk.reset();
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("op", "Reset");
    body.put("removed", removed);
    body.put("micros", (System.nanoTime() - t0) / 1000);
    return body;
  }

  @RequestMapping(path = "/bulk/{segment}", method = {RequestMethod.GET, RequestMethod.POST})
  ResponseEntity<Map<String, Object>> bulk(
      @PathVariable String segment,
      @RequestParam(defaultValue = "0") int start,
      @RequestParam(defaultValue = "10000") int count,
      @RequestParam(defaultValue = "500") int batch,
      @RequestParam(required = false) Integer size,
      @RequestParam(defaultValue = "stream") String mode,
      @RequestParam(defaultValue = "1") int inflight) {
    check("start", start, 0, Integer.MAX_VALUE - MAX_COUNT);
    check("count", count, 0, MAX_COUNT);
    check("batch", batch, 1, 100_000);
    int bytes = size == null ? cfg.payloadBytes() : size;
    check("size", bytes, 0, MAX_PAYLOAD_BYTES);
    if (!mode.equals("stream") && !mode.equals("unary")) throw new IllegalArgumentException("mode must be stream or unary");
    boolean unary = mode.equals("unary");
    check("inflight", inflight, 1, MAX_INFLIGHT);

    Map<String, Object> body = new LinkedHashMap<>();
    String stream = OPS.get(segment);
    if (stream == null) {
      body.put("error", "unknown op: " + segment);
      body.put("ops", "GET /");
      return ResponseEntity.status(404).body(body);
    }
    // KeySetStream has no unary counterpart: no single-key RPC lists keys.
    if (unary && !UNARY.containsKey(stream)) throw new IllegalArgumentException(segment + " has no unary mode");
    String op = unary ? UNARY.get(stream) : stream;

    Result result;
    long t0 = System.nanoTime();
    try {
      result = switch (op) {
        case "PutStream" -> bulk.put(start, count, batch, bytes);
        case "GetStream" -> bulk.get(start, count, batch);
        case "RemoveStream" -> bulk.remove(start, count, batch);
        case "Put" -> bulk.unaryPut(start, count, bytes, inflight);
        case "Get" -> bulk.unaryGet(start, count, inflight);
        case "Remove" -> bulk.unaryRemove(start, count, inflight);
        default -> bulk.keySet();
      };
    } catch (Exception e) {
      String code = bulk.errorCode(e);
      metrics.recordOp(op, code, System.nanoTime() - t0);   // failures are timed too
      body.put("op", op);
      body.put("error", code);
      body.put("message", String.valueOf(e.getMessage()));
      return ResponseEntity.status(502).header("X-Error-Code", code).body(body);
    }
    long nanos = System.nanoTime() - t0;
    metrics.recordOp(op, "OK", nanos);
    metrics.recordEntries(op, result.entries());

    body.put("op", op);
    body.put("mode", mode);
    if (unary) {
      body.put("inflight", inflight);
      body.put("start", start);
      body.put("requested", count);
    } else if (!op.equals("KeySetStream")) {
      body.put("start", start);
      body.put("requested", count);
      body.put("batch", op.equals("PutStream") ? chunk(batch, bytes) : batch);
    }
    body.put("entries", result.entries());
    body.put("messages", result.messages());
    body.put("micros", nanos / 1000);
    return ResponseEntity.ok()
        .header("X-GemFire-Micros", Long.toString(nanos / 1000))
        .header("X-Entries", Long.toString(result.entries()))
        .body(body);
  }

  @ExceptionHandler(IllegalArgumentException.class)
  ResponseEntity<Map<String, Object>> badRequest(IllegalArgumentException e) {
    return ResponseEntity.badRequest().body(Map.of("error", String.valueOf(e.getMessage())));
  }

  @ExceptionHandler(MethodArgumentTypeMismatchException.class)
  ResponseEntity<Map<String, Object>> notANumber(MethodArgumentTypeMismatchException e) {
    return ResponseEntity.badRequest().body(Map.of("error", e.getName() + " must be a number"));
  }

  @ExceptionHandler(Exception.class)
  ResponseEntity<Map<String, Object>> failed(Exception e) {
    return ResponseEntity.status(500).body(Map.of("error", String.valueOf(e)));
  }

  private static void check(String name, int v, int min, int max) {
    if (v < min || v > max) throw new IllegalArgumentException(name + " must be " + min + ".." + max);
  }
}
