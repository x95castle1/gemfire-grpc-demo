# 02 · Bulk streaming: native putAll/getAll/removeAll vs. GemFire for gRPC streams

This example runs the four **bulk streaming RPCs** of GemFire for gRPC next to the native Java
client's bulk Region API:

| RPC | Shape | Native counterpart |
|---|---|---|
| `PutStream` | client stream: many request messages, one response | `putAll` |
| `GetStream` | bidirectional: key chunks in, found entries out, at the same time | `getAll` |
| `RemoveStream` | client stream | `removeAll` |
| `KeySetStream` | server stream: one request, the key set back in pages | `keySetOnServer` |

It also does the same work **without** bulk calls (one unary `Put`/`Get`/`Remove`, or
`region.put/get/remove`, per entry), so you can see what streaming buys you.

Both clients are small HTTP services with an identical `/bulk` API, so the same k6 load tests
drive them and Grafana compares them.

```
 k6 (make select-load-test) ──HTTP──▶ grpc-client-java   :8080 ──one stream per call :50051──▶ server1 ──(peer messages)──▶ server2
                └──────────HTTP──▶ native-client-java :8080 ──putAll/getAll/... :40404──▶ server1 / server2 (single-hop: each server gets its own keys)
                                                        locator :10334
 prometheus ◀── clients /metrics · members :7070/metrics/ · cadvisor · k6 (remote write)        grafana :3002
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=02             # cluster, both clients (they run the narrated demo at startup), monitoring
make logs EX=02              # read the demo: pick grpc-client-java or native-client-java
make select-load-test EX=02  # pick a bulk k6 script, run it, get a comparison table
open http://localhost:3002
make stop EX=02
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=02`.

## The two clients

The same plain Spring Boot apps as 01 (Spring MVC on Tomcat, 64 worker threads, no Spring Data
for GemFire), with the same `@RestController` in both. Only the bean behind `/bulk` differs.

