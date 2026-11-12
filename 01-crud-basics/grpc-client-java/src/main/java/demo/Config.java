package demo;

/** Service settings, read from environment variables (see ../.env and docker-compose.yml). */
record Config(
    String client,
    String example,
    String region,
    String demoRegion,
    String keyPrefix,
    int payloadBytes,
    int port,
    int httpThreads,
    boolean demoOnStart) {

  static Config fromEnv(String client) {
    String shortName = client.replace("-java", "");
    return new Config(
        client,
        env("EXAMPLE", "01-crud-basics"),
        env("REGION", "crud"),
        env("DEMO_REGION", "demo-" + shortName),
        env("KEY_PREFIX", shortName + ":"),
        Integer.parseInt(env("PAYLOAD_BYTES", "1024")),
        Integer.parseInt(env("PORT", "8080")),
        Integer.parseInt(env("HTTP_THREADS", "64")),
        Boolean.parseBoolean(env("DEMO_ON_START", "true")));
  }

  static String env(String name, String def) {
    String v = System.getenv(name);
    return v == null || v.isBlank() ? def : v.trim();
  }
}
