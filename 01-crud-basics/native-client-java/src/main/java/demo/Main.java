package demo;

import org.apache.geode.cache.client.ClientCache;
import org.apache.geode.cache.client.ClientCacheFactory;

/**
 * Native GemFire Java client as a small HTTP service: the baseline the gRPC client is compared
 * against. It runs the narrated demo at startup, then serves the same CRUD API (see {@link Api}).
 *
 * <p>Connects through the locator. PR single-hop is on by default, so each op goes straight to
 * the server that owns the key's bucket.
 *
 * <p>Env: LOCATOR_HOST, LOCATOR_PORT plus the settings in {@link Config}.
 */
public final class Main {

  public static void main(String[] args) throws Exception {
    Config cfg = Config.fromEnv("native-java");
    String host = Config.env("LOCATOR_HOST", "locator");
    int port = Integer.parseInt(Config.env("LOCATOR_PORT", "10334"));
    System.out.printf("[native-java] locator=%s[%d] region=%s%n", host, port, cfg.region());

    ClientCache cache = new ClientCacheFactory()
        .addPoolLocator(host, port)
        .setPoolReadTimeout(10_000)
        .set("log-level", "warn")
        .create();
    Runtime.getRuntime().addShutdownHook(new Thread(cache::close));

    Metrics metrics = new Metrics(cfg);
    Api api = new Api(cfg, metrics, new NativeCrud(cache, cfg), () -> {
      Checks checks = new Checks(cfg.client(), metrics);
      try {
        new Demo(cache, cfg, checks).run();
      } catch (RuntimeException e) {
        checks.abort(e);
      }
      return checks.finish();
    });

    if (cfg.demoOnStart()) api.runDemo();
    api.start();
  }
}
