# 07 · Mutual TLS (Go)

06 was one-way TLS: the servers proved who they were, and anyone who trusted the CA could
connect. **Mutual TLS** checks in both directions:
- **The client presents a certificate too,** signed by a CA the servers trust.
- **The servers refuse the handshake without one.** No certificate, no connection, before any GemFire call is made.

It's 06's Go client with a client certificate, so both channels are still there:

| Channel | Target | mTLS |
|---|---|---|
| `direct` | `dns:///server1:50051` | TLS credentials with the CA **and** the client certificate |
| `xds` | `xds:///gemfire_grpc` | TLS to the locator (bootstrap); mTLS to the servers via the fallback credentials, checked against the authority `gemfire` |

Go only by design, like 03–06.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=07             # makes ./certs on first start, then the cluster, the Go client (demo at startup), monitoring
make logs EX=07              # read the demo (grpc-client-go), or certs for the certificate output
make select-load-test EX=07 SCRIPT=xds/balance     # 1 minute over both channels, then open the dashboard
open http://localhost:3007
make stop EX=07
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=07`.

## The certificates

The one-shot `certs` service runs [`gemfire/make-certs.sh`](gemfire/make-certs.sh) with **openssl**
(installed into an alpine container at run time). 06 used `keytool`, but the Go client needs its
private key as PEM, which `keytool` can't export. The files go in `./certs/` (git-ignored). Delete the
folder and restart to make new ones.

| File | What it is | Used by |
|---|---|---|
| `ca.pem` | the demo CA | both sides: the client trusts it for servers, the servers for clients |
| `server.p12` | the servers' key and certificate (alias `grpc-server`), shared by all members | servers (`ssl-keystore`) |
| `truststore.p12` | the CA alone, marked as a Java trusted entry (`openssl pkcs12 -nokeys -jdktrust anyExtendedKeyUsage`) | servers (`ssl-truststore`) |
| `client.pem`, `client-key.pem` | the client's certificate (CN `grpc-client-go`, `clientAuth`) and key | the Go client |

The server certificate's SANs: `gemfire`, `locator`, `server1`, `server2`, `localhost`, `127.0.0.1`.
`gemfire` is a cluster-wide name, so the xds channel has one hostname to check every server against.

## What changes from 06

**Servers:** [`gemfire/gemfire.properties`](gemfire/gemfire.properties) adds a truststore and turns on client authentication:

```properties
ssl-truststore=/certs/truststore.p12
ssl-truststore-password=changeit
ssl-truststore-type=PKCS12
ssl-require-authentication=true
```

**Client, direct:** the same TLS credentials, plus the client certificate:

```go
client, _ := tls.LoadX509KeyPair("/certs/client.pem", "/certs/client-key.pem")
creds := credentials.NewTLS(&tls.Config{RootCAs: pool, Certificates: []tls.Certificate{client}})
```

**Client, xds:**
- **The locator no longer advertises TLS settings** under mTLS. So the client's **fallback credentials**
  (the mTLS ones above) secure the servers.
- **The authority must be a name in the server certificate:**
  ```go
  creds, _ := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: mtlsCreds})
  grpc.NewClient("xds:///gemfire_grpc", grpc.WithTransportCredentials(creds), grpc.WithAuthority("gemfire"))
  ```
- **The bootstrap gets simpler:** `channel_creds` to the locator (TLS with the CA), and nothing else. The
  locator's xDS port doesn't ask for a client certificate, and 06's `file_watcher` provider isn't
  used under mTLS. See [`xds-bootstrap.json`](grpc-client-go/xds-bootstrap.json).

## The narrated demo

| # | Section | What it shows |
|---|---|---|
| 1 | The certificates, on both sides | the CA, and the client's certificate (issued by the CA, for client authentication) |
| 2 | A call over mutual TLS | a Put and Get over `direct`: TLS 1.3, client certificate accepted, server1's certificate verified |
| 3 | No client certificate, no entry | a TLS client that trusts the CA but has no certificate: `tls: certificate required` |
| 4 | xDS over mutual TLS | the bootstrap; 40 calls over `xds` reach both servers, all mTLS, each certificate valid for `gemfire` |
| 5 | Check it yourself | the `openssl` lines below |

## Check it yourself

From a throwaway container on the cluster's network (no openssl needed on your machine):

```bash
# with the client certificate: the handshake completes
docker run --rm --network gemfire-07-mtls_default -v "$PWD/certs:/certs:ro" alpine:3.24.3 sh -c \
  'apk add -q openssl && (echo; sleep 1) | openssl s_client -connect server1:50051 -servername server1 -alpn h2 \
     -CAfile /certs/ca.pem -cert /certs/client.pem -key /certs/client-key.pem'
