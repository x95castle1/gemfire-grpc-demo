package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// callTimeout is the deadline on every RPC, as in the Java client.
const callTimeout = 10 * time.Second

// GrpcCrud runs the 13 unary operations for the HTTP API, each as a given user.
type GrpcCrud struct {
	gemfire pb.GemFireClient
	tokens  *Tokens
	cfg     Config
	// One payload per size. Every write of a given size uses the same bytes, so ReplaceIf and
	// RemoveIf succeed whenever the key exists.
	payloads sync.Map // int -> *pb.Value
}

func newGrpcCrud(gemfire pb.GemFireClient, tokens *Tokens, cfg Config) *GrpcCrud {
	return &GrpcCrud{gemfire: gemfire, tokens: tokens, cfg: cfg}
}

// execute runs one operation on key in region with a size-byte value (for writes), as user (its
// token goes on the call), and returns its result: the value's size in bytes (nil when absent),
// a bool, or a count.
func (g *GrpcCrud) execute(user, region, op, keyName string, size int) (any, error) {
	return g.executeWith(user, g.tokens.token(user), region, op, keyName, size)
}

// executeWith is execute with an explicit token (the demo sends expired and tampered ones).
func (g *GrpcCrud) executeWith(user, token, region, op, keyName string, size int) (any, error) {
	ctx, cancel := context.WithTimeout(withToken(context.Background(), user, token), callTimeout)
	defer cancel()
	k := key(keyName)
	v := g.payload(size)

	switch op {
	case "Put":
		_, err := g.gemfire.Put(ctx, &pb.PutRequest{RegionName: region, Key: k, Value: v})
		return nil, err
	case "Get":
		r, err := g.gemfire.Get(ctx, &pb.GetRequest{RegionName: region, Key: k})
		return valueBytes(r.GetValue()), err
	case "GetAndPut":
		r, err := g.gemfire.GetAndPut(ctx, &pb.GetAndPutRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "Remove":
		r, err := g.gemfire.Remove(ctx, &pb.RemoveRequest{RegionName: region, Key: k})
		return r.GetRemoved(), err
	case "RemoveIf":
		r, err := g.gemfire.RemoveIf(ctx, &pb.RemoveIfRequest{RegionName: region, Key: k, ExpectedValue: v})
		return r.GetRemoved(), err
	case "GetAndRemove":
		r, err := g.gemfire.GetAndRemove(ctx, &pb.GetAndRemoveRequest{RegionName: region, Key: k})
		return valueBytes(r.GetValue()), err
	case "PutIfAbsent":
		r, err := g.gemfire.PutIfAbsent(ctx, &pb.PutIfAbsentRequest{RegionName: region, Key: k, Value: v})
		return r.GetInserted(), err
	case "PutIfAbsentAndGet":
		r, err := g.gemfire.PutIfAbsentAndGet(ctx, &pb.PutIfAbsentAndGetRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "Replace":
		r, err := g.gemfire.Replace(ctx, &pb.ReplaceRequest{RegionName: region, Key: k, Value: v})
		return r.GetReplaced(), err
	case "ReplaceIf":
		r, err := g.gemfire.ReplaceIf(ctx, &pb.ReplaceIfRequest{RegionName: region, Key: k, Value: v, ExpectedValue: v})
		return r.GetReplaced(), err
	case "GetAndReplace":
		r, err := g.gemfire.GetAndReplace(ctx, &pb.GetAndReplaceRequest{RegionName: region, Key: k, Value: v})
		return valueBytes(r.GetValue()), err
	case "ContainsKey":
		r, err := g.gemfire.ContainsKey(ctx, &pb.ContainsKeyRequest{RegionName: region, Key: k})
		return r.GetContains(), err
	case "EntryCount":
		r, err := g.gemfire.EntryCount(ctx, &pb.EntryCountRequest{RegionName: region})
		return r.GetSize(), err
	}
	return nil, fmt.Errorf("unknown op: %s", op)
}

// preload writes keys 0..count-1 with one PutStream (bulk streaming is covered in depth in
// 02-bulk-streaming) and returns the number written.
// preload writes keys 0..count-1 into the load region as alice (who may write anywhere).
func (g *GrpcCrud) preload(count, size int) (int, error) {
	ctx, cancel := context.WithTimeout(withToken(context.Background(), "alice", g.tokens.token("alice")), 5*time.Minute)
	defer cancel()
	stream, err := g.gemfire.PutStream(ctx)
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
