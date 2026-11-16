package demo;

/**
 * Narrated assertions for the demo. Failures are counted rather than thrown, so the whole tour
 * runs. Output goes to stdout (container logs) and is also kept for the HTTP response.
 */
final class Checks {

  record Report(int passed, int failed, String text) {}

  private final String client;
  private final DemoMetrics metrics;
  private final StringBuilder text = new StringBuilder();
  private int passed;
  private int failed;

  Checks(String client, DemoMetrics metrics) {
    this.client = client;
    this.metrics = metrics;
  }

  void section(String title) {
    line("%n[%s] ── %s %s", client, title, "─".repeat(Math.max(3, 64 - title.length())));
  }

  void check(String description, boolean ok) {
    check(description, ok, null);
  }

  void check(String description, boolean ok, Object observed) {
    String suffix = observed == null ? "" : "   → " + observed;
    line("[%s]   %s %s%s", client, ok ? "✔" : "✘ FAILED:", description, suffix);
    metrics.recordCheck(ok);
    if (ok) passed++;
    else failed++;
  }

  void note(String message) {
    line("[%s]     ℹ %s", client, message);
  }

  /** Records an unexpected exception that aborted the tour. */
  void abort(Throwable t) {
    check("demo aborted by " + t.getClass().getSimpleName(), false, t.getMessage());
  }

  Report finish() {
    line("%n[%s] ══ demo complete: %d passed, %d failed ══%n", client, passed, failed);
    return new Report(passed, failed, text.toString());
  }

  private void line(String fmt, Object... args) {
    String s = String.format(fmt, args);
    System.out.println(s);
    text.append(s).append('\n');
  }
}
