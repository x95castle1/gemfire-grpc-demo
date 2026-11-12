package demo;

import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import java.util.concurrent.TimeUnit;

/**
 * GemFire for gRPC client: unary CRUD over a plaintext channel to one server.
 *
 * <p>Env: GRPC_TARGET (default dns:///server1:50051) plus the workload settings in {@link Config}.
 */
public final class Main {

  public static void main(String[] args) throws Exception {
    Config cfg = Config.fromEnv("grpc-java");
    String target = Config.env("GRPC_TARGET", "dns:///server1:50051");
    System.out.printf("[grpc-java] mode=%s target=%s region=%s%n", cfg.mode(), target, cfg.region());

    ManagedChannel channel = ManagedChannelBuilder.forTarget(target).usePlaintext().build();
    int failures = 0;
    try (Metrics metrics = new Metrics(cfg)) {
      if (cfg.runDemo()) {
        Checks checks = new Checks(cfg.client(), metrics);
        new Demo(GemFireGrpc.newBlockingStub(channel), cfg, checks).run();
        checks.summary();
        failures = checks.failed();
      }
      if (cfg.runBench()) {
        new Bench(cfg, metrics, new GrpcOps(channel, cfg)).run();
      }
    } finally {
      channel.shutdown().awaitTermination(5, TimeUnit.SECONDS);
    }
    System.exit(failures == 0 ? 0 : 1);
  }
}
