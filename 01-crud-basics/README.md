# 01 · CRUD basics: native Java client vs. GemFire for gRPC

This example runs all **13 unary RPCs** of GemFire for gRPC, with every supported key and value
type, next to the same operations done with the **native Java client**. Both clients are small
HTTP services with an identical `/crud` API, so the same k6 load test drives both, and Grafana
compares them.

```
 k6 (make select-load-test) ──HTTP──▶ grpc-client-java :8080 ──gRPC :50051──▶ server1 ──(forward if bucket is remote)──▶ server2
                └─HTTP──▶ native-client-java :8080 ─native :40404─▶ server1 / server2 (single-hop: straight to the bucket owner)
                                                     locator :10334 (native discovery) · :50051 (xDS)
 prometheus ◀── clients /metrics · members :7070/metrics/ · cadvisor · k6 (remote write)        grafana :3001
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=01             # cluster, both clients (they run the narrated demo at startup), monitoring
make logs EX=01              # read the demo: pick grpc-client-java / native-client-java
make select-load-test EX=01  # pick a k6 script, run it against both clients, get a comparison table
open http://localhost:3001
make stop EX=01
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=01`.

## The two clients

Each client is a Java 21 HTTP service: the JDK's built-in server with the same fixed pool of 64
worker threads on both sides. Only the class behind `/crud` differs:
[`GrpcCrud`](grpc-client-java/src/main/java/demo/GrpcCrud.java) on one side and
[`NativeCrud`](native-client-java/src/main/java/demo/NativeCrud.java) on the other.

