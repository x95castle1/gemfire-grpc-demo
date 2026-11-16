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

  /**
   * How {@code a} compares with {@code b} for a time or duration, in words: "22% faster", "60%
   * slower", or from 2× up "3.1× faster" / "12× slower". The same wording as the load-test tables.
   */
  static String compare(double a, double b) {
    if (!(a > 0) || !(b > 0)) return "-";
    double r = a / b;
    if (r >= 2 || r <= 0.5) {
      double x = r >= 2 ? r : 1 / r;
      return String.format(x < 10 ? "%.1f× %s" : "%.0f× %s", x, r >= 2 ? "slower" : "faster");
    }
    long pct = Math.round(Math.abs(r - 1) * 100);
    return pct == 0 ? "same" : pct + "% " + (r < 1 ? "faster" : "slower");
  }

  private void line(String fmt, Object... args) {
    String s = String.format(fmt, args);
    System.out.println(s);
    text.append(s).append('\n');
  }
}
