package demo;

import com.google.protobuf.ByteString;
import com.vmware.gemfire.grpc.v1.ContainsKeyRequest;
import com.vmware.gemfire.grpc.v1.EntryCountRequest;
import com.vmware.gemfire.grpc.v1.GemFireGrpc.GemFireBlockingStub;
import com.vmware.gemfire.grpc.v1.GetAndPutRequest;
import com.vmware.gemfire.grpc.v1.GetAndRemoveRequest;
import com.vmware.gemfire.grpc.v1.GetAndReplaceRequest;
import com.vmware.gemfire.grpc.v1.GetRequest;
import com.vmware.gemfire.grpc.v1.GetResponse;
import com.vmware.gemfire.grpc.v1.Key;
import com.vmware.gemfire.grpc.v1.PutIfAbsentAndGetRequest;
import com.vmware.gemfire.grpc.v1.PutIfAbsentRequest;
import com.vmware.gemfire.grpc.v1.PutRequest;
import com.vmware.gemfire.grpc.v1.RemoveIfRequest;
import com.vmware.gemfire.grpc.v1.RemoveRequest;
import com.vmware.gemfire.grpc.v1.ReplaceIfRequest;
import com.vmware.gemfire.grpc.v1.ReplaceRequest;
import com.vmware.gemfire.grpc.v1.Value;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.List;
import java.util.concurrent.TimeUnit;

/**
 * A narrated tour of all 13 unary RPCs plus the supported key and value types. Every step
 * asserts the documented behavior. The native client's Demo runs the same steps with the Region
 * API, so the two logs can be read side by side.
 */
final class Demo {

  private final GemFireBlockingStub gemfire;
  private final String region;
  private final Checks c;

  Demo(GemFireBlockingStub gemfire, Settings cfg, Checks checks) {
    this.gemfire = gemfire;
    this.region = cfg.demoRegion();
    this.c = checks;
  }