| | grpc-java | native-java |
|---|---|---|
| Host port | [localhost:8101](http://localhost:8101) | [localhost:8201](http://localhost:8201) |
| Connects to | `dns:///server1:50051` (plaintext, one HTTP/2 channel) | locator `locator[10334]`, PR single-hop pool |
| Demo region | `demo-grpc` | `demo-native` |
| Load region / key prefix | `crud` / `grpc:` | `crud` / `native:` |

### HTTP API

| Endpoint | What it does |
|---|---|
| `GET /` | lists the endpoints |
| `GET /health` | 200 once the GemFire connection is up |
| `POST /demo` | runs the narrated demo; 200 means every assertion passed (also runs at startup) |
| `GET /demo` | result of the last demo run, e.g. `{"ran":true,"passed":38,"failed":0}` |
| `POST /crud/preload?count=10000&size=1024` | bulk-writes keys `0..count-1` (PutStream / putAll) |
| `GET /crud/{get,contains-key,entry-count}?key=42` | read operations |
| `POST /crud/{put,get-and-put,remove,remove-if,get-and-remove,put-if-absent,put-if-absent-and-get,replace,replace-if,get-and-replace}?key=42&size=1024` | write operations |
| `GET /metrics` | Prometheus metrics |

```bash
$ curl -s -X POST 'localhost:8101/crud/get-and-put?key=7&size=10'
{"op":"GetAndPut","key":"7","previousBytes":1024,"micros":2039}
```

Every `/crud` response carries the header **`X-GemFire-Micros`**: the time spent inside the
GemFire call alone. k6 records it next to its own end-to-end timing. The same number is the
`demo_op_latency_seconds` histogram on the dashboards.

Settings in [`.env`](.env): `DEMO_ON_START`, `PAYLOAD_BYTES` (default value size; requests can
override it with `?size=`), `HTTP_THREADS`, and the host ports.

## Load testing

The k6 scripts live at the repo root in [`../load-testing`](../load-testing/README.md), because
the same scripts run against every example. To list them, run `make select-load-test EX=01` and browse with
fzf, or run one directly:

```bash
make select-load-test EX=01 SCRIPT=crud/steady  # both clients at once
make select-load-test EX=01 SCRIPT=crud/ramp    # each client alone, 100 → 8000 req/s
make select-load-test EX=01 SCRIPT=crud/steady CLIENT=grpc-java K6_ARGS="-e RATE=3000 -e DURATION=1m"
make results                                    # browse past runs
```

A 2-minute `crud/steady` run (1,000 req/s per client, both at once, Apple Silicon laptop):

```
CLIENT         REQUESTS    REQ/S  FAILED │  E2E p50      p95      p99 │   GF p50      p99 │  CPU/REQ  RESULT
                                         │     (ms)     (ms)     (ms) │     (ms)     (ms) │    (µs)
grpc-java        120000    998.3      0% │    0.451    0.742    1.437 │    0.303    0.968 │      246  PASS
native-java      120002    998.8      0% │    0.249    0.465    0.849 │    0.094    0.423 │      181  PASS
gRPC/native                              │     1.8x     1.6x     1.7x │     3.2x     2.3x │     1.4x
```

- **E2E** is what k6 saw: the HTTP request plus the GemFire call.
- **GF** is the GemFire call alone. Both services carry the same ~0.15 ms of HTTP overhead, so the
  end-to-end ratio (1.8×) understates the difference between the clients, and the GF ratio (3.2×)
  shows it.
- **CPU/REQ** is the client container's CPU (from cAdvisor) divided by requests.

At maximum throughput (`crud/saturate`: 16 VUs in a closed loop, each client alone), the gap
widens:

```
CLIENT         REQUESTS    REQ/S  FAILED │  E2E p50      p95      p99 │   GF p50      p99 │  CPU/REQ  RESULT
grpc-java        319893  15840.6      0% │    0.824    1.832    3.474 │    0.562    2.412 │      163  PASS
native-java      623377  30926.5      0% │    0.375    1.033    2.207 │    0.091    0.670 │       78  PASS
gRPC/native                              │     2.2x     1.8x     1.6x │     6.1x     3.6x │     2.1x
```

## RPC ↔ native Java API

The native column is the cheapest native code that returns **the same information** as the RPC.
`NativeCrud` uses exactly these calls.

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
Over HTTP, a failed GemFire call returns `502` with the code in `X-Error-Code`. All of them are
covered in `10-error-handling`.

## What to look for in Grafana

Open **"01 · CRUD basics: native vs gRPC"** at [localhost:3001](http://localhost:3001). Each
`make select-load-test` run appears as a shaded band; use the **load run** dropdown to focus on one run.

- **Load test (k6) row.** For each client, a solid line (end-to-end) and a dashed line (GemFire
  call). The two clients' solid lines sit closer together than their dashed lines, because HTTP
  overhead is the same on both sides.
- **Per-operation latency table.** Sorted by the gRPC ÷ native ratio. Single-call ops sit around
  3×. The ops where native needs two calls (`GetAndPut`, `GetAndRemove`, `Remove`) drop to about
  1.4–1.8×, and the atomic gRPC call becomes the better deal.
- **Puts forwarded to the owning server.** The gRPC client sends everything to `server1`, so
  about half its writes land on a server that doesn't own the bucket and get forwarded. Native
  single-hop sends each op straight to the owner. That hop is part of the gap.
  `05-xds-discovery` spreads gRPC traffic across servers through the locator.
- **CPU: GemFire members.** `server1` is the gRPC entry point (protobuf decoding plus
  forwarding) and burns about 2–3× the CPU of `server2`.
- **Cache-server requests/s.** Only native traffic shows up here. gRPC requests bypass the
  cache-server (port 40404) statistics entirely.

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
5. **Mind the 4 MiB message limit when batching.** One `PutStream` message must stay under
   `gemfire.grpc.max-message-size` (4 MiB by default), and so must one value. The clients flush
   preload batches at 500 entries or about 1 MiB. Try `crud/large-payload` with
   `K6_ARGS="-e SIZE=5000000"`, or `curl -X POST 'localhost:8101/crud/put?key=big&size=5000000'`.
   A single 5 MB value fails over gRPC with `RESOURCE_EXHAUSTED: gRPC message exceeds maximum
   size 4194304` and succeeds natively.

6. **Turn on `TCP_NODELAY` in a JDK `HttpServer`.** Without
   `-Dsun.net.httpserver.nodelay=true`, back-to-back requests on a keep-alive connection stall
   about 40 ms on Nagle plus delayed ACK. `crud/saturate` managed 370 req/s instead of 15,000+.
   Both clients set it (`build.gradle.kts`). This is about the HTTP wrapper, not GemFire, but it
   would have hidden every difference between the two clients.

## Layout

```
01-crud-basics/
├── .env                      # image, extensions path, ports, client settings
├── docker-compose.yml        # locator, server1, server2, gfsh-init, 2 clients, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server in the foreground with the gRPC extension + metrics
│   └── init.gfsh             # creates regions crud, demo-grpc, demo-native
├── grpc-client-java/         # grpc-java 1.84.3, stubs generated from gemfire-extensions/gemfire.proto at build time
│   └── src/main/java/demo/   # Main · Api (HTTP) · GrpcCrud · Demo · Metrics · Config · Checks
├── native-client-java/       # GemFire ClientCache; jars copied from the GemFire image at build time
│   └── src/main/java/demo/   # Main · Api (HTTP) · NativeCrud · Demo · Metrics · Config · Checks
└── monitoring/
    ├── prometheus.yml        # also accepts k6 remote write (native histograms)
    └── grafana/{provisioning,dashboards}/
```

`Api`, `Metrics`, `Config` and `Checks` are identical in both clients. Only `Main`, `Demo` and the
`*Crud` class differ.

### Building outside Docker

```bash
# gRPC client: needs gemfire.proto in place first
mkdir -p grpc-client-java/src/main/proto/gemfire/v1 && cp ../gemfire-extensions/gemfire.proto grpc-client-java/src/main/proto/gemfire/v1/
(cd grpc-client-java && gradle installDist)
# Native client: point at a GemFire 10.3.2 install's lib folder
(cd native-client-java && gradle installDist -PgemfireLib=/path/to/vmware-gemfire-10.3.2/lib)
```
