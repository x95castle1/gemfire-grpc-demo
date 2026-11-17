package demo;

import java.util.Arrays;
import java.util.List;
import java.util.Objects;
import org.apache.geode.cache.Region;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientRegionShortcut;

/**
 * The same tour as the gRPC client's Demo, step for step, using the native Region API on a PROXY
 * client region (no local caching, every call goes to the servers, same as gRPC).
 *
 * <p>The native API has no 1:1 call for several RPCs. The README has the mapping table; each
 * step here shows the idiomatic native equivalent.
 */
final class Demo {

  private final ClientCache cache;
  private final Settings cfg;
  private final Region<Object, Object> region;
  private final Checks c;

  Demo(ClientCache cache, Settings cfg, Checks checks) {
    this.cache = cache;
    this.cfg = cfg;
    this.region = proxyRegion(cache, cfg.demoRegion());
    this.c = checks;
  }

  void run() {
    cleanup();

    c.section("1. Put + Get");
    region.put("user:1", "Alice");
    c.check("get(user:1) returns the value just put", "Alice".equals(region.get("user:1")), "\"Alice\"");
    c.check("get on a missing key → null", region.get("user:404") == null);

    c.section("2. GetAndPut — put, returning the previous value");
    Object prev = region.put("user:1", "Alicia");
    c.check("put(user:1, \"Alicia\") on a client region returns null, NOT the old value", prev == null, show(prev));
    c.note("Client regions don't get the old value back from the server. The native equivalent of");
    c.note("GetAndPut is get() + put(): two round trips and not atomic.");
    Object before2 = region.get("user:1");
    region.put("user:1", "Alicia2");
    c.check("get() + put() yields the previous value", "Alicia".equals(before2), show(before2));
    region.put("user:2", "Bob");

    c.section("3. PutIfAbsent");
    c.check("putIfAbsent(user:3) on a new key → null (inserted)", region.putIfAbsent("user:3", "Carol") == null);
    Object existing = region.putIfAbsent("user:3", "Mallory");
    c.check("putIfAbsent(user:3) again → returns the existing value (not inserted)", "Carol".equals(existing), show(existing));
    c.check("...and the original value is untouched", "Carol".equals(region.get("user:3")), "\"Carol\"");

    c.section("4. PutIfAbsentAndGet — no single native call");
    c.note("Native: putIfAbsent returns the existing value or null; the caller knows its own value.");
    Object v = putIfAbsentAndGet("user:3", "Mallory");
    c.check("key present → existing value", "Carol".equals(v), show(v));
    v = putIfAbsentAndGet("user:4", "Dave");
    c.check("key absent → inserts and yields the new value", "Dave".equals(v), show(v));

    c.section("5. Replace — only if the key exists");
    c.check("replace on a missing key → null (not replaced)", region.replace("user:404", "Ghost") == null);
    c.check("...and no entry was created", !region.containsKeyOnServer("user:404"));
    Object replacedOld = region.replace("user:4", "David");
    c.check("replace on an existing key → returns the old value (replaced)", "Dave".equals(replacedOld), show(replacedOld));

    c.section("6. ReplaceIf — compare-and-set");
    c.check("replace(k, stale, new) → false", !region.replace("user:4", "Dave", "Dee"));
    c.check("replace(k, current, new) → true", region.replace("user:4", "David", "Dee"));

    c.section("7. GetAndReplace");
    Object old = region.replace("user:4", "Diana");
    c.check("replace on an existing key returns the old value", "Dee".equals(old), show(old));
    c.check("replace on a missing key → null, nothing created",
        region.replace("user:405", "Ghost") == null && !region.containsKeyOnServer("user:405"));

    c.section("8. ContainsKey");
    c.note("Native: containsKey() checks the LOCAL cache (always false on a PROXY region); use containsKeyOnServer().");
    c.check("containsKeyOnServer(user:1) → true", region.containsKeyOnServer("user:1"));
    c.check("containsKeyOnServer(user:404) → false", !region.containsKeyOnServer("user:404"));
    c.check("containsKey(user:1) on a PROXY region → false (local check)", !region.containsKey("user:1"));

    c.section("9. Remove");
    Object r1 = region.remove("user:2");
    c.check("remove(user:2) on a client region returns null even though it removed the entry", r1 == null, show(r1));
    c.check("...the entry is gone", !region.containsKeyOnServer("user:2"));
    c.check("remove(user:2) again → also null: no way to tell the two calls apart", region.remove("user:2") == null);
    c.note("destroy(k) doesn't throw EntryNotFoundException from a client region either. To learn");
    c.note("`removed` natively: containsKeyOnServer() + remove(), two round trips and not atomic.");

    c.section("10. RemoveIf — remove only if the value matches");
    c.check("remove(k, wrong) → false", !region.remove("user:3", "Mallory"));
    c.check("remove(k, current) → true", region.remove("user:3", "Carol"));

    c.section("11. GetAndRemove");
    Object gone = region.remove("user:4");
    c.check("remove(user:4) on a client region returns null, NOT the removed value", gone == null, show(gone));
    c.check("...but the entry is gone", !region.containsKeyOnServer("user:4"));
    c.note("Native equivalent of GetAndRemove: get() + remove(), two round trips and not atomic.");

    c.section("12. EntryCount");
    c.note("Native: size() is the LOCAL size (0 on a PROXY region); use sizeOnServer().");
    int before = region.sizeOnServer();
    for (int i = 0; i < 5; i++) region.put("count:" + i, i);
    int after = region.sizeOnServer();
    c.check("sizeOnServer grows by exactly 5 after 5 new puts", after - before == 5, before + " → " + after);

    c.section("13. Key types — String, Integer and Long keys are distinct");
    region.put("42", "string key");
    region.put(42, "int32 key");
    region.put(42L, "int64 key");
    c.check("get(\"42\") → \"string key\"", "string key".equals(region.get("42")));
    c.check("get(42)   → \"int32 key\"", "int32 key".equals(region.get(42)));
    c.check("get(42L)  → \"int64 key\"", "int64 key".equals(region.get(42L)));
    region.put(9_000_000_000L, "beyond int32");
    c.check("Long key beyond the int32 range round-trips", "beyond int32".equals(region.get(9_000_000_000L)));
    c.note("Native also accepts any serializable key (composite keys, PDX objects); gRPC keys are string/int32/int64 only.");

    c.section("14. Value types — the Java equivalents of each gRPC Value case");
    List<Object> values = List.of("héllo wörld ✓", new byte[] {0, 1, 2, (byte) 0xFF},
        Integer.MIN_VALUE, Long.MAX_VALUE, Boolean.TRUE, Math.PI, 2.5f);
    for (Object value : values) {
      String k = "type:" + value.getClass().getSimpleName();
      region.put(k, value);
      Object back = region.get(k);
      boolean same = value instanceof byte[] b ? Arrays.equals(b, (byte[]) back) : Objects.equals(value, back);
      c.check(String.format("%-8s round-trips", value.getClass().getSimpleName()),
          same && back.getClass() == value.getClass(), show(back));
    }

    c.section("15. Errors are Java exceptions");
    Region<Object, Object> missing = proxyRegion(cache, "no-such-region");
    try {
      missing.get("k");
      c.check("get on a region the servers don't have fails", false);
    } catch (RuntimeException e) {
      c.check("get on a missing server region → exception", true, describe(e));
    }
    try {
      region.get(null);
      c.check("get(null) fails", false);
    } catch (RuntimeException e) {
      c.check("get(null) → exception (client-side, never reaches the server)", true, describe(e));
    }

    c.section("16. Two region types: PARTITION and REPLICATE");
    for (String name : List.of(cfg.region(), cfg.replicateRegion())) {
      Region<Object, Object> r = proxyRegion(cache, name);
      byte[] value = ("same call, " + name).getBytes(java.nio.charset.StandardCharsets.UTF_8);
      r.put(cfg.keyPrefix() + "demo:region-type", value);
      Object back = r.get(cfg.keyPrefix() + "demo:region-type");
      c.check("put + get on " + name, back instanceof byte[] b && Arrays.equals(b, value), new String((byte[]) back, java.nio.charset.StandardCharsets.UTF_8));
      r.remove(cfg.keyPrefix() + "demo:region-type");
    }
    c.note("The calls are identical. With single-hop, a partition key goes straight to the server that owns it;");
    c.note("a replicate key to any server, which holds them all. Neither ever needs a forwarding hop.");
    c.note("The experiments: make select-load-test EX=11 SCRIPT=experiments/region-type");
  }

