package demo;

import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;

/**
 * GemFire for gRPC client as a small HTTP service: runs the narrated demo at startup, then
 * serves the CRUD API that k6 load tests call (see {@link Api}).
 *
 * <p>Env: GRPC_TARGET (default dns:///server1:50051) plus the settings in {@link Config}.
 */
public final class Main {

  public static void main(String[] args) throws Exception {
    Config cfg = Config.fromEnv("grpc-java");
    String target = Config.env("GRPC_TARGET", "dns:///server1:50051");
    System.out.printf("[grpc-java] target=%s region=%s%n", target, cfg.region());

    ManagedChannel channel = ManagedChannelBuilder.forTarget(target).usePlaintext().build();
    Runtime.getRuntime().addShutdownHook(new Thread(channel::shutdown));

    Metrics metrics = new Metrics(cfg);
    Api api = new Api(cfg, metrics, new GrpcCrud(channel, cfg), () -> {
      Checks checks = new Checks(cfg.client(), metrics);
      try {
        new Demo(GemFireGrpc.newBlockingStub(channel), cfg, checks).run();
      } catch (RuntimeException e) {
        checks.abort(e);
      }
      return checks.finish();
    });

    if (cfg.demoOnStart()) api.runDemo();
    api.start();
  }
}
