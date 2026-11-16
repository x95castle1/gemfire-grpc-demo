package demo;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.HashMap;
import java.util.LinkedHashMap;
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
 * {@link Crud} bean differs (gRPC vs native), and it's wired up in each client's
 * {@code Application}.
 *
 * <pre>
 *   GET  /                          endpoint list
 *   GET  /health                    200 once the startup demo has run and the API takes traffic
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
@RestController
class Api implements ApplicationRunner {

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
  private static final MediaType TEXT = MediaType.parseMediaType("text/plain;charset=UTF-8");
  private static final Logger log = LoggerFactory.getLogger(Api.class);

  private final Config cfg;
  private final Metrics metrics;
  private final Crud crud;
  private final Supplier<Checks.Report> demo;
  private final ApplicationAvailability availability;
  private volatile Checks.Report lastDemo;

  Api(Config cfg, Metrics metrics, Crud crud, Supplier<Checks.Report> demo, ApplicationAvailability availability) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.crud = crud;
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

  // ---------------------------------------------------------------------------------------------

  @GetMapping("/")
  ResponseEntity<String> index() {
    StringBuilder sb = new StringBuilder();
    sb.append(cfg.client()).append(" (").append(cfg.example()).append(")\n\n");
    sb.append("GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n");
    sb.append("POST /crud/preload?count=10000&size=").append(cfg.payloadBytes()).append('\n');
    for (Map.Entry<String, String> e : OPS.entrySet()) {
      boolean read = isRead(e.getValue());
      sb.append(read ? "GET  " : "POST ").append("/crud/").append(e.getKey());
      sb.append(e.getValue().equals("EntryCount") ? "" : "?key=42").append('\n');
    }
    return ResponseEntity.ok().contentType(TEXT).body(sb.toString());
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

  @RequestMapping(path = "/crud/preload", method = {RequestMethod.GET, RequestMethod.POST})
  ResponseEntity<Map<String, Object>> preload(
      @RequestParam(defaultValue = "10000") int count, @RequestParam(required = false) Integer size) throws Exception {
    int bytes = size(size);
    long start = System.nanoTime();
    int written = crud.preload(count, bytes);
    long micros = (System.nanoTime() - start) / 1000;
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("op", "Preload");
    body.put("written", written);
    body.put("micros", micros);
    return ResponseEntity.ok().header("X-GemFire-Micros", Long.toString(micros)).body(body);
  }

  @RequestMapping(path = "/crud/{segment}", method = {RequestMethod.GET, RequestMethod.POST})
  ResponseEntity<Map<String, Object>> crud(
      @PathVariable String segment,
      @RequestParam(required = false) String key,
      @RequestParam(required = false) Integer size) {
    int bytes = size(size);
    String op = OPS.get(segment);
    Map<String, Object> body = new LinkedHashMap<>();
    if (op == null) {
      body.put("error", "unknown op: " + segment);
      body.put("ops", "GET /");
      return ResponseEntity.status(404).body(body);
    }
    if (key == null && !op.equals("EntryCount")) throw new IllegalArgumentException("key is required");

    Object result;
    long start = System.nanoTime();
    try {
      result = crud.execute(op, key == null ? null : cfg.keyPrefix() + key, bytes);
    } catch (Exception e) {
      String code = crud.errorCode(e);
      metrics.recordOp(op, code, System.nanoTime() - start);   // failures are timed too
      body.put("op", op);
      body.put("error", code);
      body.put("message", String.valueOf(e.getMessage()));
      return ResponseEntity.status(502).header("X-Error-Code", code).body(body);
    }
    long nanos = System.nanoTime() - start;
    metrics.recordOp(op, "OK", nanos);

    body.put("op", op);
    if (key != null) body.put("key", key);
    String field = RESULT_FIELD.get(op);
    if (field != null) body.put(field, result);
    body.put("micros", nanos / 1000);
    return ResponseEntity.ok().header("X-GemFire-Micros", Long.toString(nanos / 1000)).body(body);
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

  static boolean isRead(String op) {
    return op.equals("Get") || op.equals("ContainsKey") || op.equals("EntryCount");
  }

  private int size(Integer size) {
    int bytes = size == null ? cfg.payloadBytes() : size;
    if (bytes < 0 || bytes > MAX_PAYLOAD_BYTES) throw new IllegalArgumentException("size must be 0.." + MAX_PAYLOAD_BYTES);
    return bytes;
  }
}
