# 10 · Error handling (Go)

Every failed GemFire for gRPC call returns a **status**: a code and a message. This example sets
out to meet each code on purpose and shows what a client should do about it:
- **Fix the request.**
- **Retry.**
- **Wait.**
- **Give up.**

It also covers:
- **Deadlines:** the client's deadline vs the server's own query limit.
- **Three ways to ride out a server outage**, on three channels side by side.
- **Low memory**, when a server refuses writes.

The Go client reaches server1 over three channels that handle a failure differently:

| Channel | Setup | When server1 can't be reached |
|---|---|---|
| `failfast` | the grpc-go default | the call fails at once: `UNAVAILABLE` |
| `retry` | a retry policy in the service config | up to 5 attempts with backoff (about 8 s), then `UNAVAILABLE` |
| `waitforready` | `grpc.WaitForReady(true)` | the call waits for a connection, up to its 30 s deadline, then `DEADLINE_EXCEEDED` |

```
                                     ┌─ failfast      ─┐
 k6 ──HTTP──▶ grpc-client-go :8080 ──┼─ retry         ─┼── dns:///server1:50051 ──▶ server1   (server2 holds redundant copies)
   (?via= ?region=)                  └─ waitforready  ─┘
```

Go only by design, like 03–09.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=10             # cluster, the Go client (demo a few seconds after startup), monitoring
make logs EX=10              # read the demo (grpc-client-go)
make select-load-test EX=10 SCRIPT=errors/retries   # 2 minutes; server1 stops at 40 s and starts at 55 s
open http://localhost:3010
make stop EX=10
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=10`.

```bash
curl 'localhost:8310/crud/get?key=1'                        # 200 {"code":"OK","attempts":1,...}
curl 'localhost:8310/crud/get?key=1&region=missing'         # 400 FAILED_PRECONDITION "Region not found: missing"
curl -X POST 'localhost:8310/crud/put?key=1&size=5242880'   # 429 RESOURCE_EXHAUSTED "gRPC message exceeds maximum size 4194304"
```

A failed call answers with the gRPC code, its message, and what to do (`"action"`). The HTTP
status depends on the code:

| HTTP | gRPC code |
|---|---|
| 400 | `INVALID_ARGUMENT`, `FAILED_PRECONDITION` |
| 429 | `RESOURCE_EXHAUSTED` |
| 503 | `UNAVAILABLE` |
| 504 | `DEADLINE_EXCEEDED` |
| 500 | anything else |

## The status codes, and what to do

Each row was triggered by the demo, with the message the server sent:

| Code | Triggered by | Message | What to do |
|---|---|---|---|
| `INVALID_ARGUMENT` | a Get with no key | `Key must be set (string, int, or long)` | fix the request |
| | a Put with no value | `Value cannot be null for key: …` | |
| | an empty query; OQL that doesn't parse | `query must not be empty`; `Syntax error in query: unexpected token: FROM` | |
| `FAILED_PRECONDITION` | a region that doesn't exist (Get or `FROM /missing`) | `Region not found: missing` | fix the request (or create the region) |
| `UNIMPLEMENTED` | a query column that's UNDEFINED (`SELECT o.nope …`) | `Query result column is UNDEFINED` | give up and report it |
| | a method the server doesn't have | `Method not found: gemfire.v1.GemFire/Nope` | |
| `INTERNAL` | an empty region name (`""`) | `Error: path cannot be empty` | give up and report it |
| `RESOURCE_EXHAUSTED` | a 5 MB Put | `gRPC message exceeds maximum size 4194304: 5242913` | **split the request**: it will never fit |
| | a Put while a server is low on memory | `… because members [server1…] are running low on memory` | **back off, then retry**: it will succeed later |
| `DEADLINE_EXCEEDED` | the client's deadline passed | `context deadline exceeded` | check the deadline, or make the work smaller |
| | the server's query limit | `Query execution canceled after exceeding max execution time 2000ms` | |
| `CANCELLED` | the client cancelled the call | `context canceled` | nothing |
| `UNAVAILABLE` | no server to connect to | `name resolver error: produced zero addresses`; `connection refused` | retry with backoff, or wait for ready |
| `UNAUTHENTICATED`, `PERMISSION_DENIED` | security (see [08](../08-token-auth-rbac/)) | | fix the credentials or permissions |

Two misses that aren't errors:
- a Get of a key that isn't there is `OK` with no value
- a PutIfAbsent on a key that exists is `OK` with `inserted=false`

The client's decision is in [`statuses.go`](grpc-client-go/statuses.go):
- `classify(err)` reads `status.Code(err)`.
- For `RESOURCE_EXHAUSTED` it also reads the message, because only the message tells the two causes apart.

The server never attaches error details (`status.Details()` was empty every time), so the code and message are all a client gets.

## Deadlines

The demo runs a slow query, `SELECT COUNT(*) FROM /orders a, /orders b` (10,000 × 10,000 pairs),
two ways.

**The server has its own limit.** The servers run with
`-Dgemfire.Cache.MAX_QUERY_EXECUTION_TIME=2000` ([`start-member.sh`](gemfire/start-member.sh), `.env`).
With a 30 s client deadline, the server stops the query after 2.0 s and returns
`DEADLINE_EXCEEDED: Query execution canceled after exceeding max execution time 2000ms`.

**A client deadline doesn't stop the server's work.**
- With a 200 ms deadline, the client gets `DEADLINE_EXCEEDED: context deadline exceeded` after 202 ms.
- But server1 keeps going. Its `gemfire_queryExecutionTime` grows by **2 s** for that query, and its log says
  `Query execution canceled after exceeding max execution time 2000ms`. The demo reads the metric to check this.
- A client that gives up and retries a slow query adds load; it doesn't remove it.

**Cancelling a stream:** a KeySetStream cancelled after its first message ends with `CANCELLED`, and
the 1,000 keys already received are still there.

## Retries, wait-for-ready, fail-fast

Section 3 of the demo sends the same Get on each kind of channel to `dns:///server3:50051`, where no server runs:

