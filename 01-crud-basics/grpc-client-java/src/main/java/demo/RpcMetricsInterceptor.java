package demo;

import com.google.protobuf.Descriptors.FieldDescriptor;
import com.google.protobuf.Message;
import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ClientCall;
import io.grpc.ClientInterceptor;
import io.grpc.ForwardingClientCall;
import io.grpc.ForwardingClientCallListener;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Status;

/**
 * A client interceptor that times every gRPC call on the channel into
 * {@code demo_rpc_duration_seconds}: unary and streaming alike, from start until the server
 * closes the call, labeled with the method, the region and the final status code.
 *
 * <p>This is the RPC-level view. {@code demo_op_latency_seconds} (recorded in {@link CrudController}) stays
 * the operation-level view that's compared with the native client, which has no interceptors.
 */
final class RpcMetricsInterceptor implements ClientInterceptor {

  private final DemoMetrics metrics;

  RpcMetricsInterceptor(DemoMetrics metrics) {
    this.metrics = metrics;
  }

  @Override
  public <Q, R> ClientCall<Q, R> interceptCall(MethodDescriptor<Q, R> method, CallOptions options, Channel next) {
    String name = method.getBareMethodName();
    return new ForwardingClientCall.SimpleForwardingClientCall<>(next.newCall(method, options)) {
      private volatile String region = "";

      @Override
      public void start(Listener<R> listener, Metadata headers) {
        long started = System.nanoTime();
        super.start(new ForwardingClientCallListener.SimpleForwardingClientCallListener<>(listener) {
          @Override
          public void onClose(Status status, Metadata trailers) {
            metrics.recordRpc(name, region, status.getCode().name(), System.nanoTime() - started);
            super.onClose(status, trailers);
          }
        }, headers);
      }

      @Override
      public void sendMessage(Q message) {
        if (region.isEmpty() && message instanceof Message m) region = regionOf(m);
        super.sendMessage(message);
      }
    };
  }

  /**
   * The request's region, read with protobuf reflection: {@code region_name} on unary requests,
   * {@code config.region_name} on the first message of a stream, "" for Query (OQL names its
   * regions in the query text).
   */
  static String regionOf(Message m) {
    FieldDescriptor regionName = m.getDescriptorForType().findFieldByName("region_name");
    if (regionName != null) return (String) m.getField(regionName);
    FieldDescriptor config = m.getDescriptorForType().findFieldByName("config");
    if (config != null && m.hasField(config)) return regionOf((Message) m.getField(config));
    return "";
  }
}
