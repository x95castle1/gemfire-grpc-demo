# 03 · OQL query over gRPC (Go)

This example runs the **`Query` RPC** of GemFire for gRPC: the client sends an OQL statement and
its parameters, the server executes it, and the rows stream back in batches.

**It has only one client, written in Go, and nothing to compare it with.** The other
examples measure gRPC against the native Java client. This one keeps to the RPC itself: what
it accepts, what comes back, and where it fails. Values are scalars only. Structured values
(`google.protobuf.Any`) and querying into their fields are covered in 04.

```
 k6 (make select-load-test) ──HTTP──▶ grpc-client-go :8080 ──Query :50051──▶ server1 ──QueryMessage──▶ server2
                                                                                  (each server runs the query on its own buckets)
                                                        locator :10334
 prometheus ◀── client /metrics · members :7070/metrics/ · cadvisor · k6 (remote write)        grafana :3003
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=03             # cluster, the Go client (it runs the narrated demo at startup), monitoring
make logs EX=03              # read the demo: pick grpc-client-go
make select-load-test EX=03  # query/steady: a mix of five queries at 100 req/s
open http://localhost:3003
make stop EX=03
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=03`.

## The Query RPC

```protobuf
rpc Query (QueryRequest) returns (stream QueryResponse);

message QueryRequest  { string query = 1; repeated Value parameters = 2; }      // parameters[0] binds $1
message QueryResponse { repeated string field_names = 1; repeated QueryRow rows = 2; }
message QueryRow      { repeated QueryValue fields = 1; }                       // unset QueryValue = NULL
```

- **No `region_name`.** The regions come from the query's own `FROM` clause.
- **Parameters** bind in order: `parameters[0]` is `$1`. Too few → `INVALID_ARGUMENT`; extras are ignored.
- **Rows stream back** in messages of up to `gemfire.grpc.query-batch-size` rows (default 1000).
  `field_names` is on the first message only. An empty result is still one message, with no rows.
- **Columns.** `SELECT *` over plain values has one unnamed column (`field_names` empty). A projection
  (`SELECT e.key, e.value …`) names its columns, and `AS` renames them.

The client is in [`grpc-client-go/`](grpc-client-go/): `query.go` runs a query and reads the stream,
`demo.go` is the narrated tour, and `api.go` is the HTTP API.

## HTTP API

| Endpoint | What it does |
|---|---|
| `GET /` | endpoint list |
| `GET /health` | 200 once the HTTP API is up (after the startup demo) |
| `POST /demo` | runs the narrated demo; 200 means every assertion passed (also runs at startup) |
| `GET /demo` | result of the last demo run, e.g. `{"ran":true,"passed":28,"failed":0}` |
| `POST /query/load?count=10000` | fills the `query` region: `item-00000`… → ints spread evenly over 0–9,999 |
| `POST /query?p=…&p=…` | runs the OQL in the request body; each `p` binds `$1`, `$2`, … |

A `p` that parses as an int32 is bound as one, and anything else as a string. The prefixes
`int64:`, `double:` and `string:` force a type. `rows=` sets how many rows the JSON includes
(default 20; every row is still read and counted). `name=` labels the query in metrics.

```bash
curl -X POST 'localhost:8303/query/load?count=10000'
curl -X POST 'localhost:8303/query?p=100' --data 'SELECT e.key, e.value FROM /query.entrySet e WHERE e.value < $1'
```

```json
{"query":"Query","fieldNames":["key","value"],"types":["STRING","INT32"],"rowCount":100,"messages":1,
 "batches":[100],"firstRowMicros":5203,"micros":5214,"rows":[["item-09308",52],["item-03061",59],…]}
```

`types` shows how each column came across (from the first row). `batches` is rows per
`QueryResponse` message. Every response carries `X-GemFire-Micros` (the whole Query call),
`X-Rows` and `X-Messages`. Errors are 502 with `X-Error-Code`, e.g. `INVALID_ARGUMENT`.

