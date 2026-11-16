// Command grpc-client-go is the GemFire for gRPC client written in Go, as a small HTTP service.
// It reaches the cluster over two TLS channels, direct (dns:///server1:50051) and xds
// (xds:///gemfire_grpc through the locator), runs the narrated demo at startup, then serves the
// /crud API that the k6 load tests call with ?via=direct|xds (see api.go).
//
// Env: GRPC_TARGET, GRPC_XDS_TARGET, GRPC_XDS_BOOTSTRAP, TLS_CA_FILE, GRPC_XDS_AUTHORITY plus config.go.
package main

import (
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
	// direct: plain TLS credentials. The certificate must be signed by the CA and list the
	// hostname dialed (server1) in its SANs.
	// xds: xDS credentials. With one-way TLS the locator's discovery response says to use TLS for
	// the servers, with the CA from the bootstrap's file_watcher provider (system_root_certs).
	// FallbackCreds are required anyway, and used if the locator advertises no TLS settings.
	xdsTLS, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: tlsCreds(pool)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] xds credentials: %v\n", cfg.Client, err)
		os.Exit(1)
	}
	servers := newServers("server1", "server2")
	clients := map[string]pb.GemFireClient{}
	for _, ch := range channels {
		target, creds := cfg.GRPCTarget, tlsCreds(pool)
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
