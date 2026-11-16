package main

import (
	"context"
	"errors"
	"io"
	"path"
	"sync"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// The interceptors time every gRPC call on a channel into demo_rpc_duration_seconds (unary and
// streaming alike, labeled with the channel, method, region and final status code) and count it
// in demo_calls_total with the server that answered, read from the call's peer address.
// This is the RPC-level view. demo_op_latency_seconds (recorded in api.go) stays the
// operation-level view (one /crud call).

func (m *Metrics) unaryInterceptor(channel string, servers *Servers) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		var p peer.Peer
		err := invoker(ctx, method, req, reply, cc, append(opts, grpc.Peer(&p))...)
		m.recordRPC(channel, path.Base(method), regionOf(req), servers.name(p.Addr), errorCode(err), time.Since(start))
		return err
	}
}

func (m *Metrics) streamInterceptor(channel string, servers *Servers) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
		method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		start := time.Now()
		s, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			m.recordRPC(channel, path.Base(method), "", "none", errorCode(err), time.Since(start))
			return nil, err
		}
		return &timedStream{ClientStream: s, metrics: m, channel: channel, servers: servers,
			method: path.Base(method), start: start, serverStreams: desc.ServerStreams}, nil
	}
}

// timedStream records the stream once it ends. A stream ends when RecvMsg returns an error
// (io.EOF means OK), or, for a client-streaming call such as PutStream, after its single
// response arrives.
type timedStream struct {
	grpc.ClientStream
	metrics       *Metrics
	channel       string
	servers       *Servers
	method        string
	start         time.Time
	serverStreams bool
	region        string
	once          sync.Once
}

func (s *timedStream) SendMsg(msg any) error {
	if s.region == "" {
		s.region = regionOf(msg)
	}
	return s.ClientStream.SendMsg(msg)
}

func (s *timedStream) RecvMsg(msg any) error {
	err := s.ClientStream.RecvMsg(msg)
	switch {
	case errors.Is(err, io.EOF):
		s.finish(nil)
	case err != nil:
		s.finish(err)
	case !s.serverStreams:
		s.finish(nil) // client-streaming: the one response ends the call
	}
	return err
}

func (s *timedStream) finish(err error) {
	s.once.Do(func() {
		server := "unknown"
		if p, ok := peer.FromContext(s.Context()); ok {
			server = s.servers.name(p.Addr)
		}
		s.metrics.recordRPC(s.channel, s.method, s.region, server, errorCode(err), time.Since(s.start))
	})
}

// regionOf reads a request's region: region_name on unary requests, config.region_name on the
// first message of a stream, "" for Query (OQL names its regions in the query text).
func regionOf(msg any) string {
	if r, ok := msg.(interface{ GetRegionName() string }); ok {
		return r.GetRegionName()
	}
	if r, ok := msg.(interface{ GetConfig() *pb.StreamConfig }); ok {
		return r.GetConfig().GetRegionName()
	}
	return ""
}
