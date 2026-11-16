# load-testing/: k6 scripts shared by every example

Every example's clients are HTTP services with the same `/crud` API
(see [01-crud-basics](../01-crud-basics/README.md#http-api)), so one script can load-test any
example. The same `crud/steady.js` run against 01 (plaintext), 06 (TLS), 07 (mTLS) and
08 (tokens) shows what each feature costs while the workload stays the same.

```bash
make select-load-test                                          # fzf: example → script (preview) → client
make select-load-test EX=01 SCRIPT=crud/ramp                   # no prompts
make select-load-test EX=01 SCRIPT=crud/steady CLIENT=grpc-java K6_ARGS="-e RATE=3000 -e DURATION=1m"
make select-load-test EX=01 SCRIPT=crud/steady RUN=sequential  # one client at a time instead of all at once
make results                                                   # browse past runs (or: make results RUN=latest)
```

`make select-load-test` starts the example if it isn't running. It then runs k6 (`grafana/k6:2.3.0`) as a
throwaway container on the example's Docker network: one k6 process per client, all at once
(`parallel`) or one after another (`sequential`). At the end it prints a comparison table.

`CLIENT` defaults to every client the example runs and the script supports, which is all three in 01
(native-java, grpc-java, grpc-go). In a terminal, fzf asks first, with `all` at the top.

### While it runs

- **Sequential** (`crud/ramp`, `crud/saturate`, or a single client): you get k6's own screen for
  each client in turn. That's the banner, a live progress bar and k6's full end-of-test summary.
- **Parallel** (everything else): one k6 process per client, all at once, on one live screen.
  It shows k6's banner and scenario, then for each client a k6-style progress bar, plus live
  numbers for this run from Prometheus:

  ```
    native-java  steady    [===========>------------------]  38%  02/100 VUs  0m45.9s/2m0s  1000.00 iters/s
                 998.4 req/s · E2E p50 0.26 ms · GF p50 0.10 ms · GF p99 0.74 ms · 0 failed
    grpc-java    steady    [===========>------------------]  38%  03/100 VUs  0m45.9s/2m0s  1000.00 iters/s
                 996.9 req/s · E2E p50 0.46 ms · GF p50 0.31 ms · GF p99 3.02 ms · 0 failed
    grpc-go      steady    [===========>------------------]  38%  02/100 VUs  0m45.9s/2m0s  1000.00 iters/s
                 997.2 req/s · E2E p50 0.38 ms · GF p50 0.26 ms · GF p99 2.41 ms · 0 failed
  ```

  Each client's full k6 output, including its summary, goes to `results/<run>/<client>.log`.

Without a terminal (CI, scripts), k6 runs quietly and only the comparison table is printed.

### Query script (03-oql-query)

03 has one client (grpc-go) and one script. Its setup loads 10,000 entries into the `query`
region, then it sends a random mix of five named OQL queries. Each query is tagged with its name
(`op`), which the 03 dashboard's "query" dropdown filters on. Rows returned are counted in
`query_rows`.

| Script | Load | Run |
|---|---|---|
| `query/steady` | constant arrival rate · `scan`, `range`, `count`, `top`, `key` · 100 req/s · 1 m | parallel (one client) |

### People script (04-structured-values)

04 has one client (grpc-go) and one script. Its setup loads 10,000 `Person` messages (as
`google.protobuf.Any`) into the `people` region. It then mixes single-entry calls with OQL
queries into the messages' fields. The queries go through `lib/query.js`, so their rows count in
`query_rows` too.

| Script | Load | Run |
|---|---|---|
| `people/steady` | constant arrival rate · `get` 50%, `put` 20%, `by-city` 15%, `age-range` 10%, `nickname` 5% · 100 req/s · 1 m | parallel (one client) |

### xDS scripts (05-xds-discovery, 06-tls, 07-mtls, 09-secure-xds)

05–07's and 09's Go clients reach the cluster over two channels (TLS in 06, mutual TLS in 07,
mutual TLS plus tokens in 09), `direct` (`dns:///server1:50051`) and
`xds` (`xds:///gemfire_grpc`). Both scripts run one scenario per channel at the same time, with
80% Get and 20% Put. The traffic is light on purpose: these scripts are there to show on the
dashboard which server answers each call, not to measure speed. They break the results down by
`via`.

| Script | Load | Run |
|---|---|---|
| `xds/balance` | both channels · 20 req/s each · 1 m | parallel (one client) |
| `xds/failover` | both channels · 20 req/s each · 2 m · `Events: 40s stop server1, 80s start server1` · failures allowed | parallel (one client) |

### Auth script (08-token-auth-rbac, 09-secure-xds)

08's Go client acts as different users (`?user=`), each with its own JWT. `auth/mixed` sends random
users doing Gets and Puts on two regions. 401 (UNAUTHENTICATED) and 403 (PERMISSION_DENIED) are the
point of the test, so `http.expectedStatuses(200, 401, 403)` keeps them out of `http_req_failed`. Its
`Breakdown: user=…` table shows OK / 401 / 403 per user: the script tracks
`http_reqs{user:x,status:401|403}`, and `load.sh` adds those columns whenever they're present.

| Script | Load | Run |
|---|---|---|
| `auth/mixed` | alice, bob, carol, none × Get 70% / Put 30% × orders, customers · 20 req/s · 1 m | parallel (one client) |

## Layout

```
load-testing/
├── lib/crud.js        # /crud helpers (01): op mix, calls, preload, options, gemfire_call_duration trend
├── lib/bulk.js        # /bulk helpers (02): bulk calls, the bulk_entries counter, modes, per-VU key ranges
├── lib/query.js       # /query helpers (03, 04): named OQL calls, the query_rows counter, loading the data
├── lib/people.js      # /people helpers (04): Get/Put of Person messages, loading the data
├── lib/xds.js         # /crud calls with ?via=direct|xds (05): one scenario per channel
├── lib/auth.js        # /crud calls as alice, bob, carol or nobody (08); 401/403 are expected answers
├── scripts/crud/      # 01: smoke · steady · ramp · saturate · read-heavy · write-heavy · large-payload
├── scripts/bulk/      # 02: smoke · throughput · stream-vs-unary · batch-sweep · large-payload · keyset
├── scripts/query/     # 03: steady
├── scripts/people/    # 04: steady
├── scripts/xds/       # 05–07, 09: balance · failover
├── scripts/auth/      # 08, 09: mixed
├── probes/            # 01: bucket-latency.py, timings by bucket (plain Python, not k6; see "Probes")
└── results/           # git-ignored; one folder per run (see below)
```

## Script headers

`make select-load-test` reads these comment lines to decide which scripts it offers, and how to run them:

```js
// Description: Latency at a fixed, realistic rate: 1000 req/s of the full 13-op mix for 5m
// Examples: 01                               ← "all", or a list like "02, 11"
// Clients: native-java, grpc-java, grpc-go   ← clients the script can target
// Run: parallel                              ← default run style; RUN= overrides it
// Breakdown: mode=stream,unary×16,unary×1    ← optional: a tag whose values get their own table rows
// Events: 40s stop server1, 80s start server1 ← optional: docker compose actions while k6 runs
// Env: RATE=1000 DURATION=5m ...             ← knobs, passed with K6_ARGS="-e RATE=3000"
```

- **`Breakdown`:** the script tags its requests with this tag, and its thresholds put the tagged
  sub-metrics in the summary export. `bulk/*` scripts get entries/s per value. Other scripts get
  calls, failed share and GF p50/p99 per value, plus a "value vs first value" row.
- **`Events`:** `make select-load-test` runs each `docker compose <action> <service>` on the
  example's own services at that many seconds after k6 starts. Each one is logged to `events.log`,
  printed under the table, and added to the Grafana dashboard as an annotation. It waits for every
  event to finish before it reports, so a stopped server is always started again. If you interrupt
  a run, check `make status`.

Scripts that load the cluster as hard as they can (closed-loop VUs) default to `Run: sequential`,
so each client gets the cluster to itself. Light, fixed-rate scripts run all clients at once.

| Script | Executor | Default load | Run |
|---|---|---|---|
| `crud/smoke` | constant arrival rate | 100 req/s · 30 s | parallel |
| `crud/steady` | constant arrival rate | 1000 req/s · 5 m | parallel |
| `crud/ramp` | ramping arrival rate | 100 → 8000 req/s over 3 m, hold 30 s | sequential |
| `crud/saturate` | constant VUs, closed loop | 64 VUs · 2 m | sequential |
| `crud/read-heavy` | constant arrival rate | 90% Get / 10% Put · 2000 req/s · 3 m | parallel |
| `crud/write-heavy` | constant arrival rate | puts, conditional writes, removes · 2000 req/s · 3 m | parallel |
| `crud/large-payload` | constant arrival rate | 64 KiB values · 200 req/s · 2 m | parallel |

The full 13-op mix is `DEFAULT_MIX` in `lib/crud.js`. Override it with `K6_ARGS="-e MIX=Get=80,Put=20"`.

### Bulk scripts (02-bulk-streaming)

One bulk call moves `COUNT` entries in chunks of `BATCH`: one gRPC stream, or one native
putAll/getAll/removeAll per chunk. Each VU works on its own key range, and every iteration
puts, gets and removes the same keys, so the region stays the same size.

| Script | Load | Run |
|---|---|---|
| `bulk/smoke` | 1 VU: each op once in every mode, 1,000 entries | parallel |
| `bulk/throughput` | 4 VUs · put/get/remove of 10,000 entries in chunks of 500 · 2 m | sequential |
| `bulk/stream-vs-unary` | the same cycle of 2,000 entries as a stream, 16 unary calls in flight, then 1 at a time · 45 s each | sequential |
| `bulk/batch-sweep` | the throughput cycle at chunk sizes 10, 100, 500, 1000, 5000 · 30 s each | sequential |
| `bulk/large-payload` | 64 KiB values (chunks capped at ~1 MiB) · 1 m 30 s | sequential |
| `bulk/keyset` | 500,000 keys, then the whole key set over and over · 1 m | sequential |

Their table leads with **ENTRIES/S**, and CPU is per 1,000 entries. Scripts with a `Breakdown`
header add a second table with one row per client and tag value, plus "vs" rows within each
client (e.g. `grpc-java · unary×1 vs stream: 16× fewer`) and across clients. A Counter's rate in
k6 is over the whole test, so each slice records how long it ran (`bulk_slice_seconds`), and the
breakdown's entries/s is entries ÷ that time.

## What gets measured

| Measure | Source | Meaning |
|---|---|---|
| `http_req_duration` (E2E) | k6 | the whole HTTP request: what a caller of the service sees |
| `gemfire_call_duration` (GF) | client's `X-GemFire-Micros` header | the GemFire call alone (gRPC RPC or Region API) |
| CPU/REQ | cAdvisor through Prometheus | the client container's CPU-µs per request over the run |
| `bulk_entries` (ENTRIES/S) | client's `X-Entries` header | entries a bulk call moved (GetStream / Get: entries found) |
| CPU/1k | cAdvisor through Prometheus | bulk scripts: the client container's CPU-µs per 1,000 entries |

k6 pushes its metrics to the example's Prometheus as native histograms (remote write), tagged
with `client`, `run`, `example`, `script` and `op`. The example's dashboard has a k6 row and a
**load run** dropdown. Each run is also added to Grafana as an annotation (tag `load-test`).

## Results

Each run gets `results/<yyyymmdd-hhmmss>-<example>-<script>/`, containing:

- `summary.txt`: the comparison table
- `<client>.json`: the full k6 summary export for that client
- `<client>.log`: k6 output
- `meta.json`: example, script, clients, run style and k6 args
- a copy of the script that ran

A run exits non-zero if any client's k6 thresholds failed. The default threshold is under 1% failed requests.
If a client's k6 run never got to its scenario (for example, setup failed), its row shows dashes
and the reason, e.g. `FAIL (setup failed: preload failed: 404 ...)`, and its "vs" rows are left out.

## Probes

`probes/` holds measurements that don't fit a k6 run, as plain Python (standard library only).
They aren't in the `make select-load-test` menu and push no k6 metrics, so the dashboard's
load-run panels don't show them. The example must already be running.

- `probes/bucket-latency.py` (01: it needs the `/crud` API) times `Get` and `Put` per key
  through each client, one request at a time, and groups the timings by partitioned-region
  bucket. It shows what a gRPC client pays when its server has to forward to the bucket's owner,
  and that the native client doesn't pay it
  (see [01, finding 7](../01-crud-basics/README.md#findings-and-gotchas)). Like the crud
  scripts, it preloads keys `0..999` for each client into the `crud` region.

  ```bash
  python3 load-testing/probes/bucket-latency.py          # example 01, 8 rounds, about 20 s
  python3 load-testing/probes/bucket-latency.py 01 20    # example number, rounds
  ```
