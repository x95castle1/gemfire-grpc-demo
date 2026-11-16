package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"
)

// demoEntries is how many item-NNNNN → score entries the demo writes into its own region.
const demoEntries = 5000

// Demo is a narrated tour of the Query RPC. Every step asserts what the server actually does,
// computing the expected rows from the same data the demo wrote.
type Demo struct {
	ctx       context.Context // one deadline for the whole tour
	gemfire   pb.GemFireClient
	q         *GrpcQuery
	region    string
	batchSize int
	c         *Checks
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
	(&Demo{ctx: ctx, gemfire: gemfire, q: newGrpcQuery(gemfire), region: "/" + cfg.DemoRegion,
		batchSize: cfg.QueryBatchSize, c: checks}).run(cfg.DemoRegion)
}

func (d *Demo) run(regionName string) {
	c, r := d.c, d.region

	// The demo's data: item-00000..item-04999 with int values. Any strings left by an earlier
	// run's section 6 are removed first, so every count below is exact.
	for _, k := range []string{"name-1", "name-2", "name-3"} {
		must(d.gemfire.Remove(d.ctx, &pb.RemoveRequest{RegionName: regionName, Key: key(k)}))
	}
	must0(d.q.load(d.ctx, regionName, demoEntries))
	c.note(fmt.Sprintf("Loaded %d entries into %s: item-00000.. → ints spread over 0..9,999", demoEntries, r))

	c.section("1. SELECT * over plain values")
	res := d.query("SELECT * FROM "+r+" v WHERE v < $1", int32Value(100))
	c.check("rows = entries whose value is under $1 = 100", res.RowCount == countBelow(100), res.RowCount)
	c.check("field_names is empty: a plain value has no column name", len(res.FieldNames) == 0)
	c.check("each row is one INT32 field", len(res.Rows) > 0 && len(res.Rows[0]) == 1 && queryCase(res.Rows[0][0]) == "INT32",
		columnTypes(res.Rows))
	c.note("Values are the Java objects GemFire stores: a gRPC int32 Value is an Integer on the server,")
	c.note("so OQL compares it like any number. Keys aren't in a plain SELECT *; use entrySet for those.")

	c.section("2. Projections name their columns")
	res = d.query("SELECT e.key, e.value FROM "+r+".entrySet e WHERE e.value < $1", int32Value(100))
	c.check("SELECT e.key, e.value → field_names [key value]", slices.Equal(res.FieldNames, []string{"key", "value"}), res.FieldNames)
	c.check("…and each row is STRING, INT32 in that order", slices.Equal(columnTypes(res.Rows), []string{"STRING", "INT32"}), firstRow(res))
	res = d.query("SELECT e.key AS id, e.value * 2 AS doubled FROM " + r + ".entrySet e WHERE e.key = 'item-00042'")
	c.check("AS renames a column, and expressions work: [id doubled]", slices.Equal(res.FieldNames, []string{"id", "doubled"}) &&
		res.RowCount == 1 && res.Rows[0][1].GetInt32() == 2*score(42), firstRow(res))
	res = d.query("SELECT MIN(v), MAX(v) FROM " + r + " v")
	c.check("without AS, an aggregate column is named after its argument: MIN(v), MAX(v) → [v v]",
		slices.Equal(res.FieldNames, []string{"v", "v"}), res.FieldNames)

	c.section("3. Parameters: $1, $2, ... bind in order")
	res = d.query("SELECT COUNT(*) FROM "+r+" v WHERE v >= $1 AND v < $2", int32Value(1000), int32Value(1100))
	c.check("COUNT(*) WHERE v >= $1 AND v < $2 with 1000, 1100", res.Rows[0][0].GetInt32() == int32(countBelow(1100)-countBelow(1000)), show(res))
	res = d.query("SELECT e.value FROM "+r+".entrySet e WHERE e.key = $1", str("item-00042"))
	c.check("a string parameter: e.key = $1 finds item-00042", res.RowCount == 1 && res.Rows[0][0].GetInt32() == score(42), show(res))
	res = d.query("SELECT e.key FROM "+r+".entrySet e WHERE e.key IN SET($1, $2)", str("item-00001"), str("item-00002"))
	c.check("IN SET($1, $2) → 2 rows", res.RowCount == 2, res.RowCount)
	res = d.query("SELECT e.key FROM "+r+".entrySet e WHERE e.key LIKE $1", str("item-0000%"))
	c.check("LIKE $1 with 'item-0000%' → item-00000..item-00009", res.RowCount == 10, res.RowCount)
	err := d.fails("SELECT * FROM "+r+" v WHERE v >= $1 AND v < $2", int32Value(1))
	c.check("too few parameters → INVALID_ARGUMENT", errorCode(err) == "INVALID_ARGUMENT", statusText(err))
	res = d.query("SELECT * FROM "+r+" v WHERE v < $1", int32Value(100), str("unused"))
	c.check("extra parameters are ignored", res.RowCount == countBelow(100), res.RowCount)

	c.section("4. Aggregates, DISTINCT, ORDER BY, LIMIT")
	res = d.query("SELECT MIN(v) AS lo, MAX(v) AS hi, SUM(v) AS total, COUNT(*) AS n, AVG(v) AS mean FROM " + r + " v")
	lo, hi, total := minMaxSum()
	row := res.Rows[0]
	c.check("MIN, MAX, SUM, COUNT match the data", row[0].GetInt32() == lo && row[1].GetInt32() == hi &&
		int64(row[2].GetInt32()) == total && row[3].GetInt32() == demoEntries, show(res))
	c.check("AVG of ints comes back as FLOAT (a Java Float), not DOUBLE", queryCase(row[4]) == "FLOAT", columnTypes(res.Rows))
	res = d.query("SELECT e.key, e.value FROM "+r+".entrySet e ORDER BY e.value DESC LIMIT $1", int32Value(3))
	top := topScores(3)
	c.check("ORDER BY e.value DESC LIMIT $1 → the 3 highest values, in order", res.RowCount == 3 &&
		res.Rows[0][1].GetInt32() == top[0] && res.Rows[1][1].GetInt32() == top[1] && res.Rows[2][1].GetInt32() == top[2], show(res))
	res = d.query("SELECT DISTINCT v / 1000 FROM " + r + " v")
	c.check("DISTINCT v / 1000 → 10 buckets (0..9)", res.RowCount == 10, res.RowCount)

	c.section("5. Results stream back in batches")
	res = d.query("SELECT k FROM " + r + ".keySet k")
	want := (demoEntries + d.batchSize - 1) / d.batchSize
	c.check(fmt.Sprintf("%d keys arrive in %d messages of at most query-batch-size (%d) rows", demoEntries, want, d.batchSize),
		res.RowCount == demoEntries && res.Messages == want && slices.Max(res.Batches) <= d.batchSize, res.Batches)
	c.check("field_names is only on the first message", res.LaterNames == 0)
	c.note(fmt.Sprintf("First batch after %s, all %d rows after %s: the client can work on rows while the rest stream in.",
		res.FirstBatch.Round(100*time.Microsecond), res.RowCount, res.Elapsed.Round(100*time.Microsecond)))
	res = d.query("SELECT * FROM " + r + " v WHERE v < 0")
	c.check("an empty result is still one message, with no rows", res.Messages == 1 && res.RowCount == 0, res.Batches)

	c.section("6. UNDEFINED, and NULL")
	for i, s := range []string{"Ada", "Grace", "Barbara"} {
		must(d.gemfire.Put(d.ctx, &pb.PutRequest{RegionName: regionName, Key: key(fmt.Sprintf("name-%d", i+1)), Value: str(s)}))
	}
	c.note("Added 3 string values next to the ints. v.length exists on a String but not on an Integer:")
	c.note("for the ints OQL evaluates it to UNDEFINED.")
	err = d.fails("SELECT v.length FROM " + r + " v")
	c.check("UNDEFINED in a projection → UNIMPLEMENTED (QueryValue has no UNDEFINED)", errorCode(err) == "UNIMPLEMENTED", statusText(err))
	res = d.query("SELECT v, v.length FROM " + r + " v WHERE IS_DEFINED(v.length)")
	c.check("WHERE IS_DEFINED(v.length) keeps only the strings", res.RowCount == 3, show(res))
	res = d.query("SELECT * FROM " + r + " v WHERE v.length > 3")
	c.check("UNDEFINED in WHERE just doesn't match: v.length > 3 → Grace, Barbara", res.RowCount == 2, show(res))
	err = d.fails("SELECT e.key, NULL FROM " + r + ".entrySet e LIMIT 1")
	c.check("a NULL literal column fails on the server (INTERNAL) instead of coming back as an unset QueryValue",
		err != nil, statusText(err))
	c.note("Per the contract, a column whose value is null arrives as an unset QueryValue. With scalar values")
	c.note("(and no structured values with null fields, see 04) there's no simple query that yields one.")

	c.section("7. Errors")
	err = d.fails("SELEKT * FROM " + r)
	c.check("a syntax error → INVALID_ARGUMENT", errorCode(err) == "INVALID_ARGUMENT", statusText(err))
	err = d.fails("SELECT * FROM /no-such-region")
	c.check("an unknown region → FAILED_PRECONDITION", errorCode(err) == "FAILED_PRECONDITION", statusText(err))
	err = d.fails("SELECT v.getClass() FROM " + r + " v")
	c.check("a method the server's method authorizer forbids → PERMISSION_DENIED", errorCode(err) == "PERMISSION_DENIED", statusText(err))
	err = d.fails("SELECT e.key.describeConstable() FROM " + r + ".entrySet e LIMIT 1")
	c.check("a result type with no QueryValue form (java.util.Optional) → UNIMPLEMENTED", errorCode(err) == "UNIMPLEMENTED", statusText(err))
}

