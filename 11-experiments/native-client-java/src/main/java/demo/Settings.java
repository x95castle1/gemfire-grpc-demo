package demo;

/**
 * Demo settings, read from environment variables (see ../.env and docker-compose.yml). The HTTP
 * server's PORT and HTTP_THREADS are Spring Boot properties (application.properties).
 */
record Settings(
    String client,
    String example,
    String region,
    String replicateRegion,
    String demoRegion,
    String keyPrefix,
    int payloadBytes,
    boolean demoOnStart) {

  static Settings fromEnv(String client) {
    String shortName = client.replace("-java", "");
    return new Settings(
        client,
        env("EXAMPLE", "11-experiments"),
        env("REGION", "crud-partition"),
        env("REPLICATE_REGION", "crud-replicate"),
        env("DEMO_REGION", "demo-" + shortName),
        env("KEY_PREFIX", shortName + ":"),
        Integer.parseInt(env("PAYLOAD_BYTES", "1024")),
        Boolean.parseBoolean(env("DEMO_ON_START", "true")));
  }

  /** The region a request's {@code ?region=} names: partition (the default) or replicate. */
  String regionFor(String param) {
    if (param == null || param.isEmpty() || param.equals("partition")) return region;
    if (param.equals("replicate")) return replicateRegion;
    throw new IllegalArgumentException("region must be partition or replicate");
  }

  static String env(String name, String def) {
    String v = System.getenv(name);
    return v == null || v.isBlank() ? def : v.trim();
  }
}
