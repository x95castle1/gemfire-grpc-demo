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
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

/** Bench operations over gRPC. Keys and the payload are pre-built so only the RPC is timed. */
final class GrpcOps implements Bench.Ops {

  private final ManagedChannel channel;
  private final GemFireBlockingStub stub;
  private final Config cfg;
  private final String region;
  private final Key[] keys;
  private final Value payload;

  GrpcOps(ManagedChannel channel, Config cfg) {
    this.channel = channel;
    this.stub = GemFireGrpc.newBlockingStub(channel);
    this.cfg = cfg;
    this.region = cfg.region();
    this.keys = new Key[cfg.keySpace()];
    for (int i = 0; i < keys.length; i++) keys[i] = Key.newBuilder().setString("grpc:" + i).build();
    byte[] bytes = new byte[cfg.payloadBytes()];
    new java.util.Random(42).nextBytes(bytes);
    // Every put writes the same payload, so ReplaceIf/RemoveIf succeed whenever the key exists.
    this.payload = Value.newBuilder().setBytes(ByteString.copyFrom(bytes)).build();
  }

  /** Preload with a PutStream (bulk streaming is covered in depth in 02-bulk-streaming). */
  @Override
  public void preload() throws Exception {
    CompletableFuture<Void> done = new CompletableFuture<>();
    StreamObserver<PutStreamRequest> stream =
        GemFireGrpc.newStub(channel).putStream(new StreamObserver<>() {
          public void onNext(PutStreamResponse r) {}
          public void onError(Throwable t) { done.completeExceptionally(t); }
          public void onCompleted() { done.complete(null); }
        });
    stream.onNext(PutStreamRequest.newBuilder()
        .setConfig(StreamConfig.newBuilder().setRegionName(region)).build());
    PutStreamRequest.Builder batch = PutStreamRequest.newBuilder();
    for (Key k : keys) {
      batch.addEntries(Entry.newBuilder().setKey(k).setValue(payload));
      if (batch.getEntriesCount() == 500) {
        stream.onNext(batch.build());
        batch = PutStreamRequest.newBuilder();
      }
    }
    if (batch.getEntriesCount() > 0) stream.onNext(batch.build());
    stream.onCompleted();
    done.get(2, TimeUnit.MINUTES);
  }

  @Override
  public void execute(String op, int i) {
    Key k = keys[i];
    GemFireBlockingStub s = stub.withDeadlineAfter(10, TimeUnit.SECONDS);
    switch (op) {
      case "Put" -> s.put(PutRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "Get" -> s.get(GetRequest.newBuilder().setRegionName(region).setKey(k).build());
      case "GetAndPut" -> s.getAndPut(GetAndPutRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "Remove" -> s.remove(RemoveRequest.newBuilder().setRegionName(region).setKey(k).build());
      case "RemoveIf" -> s.removeIf(RemoveIfRequest.newBuilder().setRegionName(region).setKey(k).setExpectedValue(payload).build());
      case "GetAndRemove" -> s.getAndRemove(GetAndRemoveRequest.newBuilder().setRegionName(region).setKey(k).build());
      case "PutIfAbsent" -> s.putIfAbsent(PutIfAbsentRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "PutIfAbsentAndGet" -> s.putIfAbsentAndGet(PutIfAbsentAndGetRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "Replace" -> s.replace(ReplaceRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "ReplaceIf" -> s.replaceIf(ReplaceIfRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).setExpectedValue(payload).build());
      case "GetAndReplace" -> s.getAndReplace(GetAndReplaceRequest.newBuilder().setRegionName(region).setKey(k).setValue(payload).build());
      case "ContainsKey" -> s.containsKey(ContainsKeyRequest.newBuilder().setRegionName(region).setKey(k).build());
      case "EntryCount" -> s.entryCount(EntryCountRequest.newBuilder().setRegionName(region).build());
      default -> throw new IllegalArgumentException("Unknown op: " + op);
    }
  }

  @Override
  public String errorCode(Throwable t) {
    return t instanceof StatusRuntimeException e ? e.getStatus().getCode().name() : t.getClass().getSimpleName();
  }
}
