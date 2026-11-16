package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queryTimeout is the deadline on every RPC.
const queryTimeout = 30 * time.Second

// The data set: keys item-00000.. with int values spread evenly over 0..9,999, so a query for
// `v < N` matches about N rows per 10,000 entries.
func itemKey(i int) string { return fmt.Sprintf("item-%05d", i) }
func score(i int) int32    { return int32((i * 7919) % 10000) }

// QueryResult is one Query call: the column names, the rows (or the first few), and how the
// server streamed them.
type QueryResult struct {
	FieldNames []string           // from the first QueryResponse; empty for SELECT * over scalars
	Rows       [][]*pb.QueryValue // the rows kept (see run's keep)
	RowCount   int                // every row received
	Messages   int                // QueryResponse messages: about RowCount / query-batch-size
	FirstBatch time.Duration      // when the first message arrived
	Elapsed    time.Duration      // the whole call (set by the demo)
	Batches    []int              // rows per message
	// LaterNames counts messages after the first that repeated field_names (the contract: none).
	LaterNames int
}

// GrpcQuery runs OQL over the Query RPC and loads the data set over PutStream.
type GrpcQuery struct {
	gemfire pb.GemFireClient
}

func newGrpcQuery(gemfire pb.GemFireClient) *GrpcQuery {
	return &GrpcQuery{gemfire: gemfire}
}

// run executes one query and reads the whole stream. keep is how many rows to hold on to
// (-1 = all); the rest are only counted.
func (q *GrpcQuery) run(ctx context.Context, oql string, params []*pb.Value, keep int) (*QueryResult, error) {
	start := time.Now()
	stream, err := q.gemfire.Query(ctx, &pb.QueryRequest{Query: oql, Parameters: params})
	if err != nil {
		return nil, err
	}
	res := &QueryResult{}
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return res, nil
		}
		if err != nil {
			return nil, err
		}
		if res.Messages == 0 {
			res.FirstBatch = time.Since(start)
			res.FieldNames = msg.FieldNames
		} else if len(msg.FieldNames) > 0 {
			res.LaterNames++
		}
		res.Messages++
		res.Batches = append(res.Batches, len(msg.Rows))
		res.RowCount += len(msg.Rows)
		for _, row := range msg.Rows {
			if keep < 0 || len(res.Rows) < keep {
				res.Rows = append(res.Rows, row.Fields)
			}
		}
	}
}

// load writes count entries (item-00000 → score) into region with PutStream, 1000 per message.
func (q *GrpcQuery) load(ctx context.Context, region string, count int) error {
	stream, err := q.gemfire.PutStream(ctx)
	if err != nil {
		return err
	}
	msg := &pb.PutStreamRequest{Config: &pb.StreamConfig{RegionName: region}}
	for i := 0; i < count; i++ {
		msg.Entries = append(msg.Entries, &pb.Entry{Key: key(itemKey(i)), Value: int32Value(score(i))})
		if len(msg.Entries) == 1000 {
			if err := stream.Send(msg); err != nil {
				_, err = stream.CloseAndRecv()
				return cmpErr(err)
			}
			msg = &pb.PutStreamRequest{}
		}
	}
	if len(msg.Entries) > 0 || msg.Config != nil {
		if err := stream.Send(msg); err != nil {
			_, err = stream.CloseAndRecv()
			return cmpErr(err)
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}

// cmpErr returns the real status after a failed Send (Send itself only reports io.EOF).
func cmpErr(err error) error {
	if err == nil {
		return errors.New("stream closed by server")
	}
	return err
}

// param turns an HTTP parameter into a query parameter: an int32 if it parses as one, otherwise a
// string. "int64:N", "double:N" and "string:S" force a type.
func param(s string) *pb.Value {
	switch {
	case len(s) > 6 && s[:6] == "int64:":
		if n, err := strconv.ParseInt(s[6:], 10, 64); err == nil {
			return &pb.Value{ValueValue: &pb.Value_Int64{Int64: n}}
		}
	case len(s) > 7 && s[:7] == "double:":
		if f, err := strconv.ParseFloat(s[7:], 64); err == nil {
			return &pb.Value{ValueValue: &pb.Value_Double{Double: f}}
		}
	case len(s) >= 7 && s[:7] == "string:":
		return str(s[7:])
	}
	if n, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32Value(int32(n))
	}
	return str(s)
}

// queryValue renders a result value as a Go value for JSON: nil for NULL (an unset QueryValue).
func queryValue(v *pb.QueryValue) any {
	switch x := v.GetValueValue().(type) {
	case *pb.QueryValue_String_:
		return x.String_
	case *pb.QueryValue_Bytes:
		return x.Bytes
	case *pb.QueryValue_Int32:
		return x.Int32
	case *pb.QueryValue_Int64:
		return x.Int64
	case *pb.QueryValue_Bool:
		return x.Bool
	case *pb.QueryValue_Double:
		return x.Double
	case *pb.QueryValue_Float:
		return x.Float
	case *pb.QueryValue_Any:
		return "Any(" + x.Any.GetTypeUrl() + ")"
	}
	return nil
}

// queryCase names a QueryValue's oneof case (INT32, STRING, ...), or NULL when unset.
func queryCase(v *pb.QueryValue) string {
	switch v.GetValueValue().(type) {
	case *pb.QueryValue_String_:
		return "STRING"
	case *pb.QueryValue_Bytes:
		return "BYTES"
	case *pb.QueryValue_Int32:
		return "INT32"
	case *pb.QueryValue_Int64:
		return "INT64"
	case *pb.QueryValue_Bool:
		return "BOOL"
	case *pb.QueryValue_Double:
		return "DOUBLE"
	case *pb.QueryValue_Float:
		return "FLOAT"
	case *pb.QueryValue_Any:
		return "ANY"
	}
	return "NULL"
}

// statusMessage is an RPC error's description without the code.
func statusMessage(err error) string {
	return status.Convert(err).Message()
}

// errorCode maps a failure to a short label: the canonical gRPC status code name (e.g.
// FAILED_PRECONDITION) or the Go error type.
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

func key(s string) *pb.Key            { return &pb.Key{KeyValue: &pb.Key_String_{String_: s}} }
func str(s string) *pb.Value          { return &pb.Value{ValueValue: &pb.Value_String_{String_: s}} }
func int32Value(n int32) *pb.Value    { return &pb.Value{ValueValue: &pb.Value_Int32{Int32: n}} }
