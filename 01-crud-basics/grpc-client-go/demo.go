package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Demo is a narrated tour of all 13 unary RPCs plus the supported key and value types. Every
// step asserts the documented behavior. It is the same tour as grpc-client-java's Demo.java,
// step for step, so the logs of the Java and Go gRPC clients (and the native client) read side
// by side.
type Demo struct {
	ctx     context.Context // one deadline for the whole tour
	gemfire pb.GemFireClient
	region  string
	c       *Checks
}

// demoAbort carries an unexpected RPC error out of the tour (see must), so each step reads as
// one line instead of an if-err block.
type demoAbort struct{ err error }

func runDemo(gemfire pb.GemFireClient, cfg Config, checks *Checks) {
	defer func() {
		if r := recover(); r != nil {
			if a, ok := r.(demoAbort); ok {
				checks.abort(a.err)
				return
			}
			panic(r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	(&Demo{ctx: ctx, gemfire: gemfire, region: cfg.DemoRegion, c: checks}).run()
}

func (d *Demo) run() {
	c, g, region := d.c, d.gemfire, d.region
	d.cleanup()

	c.section("1. Put + Get")
	d.put(key("user:1"), str("Alice"))
	c.check("Get(user:1) returns the value just put", equal(d.get(key("user:1")), str("Alice")), `"Alice"`)
	miss := must(g.Get(d.ctx, &pb.GetRequest{RegionName: region, Key: key("user:404")}))
	c.check("Get on a missing key → `value` unset (never NOT_FOUND)", miss.Value == nil)

	c.section("2. GetAndPut — put, returning the previous value")
	prev := must(g.GetAndPut(d.ctx, &pb.GetAndPutRequest{RegionName: region, Key: key("user:1"), Value: str("Alicia")})).Value
	c.check(`GetAndPut(user:1, "Alicia") returns the old value`, equal(prev, str("Alice")), `"Alice"`)
	hadPrev := must(g.GetAndPut(d.ctx, &pb.GetAndPutRequest{RegionName: region, Key: key("user:2"), Value: str("Bob")})).Value != nil
	c.check("GetAndPut on a new key → `value` unset", !hadPrev)

	c.section("3. PutIfAbsent")
	inserted := must(g.PutIfAbsent(d.ctx, &pb.PutIfAbsentRequest{RegionName: region, Key: key("user:3"), Value: str("Carol")})).Inserted
	c.check("PutIfAbsent(user:3) on a new key → inserted=true", inserted)
	inserted = must(g.PutIfAbsent(d.ctx, &pb.PutIfAbsentRequest{RegionName: region, Key: key("user:3"), Value: str("Mallory")})).Inserted
	c.check("PutIfAbsent(user:3) again → inserted=false", !inserted)
	c.check("...and the original value is untouched", equal(d.get(key("user:3")), str("Carol")), `"Carol"`)

	c.section("4. PutIfAbsentAndGet — insert-or-read in one round trip")
	v := must(g.PutIfAbsentAndGet(d.ctx, &pb.PutIfAbsentAndGetRequest{RegionName: region, Key: key("user:3"), Value: str("Mallory")})).Value
	c.check("key present → returns the existing value", equal(v, str("Carol")), `"Carol"`)
	v = must(g.PutIfAbsentAndGet(d.ctx, &pb.PutIfAbsentAndGetRequest{RegionName: region, Key: key("user:4"), Value: str("Dave")})).Value
	c.check("key absent → inserts and returns the new value", equal(v, str("Dave")), `"Dave"`)

	c.section("5. Replace — only if the key exists")
	replaced := must(g.Replace(d.ctx, &pb.ReplaceRequest{RegionName: region, Key: key("user:404"), Value: str("Ghost")})).Replaced
	c.check("Replace on a missing key → replaced=false", !replaced)
	c.check("...and no entry was created", !d.containsKey(key("user:404")))
	replaced = must(g.Replace(d.ctx, &pb.ReplaceRequest{RegionName: region, Key: key("user:4"), Value: str("David")})).Replaced
	c.check("Replace on an existing key → replaced=true", replaced)

	c.section("6. ReplaceIf — compare-and-set")
	replaced = must(g.ReplaceIf(d.ctx, &pb.ReplaceIfRequest{RegionName: region, Key: key("user:4"),
		ExpectedValue: str("Dave"), Value: str("Dee")})).Replaced
	c.check("ReplaceIf with a stale expected value → replaced=false", !replaced)
	replaced = must(g.ReplaceIf(d.ctx, &pb.ReplaceIfRequest{RegionName: region, Key: key("user:4"),
		ExpectedValue: str("David"), Value: str("Dee")})).Replaced
	c.check("ReplaceIf with the current value → replaced=true", replaced)

	c.section("7. GetAndReplace")
	old := must(g.GetAndReplace(d.ctx, &pb.GetAndReplaceRequest{RegionName: region, Key: key("user:4"), Value: str("Diana")})).Value
	c.check("GetAndReplace on an existing key returns the old value", equal(old, str("Dee")), `"Dee"`)
	found := must(g.GetAndReplace(d.ctx, &pb.GetAndReplaceRequest{RegionName: region, Key: key("user:405"), Value: str("Ghost")})).Value != nil
	c.check("GetAndReplace on a missing key → `value` unset, nothing created", !found && !d.containsKey(key("user:405")))

	c.section("8. ContainsKey")
	c.check("ContainsKey(user:1) → true", d.containsKey(key("user:1")))
	c.check("ContainsKey(user:404) → false", !d.containsKey(key("user:404")))

	c.section("9. Remove")
	removed := must(g.Remove(d.ctx, &pb.RemoveRequest{RegionName: region, Key: key("user:2")})).Removed
	c.check("Remove(user:2) → removed=true", removed)
	removed = must(g.Remove(d.ctx, &pb.RemoveRequest{RegionName: region, Key: key("user:2")})).Removed
	c.check("Remove(user:2) again → removed=false", !removed)

	c.section("10. RemoveIf — remove only if the value matches")
	removed = must(g.RemoveIf(d.ctx, &pb.RemoveIfRequest{RegionName: region, Key: key("user:3"), ExpectedValue: str("Mallory")})).Removed
	c.check("RemoveIf with the wrong value → removed=false", !removed)
	removed = must(g.RemoveIf(d.ctx, &pb.RemoveIfRequest{RegionName: region, Key: key("user:3"), ExpectedValue: str("Carol")})).Removed
	c.check("RemoveIf with the current value → removed=true", removed)

	c.section("11. GetAndRemove")
	gone := must(g.GetAndRemove(d.ctx, &pb.GetAndRemoveRequest{RegionName: region, Key: key("user:4")})).Value
	c.check("GetAndRemove(user:4) returns the removed value", equal(gone, str("Diana")), `"Diana"`)
	again := must(g.GetAndRemove(d.ctx, &pb.GetAndRemoveRequest{RegionName: region, Key: key("user:4")})).Value != nil
	c.check("GetAndRemove(user:4) again → `value` unset", !again)

	c.section("12. EntryCount")
	before := d.entryCount()
	for i := range 5 {
		d.put(key(fmt.Sprintf("count:%d", i)), &pb.Value{ValueValue: &pb.Value_Int32{Int32: int32(i)}})
	}
	after := d.entryCount()
	c.check("EntryCount grows by exactly 5 after 5 new puts", after-before == 5, fmt.Sprintf("%d → %d", before, after))

	c.section("13. Key types — string, int32 and int64 keys are distinct")
	d.put(key("42"), str("string key"))
	d.put(intKey(42), str("int32 key"))
	d.put(longKey(42), str("int64 key"))
	c.check(`Get("42")  → "string key"`, equal(d.get(key("42")), str("string key")))
	c.check(`Get(42)    → "int32 key"`, equal(d.get(intKey(42)), str("int32 key")))
	c.check(`Get(42L)   → "int64 key"`, equal(d.get(longKey(42)), str("int64 key")))
	big := longKey(9_000_000_000)
	d.put(big, str("beyond int32"))
	c.check("int64 key beyond the int32 range round-trips", equal(d.get(big), str("beyond int32")))

	c.section("14. Value types — every scalar Value case round-trips")
	values := []*pb.Value{
		str("héllo wörld ✓"),
		{ValueValue: &pb.Value_Bytes{Bytes: []byte{0, 1, 2, 0xFF}}},
		{ValueValue: &pb.Value_Int32{Int32: math.MinInt32}},
		{ValueValue: &pb.Value_Int64{Int64: math.MaxInt64}},
		{ValueValue: &pb.Value_Bool{Bool: true}},
		{ValueValue: &pb.Value_Double{Double: math.Pi}},
		{ValueValue: &pb.Value_Float{Float: 2.5}},
	}
	for _, value := range values {
		k := key("type:" + valueCase(value))
		d.put(k, value)
		back := d.get(k)
		c.check(fmt.Sprintf("%-7s round-trips", valueCase(value)), equal(back, value), show(back))
	}
	c.note("google.protobuf.Any (structured values) is covered in 04-structured-values.")

	c.section("15. Errors are gRPC status codes")
	_, err := g.Get(d.ctx, &pb.GetRequest{RegionName: "no-such-region", Key: key("k")})
	c.check("Get on a missing region → FAILED_PRECONDITION", status.Code(err) == codes.FailedPrecondition, statusText(err))
	_, err = g.Get(d.ctx, &pb.GetRequest{RegionName: region, Key: &pb.Key{}})
	c.check("Get with an unset Key oneof → INVALID_ARGUMENT", status.Code(err) == codes.InvalidArgument, statusText(err))
	c.note("Every status code is exercised in 10-error-handling.")
}

// ---------------------------------------------------------------------------------------------

// cleanup makes the demo re-runnable: it removes every key a previous run may have left behind.
func (d *Demo) cleanup() {
	keys := []*pb.Key{}
	for _, s := range []string{"user:1", "user:2", "user:3", "user:4", "user:404", "user:405", "42"} {
		keys = append(keys, key(s))
	}
	for i := range 5 {
		keys = append(keys, key(fmt.Sprintf("count:%d", i)))
	}
	for _, t := range []string{"STRING", "BYTES", "INT32", "INT64", "BOOL", "DOUBLE", "FLOAT", "ANY"} {
		keys = append(keys, key("type:"+t))
	}
	keys = append(keys, intKey(42), longKey(42), longKey(9_000_000_000))
	for _, k := range keys {
		must(d.gemfire.Remove(d.ctx, &pb.RemoveRequest{RegionName: d.region, Key: k}))
	}
}

func (d *Demo) put(k *pb.Key, v *pb.Value) {
	must(d.gemfire.Put(d.ctx, &pb.PutRequest{RegionName: d.region, Key: k, Value: v}))
}

func (d *Demo) get(k *pb.Key) *pb.Value {
	return must(d.gemfire.Get(d.ctx, &pb.GetRequest{RegionName: d.region, Key: k})).Value
}

func (d *Demo) containsKey(k *pb.Key) bool {
	return must(d.gemfire.ContainsKey(d.ctx, &pb.ContainsKeyRequest{RegionName: d.region, Key: k})).Contains
}

func (d *Demo) entryCount() uint64 {
	return must(d.gemfire.EntryCount(d.ctx, &pb.EntryCountRequest{RegionName: d.region})).Size
}

// must returns the response of an RPC the demo expects to succeed, and aborts the tour if it
// failed.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(demoAbort{err})
	}
	return resp
}

func equal(a, b *pb.Value) bool { return a != nil && proto.Equal(a, b) }

// valueCase names a Value's oneof case the way grpc-java does (STRING, BYTES, INT32, ...), so
// both gRPC clients log identical lines.
func valueCase(v *pb.Value) string {
	switch v.GetValueValue().(type) {
	case *pb.Value_String_:
		return "STRING"
	case *pb.Value_Bytes:
		return "BYTES"
	case *pb.Value_Int32:
		return "INT32"
	case *pb.Value_Int64:
		return "INT64"
	case *pb.Value_Bool:
		return "BOOL"
	case *pb.Value_Double:
		return "DOUBLE"
	case *pb.Value_Float:
		return "FLOAT"
	case *pb.Value_Any:
		return "ANY"
	}
	return "VALUEVALUE_NOT_SET"
}

// show renders a Value for the log, in the same format as grpc-java's Demo.
func show(v *pb.Value) string {
	switch x := v.GetValueValue().(type) {
	case *pb.Value_String_:
		return `"` + x.String_ + `"`
	case *pb.Value_Bytes:
		h := hex.EncodeToString(x.Bytes)
		var parts []string
		for i := 0; i < len(h); i += 2 {
			parts = append(parts, h[i:i+2])
		}
		return strings.Join(parts, " ")
	case *pb.Value_Int32:
		return strconv.Itoa(int(x.Int32))
	case *pb.Value_Int64:
		return strconv.FormatInt(x.Int64, 10)
	case *pb.Value_Bool:
		return strconv.FormatBool(x.Bool)
	case *pb.Value_Double:
		return strconv.FormatFloat(x.Double, 'g', -1, 64)
	case *pb.Value_Float:
		return strconv.FormatFloat(float64(x.Float), 'f', -1, 32) + "f"
	}
	return valueCase(v)
}

// statusText renders an RPC error as `CODE: description` (or "OK" when the call succeeded).
func statusText(err error) string {
	s := status.Convert(err)
	if s.Message() == "" {
		return errorCode(err)
	}
	return errorCode(err) + ": " + s.Message()
}

func key(s string) *pb.Key    { return &pb.Key{KeyValue: &pb.Key_String_{String_: s}} }
func intKey(n int32) *pb.Key  { return &pb.Key{KeyValue: &pb.Key_Int{Int: n}} }
func longKey(n int64) *pb.Key { return &pb.Key{KeyValue: &pb.Key_Long{Long: n}} }
func str(s string) *pb.Value  { return &pb.Value{ValueValue: &pb.Value_String_{String_: s}} }
