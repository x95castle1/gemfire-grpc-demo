// Command grpc-client-go is the GemFire for gRPC client written in Go, as a small HTTP service.
// It runs the narrated demo at startup, then serves the same CRUD API as the Java clients,
// which the k6 load tests call (see api.go).
//
// Env: GRPC_TARGET (default dns:///server1:50051) plus the settings in config.go.
package main

import (
	"fmt"
	"os"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := configFromEnv()
	fmt.Printf("[%s] target=%s region=%s\n", cfg.Client, cfg.GRPCTarget, cfg.Region)

	// One channel (ClientConn) for the whole process, as in grpc-java: it multiplexes every
	// concurrent RPC over HTTP/2. Plaintext here; TLS is covered in 06-tls.
	conn, err := grpc.NewClient(cfg.GRPCTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] bad GRPC_TARGET: %v\n", cfg.Client, err)
		os.Exit(1)
	}
	defer conn.Close()
	gemfire := pb.NewGemFireClient(conn)

	metrics := newMetrics(cfg)
	api := newApi(cfg, metrics, newGrpcCrud(gemfire, cfg), func() Report {
		checks := newChecks(cfg.Client, metrics)
		runDemo(gemfire, cfg, checks)
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
