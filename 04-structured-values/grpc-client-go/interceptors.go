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
)

// The interceptors time every gRPC call on the connection into demo_rpc_duration_seconds:
// unary and streaming alike, labeled with the method, the region and the final status code.
// This is the RPC-level view. demo_op_latency_seconds (recorded in api.go) stays the
// operation-level view (one /query call).

func (m *Metrics) unaryInterceptor(ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	start := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	m.recordRPC(path.Base(method), regionOf(req), errorCode(err), time.Since(start))
	return err
}

func (m *Metrics) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	start := time.Now()
	s, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		m.recordRPC(path.Base(method), "", errorCode(err), time.Since(start))
		return nil, err
	}
	return &timedStream{ClientStream: s, metrics: m, method: path.Base(method), start: start,
		serverStreams: desc.ServerStreams}, nil
}

// timedStream records the stream once it ends. A stream ends when RecvMsg returns an error
// (io.EOF means OK), or, for a client-streaming call such as PutStream, after its single
// response arrives.
type timedStream struct {
	grpc.ClientStream
	metrics       *Metrics
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
	default:
		s.metrics.recordMessage(s.method, "received") // one server-stream message, e.g. a QueryResponse
	}
	return err
}

func (s *timedStream) finish(err error) {
	s.once.Do(func() { s.metrics.recordRPC(s.method, s.region, errorCode(err), time.Since(s.start)) })
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
