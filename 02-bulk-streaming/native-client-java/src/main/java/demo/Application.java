package demo;

import java.util.function.Supplier;
import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientCacheFactory;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

/**
 * Native GemFire Java client as a small Spring Boot service: the baseline the gRPC client is
 * compared against. It runs the narrated demo at startup, then serves the same bulk API (see
 * {@link Api}). Plain Spring Boot: the GemFire objects are ordinary beans built with the Region
 * API, no Spring Data for GemFire.
 *
 * <p>Connects through the locator. PR single-hop is on by default, so each op goes straight to
 * the server that owns the key's bucket, and a putAll/getAll/removeAll is split by bucket.
 *
 * <p>Env: LOCATOR_HOST, LOCATOR_PORT plus the settings in {@link Config}.
 */
@SpringBootApplication
public class Application {

  private static final Logger log = LoggerFactory.getLogger(Application.class);

  public static void main(String[] args) {
    SpringApplication.run(Application.class, args);
  }

  @Bean
  Config config() {
    return Config.fromEnv("native-java");
  }

  /** One client cache for the whole app, closed when Spring shuts down. */
  @Bean(destroyMethod = "close")
  ClientCache clientCache() {
    String host = Config.env("LOCATOR_HOST", "locator");
    int port = Integer.parseInt(Config.env("LOCATOR_PORT", "10334"));
    log.info("native-java: locator {}[{}]", host, port);
    return new ClientCacheFactory()
        .addPoolLocator(host, port)
        .setPoolReadTimeout(10_000)
        .set("log-level", "warn")
        .create();
  }

  @Bean
  Api.Bulk bulk(ClientCache cache, Config cfg) {
    return new NativeBulk(cache, cfg);
  }

  /** The narrated demo, run at startup and by POST /demo. */
  @Bean
  Supplier<Checks.Report> demo(ClientCache cache, Config cfg, Metrics metrics) {
    return () -> {
      Checks checks = new Checks(cfg.client(), metrics);
      try {
        new Demo(cache, cfg, checks).run();
      } catch (RuntimeException e) {
        checks.abort(e);
      }
      return checks.finish();
    };
  }
}
