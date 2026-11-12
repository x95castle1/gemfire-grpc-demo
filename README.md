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
- [`fzf`](https://github.com/junegunn/fzf) for `make logs` (`brew install fzf`)
- The licensed gRPC extension and `gemfire.proto` in [`vendor/`](vendor/README.md)
- A GemFire **10.3.2+** image (default `gemfire/gemfire:10.3.2-jdk17`)

You don't need a JDK, Gradle or Go on the host. Everything builds inside Docker.

## Examples

| # | Example | gRPC features | Native Java counterpart | Grafana | Status |
|---|---|---|---|---|---|
| 01 | [crud-basics](01-crud-basics/) | All 13 unary RPCs; string/int32/int64 keys; every scalar `Value` type; direct `dns:///` connection | Region API (`put`, `putIfAbsent`, `replace`, `containsKeyOnServer`, `sizeOnServer`…) | [:3001](http://localhost:3001) | ✅ |
| 02 | bulk-streaming | `PutStream`, `GetStream`, `RemoveStream`, `KeySetStream` | `putAll`, `getAll`, `removeAll`, `keySetOnServer` | :3002 | planned |
| 03 | oql-query | `Query`: parameters, `field_names`, batching, NULL/UNDEFINED | `QueryService` | :3003 | planned |
| 04 | structured-values | `google.protobuf.Any`, descriptor JAR deploy, OQL into fields; Go writes and Java reads | PDX | :3004 | planned |
| 05 | xds-discovery | Locator xDS, round-robin, failover (Java + Go) | Locator pool, single-hop | :3005 | planned |
| 06 | tls | One-way TLS | Native SSL | :3006 | planned |
| 07 | mtls | Mutual TLS | Native mTLS | :3007 | planned |
| 08 | token-auth-rbac | Bearer tokens, `SecurityManager`, per-region `DATA:READ`/`WRITE` | `AuthInitialize` + same `SecurityManager` | :3008 | planned |
| 09 | secure-xds | xDS + mTLS + tokens | SSL + auth | :3009 | planned |
| 10 | error-handling | Every gRPC status code, retries, deadlines | Native exceptions | :3010 | planned |
| 11 | benchmark-showdown | native-java vs grpc-java vs grpc-go, swept across concurrency, payload size and server tuning | full participant | :3011 | planned |
| 12 | go-client | Idiomatic Go across all features | – | :3012 | planned |

Native-only GemFire features (continuous queries, cache listeners, interest registration,
function execution) are **not** available over gRPC. Use the Java, C++ or .NET clients for those.

## Quick start

```bash
make start           # fzf-pick an example (or: make start EX=01, or cd 01-* && docker compose up -d --build)
make status          # what's running: every example's containers, state, Grafana/Prometheus URLs
make logs            # fzf picker: choose containers (TAB = several), live preview, ENTER to follow
make stop            # stop everything that's running (or: make stop EX=01)
make verify          # smoke-test every example (demo assertions must all pass)
```

## Conventions used by every example

- **Clients** read their settings from environment variables. `MODE=demo` runs a narrated tour
  with assertions and exits non-zero on failure. `MODE=bench` runs a rate-limited load and
  exports metrics. `MODE=all` does both.
- **The same metric names in every client**: `demo_op_latency_seconds{client,op}` (histogram),
  `demo_errors_total{client,op,code}` and `demo_checks_total{client,result}`.
- **Colors** follow the client in every dashboard: native-java is blue, grpc-java is orange, and
  grpc-go is aqua.
- **Ports**: example *NN* serves Grafana on `30NN` and Prometheus on `90NN`, so examples can run
  side by side.
