package main

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// runDemo is a narrated tour of the fully secured setup: xDS discovery, mutual TLS and RS256 tokens
// together, and which layer stops which kind of bad call.
func runDemo(crud *GrpcCrud, issuer *Issuer, cfg Config, c *Checks) {
	k := cfg.KeyPrefix + "demo"
	pool, err := caPool(cfg.CAFile)
	if err != nil {
		c.abort(err)
		return
	}
	// get makes one Get on server1 through conn options of our choosing (for the layer checks).
	get := func(ctx context.Context, opts ...grpc.DialOption) error {
		conn, err := grpc.NewClient(cfg.GRPCTarget, opts...)
		if err != nil {
			return err
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err = pb.NewGemFireClient(conn).Get(ctx, &pb.GetRequest{RegionName: cfg.Region, Key: key(k)})
		return err
	}
	as := func(base context.Context, via, user, region, op string) (string, string) {
		_, info, err := crud.executeCtx(asUser(base, user), via, region, op, k, 64)
		return errorCode(err), strings.TrimSpace(info.Server + " " + message(err))
	}
	expect := func(what, want, got, detail string) { c.check(what, got == want, strings.TrimSpace(got+" "+detail)) }

	c.section("1. The layers")
	alice, err := issuer.token("alice")
	if err != nil {
		c.abort(err)
		return
	}
	c.note("xDS:   the locator lists the servers (05); the bootstrap reaches it over TLS.")
	c.note("mTLS:  client and servers present certificates from the demo CA (07).")
	c.note("token: every call carries an RS256 JWT from the issuer (08, with real keys): the issuer")
	c.note("       signs with its private key; the cluster holds only the public key. alice's token:")
	c.note("       " + claimsOf(alice))

	c.section("2. Layer by layer: what stops a call")
	_, err = grpc.NewClient(cfg.GRPCTarget, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(tokenCreds{issuer}))
	c.check("a token over plaintext: grpc-go won't even build the channel (PerRPCCredentials need TLS)",
		err != nil && strings.Contains(err.Error(), "transport"), errText(err))
	err = get(asUser(context.Background(), "alice"), grpc.WithTransportCredentials(tlsCreds(pool, nil)),
		grpc.WithPerRPCCredentials(tokenCreds{issuer}))
	c.check("a good token, but no client certificate: the TLS handshake is refused first",
		err != nil && strings.Contains(err.Error(), "certificate required"), errText(err))
	got, detail := as(context.Background(), "direct", "none", "orders", "Get")
	expect("a client certificate, but no token → UNAUTHENTICATED", "UNAUTHENTICATED", got, detail)
	got, detail = as(withTokenOverride(context.Background(), rogueToken("alice")), "direct", "alice", "orders", "Get")
	expect("a token from a rogue issuer (RS256 with someone else's key) → UNAUTHENTICATED", "UNAUTHENTICATED", got, detail)
	if forged, err := algSwapToken(os.Getenv("JWT_PUBLIC_KEY_FILE")); err == nil {
		got, detail = as(withTokenOverride(context.Background(), forged), "direct", "alice", "orders", "Get")
		expect("the 'alg swap' forgery (HS256 keyed with the public key) → UNAUTHENTICATED", "UNAUTHENTICATED", got, detail)
	}
	got, detail = as(context.Background(), "direct", "bob", "orders", "Put")
	expect("a good token without the permission (bob writing) → PERMISSION_DENIED", "PERMISSION_DENIED", got, detail)
	got, detail = as(context.Background(), "direct", "alice", "orders", "Put")
	expect("certificate, token and permission all good → OK", "OK", got, detail)

	c.section("3. xDS with everything on")
	seen, ok := map[string]int{}, 0
	for range 40 {
		_, info, err := crud.execute("xds", "alice", "orders", "Get", k, 0)
		if err != nil {
			c.abort(err)
			return
		}
		seen[info.Server]++
		if info.Security == "tls" {
			ok++
		}
	}
	c.check("40 calls as alice over xds reach both servers", seen["server1"] > 0 && seen["server2"] > 0, seen)
	c.check("…all over mutual TLS, all authorized", ok == 40, ok)
	denied := map[string]bool{}
	for range 4 {
		_, info, err := crud.execute("xds", "bob", "orders", "Put", k, 64)
		if errorCode(err) == "PERMISSION_DENIED" {
			denied[info.Server] = true
		}
	}
	c.check("bob's writes over xds are denied on both servers: every member runs the SecurityManager",
		denied["server1"] && denied["server2"], denied)
	c.note("The xds channel itself needs no token: the locator's xDS endpoint doesn't ask for one.")

	c.section("4. Bad tokens over xDS")
	expired, err := issuer.expired("alice")
	if err != nil {
		c.abort(err)
		return
	}
	got, detail = as(withTokenOverride(context.Background(), expired), "xds", "alice", "orders", "Get")
	expect("a genuine token that has expired → UNAUTHENTICATED", "UNAUTHENTICATED", got, detail)
	bob, _ := issuer.token("bob")
	got, detail = as(withTokenOverride(context.Background(), tamper(bob)), "xds", "bob", "orders", "Put")
	expect("bob's token with perms edited to *:* (signature unchanged) → UNAUTHENTICATED", "UNAUTHENTICATED", got, detail)

	c.section("5. Where each check happens")
	c.note("TLS handshake (server)      no client certificate from the demo CA → connection refused")
	c.note("authenticate (any server)   no token, expired, forged, wrong key or wrong alg → UNAUTHENTICATED")
	c.note("authorize (any server)      the token's perms don't imply DATA:READ/WRITE:<region>:<key> → PERMISSION_DENIED")
	c.note("PerRPCCredentials (client)  won't send a token over plaintext at all")
}

// rogueToken is a well-formed RS256 token for user with every permission, signed by a key the
// cluster has never seen: what someone running their own "issuer" would send.
func rogueToken(user string) string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := jwtPart(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		jwtPart(map[string]any{"sub": user, "exp": time.Now().Add(time.Hour).Unix(), "perms": []string{"*:*"}})
	digest := sha256.Sum256([]byte(body))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// algSwapToken is the classic forgery against verifiers that trust the token's own "alg": an HS256
// token whose HMAC key is the issuer's PUBLIC key (which anyone can have). A verifier that only
// accepts RS256 rejects it.
func algSwapToken(publicKeyFile string) (string, error) {
	pub, err := os.ReadFile(publicKeyFile)
	if err != nil {
		return "", err
	}
	body := jwtPart(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." +
		jwtPart(map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix(), "perms": []string{"*:*"}})
	mac := hmac.New(sha256.New, pub)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// tamper swaps a token's payload for one granting everything, keeping the original signature.
func tamper(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token
	}
	widened := jwtPart(map[string]any{"sub": "bob", "exp": time.Now().Add(time.Hour).Unix(), "perms": []string{"*:*"}})
	return parts[0] + "." + widened + "." + parts[2]
}

func jwtPart(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// message is an RPC error's description in parentheses ("" for success).
func message(err error) string {
	if err == nil {
		return ""
	}
	return "(" + status.Convert(err).Message() + ")"
}

func errText(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

func statusText(err error) string { return errorCode(err) + " " + message(err) }

var _ = fmt.Sprintf
