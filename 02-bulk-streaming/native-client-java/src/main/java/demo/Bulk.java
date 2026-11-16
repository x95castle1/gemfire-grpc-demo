package demo;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * One client's implementation of the bulk operations, served over HTTP by {@link BulkController}.
 * Each client's {@code *ClientApplication} supplies the bean: gRPC streams ({@code GrpcBulk}) or
 * the native bulk Region API ({@code NativeBulk}).
 */
interface Bulk {

  /**
   * What one bulk call did: entries moved, and the number of messages (gRPC: stream messages
   * sent for put/get/remove, received for keyset) or round trips (native: Region API calls;
   * unary mode: one call per entry).
   */
  record Result(long entries, long messages) {}

  /** URL segment → RPC name (used as the `op` metric label in every client). */
  Map<String, String> OPS = ordered(
      "put", "PutStream",
      "get", "GetStream",
      "remove", "RemoveStream",
      "keyset", "KeySetStream");

  /** Streaming RPC → the unary RPC that does the same work one entry at a time (mode=unary). */
  Map<String, String> UNARY = ordered(
      "PutStream", "Put",
      "GetStream", "Get",
      "RemoveStream", "Remove");

  int MAX_INFLIGHT = 256;
  int MAX_PAYLOAD_BYTES = 16 * 1024 * 1024;
  int MAX_COUNT = 5_000_000;
  /** Chunks are also capped at this many bytes of values, whatever {@code batch} says. */
  int MAX_CHUNK_BYTES = 1 << 20;

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

  /** Every `op` label value: the 4 streams and the 3 unary RPCs. */
  static List<String> allOps() {
    List<String> all = new ArrayList<>(OPS.values());
    all.addAll(UNARY.values());
    return all;
  }

  /** Chunk size for {@code batch} entries of {@code size} bytes: at most ~1 MiB of values. */
  static int chunk(int batch, int size) {
    return Math.max(1, Math.min(batch, MAX_CHUNK_BYTES / Math.max(1, size)));
  }

  private static Map<String, String> ordered(String... keysAndValues) {
    Map<String, String> map = new LinkedHashMap<>();
    for (int i = 0; i < keysAndValues.length; i += 2) map.put(keysAndValues[i], keysAndValues[i + 1]);
    return Collections.unmodifiableMap(map);
  }
}
