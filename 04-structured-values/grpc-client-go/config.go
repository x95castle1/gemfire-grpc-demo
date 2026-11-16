package main

import (
	"os"
	"strconv"
	"strings"
)

// Config holds the service settings, read from environment variables (see ../.env and
// docker-compose.yml).
type Config struct {
	Client         string // metric label: grpc-go
	Example        string
	Region         string // the people region the load tests use (filled by POST /people/load)
	DemoRegion     string // the demo's own people
	NotesRegion    string // the demo's Note messages: a type the server has no descriptor for
	Port           int
	HTTPThreads    int // max requests handled at once
	DemoOnStart    bool
	GRPCTarget     string
}

func configFromEnv() Config {
	return Config{
		Client:         "grpc-go",
		Example:        env("EXAMPLE", "04-structured-values"),
		Region:         env("REGION", "people"),
		DemoRegion:     env("DEMO_REGION", "demo-grpc-go"),
		NotesRegion:    env("NOTES_REGION", "demo-notes"),
		Port:           envInt("PORT", 8080),
		HTTPThreads:    envInt("HTTP_THREADS", 64),
		DemoOnStart:    env("DEMO_ON_START", "true") == "true",
		GRPCTarget:     env("GRPC_TARGET", "dns:///server1:50051"),
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
