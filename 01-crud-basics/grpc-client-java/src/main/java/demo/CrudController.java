package demo;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.LinkedHashMap;
import java.util.Map;
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
 * {@link Crud} bean behind it differs (gRPC vs native).
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
class CrudController {

  private static final MediaType TEXT = MediaType.parseMediaType("text/plain;charset=UTF-8");

  private final Settings cfg;
  private final DemoMetrics metrics;
  private final Crud crud;
  private final DemoRunner demo;
  private final ApplicationAvailability availability;

  CrudController(Settings cfg, DemoMetrics metrics, Crud crud, DemoRunner demo, ApplicationAvailability availability) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.crud = crud;
    this.demo = demo;
    this.availability = availability;
  }

  @GetMapping("/")
  ResponseEntity<String> index() {
    StringBuilder sb = new StringBuilder();
    sb.append(cfg.client()).append(" (").append(cfg.example()).append(")\n\n");
    sb.append("GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n");
    sb.append("POST /crud/preload?count=10000&size=").append(cfg.payloadBytes()).append('\n');
    for (Map.Entry<String, String> e : Crud.OPS.entrySet()) {
      boolean read = Crud.isRead(e.getValue());
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
    Checks.Report r = demo.last();
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("ran", r != null);
    if (r != null) {
      body.put("passed", r.passed());
      body.put("failed", r.failed());
    }
    return body;
  }

  @PostMapping("/demo")
  ResponseEntity<String> runDemo() {
    Checks.Report r = demo.runDemo();
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
    String op = Crud.OPS.get(segment);
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
    String field = Crud.RESULT_FIELD.get(op);
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

  private int size(Integer size) {
    int bytes = size == null ? cfg.payloadBytes() : size;
    if (bytes < 0 || bytes > Crud.MAX_PAYLOAD_BYTES) throw new IllegalArgumentException("size must be 0.." + Crud.MAX_PAYLOAD_BYTES);
    return bytes;
  }
}
