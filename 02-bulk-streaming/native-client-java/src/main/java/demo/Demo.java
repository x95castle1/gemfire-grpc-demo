package demo;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import org.apache.geode.cache.Region;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientRegionShortcut;

/**
 * The same tour as the gRPC client's Demo, step for step, using the native bulk Region API
 * (putAll, getAll, removeAll, keySetOnServer) on a PROXY client region. Where a stream behavior
 * has no native counterpart, the step says so instead of asserting anything.
 */
final class Demo {

  private final ClientCache cache;
  private final Region<Object, Object> region;
  private final Checks c;
  private final Config cfg;

  Demo(ClientCache cache, Config cfg, Checks checks) {
    this.cache = cache;
    this.cfg = cfg;
    this.region = proxyRegion(cache, cfg.demoRegion());
    this.c = checks;
  }

  void run() {
    removeAll();   // re-runnable: start from an empty region

    c.section("1. PutStream: putAll as a client stream");
    int calls = 0;
    for (Map<Object, Object> chunk : chunks(range("item:", 0, 5_000), 500)) {
      region.putAll(chunk);
      calls++;
    }
    c.check("putAll of 5,000 entries in chunks of 500 → " + calls + " calls, each a blocking round trip", calls == 10);
    c.check("sizeOnServer → 5,000", region.sizeOnServer() == 5_000, region.sizeOnServer());

    c.section("2. The server re-batches what it receives");
    region.putAll(range("big:", 0, 2_500));
    c.check("one putAll of 2,500 entries → OK", true);
    c.note("the client splits it by bucket (PR single-hop) and sends each server its share");
    c.check("sizeOnServer → 7,500", region.sizeOnServer() == 7_500, region.sizeOnServer());

    c.section("3. Key types: string, int32 and int64 keys in one stream");
    Map<Object, Object> typed = new LinkedHashMap<>();
    typed.put("7", "string key");
    typed.put(7, "int32 key");
    typed.put(7L, "int64 key");
    region.putAll(typed);
    Map<Object, Object> back = region.getAll(typed.keySet());
    c.check("getAll returns all 3, each with its own key type and value", back.equals(typed),
        back.entrySet().stream().map(e -> e.getKey().getClass().getSimpleName() + "=" + e.getValue()).toList());

    c.section("4. GetStream: misses are omitted, not returned as nulls");
    List<Object> ask = new ArrayList<>();
    for (int i = 0; i < 6; i++) ask.add("item:" + i);
    for (int i = 0; i < 4; i++) ask.add("missing:" + i);
    Map<Object, Object> got = region.getAll(ask);
    long nulls = got.values().stream().filter(Objects::isNull).count();
    c.check("getAll for 10 keys (6 present, 4 absent) → all 10 keys back", got.size() == 10, got.size() + " entries");
    c.check("...the 4 absent ones mapped to null", nulls == 4, nulls + " nulls");
    c.note("gRPC GetStream returns only the 6 found entries");

    c.section("5. GetStream is bidirectional");
    c.note("no native counterpart: each getAll is one request and one response");

    c.section("6. KeySetStream: keySet as a server stream");
    Set<Object> keys = region.keySetOnServer();
    c.check("keySetOnServer returns every key: 7,503", keys.size() == 7_503, keys.size());
    c.note("in one response, built in client memory in one go (gRPC streams pages of bulk-batch-size keys)");

    c.section("7. RemoveStream: removeAll as a client stream");
    List<Object> all = new ArrayList<>(keys);
    for (int i = 0; i < 100; i++) all.add("never-existed:" + i);
    for (int i = 0; i < all.size(); i += 1_000) region.removeAll(all.subList(i, Math.min(all.size(), i + 1_000)));
    c.check("removeAll of every key, plus 100 that don't exist → OK", true);
    c.check("sizeOnServer → 0", region.sizeOnServer() == 0, region.sizeOnServer());

    c.section("8. An empty stream is fine");
    region.putAll(Map.of());
    c.check("putAll of an empty map → OK, nothing written", region.sizeOnServer() == 0);

    c.section("9. Errors");
    c.note("no StreamConfig in the native API: the region is the client Region object");
    Region<Object, Object> missing = proxyRegion(cache, "no-such-region");
    try {
      missing.putAll(range("x:", 0, 10));
      c.check("putAll to a region the servers don't have fails", false);
    } catch (RuntimeException e) {
      c.check("putAll to a missing server region → exception", true, describe(e));
    }
    try {
      missing.keySetOnServer();
      c.check("keySetOnServer on a missing server region fails", false);
    } catch (RuntimeException e) {
      c.check("keySetOnServer on a missing server region → exception", true, describe(e));
    }
    Map<Object, Object> fat = new HashMap<>();
    for (int i = 0; i < 5; i++) fat.put("fat:" + i, new byte[1 << 20]);
    region.putAll(fat);
    c.check("one 5 MiB putAll → OK: the native protocol has no per-message size limit",
        region.sizeOnServer() == 5, region.sizeOnServer());
    region.removeAll(fat.keySet());

    c.section("10. Streams aren't transactional");
    Map<Object, Object> bad = new HashMap<>(range("part:", 0, 3_000));
    bad.put(null, "bad");
    try {
      region.putAll(bad);
      c.check("putAll with a null key fails", false);
    } catch (RuntimeException e) {
      c.check("putAll of 3,000 entries plus a null key → exception", true, describe(e));
    }
    c.check("...thrown on the client before sending, so nothing is written", region.sizeOnServer() == 0, region.sizeOnServer());
    c.note("putAll isn't transactional either: a failure on a server partway through leaves");
    c.note("the entries already applied (PutAllPartialResultException lists them)");
    removeAll();

    c.section("11. Why stream? The same 2,000 entries without streaming");
    try {
      Api.Bulk bulk = new NativeBulk(cache, cfg.withRegion(region.getName()));
      double one = millis(() -> bulk.unaryPut(0, 2_000, 1024, 1));
      bulk.reset();
      double sixteen = millis(() -> bulk.unaryPut(0, 2_000, 1024, 16));
      bulk.reset();
      double putAll = millis(() -> bulk.put(0, 2_000, 500, 1024));
      bulk.reset();
      c.note(String.format("2,000 puts, one at a time      %7.1f ms   (each a blocking round trip)", one));
      c.note(String.format("2,000 puts, 16 in flight       %7.1f ms   (16 threads)", sixteen));
      c.note(String.format("4 putAll calls of 500          %7.1f ms", putAll));
      c.check("putAll is faster than 2,000 puts one at a time", putAll < one, "putAll " + Checks.compare(putAll, one));
      c.note("vs 16 puts in flight: putAll " + Checks.compare(putAll, sixteen));
    } catch (Exception e) {
      c.abort(e);
    }
  }

