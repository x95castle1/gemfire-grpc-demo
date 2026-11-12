package demo;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.atomic.LongAdder;
import java.util.concurrent.locks.LockSupport;

/**
 * Rate-limited, multi-threaded load loop. Each worker picks an op from the weighted mix and a
 * random key, runs it, and records latency (or the error code) in {@link Metrics}.
 *
 * <p>The workload engine is identical in the gRPC and native clients; only {@link Ops} differs.
 */
final class Bench {

  /** One client's implementation of the unary operations. */
  interface Ops {
    /** Writes every key once so reads hit existing entries. */
    void preload() throws Exception;

    void execute(String op, int keyIndex) throws Exception;

    /** Maps a failure to a short label: a gRPC status code or an exception class name. */
    String errorCode(Throwable t);
  }

  private final Config cfg;
  private final Metrics metrics;
  private final Ops ops;
  private final String[] opTable;
  private final LongAdder done = new LongAdder();
  private final LongAdder failed = new LongAdder();

  Bench(Config cfg, Metrics metrics, Ops ops) {
    this.cfg = cfg;
    this.metrics = metrics;
    this.ops = ops;
    this.opTable = buildOpTable(cfg.opMix());
  }

  void run() throws Exception {
    log("preloading %,d keys into /%s ...", cfg.keySpace(), cfg.region());
    ops.preload();

    Duration d = cfg.benchDuration();
    long deadline = d.isZero() ? Long.MAX_VALUE : System.nanoTime() + d.toNanos();
    log(
        "running: %d workers, target %s ops/s, payload %,d B, duration %s, mix %s",
        cfg.concurrency(),
        cfg.targetRate() > 0 ? String.format("%,d", cfg.targetRate()) : "unlimited",
        cfg.payloadBytes(),
        d.isZero() ? "until stopped" : d,
        cfg.opMix());

    CountDownLatch finished = new CountDownLatch(cfg.concurrency());
    for (int i = 0; i < cfg.concurrency(); i++) {
      Thread t = new Thread(() -> worker(deadline, finished), "bench-" + i);
      t.setDaemon(true);
      t.start();
    }

    long lastDone = 0;
    long lastTime = System.nanoTime();
    while (!finished.await(10, java.util.concurrent.TimeUnit.SECONDS)) {
      long now = System.nanoTime();
      long total = done.sum();
      log(
          "%,.0f ops/s  (total %,d ok, %,d failed)",
          (total - lastDone) / ((now - lastTime) / 1e9),
          total,
          failed.sum());
      lastDone = total;
      lastTime = now;
    }
    log("finished: %,d ok, %,d failed", done.sum(), failed.sum());
  }

  private void worker(long deadline, CountDownLatch finished) {
    ThreadLocalRandom rnd = ThreadLocalRandom.current();
    long interval = cfg.targetRate() > 0 ? 1_000_000_000L * cfg.concurrency() / cfg.targetRate() : 0;
    long next = System.nanoTime();
    try {
      while (System.nanoTime() < deadline) {
        if (interval > 0) {
          next += interval;
          long wait = next - System.nanoTime();
          if (wait > 0) {
            LockSupport.parkNanos(wait);
          } else if (wait < -1_000_000_000L) {
            next = System.nanoTime(); // fell >1s behind: don't burst to catch up
          }
        }
        String op = opTable[rnd.nextInt(opTable.length)];
        int key = rnd.nextInt(cfg.keySpace());
        long start = System.nanoTime();
        try {
          ops.execute(op, key);
          metrics.recordLatency(op, System.nanoTime() - start);
          done.increment();
        } catch (Exception e) {
          metrics.recordError(op, ops.errorCode(e));
          failed.increment();
        }
      }
    } finally {
      finished.countDown();
    }
  }

  /** Expands {Get=40, Put=15, ...} into a lookup table so picking an op is one array index. */
  private static String[] buildOpTable(Map<String, Integer> mix) {
    List<String> table = new java.util.ArrayList<>();
    mix.forEach((op, weight) -> {
      for (int i = 0; i < weight; i++) table.add(op);
    });
    return table.toArray(String[]::new);
  }

  private void log(String fmt, Object... args) {
    System.out.printf("[%s bench] %s%n", cfg.client(), String.format(fmt, args));
  }
}
