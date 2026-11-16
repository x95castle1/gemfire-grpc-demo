package demo;

import java.util.function.Supplier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.stereotype.Component;

/**
 * Runs the narrated demo (the {@code demo} bean from each client's {@code *ClientApplication}) at
 * startup and on POST /demo, and keeps the last report for GET /demo.
 *
 * <p>Spring Boot calls runners after Tomcat is listening but before it marks the app ready, so
 * /health stays 503 (and Docker keeps the client "starting") until the startup demo is done.
 */
@Component
class DemoRunner implements ApplicationRunner {

  private static final Logger log = LoggerFactory.getLogger(DemoRunner.class);

  private final Settings cfg;
  private final Supplier<Checks.Report> demo;
  private volatile Checks.Report last;

  DemoRunner(Settings cfg, Supplier<Checks.Report> demo) {
    this.cfg = cfg;
    this.demo = demo;
  }

  @Override
  public void run(ApplicationArguments args) {
    log.info("{} ({}) serving region {}", cfg.client(), cfg.example(), cfg.region());
    if (cfg.demoOnStart()) runDemo();
  }

  /** Runs the demo now (one run at a time) and remembers the result. */
  synchronized Checks.Report runDemo() {
    last = demo.get();
    return last;
  }

  /** The last run's report, or null if the demo hasn't run yet. */
  Checks.Report last() {
    return last;
  }
}