  void run() {
    cleanup();

    c.section("1. Put + Get");
    put(key("user:1"), str("Alice"));
    c.check("Get(user:1) returns the value just put", get(key("user:1")).equals(str("Alice")), "\"Alice\"");
    GetResponse miss = gemfire().get(GetRequest.newBuilder().setRegionName(region).setKey(key("user:404")).build());
    c.check("Get on a missing key → `value` unset (never NOT_FOUND)", !miss.hasValue());

    c.section("2. GetAndPut — put, returning the previous value");
    Value prev = gemfire().getAndPut(GetAndPutRequest.newBuilder()
        .setRegionName(region).setKey(key("user:1")).setValue(str("Alicia")).build()).getValue();
    c.check("GetAndPut(user:1, \"Alicia\") returns the old value", prev.equals(str("Alice")), "\"Alice\"");
    boolean hadPrev = gemfire().getAndPut(GetAndPutRequest.newBuilder()
        .setRegionName(region).setKey(key("user:2")).setValue(str("Bob")).build()).hasValue();
    c.check("GetAndPut on a new key → `value` unset", !hadPrev);

    c.section("3. PutIfAbsent");
    boolean inserted = gemfire().putIfAbsent(PutIfAbsentRequest.newBuilder()
        .setRegionName(region).setKey(key("user:3")).setValue(str("Carol")).build()).getInserted();
    c.check("PutIfAbsent(user:3) on a new key → inserted=true", inserted);
    inserted = gemfire().putIfAbsent(PutIfAbsentRequest.newBuilder()
        .setRegionName(region).setKey(key("user:3")).setValue(str("Mallory")).build()).getInserted();
    c.check("PutIfAbsent(user:3) again → inserted=false", !inserted);
    c.check("...and the original value is untouched", get(key("user:3")).equals(str("Carol")), "\"Carol\"");

    c.section("4. PutIfAbsentAndGet — insert-or-read in one round trip");
    Value v = gemfire().putIfAbsentAndGet(PutIfAbsentAndGetRequest.newBuilder()
        .setRegionName(region).setKey(key("user:3")).setValue(str("Mallory")).build()).getValue();
    c.check("key present → returns the existing value", v.equals(str("Carol")), "\"Carol\"");
    v = gemfire().putIfAbsentAndGet(PutIfAbsentAndGetRequest.newBuilder()
        .setRegionName(region).setKey(key("user:4")).setValue(str("Dave")).build()).getValue();
    c.check("key absent → inserts and returns the new value", v.equals(str("Dave")), "\"Dave\"");

    c.section("5. Replace — only if the key exists");
    boolean replaced = gemfire().replace(ReplaceRequest.newBuilder()
        .setRegionName(region).setKey(key("user:404")).setValue(str("Ghost")).build()).getReplaced();
    c.check("Replace on a missing key → replaced=false", !replaced);
    c.check("...and no entry was created", !containsKey(key("user:404")));
    replaced = gemfire().replace(ReplaceRequest.newBuilder()
        .setRegionName(region).setKey(key("user:4")).setValue(str("David")).build()).getReplaced();
    c.check("Replace on an existing key → replaced=true", replaced);

    c.section("6. ReplaceIf — compare-and-set");
    replaced = gemfire().replaceIf(ReplaceIfRequest.newBuilder().setRegionName(region)
        .setKey(key("user:4")).setExpectedValue(str("Dave")).setValue(str("Dee")).build()).getReplaced();
    c.check("ReplaceIf with a stale expected value → replaced=false", !replaced);
    replaced = gemfire().replaceIf(ReplaceIfRequest.newBuilder().setRegionName(region)
        .setKey(key("user:4")).setExpectedValue(str("David")).setValue(str("Dee")).build()).getReplaced();
    c.check("ReplaceIf with the current value → replaced=true", replaced);

    c.section("7. GetAndReplace");
    Value old = gemfire().getAndReplace(GetAndReplaceRequest.newBuilder()
        .setRegionName(region).setKey(key("user:4")).setValue(str("Diana")).build()).getValue();
    c.check("GetAndReplace on an existing key returns the old value", old.equals(str("Dee")), "\"Dee\"");
    boolean any = gemfire().getAndReplace(GetAndReplaceRequest.newBuilder()
        .setRegionName(region).setKey(key("user:405")).setValue(str("Ghost")).build()).hasValue();
    c.check("GetAndReplace on a missing key → `value` unset, nothing created",
        !any && !containsKey(key("user:405")));

    c.section("8. ContainsKey");
    c.check("ContainsKey(user:1) → true", containsKey(key("user:1")));
    c.check("ContainsKey(user:404) → false", !containsKey(key("user:404")));

    c.section("9. Remove");
    boolean removed = gemfire().remove(RemoveRequest.newBuilder().setRegionName(region).setKey(key("user:2")).build()).getRemoved();
    c.check("Remove(user:2) → removed=true", removed);
    removed = gemfire().remove(RemoveRequest.newBuilder().setRegionName(region).setKey(key("user:2")).build()).getRemoved();
    c.check("Remove(user:2) again → removed=false", !removed);

    c.section("10. RemoveIf — remove only if the value matches");
    removed = gemfire().removeIf(RemoveIfRequest.newBuilder().setRegionName(region)
        .setKey(key("user:3")).setExpectedValue(str("Mallory")).build()).getRemoved();
    c.check("RemoveIf with the wrong value → removed=false", !removed);
    removed = gemfire().removeIf(RemoveIfRequest.newBuilder().setRegionName(region)
        .setKey(key("user:3")).setExpectedValue(str("Carol")).build()).getRemoved();
    c.check("RemoveIf with the current value → removed=true", removed);

    c.section("11. GetAndRemove");
    Value gone = gemfire().getAndRemove(GetAndRemoveRequest.newBuilder().setRegionName(region).setKey(key("user:4")).build()).getValue();
    c.check("GetAndRemove(user:4) returns the removed value", gone.equals(str("Diana")), "\"Diana\"");
    boolean again = gemfire().getAndRemove(GetAndRemoveRequest.newBuilder().setRegionName(region).setKey(key("user:4")).build()).hasValue();
    c.check("GetAndRemove(user:4) again → `value` unset", !again);

    c.section("12. EntryCount");
    long before = entryCount();
    for (int i = 0; i < 5; i++) put(key("count:" + i), Value.newBuilder().setInt32(i).build());
    long after = entryCount();
    c.check("EntryCount grows by exactly 5 after 5 new puts", after - before == 5, before + " → " + after);

    c.section("13. Key types — string, int32 and int64 keys are distinct");
    put(Key.newBuilder().setString("42").build(), str("string key"));
    put(Key.newBuilder().setInt(42).build(), str("int32 key"));
    put(Key.newBuilder().setLong(42L).build(), str("int64 key"));
    c.check("Get(\"42\")  → \"string key\"", get(Key.newBuilder().setString("42").build()).equals(str("string key")));
    c.check("Get(42)    → \"int32 key\"", get(Key.newBuilder().setInt(42).build()).equals(str("int32 key")));
    c.check("Get(42L)   → \"int64 key\"", get(Key.newBuilder().setLong(42L).build()).equals(str("int64 key")));
    Key big = Key.newBuilder().setLong(9_000_000_000L).build();
    put(big, str("beyond int32"));
    c.check("int64 key beyond the int32 range round-trips", get(big).equals(str("beyond int32")));

    c.section("14. Value types — every scalar Value case round-trips");
    List<Value> values = List.of(
        str("héllo wörld ✓"),
        Value.newBuilder().setBytes(ByteString.copyFrom(new byte[] {0, 1, 2, (byte) 0xFF})).build(),
        Value.newBuilder().setInt32(Integer.MIN_VALUE).build(),
        Value.newBuilder().setInt64(Long.MAX_VALUE).build(),
        Value.newBuilder().setBool(true).build(),
        Value.newBuilder().setDouble(Math.PI).build(),
        Value.newBuilder().setFloat(2.5f).build());
    for (Value value : values) {
      Key k = key("type:" + value.getValueValueCase());
      put(k, value);
      Value back = get(k);
      c.check(String.format("%-7s round-trips", value.getValueValueCase()), back.equals(value), show(back));
    }
    c.note("google.protobuf.Any (structured values) is covered in 04-structured-values.");

    c.section("15. Errors are gRPC status codes");
    try {
      gemfire().get(GetRequest.newBuilder().setRegionName("no-such-region").setKey(key("k")).build());
      c.check("Get on a missing region fails", false);
    } catch (StatusRuntimeException e) {
      c.check("Get on a missing region → FAILED_PRECONDITION",
          e.getStatus().getCode() == Status.Code.FAILED_PRECONDITION, e.getStatus());
    }
    try {
      gemfire().get(GetRequest.newBuilder().setRegionName(region).setKey(Key.getDefaultInstance()).build());
      c.check("Get with an unset key fails", false);
    } catch (StatusRuntimeException e) {
      c.check("Get with an unset Key oneof → INVALID_ARGUMENT",
          e.getStatus().getCode() == Status.Code.INVALID_ARGUMENT, e.getStatus());
    }
    c.note("Every status code is exercised in 10-error-handling.");
  }

