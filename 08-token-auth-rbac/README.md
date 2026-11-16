# 08 · Tokens and permissions (Go)

GemFire for gRPC authenticates each call with a **bearer token**, and authorizes it with GemFire's
**permissions**. The decisions are made by a `SecurityManager`, a Java class you deploy to the cluster:
- **No valid token** → `UNAUTHENTICATED`.
- **A valid token without the permission** → `PERMISSION_DENIED`.

This example's `SecurityManager` accepts **signed JWTs** whose `perms` claim lists GemFire
permissions. The Go client acts as four different callers on one connection:

| User | Token `perms` | Gets |
|---|---|---|
| `alice` | `DATA:READ`, `DATA:WRITE` | everything, in every region |
| `bob` | `DATA:READ` | reads; every write → `PERMISSION_DENIED` |
| `carol` | `DATA:READ:orders`, `DATA:WRITE:orders` | the `orders` region; `customers` → `PERMISSION_DENIED` |
| `none` | no token | `UNAUTHENTICATED` |

**Plaintext on purpose.** It keeps the example about authentication and authorization. A bearer
token works like a password for as long as it's valid, so in real use send it only over TLS. 09
puts xDS, mutual TLS and tokens together. Go only, like 03–07.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=08             # builds the member image (GemFire + the SecurityManager), cluster, Go client (demo at startup)
make logs EX=08              # read the demo (grpc-client-go); server1 logs each denial
make select-load-test EX=08 SCRIPT=auth/mixed      # 1 minute of all four users, then open the dashboard
open http://localhost:3008
make stop EX=08
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=08`.

```bash
curl 'localhost:8308/crud/get?key=1&user=bob'                       # 200
curl -X POST 'localhost:8308/crud/put?key=1&user=bob'               # 403 PERMISSION_DENIED: bob not authorized for DATA:WRITE:orders:grpc-go:1
curl 'localhost:8308/crud/get?key=1&user=carol&region=customers'    # 403
curl 'localhost:8308/crud/get?key=1&user=none'                      # 401 UNAUTHENTICATED: Missing or invalid token
```

## How a call is checked

```
client ──gRPC call + "authorization: Bearer eyJ…"──▶ server1
          SecurityManager.authenticate({security-token: "eyJ…"}) → User(bob, [DATA:READ])     or UNAUTHENTICATED
          SecurityManager.authorize(bob, DATA:WRITE:orders:grpc-go:1) → false                → PERMISSION_DENIED