  // ---------------------------------------------------------------------------------------------

  /** gRPC PutIfAbsentAndGet ≈ putIfAbsent + "if null, my value won". */
  private Object putIfAbsentAndGet(Object key, Object value) {
    Object existing = region.putIfAbsent(key, value);
    return existing != null ? existing : value;
  }

  /** Client regions live for the life of the cache, so reuse one if the demo already made it. */
  private static Region<Object, Object> proxyRegion(ClientCache cache, String name) {
    Region<Object, Object> existing = cache.getRegion(name);
    return existing != null ? existing
        : cache.createClientRegionFactory(ClientRegionShortcut.PROXY).create(name);
  }

  private void cleanup() {
    for (Object k : List.of("user:1", "user:2", "user:3", "user:4", "user:404", "user:405", "42", 42, 42L, 9_000_000_000L)) {
      region.remove(k);
    }
    for (int i = 0; i < 5; i++) region.remove("count:" + i);
    for (String t : List.of("String", "byte[]", "Integer", "Long", "Boolean", "Double", "Float")) region.remove("type:" + t);
  }

  private static String describe(Throwable e) {
    String s = e.getClass().getSimpleName();
    if (e.getCause() != null && e.getCause() != e) s += " ← " + e.getCause().getClass().getSimpleName();
    String msg = e.getMessage() == null ? "" : e.getMessage();
    return s + (msg.isEmpty() ? "" : ": " + (msg.length() > 90 ? msg.substring(0, 90) + "…" : msg));
  }

  private static String show(Object v) {
    if (v == null) return "null";
    if (v instanceof String s) return "\"" + s + "\"";
    if (v instanceof byte[] b) return java.util.HexFormat.ofDelimiter(" ").formatHex(b);
    return v + " (" + v.getClass().getSimpleName() + ")";
  }
}
