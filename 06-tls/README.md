# 06 · One-way TLS (Go)

This example turns on **TLS for GemFire for gRPC**:
- **The servers prove who they are.** Every member's gRPC endpoint (both servers, and the locator's xDS
  endpoint) presents a certificate signed by a demo CA.
- **The client checks it.** The Go client trusts that CA and verifies the certificate before it sends anything.
- **Clients don't present certificates.** That's mutual TLS, covered in 07.

It's 05's client with TLS added, so both channels are still there:

| Channel | Target | TLS |
|---|---|---|
| `direct` | `dns:///server1:50051` | plain TLS credentials: the CA, and the hostname dialed (`server1`) |
| `xds` | `xds:///gemfire_grpc` | TLS to the locator (bootstrap file); the locator then tells the client to use TLS for the servers |

Go only by design, like 03–05.

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=06             # makes ./certs on first start, then the cluster, the Go client (demo at startup), monitoring
make logs EX=06              # read the demo (grpc-client-go), or certs for the certificate output
make select-load-test EX=06 SCRIPT=xds/balance     # 1 minute over both channels, then open the dashboard
open http://localhost:3006
make stop EX=06
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=06`.

## The certificates

The one-shot `certs` service runs [`gemfire/make-certs.sh`](gemfire/make-certs.sh) with the GemFire image's
`keytool`, so you don't need any tools on your machine. It writes to `./certs/` (git-ignored) and skips the
work if the files are already there. Delete the folder and restart to make new ones.

| File | What it is |
|---|---|
| `ca.p12`, `ca.pem` | a demo certificate authority (10 years). Clients trust `ca.pem`. |
| `server.p12` | the servers' key and certificate (alias `grpc-server`, 1 year), signed by the CA. One certificate shared by all three members. |

The server certificate's SANs list every name a client dials: `locator`, `server1`, `server2`,
`localhost`, `127.0.0.1`. A client checks the name it dialed against that list.

## Server configuration

[`gemfire/gemfire.properties`](gemfire/gemfire.properties), passed to every member with
`--properties-file` ([`start-member.sh`](gemfire/start-member.sh)):

```properties
ssl-enabled-components=grpc
ssl-keystore=/certs/server.p12
ssl-keystore-password=changeit
ssl-keystore-type=PKCS12
ssl-grpc-alias=grpc-server
ssl-require-authentication=false
```

Only the `grpc` component gets TLS. gfsh, the locator and server protocols and the Prometheus
endpoint stay plaintext, so gfsh-init, the healthchecks and scraping work as before.

## Client configuration (Go)

**direct:** ordinary TLS credentials with the demo CA. They verify both the CA and the hostname dialed.

```go
pool := x509.NewCertPool()
pool.AppendCertsFromPEM(caPEM)                       // ./certs/ca.pem
creds := credentials.NewTLS(&tls.Config{RootCAs: pool})
grpc.NewClient("dns:///server1:50051", grpc.WithTransportCredentials(creds))
```

**xds** needs TLS in two places.

1. **The locator.** The [bootstrap file](grpc-client-go/xds-bootstrap.json) makes the connection
   to the xDS server TLS (`channel_creds` type `tls`). It also names the CA for the servers, in a
   `file_watcher` certificate provider called `system_root_certs`:
   ```json
   "channel_creds": [{ "type": "tls", "config": { "ca_certificate_file": "/certs/ca.pem" } }],
   "certificate_providers": {
     "system_root_certs": { "plugin_name": "file_watcher", "config": { "ca_certificate_file": "/certs/ca.pem" } }
   }
   ```
2. **The servers.** Use **xDS credentials**, not plain TLS credentials. With one-way TLS, the locator's
   discovery response tells the client to use TLS for the servers, using that provider's CA. Plain
   TLS credentials would ignore what the locator says. The fallback credentials are required anyway:
   ```go
   creds, _ := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: tlsCreds})
   grpc.NewClient("xds:///gemfire_grpc", grpc.WithTransportCredentials(creds))
   ```

## The narrated demo

| # | Section | What it shows |
|---|---|---|
| 1 | The certificates | the CA the client trusts, and the server TLS settings |
| 2 | A call over TLS | a Put and Get over `direct`: TLS 1.3, and server1's certificate (issued by the demo CA, `server1` in its SANs) |
| 3 | Plaintext is refused | a plaintext Get to server1 fails with UNAVAILABLE |
| 4 | xDS over TLS | the bootstrap file; 40 calls over `xds` reach both servers, all TLS with the demo CA's certificate |
| 5 | Check it yourself | the `openssl s_client` line below |

## Check it yourself

From a throwaway container on the cluster's network (no openssl needed on your machine):

```bash
docker run --rm --network gemfire-06-tls_default -v "$PWD/certs:/certs:ro" alpine:3.24.3 sh -c \
  'apk add -q openssl && echo | openssl s_client -connect server1:50051 -CAfile /certs/ca.pem -servername server1 -alpn h2'
