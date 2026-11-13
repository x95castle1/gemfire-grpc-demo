package demo;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Semaphore;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.IntPredicate;
import org.apache.geode.cache.Region;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientRegionShortcut;

/**
 * The bulk operations over the native client, for the HTTP API: the Region API's bulk calls,
 * one per chunk of {@code batch} entries, the same chunk size the gRPC client puts in each
 * stream message. Each call is a blocking round trip (with PR single-hop, the client splits a
 * chunk by bucket and sends each server its share), so chunk N+1 waits for chunk N. The gRPC
 * client sends its chunks back to back on one stream instead.
 */
final class NativeBulk implements Api.Bulk {

  private final Region<String, byte[]> region;
  private final Config cfg;
  /** One payload per size: every write of a given size uses the same bytes. */
  private final Map<Integer, byte[]> payloads = new ConcurrentHashMap<>();
  /**
   * Threads for mode=unary with inflight > 1. The Region API is blocking, so N calls in flight
   * take N threads (the gRPC client gets the same concurrency from async calls on one channel).
   */
  private final ExecutorService unaryPool = Executors.newFixedThreadPool(Api.MAX_INFLIGHT, r -> {
    Thread t = new Thread(r, "unary");
    t.setDaemon(true);
    return t;
  });

  NativeBulk(ClientCache cache, Config cfg) {
    Region<String, byte[]> existing = cache.getRegion(cfg.region());   // the demo's, if it made it first
    this.region = existing != null ? existing
        : cache.<String, byte[]>createClientRegionFactory(ClientRegionShortcut.PROXY).create(cfg.region());
    this.cfg = cfg;
  }

  @Override
  public Api.Result put(int start, int count, int batch, int size) {
    byte[] v = payload(size);
    int chunk = Api.chunk(batch, size);
    Map<String, byte[]> map = new HashMap<>();
    long calls = 0;
    for (int i = start; i < start + count; i++) {
      map.put(cfg.keyPrefix() + i, v);
      if (map.size() == chunk) {
        region.putAll(map);
        calls++;
        map = new HashMap<>();
      }
    }
    if (!map.isEmpty()) {
      region.putAll(map);
      calls++;
    }
    return new Api.Result(count, calls);
  }

  @Override
  public Api.Result get(int start, int count, int batch) {
    long found = 0;
    long calls = 0;
    List<String> keys = new ArrayList<>(batch);
    for (int i = start; i < start + count; i++) {
      keys.add(cfg.keyPrefix() + i);
      if (keys.size() == batch || i == start + count - 1) {
        // getAll returns every requested key; the ones that don't exist map to null.
        for (byte[] v : region.getAll(keys).values()) if (v != null) found++;
        calls++;
        keys = new ArrayList<>(batch);
      }
    }
    return new Api.Result(found, calls);
  }

  @Override
  public Api.Result remove(int start, int count, int batch) {
    List<String> keys = new ArrayList<>(count);
    for (int i = start; i < start + count; i++) keys.add(cfg.keyPrefix() + i);
    return removeKeys(keys, batch);
  }

  private Api.Result removeKeys(List<String> keys, int batch) {
    long calls = 0;
    for (int i = 0; i < keys.size(); i += batch) {
      region.removeAll(keys.subList(i, Math.min(keys.size(), i + batch)));
      calls++;
    }
    return new Api.Result(keys.size(), calls);
  }

  // --- mode=unary: the same work without bulk calls, one Region call per entry -----------------

  @Override
  public Api.Result unaryPut(int start, int count, int size, int inflight) throws Exception {
    byte[] v = payload(size);
    return unary(start, count, inflight, i -> {
      region.put(cfg.keyPrefix() + i, v);
      return true;
    });
  }

  @Override
  public Api.Result unaryGet(int start, int count, int inflight) throws Exception {
    return unary(start, count, inflight, i -> region.get(cfg.keyPrefix() + i) != null);
  }

  @Override
  public Api.Result unaryRemove(int start, int count, int inflight) throws Exception {
    // remove() on a client region returns null, not the old value; the count is of calls made.
    return unary(start, count, inflight, i -> {
      region.remove(cfg.keyPrefix() + i);
      return true;
    });
  }

  /**
   * Runs {@code call(i)} for every key with at most {@code inflight} at a time: a plain loop for
   * 1, otherwise on {@link #unaryPool}. {@code call} returns whether the entry counts (Get: found).
   * Stops at the first failure and throws it.
   */
  private Api.Result unary(int start, int count, int inflight, IntPredicate call) throws Exception {
    if (inflight == 1) {
      long hits = 0;
      for (int i = start; i < start + count; i++) if (call.test(i)) hits++;
      return new Api.Result(hits, count);
    }
    Semaphore permits = new Semaphore(inflight);
    AtomicLong hits = new AtomicLong();
    AtomicReference<RuntimeException> failure = new AtomicReference<>();
    long calls = 0;
    for (int i = start; i < start + count && failure.get() == null; i++) {
      permits.acquire();
      calls++;
      int key = i;
      unaryPool.execute(() -> {
        try {
          if (call.test(key)) hits.incrementAndGet();
        } catch (RuntimeException e) {
          failure.compareAndSet(null, e);
        } finally {
          permits.release();
        }
      });
    }
    permits.acquire(inflight);   // wait for the last ones
    if (failure.get() != null) throw failure.get();
    return new Api.Result(hits.get(), calls);
  }

  // --- keySetOnServer ----------------------------------------------------------------------------

  @Override
  public Api.Result keySet() {
    // One request; the whole key set comes back as one response, held in client memory.
    return new Api.Result(region.keySetOnServer().size(), 1);
  }

  @Override
  public long reset() {
    List<String> all = new ArrayList<>(region.keySetOnServer());
    removeKeys(all, 1000);
    return all.size();
  }

  @Override
  public String errorCode(Throwable t) {
    return t.getClass().getSimpleName();
  }

  private byte[] payload(int size) {
    return payloads.computeIfAbsent(size, n -> {
      byte[] b = new byte[n];
      new Random(n).nextBytes(b);
      return b;
    });
  }
}