Because the values are ints, `v < N` matches about N rows per 10,000 entries.

## The narrated demo

The client runs this at startup (`make logs EX=03`) on its own region, `demo-grpc-go` (5,000 entries).
Every expected row count is computed from the data the demo wrote.

| # | Section | What it shows |
|---|---|---|
| 1 | `SELECT *` over plain values | `v < $1` returns the right rows; `field_names` is empty; each row is one `INT32` |
| 2 | Projections name their columns | `e.key, e.value` → `[key value]`; `AS` and expressions (`e.value * 2 AS doubled`); without `AS`, `MIN(v), MAX(v)` are both named `v` |
| 3 | Parameters | `$1`/`$2` ranges, a string key, `IN SET($1, $2)`, `LIKE $1`; too few → `INVALID_ARGUMENT`; extras ignored |
| 4 | Aggregates, DISTINCT, ORDER BY, LIMIT | `MIN/MAX/SUM/COUNT` match the data, `AVG` comes back as `FLOAT`; `ORDER BY … DESC LIMIT $1`; `DISTINCT v / 1000` |
| 5 | Results stream in batches | 5,000 keys arrive as 5 messages of 1,000; `field_names` only on the first; an empty result is one message |
| 6 | UNDEFINED, and NULL | `v.length` over mixed ints and strings → `UNIMPLEMENTED`; `IS_DEFINED` and `WHERE` filter it out; a `NULL` literal column → `INTERNAL` |
| 7 | Errors | syntax → `INVALID_ARGUMENT`; unknown region → `FAILED_PRECONDITION`; a forbidden method → `PERMISSION_DENIED`; an unrepresentable type → `UNIMPLEMENTED` |

## How GemFire's results come across

A `Value` you write is stored as the matching Java object, and a query result is mapped back
the same way:

| Result (Java) | `QueryValue` | Example |
|---|---|---|
| `String` | `string` | `e.key`, `e.key.toUpperCase()` |
| `Integer` | `int32` | `v`, `COUNT(*)`, `SUM(v)`, `v.length` on a string |
| `Long` | `int64` | |
| `Float` | `float` | `AVG(v)` of ints |
| `Double` | `double` | |
| `Boolean` | `bool` | `v > 5000` |
| `byte[]` | `bytes` | |
| UNDEFINED | none: the call fails with `UNIMPLEMENTED` | `v.length` on an int |
| anything else | none: `UNIMPLEMENTED: Unsupported query result column type` | `java.util.Optional` |

## Findings

1. **UNDEFINED fails a projection but not a filter.** `SELECT v.length` over mixed ints and
   strings fails the whole call with `UNIMPLEMENTED: Query result column is UNDEFINED`, because
   `QueryValue` has no UNDEFINED. The same expression in `WHERE` just doesn't match, so
   `WHERE v.length > 3` or `WHERE IS_DEFINED(v.length)` returns only the strings. Filter
   UNDEFINED out before you project it.
2. **A `NULL` literal column crashes the query on the server.** `SELECT e.key, NULL …` returns
   `INTERNAL: Cannot invoke "Object.toString()" because "projExpr._obj" is null` instead of an
   unset `QueryValue`. The contract says a null column arrives as an unset `QueryValue`, but with
   scalar values there's no simple query that produces one.
3. **Name your aggregate columns.** Without `AS`, a column is named after its argument, so
   `SELECT MIN(v), MAX(v)` returns `field_names` `[v v]`. With `AS lo, AS hi` you can tell them apart.
4. **`AVG` of ints is a `FLOAT`** (a Java `Float`), so it's rounded to about 7 significant
   digits. `MIN`, `MAX`, `SUM` and `COUNT` stay `INT32`.
