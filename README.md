# GemFire for gRPC: feature tour

Standalone, Docker Compose–based examples that exercise every feature of
**[VMware Tanzu GemFire for gRPC 1.0](https://techdocs.broadcom.com/us/en/vmware-tanzu/data-solutions/tanzu-gemfire-for-grpc/1-0/gf-grpc/overview.html)**.
Each one compares the gRPC path with the **native GemFire Java client** doing the same work, and
uses Prometheus and Grafana for the comparison.

Every example folder is self-contained. It has its own `docker-compose.yml`, GemFire config,
clients, Prometheus config and Grafana dashboards, and always uses the same topology:
**1 locator + 2 servers**.

## Prerequisites

- Docker with Compose v2.17 or later (Docker Desktop is fine, Apple Silicon included)
- `fzf`, `jq` and `curl` for the make targets (`brew install fzf jq`)
- The licensed gRPC extension and `gemfire.proto` in [`gemfire-extensions/`](gemfire-extensions/README.md)
- A GemFire **10.3.2+** image (default `gemfire/gemfire:10.3.2-jdk17`)

You don't need a JDK, Gradle, Go or k6 on the host. Everything builds and runs inside Docker.

## Examples

| # | Example | gRPC features | Native Java counterpart | Grafana | Status |
|---|---|---|---|---|---|
| 01 | [crud-basics](01-crud-basics/) | All 13 unary RPCs from Java **and Go**; string/int32/int64 keys; every scalar `Value` type; direct `dns:///` connection | Region API (`put`, `putIfAbsent`, `replace`, `containsKeyOnServer`, `sizeOnServer`…) | [:3001](http://localhost:3001) | ✅ |
| 02 | [bulk-streaming](02-bulk-streaming/) | `PutStream`, `GetStream`, `RemoveStream`, `KeySetStream`; chunking, flow control, non-transactional failure; vs. one unary call per entry | `putAll`, `getAll`, `removeAll`, `keySetOnServer` (vs. `put`/`get`/`remove` per entry) | [:3002](http://localhost:3002) | ✅ |
| 03 | [oql-query](03-oql-query/) | `Query` from a **Go** client: parameters, `field_names`, aggregates, ORDER BY/LIMIT, batching, UNDEFINED, errors | none (Go only, by design) | [:3003](http://localhost:3003) | ✅ |
| 04 | [structured-values](04-structured-values/) | `google.protobuf.Any` values from a **Go** client, descriptor JAR deploy (`list protobuf-types`), OQL into nested, repeated and enum fields, NULL vs UNDEFINED, unregistered types | none (Go only, by design) | [:3004](http://localhost:3004) | ✅ |
| 05 | [xds-discovery](05-xds-discovery/) | The locator as xDS server; a **Go** client with a direct and an `xds:///` channel side by side: discovery, round robin, scripted failover (`Events`) | none (Go only, by design) | [:3005](http://localhost:3005) | ✅ |
| 06 | [tls](06-tls/) | One-way TLS from a **Go** client: certificates made at startup, `ssl-enabled-components=grpc`, direct TLS and xDS over TLS (locator-advertised), `openssl s_client` | none (Go only, by design) | [:3006](http://localhost:3006) | ✅ |
| 07 | mtls | Mutual TLS | Native mTLS | :3007 | planned |
| 08 | token-auth-rbac | Bearer tokens, `SecurityManager`, per-region `DATA:READ`/`WRITE` | `AuthInitialize` + same `SecurityManager` | :3008 | planned |
| 09 | secure-xds | xDS + mTLS + tokens | SSL + auth | :3009 | planned |
| 10 | error-handling | Every gRPC status code, retries, deadlines | Native exceptions | :3010 | planned |
| 11 | experiments | What drives the gRPC vs native gap: REPLICATE vs PARTITION (forwarding hop vs translation cost), a GemFire REST API baseline, sweeps across concurrency and payload size, server `worker-threads` tuning | full participant | :3011 | planned |

Native-only GemFire features (continuous queries, cache listeners, interest registration,
function execution) are **not** available over gRPC. Use the Java, C++ or .NET clients for those.

## Quick start

```bash
make                 # the menu
make start             # fzf-pick an example (or: make start EX=01)
make status            # what's running: containers, state, Grafana/Prometheus URLs
make logs              # fzf picker: choose containers, live preview, ENTER to follow
make select-load-test  # k6 load test: pick example → script → client, get a native-vs-gRPC table
make results           # browse past load-test runs
make stop              # stop everything that's running (or: make stop EX=01)
make verify            # smoke-test every example (all demo assertions must pass)
```

## Load testing

Every example's clients are HTTP services with the same `/crud` API, so the k6 scripts in
[`load-testing/`](load-testing/README.md) live at the root and run against any example. That
makes it easy to compare features with the workload held constant, for example plaintext (01)
vs. TLS (06) vs. tokens (08).

```bash
make select-load-test EX=01 SCRIPT=crud/steady                         # all clients at once, 1000 req/s each
make select-load-test EX=01 SCRIPT=crud/ramp                           # each client alone, 100 → 8000 req/s
make select-load-test EX=01 SCRIPT=crud/steady K6_ARGS="-e RATE=3000"  # override a script's knobs
```

Each run prints end-to-end latency (what k6 saw) next to the time inside the GemFire call
(reported by the client) and CPU per request, then says in words how each gRPC client compares
with native, e.g. `grpc-java vs native: 73% slower` or, from 2× up, `3.0× slower`. It saves everything under
`load-testing/results/` and adds an annotation to the example's Grafana dashboard.

## Conventions used by every example

- **Clients are HTTP services** (`:8080` in the container, `81NN`/`82NN`/`83NN` on the host). The
  Java clients are plain Spring Boot apps (Spring MVC on Tomcat, no Spring Data for GemFire) that
  share one `@RestController`, so the HTTP layer is identical on both sides of a comparison. Each
  runs a narrated demo with assertions at startup (`POST /demo` re-runs it; 200 means everything
  passed) and serves the example's API for load tests (`/crud/*` in 01, `/bulk/*` in 02).
- **The same metric names in every client**, with buckets from 50 µs to 2.5 s (to 60 s in 02):
  - `demo_op_latency_seconds{client,region,op,code}`: one API call's GemFire work,
    failures included (`code` is `OK`, a gRPC status code, or a native exception class).
  - `demo_entries_total{client,region,op}` (02): entries a bulk call moved.
  - `demo_rpc_duration_seconds{client,region,method,code}`: every gRPC call, streaming ones
    included, recorded by a gRPC client interceptor (gRPC clients only).
- **Colors** follow the client in every dashboard: native-java is blue, grpc-java is orange, and
  grpc-go is aqua.
- **Ports**: example *NN* uses Grafana `30NN`, Prometheus `90NN`, grpc-java `81NN`,
  native-java `82NN` and grpc-go `83NN`, so examples can run side by side.
