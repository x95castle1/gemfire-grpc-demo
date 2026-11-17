# 11 · Experiments: what drives the gap between native and gRPC

[01](../01-crud-basics/) measured the gap: a GemFire call over gRPC takes 2.4–6× as long as over the
native Java client. It also gave reasons. For every gRPC call, server1:
- **translates:** decodes protobuf and converts it to Java objects
- **forwards:** sends about half the calls on to server2, because a gRPC client can't send a key to the server that owns it

This example tests those reasons. The same load runs on two clients:
- **native-java:** single-hop, straight to the server that owns each key
- **grpc-go:** `dns:///server1:50051`, everything to server1

There are four experiments, each isolating one cause:

| Experiment | The question | Answer, in one line |
|---|---|---|
| [REPLICATE vs PARTITION](#1-replicate-vs-partition-forwarding) | How much of the gap is forwarding? | about an eighth |
| [Concurrency](#2-concurrency-how-the-gap-grows-with-callers) | How does the gap change with more callers? | it grows from 0.13 ms to 1.2 ms |
| [Payload size](#3-payload-size-per-call-vs-per-byte) | Is it a cost per call or per byte? | mostly per call; large values add a per-byte cost |
| [worker-threads](#4-worker-threads-is-the-servers-thread-pool-the-limit) | Is the gRPC server's thread pool the limit? | not at its default; too few threads hurt |

The clients are 01's, without grpc-java. 01 showed grpc-java and grpc-go within 3–15% of each other
on the GemFire call, so one gRPC client is enough.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=11                                                   # cluster, both clients (demo at startup), monitoring
make select-load-test EX=11 SCRIPT=experiments/region-type         # ~4 min
make select-load-test EX=11 SCRIPT=experiments/concurrency         # ~6 min
make select-load-test EX=11 SCRIPT=experiments/payload             # ~7 min
./11-experiments/worker-threads.sh                                 # ~12 min: restarts the servers per setting
open http://localhost:3011
make stop EX=11
```

Smoke test: `make verify EX=11`.

**Warm the stack up first.** The first load run after `make start` measures JIT warm-up as much
as anything; 01 saw p99s up to 16× higher. The numbers below come after a short throwaway run
(`make select-load-test EX=11 SCRIPT=experiments/concurrency K6_ARGS="-e DURATION=10s"`).

**Each experiment runs its steps one after another, one client at a time,** like 02's batch sweep:
- every step (region, number of callers, value size) gets the cluster to itself
- `load.sh` prints a table with a row per step, then "step vs first step" rows within a client, and "grpc-go vs native" rows per step

## What's set up

| Piece | Where |
|---|---|
| `crud-partition` (PARTITION, as 01's `crud`) and `crud-replicate` (REPLICATE: every server holds every key) | [`gemfire/init.gfsh`](gemfire/init.gfsh) |
| `?region=partition\|replicate` on every `/crud` op, default `partition` | both clients (`Settings.regionFor`, `Config.regionFor`) |
| `/crud/preload?start=`, so the payload sweep loads its own key range per value size | both clients |
| `gemfire.grpc.worker-threads` from `GRPC_WORKER_THREADS` (empty = the default) | [`.env`](.env), [`start-member.sh`](gemfire/start-member.sh) |
| The experiment scripts | [`../load-testing/scripts/experiments/`](../load-testing/scripts/experiments/) |
| The worker-threads runner | [`worker-threads.sh`](worker-threads.sh) |

The demo is 01's, plus section 16, which does the same Put and Get on both region types.

## 1. REPLICATE vs PARTITION: forwarding

`experiments/region-type`: 16 callers in a closed loop, 01's 13-op mix, 45 s on `crud-partition`, then
45 s on `crud-replicate`.

**Why REPLICATE isolates forwarding:**
- **On PARTITION:** server1 owns about half the keys, and forwards the gRPC client's calls for the rest to server2.
- **On REPLICATE:** server1 holds every key, so a gRPC Get never leaves it.
- **Native never forwards,** in either case.

```
CLIENT · region                      │    CALLS     REQ/S   FAILED │      GF p50         p99
                                     │                             │        (ms)        (ms)
native-java · partition              │  1431106   31802.4    0.00% │       0.089       0.611
native-java · replicate              │  1353643     30081    0.00% │       0.115       0.719
native-java · replicate vs partition │           5% fewer          │  29% slower  18% slower
grpc-go · partition                  │   931486   20699.7    0.00% │       0.480       1.292
grpc-go · replicate                  │   971541   21589.8    0.00% │       0.459       1.145
grpc-go · replicate vs partition     │            4% more          │   4% faster  11% faster
grpc-go vs native · partition        │          35% fewer          │ 5.4× slower 2.1× slower
grpc-go vs native · replicate        │          28% fewer          │ 4.0× slower  59% slower
```

**Get, the cleanest comparison** (p50 of the GemFire call, from the same run):

| Region | native-java | grpc-go | Gap |
|---|---|---|---|
| PARTITION | 0.073 ms | 0.472 ms | 0.40 ms |
| REPLICATE | 0.073 ms | 0.423 ms | 0.35 ms |

- **Forwarding is about an eighth of the gap.** Removing it saved grpc-go 0.05 ms per Get. The other
  0.35 ms is the gRPC path itself.
- **The whole mix overstates the change.** It goes from 5.4× to 4.0× mostly because *native* got slower:
  on REPLICATE every write goes to both servers, for both clients. Native's Put went from 0.078 to 0.177 ms.
- **Forwarding really stops on REPLICATE** (rates at the middle of each step):

  | grpc-go on | Gets forwarded by server1 | writes forwarded | Gets on server1 | Gets on server2 |
  |---|---|---|---|---|
  | PARTITION | 5,033/s | 4,374/s | 4,926/s | 5,047/s |
  | REPLICATE | 0 | 0 | 10,382/s | 0 |

  Native forwarded nothing on either region type: single-hop sends each key to its owner.

## 2. Concurrency: how the gap grows with callers

`experiments/concurrency`: 1, 4, 16 and 64 callers in a closed loop, 01's mix on `crud-partition`, 30 s each.

```
CLIENT · vus                         │    CALLS     REQ/S   FAILED │      GF p50         p99
                                     │                             │        (ms)        (ms)
native-java · 1                      │   132030      4401    0.00% │       0.065       0.196
native-java · 4                      │   510732   17024.4    0.00% │       0.067       0.221
native-java · 16                     │  1024662   34155.4    0.00% │       0.085       0.546
native-java · 64                     │  1443744   48124.8    0.00% │       0.124       1.189
grpc-go · 1                          │    95349    3178.3    0.00% │       0.196       0.357
grpc-go · 4                          │   348618   11620.6    0.00% │       0.204       0.531
grpc-go · 16                         │   627493   20916.4    0.00% │       0.477       1.257
grpc-go · 64                         │   964539   32151.3    0.00% │       1.284       3.755
grpc-go vs native · 1                │          28% fewer          │ 3.0× slower  82% slower
grpc-go vs native · 4                │          32% fewer          │ 3.0× slower 2.4× slower
grpc-go vs native · 16               │          39% fewer          │ 5.6× slower 2.3× slower
grpc-go vs native · 64               │          33% fewer          │  10× slower 3.2× slower
```

**Alone, the gRPC path costs 0.13 ms per call.** With one caller there's no queueing anywhere, and grpc-go's
call takes 0.196 ms against native's 0.065 ms. That matches 01's bucket probe: about 80 µs of gRPC path, plus
a forward on half the keys.

**Under load the gap grows, because all the gRPC work lands on one server.**

| | 1 caller | 64 callers | Growth |
|---|---|---|---|
| native-java call | 0.065 ms | 0.124 ms | 1.9× |
| grpc-go call | 0.196 ms | 1.284 ms | 6.6× |
| The gap | 0.13 ms | 1.16 ms | |

- **Native spreads its calls over both servers.**
- **grpc-go queues every call on server1,** which does all the translation and the forwarding. Section 4 shows server1's CPU.

**Throughput is 28–39% lower at every level,** much less than the call-time gap. Each request also
pays for HTTP and k6, the same for both clients. At these rates that cost, not GemFire, limits
requests per second.

## 3. Payload size: per call vs per byte

`experiments/payload`: values of 100 B, 1 KiB, 10 KiB, 100 KiB and 1 MiB, 8 callers, Get 80% / Put 20%
on `crud-partition`, 30 s each. Each size has its own key range, preloaded with that size (at most 50 MiB per size).

```
CLIENT · size                        │    CALLS     REQ/S   FAILED │      GF p50         p99
                                     │                             │        (ms)        (ms)
native-java · 100                    │   789686   26322.9    0.00% │       0.075       0.235
native-java · 1024                   │   784153   26138.4    0.00% │       0.076       0.253
native-java · 10240                  │   760569   25352.3    0.00% │       0.084       0.274
native-java · 102400                 │   540792   18026.4    0.00% │       0.173       0.524
native-java · 1048576                │   143989    4799.6    0.00% │       1.139      11.340
grpc-go · 100                        │   489990     16333    0.00% │       0.298       0.751
grpc-go · 1024                       │   491737   16391.2    0.00% │       0.300       0.714
grpc-go · 10240                      │   462037   15401.2    0.00% │       0.324       0.785
grpc-go · 102400                     │   274134    9137.8    0.00% │       0.646       1.722
grpc-go · 1048576                    │    57659      1922    0.00% │       3.324      10.760
grpc-go vs native · 100              │          38% fewer          │ 4.0× slower 3.2× slower
grpc-go vs native · 1024             │          37% fewer          │ 3.9× slower 2.8× slower
grpc-go vs native · 10240            │          39% fewer          │ 3.9× slower 2.9× slower
grpc-go vs native · 102400           │          49% fewer          │ 3.7× slower 3.3× slower
grpc-go vs native · 1048576          │          2.5× fewer          │ 2.9× slower   5% faster
```

- **Up to 10 KiB, the size doesn't matter.** Both clients' calls take the same time at 100 B, 1 KiB and 10 KiB.
  The gap stays at about 4× (0.22 ms) because it's a cost per call.
- **From 100 KiB a cost per byte appears.** grpc-go's extra time grows from 0.22 ms (100 B) to 0.47 ms (100 KiB)
  and 2.19 ms (1 MiB): roughly 2 ms per extra MiB. Every value is copied into a protobuf message on
  the client, out of it on server1, and half the Puts are forwarded on to server2.
- **At 1 MiB:** native moved 4,800 values/s (5.0 GB/s), grpc-go 1,922 (2.0 GB/s). The p99s are equal,
  because big values make both clients' tails long.

## 4. worker-threads: is the server's thread pool the limit?

**What the setting is.** The gRPC server runs calls on a pool named `grpc-worker-*`
(`GrpcWorkerExecutorFactory`, read from the extension's bytecode):

| Property | Default |
|---|---|
| `gemfire.grpc.worker-threads` | 2 × the CPUs the server sees (24 here) |
| `gemfire.grpc.worker-threads-core` | 1 |
| `gemfire.grpc.worker-idle-timeout-s` | 1800 |

Calls beyond the limit wait for a thread. It's a system property, read at startup.

**The experiment.** [`worker-threads.sh`](worker-threads.sh) restarts server1 and server2 for each
setting, then runs `experiments/worker-threads` with grpc-go: 64 callers in a closed loop, 01's mix, 1 minute.
Native runs once at the end, as the reference.

```
━━ 11-experiments · gemfire.grpc.worker-threads (experiments/worker-threads: 64 VUs, 1 minute each) ━━
worker-threads         CLIENT            REQ/S  FAILED    GF p50    GF p99  server1    grpc-
                                                            (ms)      (ms)  (cores)  workers
4                      grpc-go         20914.1      0%     2.443     5.999     1.70        4
8                      grpc-go         24811.9      0%     1.948     5.238     2.17        8
24                     grpc-go         33076.6      0%     1.217     4.214     2.19       24
96                     grpc-go         33464.2      0%     1.171     4.303     2.46       64
default (2 × 12 CPUs)  grpc-go         32739.4      0%     1.231     4.262     2.47       24
(not used by native)   native-java     44600.2      0%     0.122     1.343     0.93        –
```

- **The default is enough.** With 96 allowed, 64 callers made the server create 64 threads, yet it ran
  only 1–2% faster than with the default 24.
- **Too few threads hurt.** With 4, grpc-go lost 36% of its throughput and its calls took twice as long.
  Don't set it below the default.
- **The real limit is server1's work.** grpc-go kept server1 at 2.2–2.5 cores for 33,000 calls/s. Native
  kept it at 0.9 cores for 44,600 calls/s, and shared the work with server2. The calls wait for server1's
  CPU, not for its threads.

## Attributing the gap

For a Get with 16 callers, the gap is 0.40 ms (0.073 vs 0.472). Where it goes:

| Cause | Share | How we know |
|---|---|---|
| Forwarding to server2 | ~0.05 ms (an eighth) | REPLICATE removes it (experiment 1) |
| The gRPC path itself (HTTP/2, protobuf, conversion), with no queueing | ~0.08–0.13 ms | the one-caller gap (experiment 2), 01's bucket probe |
| Queueing on server1, which does every gRPC call's work | the rest, ~0.2 ms, growing with load | the gap grows with callers (2); server1's CPU (4) |
| The server's thread pool | none at the default | worker-threads (4) |
| Value size | none up to 10 KiB; about 2 ms per extra MiB | payload (3) |

**The biggest lever isn't in the protocol.** It's that one server does all the work. xDS
([05](../05-xds-discovery/)) spreads gRPC calls across the servers, but it isn't partition-aware, so it
removes the queueing on one server, not the forwarding.

## Findings

1. **Forwarding is a small part of the gap.** Making every key local (REPLICATE) took 0.05 ms off a
   0.40 ms gap. The client not being partition-aware isn't the main cost.
2. **The gap is a cost per call that grows with load.** It's 0.13 ms alone, 0.40 ms at 16 callers and
   1.16 ms at 64, because every gRPC call is queued and translated on server1.
3. **Small values don't change it; big ones add to it.** The gap stays flat from 100 B to 10 KiB.
   It grows by roughly 2 ms per extra MiB.
4. **`worker-threads` is fine at its default** (2 × CPUs). Raising it barely helps; lowering it costs up to a third of the throughput.
5. **REPLICATE costs native writes.** Native's Puts took 2.3× as long on REPLICATE as on PARTITION
   (no redundancy), because every write goes to both servers. Compare region types per operation, not over a mix.
6. **Requests/s understates the gap.** With HTTP and k6 in front of both clients, grpc-go managed 28–39% fewer
   requests per second while its GemFire calls took 3–10× as long.

**Caveats:**
- Everything ran on one laptop (Docker with 12 CPUs): k6, both clients, the cluster and monitoring share the CPUs.
- Treat the numbers as relative between the clients, not absolute.
- Each step ran for 30–45 s, enough for the trends, not for small differences: a few percent either way is noise.

## See it in Grafana

The dashboard (`:3011`) shows every experiment over time. Each step is a plateau, and the load-test
annotations name each run. Narrow the time range to one run to read it.
- **What the clients see:** calls/s, GemFire call p50 and p99, by client and region.
- **Forwarding:**
  - what server1 sends on to server2: `GetMessage`s, and Puts via `putRemoteCompleted`
  - Gets per server per region
- **Server work:** CPU by container, members up (the worker-threads restarts), gRPC connections.
- **Bytes on the wire:** network traffic per container, during the payload sweep.

## Layout

```
11-experiments/
├── .env                      # image, extension paths, ports (3011/9011/8211/8311), GRPC_WORKER_THREADS
├── docker-compose.yml        # locator, server1, server2, gfsh-init, native-client-java, grpc-client-go, prometheus, grafana, cadvisor
├── worker-threads.sh         # restarts the servers per worker-threads setting and runs experiments/worker-threads
├── gemfire/
│   ├── start-member.sh       # as 01, plus gemfire.grpc.worker-threads when GRPC_WORKER_THREADS is set
│   └── init.gfsh             # crud-partition, crud-replicate, demo-grpc-go, demo-native
├── grpc-client-go/           # 01's client: ?region=, preload ?start=, demo section 16
├── native-client-java/       # 01's client: one PROXY region per region type, ?region=, preload ?start=, demo section 16
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/experiments.json
        └── provisioning/
```

Scripts: [`load-testing/scripts/experiments/`](../load-testing/scripts/experiments/) (`region-type`,
`concurrency`, `payload`, `worker-threads`), with [`lib/experiments.js`](../load-testing/lib/experiments.js)
and [`lib/slices.js`](../load-testing/lib/slices.js).
