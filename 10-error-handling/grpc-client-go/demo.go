package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// slowQuery is a cross product of orders with itself: with 10,000 entries that's 100 million
// pairs, far more than the server's MAX_QUERY_EXECUTION_TIME allows.
const slowQuery = "SELECT COUNT(*) FROM /orders a, /orders b"

// runDemo is a narrated tour of the errors a GemFire for gRPC client meets and what to do about
// each: the status codes, deadlines, retries, and low memory. Failover (a server stopping under
// load) is the errors/retries load test instead.
func runDemo(crud *GrpcCrud, conns map[string]*grpc.ClientConn, cfg Config, c *Checks) {
	gemfire := crud.clients["failfast"]
	ctx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), callDeadline)
	}
	k := func(s string) *pb.Key { return key(cfg.KeyPrefix + s) }
	// expect checks that err has the given code and says what the client should do about it.
	expect := func(what string, err error, want codes.Code) {
		st := status.Convert(err)
		c.check(fmt.Sprintf("%s → %s", what, codeNames[want]), st.Code() == want, fmt.Sprintf("%s %q", codeNames[st.Code()], st.Message()))
		if st.Code() == want && want != codes.OK {
			c.note("what to do: " + classify(err))
		}
	}

	if _, err := crud.preload(10000, 100); err != nil {
		c.abort(err)
		return
	}

	// ── 1 ───────────────────────────────────────────────────────────────────────────────────
	c.section("1. The status code catalog")
	c.note("Every failed call returns a status: a code and a message. The code says what kind of")
	c.note("problem it is, and so what to do. In Go: status.Code(err), status.Convert(err).Message().")
	var details int
	track := func(err error) error { details += len(status.Convert(err).Details()); return err }

	cx, cancel := ctx()
	_, err := gemfire.Get(cx, &pb.GetRequest{RegionName: cfg.Region})
	expect("a Get with no key", track(err), codes.InvalidArgument)
	_, err = gemfire.Put(cx, &pb.PutRequest{RegionName: cfg.Region, Key: k("no-value")})
	expect("a Put with no value", track(err), codes.InvalidArgument)
	expect("an empty query", track(drainQuery(cx, gemfire, "")), codes.InvalidArgument)
	expect("OQL that doesn't parse (SELEKT)", track(drainQuery(cx, gemfire, "SELEKT * FROM /orders")), codes.InvalidArgument)
	_, err = gemfire.Get(cx, &pb.GetRequest{RegionName: "missing", Key: k("1")})
	expect("a Get on a region that doesn't exist", track(err), codes.FailedPrecondition)
	expect("a query FROM a region that doesn't exist", track(drainQuery(cx, gemfire, "SELECT * FROM /missing")), codes.FailedPrecondition)
	expect("a query whose column is UNDEFINED (o.nope)", track(drainQuery(cx, gemfire, "SELECT o.nope FROM /orders o")), codes.Unimplemented)
	err = conns["failfast"].Invoke(cx, "/gemfire.v1.GemFire/Nope", &pb.GetRequest{}, &pb.GetResponse{})
	expect("a method the server doesn't have", track(err), codes.Unimplemented)
	_, err = gemfire.Get(cx, &pb.GetRequest{RegionName: "", Key: k("1")})
	expect(`a Get with an empty region name ("")`, track(err), codes.Internal)
	c.note("An empty region name is a mistake in the request, but the server reports INTERNAL.")
	_, err = gemfire.Put(cx, &pb.PutRequest{RegionName: cfg.Region, Key: k("big"), Value: crud.payload(5 << 20)})
	expect("a 5 MB Put (over the 4 MB message limit)", track(err), codes.ResourceExhausted)
	cancel()
	_, _, err = crud.execute("unreachable/failfast", cfg.Region, "Get", cfg.KeyPrefix+"1", 0)
	expect("a Get to "+cfg.Unreachable+", where no server is running", track(err), codes.Unavailable)
	c.check("no status carried error details: the code and message are all there is", details == 0, fmt.Sprintf("%d details", details))

	c.note("Not every miss is an error:")
	got, _, err := crud.execute("failfast", cfg.Region, "Get", cfg.KeyPrefix+"never-written", 0)
	c.check("a Get of a key that isn't there is OK, with no value", err == nil && got == nil, got)
	got, _, err = crud.execute("failfast", cfg.Region, "PutIfAbsent", cfg.KeyPrefix+"0", 100)
	c.check("a PutIfAbsent on a key that exists is OK, inserted=false", err == nil && got == false, got)

	// ── 2 ───────────────────────────────────────────────────────────────────────────────────
	c.section("2. Deadlines")
	c.note("A deadline is how long the client will wait. Two things can stop a slow call: the")
	c.note(fmt.Sprintf("client's deadline, and the server's own query limit (MAX_QUERY_EXECUTION_TIME=%s ms here).", cfg.MaxQueryExecutionTime))
	c.note("The slow query: " + slowQuery + " (10,000 × 10,000 pairs)")

	before, metricErr := serverQueryTime()
	cx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	start := time.Now()
	err = drainQuery(cx, gemfire, slowQuery)
	took := time.Since(start)
	cancel()
	expect("the slow query with a 200 ms client deadline", err, codes.DeadlineExceeded)
	c.check("the client gave up at its deadline", took < time.Second, took.Round(time.Millisecond))
	if metricErr == nil {
		spent := waitForQueryTime(before, 20*time.Second)
		c.check("but server1 kept running the query until its own limit (gemfire_queryExecutionTime)",
			spent > 1500*time.Millisecond, fmt.Sprintf("server1 spent %v", spent.Round(100*time.Millisecond)))
		c.note("Cancelling a call doesn't stop the server's work. Only the server's limit does.")
	} else {
		c.note("(couldn't read server1's metrics: " + metricErr.Error() + ")")
	}

	cx, cancel = ctx()
	start = time.Now()
	err = drainQuery(cx, gemfire, slowQuery)
	took = time.Since(start)
	cancel()
	expect(fmt.Sprintf("the same query with a %v deadline", callDeadline), err, codes.DeadlineExceeded)
	c.check("this time the server stopped it, at its limit", took > 1500*time.Millisecond && took < 10*time.Second &&
		strings.Contains(status.Convert(err).Message(), "max execution time"), took.Round(time.Millisecond))
	c.note("Same code, two sources: 'context deadline exceeded' is the client's; the server's says so.")

	cx, cancel = ctx()
	keys, err := cancelKeySet(cx, cancel, gemfire, cfg.Region)
	cancel()
	expect("a KeySetStream the client cancels after its first message", err, codes.Canceled)
	c.check("the keys received before the cancel are still there", keys > 0, fmt.Sprintf("%d keys", keys))
	c.note("Always set a deadline. Without one, a call on a waitforready channel can wait forever.")

	// ── 3 ───────────────────────────────────────────────────────────────────────────────────
	c.section("3. Retries, wait-for-ready and fail-fast")
	c.note("The same Get on three channels to " + cfg.Unreachable + ", where no server is running:")
	type try struct {
		channel  string
		deadline time.Duration
		code     codes.Code
		attempts func(int) bool
		took     func(time.Duration) bool
	}
	for _, t := range []try{
		{"failfast", callDeadline, codes.Unavailable, func(n int) bool { return n == 1 }, func(d time.Duration) bool { return d < time.Second }},
		{"retry", callDeadline, codes.Unavailable, func(n int) bool { return n == 5 }, func(d time.Duration) bool { return d > time.Second && d < 15*time.Second }},
		{"waitforready", 2 * time.Second, codes.DeadlineExceeded, func(n int) bool { return n == 1 }, func(d time.Duration) bool { return d >= 2*time.Second && d < 3*time.Second }},
	} {
		cx, cancel := context.WithTimeout(context.Background(), t.deadline)
		start := time.Now()
		_, attempts, err := crud.executeCtx(cx, "unreachable/"+t.channel, cfg.Region, "Get", cfg.KeyPrefix+"1", 0)
		took := time.Since(start)
		cancel()
		c.check(fmt.Sprintf("%-12s → %s", t.channel, codeNames[t.code]),
			status.Code(err) == t.code && t.attempts(attempts) && t.took(took),
			fmt.Sprintf("%s after %d attempt(s), %v", codeNames[status.Code(err)], attempts, took.Round(100*time.Microsecond)))
	}
	c.note("failfast:     the grpc-go default. No connection, so it fails at once.")
	c.note("retry:        5 attempts with backoff (0.5 s, 1 s, 2 s, 4 s, randomized), then the last error.")
	c.note("waitforready: no attempt fails for lack of a connection; it waits, and the deadline ends it.")
	c.note(`The retry policy (service config, channels.go): "maxAttempts": 5, "initialBackoff": "0.5s",`)
	c.note(`"maxBackoff": "4s", "backoffMultiplier": 2, "retryableStatusCodes": ["UNAVAILABLE"]`)
	_, attempts, err := crud.execute("unreachable/retry", cfg.Region, "PutIfAbsent", cfg.KeyPrefix+"1", 100)
	c.check("PutIfAbsent isn't in the policy, so the retry channel tries it once", status.Code(err) == codes.Unavailable && attempts == 1,
		fmt.Sprintf("%d attempt(s)", attempts))
	c.note("Only idempotent methods are retried: Get, Put, ContainsKey, EntryCount, Query, GetStream,")
	c.note("KeySetStream. If an attempt was applied but its answer lost, a retried PutIfAbsent says")
	c.note("inserted=false and a retried Remove says removed=false: wrong answers, not errors.")
	c.note("A server stopping under load: make select-load-test EX=10 SCRIPT=errors/retries")

	// ── 4 ───────────────────────────────────────────────────────────────────────────────────
	c.section("4. Low memory")
	c.note(fmt.Sprintf("Servers refuse writes once their heap is %s%% full (--critical-heap-percentage).", cfg.CriticalHeapPercentage))
	c.note("Filling the ballast region with 1 MB values until they do:")
	written, refused := 0, error(nil)
	start = time.Now()
	for i := range 3000 {
		if _, _, err := crud.execute("failfast", "ballast", "Put", strconv.Itoa(i), 1<<20); err != nil {
			refused = err
			break
		}
		written++
	}
	defer func() { // whatever happens below, leave the servers with their memory back
		for i := range written + 1 {
			crud.execute("failfast", "ballast", "Remove", strconv.Itoa(i), 0)
		}
	}()
	expect(fmt.Sprintf("a 1 MB Put after %d of them (%v)", written, time.Since(start).Round(100*time.Millisecond)), refused, codes.ResourceExhausted)
	c.check("the message says why: a member is running low on memory",
		strings.Contains(status.Convert(refused).Message(), "low on memory"), nil)
	small := 0
	for i := range 20 {
		if _, _, err := crud.execute("failfast", cfg.Region, "Put", cfg.KeyPrefix+strconv.Itoa(i), 100); status.Code(err) == codes.ResourceExhausted {
			small++
		}
	}
	c.check("small Puts to orders are refused too: each one also writes orders' redundant copy on server1", small == 20, fmt.Sprintf("%d of 20 refused", small))
	c.note("Low memory is per member, not per region: every write that touches server1 is refused.")
	_, _, err = crud.execute("failfast", "ballast", "Get", "0", 0)
	c.check("reads still work", err == nil, codeNames[status.Code(err)])
	// GemFire publishes its heap statistics every 5 s here: stay full long enough for the
	// dashboard's heap panel to see it.
	time.Sleep(6 * time.Second)
	c.note("Same code as the 5 MB Put, opposite advice: that one never succeeds, this one will after")
	c.note("the server frees memory. Only the message tells them apart (classify in statuses.go).")

	start = time.Now()
	for i := range written + 1 {
		crud.execute("failfast", "ballast", "Remove", strconv.Itoa(i), 0)
	}
	recovered := false
	for backoff := 250 * time.Millisecond; time.Since(start) < 60*time.Second; backoff = min(backoff*2, 4*time.Second) {
		if _, _, err := crud.execute("failfast", "ballast", "Put", "after", 1<<20); err == nil {
			recovered = true
			break
		}
		time.Sleep(backoff)
	}
	c.check("after clearing ballast, a Put retried with backoff succeeds", recovered, time.Since(start).Round(100*time.Millisecond))
	crud.execute("failfast", "ballast", "Remove", "after", 0)
}

