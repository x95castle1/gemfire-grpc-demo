package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// GrpcCrud runs the 13 unary operations for the HTTP API, over any of the channels.
type GrpcCrud struct {
	clients map[string]pb.GemFireClient // by channel (channels.go), plus the demo's unreachable/*
	cfg     Config
	// One payload per size. Every write of a given size uses the same bytes, so ReplaceIf and
	// RemoveIf succeed whenever the key exists.
	payloads sync.Map // int -> *pb.Value
}

func newGrpcCrud(clients map[string]pb.GemFireClient, cfg Config) *GrpcCrud {
	return &GrpcCrud{clients: clients, cfg: cfg}
}

// execute runs one operation on key in region with a size-byte value (for writes) over the given
// channel, with callDeadline. It returns the result (the value's size in bytes, nil when absent, a
// bool, or a count) and how many attempts the call took (more than 1 only on the retry channel).
func (g *GrpcCrud) execute(channel, region, op, keyName string, size int) (result any, attempts int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), callDeadline)
	defer cancel()
	return g.executeCtx(ctx, channel, region, op, keyName, size)
}

// executeCtx is execute with the caller's context (and so its deadline).
func (g *GrpcCrud) executeCtx(ctx context.Context, channel, region, op, keyName string, size int) (any, int, error) {
	var n atomic.Int32
	result, err := g.call(withAttempts(ctx, &n), g.clients[channel], region, op, keyName, size)
	return result, int(n.Load()), err
}