// query runs OQL that must succeed and keeps every row.
func (d *Demo) query(oql string, params ...*pb.Value) *QueryResult {
	start := time.Now()
	res := must(d.q.run(d.ctx, oql, params, -1))
	res.Elapsed = time.Since(start)
	return res
}

// fails runs OQL that is expected to fail and returns the error (nil if it unexpectedly worked).
func (d *Demo) fails(oql string, params ...*pb.Value) error {
	_, err := d.q.run(d.ctx, oql, params, 0)
	return err
}

// countBelow is how many demo entries have a value under n.
func countBelow(n int32) int {
	count := 0
	for i := range demoEntries {
		if score(i) < n {
			count++
		}
	}
	return count
}

func minMaxSum() (lo, hi int32, total int64) {
	lo, hi = score(0), score(0)
	for i := range demoEntries {
		s := score(i)
		lo, hi, total = min(lo, s), max(hi, s), total+int64(s)
	}
	return lo, hi, total
}

func topScores(n int) []int32 {
	all := make([]int32, demoEntries)
	for i := range all {
		all[i] = score(i)
	}
	sort.Slice(all, func(a, b int) bool { return all[a] > all[b] })
	return all[:n]
}

// show renders up to 3 rows for the log: [item-00042 2598] [..]
func show(res *QueryResult) string {
	var parts []string
	for i, row := range res.Rows {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("… (%d rows)", res.RowCount))
			break
		}
		var vals []string
		for _, v := range row {
			vals = append(vals, fmt.Sprint(queryValue(v)))
		}
		parts = append(parts, "["+strings.Join(vals, " ")+"]")
	}
	return strings.Join(parts, " ")
}

func firstRow(res *QueryResult) string {
	if len(res.Rows) == 0 {
		return "no rows"
	}
	one := *res
	one.Rows = res.Rows[:1]
	one.RowCount = 1
	return show(&one)
}

// statusText renders an RPC error as `CODE: description` ("no error" when the call succeeded).
func statusText(err error) string {
	if err == nil {
		return "no error"
	}
	if msg := statusMessage(err); msg != "" {
		return errorCode(err) + ": " + msg
	}
	return errorCode(err)
}

func must[T any](resp T, err error) T {
	if err != nil {
		panic(demoAbort{err})
	}
	return resp
}

func must0(err error) {
	if err != nil {
		panic(demoAbort{err})
	}
}