// drainQuery runs an OQL query and reads every response message; it returns the call's status.
func drainQuery(ctx context.Context, gemfire pb.GemFireClient, oql string) error {
	stream, err := gemfire.Query(ctx, &pb.QueryRequest{Query: oql})
	if err != nil {
		return err
	}
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// cancelKeySet starts a KeySetStream, cancels it after the first message, and returns how many
// keys arrived and the status the stream ended with.
func cancelKeySet(ctx context.Context, cancel context.CancelFunc, gemfire pb.GemFireClient, region string) (int, error) {
	stream, err := gemfire.KeySetStream(ctx, &pb.KeySetStreamRequest{RegionName: region})
	if err != nil {
		return 0, err
	}
	keys := 0
	for {
		r, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return keys, nil
			}
			return keys, err
		}
		keys += len(r.GetKeys())
		cancel()
	}
}

// serverQueryTime reads server1's total query execution time (CachePerfStats queryExecutionTime,
// in nanoseconds) from its Prometheus endpoint.
func serverQueryTime() (time.Duration, error) {
	resp, err := http.Get("http://server1:7070/metrics/")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "gemfire_queryExecutionTime{") && strings.Contains(line, `instance="cachePerfStats"`) {
			f, err := strconv.ParseFloat(line[strings.LastIndexByte(line, ' ')+1:], 64)
			return time.Duration(f), err
		}
	}
	return 0, errors.New("gemfire_queryExecutionTime not found")
}

// waitForQueryTime polls server1's query time until it moves past before (GemFire refreshes its
// metrics every few seconds), and returns by how much.
func waitForQueryTime(before, limit time.Duration) time.Duration {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(time.Second) {
		if now, err := serverQueryTime(); err == nil && now > before {
			return now - before
		}
	}
	return 0
}
