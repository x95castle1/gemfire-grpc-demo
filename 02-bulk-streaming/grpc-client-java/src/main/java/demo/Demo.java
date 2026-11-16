package demo;

import com.vmware.gemfire.grpc.v1.Entry;
import com.vmware.gemfire.grpc.v1.EntryCountRequest;
import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import com.vmware.gemfire.grpc.v1.GetStreamRequest;
import com.vmware.gemfire.grpc.v1.GetStreamResponse;
import com.vmware.gemfire.grpc.v1.Key;
import com.vmware.gemfire.grpc.v1.KeySetStreamRequest;
import com.vmware.gemfire.grpc.v1.KeySetStreamResponse;
import com.vmware.gemfire.grpc.v1.PutStreamRequest;
import com.vmware.gemfire.grpc.v1.PutStreamResponse;
import com.vmware.gemfire.grpc.v1.RemoveStreamRequest;
import com.vmware.gemfire.grpc.v1.RemoveStreamResponse;
import com.vmware.gemfire.grpc.v1.StreamConfig;
import com.vmware.gemfire.grpc.v1.Value;
import com.google.protobuf.ByteString;
import io.grpc.Channel;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.stream.Collectors;
import java.util.stream.IntStream;

/**
 * A narrated tour of the four bulk streaming RPCs. Every step asserts the documented behavior.
 * The native client's Demo runs the same steps with putAll/getAll/removeAll/keySetOnServer, so
 * the two logs can be read side by side.
 */
final class Demo {

  private static final long TIMEOUT_S = 60;

  private final Channel channel;
  private final String region;
  private final int bulkBatchSize;
  private final Checks c;
  private final Settings cfg;

  Demo(Channel channel, Settings cfg, Checks checks) {
    this.channel = channel;
    this.cfg = cfg;
    this.region = cfg.demoRegion();
    this.bulkBatchSize = Integer.parseInt(Settings.env("GRPC_BULK_BATCH_SIZE", "1000"));
    this.c = checks;
  }