```

```
subject=O=GemFire gRPC demo, CN=gemfire-grpc
issuer=CN=GemFire gRPC Demo CA
Protocol: TLSv1.3
ALPN protocol: h2
Verify return code: 0 (ok)
```

The same works for `locator:50051` (the xDS endpoint).

## See it in Grafana

`make select-load-test EX=06 SCRIPT=xds/balance` (05's script, which works here unchanged), then open `:3006`:
- **Every call is TLS:**
  - each channel's share of calls by transport security (100% `tls`, from each call's peer `AuthInfo`)
  - how long until each server's certificate expires
- **Where the calls went:** as in 05, now over TLS. direct is all server1; xds is split 50/50.
- `xds/failover` works here too.

## Findings

1. **Set `ssl-require-authentication=false` for one-way TLS.** The docs list it as defaulting to
   `false`, but without it the servers asked every client for a certificate. Both channels failed:
   `tls: certificate required` on direct, and `tls: illegal parameter` on xds. Setting it explicitly
   gives one-way TLS.
2. **The direct channel checks the hostname; the xds channel doesn't.**
   - Dialing server1 by a name that isn't in the SANs fails on direct with `x509: certificate is valid for locator, server1, server2, localhost, not gemfire-06-tls-server1-1`.
   - Over xds, the certificate must be signed by the CA from the bootstrap's `file_watcher` provider (point it at a missing file and the channel can't connect).
   - But any authority worked: none set, `server1`, even `wrong.example`. The docs' advice to set `grpc.WithAuthority` to the real hostname applies to the fallback (mTLS) path in 07.
3. **The locator really does advertise TLS.** The xds channel used the CA from the `file_watcher`
   provider the locator's response refers to, not its fallback credentials.
4. **TLS 1.3 by default.** Every call negotiated TLS 1.3 (`TLS_AES_128_GCM_SHA256`) with HTTP/2 over ALPN.
5. **The extension's connection count drifts with TLS.** `grpc.server.connections` drops by one for
   every refused handshake (each demo run makes one plaintext attempt) and never adds it back. So
   server1 reads 0 or below while it holds 2 real connections. The dashboard panel says so.

## Layout

```
06-tls/
├── .env                      # image, extension paths, ports (3006/9006/8306)
├── docker-compose.yml        # certs (one-shot), locator, server1, server2, gfsh-init, grpc-client-go, prometheus, grafana, cadvisor
├── certs/                    # git-ignored, made at startup: ca.pem, ca.p12, server.p12, server.pem
├── gemfire/
│   ├── make-certs.sh         # the demo CA and server certificate (keytool)
│   ├── gemfire.properties    # ssl-enabled-components=grpc + keystore
│   ├── start-member.sh       # every member: gRPC extension, addresses, --properties-file
│   └── init.gfsh             # PARTITION_REDUNDANT regions tls and demo-grpc-go
├── grpc-client-go/
│   ├── xds-bootstrap.json    # TLS to the locator, plus the file_watcher CA for the servers
│   └── *.go                  # main (two TLS channels) · tlsinfo (CA, peer AuthInfo) · crud · api · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/tls.json
        └── provisioning/
```
