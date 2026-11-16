package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"
)

// The demo's users and the GemFire permissions their tokens carry. The SecurityManager on the
// cluster grants a call when any of these implies the permission it needs (RESOURCE:OPERATION
// [:REGION[:KEY]]). "none" sends no token at all.
var users = map[string][]string{
	"alice": {"DATA:READ", "DATA:WRITE"},                  // everything, every region
	"bob":   {"DATA:READ"},                                // read anything, write nothing
	"carol": {"DATA:READ:orders", "DATA:WRITE:orders"},    // only the orders region
}

var userNames = []string{"alice", "bob", "carol", "none"}

const tokenTTL = 10 * time.Minute

// Tokens mints HS256 JWTs for the demo users. In real use an identity provider does this; here
// the client plays that part, with the same key the SecurityManager checks against (JWT_SECRET).
type Tokens struct {
	secret []byte
	mu     sync.Mutex
	cache  map[string]cachedToken
}

type cachedToken struct {
	token string
	exp   time.Time
}

type claims struct {
	Sub   string   `json:"sub"`
	Exp   int64    `json:"exp"`
	Perms []string `json:"perms"`
}

func newTokens(secret string) *Tokens {
	return &Tokens{secret: []byte(secret), cache: map[string]cachedToken{}}
}

// token returns a valid token for user ("" for "none"), reusing one until a minute before it expires.
func (t *Tokens) token(user string) string {
	perms, ok := users[user]
	if !ok {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.cache[user]; ok && time.Until(c.exp) > time.Minute {
		return c.token
	}
	exp := time.Now().Add(tokenTTL)
	tok := sign(t.secret, claims{Sub: user, Exp: exp.Unix(), Perms: perms})
	t.cache[user] = cachedToken{tok, exp}
	return tok
}

// expired is a correctly signed token that expired a minute ago.
func (t *Tokens) expired(user string) string {
	return sign(t.secret, claims{Sub: user, Exp: time.Now().Add(-time.Minute).Unix(), Perms: users[user]})
}

// tampered is user's token with the payload swapped for one that grants everything, keeping the
// original signature: what someone editing a token by hand would send.
func (t *Tokens) tampered(user string) string {
	parts := strings.Split(t.token(user), ".")
	widened, _ := json.Marshal(claims{Sub: user, Exp: time.Now().Add(tokenTTL).Unix(), Perms: []string{"*:*"}})
	return parts[0] + "." + b64(widened) + "." + parts[2]
}

// otherKey is a valid-looking token for user signed with a key the cluster doesn't know.
func (t *Tokens) otherKey(user string) string {
	return sign([]byte("some-other-key"), claims{Sub: user, Exp: time.Now().Add(tokenTTL).Unix(), Perms: users[user]})
}

func sign(secret []byte, c claims) string {
	header := b64([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(c)
	body := header + "." + b64(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return body + "." + b64(mac.Sum(nil))
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// decodePayload shows a token's claims (for the demo's log).
func decodePayload(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "?"
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "?"
	}
	return string(b)
}

// userKey carries the acting user in a call's context, for the interceptor's metrics.
type userKey struct{}

// withToken returns ctx carrying user (for metrics) and, unless the token is empty, the
// `authorization: Bearer <token>` metadata the server authenticates. It goes on every call: the
// server checks the token per call, so one connection can serve every user.
func withToken(ctx context.Context, user, token string) context.Context {
	ctx = context.WithValue(ctx, userKey{}, user)
	if token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

func userOf(ctx context.Context) string {
	if u, ok := ctx.Value(userKey{}).(string); ok {
		return u
	}
	return "unknown"
}
