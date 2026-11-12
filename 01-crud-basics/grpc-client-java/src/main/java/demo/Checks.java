package demo;

/** Narrated assertions for demo mode. Failures are counted, not thrown, so the whole tour runs. */
final class Checks {

  private final String client;
  private final Metrics metrics;
  private int passed;
  private int failed;

  Checks(String client, Metrics metrics) {
    this.client = client;
    this.metrics = metrics;
  }

  void section(String title) {
    System.out.printf("%n[%s] ── %s %s%n", client, title, "─".repeat(Math.max(3, 64 - title.length())));
  }

  void check(String description, boolean ok) {
    check(description, ok, null);
  }

  void check(String description, boolean ok, Object observed) {
    String suffix = observed == null ? "" : "   → " + observed;
    System.out.printf("[%s]   %s %s%s%n", client, ok ? "✔" : "✘ FAILED:", description, suffix);
    metrics.recordCheck(ok);
    if (ok) passed++;
    else failed++;
  }

  void note(String text) {
    System.out.printf("[%s]     ℹ %s%n", client, text);
  }

  int failed() {
    return failed;
  }

  void summary() {
    System.out.printf(
        "%n[%s] ══ demo complete: %d passed, %d failed ══%n%n", client, passed, failed);
  }
}
