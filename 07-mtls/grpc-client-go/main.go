// Command grpc-client-go is the GemFire for gRPC client written in Go, as a small HTTP service.
// It reaches the cluster over two mutual-TLS channels, direct (dns:///server1:50051) and xds
// (xds:///gemfire_grpc through the locator), runs the narrated demo at startup, then serves the
// /crud API that the k6 load tests call with ?via=direct|xds (see api.go).
//
// Env: GRPC_TARGET, GRPC_XDS_TARGET, GRPC_XDS_BOOTSTRAP, TLS_CA_FILE, TLS_CERT_FILE, TLS_KEY_FILE,
// GRPC_XDS_AUTHORITY plus config.go.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	xdscreds "google.golang.org/grpc/credentials/xds"
	_ "google.golang.org/grpc/xds" // registers the xds:/// resolver and the xDS balancers
)

func main() {
	cfg := configFromEnv()
	fmt.Printf("[%s] region=%s\n", cfg.Client, cfg.Region)

	metrics := newMetrics(cfg)
	var methods []string
	for _, m := range pb.GemFire_ServiceDesc.Methods {
		methods = append(methods, m.MethodName)
	}
	for _, s := range pb.GemFire_ServiceDesc.Streams {
		methods = append(methods, s.StreamName)
	}
	metrics.initRPCs(methods, cfg.Region, cfg.DemoRegion)
	// Two channels (ClientConns) to the same cluster. Each multiplexes every concurrent RPC over
	// HTTP/2 and is timed by its own interceptors (demo_rpc_duration_seconds, demo_calls_total).
	//   direct: dns:///server1:50051, as in 01-04. Every call goes to server1.
	//   xds:    xds:///gemfire_grpc. grpc-go reads GRPC_XDS_BOOTSTRAP (the locator's address),
	//           asks the locator for the gRPC servers, and balances calls across them.
	pool, err := caPool(cfg.CAFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] can't read the CA (TLS_CA_FILE): %v\n", cfg.Client, err)
		os.Exit(1)
	}
	client, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] can't read the client certificate (TLS_CERT_FILE/TLS_KEY_FILE): %v\n", cfg.Client, err)
		os.Exit(1)
	}
	if leaf, err := x509.ParseCertificate(client.Certificate[0]); err == nil {
		metrics.clientCertExpiry.WithLabelValues(cfg.Client, cfg.Example).Set(float64(leaf.NotAfter.Unix()))
	}
	// Mutual TLS: both channels present the client certificate and trust the demo CA.
	// direct: the server certificate must list the hostname dialed (server1) in its SANs.
	// xds: xDS credentials. Under mTLS the locator advertises no TLS settings, so the client uses
	// these FallbackCreds (with its certificate) for the servers, and checks their certificates
	// against the authority (grpc.WithAuthority, GRPC_XDS_AUTHORITY=gemfire).
	mtls := tlsCreds(pool, &client)
	xdsTLS, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: mtls})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] xds credentials: %v\n", cfg.Client, err)
		os.Exit(1)
	}
	servers := newServers("server1", "server2")
	clients := map[string]pb.GemFireClient{}
	for _, ch := range channels {
		target, creds := cfg.GRPCTarget, mtls
		opts := []grpc.DialOption{}
		if ch == "xds" {
			target, creds = cfg.XDSTarget, xdsTLS
			if cfg.XDSAuthority != "" {
				opts = append(opts, grpc.WithAuthority(cfg.XDSAuthority))
			}
		}
		conn, err := grpc.NewClient(target, append(opts,
			grpc.WithTransportCredentials(creds),
			grpc.WithChainUnaryInterceptor(metrics.unaryInterceptor(ch, servers)),
			grpc.WithChainStreamInterceptor(metrics.streamInterceptor(ch, servers)))...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] bad %s target %q: %v\n", cfg.Client, ch, target, err)
			os.Exit(1)
		}
		defer conn.Close()
		clients[ch] = pb.NewGemFireClient(conn)
	}
	fmt.Printf("[%s] channels: direct=%s xds=%s (bootstrap %s)\n", cfg.Client, cfg.GRPCTarget, cfg.XDSTarget, os.Getenv("GRPC_XDS_BOOTSTRAP"))

	crud := newGrpcCrud(clients, servers, cfg)
	api := newApi(cfg, metrics, crud, func() Report {
		checks := newChecks(cfg.Client, metrics)
		runDemo(crud, cfg, checks)
		return checks.finish()
	})

	if cfg.DemoOnStart {
		api.runDemo()
	}
	if err := api.start(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] HTTP server stopped: %v\n", cfg.Client, err)
		os.Exit(1)
	}
}
