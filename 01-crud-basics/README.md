# 01 · CRUD basics: native Java client vs. GemFire for gRPC

This example runs all **13 unary RPCs** of GemFire for gRPC, with every supported key and value
type, next to the same operations done with the **native Java client**. The gRPC side has two
clients, one in **Java** and one in **Go**, so you can see every RPC from both languages and
tell which costs come from GemFire's gRPC service and which from a particular gRPC library. All
three clients are small HTTP services with an identical `/crud` API, so the same k6 load test
drives them all, and Grafana compares them.

```
 k6 (make select-load-test) ──HTTP──▶ grpc-client-java :8080 ──gRPC :50051──▶ server1 ──(forward if bucket is remote)──▶ server2
                ├─HTTP──▶ grpc-client-go   :8080 ──gRPC :50051──┘
                └─HTTP──▶ native-client-java :8080 ─native :40404─▶ server1 / server2 (single-hop: straight to the bucket owner)
                                                     locator :10334 (native discovery) · :50051 (xDS)
 prometheus ◀── clients /metrics · members :7070/metrics/ · cadvisor · k6 (remote write)        grafana :3001
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=01             # cluster, all three clients (they run the narrated demo at startup), monitoring
make logs EX=01              # read the demo: pick grpc-client-java / grpc-client-go / native-client-java
make select-load-test EX=01  # pick a k6 script, run it against the clients, get a comparison table
open http://localhost:3001
make stop EX=01
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=01`.

## The three clients

The two Java clients are Java 21 HTTP services: the JDK's built-in server with a fixed pool of
64 worker threads. Only the class behind `/crud` differs:
[`GrpcCrud`](grpc-client-java/src/main/java/demo/GrpcCrud.java) on one side and
[`NativeCrud`](native-client-java/src/main/java/demo/NativeCrud.java) on the other.

The Go client ([`grpc-client-go`](grpc-client-go/)) is the same service written in Go 1.27 with
grpc-go 1.84. It has the same endpoints, JSON, headers, metric names and narrated demo (the same
15 sections and 38 checks, line for line). Go's `net/http` runs a goroutine per connection, so a
64-slot semaphore caps requests in flight to match the Java thread pools.

