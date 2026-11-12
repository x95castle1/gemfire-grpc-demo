# 01 · CRUD basics: native Java client vs. GemFire for gRPC

This example runs all **13 unary RPCs** of GemFire for gRPC, with every supported key and value
type, next to the same operations done with the **native Java client**. Both clients then run an
identical load, and Grafana compares them.

```
                 ┌──────────── GemFire 10.3.2 + gRPC extension 1.0.0 ────────────┐
 grpc-client-java ──── gRPC :50051 ─────▶ server1 ──(forward if bucket is remote)─▶ server2
 native-client-java ─ native :40404 ─▶ server1 / server2 (single-hop, straight to the bucket owner)
                 │                   locator :10334 (native discovery) · :50051 (xDS)     │
                 └────────────────────────────────────────────────────────────────────────┘
 prometheus ◀─ clients :8080 · members :7070/metrics/ · cadvisor        grafana :3001
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../vendor`](../vendor/README.md).

```bash
docker compose up -d --build                                    # cluster, clients, monitoring
docker compose logs -f grpc-client-java native-client-java      # narrated demo, then bench progress
open http://localhost:3001                                      # Grafana (anonymous admin)
docker compose down -v                                          # stop and clean up
```

Smoke test (demo assertions only; exit code 0 means pass): `make verify EX=01` from the repo root. Other targets: `make start|stop|status|logs`.

## What each client does

1. **Demo** (`MODE=demo` or `all`): a narrated walk through every operation, where each step
   asserts the documented behavior (`✔`/`✘`). gRPC uses region `demo-grpc` and native uses
   `demo-native`, so counts are exact.
2. **Bench** (`MODE=bench` or `all`): `CONCURRENCY` threads at a fixed `TARGET_RATE`, running a
   weighted mix of all 13 operations against region `crud`. Each client uses its own key prefix.
   Latency is measured around each call, inside the client.

Knobs live in [`.env`](.env):

| Variable | Default | Meaning |
|---|---|---|
| `MODE` | `all` | `demo`, `bench`, or `all` (demo, then bench) |
| `BENCH_DURATION` | `15m` | `30s`, `10m`, `1h`… or `0` to run until stopped |
| `CONCURRENCY` | `4` | worker threads per client |
| `TARGET_RATE` | `1000` | ops/s per client (`0` = unlimited). A fixed rate keeps the comparison fair. |
| `KEY_SPACE` | `10000` | distinct keys per client (preloaded before the bench) |
| `PAYLOAD_BYTES` | `1024` | `byte[]` value size for writes |
| `OP_MIX` | see `Config.java` | e.g. `Get=80,Put=20` to override the 13-op mix |

## RPC ↔ native Java API

The native column is the cheapest native code that returns **the same information** as the RPC.
The bench uses exactly these calls.

| gRPC RPC | Returns | Native Java (client `PROXY` region) | Notes |
|---|---|---|---|
| `Put` | – | `put(k, v)` | |
| `Get` | value, or unset | `get(k)` | Missing key: gRPC leaves `value` unset; native returns `null` |
| `GetAndPut` | previous value | `get(k)` + `put(k, v)` | ⚠ Client `put()` returns `null`, not the old value. Natively that's **2 round trips and not atomic** |
| `Remove` | `removed` flag | `containsKeyOnServer(k)` + `remove(k)` | ⚠ Client `remove()` returns `null` either way, and `destroy()` doesn't throw for a missing key. **2 round trips** |
| `RemoveIf` | `removed` | `remove(k, expected)` | |
| `GetAndRemove` | removed value | `get(k)` + `remove(k)` | ⚠ **2 round trips and not atomic** |
| `PutIfAbsent` | `inserted` | `putIfAbsent(k, v) == null` | |
| `PutIfAbsentAndGet` | existing or new value | `putIfAbsent(k, v)`, which returns the existing value or `null` (meaning yours went in) | |
| `Replace` | `replaced` | `replace(k, v) != null` | |
| `ReplaceIf` | `replaced` | `replace(k, expected, v)` | |
| `GetAndReplace` | previous value | `replace(k, v)` | |
| `ContainsKey` | `contains` | `containsKeyOnServer(k)` | ⚠ `containsKey()` only checks the local cache (always `false` on PROXY) |
| `EntryCount` | `size` | `sizeOnServer()` | ⚠ `size()` is local (always `0` on PROXY) |

**Key types.** gRPC accepts `string`, `int32` and `int64`, which map to Java `String`, `Integer`
and `Long`. `"42"`, `42` and `42L` are three different keys in both APIs. Native also accepts any
serializable or PDX key; gRPC does not.

**Value types.** `string`, `bytes`, `int32`, `int64`, `bool`, `double`, `float` round-trip
exactly. They map to `String`, `byte[]`, `Integer`, `Long`, `Boolean`, `Double`, `Float`.
`google.protobuf.Any` is covered in `04-structured-values`.

