package main

import (
	"context"
	"crypto/x509"

	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// runDemo is a narrated tour of mutual TLS: the certificates on both sides, a call where the client
// proves who it is too, proof that a client without a certificate is refused, and xDS over mTLS.
func runDemo(crud *GrpcCrud, cfg Config, c *Checks) {
	key := func(i int) string { return fmt.Sprintf("%sdemo-%d", cfg.KeyPrefix, i) }

	c.section("1. The certificates, on both sides")
	pool, err := caPool(cfg.CAFile)
	if err != nil {
		c.abort(err)
		return
	}
	client, err := readCert(cfg.CertFile)
	if err != nil {
		c.abort(err)
		return
	}
	if ca, err := readCert(cfg.CAFile); err == nil {
		c.note("Both sides trust one CA (" + cfg.CAFile + "): " + describe(ca))
	}
	c.check("the client has its own certificate, signed by the demo CA, for client authentication",
		client.Issuer.CommonName == "GemFire gRPC Demo CA" && slices.Contains(client.ExtKeyUsage, x509.ExtKeyUsageClientAuth),
		describe(client))
	c.note("The servers add a truststore and ssl-require-authentication=true to 06's settings")
	c.note("(gemfire/gemfire.properties): no certificate from the demo CA, no connection.")

	c.section("2. A call over mutual TLS (direct channel)")
	if _, _, err := crud.execute("direct", "Put", key(1), 100); err != nil {
		c.abort(err)
		return
	}
	_, info, err := crud.execute("direct", "Get", key(1), 0)
	if err != nil {
		c.abort(err)
		return
	}
	c.check("the call went over TLS, with the client certificate accepted", info.Security == "tls", info.Version+", "+info.Cipher)
	c.check("server1 presented the demo CA's certificate, with server1 in its SANs", info.Cert != nil &&
		info.Cert.Issuer.CommonName == "GemFire gRPC Demo CA" && slices.Contains(info.Cert.DNSNames, "server1"), describe(info.Cert))

	c.section("3. No client certificate, no entry")
	conn, err := grpc.NewClient(cfg.GRPCTarget, grpc.WithTransportCredentials(tlsCreds(pool, nil)))
	if err != nil {
		c.abort(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = pb.NewGemFireClient(conn).Get(ctx, &pb.GetRequest{RegionName: cfg.DemoRegion, Key: &pb.Key{KeyValue: &pb.Key_String_{String_: key(1)}}})
	cancel()
	conn.Close()
	c.check("a TLS client that trusts the CA but has no certificate is refused",
		err != nil && strings.Contains(err.Error(), "certificate required"), statusText(err))

	c.section("4. xDS over mutual TLS")
	if b, err := os.ReadFile(os.Getenv("GRPC_XDS_BOOTSTRAP")); err == nil {
		c.note("The bootstrap reaches the locator over TLS. The locator's xDS port doesn't ask for a client")
		c.note("certificate, so the bootstrap needs only the CA:")
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			c.note("  " + line)
		}
	}
	c.note("Under mTLS the locator doesn't tell the client how to secure the servers, so the xds channel uses")
	c.note("its fallback credentials (with the client certificate) and checks each server's certificate")
	c.note(fmt.Sprintf("against the authority it sets: %q, which is in the server certificate's SANs.", cfg.XDSAuthority))
	seen, mtlsCalls := map[string]int{}, 0
	for i := range 40 {
		_, info, err := crud.execute("xds", "Get", key(i%2), 0)
		if err != nil {
			c.abort(err)
			return
		}
		seen[info.Server]++
		if info.Security == "tls" && info.Cert != nil && slices.Contains(info.Cert.DNSNames, cfg.XDSAuthority) {
			mtlsCalls++
		}
	}
	c.check("40 calls over xds reach both servers", seen["server1"] > 0 && seen["server2"] > 0, seen)
	c.check(fmt.Sprintf("…all over mutual TLS, each server's certificate valid for %q", cfg.XDSAuthority), mtlsCalls == 40, mtlsCalls)

	c.section("5. Check it yourself")
	c.note("openssl s_client, with and without the client certificate (README has a no-install version):")
	c.note("  … -CAfile certs/ca.pem -cert certs/client.pem -key certs/client-key.pem   → handshake completes")
	c.note("  … -CAfile certs/ca.pem   (no -cert)                                       → alert certificate required")

}

// statusText renders an RPC error as `CODE: description` ("no error" when the call succeeded).
func statusText(err error) string {
	if err == nil {
		return "no error"
	}
	return errorCode(err) + ": " + status.Convert(err).Message()
}
