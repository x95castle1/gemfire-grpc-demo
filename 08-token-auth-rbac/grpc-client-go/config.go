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
	Region       string // the default region (?region= picks another)
	DemoRegion   string // this client's own demo region, so EntryCount checks are exact
	KeyPrefix    string // keeps this client's bench keys apart from the other clients'
	PayloadBytes int
	Port         int
	HTTPThreads  int // max requests handled at once (the Java clients' fixed thread pool)
	DemoOnStart  bool
	GRPCTarget   string
	JWTSecret    string // the key the demo tokens are signed with (JWT_SECRET; the servers check with it)
}

func configFromEnv() Config {
	return Config{
		Client:       "grpc-go",
		Example:      env("EXAMPLE", "08-token-auth-rbac"),
		Region:       env("REGION", "orders"),
		DemoRegion:   env("DEMO_REGION", "demo-grpc-go"),
		KeyPrefix:    env("KEY_PREFIX", "grpc-go:"),
		PayloadBytes: envInt("PAYLOAD_BYTES", 1024),
		Port:         envInt("PORT", 8080),
		HTTPThreads:  envInt("HTTP_THREADS", 64),
		DemoOnStart:  env("DEMO_ON_START", "true") == "true",
		GRPCTarget:   env("GRPC_TARGET", "dns:///server1:50051"),
		JWTSecret:    env("JWT_SECRET", ""),
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
