package demo;

/**
 * Demo settings, read from environment variables (see ../.env and docker-compose.yml). The HTTP
 * server's PORT and HTTP_THREADS are Spring Boot properties (application.properties).
 */
record Config(
    String client,
    String example,
    String region,
    String demoRegion,
    String keyPrefix,
    int payloadBytes,
    boolean demoOnStart) {

  static Config fromEnv(String client) {
    String shortName = client.replace("-java", "");
    return new Config(
        client,
        env("EXAMPLE", "02-bulk-streaming"),
        env("REGION", "bulk-" + shortName),
        env("DEMO_REGION", "demo-" + shortName),
        env("KEY_PREFIX", shortName + ":"),
        Integer.parseInt(env("PAYLOAD_BYTES", "1024")),
        Boolean.parseBoolean(env("DEMO_ON_START", "true")));
  }

  /** The same settings for another region (the demos reuse the bulk code on their own region). */
  Config withRegion(String name) {
    return new Config(client, example, name, demoRegion, keyPrefix, payloadBytes, demoOnStart);
  }

  static String env(String name, String def) {
    String v = System.getenv(name);
    return v == null || v.isBlank() ? def : v.trim();
  }
}
