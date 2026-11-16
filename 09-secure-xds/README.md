# 09 · Secure xDS: everything on (Go)

The capstone. One cluster with every security and discovery feature from 05–08 turned on at once:

| Layer | From | What it does here |
|---|---|---|
| **xDS discovery** | [05](../05-xds-discovery/) | the locator lists the servers; the `xds` channel round-robins over both and survives one stopping |
| **Mutual TLS** | [07](../07-mtls/) | servers and client each present a certificate from the demo CA; no certificate, no connection |
| **Tokens and permissions** | [08](../08-token-auth-rbac/) | every call carries a JWT; the SecurityManager checks it and the permission the call needs |
| **Real signing keys** | new | tokens are **RS256**, signed by an **issuer** that alone holds the private key. The cluster holds only the public key. |

```
                  issuer :8409 ── RS256 JWT (private key: jwt-private.pem, issuer only)
                    ▲  GET /token?user=bob
                    │
 k6 ──HTTP──▶ grpc-client-go ──┬─ direct  dns:///server1:50051 ─────────────────────▶ server1
   (?user= ?via= ?region=)     └─ xds     xds:///gemfire_grpc ──round robin──▶ server1, server2
              every call: mTLS (client.pem) + authorization: Bearer <JWT>       │
                                 │ bootstrap: TLS to the locator (CA only, no token)
                                 └──────────▶ locator :50051 (xDS)
 members: keystore + truststore (mTLS) · SecurityManager with jwt-public.pem (RS256) · cluster users
```

Go only by design, like 03–08.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=09             # certs + keys, member image (GemFire + SecurityManager), issuer, cluster, Go client (demo at startup)
make logs EX=09              # read the demo (grpc-client-go)
make select-load-test EX=09 SCRIPT=auth/mixed      # who got in (08's script)
make select-load-test EX=09 SCRIPT=xds/failover    # secured failover (05's script): server1 stops at 40 s, starts at 80 s
open http://localhost:3009
make stop EX=09
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=09`.

```bash
curl 'localhost:8409/token?user=bob'                                   # an RS256 JWT from the issuer
curl 'localhost:8309/crud/get?key=1&user=bob&via=xds'                  # 200, from server1 or server2
curl -X POST 'localhost:8309/crud/put?key=1&user=bob&via=xds'          # 403 PERMISSION_DENIED, on either server
curl 'localhost:8309/crud/get?key=1&user=none'                         # 401 UNAUTHENTICATED
```

## The pieces, and where each is configured

| Piece | Where |
|---|---|
| CA, server and client certificates, truststore, **issuer key pair** | [`gemfire/make-certs.sh`](gemfire/make-certs.sh) (openssl, the one-shot `certs` service) → `./certs/` (git-ignored) |
| mTLS on gRPC | [`gemfire/gemfire.properties`](gemfire/gemfire.properties) (`--properties-file`), as in 07 |
| SecurityManager, cluster users | [`gemfire/gfsecurity.properties`](gemfire/gfsecurity.properties) (`--security-properties-file`) and [`security/`](security/) (`--classpath`), as in 08 |
| RS256 verification | [`JwtSecurityManager.java`](security/src/demo/security/JwtSecurityManager.java), with the public key from `JWT_PUBLIC_KEY_FILE` |
| The issuer | [`issuer/main.go`](issuer/main.go): `GET /token?user=` signs with `jwt-private.pem` |
| Client: mTLS channels, authority `gemfire`, xDS bootstrap | as in 07 ([`main.go`](grpc-client-go/main.go), [`xds-bootstrap.json`](grpc-client-go/xds-bootstrap.json)) |
| Client: tokens | [`tokens.go`](grpc-client-go/tokens.go): fetched from the issuer, attached by `PerRPCCredentials` |
| Prometheus login | [`monitoring/prometheus.yml`](monitoring/prometheus.yml): `basic_auth` as the `metrics` user (`CLUSTER:READ`) |

**Each container gets only the keys it needs:**
- the issuer gets `jwt-private.pem` and nothing else
- the members get `server.p12`, `truststore.p12` and `jwt-public.pem`
- the client gets `ca.pem`, its own `client.pem`/`client-key.pem`, and `jwt-public.pem`, which is public by nature (the demo uses it to attempt a forgery)

### RS256: why the cluster holds only the public key

In 08 one HMAC secret both signed and checked tokens, so every server could also mint them.
Here the issuer signs with its **private** key, and the SecurityManager verifies with the
**public** key (`java.security.Signature("SHA256withRSA")`). A compromised server can't mint
a token. The SecurityManager also accepts **only** `alg: RS256`. That blocks the classic
"alg swap" forgery: an HS256 token keyed with the public key, which anyone can have, gets accepted by
verifiers that trust the token's own `alg`.

### Tokens with PerRPCCredentials

08 attached tokens by hand, because grpc-go's per-RPC credentials won't go over plaintext.
Here every channel is TLS, so the client uses them. They're called for each RPC with that call's
context, which carries the acting user:

```go
type tokenCreds struct{ issuer *Issuer }
func (t tokenCreds) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	tok, err := t.issuer.token(userOf(ctx))         // cached until a minute before it expires
	return map[string]string{"authorization": "Bearer " + tok}, err
}
func (tokenCreds) RequireTransportSecurity() bool { return true }

