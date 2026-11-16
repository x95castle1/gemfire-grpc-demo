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
 * serves the CRUD API that k6 load tests call (see {@link CrudController}). Plain Spring Boot: the gRPC
 * channel is an ordinary bean built with grpc-java.
 *
 * <p>Env: GRPC_TARGET (default dns:///server1:50051) plus the settings in {@link Settings}.
 */
@SpringBootApplication
public class GrpcClientApplication {

  private static final Logger log = LoggerFactory.getLogger(GrpcClientApplication.class);

  public static void main(String[] args) {
    SpringApplication.run(GrpcClientApplication.class, args);
  }

  @Bean
  Settings settings() {
    return Settings.fromEnv("grpc-java");
  }

  /** One channel for the whole app; every RPC on it is timed by the interceptor (demo_rpc_duration_seconds). */
  @Bean(destroyMethod = "shutdown")
  ManagedChannel channel(Settings cfg, DemoMetrics metrics) {
    String target = Settings.env("GRPC_TARGET", "dns:///server1:50051");
    log.info("grpc-java: target {}", target);
    metrics.initRpcs(GemFireGrpc.getServiceDescriptor().getMethods().stream()
        .map(m -> m.getBareMethodName()).toList(), cfg.region(), cfg.demoRegion());
    return ManagedChannelBuilder.forTarget(target).usePlaintext()
        .intercept(new RpcMetricsInterceptor(metrics))
        .build();
  }

  @Bean
  Crud crud(ManagedChannel channel, Settings cfg) {
    return new GrpcCrud(channel, cfg);
  }

  /** The narrated demo, run at startup and by POST /demo. */
  @Bean
  Supplier<Checks.Report> demo(ManagedChannel channel, Settings cfg, DemoMetrics metrics) {
    return () -> {
      Checks checks = new Checks(cfg.client(), metrics);
      try {
        new Demo(GemFireGrpc.newBlockingStub(channel), cfg, checks).run();
      } catch (RuntimeException e) {
        checks.abort(e);
      }
      return checks.finish();
    };
  }
}
