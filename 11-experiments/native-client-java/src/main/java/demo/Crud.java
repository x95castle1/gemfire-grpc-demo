package demo;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * One client's implementation of the 13 unary operations, served over HTTP by
 * {@link CrudController}. Each client's {@code *ClientApplication} supplies the bean: gRPC
 * ({@code GrpcCrud}) or native ({@code NativeCrud}).
 */
interface Crud {

  /** URL segment → RPC name (used as the `op` metric label in every client). */
  Map<String, String> OPS = ordered(
      "get", "Get",
      "put", "Put",
      "get-and-put", "GetAndPut",
      "remove", "Remove",
      "remove-if", "RemoveIf",
      "get-and-remove", "GetAndRemove",
      "put-if-absent", "PutIfAbsent",
      "put-if-absent-and-get", "PutIfAbsentAndGet",
      "replace", "Replace",
      "replace-if", "ReplaceIf",
      "get-and-replace", "GetAndReplace",
      "contains-key", "ContainsKey",
      "entry-count", "EntryCount");

  /** RPC name → JSON field name of its result (absent for Put). */
  Map<String, String> RESULT_FIELD = ordered(
      "Get", "valueBytes",
      "GetAndPut", "previousBytes",
      "Remove", "removed",
      "RemoveIf", "removed",
      "GetAndRemove", "removedBytes",
      "PutIfAbsent", "inserted",
      "PutIfAbsentAndGet", "valueBytes",
      "Replace", "replaced",
      "ReplaceIf", "replaced",
      "GetAndReplace", "previousBytes",
      "ContainsKey", "contains",
      "EntryCount", "size");

  int MAX_PAYLOAD_BYTES = 16 * 1024 * 1024;

  /**
   * Runs one operation on {@code key} in {@code region} with a {@code payloadBytes} value (for
   * writes) and returns its result: value size in bytes (or null when absent), a boolean, or a count.
   */
  Object execute(String region, String op, String key, int payloadBytes) throws Exception;

  /** Writes keys start..start+count-1 into {@code region} with a bulk API. Returns the number written. */
  int preload(String region, int start, int count, int payloadBytes) throws Exception;

  /** Maps a failure to a short label: a gRPC status code or an exception class name. */
  String errorCode(Throwable t);

  /** Ops that only read (served on GET in the endpoint list). */
  static boolean isRead(String op) {
    return op.equals("Get") || op.equals("ContainsKey") || op.equals("EntryCount");
  }

  private static Map<String, String> ordered(String... keysAndValues) {
    Map<String, String> map = new LinkedHashMap<>();
    for (int i = 0; i < keysAndValues.length; i += 2) map.put(keysAndValues[i], keysAndValues[i + 1]);
    return Collections.unmodifiableMap(map);
  }
}
