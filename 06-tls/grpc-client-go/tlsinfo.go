package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// caPool reads the CA certificate(s) the client trusts (TLS_CA_FILE: the demo CA, ./certs/ca.pem).
func caPool(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates in %s", file)
	}
	return pool, nil
}

// tlsCreds verifies the server's certificate against the CA pool and the hostname dialed.
func tlsCreds(pool *x509.CertPool) credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
}

// CallInfo is what a call learned about the connection it used: the server that answered and,
// over TLS, the protocol version, cipher suite and the certificate the server presented.
type CallInfo struct {
	Server   string
	Security string // "tls", "insecure", or "none" when the call never reached a server
	Version  string // "TLS 1.3"
	Cipher   string
	Cert     *x509.Certificate
}

func callInfo(p *peer.Peer, servers *Servers) CallInfo {
	ci := CallInfo{Server: servers.name(p.Addr), Security: "insecure"}
	if p.Addr == nil {
		ci.Security = "none" // the call never reached a server (e.g. UNAVAILABLE during a failover)
	} else if t, ok := p.AuthInfo.(credentials.TLSInfo); ok {
		ci.Security = "tls"
		ci.Version = tls.VersionName(t.State.Version)
		ci.Cipher = tls.CipherSuiteName(t.State.CipherSuite)
		if len(t.State.PeerCertificates) > 0 {
			ci.Cert = t.State.PeerCertificates[0]
		}
	} else if p.AuthInfo != nil {
		ci.Security = fmt.Sprintf("%T", p.AuthInfo) // shouldn't happen; shows up in metrics if it does
	}
	return ci
}

// describe renders a certificate for the demo's log: subject, issuer, SANs and expiry.
func describe(c *x509.Certificate) string {
	if c == nil {
		return "no certificate"
	}
	sans := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		sans = append(sans, ip.String())
	}
	return fmt.Sprintf("subject %q, issued by %q, SANs [%s], expires %s",
		c.Subject.CommonName, c.Issuer.CommonName, strings.Join(sans, " "), c.NotAfter.Format("2006-01-02"))
}

// readCert reads the first certificate in a PEM file.
func readCert(file string) (*x509.Certificate, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", file)
	}
	return x509.ParseCertificate(block.Bytes)
}