func (g *GrpcCrud) call(ctx context.Context, gemfire pb.GemFireClient, region, op, keyName string, size int) (any, error) {
	k := key(keyName)
	v := g.payload(size)

	switch op {
	case "Put":
		_, err := gemfire.Put(ctx, &pb.PutRequest{RegionName: region, Key: k, Value: v})
		return nil, err
	case "Get":
		r, err := gemfire.Get(ctx, &pb.GetRequest{RegionName: region, Key: k})
		return valueBytes(r.GetValue()), err
	case "GetAndPut":
		r, err := gemfire.GetAndPut(ctx, &pb.GetAndPutRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "Remove":
		r, err := gemfire.Remove(ctx, &pb.RemoveRequest{RegionName: region, Key: k})
		return r.GetRemoved(), err
	case "RemoveIf":
		r, err := gemfire.RemoveIf(ctx, &pb.RemoveIfRequest{RegionName: region, Key: k, ExpectedValue: v})
		return r.GetRemoved(), err
	case "GetAndRemove":
		r, err := gemfire.GetAndRemove(ctx, &pb.GetAndRemoveRequest{RegionName: region, Key: k})
		return valueBytes(r.GetValue()), err
	case "PutIfAbsent":
		r, err := gemfire.PutIfAbsent(ctx, &pb.PutIfAbsentRequest{RegionName: region, Key: k, Value: v})
		return r.GetInserted(), err
	case "PutIfAbsentAndGet":
		r, err := gemfire.PutIfAbsentAndGet(ctx, &pb.PutIfAbsentAndGetRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "Replace":
		r, err := gemfire.Replace(ctx, &pb.ReplaceRequest{RegionName: region, Key: k, Value: v})
		return r.GetReplaced(), err
	case "ReplaceIf":
		r, err := gemfire.ReplaceIf(ctx, &pb.ReplaceIfRequest{RegionName: region, Key: k, Value: v, ExpectedValue: v})
		return r.GetReplaced(), err
	case "GetAndReplace":
		r, err := gemfire.GetAndReplace(ctx, &pb.GetAndReplaceRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "ContainsKey":
		r, err := gemfire.ContainsKey(ctx, &pb.ContainsKeyRequest{RegionName: region, Key: k})
		return r.GetContains(), err
	case "EntryCount":
		r, err := gemfire.EntryCount(ctx, &pb.EntryCountRequest{RegionName: region})
		return r.GetSize(), err
	}
	return nil, fmt.Errorf("unknown op: %s", op)
}

// preload writes keys 0..count-1 with one PutStream (bulk streaming is covered in depth in
// 02-bulk-streaming) and returns the number written.
func (g *GrpcCrud) preload(count, size int) (int, error) {
	gemfire := g.clients["failfast"]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stream, err := gemfire.PutStream(ctx)
	if err != nil {
		return 0, err
	}
	// The first message names the region; later messages carry only entries.
	if err := stream.Send(&pb.PutStreamRequest{Config: &pb.StreamConfig{RegionName: g.cfg.Region}}); err != nil {
		return 0, streamErr(stream.CloseAndRecv())
	}
	v := g.payload(size)
	batch := &pb.PutStreamRequest{}
	for i := 0; i < count; i++ {
		batch.Entries = append(batch.Entries, &pb.Entry{Key: key(fmt.Sprintf("%s%d", g.cfg.KeyPrefix, i)), Value: v})
		// Flush every 500 entries or ~1 MiB, whichever comes first: one stream message must stay
		// under the server's gemfire.grpc.max-message-size (4 MiB by default).
		if len(batch.Entries) == 500 || len(batch.Entries)*size >= 1<<20 {
			if err := stream.Send(batch); err != nil {
				return 0, streamErr(stream.CloseAndRecv())
			}
			batch = &pb.PutStreamRequest{}
		}
	}
	if len(batch.Entries) > 0 {
		if err := stream.Send(batch); err != nil {
			return 0, streamErr(stream.CloseAndRecv())
		}
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		return 0, err
	}
	return count, nil
}

// streamErr returns the real status after a failed Send (Send itself only reports io.EOF).
func streamErr(_ *pb.PutStreamResponse, err error) error {
	if err == nil {
		return errors.New("stream closed by server")
	}
	return err
}

func (g *GrpcCrud) payload(size int) *pb.Value {
	if v, ok := g.payloads.Load(size); ok {
		return v.(*pb.Value)
	}
	b := make([]byte, size)
	r := rand.New(rand.NewPCG(uint64(size), 0))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	v, _ := g.payloads.LoadOrStore(size, &pb.Value{ValueValue: &pb.Value_Bytes{Bytes: b}})
	return v.(*pb.Value)
}

// valueBytes is the size of a returned value in bytes, or nil when the response left `value`
// unset (missing key).
func valueBytes(v *pb.Value) any {
	if v == nil || v.ValueValue == nil {
		return nil
	}
	switch x := v.ValueValue.(type) {
	case *pb.Value_Bytes:
		return len(x.Bytes)
	case *pb.Value_String_:
		return len(x.String_)
	}
	return proto.Size(v)
}

// errorCode maps a failure to a short label: the canonical gRPC status code name (the same
// spelling grpc-java uses, e.g. FAILED_PRECONDITION) or the Go error type.
func errorCode(err error) string {
	if s, ok := status.FromError(err); ok {
		return codeNames[s.Code()]
	}
	return fmt.Sprintf("%T", err)
}

var codeNames = map[codes.Code]string{
	codes.OK: "OK", codes.Canceled: "CANCELLED", codes.Unknown: "UNKNOWN",
	codes.InvalidArgument: "INVALID_ARGUMENT", codes.DeadlineExceeded: "DEADLINE_EXCEEDED",
	codes.NotFound: "NOT_FOUND", codes.AlreadyExists: "ALREADY_EXISTS",
	codes.PermissionDenied: "PERMISSION_DENIED", codes.ResourceExhausted: "RESOURCE_EXHAUSTED",
	codes.FailedPrecondition: "FAILED_PRECONDITION", codes.Aborted: "ABORTED",
	codes.OutOfRange: "OUT_OF_RANGE", codes.Unimplemented: "UNIMPLEMENTED", codes.Internal: "INTERNAL",
	codes.Unavailable: "UNAVAILABLE", codes.DataLoss: "DATA_LOSS", codes.Unauthenticated: "UNAUTHENTICATED",
}

func key(s string) *pb.Key { return &pb.Key{KeyValue: &pb.Key_String_{String_: s}} }