  void run() {
    removeAll(region);   // re-runnable: start from an empty region

    c.section("1. PutStream: putAll as a client stream");
    Result put = putStream(region, chunks(range("item:", 0, 5_000), 500));
    c.check("PutStream of 5,000 entries in chunks of 500 → OK", put.ok(), show(put.status()));
    c.note("one call, " + put.sent() + " messages: the StreamConfig (region) first, then 10 chunks, sent back to back");
    c.check("EntryCount → 5,000", entryCount(region) == 5_000, entryCount(region));

    c.section("2. The server re-batches what it receives");
    put = putStream(region, List.of(range("big:", 0, 2_500)));
    c.check("one message of 2,500 entries → OK", put.ok(), show(put.status()));
    c.note("the server applies it as putAll operations of at most bulk-batch-size ("
        + bulkBatchSize + ") entries, so message size and server batch size are independent");
    c.check("EntryCount → 7,500", entryCount(region) == 7_500, entryCount(region));

    c.section("3. Key types: string, int32 and int64 keys in one stream");
    List<Entry> typed = List.of(
        entry(Key.newBuilder().setString("7").build(), str("string key")),
        entry(Key.newBuilder().setInt(7).build(), str("int32 key")),
        entry(Key.newBuilder().setLong(7L).build(), str("int64 key")));
    c.check("PutStream with mixed key types → OK", putStream(region, List.of(typed)).ok());
    List<Entry> back = getStream(region, List.of(typed.stream().map(Entry::getKey).toList())).entries();
    c.check("GetStream returns all 3, each with its own key type and value",
        back.size() == 3 && back.containsAll(typed),
        back.stream().map(e -> e.getKey().getKeyValueCase() + "=" + e.getValue().getString()).toList());

    c.section("4. GetStream: misses are omitted, not returned as nulls");
    List<Key> ask = new ArrayList<>();
    for (int i = 0; i < 6; i++) ask.add(key("item:" + i));
    for (int i = 0; i < 4; i++) ask.add(key("missing:" + i));
    Result got = getStream(region, List.of(ask));
    c.check("ask for 10 keys (6 present, 4 absent) → 6 entries", got.entries().size() == 6, got.entries().size() + " entries");
    c.check("...and only for keys that exist",
        got.entries().stream().allMatch(e -> e.getKey().getString().startsWith("item:")));
    c.note("native getAll returns all 10 keys, the 4 absent ones mapped to null");

    c.section("5. GetStream is bidirectional");
    // Send one chunk and wait for its entries before sending the next. A client stream (PutStream)
    // can't do this: its only response comes after the client has finished sending.
    List<Entry> seen = new CopyOnWriteArrayList<>();
    StreamCall<GetStreamRequest, GetStreamResponse> bidi =
        new StreamCall<>(r -> seen.addAll(r.getEntriesList()));
    GemFireGrpc.newStub(channel).withDeadlineAfter(TIMEOUT_S, TimeUnit.SECONDS).getStream(bidi);
    bidi.send(GetStreamRequest.newBuilder().setConfig(config(region)).addAllKeys(range("item:", 0, 100).stream().map(Entry::getKey).toList()).build());
    long waited = waitFor(() -> seen.size() >= 100, 5_000);
    c.check("entries for chunk 1 arrive before chunk 2 is sent", seen.size() == 100,
        seen.size() + " entries after " + waited + " ms");
    bidi.send(GetStreamRequest.newBuilder().addAllKeys(range("item:", 100, 200).stream().map(Entry::getKey).toList()).build());
    bidi.halfClose();
    bidi.await(TIMEOUT_S, TimeUnit.SECONDS);
    c.check("then chunk 2's entries, and the call completes OK", seen.size() == 200, seen.size() + " entries");

    c.section("6. KeySetStream: keySet as a server stream");
    List<KeySetStreamResponse> pages = keySet(region);
    long keys = pages.stream().mapToLong(KeySetStreamResponse::getKeysCount).sum();
    int largest = pages.stream().mapToInt(KeySetStreamResponse::getKeysCount).max().orElse(0);
    c.check("returns every key: 7,503", keys == 7_503, keys);
    c.check("in messages of at most bulk-batch-size (" + bulkBatchSize + ") keys",
        largest <= bulkBatchSize && pages.size() >= Math.ceilDiv(7_503, bulkBatchSize),
        pages.size() + " messages, largest " + largest);
    c.note("native keySetOnServer returns one Set, built in client memory in one go");

    c.section("7. RemoveStream: removeAll as a client stream");
    List<Key> all = pages.stream().flatMap(p -> p.getKeysList().stream()).collect(Collectors.toCollection(ArrayList::new));
    all.addAll(IntStream.range(0, 100).mapToObj(i -> key("never-existed:" + i)).toList());
    Result removed = removeStream(region, chunks(all, 1_000));
    c.check("RemoveStream of every key, plus 100 that don't exist → OK", removed.ok(), show(removed.status()));
    c.check("EntryCount → 0", entryCount(region) == 0, entryCount(region));

    c.section("8. An empty stream is fine");
    Result empty = putStream(region, List.of());
    c.check("PutStream with only the StreamConfig → OK, nothing written",
        empty.ok() && entryCount(region) == 0, show(empty.status()));

    c.section("9. Errors");
    Result noConfig = stream(region, false, chunks(range("x:", 0, 10), 10));
    c.check("first message without a StreamConfig → INVALID_ARGUMENT", noConfig.is(Status.Code.INVALID_ARGUMENT), show(noConfig.status()));
    Result noRegion = putStream("no-such-region", chunks(range("x:", 0, 10), 10));
    c.check("PutStream to a region that doesn't exist → FAILED_PRECONDITION",
        noRegion.is(Status.Code.FAILED_PRECONDITION), show(noRegion.status()));
    try {
      keySet("no-such-region");
      c.check("KeySetStream on a missing region fails", false);
    } catch (StatusRuntimeException e) {
      c.check("KeySetStream on a missing region → FAILED_PRECONDITION",
          e.getStatus().getCode() == Status.Code.FAILED_PRECONDITION, show(e.getStatus()));
    }
    Value mib = Value.newBuilder().setBytes(ByteString.copyFrom(new byte[1 << 20])).build();
    List<Entry> fat = IntStream.range(0, 5).mapToObj(i -> entry(key("fat:" + i), mib)).toList();
    Result tooBig = putStream(region, List.of(fat));
    c.check("one 5 MiB message (over max-message-size, 4 MiB) → RESOURCE_EXHAUSTED",
        tooBig.is(Status.Code.RESOURCE_EXHAUSTED), show(tooBig.status()));
    c.note("chunk by bytes as well as entries: this client caps every chunk at ~1 MiB of values");

    c.section("10. Streams aren't transactional");
    List<List<Entry>> chunks = new ArrayList<>(chunks(range("part:", 0, 3_000), 1_000));
    chunks.add(List.of(Entry.newBuilder().setKey(Key.getDefaultInstance()).setValue(str("bad")).build()));
    Result partial = putStream(region, chunks);
    c.check("3 good chunks of 1,000, then one with an unset Key → INVALID_ARGUMENT",
        partial.is(Status.Code.INVALID_ARGUMENT), show(partial.status()));
    long kept = entryCount(region);
    c.check("...and the entries already applied stay written (no rollback)", kept == 3_000, kept + " entries kept");
    c.note("the server applies each message as it arrives; whatever came before the failure stays");
    c.note("native putAll checks keys on the client before sending, so a null key writes nothing");
    removeAll(region);

    c.section("11. Why stream? The same 2,000 entries without streaming");
    try {
      Bulk bulk = new GrpcBulk(channel, cfg.withRegion(region));
      double one = millis(() -> bulk.unaryPut(0, 2_000, 1024, 1));
      bulk.reset();
      double sixteen = millis(() -> bulk.unaryPut(0, 2_000, 1024, 16));
      bulk.reset();
      double stream = millis(() -> bulk.put(0, 2_000, 500, 1024));
      bulk.reset();
      c.note(String.format("2,000 Puts, one at a time      %7.1f ms   (wait for each response)", one));
      c.note(String.format("2,000 Puts, 16 in flight       %7.1f ms   (async stub, one channel)", sixteen));
      c.note(String.format("1 PutStream, 4 chunks of 500   %7.1f ms", stream));
      c.check("the stream is faster than 2,000 Puts one at a time", stream < one,
          "PutStream " + Checks.compare(stream, one));
      c.note("vs 16 Puts in flight: PutStream " + Checks.compare(stream, sixteen)
          + ". In flight stops the waiting; the stream also drops the per-call cost (one call, 4 messages)");
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

  /** Outcome of one stream: its final status, messages sent, and entries received. */
  record Result(Status status, long sent, List<Entry> entries) {
    boolean ok() {
      return status.isOk();
    }

    boolean is(Status.Code code) {
      return status.getCode() == code;
    }
  }

  /** A status for the log: "OK", or "CODE: description". */
  private static String show(Status s) {
    return s.getDescription() == null ? s.getCode().name() : s.getCode() + ": " + s.getDescription();
  }

  private static StreamConfig config(String region) {
    return StreamConfig.newBuilder().setRegionName(region).build();
  }

  private Result putStream(String region, List<List<Entry>> chunks) {
    return stream(region, true, chunks);
  }

  /** PutStream: an optional StreamConfig message, then one message per chunk. */
  private Result stream(String region, boolean withConfig, List<List<Entry>> chunks) {
    StreamCall<PutStreamRequest, PutStreamResponse> call = new StreamCall<>();
    GemFireGrpc.newStub(channel).withDeadlineAfter(TIMEOUT_S, TimeUnit.SECONDS).putStream(call);
    Iterator<List<Entry>> it = chunks.iterator();
    try {
      if (withConfig) {
        call.send(PutStreamRequest.newBuilder().setConfig(config(region)).build());
      } else if (it.hasNext()) {
        call.send(PutStreamRequest.newBuilder().addAllEntries(it.next()).build());
      }
      while (it.hasNext()) call.send(PutStreamRequest.newBuilder().addAllEntries(it.next()).build());
      call.halfClose();
      call.await(TIMEOUT_S, TimeUnit.SECONDS);
      return new Result(Status.OK, call.sent(), List.of());
    } catch (StatusRuntimeException e) {
      return new Result(e.getStatus(), call.sent(), List.of());
    }
  }

  private Result getStream(String region, List<List<Key>> chunks) {
    List<Entry> found = new CopyOnWriteArrayList<>();
    StreamCall<GetStreamRequest, GetStreamResponse> call = new StreamCall<>(r -> found.addAll(r.getEntriesList()));
    GemFireGrpc.newStub(channel).withDeadlineAfter(TIMEOUT_S, TimeUnit.SECONDS).getStream(call);
    try {
      call.send(GetStreamRequest.newBuilder().setConfig(config(region)).build());
      for (List<Key> chunk : chunks) call.send(GetStreamRequest.newBuilder().addAllKeys(chunk).build());
      call.halfClose();
      call.await(TIMEOUT_S, TimeUnit.SECONDS);
      return new Result(Status.OK, call.sent(), found);
    } catch (StatusRuntimeException e) {
      return new Result(e.getStatus(), call.sent(), found);
    }
  }

  private Result removeStream(String region, List<List<Key>> chunks) {
    StreamCall<RemoveStreamRequest, RemoveStreamResponse> call = new StreamCall<>();
    GemFireGrpc.newStub(channel).withDeadlineAfter(TIMEOUT_S, TimeUnit.SECONDS).removeStream(call);
    try {
      call.send(RemoveStreamRequest.newBuilder().setConfig(config(region)).build());
      for (List<Key> chunk : chunks) call.send(RemoveStreamRequest.newBuilder().addAllKeys(chunk).build());
      call.halfClose();
      call.await(TIMEOUT_S, TimeUnit.SECONDS);
      return new Result(Status.OK, call.sent(), List.of());
    } catch (StatusRuntimeException e) {
      return new Result(e.getStatus(), call.sent(), List.of());
    }
  }

  /** KeySetStream: every response message (page of keys). Throws on failure. */
  private List<KeySetStreamResponse> keySet(String region) {
    List<KeySetStreamResponse> pages = new ArrayList<>();
    GemFireGrpc.newBlockingStub(channel).withDeadlineAfter(TIMEOUT_S, TimeUnit.SECONDS)
        .keySetStream(KeySetStreamRequest.newBuilder().setRegionName(region).build())
        .forEachRemaining(pages::add);
    return pages;
  }

  private void removeAll(String region) {
    List<Key> keys = keySet(region).stream().flatMap(p -> p.getKeysList().stream()).toList();
    Result r = removeStream(region, chunks(keys, 1_000));
    if (!r.ok()) throw r.status().asRuntimeException();
  }

  private long entryCount(String region) {
    return GemFireGrpc.newBlockingStub(channel).withDeadlineAfter(10, TimeUnit.SECONDS)
        .entryCount(EntryCountRequest.newBuilder().setRegionName(region).build()).getSize();
  }

  /** Polls until {@code done} or the timeout; returns the milliseconds waited. */
  private static long waitFor(java.util.function.BooleanSupplier done, long timeoutMs) {
    long t0 = System.nanoTime();
    while (!done.getAsBoolean() && (System.nanoTime() - t0) / 1_000_000 < timeoutMs) {
      try {
        Thread.sleep(5);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        break;
      }
    }
    return (System.nanoTime() - t0) / 1_000_000;
  }

  /** Entries prefix+from .. prefix+(to-1), each valued "v" + i. */
  private static List<Entry> range(String prefix, int from, int to) {
    return IntStream.range(from, to).mapToObj(i -> entry(key(prefix + i), str("v" + i))).toList();
  }

  private static <T> List<List<T>> chunks(List<T> items, int size) {
    List<List<T>> out = new ArrayList<>();
    for (int i = 0; i < items.size(); i += size) out.add(items.subList(i, Math.min(items.size(), i + size)));
    return out;
  }

  private static Entry entry(Key k, Value v) {
    return Entry.newBuilder().setKey(k).setValue(v).build();
  }

  private static Key key(String s) {
    return Key.newBuilder().setString(s).build();
  }

  private static Value str(String s) {
    return Value.newBuilder().setString(s).build();
  }
}