```

1. The client puts the token in the call's metadata. Each call carries its own token, so one connection serves every user.
2. GemFire hands it to `authenticate(Properties)` as `security-token` (`SecurityManager.TOKEN`). The
   object `authenticate` returns becomes the principal.
3. GemFire asks `authorize(principal, permission)` for the permission the RPC needs, scoped to the
   region **and key**:

   | RPC | Needs |
   |---|---|
   | Get, ContainsKey, EntryCount, GetStream, KeySetStream, Query | `DATA:READ` (Query: on each region in FROM) |
   | Put, PutStream, RemoveStream, PutIfAbsent, Replace, ReplaceIf, Remove, RemoveIf | `DATA:WRITE` |
   | GetAndPut, GetAndRemove, GetAndReplace, PutIfAbsentAndGet | both |

## The SecurityManager

[`security/src/demo/security/JwtSecurityManager.java`](security/src/demo/security/JwtSecurityManager.java)
is under 200 lines, with no dependencies beyond GemFire.

**`authenticate`:**
- **A token:**
  - check the HS256 signature (`javax.crypto.Mac`) and `exp`
  - read `sub` and `perms`
  - turn each permission string (`DATA:READ:orders`) into a `ResourcePermission`
- **A username and password:** the cluster's own users (below).

**`authorize`:** whether any of the user's permissions `implies()` the one requested. GemFire's own
wildcard rules do the work, so `DATA:READ` covers every region and `DATA:READ:orders` covers every key in `orders`.

**Rejections are logged with the reason** ("Expired token for bob", "bad signature"). The caller
only ever sees `Missing or invalid token`, and GemFire itself logs just `Rejected gRPC call:
authentication failed`. Denials are logged too: `Denied bob DATA:WRITE:orders:grpc-go:1 (has [DATA:READ])`.

**How it's deployed:**
- [`security/Dockerfile`](security/Dockerfile) compiles the class inside the GemFire image (which has a JDK), using GemFire's jars.
- Every member runs that image.
- [`start-member.sh`](gemfire/start-member.sh) adds `--classpath=/security/token-security.jar` and
  `--security-properties-file=/scripts/gfsecurity.properties`.
- [`gemfire/gfsecurity.properties`](gemfire/gfsecurity.properties) names the class.

**The key is shared, for the demo only.** The JWTs are HMAC-signed with `JWT_SECRET` (`.env`),
which both the servers and the client have, because the client also plays the identity provider.
In real use the identity provider signs tokens (RS256 or ES256) and the cluster holds only its public key.

### One SecurityManager guards the whole cluster

Turning it on for gRPC turns it on for everything. So it also has three username/password users:

| User | Permissions | Used by |
|---|---|---|
| `cluster` | `CLUSTER:*` | members joining (`security-username`/`security-password` in `gfsecurity.properties`) |
| `admin` | `*:*` | gfsh: `connect --user=admin --password=admin-secret` ([`init.gfsh`](gemfire/init.gfsh)) |
| `metrics` | `CLUSTER:READ` | Prometheus: each member's `/metrics/` endpoint now answers 401 without a login ([`prometheus.yml`](monitoring/prometheus.yml) `basic_auth`) |

## The Go client

[`jwt.go`](grpc-client-go/jwt.go) mints HS256 tokens with `crypto/hmac`, no JWT library needed.
Each lasts 10 minutes and is reused until a minute before it expires. Every call gets its token
as metadata:

```go
ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
gemfire.Get(ctx, &pb.GetRequest{...})
```

grpc-go's `PerRPCCredentials` would attach it automatically. Its stock implementations (`oauth`)
require transport security and won't send a token over a plaintext connection, which is the right
instinct. With TLS (09) it's the neater option.

The client's API:
- `/crud/*` takes `?user=alice|bob|carol|none` (default `alice`) and `?region=orders|customers` (default `orders`).
- `UNAUTHENTICATED` becomes HTTP 401 and `PERMISSION_DENIED` becomes 403, so k6 and curl can tell them apart from failures.

## The narrated demo

| # | Section | What it shows |
|---|---|---|
| 1 | The tokens | each user's decoded JWT (`sub`, `exp`, `perms`) |
| 2 | No token, no entry | a Get without a token → `UNAUTHENTICATED` |
| 3 | alice may do anything | Puts on both regions, and GetAndPut (which needs read and write) |
| 4 | bob is read-only | Get and Query work; Put and GetAndPut → `PERMISSION_DENIED` |
| 5 | carol may only touch orders | Put on orders works, Get on customers is denied; `SELECT * FROM /orders` works, `FROM /customers` is denied |
| 6 | Bad tokens | expired, tampered (`perms` widened to `*:*`, signature unchanged), and signed with another key: all `UNAUTHENTICATED` |
| 7 | Per call, not per connection | on the same connection, alice's Put succeeds and bob's next Put is denied |

## Load test and dashboard

`make select-load-test EX=08 SCRIPT=auth/mixed` sends 20 calls/s for 1 minute: random users doing
Gets and Puts on random regions. 401 and 403 are expected answers here, so k6 doesn't count them as
failures. The table shows what each user got back:

```
By user: what each one got back (401 = UNAUTHENTICATED, 403 = PERMISSION_DENIED; both expected here)
CLIENT · user                        │    CALLS       OK      401      403 │      GF p50         p99
grpc-go · alice                      │      162      162        0        0 │       2.238       5.201
grpc-go · bob                        │      148       98        0       50 │       2.235       6.324
grpc-go · carol                      │      147       70        0       77 │       2.255       5.210
grpc-go · none                       │      144        0      144        0 │       0.000       0.000
```

The dashboard (`:3008`):
- **Who got in:**
  - each user's share of OK / PERMISSION_DENIED / UNAUTHENTICATED
  - calls/s by user and result
  - the permissions table
- **By region:** results per region.
- **Collapsed:** performance, and the servers (including "members up", which shows Prometheus logging in).

## Findings

1. **Every UNAUTHENTICATED looks the same to the caller.** A missing token, an expired one, a forged
   signature and a wrong key all return `Missing or invalid token`. That's good: it tells an attacker
   nothing. But GemFire logs only `authentication failed`, so log the reason in your SecurityManager,
   as this one does.
2. **PERMISSION_DENIED names the exact permission:** `bob not authorized for DATA:WRITE:orders:grpc-go:1`.
   It's checked per region and key (a Query: per region). With `implies()`, a token should be able to
   grant single keys too (`DATA:READ:orders:42`); this demo doesn't exercise that.
3. **GetAndPut needs both permissions.** bob holds READ, so it's denied on WRITE.
4. **Turning security on affects more than gRPC.**
   - Members must log in to join.
   - gfsh must log in.
   - Prometheus gets 401 from every member's metrics endpoint until it scrapes with a user that has `CLUSTER:READ`.
5. **One connection, many users.** The token is checked on every call, so one gRPC connection can carry
   calls for different users. That's useful for a service acting on behalf of its own callers.

## Layout

```
08-token-auth-rbac/
├── .env                      # image, extension paths, ports (3008/9008/8308), JWT_SECRET
├── docker-compose.yml        # locator, server1, server2 and gfsh-init (built: GemFire + SecurityManager), grpc-client-go, prometheus, grafana, cadvisor
├── security/
│   ├── Dockerfile            # compiles the SecurityManager inside the GemFire image; the member image
│   └── src/demo/security/JwtSecurityManager.java
├── gemfire/
│   ├── gfsecurity.properties # security-manager, this member's login, the built-in users' passwords
│   ├── start-member.sh       # --classpath + --security-properties-file
│   └── init.gfsh             # connect as admin; regions orders and customers
├── grpc-client-go/           # jwt (mint tokens, per-call metadata) · crud · api (?user=, ?region=) · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml        # basic_auth as the metrics user
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/auth.json
        └── provisioning/
```
