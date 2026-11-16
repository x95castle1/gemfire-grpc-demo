// Command grpc-client-go is the GemFire for gRPC client written in Go, as a small HTTP service.
// Every call carries a JWT for the user it acts as (alice, bob, carol, or none). It runs the
// narrated demo at startup, then serves the /crud API (?user=, ?region=) the k6 load tests call.
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

	metrics := newMetrics(cfg)
	var methods []string
	for _, m := range pb.GemFire_ServiceDesc.Methods {
		methods = append(methods, m.MethodName)
	}
	for _, s := range pb.GemFire_ServiceDesc.Streams {
		methods = append(methods, s.StreamName)
	}
	metrics.initRPCs(methods, cfg.Region, cfg.DemoRegion)
	// One channel (ClientConn) for the whole process, as in grpc-java: it multiplexes every
	// concurrent RPC over HTTP/2. Plaintext here; TLS is covered in 06-tls. Every RPC on it is
	// timed by the interceptors (demo_rpc_duration_seconds).
	conn, err := grpc.NewClient(cfg.GRPCTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(metrics.unaryInterceptor),
		grpc.WithChainStreamInterceptor(metrics.streamInterceptor))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] bad GRPC_TARGET: %v\n", cfg.Client, err)
		os.Exit(1)
	}
	defer conn.Close()
	gemfire := pb.NewGemFireClient(conn)

	if cfg.JWTSecret == "" {
		fmt.Fprintf(os.Stderr, "[%s] JWT_SECRET is not set\n", cfg.Client)
		os.Exit(1)
	}
	tokens := newTokens(cfg.JWTSecret)
	crud := newGrpcCrud(gemfire, tokens, cfg)
	api := newApi(cfg, metrics, crud, func() Report {
		checks := newChecks(cfg.Client, metrics)
		runDemo(gemfire, crud, tokens, cfg, checks)
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