# → Verify return code: 0 (ok)

# without it: refused
docker run --rm --network gemfire-07-mtls_default -v "$PWD/certs:/certs:ro" alpine:3.24.3 sh -c \
  'apk add -q openssl && (echo; sleep 1) | openssl s_client -connect server1:50051 -servername server1 -alpn h2 \
     -CAfile /certs/ca.pem'
# → ssl3_read_bytes:tlsv13 alert certificate required … SSL alert number 116
```

The same command without a certificate **succeeds** against `locator:50051`: the xDS port doesn't require one.

## See it in Grafana

`make select-load-test EX=07 SCRIPT=xds/balance`, then open `:3007`:
- **Every call is mutual TLS:**
  - 100% TLS on both channels
  - how long until the server certificates and the client's own certificate expire
- **Where the calls went:** direct all server1, xds 50/50.
- `xds/failover` works here too.

## Findings

1. **Under mTLS the authority is required, not optional.**
   - With no `grpc.WithAuthority`, the xds channel can't connect at all: `remote error: tls: illegal parameter`.
     Without an authority the client uses the xDS name `gemfire_grpc` as the TLS server name. That
     isn't a valid hostname (the underscore), which is most likely why the server rejects the handshake
     with that alert.
   - With an authority the server certificate doesn't list, verification fails:
     `x509: certificate is valid for gemfire, locator, server1, server2, localhost, not wrong.example`.
   - With `gemfire` (in the SANs) it works.
   - Give the server certificate a cluster-wide name and use it as the authority.
2. **This is the opposite of 06.** With one-way TLS, the locator-advertised settings checked the CA but no
   hostname, and any authority worked. Under mTLS the fallback credentials check both.
3. **Only the servers' gRPC port requires a client certificate.** The locator's xDS port accepts
   any TLS client that trusts the CA. That's why the bootstrap needs only the CA.
4. **A Java truststore from openssl works.** `openssl pkcs12 -export -nokeys -jdktrust anyExtendedKeyUsage`
   makes a PKCS12 file whose CA entry Java treats as trusted (`keytool -list` shows `trustedCertEntry`), so
   no `keytool` step is needed.
5. **The connection count drifts here too.** As in 06, `grpc.server.connections` drops by one for every
   refused handshake. Each demo run makes one attempt without a client certificate, so it can go negative.

## Layout

```
07-mtls/
├── .env                      # image, extension paths, ports (3007/9007/8307)
├── docker-compose.yml        # certs (one-shot, openssl), locator, server1, server2, gfsh-init, grpc-client-go, prometheus, grafana, cadvisor
├── certs/                    # git-ignored, made at startup: ca.pem, server.p12, truststore.p12, client.pem, client-key.pem
├── gemfire/
│   ├── make-certs.sh         # CA, server and client certificates (openssl)
│   ├── gemfire.properties    # ssl-enabled-components=grpc, keystore, truststore, require-authentication
│   ├── start-member.sh       # every member: gRPC extension, addresses, --properties-file
│   └── init.gfsh             # PARTITION_REDUNDANT regions mtls and demo-grpc-go
├── grpc-client-go/
│   ├── xds-bootstrap.json    # TLS to the locator (CA only)
│   └── *.go                  # main (two mTLS channels) · tlsinfo · crud · api · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/mtls.json
        └── provisioning/
```
