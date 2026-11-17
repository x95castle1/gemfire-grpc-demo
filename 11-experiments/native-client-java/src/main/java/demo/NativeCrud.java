package demo;

import java.util.HashMap;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.ConcurrentHashMap;
import org.apache.geode.cache.Region;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientRegionShortcut;

/**
 * The 13 operations over the native client, for the HTTP API. Each op uses the cheapest native
 * code that returns the same information as the gRPC RPC of the same name (README mapping table).
 */
final class NativeCrud implements Crud {

  /** One PROXY region per server region the experiments use (crud-partition, crud-replicate). */
  private final Map<String, Region<String, byte[]>> regions = new HashMap<>();
  private final Settings cfg;
  /** One payload per size, so ReplaceIf/RemoveIf succeed whenever the key exists. */
  private final Map<Integer, byte[]> payloads = new ConcurrentHashMap<>();

  NativeCrud(ClientCache cache, Settings cfg) {
    for (String name : new String[] {cfg.region(), cfg.replicateRegion()}) {
      regions.put(name, cache.<String, byte[]>createClientRegionFactory(ClientRegionShortcut.PROXY).create(name));
    }
    this.cfg = cfg;
  }

  @Override
  public Object execute(String regionName, String op, String k, int size) {
    Region<String, byte[]> region = regions.get(regionName);
    byte[] v = payload(size);
    return switch (op) {
      case "Put" -> {
        region.put(k, v);
        yield null;
      }
      case "Get" -> bytes(region.get(k));
      // Client put()/remove() return null rather than the old value, so returning the same
      // information as the RPC takes a second round trip (and isn't atomic). That second
      // round trip is the honest native cost of the same result.
      case "GetAndPut" -> {
        byte[] old = region.get(k);
        region.put(k, v);
        yield bytes(old);
      }
      case "Remove" -> {
        boolean existed = region.containsKeyOnServer(k);
        region.remove(k);
        yield existed;
      }
      case "RemoveIf" -> region.remove(k, v);
      case "GetAndRemove" -> {
        byte[] old = region.get(k);
        region.remove(k);
        yield bytes(old);
      }
      case "PutIfAbsent" -> region.putIfAbsent(k, v) == null;
      case "PutIfAbsentAndGet" -> {
        byte[] existing = region.putIfAbsent(k, v);
        yield bytes(existing != null ? existing : v);
      }
      case "Replace" -> region.replace(k, v) != null;
      case "ReplaceIf" -> region.replace(k, v, v);
      case "GetAndReplace" -> bytes(region.replace(k, v));
      case "ContainsKey" -> region.containsKeyOnServer(k);
      case "EntryCount" -> region.sizeOnServer();
      default -> throw new IllegalArgumentException("Unknown op: " + op);
    };
  }

  /** Preload with putAll (the native equivalent of PutStream). */
  @Override
  public int preload(String regionName, int start, int count, int size) {
    Region<String, byte[]> region = regions.get(regionName);
    byte[] v = payload(size);
    Map<String, byte[]> batch = new HashMap<>();
    for (int i = start; i < start + count; i++) {
      batch.put(cfg.keyPrefix() + i, v);
      // Same batching rule as the gRPC client's PutStream: 500 entries or ~1 MiB.
      if (batch.size() == 500 || (long) batch.size() * size >= 1 << 20) {
        region.putAll(batch);
        batch.clear();
      }
    }
    if (!batch.isEmpty()) region.putAll(batch);
    return count;
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

  private static Integer bytes(byte[] v) {
    return v == null ? null : v.length;
  }
}
