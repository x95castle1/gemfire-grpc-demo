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
	GRPCTarget   string // the direct channel: dns:///server1:50051
	XDSTarget    string // the xds channel: xds:///gemfire_grpc (needs GRPC_XDS_BOOTSTRAP)
	CAFile       string // the CA the client trusts for TLS (TLS_CA_FILE)
	CertFile     string // the client's certificate (TLS_CERT_FILE), presented to the servers
	KeyFile      string // and its private key (TLS_KEY_FILE)
	XDSAuthority string // the hostname the xds channel's certificates are checked against
	IssuerURL    string // where the users' tokens come from (ISSUER_URL)
}

func configFromEnv() Config {
	return Config{
		Client:       "grpc-go",
		Example:      env("EXAMPLE", "09-secure-xds"),
		Region:       env("REGION", "orders"),
		DemoRegion:   env("DEMO_REGION", "demo-grpc-go"),
		KeyPrefix:    env("KEY_PREFIX", "grpc-go:"),
		PayloadBytes: envInt("PAYLOAD_BYTES", 1024),
		Port:         envInt("PORT", 8080),
		HTTPThreads:  envInt("HTTP_THREADS", 64),
		DemoOnStart:  env("DEMO_ON_START", "true") == "true",
		GRPCTarget:   env("GRPC_TARGET", "dns:///server1:50051"),
		XDSTarget:    env("GRPC_XDS_TARGET", "xds:///gemfire_grpc"),
		CAFile:       env("TLS_CA_FILE", "/certs/ca.pem"),
		CertFile:     env("TLS_CERT_FILE", "/certs/client.pem"),
		KeyFile:      env("TLS_KEY_FILE", "/certs/client-key.pem"),
		XDSAuthority: os.Getenv("GRPC_XDS_AUTHORITY"),
		IssuerURL:    env("ISSUER_URL", "http://issuer:8080"),
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
