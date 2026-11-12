package demo;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;

/** Workload settings, read from environment variables (see ../.env). */
record Config(
    String client,
    String example,
    String mode,
    Duration benchDuration,
    int concurrency,
    int targetRate,
    int keySpace,
    int payloadBytes,
    String region,
    String demoRegion,
    Map<String, Integer> opMix,
    int metricsPort) {

  /**
   * Default unary op mix (weights). Op names are the gRPC RPC names; the native client uses the
   * same labels so the dashboards line up op-for-op.
   */
  static final String DEFAULT_OP_MIX =
      "Get=40,Put=15,ContainsKey=10,GetAndPut=5,PutIfAbsent=5,PutIfAbsentAndGet=2,Replace=5,"
          + "ReplaceIf=3,GetAndReplace=2,Remove=4,RemoveIf=3,GetAndRemove=3,EntryCount=1";

  static Config fromEnv(String client) {
    return new Config(
        client,
        env("EXAMPLE", "01-crud-basics"),
        env("MODE", "all").toLowerCase(),
        parseDuration(env("BENCH_DURATION", "15m")),
        Integer.parseInt(env("CONCURRENCY", "4")),
        Integer.parseInt(env("TARGET_RATE", "1000")),
        Integer.parseInt(env("KEY_SPACE", "10000")),
        Integer.parseInt(env("PAYLOAD_BYTES", "1024")),
        env("REGION", "crud"),
        env("DEMO_REGION", "demo-" + client.replace("-java", "")),
        parseMix(env("OP_MIX", DEFAULT_OP_MIX)),
        Integer.parseInt(env("METRICS_PORT", "8080")));
  }

  boolean runDemo() {
    return mode.equals("demo") || mode.equals("all");
  }

  boolean runBench() {
    return mode.equals("bench") || mode.equals("all");
  }

  static String env(String name, String def) {
    String v = System.getenv(name);
    return v == null || v.isBlank() ? def : v.trim();
  }

  /** Parses "500ms", "30s", "10m", "2h"; "0" means run until stopped. */
  static Duration parseDuration(String s) {
    if (s.equals("0")) return Duration.ZERO;
    if (s.endsWith("ms")) return Duration.ofMillis(Long.parseLong(s.substring(0, s.length() - 2)));
    long n = Long.parseLong(s.substring(0, s.length() - 1));
    return switch (s.charAt(s.length() - 1)) {
      case 's' -> Duration.ofSeconds(n);
      case 'm' -> Duration.ofMinutes(n);
      case 'h' -> Duration.ofHours(n);
      default -> throw new IllegalArgumentException("Bad duration: " + s);
    };
  }

  static Map<String, Integer> parseMix(String s) {
    Map<String, Integer> mix = new LinkedHashMap<>();
    for (String part : s.split(",")) {
      String[] kv = part.trim().split("=");
      int w = Integer.parseInt(kv[1].trim());
      if (w > 0) mix.put(kv[0].trim(), w);
    }
    return mix;
  }
}
