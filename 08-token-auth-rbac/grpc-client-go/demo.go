package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc/status"
)

// runDemo is a narrated tour of token authentication and per-region authorization: who gets in,
// who may do what where, and what bad tokens get back. Every call carries the token of the user
// it acts as; the SecurityManager on the cluster (security/) decides.
func runDemo(gemfire pb.GemFireClient, crud *GrpcCrud, tokens *Tokens, cfg Config, c *Checks) {
	k := cfg.KeyPrefix + "demo"
	// as runs op as user on region and returns the gRPC status code ("OK" on success).
	as := func(user, region, op string) (string, string) {
		_, err := crud.execute(user, region, op, k, 64)
		return errorCode(err), message(err)
	}
	query := func(user, oql string) (string, string) {
		ctx, cancel := context.WithTimeout(withToken(context.Background(), user, tokens.token(user)), 10*time.Second)
		defer cancel()
		stream, err := gemfire.Query(ctx, &pb.QueryRequest{Query: oql})
		for err == nil {
			_, err = stream.Recv()
		}
		if err == io.EOF {
			err = nil
		}
		return errorCode(err), message(err)
	}
	expect := func(what, want string, got, msg string) {
		c.check(what, got == want, strings.TrimSpace(got+" "+msg))
	}

	c.section("1. The tokens")
	for _, u := range []string{"alice", "bob", "carol"} {
		c.note(fmt.Sprintf("%-5s %s", u, decodePayload(tokens.token(u))))
	}
	c.note("HS256 JWTs signed with JWT_SECRET. The cluster's SecurityManager checks the signature and the")
	c.note("expiry, then grants a call when one of the token's perms implies the permission it needs:")
	c.note("Get/Query need DATA:READ, Put/Remove DATA:WRITE, GetAndPut and friends both, per region and key.")

	c.section("2. No token, no entry")
	got, msg := as("none", "orders", "Get")
	expect("a Get without a token → UNAUTHENTICATED", "UNAUTHENTICATED", got, msg)

	c.section("3. alice may do anything")
	got, msg = as("alice", "orders", "Put")
	expect("alice: Put on orders", "OK", got, msg)
	got, msg = as("alice", "customers", "Put")
	expect("alice: Put on customers", "OK", got, msg)
	got, msg = as("alice", "customers", "GetAndPut")
	expect("alice: GetAndPut on customers (needs READ and WRITE)", "OK", got, msg)

	c.section("4. bob is read-only")
	got, msg = as("bob", "customers", "Get")
	expect("bob: Get on customers", "OK", got, msg)
	got, msg = as("bob", "orders", "Put")
	expect("bob: Put on orders → PERMISSION_DENIED", "PERMISSION_DENIED", got, msg)
	got, msg = as("bob", "orders", "GetAndPut")
	expect("bob: GetAndPut → PERMISSION_DENIED (it needs WRITE too)", "PERMISSION_DENIED", got, msg)
	got, msg = query("bob", "SELECT * FROM /customers")
	expect("bob: a Query is a read, so it's allowed", "OK", got, msg)

	c.section("5. carol may only touch orders")
	got, msg = as("carol", "orders", "Put")
	expect("carol: Put on orders", "OK", got, msg)
	got, msg = as("carol", "customers", "Get")
	expect("carol: Get on customers → PERMISSION_DENIED", "PERMISSION_DENIED", got, msg)
	got, msg = query("carol", "SELECT * FROM /orders")
	expect("carol: SELECT * FROM /orders", "OK", got, msg)
	got, msg = query("carol", "SELECT * FROM /customers")
	expect("carol: SELECT * FROM /customers → PERMISSION_DENIED (the FROM region is checked)", "PERMISSION_DENIED", got, msg)

	c.section("6. Bad tokens")
	_, err := crud.executeWith("bob", tokens.expired("bob"), "orders", "Get", k, 0)
	expect("an expired token → UNAUTHENTICATED", "UNAUTHENTICATED", errorCode(err), message(err))
	_, err = crud.executeWith("bob", tokens.tampered("bob"), "orders", "Put", k, 64)
	expect("bob's token with perms edited to *:* (signature unchanged) → UNAUTHENTICATED", "UNAUTHENTICATED", errorCode(err), message(err))
	_, err = crud.executeWith("bob", tokens.otherKey("bob"), "orders", "Get", k, 0)
	expect("a token signed with a different key → UNAUTHENTICATED", "UNAUTHENTICATED", errorCode(err), message(err))

	c.section("7. Checked per call, not per connection")
	c.note("All of the above went over one gRPC connection. The token rides on each call, so the next")
	c.note("two calls on that same connection get different answers:")
	got, msg = as("alice", "orders", "Put")
	expect("alice: Put on orders", "OK", got, msg)
	got, msg = as("bob", "orders", "Put")
	expect("bob, right after, same connection: Put on orders → PERMISSION_DENIED", "PERMISSION_DENIED", got, msg)
}

// message is an RPC error's description ("" for success).
func message(err error) string {
	if err == nil {
		return ""
	}
	return "(" + status.Convert(err).Message() + ")"
}
