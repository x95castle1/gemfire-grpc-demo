package demo;

import com.vmware.gemfire.grpc.v1.GemFireGrpc;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import java.util.function.Supplier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

/**
 * GemFire for gRPC client as a small Spring Boot service: runs the narrated demo at startup, then
 * serves the bulk API that k6 load tests call (see {@link Api}). Plain Spring Boot: the gRPC
 * channel is an ordinary bean built with grpc-java.
 *
 * <p>Env: GRPC_TARGET (default dns:///server1:50051) plus the settings in {@link Config}.
 */
@SpringBootApplication
public class Application {

  private static final Logger log = LoggerFactory.getLogger(Application.class);

  public static void main(String[] args) {
    SpringApplication.run(Application.class, args);
  }

  @Bean
  Config config() {
    return Config.fromEnv("grpc-java");
  }

  /** One channel for the whole app; every RPC on it is timed by the interceptor (demo_rpc_duration_seconds). */
  @Bean(destroyMethod = "shutdown")
  ManagedChannel channel(Config cfg, Metrics metrics) {
    String target = Config.env("GRPC_TARGET", "dns:///server1:50051");
    log.info("grpc-java: target {}", target);
    metrics.initRpcs(GemFireGrpc.getServiceDescriptor().getMethods().stream()
        .map(m -> m.getBareMethodName()).toList(), cfg.region(), cfg.demoRegion());
    return ManagedChannelBuilder.forTarget(target).usePlaintext()
        .intercept(new RpcMetrics(metrics))
        .build();
  }

  @Bean
  Api.Bulk bulk(ManagedChannel channel, Config cfg) {
    return new GrpcBulk(channel, cfg);
  }

  /** The narrated demo, run at startup and by POST /demo. */
  @Bean
  Supplier<Checks.Report> demo(ManagedChannel channel, Config cfg, Metrics metrics) {
    return () -> {
      Checks checks = new Checks(cfg.client(), metrics);
      try {
        new Demo(channel, cfg, checks).run();
      } catch (RuntimeException e) {
        checks.abort(e);
      }
      return checks.finish();
    };
  }
}