5. **The server's method authorizer still applies.** `v.getClass()` → `PERMISSION_DENIED:
   Unauthorized access to method: getClass`. GemFire's default authorizer allows the usual
   `String` and `Number` methods (`toUpperCase`, `length`, …) and blocks reflection.
6. **A partitioned-region query runs on both servers.** The client only talks to `server1`, but
   `server1` sends a `QueryMessage` to `server2` for its buckets and merges the results. The
   dashboard's "Partitioned-query messages sent" panel shows it.
7. **A key lookup through OQL is a scan.** `WHERE e.key = $1` took a median 2.9 ms over the
   10,000 entries in `query/steady`. The same Go client's unary calls in 01 (`Get` among them)
   have a median around 0.5 ms. Use `Get` for keys and OQL for everything else. An OQL index would close part of the gap; this example
   doesn't create one.

## Load test results

`make select-load-test EX=03 SCRIPT=query/steady` (100 req/s for 1 minute, 10,000 entries, Docker Desktop on an Apple Silicon Mac):

| Query | OQL | Rows | p50 | p99 |
|---|---|---|---|---|
| `scan` | `SELECT * FROM /query v WHERE v < $1` ($1 = 10–200) | ~104 | 1.80 ms | 3.54 ms |
| `count` | `SELECT COUNT(*) FROM /query v WHERE v < $1` | 1 | 2.13 ms | 4.35 ms |
| `key` | `SELECT e.value FROM /query.entrySet e WHERE e.key = $1` | 1 | 2.86 ms | 4.74 ms |
| `range` | `SELECT e.key, e.value FROM /query.entrySet e WHERE e.value >= $1 AND e.value < $2` | 100 | 3.28 ms | 5.43 ms |
| `top` | `SELECT e.key, e.value FROM /query.entrySet e ORDER BY e.value DESC LIMIT 10` | 10 | 4.67 ms | 7.11 ms |

Every query runs over the whole region (there's no index), so the time depends on the work per
entry more than on the rows returned. `entrySet` queries cost more than plain values, and
`ORDER BY` costs the most. End to end (HTTP included), the p50 was 3.1 ms, and the client used
about 0.5 ms of CPU per query.

## Tuning query-batch-size

`gemfire.grpc.query-batch-size` (default 1000) caps the rows in one `QueryResponse`. It's set
on the servers from `GRPC_QUERY_BATCH_SIZE` in [`.env`](.env). After changing it, recreate them:

```bash
docker compose up -d --force-recreate server1 server2
```

The demo reads the same variable, so section 5 expects the new message count. Smaller batches
mean smaller messages, so the client can start on the first rows while the rest stream in;
larger batches mean fewer messages for big results. Every message must also fit in `gemfire.grpc.max-message-size` (4 MiB).

## What to look for in Grafana

The dashboard (`:3003`) follows the **load run** and **query** dropdowns.
- **Queries by name:** p50/p99 and rows/s per query, plus a table over the time range.
- **gRPC Query streams:** every Query call by status code (the demo's errors show up here),
  duration, and `QueryResponse` messages per call.
- **GemFire servers:**
  - query executions and average execution time per server. Both count per-bucket executions, so they're far higher and shorter than the client's queries.
  - the partitioned-query messages `server1` sends to `server2`.

## Layout

```
03-oql-query/
├── .env                      # image, extension paths, ports (3003/9003/8303), GRPC_QUERY_BATCH_SIZE
├── docker-compose.yml        # locator, server1, server2, gfsh-init, grpc-client-go, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # starts a locator/server with the gRPC extension and query-batch-size
│   └── init.gfsh             # creates query and demo-grpc-go
├── grpc-client-go/           # main · api (HTTP) · query · demo · metrics · interceptors · config · checks
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/oql-query.json
        └── provisioning/
```

Metrics, the same names as the other examples plus three:
- `demo_op_latency_seconds{op}` is one `/query` call, with `op` from `name=`.
- `demo_rows_total{op}` counts the rows returned.
- `demo_stream_messages_total{method="Query", direction="received"}` counts `QueryResponse` messages.
- `demo_rpc_duration_seconds{method="Query"}` comes from the stream interceptor.
- k6 adds `query_rows`.
