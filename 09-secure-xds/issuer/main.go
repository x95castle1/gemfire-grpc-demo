// Command issuer stands in for an identity provider: it holds the RSA private key and hands out
// RS256 JWTs that say who a user is and what they may do in GemFire. The cluster holds only the
// public key, so it can check these tokens but never mint one.
//
//	GET /token?user=alice           → {"user":"alice","token":"eyJ…","expires":…}
//	GET /token?user=bob&ttl=-60     → a token that expired a minute ago (for the demo)
//	GET /users                      → who exists and their perms
//
// Env: JWT_PRIVATE_KEY_FILE (PEM, PKCS#8 or PKCS#1), PORT (default 8080).
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// The issuer decides who may do what: GemFire permission strings per user. The cluster's
// SecurityManager grants a call when one of them implies the permission the call needs.
var users = map[string][]string{
	"alice": {"DATA:READ", "DATA:WRITE"},               // everything, every region
	"bob":   {"DATA:READ"},                             // read anything, write nothing
	"carol": {"DATA:READ:orders", "DATA:WRITE:orders"}, // only the orders region
}

const defaultTTL = 10 * time.Minute

func main() {
	key, err := loadKey(os.Getenv("JWT_PRIVATE_KEY_FILE"))
	if err != nil {
		log.Fatalf("issuer: %v", err)
	}
	http.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		perms, ok := users[user]
		if !ok {
			http.Error(w, `{"error":"unknown user; try alice, bob or carol"}`, http.StatusNotFound)
			return
		}
		ttl := defaultTTL
		if s := r.URL.Query().Get("ttl"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				http.Error(w, `{"error":"ttl is seconds"}`, http.StatusBadRequest)
				return
			}
			ttl = time.Duration(n) * time.Second
		}
		exp := time.Now().Add(ttl).Unix()
		token, err := sign(key, map[string]any{"sub": user, "exp": exp, "perms": perms, "iss": "demo-issuer"})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user": user, "token": token, "expires": exp})
	})
	http.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(users)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("issuer: signing RS256 tokens on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// sign makes an RS256 JWT: base64url(header).base64url(claims).base64url(RSA-SHA256 signature).
func sign(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	header := b64([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := header + "." + b64(payload)
	digest := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return body + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func loadKey(file string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_FILE: %w", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", file)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, fmt.Errorf("%s is not an RSA key", file)
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
