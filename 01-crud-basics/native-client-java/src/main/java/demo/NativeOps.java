package demo;

import java.util.HashMap;
import java.util.Map;
import org.apache.geode.cache.Region;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientRegionShortcut;

/**
 * Bench operations over the native client. Each op uses the closest Region API equivalent of the
 * gRPC RPC with the same name (mapping table in the README).
 */
final class NativeOps implements Bench.Ops {

  private final Region<String, byte[]> region;
  private final String[] keys;
  private final byte[] payload;

  NativeOps(ClientCache cache, Config cfg) {
    this.region = cache.<String, byte[]>createClientRegionFactory(ClientRegionShortcut.PROXY).create(cfg.region());
    this.keys = new String[cfg.keySpace()];
    for (int i = 0; i < keys.length; i++) keys[i] = "native:" + i;
    this.payload = new byte[cfg.payloadBytes()];
    new java.util.Random(42).nextBytes(payload);
  }

  /** Preload with putAll (the native equivalent of PutStream). */
  @Override
  public void preload() {
    Map<String, byte[]> batch = new HashMap<>();
    for (String k : keys) {
      batch.put(k, payload);
      if (batch.size() == 500) {
        region.putAll(batch);
        batch.clear();
      }
    }
    if (!batch.isEmpty()) region.putAll(batch);
  }

  @Override
  public void execute(String op, int i) {
    String k = keys[i];
    switch (op) {
      case "Put" -> region.put(k, payload);
      case "Get" -> region.get(k);
      // Client put()/remove() return null rather than the old value, so returning the same
      // information as the RPC takes a second round trip (and is not atomic). That second
      // round trip is the honest native cost of the same result.
      case "GetAndPut" -> { region.get(k); region.put(k, payload); }
      case "Remove" -> { region.containsKeyOnServer(k); region.remove(k); }
      case "RemoveIf" -> region.remove(k, payload);
      case "GetAndRemove" -> { region.get(k); region.remove(k); }
      case "PutIfAbsent" -> region.putIfAbsent(k, payload);
      case "PutIfAbsentAndGet" -> region.putIfAbsent(k, payload);
      case "Replace" -> region.replace(k, payload);
      case "ReplaceIf" -> region.replace(k, payload, payload);
      case "GetAndReplace" -> region.replace(k, payload);
      case "ContainsKey" -> region.containsKeyOnServer(k);
      case "EntryCount" -> region.sizeOnServer();
      default -> throw new IllegalArgumentException("Unknown op: " + op);
    }
  }

  @Override
  public String errorCode(Throwable t) {
    return t.getClass().getSimpleName();
  }
}