**Errors.** gRPC returns status codes; for example, a missing region gives `FAILED_PRECONDITION` and
an unset key gives `INVALID_ARGUMENT`. The native client throws exceptions:
`ServerOperationException ← RegionDestroyedException`, or a client-side `NullPointerException`.
All of them are covered in `10-error-handling`.

## What to look for in Grafana

Open the **"01 · CRUD basics: native vs gRPC"** dashboard. The numbers below are from one run on
an Apple Silicon laptop under Docker Desktop, at 1,000 ops/s per client with 4 threads and 1 KiB
values. Treat the *shape* as the takeaway; your absolute numbers will differ.

| Signal | native-java | grpc-java |
|---|---|---|
| p50 latency, all ops | ~135 µs | ~370 µs |
| p99 latency, all ops | ~0.7 ms | ~1.3–1.8 ms |
| `Get` p50 | ~115 µs | ~360 µs (≈3.1×) |
| Client CPU per operation | ~100 µs | ~180 µs |
| `GetAndPut` / `GetAndRemove` / `Remove`, gRPC ÷ native | | ≈1.4–1.8×. The gap narrows because native needs 2 round trips |

- **Per-operation latency table.** Sorted by the gRPC ÷ native ratio. Single-call ops sit around
  2.3–3×. The ops where native needs two calls drop to about 1.4–1.8×, and the atomic gRPC call
  becomes the better deal.
- **Puts forwarded to the owning server.** The gRPC client sends everything to `server1`, so
  about half its writes (~200/s here) land on a server that doesn't own the bucket and get
  forwarded. Native single-hop sends each op straight to the owner. That forwarding hop is part
  of the latency gap. `05-xds-discovery` spreads gRPC traffic across servers through the locator.
- **CPU: GemFire members.** `server1` (the gRPC entry point: protobuf decoding plus forwarding)
  burns about 2–3× the CPU of `server2`.
- **Cache-server requests/s.** Only native traffic shows up here. gRPC requests bypass the
  cache-server (port 40404) statistics entirely.
- **Client connections per server.** Native uses a pool of sockets per server. gRPC uses one
  multiplexed HTTP/2 connection.

## Findings and gotchas

1. **The gRPC extension exposes almost no server-side metrics.** It registers a single GemFire
   statistic, `GrpcServerStats.grpc.server.connections`. There are no per-RPC counts, latencies or
   errors, so measure in the client, as this example does. Region-level stats
   (`PartitionedRegionStats`) still count gRPC traffic.
2. **Don't TCP-probe the gRPC port.** Each non-gRPC connection to `:50051`, such as a bash
   `/dev/tcp` check or even a bare `curl --http2-prior-knowledge`, *decrements*
   `grpc.server.connections` without incrementing it, and the gauge goes negative. We saw −21
   after a few minutes of 5-second healthchecks. Real gRPC clients count correctly. The
   healthchecks here grep the member log for `gRPC Cache Server started` instead.
3. **The Prometheus endpoint path needs a trailing slash.** `/metrics` returns `301` to
   `/metrics/`. GemFire 10.3 serves it from the HTTP service (`--http-service-port`, plus
   `--start-rest-api` on servers) once `-Dgemfire.prometheus.metrics.emission=All` is set. Stats
   are sampled every `gemfire.prometheus.metrics.interval` (15 s here).
4. **Client-region `put()` and `remove()` don't return old values.** That's native-client
   behavior, not a gRPC issue, but it makes `GetAndPut`, `GetAndRemove` and `Remove` (with its
   flag) genuinely more capable over gRPC: one atomic round trip.

## Layout

```
01-crud-basics/
├── .env                      # image, vendor path, ports, workload knobs
├── docker-compose.yml        # locator, server1, server2, gfsh-init, 2 clients, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server in the foreground with the gRPC extension + metrics
│   └── init.gfsh             # creates regions crud, demo-grpc, demo-native
├── grpc-client-java/         # grpc-java 1.84.3, stubs generated from vendor/gemfire.proto at build time
│   └── src/main/java/demo/   # Main · Demo (tour) · GrpcOps (bench ops) · Bench · Metrics · Config · Checks
├── native-client-java/       # GemFire ClientCache; jars copied from the GemFire image at build time
│   └── src/main/java/demo/   # Main · Demo (tour) · NativeOps (bench ops) · Bench · Metrics · Config · Checks
└── monitoring/
    ├── prometheus.yml
    └── grafana/{provisioning,dashboards}/
```

`Bench`, `Metrics`, `Config` and `Checks` are the same in both clients. Only `Demo` and the
`*Ops` class differ.

### Building outside Docker

```bash
# gRPC client: needs gemfire.proto in place first
mkdir -p grpc-client-java/src/main/proto/gemfire/v1 && cp ../vendor/gemfire.proto grpc-client-java/src/main/proto/gemfire/v1/
(cd grpc-client-java && gradle installDist)
# Native client: point at a GemFire 10.3.2 install's lib folder
(cd native-client-java && gradle installDist -PgemfireLib=/path/to/vmware-gemfire-10.3.2/lib)
```
