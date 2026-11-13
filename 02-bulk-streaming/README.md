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

The same Java 21 HTTP services as 01 (the JDK's built-in server, 64 worker threads). Only the
class behind `/bulk` differs.

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

RESULTS_PLACEHOLDER

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

FINDINGS_PLACEHOLDER

## Layout

```
02-bulk-streaming/
├── .env                      # image, extension paths, ports (30NN/90NN/81NN/82NN), GRPC_BULK_BATCH_SIZE
├── docker-compose.yml        # locator, server1, server2, gfsh-init, 2 clients, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server with the gRPC extension and bulk-batch-size
│   └── init.gfsh             # creates bulk-grpc, bulk-native, demo-grpc, demo-native
├── grpc-client-java/         # Api, GrpcBulk, StreamCall, Demo, Metrics, RpcMetrics (interceptor)
├── native-client-java/       # Api, NativeBulk, Demo, Metrics
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/bulk-native-vs-grpc.json
        └── provisioning/
```

`Api.java`, `Metrics.java`, `Checks.java` and `Config.java` are identical in both clients.

Metrics, the same names as 01 plus two:

- `demo_op_latency_seconds{client,example,region,op,code}`: one /bulk call's GemFire work.
- `demo_entries_total{client,example,region,op}`: entries moved.
- `demo_rpc_duration_seconds{client,example,region,method,code}`: every gRPC call (interceptor).
- `demo_stream_messages_total{client,example,method,direction}`: every gRPC message sent and received.
