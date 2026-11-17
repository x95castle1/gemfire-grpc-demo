package main

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/stats"
)

// channels are three ways of handling the same failure, each a separate ClientConn to server1:
//
//	failfast      the grpc-go default: if server1 can't be reached, the call fails at once (UNAVAILABLE)
//	retry         a retry policy in the service config: UNAVAILABLE is retried, with backoff, up to
//	              5 attempts in all, on the idempotent methods only (retryPolicy below)
//	waitforready  WaitForReady(true): the call waits for server1 to be reachable, up to its deadline
var channels = []string{"failfast", "retry", "waitforready"}

// callDeadline is every call's deadline. It's what bounds waitforready: a call waits for a
// connection for up to this long, then fails with DEADLINE_EXCEEDED.
const callDeadline = 30 * time.Second

// retryPolicy is the retry channel's service config. Only methods that are safe to repeat are
// listed. A retry happens when an attempt failed, and an attempt that failed may still have been
// applied (UNAVAILABLE can mean the connection dropped after the server acted). Repeating a Get,
// Put, ContainsKey or EntryCount gives the same result; repeating a PutIfAbsent, Remove, ReplaceIf
// or GetAndPut may not (the first attempt's effect changes the second attempt's answer).
const retryPolicy = `{
  "methodConfig": [{
    "name": [
      {"service": "gemfire.v1.GemFire", "method": "Get"},
      {"service": "gemfire.v1.GemFire", "method": "Put"},
      {"service": "gemfire.v1.GemFire", "method": "ContainsKey"},
      {"service": "gemfire.v1.GemFire", "method": "EntryCount"},
      {"service": "gemfire.v1.GemFire", "method": "Query"},
      {"service": "gemfire.v1.GemFire", "method": "GetStream"},
      {"service": "gemfire.v1.GemFire", "method": "KeySetStream"}
    ],
    "retryPolicy": {
      "maxAttempts": 5,
      "initialBackoff": "0.5s",
      "maxBackoff": "4s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// reconnect caps grpc-go's reconnect backoff at 5 s (the default grows to 120 s). After an outage a
// channel tries server1 again at least every 5 s, so it notices quickly that server1 is back.
var reconnect = grpc.WithConnectParams(grpc.ConnectParams{
	Backoff:           backoff.Config{BaseDelay: time.Second, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 5 * time.Second},
	MinConnectTimeout: 5 * time.Second,
})

// dial opens one channel of the given kind to target, timed by the interceptors and with every
// attempt counted.
func dial(kind, label, target string, m *Metrics) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(m.unaryInterceptor(label)),
		grpc.WithChainStreamInterceptor(m.streamInterceptor(label)),
		grpc.WithStatsHandler(&attempts{channel: label, metrics: m}),
		reconnect,
	}
	switch kind {
	case "retry":
		opts = append(opts, grpc.WithDefaultServiceConfig(retryPolicy))
	case "waitforready":
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.WaitForReady(true)))
	}
	return grpc.NewClient(target, opts...)
}

// attempts counts every attempt of every call, retries included, into demo_call_attempts_total and
// into the call's own counter (withAttempts). Interceptors see a call once, however many attempts
// it took; a stats handler sees each attempt.
//
// It counts stats.End: grpc-go starts (stats.Begin) an extra attempt for each retry that is
// abandoned without ending, so counting Begin would count each retry twice.
type attempts struct {
	channel string
	metrics *Metrics
}

type attemptsKey struct{}

// withAttempts returns a context whose calls count their attempts into n.
func withAttempts(ctx context.Context, n *atomic.Int32) context.Context {
	return context.WithValue(ctx, attemptsKey{}, n)
}

func (a *attempts) HandleRPC(ctx context.Context, s stats.RPCStats) {
	if _, ok := s.(*stats.End); ok {
		a.metrics.recordAttempt(a.channel)
		if n, ok := ctx.Value(attemptsKey{}).(*atomic.Int32); ok {
			n.Add(1)
		}
	}
}
func (a *attempts) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (a *attempts) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (a *attempts) HandleConn(context.Context, stats.ConnStats)                       {}
