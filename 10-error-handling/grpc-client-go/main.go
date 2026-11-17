// Command grpc-client-go is the GemFire for gRPC client written in Go, as a small HTTP service.
// It reaches server1 over three channels that handle failures differently (failfast, retry,
// waitforready; see channels.go), runs the narrated error-handling demo at startup, then serves
// the /crud API that the k6 load tests call with ?via= (see api.go).
//
// Env: GRPC_TARGET, GRPC_UNREACHABLE_TARGET plus the settings in config.go.
package main

import (
	"fmt"
	"os"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
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

	// Three channels (ClientConns) to server1, one per way of handling a failure, plus the same
	// three to a target with no server behind it, for the demo's retry section.
	clients := map[string]pb.GemFireClient{}
	conns := map[string]*grpc.ClientConn{}
	for _, kind := range channels {
		for label, target := range map[string]string{kind: cfg.GRPCTarget, "unreachable/" + kind: cfg.Unreachable} {
			conn, err := dial(kind, label, target, metrics)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] bad %s target %q: %v\n", cfg.Client, label, target, err)
				os.Exit(1)
			}
			defer conn.Close()
			clients[label] = pb.NewGemFireClient(conn)
			conns[label] = conn
		}
	}
	fmt.Printf("[%s] channels failfast, retry, waitforready → %s (deadline %v per call)\n", cfg.Client, cfg.GRPCTarget, callDeadline)

	crud := newGrpcCrud(clients, cfg)
	api := newApi(cfg, metrics, crud, func() Report {
		checks := newChecks(cfg.Client, metrics)
		runDemo(crud, conns, cfg, checks)
		return checks.finish()
	})

	if cfg.DemoOnStart {
		// Serve /metrics first and give Prometheus one scrape (every 5 s) of the zeroed counters:
		// the dashboard's status code and low-memory panels count the demo's calls with increase(),
		// which can't see a counter's first value.
		go func() {
			time.Sleep(6 * time.Second)
			api.runDemo()
		}()
	}
	if err := api.start(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] HTTP server stopped: %v\n", cfg.Client, err)
		os.Exit(1)
	}
}
