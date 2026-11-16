# 05 · xDS discovery: one client, two ways in (Go)

In 01–04 every gRPC client dialed `dns:///server1:50051`:
- **One server takes everything.** server1 handles every call and does all the forwarding.
- **It's a single point of failure.** If server1 stops, the client has nowhere to go.

GemFire for gRPC fixes this with **xDS**. The gRPC extension on the **locator** is also an
xDS control plane. A client that dials `xds:///gemfire_grpc` asks it which gRPC servers exist,
round-robins its calls across them, and stops using a server that goes away.

This example has **one Go client with two channels to the same cluster**, so you can watch them
side by side:

| Channel | Target | Reaches |
|---|---|---|
| `direct` | `dns:///server1:50051` | server1 only, as in 01–04 |
| `xds` | `xds:///gemfire_grpc` | every server the locator reports (server1 and server2) |

```
                                   ┌── direct  dns:///server1:50051 ───────────────▶ server1
 k6 ──HTTP──▶ grpc-client-go :8080 ┤
   (?via=direct|xds)               └── xds  xds:///gemfire_grpc ──▶ round robin ──▶ server1, server2
                                          │  bootstrap: GRPC_XDS_BOOTSTRAP
                                          └─ "which servers?" ──▶ locator :50051 (xDS server)
```

Like 03 and 04, it's Go only by design: there's no native or Java client to compare with.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=05             # cluster, the Go client (demo at startup), monitoring
make logs EX=05              # read the demo (grpc-client-go)
make stop EX=05
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=05`.

## See it: balanced vs not balanced

The demo checks the balance in its log. To **watch** it, put some traffic on both channels and
open the dashboard:

```bash
make select-load-test EX=05 SCRIPT=xds/balance     # 1 minute, 20 req/s on each channel
open http://localhost:3005                          # top row: "Balanced vs not balanced"
```

| Panel | direct (not balanced) | xds (balanced) |
|---|---|---|
| Share of calls per server | server1 100% | server1 50%, server2 50% |
| Calls/s by server | one band, all server1 | two equal bands |
| gRPC connections per server | server1 holds 2 (one per channel), server2 holds 1 (xds only) | |

Then take a server away:

```bash
make select-load-test EX=05 SCRIPT=xds/failover    # 2 minutes; server1 stops at 40 s and starts at 80 s
```

The **Failover** row shows the outage:
- **direct** turns into a red "none" band of UNAVAILABLE.
- **xds** moves everything to server2, then splits again once server1 is back.

The stop and start are annotated on every panel. Without k6, a few curls show the same thing:

```bash
for i in 1 2 3 4; do curl -s 'localhost:8305/crud/get?key=1&via=xds' | jq -r .server; done      # server2 server1 server2 server1
for i in 1 2 3 4; do curl -s 'localhost:8305/crud/get?key=1&via=direct' | jq -r .server; done   # server1 server1 server1 server1
```

## Setting up xDS

**The cluster:**
- Nothing extra. Every member runs the gRPC extension with `gemfire.grpc.bind-address` and
  `gemfire.grpc.advertised-address` set, as in every example (`gemfire/start-member.sh`).
- The locator's endpoint, `locator:50051`, is the xDS server.
- It advertises the servers by their `advertised-address`, here `server1:50051` and `server2:50051`.

**The client** needs three things:

1. **A bootstrap file** that tells grpc-go where the xDS server is,
   [`grpc-client-go/xds-bootstrap.json`](grpc-client-go/xds-bootstrap.json), named by `GRPC_XDS_BOOTSTRAP`:
   ```json
   {
     "xds_servers": [{ "server_uri": "locator:50051", "channel_creds": [{ "type": "insecure" }], "server_features": ["xds_v3"] }],
     "node": { "id": "grpc-client-go", "cluster": "gemfire" }
   }
   ```
2. **The xDS resolver,** registered by a blank import: `_ "google.golang.org/grpc/xds"`. It pulls in a lot
   of dependencies (Envoy's API types among them, see `go.mod`).
3. **The target** `xds:///gemfire_grpc`, instead of `dns:///server1:50051`. Nothing else in the code changes.

## HTTP API

The same `/crud` API as 01, with one extra parameter and two extra outputs:
- **`via=direct|xds`** (default `xds`) picks the channel.
- **`server`** in the JSON says which server answered. The client reads the call's peer address and maps it to server1 or server2.
- The **`X-Server`** header carries the same thing.

```bash
curl 'localhost:8305/crud/get?key=1&via=xds'     # {"op":"Get","via":"xds","server":"server2",...}, then server1, then server2...
curl 'localhost:8305/crud/get?key=1&via=direct'  # always "server":"server1"
```

Metrics:
- `demo_calls_total{channel, server, code}` counts every gRPC call by channel and the server that
  answered. `server="none"` means the call never reached one.
- `demo_op_latency_seconds` and `demo_rpc_duration_seconds` carry a `channel` label.

## The narrated demo

