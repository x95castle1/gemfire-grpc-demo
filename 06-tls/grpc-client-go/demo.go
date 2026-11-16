package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// runDemo is a narrated tour of one-way TLS: the certificates, a TLS call over the direct channel,
// proof that plaintext is refused, and xDS over TLS (to the locator and to the servers).
func runDemo(crud *GrpcCrud, cfg Config, c *Checks) {
	key := func(i int) string { return fmt.Sprintf("%sdemo-%d", cfg.KeyPrefix, i) }

	c.section("1. The certificates")
	_, err := caPool(cfg.CAFile)
	if err != nil {
		c.abort(err)
		return
	}
	if ca, err := readCert(cfg.CAFile); err == nil {
		c.note("The client trusts one CA (" + cfg.CAFile + "): " + describe(ca))
	}
	c.note("The servers present one certificate signed by it (gemfire/make-certs.sh), configured with")
	c.note("ssl-enabled-components=grpc, ssl-keystore=/certs/server.p12 and ssl-require-authentication=false")
	c.note("in gemfire/gemfire.properties. Only gRPC is encrypted; gfsh and the metrics endpoint aren't.")

	c.section("2. A call over TLS (direct channel)")
	if _, _, err := crud.execute("direct", "Put", key(1), 100); err != nil {
		c.abort(err)
		return
	}
	_, info, err := crud.execute("direct", "Get", key(1), 0)
	if err != nil {
		c.abort(err)
		return
	}
	c.check("the call went over TLS", info.Security == "tls", info.Version+", "+info.Cipher)
	c.check("server1 presented a certificate issued by the demo CA", info.Cert != nil &&
		info.Cert.Issuer.CommonName == "GemFire gRPC Demo CA", describe(info.Cert))
	c.check("…whose SANs include server1, the name this channel dials, so verification passed",
		info.Cert != nil && slices.Contains(info.Cert.DNSNames, "server1"))

	c.section("3. Plaintext is refused")
	conn, err := grpc.NewClient(cfg.GRPCTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		c.abort(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = pb.NewGemFireClient(conn).Get(ctx, &pb.GetRequest{RegionName: cfg.DemoRegion, Key: &pb.Key{KeyValue: &pb.Key_String_{String_: key(1)}}})
	cancel()
	conn.Close()
	c.check("a plaintext Get to server1:50051 fails", err != nil, statusText(err))

	c.section("4. xDS over TLS")
	if b, err := os.ReadFile(os.Getenv("GRPC_XDS_BOOTSTRAP")); err == nil {
		c.note("The bootstrap file reaches the locator over TLS (channel_creds type tls) and names the CA for")
		c.note("the servers (certificate_providers.system_root_certs, a file_watcher):")
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			c.note("  " + line)
		}
	}
	seen, tlsCalls := map[string]int{}, 0
	for i := range 40 {
		_, info, err := crud.execute("xds", "Get", key(i%2), 0)
		if err != nil {
			c.abort(err)
			return
		}
		seen[info.Server]++
		if info.Security == "tls" && info.Cert != nil && info.Cert.Issuer.CommonName == "GemFire gRPC Demo CA" {
			tlsCalls++
		}
	}
	c.check("40 calls over xds reach both servers", seen["server1"] > 0 && seen["server2"] > 0, seen)
	c.check("…and all 40 went over TLS, with the demo CA's certificate", tlsCalls == 40, tlsCalls)
	c.note("With one-way TLS the locator tells the client to use TLS for the servers, with the CA from the")
	c.note("bootstrap's file_watcher provider (remove that file and the xds channel can't connect).")

	c.section("5. Check it yourself")
	c.note("From the host (the README shows a version that needs no openssl on your machine):")
	c.note("  openssl s_client -connect <server1>:50051 -CAfile 06-tls/certs/ca.pem -servername server1")
	c.note("  → Verify return code: 0 (ok)")
}

// statusText renders an RPC error as `CODE: description` ("no error" when the call succeeded).
func statusText(err error) string {
	if err == nil {
		return "no error"
	}
	return errorCode(err) + ": " + status.Convert(err).Message()
}