| | native-java | grpc-java |
|---|---|---|
| Host port | [localhost:8202](http://localhost:8202) | [localhost:8102](http://localhost:8102) |
| Connects to | locator `locator[10334]`, PR single-hop pool | `dns:///server1:50051` (plaintext, one HTTP/2 channel) |
| GemFire code | [`NativeBulk.java`](native-client-java/src/main/java/demo/NativeBulk.java) | [`GrpcBulk.java`](grpc-client-java/src/main/java/demo/GrpcBulk.java), [`StreamCall.java`](grpc-client-java/src/main/java/demo/StreamCall.java) |
| Demo | [`Demo.java`](native-client-java/src/main/java/demo/Demo.java) | [`Demo.java`](grpc-client-java/src/main/java/demo/Demo.java) |
| Load region / key prefix | `bulk-native` / `native:` | `bulk-grpc` / `grpc:` |
| Demo region | `demo-native` | `demo-grpc` |

Each client has **its own region**, so key sets and entry counts stay exact while both run.

**One chunk size for both.** `batch` entries go in each gRPC stream message, and the native
client sends `batch` entries per putAll/getAll/removeAll call. The difference is what happens
between chunks: grpc-java sends its chunks back to back on one stream without waiting, while
each native call is a blocking round trip. Both clients also cap a chunk at about 1 MiB of
values, which keeps every gRPC message under the server's 4 MiB `max-message-size`.

**Flow control.** A gRPC `StreamObserver.onNext` never blocks: it queues the message even when
the server is slower than the client, so a big stream can pile up in client memory.
[`StreamCall`](grpc-client-java/src/main/java/demo/StreamCall.java) waits for `isReady()` before
each message, and stops sending as soon as the server fails the call.

### HTTP API

| Endpoint | What it does |
|---|---|
| `GET /` | lists the endpoints |
| `GET /health` | 200 once the GemFire connection is up |
| `POST /demo` | runs the narrated demo; 200 means every assertion passed (also runs at startup) |
| `GET /demo` | result of the last demo run, e.g. `{"ran":true,"passed":22,"failed":0}` |
| `GET /metrics` | Prometheus metrics |
| `POST /bulk/put?start=0&count=10000&batch=500&size=1024` | writes keys `start..start+count-1` |
| `POST /bulk/get?start=0&count=10000&batch=500` | reads them back; `entries` = how many were found |
| `POST /bulk/remove?start=0&count=10000&batch=500` | removes them |
| `GET /bulk/keyset` | every key in the client's region; `messages` = pages received |
| `POST /bulk/reset` | removes everything in the client's region |

`put`, `get` and `remove` also take **`mode=stream`** (the default) or **`mode=unary`**, which
does the same work one call per entry, and **`inflight=1..256`**: how many unary calls are
outstanding at once (1 = wait for each response before sending the next). grpc-java runs those
as async calls on its one channel; native-java uses that many threads. `keyset` has no unary
mode: no single-key RPC lists keys.

```bash
curl -s -X POST 'localhost:8102/bulk/put?count=5000'
{"op":"PutStream","mode":"stream","start":0,"requested":5000,"batch":500,"entries":5000,"messages":10,"micros":70412}
curl -s -X POST 'localhost:8102/bulk/put?count=5000&mode=unary&inflight=16'
{"op":"Put","mode":"unary","inflight":16,"start":0,"requested":5000,"entries":5000,"messages":5000,"micros":353127}
```

Every `/bulk` response carries `X-GemFire-Micros` (time spent in the GemFire calls alone) and
`X-Entries`. Failures return 502 with `X-Error-Code` (a gRPC status or an exception class).

## The narrated demo

Both clients walk through the same 11 sections at startup and assert every result (grpc-java 22
checks, native-java 17; where a stream behavior has no native counterpart, native says so).
Highlights:

| # | What it shows | grpc-java | native-java |
|---|---|---|---|
| 1 | 5,000 entries in chunks of 500 | one PutStream, 11 messages | 10 putAll calls |
| 2 | one 2,500-entry chunk | the server splits it into putAll operations of at most `bulk-batch-size` (1000) | the client splits it by bucket (single-hop) |
| 4 | ask for 10 keys, 4 absent | **6 entries back**: misses are left out | 10 entries back, 4 of them null |
| 5 | bidirectional | chunk 1's entries arrive (~11 ms) before chunk 2 is sent | no counterpart |
| 6 | key set of 7,503 keys | 8 pages of at most 1,000 keys | one Set |
| 9 | errors | no StreamConfig → `INVALID_ARGUMENT`; missing region → `FAILED_PRECONDITION`; one 5 MiB message → `RESOURCE_EXHAUSTED` | missing region → `ServerOperationException ← RegionDestroyedException`; a 5 MiB putAll works (no message limit) |
| 10 | 3 good chunks, then a chunk with an unset key | `INVALID_ARGUMENT`, and **the 3,000 entries already sent stay written** | a null key fails on the client, before anything is sent: 0 written |
| 11 | the same 2,000 entries without streaming | PutStream ≈ 35× faster than 2,000 sequential Puts, ≈ 9× faster than 16 in flight | putAll ≈ 20× faster than sequential puts, ≈ 7× faster than 16 in flight |

Streams aren't transactional: the server applies each message as it arrives, and nothing rolls
back when a later one fails. Neither is putAll: a failure on one server partway through leaves
what was already applied (`PutAllPartialResultException` lists it).

## Load testing

```bash
make select-load-test EX=02                                   # pick a script
make select-load-test EX=02 SCRIPT=bulk/stream-vs-unary       # streaming vs one call per entry
make select-load-test EX=02 SCRIPT=bulk/throughput K6_ARGS="-e COUNT=50000 -e BATCH=1000"
```

The scripts are in [`../load-testing/scripts/bulk`](../load-testing/README.md#bulk-scripts-02-bulk-streaming).
Everything except `bulk/smoke` runs one client at a time, so each gets the cluster to itself.

### Streaming vs one call per entry

`bulk/stream-vs-unary`: 4 VUs repeat put → get → remove of 2,000 entries, first as streams,
then as 16 unary calls in flight, then one call at a time (45 s each, each client alone, Apple
Silicon laptop):

```
CLIENT · mode                        │    CALLS     ENTRIES  ENTRIES/S │      GF p50         p99
                                     │                                 │        (ms)        (ms)
native-java · stream                 │    15120    30240000     672000 │       7.707     116.999
native-java · unary×16               │     3765     7530000     167333 │      43.801      93.430
native-java · unary×1                │     1188     2376000      52800 │     139.941     296.375
native-java · unary×16 vs stream     │                      4.0× fewer │ 5.7× slower  20% faster
native-java · unary×1 vs stream      │                       13× fewer │  18× slower 2.5× slower
grpc-java · stream                   │     4020     8040000     178667 │      28.125     127.376
grpc-java · unary×16                 │      729     1458000      32400 │     227.839     610.509
grpc-java · unary×1                  │      288      576000      12800 │     617.703     972.357
grpc-java · unary×16 vs stream       │                      5.5× fewer │ 8.1× slower 4.8× slower
grpc-java · unary×1 vs stream        │                       14× fewer │  22× slower 7.6× slower
grpc-java vs native · stream         │                      3.8× fewer │ 3.6× slower   9% slower
grpc-java vs native · unary×16       │                      5.2× fewer │ 5.2× slower 6.5× slower
grpc-java vs native · unary×1        │                      4.1× fewer │ 4.4× slower 3.3× slower
```

- **Streaming is worth 13–14× over the naive loop, on both paths.** One call at a time pays a
  full round trip per entry.
- **Pipelining alone doesn't close the gap.** With 16 calls in flight the unary path recovers
  about 3×, but the stream is still 4× (native) to 5.5× (gRPC) ahead: one call per entry still
  costs a request, a response and server work per entry. A stream pays those per chunk.
- **gRPC unary calls are the most expensive.** With 16 in flight, grpc-java reaches 32,400
  entries/s against native's 167,000. Every unary gRPC call goes to server1, which forwards about
  half of them to server2 (01's finding).
- **Streaming doesn't close the native vs gRPC gap.** Streams vs putAll/getAll/removeAll is still
  3.8× fewer entries/s for gRPC. See the findings below for where it goes.

### Throughput, chunk size, large values, key sets

Each script runs one client at a time on an Apple Silicon laptop (Docker with 12 CPUs, no limits).
Server and client CPU come from cAdvisor, over each client's run.

**`bulk/throughput`**: 4 VUs loop put → get → remove of 10,000 entries in chunks of 500, for 2 minutes.

```
CLIENT                   CALLS     ENTRIES  ENTRIES/S  FAILED │     E2E p50         p95         p99 │      GF p50         p99 │    CPU/1k  RESULT
                                                              │        (ms)        (ms)        (ms) │        (ms)        (ms) │     (µs)
native-java               7091    70890000     589348      0% │      45.500     175.418     313.682 │      43.921     311.556 │      3725  PASS
grpc-java                 2525    25230000     209226      0% │     113.082     410.516     502.515 │     112.001     501.264 │       948  PASS
grpc-java vs native                        2.8× fewer         │ 2.5× slower 2.3× slower  60% slower │ 2.6× slower  61% slower │ 3.9× less
```

| CPU (cores) during the run | server1 | server2 | client | server CPU per entry | client CPU per entry |
|---|---|---|---|---|---|
| native-java | 2.48 | 2.47 | 1.98 | 8.4 µs | 3.4 µs |
| grpc-java | 2.48 | 1.54 | 0.19 | 19.2 µs | 0.9 µs |

- **gRPC moves 2.8× fewer entries per second.**
- **Lower client CPU doesn't mean cheaper.** grpc-java's client uses 3.9× less CPU per entry because
  the work moved to the servers. The servers spend 2.3× more CPU per entry. Counting both sides, a
  gRPC entry costs 20 µs of CPU and a native one 12 µs.
- **The work isn't spread evenly.** Native single-hop splits each chunk by bucket, so both servers
  do the same work. Every gRPC stream goes to server1, which translates the messages and forwards
  about half the entries to server2, so server1 does 62% of the server work.

**`bulk/batch-sweep`**: the same cycle at 10, 100, 500, 1,000 and 5,000 entries per chunk, 30 s each.

```
CLIENT · batch                       │    CALLS     ENTRIES  ENTRIES/S │      GF p50         p99
                                     │                                 │        (ms)        (ms)
native-java · 10                     │      624     6240000     208000 │     189.269     223.701
native-java · 100                    │     1713    17130000     571000 │      63.033     122.142
native-java · 500                    │     1839    18390000     613000 │      43.283     338.297
native-java · 1000                   │     1563    15630000     521000 │      63.245     327.359
native-java · 5000                   │     2076    20760000     692000 │      46.546     291.552
grpc-java · 10                       │      300     3000000     100000 │     411.107    1159.204
grpc-java · 100                      │      372     3720000     124000 │     329.515     378.968
grpc-java · 500                      │      708     7080000     236000 │     104.928     357.864
grpc-java · 1000                     │      816     8160000     272000 │      69.394     344.377
grpc-java · 5000                     │      804     8040000     268000 │      73.395     375.291
grpc-java vs native · 10             │                      2.1× fewer │ 2.2× slower 5.2× slower
grpc-java vs native · 100            │                      4.6× fewer │ 5.2× slower 3.1× slower
grpc-java vs native · 500            │                      2.6× fewer │ 2.4× slower   6% slower
grpc-java vs native · 1000           │                       48% fewer │  10% slower   5% slower
grpc-java vs native · 5000           │                      2.6× fewer │  58% slower  29% slower
```

- **gRPC needs bigger chunks.** At 10 and 100 entries per chunk it barely moves (100,000 and 124,000
  entries/s). It jumps at 500 and levels off at 1,000 (272,000).
- **Native is already at 571,000 entries/s with chunks of 100.** Each native call is a blocking
  round trip, but single-hop sends each server only its own keys.
- **Chunks bigger than `bulk-batch-size` don't help gRPC.** 5,000 is no better than 1,000. The
  server re-batches every stream into operations of at most 1,000 entries (the default).
- **The native rows are noisy.** 1,000 came out below 500, and 5,000 above both. 30 s per chunk size
  is short for the native client; read the trend, not each step.

**`bulk/large-payload`**: the cycle with 64 KiB values, 2,000 entries, chunks capped at ~1 MiB (16 entries), 1.5 minutes.

```
CLIENT                   CALLS     ENTRIES  ENTRIES/S  FAILED │     E2E p50         p95         p99 │      GF p50         p99 │    CPU/1k  RESULT
                                                              │        (ms)        (ms)        (ms) │        (ms)        (ms) │     (µs)
native-java               4121     8238000      91422      0% │      99.466     177.718     207.867 │      98.040     206.083 │     35124  PASS
grpc-java                 1454     2904000      32187      0% │     336.372     398.433     463.019 │     335.805     462.139 │     21685  PASS
grpc-java vs native                        2.8× fewer         │ 3.4× slower 2.2× slower 2.2× slower │ 3.4× slower 2.2× slower │  38% less
```

- **The same 2.8× as with 1 KiB values.** That's about 2.1 GB/s of values for gRPC and 6.0 GB/s for native.
- **server1 works hardest.** During grpc-java's run it used 3.4 cores against server2's 1.3. Server
  CPU per entry is 3.0× native's (147 µs vs 49 µs), because every value is copied into a protobuf message
  and back out on server1, and half are forwarded on.

**`bulk/keyset`**: a region of 500,000 keys, its whole key set fetched over and over by 2 VUs for 1 minute.

```
CLIENT                   CALLS     ENTRIES  ENTRIES/S  FAILED │     E2E p50         p95         p99 │      GF p50         p99 │    CPU/1k  RESULT
                                                              │        (ms)        (ms)        (ms) │        (ms)        (ms) │     (µs)
native-java                554   275500000    4445965      0% │     214.813     265.149     308.157 │     213.206     295.024 │       750  PASS
grpc-java                  623   310000000    4862238      0% │     192.476     208.722     233.652 │     191.165     221.528 │       104  PASS
grpc-java vs native                           9% more         │  10% faster  21% faster  24% faster │  10% faster  25% faster │ 7.2× less
```

- **The one bulk operation where gRPC wins.** KeySetStream lists 500,000 keys in 191 ms (p50), 10%
  faster than `keySetOnServer`, with a 25% better p99.
- **The client CPU tells why.** native-java spent 2.8 cores deserializing one 500,000-key `Set` per call,
  and grpc-java 0.5 cores reading pages of 1,000 keys. Server CPU was about the same (2.5 vs 2.2 cores).

## RPC ↔ native Java API

| gRPC (this example) | Native Java client | Notes |
|---|---|---|
| `PutStream` | `region.putAll(map)` per chunk | gRPC: one call for all chunks; the first message carries `StreamConfig` (the region) |
| `GetStream` | `region.getAll(keys)` per chunk | gRPC leaves out missing keys; native maps them to null |
| `RemoveStream` | `region.removeAll(keys)` per chunk | removing a missing key is fine on both |
| `KeySetStream` | `region.keySetOnServer()` | gRPC pages of `bulk-batch-size` keys; native one Set |
| `Put` / `Get` / `Remove` per entry (`mode=unary`) | `region.put/get/remove` per entry | the non-streaming baseline |
| `StreamConfig` in the first message | the client `Region` object | a later `config` is ignored |
| `max-message-size` (4 MiB) | none | chunk by bytes as well as by count |
| `gemfire.grpc.bulk-batch-size` (server, default 1000) | none | entries per internal bulk operation, and keys per KeySetStream page |

## Tuning `bulk-batch-size`

The server re-batches whatever the client sends into putAll/getAll/removeAll operations of at
most `gemfire.grpc.bulk-batch-size` entries. It's a server setting, so changing it means a
restart:

```bash
# in .env
GRPC_BULK_BATCH_SIZE=5000
# then
docker compose up -d --force-recreate server1 server2 grpc-client-java native-client-java
make select-load-test EX=02 SCRIPT=bulk/throughput
```

Regions are defined in the cluster configuration on the locator, so the restarted servers get
them back, but empty. Each load run is its own entry in Grafana's **load run** dropdown, so you
can compare the runs before and after. The client's demo checks KeySetStream's page size against
the same `.env` value.

## What to look for in Grafana

[localhost:3002](http://localhost:3002), dashboard "02 · Bulk streaming: native vs gRPC". The
**load run** dropdown picks which runs the panels show. The **mode** dropdown narrows every k6
panel to `stream` (PutStream/GetStream/RemoveStream/KeySetStream, or native putAll/getAll/removeAll/keySetOnServer),
`unary×16` (one Put/Get/Remove per entry, 16 in flight) or `unary×1` (one at a time). The **op**
dropdown then lists only that mode's ops: streamed calls are named `…Stream`, unary ones `Put`,
`Get`, `Remove`.

- **Streaming vs one call per entry.** Entries/s by mode (solid = stream, dashed = 16 in flight,
  dotted = one at a time) and a table comparing each client's modes with its own stream, in words.
- **Throughput by chunk size.** Fills in after `bulk/batch-sweep`.
- **Per-operation latency table.** GetStream stands out: see the next point.
- **GemFire servers → Messages to the other server.** server1 forwards a putAll or removeAll as
  one message per bucket, but a getAll as **one `GetMessage` per key** it doesn't host. Three
  100,000-entry GetStreams made server1 send about 187,000 of them. Native single-hop sends each
  server only its own keys, so it adds almost nothing here.
- **Bulk operations per server.** `putalls` / `removeAlls` (CachePerfStats) count every bulk
  operation, the streams' too. `putAllRequests` / `getAllRequests` (CacheServerStats) count only
  native-protocol requests.
- **gRPC streams (interceptor).** Messages per call show the chunking: a 10,000-entry PutStream in
  chunks of 500 sends 21 messages and receives 1.
- **Memory working set** during `bulk/keyset`.

## Findings and gotchas

1. **Streaming is the only fast way to move many entries over gRPC.** A PutStream is about 14×
   faster than one Put per entry, and 5.5× faster than 16 unary calls in flight (`bulk/stream-vs-unary`).
2. **Even streamed, gRPC moves about 2.8× fewer entries per second than putAll/getAll/removeAll.**
   That held for 1 KiB values, 64 KiB values, and chunk sizes from 500 up (`bulk/throughput`,
   `bulk/large-payload`, `bulk/batch-sweep`).
3. **Key sets are the exception.** KeySetStream beat `keySetOnServer` by 10% at p50 and 25% at p99,
   with 7× less client CPU: pages of keys are cheaper to read than one huge `Set`.
4. **The gap is server work, and it lands on server1.**
   - Every stream from a `dns:///server1` channel goes to server1, which translates it and forwards half the entries.
   - Native single-hop sends each server its own keys.
   - Per entry, the servers spent 2.3× (1 KiB) to 3.0× (64 KiB) more CPU for gRPC, and server1 did most of it.
   - xDS ([05](../05-xds-discovery/)) spreads the streams across servers, but it isn't partition-aware either.
5. **Low client CPU isn't low cost.** grpc-java's client used less CPU per entry than native-java's
   in every run: 38% less with 64 KiB values, 3.9× less with 1 KiB, 7.2× less for key sets.
   That's because the server does the work the native client does itself (bucket routing, serialization).
6. **Use chunks of at least 500 entries, and no more than `bulk-batch-size`.** gRPC's throughput rises
   steeply up to 1,000 entries per chunk and stops there, because the server re-batches into operations of
   `gemfire.grpc.bulk-batch-size` (1,000). Raise that setting before raising the chunk size.
7. **Chunk by bytes too.** One message over `max-message-size` (4 MiB) fails the whole stream with
   `RESOURCE_EXHAUSTED`. Native putAll has no such limit (demo section 9).
8. **Streams aren't transactional, and neither is putAll.** A bad chunk fails the stream with
   `INVALID_ARGUMENT`, but the chunks before it stay written (demo section 10). Make bulk writes
   idempotent, so a failed stream can simply be sent again.
9. **GetStream leaves out missing keys; getAll maps them to null.** Count what comes back, rather than
   assuming one result per key.
10. **Respect flow control.** `StreamObserver.onNext` never blocks, so a fast producer piles messages
    up in client memory. Wait for `isReady()` (see `StreamCall`).

## Layout

```
02-bulk-streaming/
├── .env                      # image, extension paths, ports (30NN/90NN/81NN/82NN), GRPC_BULK_BATCH_SIZE
├── docker-compose.yml        # locator, server1, server2, gfsh-init, 2 clients, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server with the gRPC extension and bulk-batch-size
│   └── init.gfsh             # creates bulk-grpc, bulk-native, demo-grpc, demo-native
├── grpc-client-java/         # GrpcClientApplication, BulkController, Bulk, GrpcBulk, StreamCall, RpcMetricsInterceptor, Demo, DemoRunner, DemoMetrics, Settings
├── native-client-java/       # NativeClientApplication, BulkController, Bulk, NativeBulk, Demo, DemoRunner, DemoMetrics, Settings
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/bulk-native-vs-grpc.json
        └── provisioning/
```

`BulkController.java`, `Bulk.java`, `DemoRunner.java`, `DemoMetrics.java`, `Settings.java` and `Checks.java` are identical in both clients.

Metrics, the same names as 01 plus two:

- `demo_op_latency_seconds{client,example,region,op,code}`: one /bulk call's GemFire work.
- `demo_entries_total{client,example,region,op}`: entries moved.
- `demo_rpc_duration_seconds{client,example,region,method,code}`: every gRPC call (interceptor).
- `demo_stream_messages_total{client,example,method,direction}`: every gRPC message sent and received.
