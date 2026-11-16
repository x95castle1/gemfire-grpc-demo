package demo;

import com.google.protobuf.ByteString;
import com.vmware.gemfire.grpc.v1.Entry;
import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import com.vmware.gemfire.grpc.v1.GemFireGrpc.GemFireFutureStub;
import com.vmware.gemfire.grpc.v1.GemFireGrpc.GemFireStub;
import com.vmware.gemfire.grpc.v1.GetRequest;
import com.vmware.gemfire.grpc.v1.PutRequest;
import com.vmware.gemfire.grpc.v1.RemoveRequest;
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
import com.google.common.util.concurrent.FutureCallback;
import com.google.common.util.concurrent.Futures;
import com.google.common.util.concurrent.ListenableFuture;
import com.google.common.util.concurrent.MoreExecutors;
import io.grpc.Channel;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.IntFunction;
import java.util.function.Predicate;

/**
 * The bulk operations over gRPC streams, for the HTTP API. Each call is ONE stream: the first
 * message carries the {@link StreamConfig} (the region), then one message per chunk of
 * {@code batch} keys or entries, sent back to back without waiting for the server. The server
 * re-batches what it receives into putAll/getAll/removeAll operations of at most
 * {@code gemfire.grpc.bulk-batch-size} entries.
 */
final class GrpcBulk implements Bulk {

  /** Long enough for millions of entries; the server stops work on a call that runs past it. */
  static final long DEADLINE_MINUTES = 5;

  private final Channel channel;
  private final Settings cfg;
  private final String region;
  /** One payload per size: every write of a given size uses the same bytes. */
  private final Map<Integer, Value> payloads = new ConcurrentHashMap<>();

  GrpcBulk(Channel channel, Settings cfg) {
    this.channel = channel;
    this.cfg = cfg;
    this.region = cfg.region();
  }

  private GemFireStub stub() {
    return GemFireGrpc.newStub(channel).withDeadlineAfter(DEADLINE_MINUTES, TimeUnit.MINUTES);
  }

  private StreamConfig config() {
    return StreamConfig.newBuilder().setRegionName(region).build();
  }

  private Key key(int i) {
    return Key.newBuilder().setString(cfg.keyPrefix() + i).build();
  }

  @Override
  public Bulk.Result put(int start, int count, int batch, int size) {
    Value v = payload(size);
    int chunk = Bulk.chunk(batch, size);
    StreamCall<PutStreamRequest, PutStreamResponse> call = new StreamCall<>();
    stub().putStream(call);
    call.send(PutStreamRequest.newBuilder().setConfig(config()).build());
    PutStreamRequest.Builder msg = PutStreamRequest.newBuilder();
    for (int i = start; i < start + count; i++) {
      msg.addEntries(Entry.newBuilder().setKey(key(i)).setValue(v));
      if (msg.getEntriesCount() == chunk) {
        call.send(msg.build());
        msg = PutStreamRequest.newBuilder();
      }
    }
    if (msg.getEntriesCount() > 0) call.send(msg.build());
    call.halfClose();
    call.await(DEADLINE_MINUTES, TimeUnit.MINUTES);
    return new Bulk.Result(count, call.sent() - 1);   // -1: the config message
  }

  @Override
  public Bulk.Result get(int start, int count, int batch) {
    // Bidirectional: entries come back while later chunks are still being sent. Only keys that
    // were found come back at all.
    AtomicLong found = new AtomicLong();
    StreamCall<GetStreamRequest, GetStreamResponse> call =
        new StreamCall<>(r -> found.addAndGet(r.getEntriesCount()));
    stub().getStream(call);
    call.send(GetStreamRequest.newBuilder().setConfig(config()).build());
    GetStreamRequest.Builder msg = GetStreamRequest.newBuilder();
    for (int i = start; i < start + count; i++) {
      msg.addKeys(key(i));
      if (msg.getKeysCount() == batch) {
        call.send(msg.build());
        msg = GetStreamRequest.newBuilder();
      }
    }
    if (msg.getKeysCount() > 0) call.send(msg.build());
    call.halfClose();
    call.await(DEADLINE_MINUTES, TimeUnit.MINUTES);
    return new Bulk.Result(found.get(), call.sent() - 1);
  }

  @Override
  public Bulk.Result remove(int start, int count, int batch) {
    List<Key> keys = new ArrayList<>(count);
    for (int i = start; i < start + count; i++) keys.add(key(i));
    return removeKeys(keys, batch);
  }