| # | Section | What it shows |
|---|---|---|
| 1 | Two channels | both targets, and the bootstrap file that points grpc-go at the locator |
| 2 | Discovery | 40 Gets over `direct` all reach server1; 40 over `xds` reach both servers |
| 3 | Round robin | consecutive `xds` calls strictly alternate: exactly 20/20 |
| 4 | One cluster | a value Put over `direct` reads back over `xds` from both servers |
| 5 | Not partition-aware | Round robin doesn't pick the key's owner. With a redundant copy on each server Gets are local, but about half the Puts are forwarded to the primary. |

Failover needs a server to stop, so it's a load test rather than part of the demo.

## Failover

`make select-load-test EX=05 SCRIPT=xds/failover` runs 20 req/s over **each** channel for 2
minutes. It also stops server1 at 40 s and starts it again at 80 s. The `// Events:` header in the
script tells `load.sh` to do that, and annotates the dashboard. The regions are
`PARTITION_REDUNDANT`, so server2 holds a copy of every bucket and no data is lost while server1 is down.

```
By via: rows compare each via with direct (all ran at the same time)
CLIENT · via                         │    CALLS   FAILED
grpc-go · direct                     │     2401   37.23%
grpc-go · xds                        │     2401    0.00%

Events:
  13:44:39  +40s  stop server1
  13:45:18  +80s  start server1
```

What happened, second by second, from a probe with 5 Gets per channel per second:
- **server1 stops.**
  - xds: only a call **in flight** on server1 at that moment fails (UNAVAILABLE). Every call after it goes to server2. At 20 req/s none happened to be in flight; at 100 req/s it was 4 out of 12,000.
  - direct: every call fails with UNAVAILABLE, because it has no other server.
- **server1 starts.** xds begins using server1 again as soon as its gRPC endpoint is up (about 3 s after the container starts), back to a 50/50 split.
- **direct recovers about 11 s later than xds.** grpc-go reconnects to a lost server with exponential
  backoff, while xds already had a healthy server and rebalanced as soon as server1 reappeared.

## Findings

1. **xDS turns a server failure into a blip.** The xds channel lost nothing, or only the calls in
   flight at the moment server1 stopped. The direct channel lost 37%: everything for the 40 s
   server1 was down, plus the time it took to reconnect.
2. **Round robin is strict and not partition-aware.** Calls alternate server1, server2, server1, …
   regardless of the key. In this cluster every server holds every bucket (one redundant copy on
   two servers), so Gets are answered locally wherever they land. Puts must go to the bucket's
   primary, so about half are forwarded. xDS doesn't remove the forwarding hop; it shares it
   between the servers. The native client's single-hop does remove it, by sending each key to its owner.
3. **The client keeps working without the locator, but it stops learning.** With the locator
   stopped, the xds channel kept round-robining over the servers it already knew. It also routed
   around server2 when that stopped. But server2 wasn't used again after it restarted, not until
   the locator came back: the client only learns about (re)started servers from the xDS server.
   Run more than one locator in production.
4. **Two channels, two connections on server1.** The dashboard shows server1 holding 2 gRPC
   connections (direct and xds) and server2 holding 1. Each channel is one HTTP/2 connection per server.

**A note on speed.** This example isn't about speed, and the two channels are close. One thing
might surprise you on a fresh cluster: in 01–04 server2 never served a gRPC call. Its gRPC code
is still being JIT-compiled when xds starts sending it half the traffic, so the first runs show
xds slower than direct; one run measured 56% slower at p50. After a few minutes of load both
servers answer Gets in about 0.6 ms. The latency panels are in the dashboard's collapsed
"Performance" row.

## What to look for in Grafana

The dashboard (`:3005`) follows the **load run** dropdown. Load runs and failover events are annotated.
- **Balanced vs not balanced** (top row):
  - each channel's share of calls per server: direct 100% server1; xds 50/50
  - calls/s by server for each channel
  - gRPC connections per server
  - Puts forwarded to the primary, which shows xds isn't partition-aware
- **Failover:**
  - members up (server1's outage)
  - failed calls by channel and code (direct's UNAVAILABLE)
  - the share of failed k6 requests per channel
- **Collapsed:** Performance (k6 latency by channel), and GemFire servers and resource cost (gets per server, CPU, heap).

## Layout

```
05-xds-discovery/
├── .env                      # image, extension paths, ports (3005/9005/8305)
├── docker-compose.yml        # locator (xDS server), server1, server2, gfsh-init, grpc-client-go, prometheus, grafana, cadvisor
├── gemfire/
│   ├── start-member.sh       # every member: gRPC extension, bind + advertised address
│   └── init.gfsh             # PARTITION_REDUNDANT regions xds and demo-grpc-go
├── grpc-client-go/
│   ├── xds-bootstrap.json    # points grpc-go at locator:50051
│   └── *.go                  # main (two channels) · servers (peer → server1/2) · crud · api · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/xds-discovery.json
        └── provisioning/
```