  private interface Work {
    Object run() throws Exception;
  }

  private static double millis(Work w) throws Exception {
    long t0 = System.nanoTime();
    w.run();
    return (System.nanoTime() - t0) / 1e6;
  }

  // ---------------------------------------------------------------------------------------------

  /** Client regions live for the life of the cache, so reuse one if the demo already made it. */
  private static Region<Object, Object> proxyRegion(ClientCache cache, String name) {
    Region<Object, Object> existing = cache.getRegion(name);
    return existing != null ? existing
        : cache.createClientRegionFactory(ClientRegionShortcut.PROXY).create(name);
  }

  private void removeAll() {
    List<Object> keys = new ArrayList<>(region.keySetOnServer());
    for (int i = 0; i < keys.size(); i += 1_000) region.removeAll(keys.subList(i, Math.min(keys.size(), i + 1_000)));
  }

  /** Entries prefix+from .. prefix+(to-1), each valued "v" + i, in order. */
  private static Map<Object, Object> range(String prefix, int from, int to) {
    Map<Object, Object> m = new LinkedHashMap<>();
    for (int i = from; i < to; i++) m.put(prefix + i, "v" + i);
    return m;
  }

  private static List<Map<Object, Object>> chunks(Map<Object, Object> entries, int size) {
    List<Map<Object, Object>> out = new ArrayList<>();
    Map<Object, Object> chunk = new LinkedHashMap<>();
    for (Map.Entry<Object, Object> e : entries.entrySet()) {
      chunk.put(e.getKey(), e.getValue());
      if (chunk.size() == size) {
        out.add(chunk);
        chunk = new LinkedHashMap<>();
      }
    }
    if (!chunk.isEmpty()) out.add(chunk);
    return out;
  }

  private static String describe(Throwable e) {
    String s = e.getClass().getSimpleName();
    if (e.getCause() != null && e.getCause() != e) s += " ← " + e.getCause().getClass().getSimpleName();
    String msg = e.getMessage() == null ? "" : e.getMessage();
    return s + (msg.isEmpty() ? "" : ": " + (msg.length() > 90 ? msg.substring(0, 90) + "…" : msg));
  }
}
