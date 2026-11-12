package demo;

import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientCacheFactory;

/**
 * Native GemFire Java client: the baseline the gRPC client is compared against.
 *
 * <p>Connects through the locator. PR single-hop is on by default, so each op goes straight to
 * the server that owns the key's bucket.
 *
 * <p>Env: LOCATOR_HOST, LOCATOR_PORT plus the workload settings in {@link Config}.
 */
public final class Main {

  public static void main(String[] args) throws Exception {
    Config cfg = Config.fromEnv("native-java");
    String host = Config.env("LOCATOR_HOST", "locator");
    int port = Integer.parseInt(Config.env("LOCATOR_PORT", "10334"));
    System.out.printf("[native-java] mode=%s locator=%s[%d] region=%s%n", cfg.mode(), host, port, cfg.region());

    ClientCache cache = new ClientCacheFactory()
        .addPoolLocator(host, port)
        .setPoolReadTimeout(10_000)
        .set("log-level", "warn")
        .create();

    int failures = 0;
    try (Metrics metrics = new Metrics(cfg)) {
      if (cfg.runDemo()) {
        Checks checks = new Checks(cfg.client(), metrics);
        new Demo(cache, cfg, checks).run();
        checks.summary();
        failures = checks.failed();
      }
      if (cfg.runBench()) {
        new Bench(cfg, metrics, new NativeOps(cache, cfg)).run();
      }
    } finally {
      cache.close();
    }
    System.exit(failures == 0 ? 0 : 1);
  }
}
