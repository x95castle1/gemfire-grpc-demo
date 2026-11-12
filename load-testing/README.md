# load-testing/: k6 scripts shared by every example

Every example's clients are HTTP services with the same `/crud` API
(see [01-crud-basics](../01-crud-basics/README.md#http-api)), so one script can load-test any
example. The same `crud/steady.js` run against 01 (plaintext), 06 (TLS), 07 (mTLS) and
08 (tokens) shows what each feature costs while the workload stays the same.

```bash
make load                                   # fzf: example → script (preview) → client
make load EX=01 SCRIPT=crud/ramp            # no prompts
make load EX=01 SCRIPT=crud/steady CLIENT=grpc-java K6_ARGS="-e RATE=3000 -e DURATION=1m"
make load EX=01 SCRIPT=crud/steady RUN=sequential    # one client at a time instead of both at once
make results                                # browse past runs (or: make results RUN=latest)
```

`make load` starts the example if it isn't running. It then runs k6 (`grafana/k6:2.3.0`) as a
throwaway container on the example's Docker network: one k6 process per client, all at once
(`parallel`) or one after another (`sequential`). At the end it prints a comparison table.

## Layout

```
load-testing/
├── lib/crud.js        # shared helpers: op mix, /crud calls, preload, options, gemfire_call_duration trend
├── scripts/crud/      # smoke · steady · ramp · saturate · read-heavy · write-heavy · large-payload
└── results/           # git-ignored; one folder per run (see below)
```

## Script headers

`make load` reads these comment lines to decide which scripts it offers, and how to run them:

```js
// Description: Latency at a fixed, realistic rate: 1000 req/s of the full 13-op mix for 5m
// Examples: all                     ← "all", or a list like "02, 11"
// Clients: native-java, grpc-java   ← clients the script can target
// Run: parallel                     ← default run style; RUN= overrides it
// Env: RATE=1000 DURATION=5m ...    ← knobs, passed with K6_ARGS="-e RATE=3000"
```

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

## What gets measured

| Measure | Source | Meaning |
|---|---|---|
| `http_req_duration` (E2E) | k6 | the whole HTTP request: what a caller of the service sees |
| `gemfire_call_duration` (GF) | client's `X-GemFire-Micros` header | the GemFire call alone (gRPC RPC or Region API) |
| CPU/REQ | cAdvisor through Prometheus | the client container's CPU-µs per request over the run |

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