grpc.NewClient(target, grpc.WithTransportCredentials(mtls), grpc.WithPerRPCCredentials(tokenCreds{issuer}))
```

## The narrated demo: which layer stops what

| # | The call | Stopped by | Result |
|---|---|---|---|
| 1 | a token over a **plaintext** channel | grpc-go itself | `grpc: the credentials require transport level security`: the channel isn't even built |
| 2 | a good token, **no client certificate** | the TLS handshake | `remote error: tls: certificate required`: the token is never read |
| 3 | a client certificate, **no token** | `authenticate` | `UNAUTHENTICATED` |
| 4 | a token from a **rogue issuer** (RS256, someone else's key) | `authenticate` | `UNAUTHENTICATED` (server log: *not signed by the issuer*) |
| 5 | the **alg-swap forgery** (HS256 keyed with the public key) | `authenticate` | `UNAUTHENTICATED` (server log: *alg must be RS256*) |
| 6 | a good token **without the permission** (bob writes) | `authorize` | `PERMISSION_DENIED: bob not authorized for DATA:WRITE:orders:grpc-go:demo` |
| 7 | everything right | (none) | OK |

Then over **xds**:
- 40 calls as alice reach both servers, all over mTLS and all authorized.
- bob's writes are denied on **both** servers.
- An expired token from the issuer and a tampered one are both `UNAUTHENTICATED`.

Every caller sees the same `Missing or invalid token`, whatever the reason. The members log it:
```
Rejected token: Expired token for alice
Rejected token: Invalid token: not signed by the issuer
Rejected token: Unsupported token: alg must be RS256, got {"alg":"HS256","typ":"JWT"}
```

## Secured failover

`make select-load-test EX=09 SCRIPT=xds/failover`: 20 calls/s per channel as alice, with server1 stopped
at 40 s and started at 80 s:

```
By via: rows compare each via with direct (all ran at the same time)
CLIENT · via                         │    CALLS   FAILED │      GF p50         p99
grpc-go · direct                     │     2401   38.19% │       1.681       6.243
grpc-go · xds                        │     2401    0.00% │       1.755       6.247

Events:
  15:22:51  +40s  stop server1
  15:23:30  +80s  start server1
```

Same as 05 without security: the xds channel moved everything to server2 and lost no calls.
The direct channel lost every call while server1 was down. mTLS and tokens don't get in the way
of discovery or failover. When server1 came back, it accepted the same certificates and tokens.

## Who got in

`make select-load-test EX=09 SCRIPT=auth/mixed` (08's script, over the xds channel here):

```
By user: what each one got back (401 = UNAUTHENTICATED, 403 = PERMISSION_DENIED; both expected here)
CLIENT · user                        │    CALLS       OK      401      403
grpc-go · alice                      │      288      288        0        0
grpc-go · bob                        │      319      207        0      112
grpc-go · carol                      │      312      153        0      159
grpc-go · none                       │      282        0      282        0
```

## See it in Grafana

The dashboard (`:3009`):
- **Every call: mutual TLS + a token:**
  - each channel's calls by transport security
  - certificate expiry, server and client
- **Who got in:** each user's share of OK / PERMISSION_DENIED / UNAUTHENTICATED, plus UNAVAILABLE while a failover had direct with no server.
- **Where the calls went:** direct all server1, xds split.
- **Failover:** members up, failed calls by channel and code.
- **Collapsed:** performance, servers.

## Findings

1. **The layers fail in order, and each failure looks different.** No certificate stops the
   connection before any token is read. A bad token is `UNAUTHENTICATED`, and a missing permission is
   `PERMISSION_DENIED`. grpc-go refuses to send a token over plaintext at all. So a symptom tells
   you which layer to look at.
2. **The locator's xDS endpoint needs neither a client certificate nor a token.** The bootstrap
   holds only the CA. Tokens are checked by the servers on every data call.
3. **Every member enforces the same rules.** bob was denied on server1 and server2 alike, because
   each runs the SecurityManager. Round robin doesn't open a gap.
4. **RS256 with a strict `alg` check is cheap to do right.** The SecurityManager change from 08 is a
   public key and `Signature.verify` instead of an HMAC. Rejecting anything but RS256 stops the alg-swap forgery.
5. **Failover is unaffected by security.** With mTLS and tokens on, the xds channel still lost no calls
   while server1 was down, matching 05's result without security.
6. **06/07's connection-count drift applies here too.** Refused handshakes push `grpc.server.connections`
   down. The panel's description explains it.

## Layout

```
09-secure-xds/
├── .env                      # image, extension paths, ports (3009/9009/8309, issuer 8409)
├── docker-compose.yml        # certs, locator, server1, server2, gfsh-init (built: GemFire + SecurityManager), issuer, grpc-client-go, prometheus, grafana, cadvisor
├── certs/                    # git-ignored: CA, server/client certs, truststore, jwt-private.pem, jwt-public.pem
├── security/                 # the RS256 SecurityManager and the member image (from 08)
├── issuer/                   # the token issuer (Go, standard library only)
├── gemfire/
│   ├── make-certs.sh         # certificates and the issuer's RSA key pair (openssl)
│   ├── gemfire.properties    # mTLS on gRPC (from 07)
│   ├── gfsecurity.properties # SecurityManager and cluster users (from 08)
│   ├── start-member.sh       # --properties-file, --classpath, --security-properties-file
│   └── init.gfsh             # connect as admin; PARTITION_REDUNDANT orders and customers
├── grpc-client-go/
│   ├── xds-bootstrap.json    # TLS to the locator (CA only)
│   └── *.go                  # main (two mTLS channels + PerRPCCredentials) · tokens (issuer, per-call creds) · tlsinfo · crud · api · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml        # basic_auth as the metrics user
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/secure-xds.json
        └── provisioning/
```
