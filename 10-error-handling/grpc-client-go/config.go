package main

import (
	"os"
	"strconv"
	"strings"
)

// Config holds the service settings, read from environment variables (see ../.env and
// docker-compose.yml). Same variables and defaults as the Java clients.
type Config struct {
	Client       string // metric label: grpc-go
	Example      string
	Region       string // the region the load tests use
	DemoRegion   string // this client's own demo region, so EntryCount checks are exact
	KeyPrefix    string // prefix for every key this client writes
	PayloadBytes int
	Port         int
	HTTPThreads  int // max requests handled at once (the Java clients' fixed thread pool)
	DemoOnStart  bool
	GRPCTarget   string // every channel's target: dns:///server1:50051
	Unreachable  string // a target with no server behind it, for the demo's retry section
	// The servers' limits (start-member.sh), shown in the demo
	CriticalHeapPercentage string
	MaxQueryExecutionTime  string
}

func configFromEnv() Config {
	return Config{
		Client:       "grpc-go",
		Example:      env("EXAMPLE", "10-error-handling"),
		Region:       env("REGION", "orders"),
		DemoRegion:   env("DEMO_REGION", "demo-grpc-go"),
		KeyPrefix:    env("KEY_PREFIX", "grpc-go:"),
		PayloadBytes: envInt("PAYLOAD_BYTES", 1024),
		Port:         envInt("PORT", 8080),
		HTTPThreads:  envInt("HTTP_THREADS", 64),
		DemoOnStart:  env("DEMO_ON_START", "true") == "true",
		GRPCTarget:   env("GRPC_TARGET", "dns:///server1:50051"),
		Unreachable:  env("GRPC_UNREACHABLE_TARGET", "dns:///server3:50051"),

		CriticalHeapPercentage: env("CRITICAL_HEAP_PERCENTAGE", "80"),
		MaxQueryExecutionTime:  env("MAX_QUERY_EXECUTION_TIME", "2000"),
	}
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	n, err := strconv.Atoi(env(name, strconv.Itoa(def)))
	if err != nil {
		return def
	}
	return n
}