| | native-java | grpc-java | grpc-go |
|---|---|---|---|
| Host port | [localhost:8201](http://localhost:8201) | [localhost:8101](http://localhost:8101) | [localhost:8301](http://localhost:8301) |
| Connects to | locator `locator[10334]`, PR single-hop pool | `dns:///server1:50051` (plaintext, one HTTP/2 channel) | same as grpc-java |
| GemFire code | [`NativeCrud.java`](native-client-java/src/main/java/demo/NativeCrud.java) | [`GrpcCrud.java`](grpc-client-java/src/main/java/demo/GrpcCrud.java) | [`crud.go`](grpc-client-go/crud.go) |
| Demo | [`Demo.java`](native-client-java/src/main/java/demo/Demo.java) | [`Demo.java`](grpc-client-java/src/main/java/demo/Demo.java) | [`demo.go`](grpc-client-go/demo.go) |
| Demo region | `demo-native` | `demo-grpc` | `demo-grpc-go` |
| Load region / key prefix | `crud` / `native:` | `crud` / `grpc:` | `crud` / `grpc-go:` |

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
make select-load-test EX=01 SCRIPT=crud/steady  # all clients at once
make select-load-test EX=01 SCRIPT=crud/ramp    # each client alone, 100 → 8000 req/s
make select-load-test EX=01 SCRIPT=crud/steady CLIENT=grpc-java K6_ARGS="-e RATE=3000 -e DURATION=1m"
make results                                    # browse past runs
```

A 2-minute `crud/steady` run (1,000 req/s per client, all three at once, Apple Silicon laptop):

```
CLIENT                  REQUESTS    REQ/S  FAILED │     E2E p50         p95         p99 │      GF p50         p99 │   CPU/REQ  RESULT
                                                  │        (ms)        (ms)        (ms) │        (ms)        (ms) │      (µs)
native-java               120002    997.9      0% │       0.256       0.694       2.502 │       0.102       0.761 │       187  PASS
grpc-java                 119997    996.3      0% │       0.455       1.152       4.582 │       0.311       3.088 │       266  PASS
grpc-go                   119986    995.7      0% │       0.381       0.958       3.292 │       0.258       2.398 │       216  PASS
grpc-java vs native                               │  78% slower  66% slower  83% slower │ 205% slower 306% slower │  42% more
grpc-go vs native                                 │  49% slower  38% slower  32% slower │ 153% slower 215% slower │  16% more
grpc-go vs grpc-java                              │  16% faster  17% faster  28% faster │  17% faster  22% faster │  19% less
```

- **E2E** is what k6 saw: the HTTP request plus the GemFire call.
- **GF** is the GemFire call alone. Every service adds roughly the same 0.12–0.15 ms of HTTP
  overhead, so the end-to-end comparison understates the difference between the clients, and
  the GF comparison shows it.
- **CPU/REQ** is the client container's CPU (from cAdvisor) divided by requests.
- **"A vs B" rows** say how A did compared with B, column by column: `grpc-go vs grpc-java: 22%
  faster` means grpc-go's number was 22% lower. In the CPU column it's `less` or `more`. Over
  100% means more than twice as long: `200% slower` takes three times as long. The rows compare
  each gRPC client with native, then the two gRPC libraries with each other.
- Quote numbers from `crud/steady` or `crud/saturate`, not `crud/smoke`. At 100 req/s, CPU/REQ
  mostly measures background overhead (JVM housekeeping, GC, metrics scrapes) spread over very
  few requests.

At maximum throughput (`crud/saturate`: 16 VUs in a closed loop, each client alone), the gap
widens:

```
CLIENT                  REQUESTS    REQ/S  FAILED │     E2E p50         p95         p99 │      GF p50         p99 │   CPU/REQ  RESULT
                                                  │        (ms)        (ms)        (ms) │        (ms)        (ms) │      (µs)
native-java               655329    32685      0% │       0.368       0.916       1.893 │       0.090       0.610 │        90  PASS
grpc-java                 336869    16734      0% │       0.808       1.585       2.763 │       0.548       1.854 │       153  PASS
grpc-go                   368959  18344.8      0% │       0.679       1.794       2.555 │       0.476       1.956 │        88  PASS
grpc-java vs native                               │ 120% slower  73% slower  46% slower │ 509% slower 204% slower │  70% more
grpc-go vs native                                 │  85% slower  96% slower  35% slower │ 429% slower 221% slower │   2% less
grpc-go vs grpc-java                              │  16% faster  13% slower   8% faster │  13% faster   6% slower │  42% less
```

Go's gRPC library is a little faster than grpc-java (about 20% at a steady rate, 10% at
saturation) and much cheaper on the client: about 40% less CPU per request than grpc-java, about
the same as native, in a 25 MB container next to ~900 MB for each JVM. Even so, grpc-go's
GemFire calls still take 2.5–5 times as long as native's. **Most of the gap is on the gRPC protocol and server
side, not in grpc-java.** See finding 7.

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
`make select-load-test` run appears as a shaded band. Every latency and throughput panel reads
k6's metrics, so the **load run** dropdown filters all of them. The "At a glance" stats and the
per-operation table cover the whole time range, so they keep their numbers after a run ends. Pick
a single run to get the same numbers as the terminal table.

- **Load test (k6) row.** For each client, a solid line (end-to-end) and a dashed line (GemFire
  call). The clients' solid lines sit closer together than their dashed lines, because HTTP
  overhead is about the same in every client.
- **Per-operation latency table.** Every op's p50 for all three clients, plus "grpc-java vs
  native" and "grpc-go vs native" columns in words (red = slower, green = faster), sorted by the
  first. Single-call ops come out about 110–250% slower over gRPC. The ops where native needs two
  calls (`GetAndPut`, `GetAndRemove`, `Remove`) drop to about 10–90% slower, and the atomic gRPC
  call becomes the better deal.
- **Puts forwarded to the owning server.** Both gRPC clients send everything to `server1`, so
  about half their writes land on a server that doesn't own the bucket and get forwarded. Native
  single-hop sends each op straight to the owner. That hop is part of the gap.
  `05-xds-discovery` spreads gRPC traffic across servers through the locator.
- **CPU: GemFire members.** `server1` is the gRPC entry point (protobuf decoding plus
  forwarding) and burns about 2–3 times the CPU of `server2`.
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
   Both Java clients set it (`build.gradle.kts`); Go's `net/http` turns it on by default. This is
   about the HTTP wrapper, not GemFire, but it would have hidden every difference between the
   clients.
7. **The gRPC overhead isn't grpc-java's fault.** Go's gRPC library is a different
   implementation (its own HTTP/2 stack, no Netty, no JIT), yet it lands within 10–20% of
   grpc-java on the GemFire call and still takes 2.5–5 times as long as native. What both gRPC clients
   share is the server side: every call goes through `server1`'s gRPC service, which decodes
   protobuf, converts it to Java objects, and forwards about half of all operations to
   `server2`. Native single-hop goes straight to the owning server in GemFire's own binary
   protocol. Where the gRPC library *does* matter is client cost: grpc-go uses about 40% less
   CPU per request than grpc-java.

## Layout

```
01-crud-basics/
├── .env                      # image, extensions path, ports, client settings
├── docker-compose.yml        # locator, server1, server2, gfsh-init, 3 clients, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server in the foreground with the gRPC extension + metrics
│   └── init.gfsh             # creates regions crud, demo-grpc, demo-grpc-go, demo-native
├── grpc-client-java/         # grpc-java 1.84.3, stubs generated from gemfire-extensions/gemfire.proto at build time
│   └── src/main/java/demo/   # Main · Api (HTTP) · GrpcCrud · Demo · Metrics · Config · Checks
├── grpc-client-go/           # grpc-go 1.84, stubs generated by generate.sh at build time
│   └── *.go                  # main · api (HTTP) · crud · demo · metrics · config · checks
├── native-client-java/       # GemFire ClientCache; jars copied from the GemFire image at build time
│   └── src/main/java/demo/   # Main · Api (HTTP) · NativeCrud · Demo · Metrics · Config · Checks
└── monitoring/
    ├── prometheus.yml        # also accepts k6 remote write (native histograms)
    └── grafana/
        ├── generate-dashboard.py   # source of the dashboard: edit this, then run it (the JSON is generated)
        └── {provisioning,dashboards}/
```

`Api`, `Metrics`, `Config` and `Checks` are identical in both Java clients. Only `Main`, `Demo` and
the `*Crud` class differ. The Go client has the same pieces, one file each.

### Building outside Docker

```bash
# gRPC client: needs gemfire.proto in place first
mkdir -p grpc-client-java/src/main/proto/gemfire/v1 && cp ../gemfire-extensions/gemfire.proto grpc-client-java/src/main/proto/gemfire/v1/
(cd grpc-client-java && gradle installDist)
# Native client: point at a GemFire 10.3.2 install's lib folder
(cd native-client-java && gradle installDist -PgemfireLib=/path/to/vmware-gemfire-10.3.2/lib)
# Go client: needs protoc on your PATH (brew install protobuf); generate.sh does the rest
(cd grpc-client-go && ./generate.sh ../../gemfire-extensions/gemfire.proto && go build -o build/grpc-client-go .)
```
