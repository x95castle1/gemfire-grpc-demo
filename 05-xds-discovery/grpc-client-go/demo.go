package main

import (
	"fmt"
	"os"
	"strings"
)

// runDemo is a narrated tour of xDS discovery: the same calls over the direct channel
// (dns:///server1:50051) and the xds channel (xds:///gemfire_grpc), checking which server
// answered each one. Failover needs a server to stop, so it's the xds/failover load test instead.
func runDemo(crud *GrpcCrud, cfg Config, c *Checks) {
	key := func(i int) string { return fmt.Sprintf("%sdemo-%d", cfg.KeyPrefix, i) }
	// calls makes n Gets over a channel and counts the servers that answered.
	calls := func(channel string, n int) (map[string]int, []string, error) {
		seen, order := map[string]int{}, []string{}
		for i := range n {
			_, server, err := crud.execute(channel, "Get", key(i), 0)
			if err != nil {
				return nil, nil, err
			}
			seen[server]++
			order = append(order, server)
		}
		return seen, order, nil
	}

	c.section("1. Two channels to the same cluster")
	c.note("direct: " + cfg.GRPCTarget + "  (as in 01-04: DNS gives one address, server1)")
	c.note("xds:    " + cfg.XDSTarget + "  (grpc-go asks an xDS server which servers exist)")
	if b, err := os.ReadFile(os.Getenv("GRPC_XDS_BOOTSTRAP")); err == nil {
		c.note("The bootstrap file ($GRPC_XDS_BOOTSTRAP) points grpc-go at the locator, the xDS server:")
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			c.note("  " + line)
		}
	}
	for i := range 40 {
		if _, _, err := crud.execute("direct", "Put", key(i), 100); err != nil {
			c.abort(err)
			return
		}
	}

	c.section("2. xDS discovers both servers")
	direct, _, err := calls("direct", 40)
	if err != nil {
		c.abort(err)
		return
	}
	c.check("40 Gets over direct all reach server1", direct["server1"] == 40, direct)
	xds, order, err := calls("xds", 40)
	if err != nil {
		c.abort(err)
		return
	}
	c.check("40 Gets over xds reach both server1 and server2", xds["server1"] > 0 && xds["server2"] > 0, xds)

	c.section("3. Round robin")
	alternates := true
	for i := 1; i < len(order); i++ {
		alternates = alternates && order[i] != order[i-1]
	}
	c.check("consecutive xds calls alternate between the servers", alternates, strings.Join(order[:6], " ")+" …")
	c.check("so each server gets half", xds["server1"] == 20 && xds["server2"] == 20, xds)
	c.note("Strict alternation: round robin over the gRPC servers the locator reported.")

	c.section("4. One cluster, whichever way in")
	_, wrote, err := crud.execute("direct", "Put", key(999), 4321)
	if err != nil {
		c.abort(err)
		return
	}
	got := map[string]bool{}
	ok := true
	for range 4 {
		size, server, err := crud.execute("xds", "Get", key(999), 0)
		if err != nil {
			c.abort(err)
			return
		}
		got[server] = true
		ok = ok && size == 4321
	}
	c.check(fmt.Sprintf("a value Put over direct (to %s) reads back over xds from both servers", wrote), ok && len(got) == 2, "4321 bytes")

	c.section("5. Balanced is not partition-aware")
	c.note("Round robin picks the next server, not the one that holds the key's primary bucket. In this")
	c.note("example every server has a copy of every bucket (PARTITION_REDUNDANT on two servers), so a Get")
	c.note("is answered locally wherever it lands, but a Put must go to the primary: about half of them are")
	c.note("forwarded to the other server. The difference from direct is that the work is shared by both")
	c.note("servers, and losing one doesn't take the client down. The dashboard shows the forwarding.")
	c.note("Failover (stopping server1 under load): make select-load-test EX=05 SCRIPT=xds/failover")
}