  private Bulk.Result removeKeys(List<Key> keys, int batch) {
    StreamCall<RemoveStreamRequest, RemoveStreamResponse> call = new StreamCall<>();
    stub().removeStream(call);
    call.send(RemoveStreamRequest.newBuilder().setConfig(config()).build());
    for (int i = 0; i < keys.size(); i += batch) {
      call.send(RemoveStreamRequest.newBuilder().addAllKeys(keys.subList(i, Math.min(keys.size(), i + batch))).build());
    }
    call.halfClose();
    call.await(DEADLINE_MINUTES, TimeUnit.MINUTES);
    return new Bulk.Result(keys.size(), call.sent() - 1);
  }

  // --- mode=unary: the same work without streams, one RPC per entry ---------------------------

  @Override
  public Bulk.Result unaryPut(int start, int count, int size, int inflight) throws Exception {
    Value v = payload(size);
    return unary(start, count, inflight,
        i -> unaryStub().put(PutRequest.newBuilder().setRegionName(region).setKey(key(i)).setValue(v).build()),
        r -> true);
  }

  @Override
  public Bulk.Result unaryGet(int start, int count, int inflight) throws Exception {
    return unary(start, count, inflight,
        i -> unaryStub().get(GetRequest.newBuilder().setRegionName(region).setKey(key(i)).build()),
        r -> r.hasValue());
  }

  @Override
  public Bulk.Result unaryRemove(int start, int count, int inflight) throws Exception {
    return unary(start, count, inflight,
        i -> unaryStub().remove(RemoveRequest.newBuilder().setRegionName(region).setKey(key(i)).build()),
        r -> true);
  }

  private GemFireFutureStub unaryStub() {
    return GemFireGrpc.newFutureStub(channel).withDeadlineAfter(10, TimeUnit.SECONDS);
  }

  /**
   * Issues {@code call(i)} for every key, keeping at most {@code inflight} RPCs outstanding (1 =
   * wait for each response before the next request). All of them share the one channel, i.e.
   * one HTTP/2 connection. Stops at the first failure and throws it.
   */
  private <T> Bulk.Result unary(int start, int count, int inflight, IntFunction<ListenableFuture<T>> call,
      Predicate<T> counts) throws Exception {
    Semaphore permits = new Semaphore(inflight);
    AtomicLong hits = new AtomicLong();
    AtomicReference<Throwable> failure = new AtomicReference<>();
    long calls = 0;
    for (int i = start; i < start + count && failure.get() == null; i++) {
      permits.acquire();
      calls++;
      Futures.addCallback(call.apply(i), new FutureCallback<T>() {
        public void onSuccess(T r) {
          if (counts.test(r)) hits.incrementAndGet();
          permits.release();
        }

        public void onFailure(Throwable t) {
          failure.compareAndSet(null, t);
          permits.release();
        }
      }, MoreExecutors.directExecutor());
    }
    permits.acquire(inflight);   // wait for the last ones
    if (failure.get() != null) throw Status.fromThrowable(failure.get()).asRuntimeException();
    return new Bulk.Result(hits.get(), calls);
  }

  // --- KeySetStream ------------------------------------------------------------------------------

  @Override
  public Bulk.Result keySet() throws Exception {
    AtomicLong keys = new AtomicLong();
    AtomicLong messages = new AtomicLong();
    awaitServerStream(r -> {
      keys.addAndGet(r.getKeysCount());
      messages.incrementAndGet();
    });
    return new Bulk.Result(keys.get(), messages.get());
  }

  @Override
  public long reset() throws Exception {
    List<Key> all = new ArrayList<>();
    awaitServerStream(r -> all.addAll(r.getKeysList()));
    if (!all.isEmpty()) removeKeys(all, 1000);
    return all.size();
  }

  /** Runs a KeySetStream on the region, handing each response message to {@code each}. */
  private void awaitServerStream(java.util.function.Consumer<KeySetStreamResponse> each) throws Exception {
    CompletableFuture<Void> done = new CompletableFuture<>();
    stub().keySetStream(KeySetStreamRequest.newBuilder().setRegionName(region).build(),
        new StreamObserver<>() {
          public void onNext(KeySetStreamResponse r) { each.accept(r); }
          public void onError(Throwable t) { done.completeExceptionally(t); }
          public void onCompleted() { done.complete(null); }
        });
    try {
      done.get(DEADLINE_MINUTES, TimeUnit.MINUTES);
    } catch (ExecutionException e) {
      throw Status.fromThrowable(e.getCause()).asRuntimeException();
    }
  }

  @Override
  public String errorCode(Throwable t) {
    Status s = Status.fromThrowable(t);
    return s.getCode() == Status.Code.UNKNOWN && !(t instanceof io.grpc.StatusRuntimeException)
        ? t.getClass().getSimpleName() : s.getCode().name();
  }

  private Value payload(int size) {
    return payloads.computeIfAbsent(size, n -> {
      byte[] b = new byte[n];
      new Random(n).nextBytes(b);
      return Value.newBuilder().setBytes(ByteString.copyFrom(b)).build();
    });
  }
}
