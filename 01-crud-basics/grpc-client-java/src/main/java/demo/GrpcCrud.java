package demo;

import com.google.protobuf.ByteString;
import com.vmware.gemfire.grpc.v1.ContainsKeyRequest;
import com.vmware.gemfire.grpc.v1.Entry;
import com.vmware.gemfire.grpc.v1.EntryCountRequest;
import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import com.vmware.gemfire.grpc.v1.GemFireGrpc.GemFireBlockingStub;
import com.vmware.gemfire.grpc.v1.GetAndPutRequest;
import com.vmware.gemfire.grpc.v1.GetAndRemoveRequest;
import com.vmware.gemfire.grpc.v1.GetAndReplaceRequest;
import com.vmware.gemfire.grpc.v1.GetRequest;
import com.vmware.gemfire.grpc.v1.Key;
import com.vmware.gemfire.grpc.v1.PutIfAbsentAndGetRequest;
import com.vmware.gemfire.grpc.v1.PutIfAbsentRequest;
import com.vmware.gemfire.grpc.v1.PutRequest;
import com.vmware.gemfire.grpc.v1.PutStreamRequest;
import com.vmware.gemfire.grpc.v1.PutStreamResponse;
import com.vmware.gemfire.grpc.v1.RemoveIfRequest;
import com.vmware.gemfire.grpc.v1.RemoveRequest;
import com.vmware.gemfire.grpc.v1.ReplaceIfRequest;
import com.vmware.gemfire.grpc.v1.ReplaceRequest;
import com.vmware.gemfire.grpc.v1.StreamConfig;
import com.vmware.gemfire.grpc.v1.Value;
import io.grpc.ManagedChannel;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;

/** The 13 unary operations over gRPC, for the HTTP API. */
final class GrpcCrud implements Crud {

  private final ManagedChannel channel;
  private final GemFireBlockingStub stub;
  private final Settings cfg;
  private final String region;
  /**
   * One payload per size. Every write of a given size uses the same bytes, so ReplaceIf and
   * RemoveIf succeed whenever the key exists.
   */
  private final Map<Integer, Value> payloads = new ConcurrentHashMap<>();

  GrpcCrud(ManagedChannel channel, Settings cfg) {
    this.channel = channel;
    this.stub = GemFireGrpc.newBlockingStub(channel);
    this.cfg = cfg;
    this.region = cfg.region();
  }

  @Override
  public Object execute(String op, String key, int size) {
    Key k = key == null ? null : Key.newBuilder().setString(key).build();
    Value v = payload(size);
    GemFireBlockingStub s = stub.withDeadlineAfter(10, TimeUnit.SECONDS);
    return switch (op) {
      case "Put" -> {
        s.put(PutRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build());
        yield null;
      }
      case "Get" -> bytes(s.get(GetRequest.newBuilder().setRegionName(region).setKey(k).build()).getValue());
      case "GetAndPut" -> bytes(s.getAndPut(GetAndPutRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build()).getValue());
      case "Remove" -> s.remove(RemoveRequest.newBuilder().setRegionName(region).setKey(k).build()).getRemoved();
      case "RemoveIf" -> s.removeIf(RemoveIfRequest.newBuilder().setRegionName(region).setKey(k).setExpectedValue(v).build()).getRemoved();
      case "GetAndRemove" -> bytes(s.getAndRemove(GetAndRemoveRequest.newBuilder().setRegionName(region).setKey(k).build()).getValue());
      case "PutIfAbsent" -> s.putIfAbsent(PutIfAbsentRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build()).getInserted();
      case "PutIfAbsentAndGet" -> bytes(s.putIfAbsentAndGet(PutIfAbsentAndGetRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build()).getValue());
      case "Replace" -> s.replace(ReplaceRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build()).getReplaced();
      case "ReplaceIf" -> s.replaceIf(ReplaceIfRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).setExpectedValue(v).build()).getReplaced();
      case "GetAndReplace" -> bytes(s.getAndReplace(GetAndReplaceRequest.newBuilder().setRegionName(region).setKey(k).setValue(v).build()).getValue());
      case "ContainsKey" -> s.containsKey(ContainsKeyRequest.newBuilder().setRegionName(region).setKey(k).build()).getContains();
      case "EntryCount" -> s.entryCount(EntryCountRequest.newBuilder().setRegionName(region).build()).getSize();
      default -> throw new IllegalArgumentException("Unknown op: " + op);
    };
  }

  /** Preload with a PutStream (bulk streaming is covered in depth in 02-bulk-streaming). */
  @Override
  public int preload(int count, int size) throws Exception {
    Value v = payload(size);
    CompletableFuture<Void> done = new CompletableFuture<>();
    StreamObserver<PutStreamRequest> stream =
        GemFireGrpc.newStub(channel).putStream(new StreamObserver<>() {
          public void onNext(PutStreamResponse r) {}
          public void onError(Throwable t) { done.completeExceptionally(t); }
          public void onCompleted() { done.complete(null); }
        });
    stream.onNext(PutStreamRequest.newBuilder().setConfig(StreamConfig.newBuilder().setRegionName(region)).build());
    PutStreamRequest.Builder batch = PutStreamRequest.newBuilder();
    for (int i = 0; i < count; i++) {
      batch.addEntries(Entry.newBuilder().setKey(Key.newBuilder().setString(cfg.keyPrefix() + i)).setValue(v));
      // Flush every 500 entries or ~1 MiB, whichever comes first: one stream message must stay
      // under the server's gemfire.grpc.max-message-size (4 MiB by default).
      if (batch.getEntriesCount() == 500 || (long) batch.getEntriesCount() * size >= 1 << 20) {
        stream.onNext(batch.build());
        batch = PutStreamRequest.newBuilder();
      }
    }
    if (batch.getEntriesCount() > 0) stream.onNext(batch.build());
    stream.onCompleted();
    done.get(5, TimeUnit.MINUTES);
    return count;
  }

  @Override
  public String errorCode(Throwable t) {
    return t instanceof StatusRuntimeException e ? e.getStatus().getCode().name() : t.getClass().getSimpleName();
  }

  private Value payload(int size) {
    return payloads.computeIfAbsent(size, n -> {
      byte[] b = new byte[n];
      new Random(n).nextBytes(b);
      return Value.newBuilder().setBytes(ByteString.copyFrom(b)).build();
    });
  }

  /** Size of a returned value in bytes, or null when the response left `value` unset. */
  private static Integer bytes(Value v) {
    return switch (v.getValueValueCase()) {
      case VALUEVALUE_NOT_SET -> null;
      case BYTES -> v.getBytes().size();
      case STRING -> v.getString().length();
      default -> v.getSerializedSize();
    };
  }
}
