package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var userNames = []string{"alice", "bob", "carol", "none"}

// Issuer fetches the users' tokens from the issuer service (ISSUER_URL), the identity provider
// stand-in that holds the signing key. Tokens are reused until a minute before they expire.
type Issuer struct {
	url   string
	http  *http.Client
	mu    sync.Mutex
	cache map[string]issued
}

type issued struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
}

func newIssuer(url string) *Issuer {
	return &Issuer{url: url, http: &http.Client{Timeout: 5 * time.Second}, cache: map[string]issued{}}
}

// token returns a valid token for user.
func (i *Issuer) token(user string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if t, ok := i.cache[user]; ok && time.Until(time.Unix(t.Expires, 0)) > time.Minute {
		return t.Token, nil
	}
	t, err := i.fetch(user, "")
	if err != nil {
		return "", err
	}
	i.cache[user] = t
	return t.Token, nil
}

// expired asks the issuer for a correctly signed token that expired a minute ago.
func (i *Issuer) expired(user string) (string, error) {
	t, err := i.fetch(user, "&ttl=-60")
	return t.Token, err
}

func (i *Issuer) fetch(user, extra string) (issued, error) {
	var t issued
	resp, err := i.http.Get(i.url + "/token?user=" + user + extra)
	if err != nil {
		return t, fmt.Errorf("issuer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return t, fmt.Errorf("issuer: %s for user %s", resp.Status, user)
	}
	return t, json.NewDecoder(resp.Body).Decode(&t)
}

// Context values: who a call acts as, and (for the demo's bad tokens) a token to send instead.
type userKey struct{}
type tokenKey struct{}

func asUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func withTokenOverride(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

func userOf(ctx context.Context) string {
	if u, ok := ctx.Value(userKey{}).(string); ok {
		return u
	}
	return "unknown"
}

// tokenCreds puts `authorization: Bearer <token>` on every call: grpc-go asks it for each RPC,
// with that call's context, so one connection can carry calls for different users.
type tokenCreds struct{ issuer *Issuer }

func (t tokenCreds) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	tok, _ := ctx.Value(tokenKey{}).(string)
	if tok == "" {
		user := userOf(ctx)
		if user == "none" || user == "unknown" {
			return nil, nil // no token at all
		}
		var err error
		if tok, err = t.issuer.token(user); err != nil {
			return nil, err
		}
	}
	return map[string]string{"authorization": "Bearer " + tok}, nil
}

// RequireTransportSecurity: grpc-go refuses to send these credentials over a plaintext connection.
func (tokenCreds) RequireTransportSecurity() bool { return true }

// claimsOf shows a token's header and payload (for the demo's log).
func claimsOf(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "?"
	}
	var out []string
	for _, p := range parts[:2] {
		b, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil {
			return "?"
		}
		out = append(out, string(b))
	}
	return strings.Join(out, " ")
}