| Channel | Result | Attempts | Took |
|---|---|---|---|
| `failfast` | `UNAVAILABLE` | 1 | under 1 ms |
| `retry` | `UNAVAILABLE` (`max retries exhausted: failed after 5 attempts`) | 5 | 6.6 s |
| `waitforready` (2 s deadline) | `DEADLINE_EXCEEDED` (`latest balancer error: …`) | 1 | 2.0 s |

**The retry policy** is service config ([`channels.go`](grpc-client-go/channels.go)):

```go
grpc.NewClient("dns:///server1:50051", grpc.WithDefaultServiceConfig(`{
  "methodConfig": [{
    "name": [{"service": "gemfire.v1.GemFire", "method": "Get"}, {"service": "gemfire.v1.GemFire", "method": "Put"}, …],
    "retryPolicy": {"maxAttempts": 5, "initialBackoff": "0.5s", "maxBackoff": "4s",
                    "backoffMultiplier": 2, "retryableStatusCodes": ["UNAVAILABLE"]}
  }]}`))
```

**Only idempotent methods are listed:** Get, Put, ContainsKey, EntryCount, Query, GetStream, KeySetStream.
- A retry follows a failed attempt, and an attempt that failed with `UNAVAILABLE` may still have been applied.
  Repeating a Get or a Put gives the same result.
- Repeating a PutIfAbsent gives `inserted=false`, and a Remove gives `removed=false`. Those are wrong answers, not errors.
- The demo checks that PutIfAbsent on the retry channel is tried exactly once.

**Wait-for-ready** is a call option (`grpc.WithDefaultCallOptions(grpc.WaitForReady(true))` here):
- The call waits for a connection instead of failing, so **the deadline is what ends it**.
- Without a deadline it can wait forever.

**Reconnect backoff:**
- After a connection fails, grpc-go waits longer and longer between reconnect attempts. By default the wait grows to 120 s.
- Here every channel caps it at 5 s (`grpc.WithConnectParams`), so a channel notices quickly that server1 is back.

**Counting attempts:**
- Interceptors see each call once, however many times it was retried. A `stats.Handler` sees each attempt.
- It has to count `stats.End`, not `stats.Begin`. grpc-go begins an extra attempt for each retry and
  abandons it without ending it, so counting `Begin` reports 9 attempts for 5.

## The outage: `errors/retries`

`make select-load-test EX=10 SCRIPT=errors/retries` sends 20 calls/s on each channel for 2 minutes
(80% Get, 20% Put). server1 is stopped at 40 s and started at 55 s. Counting its startup, it can't be
reached for about 20 s.

```
By via: rows compare each via with failfast (all ran at the same time)
CLIENT · via                         │    CALLS   FAILED │      GF p50         p99
grpc-go · failfast                   │     2401   16.95% │       1.275       5.166
grpc-go · retry                      │     2401    9.95% │       1.338    7936.329
grpc-go · waitforready               │     2401    0.04% │       1.426   18512.004

Events:
  12:58:47  +40s  stop server1
  12:59:01  +55s  start server1
```

**failfast** failed every call for as long as server1 couldn't be reached.

**retry** saved only the calls that started in the last ~8 s of the outage. Their retries ran past it:
- the policy's window is short (5 attempts, about 8 s), and the outage was longer
- the saved calls took up to 7.9 s (p99 of successful calls)

