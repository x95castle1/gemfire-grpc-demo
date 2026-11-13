#!/usr/bin/env python3
"""Does a GemFire call take longer when another server owns the key's bucket?

Times Get and Put on 1000 keys through each client's HTTP API, one request at a time, and groups
the timings by the key's bucket in the partitioned region. A gRPC client that talks to one server
pays for a forward whenever another server owns the bucket. The native client is single-hop, so
it shouldn't care. See 01-crud-basics/README.md, finding 7.

    python3 load-testing/probes/bucket-latency.py          # example 01, 8 rounds, about 20 s
    python3 load-testing/probes/bucket-latency.py 01 20    # example number, rounds

The example must be running (make start EX=01) and its clients must serve the /crud API, which
today means 01. Python 3 standard library only.

This isn't a k6 script, because it needs each request's timing by key. It isn't in the
`make select-load-test` menu and pushes no k6 metrics, so Grafana's load-run panels don't show
it. Like the crud scripts, it preloads keys 0..999 for each client into the `crud` region.
"""
import http.client
import pathlib
import random
import statistics
import sys
import time

# client -> (its host port in the example's .env, its key prefix: KEY_PREFIX in the client's Config)
CLIENTS = {
    "native-java": ("NATIVE_CLIENT_PORT", "native:"),
    "grpc-java": ("GRPC_CLIENT_PORT", "grpc:"),
    "grpc-go": ("GRPC_GO_CLIENT_PORT", "grpc-go:"),
}
OPS = {"Get": ("GET", "get"), "Put": ("POST", "put")}
KEYS = 1000
BUCKETS = 113  # a partitioned region's total-num-buckets unless it's set (gemfire/init.gfsh doesn't)


def bucket(key):
    """The bucket GemFire puts a String key in: abs(key.hashCode() % total-num-buckets)."""
    h = 0
    for ch in key:
        h = (31 * h + ord(ch)) & 0xFFFFFFFF
    if h >= 1 << 31:
        h -= 1 << 32
    return abs(h) % BUCKETS


def call(conn, method, path):
    """One request -> the microseconds the client spent inside the GemFire call."""
    conn.request(method, path)
    res = conn.getresponse()
    body = res.read()
    if res.status != 200:
        sys.exit(f"{method} {path}: HTTP {res.status} {body[:200].decode(errors='replace')}")
    return int(res.getheader("X-GemFire-Micros", 0))


def split(medians):
    """Splits buckets at the widest gap between their sorted medians -> (slow buckets, gap as a fraction)."""
    ranked = sorted(medians.items(), key=lambda kv: kv[1])
    # Skip the outer 10% on each side so one outlier bucket can't define the split.
    edge = len(ranked) // 10
    gap, i = max((ranked[i + 1][1] - ranked[i][1], i) for i in range(edge, len(ranked) - edge - 1))
    return {b for b, _ in ranked[i + 1:]}, gap / ranked[i][1]


def p99(values):
    return sorted(values)[min(len(values) - 1, int(0.99 * len(values)))]


def main():
    ex = sys.argv[1] if len(sys.argv) > 1 else "01"
    rounds = int(sys.argv[2]) if len(sys.argv) > 2 else 8
    envs = sorted(pathlib.Path(__file__).resolve().parents[2].glob(f"{ex}-*/.env"))
    if not envs:
        sys.exit(f"no example matches {ex}-*")
    env = dict(line.split("=", 1) for line in envs[0].read_text().splitlines()
               if "=" in line and not line.startswith("#"))
    conns = {c: http.client.HTTPConnection("localhost", int(env[var]), timeout=300)
             for c, (var, _) in CLIENTS.items() if var in env}
    # The split comes from a gRPC client: it's the one that forwards.
    reference = next((c for c in ("grpc-go", "grpc-java") if c in conns), None)
    if reference is None:
        sys.exit(f"{envs[0].parent.name} has no gRPC client to take the bucket split from")

    print(f"{envs[0].parent.name}: preloading {KEYS} keys per client and warming up...", flush=True)
    try:
        for conn in conns.values():
            call(conn, "GET", "/health")
            call(conn, "POST", f"/crud/preload?count={KEYS}&size=1024")
    except OSError as e:
        sys.exit(f"can't reach the clients ({e}). Is the example running? make start EX={ex}")
    for conn in conns.values():
        for k in range(KEYS):
            for method, segment in OPS.values():
                call(conn, method, f"/crud/{segment}?key={k}&size=1024")

    # samples[client][op][bucket] -> [micros]
    samples = {c: {op: {} for op in OPS} for c in conns}
    order, keys = list(conns), list(range(KEYS))
    start = time.time()
    for _ in range(rounds):
        random.shuffle(order)
        for c in order:
            for op, (method, segment) in OPS.items():
                random.shuffle(keys)
                for k in keys:
                    micros = call(conns[c], method, f"/crud/{segment}?key={k}&size=1024")
                    samples[c][op].setdefault(bucket(f"{CLIENTS[c][1]}{k}"), []).append(micros)
    total = rounds * len(conns) * len(OPS) * KEYS
    print(f"{total} timed requests in {time.time() - start:.0f}s, one at a time\n")

    medians = {c: {op: {b: statistics.median(v) for b, v in by_bucket.items()} for op, by_bucket in ops.items()}
               for c, ops in samples.items()}
    slow, _ = split(medians[reference]["Get"])
    print(f"Buckets split at the widest gap in {reference}'s Get medians: "
          f"{len(medians[reference]['Get']) - len(slow)} fast, {len(slow)} slow\n")
    print(f"{'client':<12} {'op':<4} │ {'p50 (µs)':>9} {'p99':>7} │ {'fast buckets':>13} {'slow buckets':>13} "
          f"{'slow − fast':>12} │ own split")
    for c in conns:
        for op in OPS:
            by_bucket = samples[c][op]
            everything = [m for v in by_bucket.values() for m in v]
            fast50 = statistics.median(m for b, v in by_bucket.items() if b not in slow for m in v)
            slow50 = statistics.median(m for b, v in by_bucket.items() if b in slow for m in v)
            own, gap = split(medians[c][op])
            match = sum((b in own) == (b in slow) for b in by_bucket) / len(by_bucket)
            print(f"{c:<12} {op:<4} │ {statistics.median(everything):9.0f} {p99(everything):7.0f} │ {fast50:13.0f} "
                  f"{slow50:13.0f} {slow50 - fast50:+12.0f} │ {match * 100:3.0f}% match, gap {gap * 100:.0f}%")
    print(f"""
Microseconds inside the GemFire call (X-GemFire-Micros). fast/slow buckets and slow − fast are p50.
fast / slow   the two groups of buckets in {reference}'s Get timings. For a gRPC client that talks to
              one server, fast = buckets that server owns, slow = buckets it forwards.
own split     the same split done on that row's own timings. Near 100% match with a wide gap: the
              same buckets are slow for this client. Near 50% with a gap of a few percent: its
              latency doesn't depend on the bucket.""")


if __name__ == "__main__":
    main()