  // ---------------------------------------------------------------------------------------------

  private GemFireBlockingStub gemfire() {
    return gemfire.withDeadlineAfter(10, TimeUnit.SECONDS);
  }

  /** Makes the demo re-runnable: removes every key a previous run may have left behind. */
  private void cleanup() {
    List<Key> keys = new java.util.ArrayList<>();
    for (String s : List.of("user:1", "user:2", "user:3", "user:4", "user:404", "user:405", "42")) keys.add(key(s));
    for (int i = 0; i < 5; i++) keys.add(key("count:" + i));
    for (Value.ValueValueCase t : Value.ValueValueCase.values()) keys.add(key("type:" + t));
    keys.add(Key.newBuilder().setInt(42).build());
    keys.add(Key.newBuilder().setLong(42L).build());
    keys.add(Key.newBuilder().setLong(9_000_000_000L).build());
    for (Key k : keys) gemfire().remove(RemoveRequest.newBuilder().setRegionName(region).setKey(k).build());
  }

  private void put(Key k, Value v) {
    gemfire().put(PutRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build());
  }

  private Value get(Key k) {
    return gemfire().get(GetRequest.newBuilder().setRegionName(region).setKey(k).build()).getValue();
  }

  private boolean containsKey(Key k) {
    return gemfire().containsKey(ContainsKeyRequest.newBuilder().setRegionName(region).setKey(k).build()).getContains();
  }

  private long entryCount() {
    return gemfire().entryCount(EntryCountRequest.newBuilder().setRegionName(region).build()).getSize();
  }

  /** Renders a Value as `CASE value` for the log (the default proto text escapes UTF-8). */
  private static String show(Value v) {
    return switch (v.getValueValueCase()) {
      case STRING -> "\"" + v.getString() + "\"";
      case BYTES -> java.util.HexFormat.ofDelimiter(" ").formatHex(v.getBytes().toByteArray());
      case INT32 -> Integer.toString(v.getInt32());
      case INT64 -> Long.toString(v.getInt64());
      case BOOL -> Boolean.toString(v.getBool());
      case DOUBLE -> Double.toString(v.getDouble());
      case FLOAT -> v.getFloat() + "f";
      default -> v.getValueValueCase().name();
    };
  }

  private static Key key(String s) {
    return Key.newBuilder().setString(s).build();
  }

  private static Value str(String s) {
    return Value.newBuilder().setString(s).build();
  }
}