**waitforready** lost one call in 2,401:
- **The one call it lost was already in flight on server1 when server1 stopped** (`UNAVAILABLE`).
  Wait-for-ready waits *before* a call is sent; it can't save a call already on the wire.
- **Every other call waited.** The calls held during the outage took up to 18.5 s, under their 30 s deadline.
- **The wait costs threads.** At 20 calls/s, ~400 requests were parked at once. That's why this
  example's `HTTP_THREADS` is 1024, not 64; otherwise the parked calls take every slot and starve the other channels.

None of the three keeps the service working while server1 is down. They only choose between
failing fast, failing a few seconds later, and making callers wait. To keep serving, you need a
second server to send the calls to (xDS, [05](../05-xds-discovery/)).

## Low memory

The servers run with a 512 MB heap and `--critical-heap-percentage=80` (`.env`).

**Getting there.** The demo Puts 1 MB values into the `ballast` region until a server refuses:
- **About 550 Puts and under 2 s:** `RESOURCE_EXHAUSTED: PartitionedRegion: /ballast cannot process operation on key 574 because members [server1…] are running low on memory`.
- GemFire logs `Member: server1 … above heap critical threshold`.

**While the server is critical:**
- **Low memory is per member, not per region.** Small Puts to `orders` are refused too, 20 of 20,
  because each also writes `orders`' redundant copy on server1.
- **Reads and removes still work.**

**Recovery:**
- The demo removes the ballast and retries a Put with backoff. It succeeds **1.4 s** later.
- The server lets writes in as soon as a GC brings its heap back under the threshold.

## See it in Grafana

The dashboard (`:3010`):
- **Retries vs no retries** (top row, from `errors/retries`):
  - each channel's share of failed requests
  - failed calls by channel and code
  - p99 call time by channel (waitforready's waiting shows here)
  - attempts per call (up to 5 on retry)
  - members up
  - the stop and start, annotated
- **Status codes:** every call by final code, and the table of what to do.
- **Low memory:** server heap (tenured) against the critical threshold; refused writes; GemFire's
  `heapCriticalEvents`. GemFire publishes its statistics every 5 s here (15 s elsewhere), and the demo
  stays full for 6 s so the panel catches it.
- **Collapsed:** performance, servers and resources.

The startup demo runs about 6 seconds after the client starts. By then Prometheus has scraped
its counters at zero, so `increase()` counts every one of the demo's calls.

## Findings

1. **A client deadline doesn't stop the server.** The client gave up after 200 ms, but server1 ran the query
   for its full 2 s limit. Set `MAX_QUERY_EXECUTION_TIME` on the servers; that's the only thing that stops a runaway query.
2. **The same code can need opposite handling; read the message.**
   - `RESOURCE_EXHAUSTED` means "too big, never retry" for a 5 MB message, and "back off and retry" under low memory.
   - `DEADLINE_EXCEEDED` comes from the client (`context deadline exceeded`) or from the server's limit.
3. **No error details.** GemFire for gRPC sends a code and a message, never `google.rpc` details.
4. **An empty region name is `INTERNAL`** (`path cannot be empty`), though it's a bad request. The
   proto lists `INVALID_ARGUMENT` for bad requests. A missing region is `FAILED_PRECONDITION`, as documented.
5. **Retries are for blips, not outages.** The retry policy cut failures from 17% to 10% over a
   20 s outage. It helps most when a server restarts quickly, or when a call is lost in flight.
6. **Wait-for-ready trades failures for waiting.** It lost only the call already in flight, but held
   calls for up to 18.5 s. That needs a deadline on every call, and room for the parked requests.
7. **Low memory is per member, and it clears itself.** A member above its critical threshold refuses
   every write it would store (including redundant copies), keeps serving reads and removes, and
   accepts writes again once its heap drops.
8. **Count retries with `stats.End`.** grpc-go emits two `stats.Begin` events per retry, one of them for an attempt it abandons.

## Layout

```
10-error-handling/
├── .env                      # image, extension paths, ports (3010/9010/8310), server heap, critical heap %, query limit, HTTP_THREADS
├── docker-compose.yml        # locator, server1, server2, gfsh-init, grpc-client-go, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # servers: --critical-heap-percentage, MAX_QUERY_EXECUTION_TIME, metrics every 5 s
│   └── init.gfsh             # PARTITION_REDUNDANT orders and demo-grpc-go; PARTITION ballast; no "missing"
├── grpc-client-go/
│   └── *.go                  # main · channels (failfast, retry policy, waitforready; attempt counting) · statuses (what to do)
│                             # · crud · api (?via=, ?region=, HTTP status by code) · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml        # GemFire scraped every 5 s
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/error-handling.json
        └── provisioning/
```
