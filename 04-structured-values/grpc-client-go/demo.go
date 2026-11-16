package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	pb "grpc-client-go/gen/gemfirev1"
	notev1 "grpc-client-go/gen/notev1"
	personv1 "grpc-client-go/gen/personv1"

	"google.golang.org/protobuf/proto"
)

// demoPeople is how many people (person(0..199)) the demo writes into its own region.
const demoPeople = 200

// Demo is a narrated tour of structured values: protobuf messages stored as google.protobuf.Any,
// then queried by field once their descriptor is deployed. Every expected count is computed from
// person(i), the same function that generated the data.
type Demo struct {
	ctx     context.Context // one deadline for the whole tour
	gemfire pb.GemFireClient
	people  *People
	q       *GrpcQuery
	region  string // demo-grpc-go
	notes   string // demo-notes
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
	(&Demo{ctx: ctx, gemfire: gemfire, people: newPeople(gemfire), q: newGrpcQuery(gemfire),
		region: cfg.DemoRegion, notes: cfg.NotesRegion, c: checks}).run()
}

func (d *Demo) run() {
	c, r := d.c, "/"+d.region
	must(d.gemfire.Remove(d.ctx, &pb.RemoveRequest{RegionName: d.region, Key: key("note-1")})) // left by an aborted run
	must0(d.people.load(d.ctx, d.region, demoPeople))
	c.note(fmt.Sprintf("Loaded person(0..%d) into %s, each packed in a google.protobuf.Any.", demoPeople-1, r))

	c.section("1. Put and Get a message as google.protobuf.Any")
	got, size := must2(d.people.get(d.ctx, d.region, 42))
	c.check("Get(p-00042) unpacks to the same Person that was put", proto.Equal(got, person(42)),
		fmt.Sprintf("%s %s, %d, %s", got.GetFirstName(), got.GetLastName(), got.GetAge(), got.GetAddress().GetCity()))
	raw := must(d.gemfire.Get(d.ctx, &pb.GetRequest{RegionName: d.region, Key: key(personID(42))})).GetValue().GetAny()
	c.check("the Value is an Any with type_url type.googleapis.com/person.v1.Person",
		raw.GetTypeUrl() == "type.googleapis.com/person.v1.Person", raw.GetTypeUrl())
	c.note(fmt.Sprintf("The server stores the Any's %d bytes as they are: Put and Get need no setup at all.", size))
	c.note("Querying into the fields is what needs the descriptor JAR that gfsh-init deployed")
	c.note("(gemfire/Dockerfile builds it from proto/person/v1/person.proto; see `list protobuf-types`).")

	c.section("2. Query into the fields")
	res := d.query("SELECT p.first_name, p.age, p.active FROM "+r+" p WHERE p.address.city = $1", str("London"))
	c.check("WHERE p.address.city = $1 ('London') → every Londoner", res.RowCount == count(func(p *personv1.Person) bool {
		return p.GetAddress().GetCity() == "London"
	}), res.RowCount)
	c.check("field_names are the proto field names: [first_name age active]",
		slices.Equal(res.FieldNames, []string{"first_name", "age", "active"}), res.FieldNames)
	c.check("proto string, int32 and bool fields come back as STRING, INT32, BOOL",
		slices.Equal(columnTypes(res.Rows), []string{"STRING", "INT32", "BOOL"}), firstRow(res))
	res = d.query("SELECT MIN(p.age) AS youngest, MAX(p.age) AS oldest FROM "+r+" p WHERE p.address.country = $1", str("USA"))
	lo, hi := ageRange(func(p *personv1.Person) bool { return p.GetAddress().GetCountry() == "USA" })
	c.check("aggregates over a nested field: MIN/MAX(p.age) of people in the USA",
		res.Rows[0][0].GetInt32() == lo && res.Rows[0][1].GetInt32() == hi, show(res))

	c.section("3. Enums, repeated fields, GROUP BY")
	res = d.query("SELECT COUNT(*) FROM "+r+" p WHERE p.role = $1", str("ENGINEER"))
	c.check("an enum compares by name: p.role = 'ENGINEER'", res.Rows[0][0].GetInt32() == int32(count(func(p *personv1.Person) bool {
		return p.GetRole() == personv1.Role_ENGINEER
	})), show(res))
	res = d.query("SELECT COUNT(*) FROM "+r+" p WHERE p.role = $1", int32Value(1))
	c.check("…not by number: p.role = 1 matches nothing", res.Rows[0][0].GetInt32() == 0, show(res))
	res = d.query("SELECT p.role FROM "+r+" p WHERE p.id = $1", str(personID(42)))
	c.check("and a projected enum comes back as its name (STRING)", queryValue(res.Rows[0][0]) == person(42).GetRole().String(), show(res))
	res = d.query("SELECT COUNT(*) FROM "+r+" p WHERE $1 IN p.nicknames", str("Ace"))
	c.check("$1 IN p.nicknames: anyone with the nickname 'Ace'", res.Rows[0][0].GetInt32() == int32(count(func(p *personv1.Person) bool {
		return slices.Contains(p.GetNicknames(), "Ace")
	})), show(res))
	res = d.query("SELECT p.nicknames.size AS n FROM "+r+" p WHERE p.id = $1", str(personID(2)))
	c.check("p.nicknames.size is the list's length", res.Rows[0][0].GetInt32() == int32(len(person(2).GetNicknames())), show(res))
	err := d.fails("SELECT COUNT(*) FROM "+r+" p WHERE p.nicknames[0] = $1", str("Ace"))
	c.check("p.nicknames[0] on an EMPTY list fails the whole query → INTERNAL (even in WHERE)", errorCode(err) == "INTERNAL", statusText(err))
	res = d.query("SELECT COUNT(*) FROM "+r+" p WHERE p.nicknames.size > 0 AND p.nicknames[0] = $1", str("Ace"))
	c.check("…so guard it: p.nicknames.size > 0 AND p.nicknames[0] = $1", res.Rows[0][0].GetInt32() == int32(count(func(p *personv1.Person) bool {
		return len(p.GetNicknames()) > 0 && p.GetNicknames()[0] == "Ace"
	})), show(res))
	err = d.fails("SELECT p.nicknames FROM "+r+" p WHERE p.id = $1", str(personID(2)))
	c.check("a whole repeated field can't be a column → UNIMPLEMENTED (java.util.ArrayList)", errorCode(err) == "UNIMPLEMENTED", statusText(err))
	res = d.query("SELECT p.address.city AS city, COUNT(*) AS n FROM " + r + " p WHERE IS_DEFINED(p.address.city) GROUP BY p.address.city")
	c.check("GROUP BY p.address.city → one row per city", res.RowCount == distinctCities(), show(res))

	c.section("4. Missing values: NULL, UNDEFINED and proto3 defaults")
	c.note("Every 10th person has no address: the nested message is simply not set.")
	res = d.query("SELECT p.id, p.address FROM "+r+" p WHERE p.id = $1", str(personID(9)))
	c.check("p.address on p-00009 (no address) → an unset QueryValue: NULL", queryCase(res.Rows[0][1]) == "NULL", show(res))
	res = d.query("SELECT p.id, p.address FROM "+r+" p WHERE p.id = $1", str(personID(42)))
	c.check("p.address on p-00042 → the Address message itself, as an Any", queryCase(res.Rows[0][1]) == "ANY" &&
		res.Rows[0][1].GetAny().GetTypeUrl() == "type.googleapis.com/person.v1.Address", show(res))
	noAddress := count(func(p *personv1.Person) bool { return p.GetAddress() == nil })
	res = d.query("SELECT COUNT(*) FROM " + r + " p WHERE p.address = NULL")
	c.check("WHERE p.address = NULL finds the people without one", res.Rows[0][0].GetInt32() == int32(noAddress), show(res))
	err = d.fails("SELECT p.id, p.address.city FROM "+r+" p WHERE p.id = $1", str(personID(9)))
	c.check("…but a field INSIDE the missing message is UNDEFINED → UNIMPLEMENTED", errorCode(err) == "UNIMPLEMENTED", statusText(err))
	res = d.query("SELECT COUNT(*) FROM " + r + " p WHERE IS_DEFINED(p.address.city)")
	c.check("WHERE IS_DEFINED(p.address.city) keeps everyone else", res.Rows[0][0].GetInt32() == int32(demoPeople-noAddress), show(res))
	res = d.query("SELECT p.active FROM "+r+" p WHERE p.id = $1", str(personID(0)))
	c.check("a proto3 scalar left at its default is the default (false), not NULL", queryCase(res.Rows[0][0]) == "BOOL" &&
		!res.Rows[0][0].GetBool(), show(res))
	res = d.query("SELECT COUNT(*) FROM " + r + " p WHERE p.no_such_field = 1")
	c.check("a field the message doesn't have: no match in WHERE", res.Rows[0][0].GetInt32() == 0, show(res))
	err = d.fails("SELECT p.no_such_field FROM " + r + " p")
	c.check("…and UNDEFINED → UNIMPLEMENTED as a column (not a parse error)", errorCode(err) == "UNIMPLEMENTED", statusText(err))

	c.section("5. SELECT * returns whole messages")
	res = d.query("SELECT * FROM "+r+" p WHERE p.id = $1", str(personID(7)))
	whole := &personv1.Person{}
	ok := queryCase(res.Rows[0][0]) == "ANY" && res.Rows[0][0].GetAny().UnmarshalTo(whole) == nil
	c.check("each row is one ANY: the stored Any, which the client unpacks", ok && proto.Equal(whole, person(7)),
		fmt.Sprintf("%s %s", whole.GetFirstName(), whole.GetLastName()))
	c.check("field_names is empty, as for any plain SELECT *", len(res.FieldNames) == 0)

	c.section("6. A type the server has no descriptor for")
	c.note("note.v1.Note is compiled into this client but NOT into the descriptor JAR.")
	must(d.gemfire.Put(d.ctx, &pb.PutRequest{RegionName: d.notes, Key: key("note-1"), Value: packed(&notev1.Note{Text: "hello"})}))
	res = d.query("SELECT * FROM /" + d.notes + " n")
	note := &notev1.Note{}
	c.check("Put, Get and SELECT * still work: the bytes come back as they went in",
		res.RowCount == 1 && res.Rows[0][0].GetAny().UnmarshalTo(note) == nil && note.GetText() == "hello", res.Rows[0][0].GetAny().GetTypeUrl())
	err = d.fails("SELECT n.text FROM /" + d.notes + " n")
	c.check("querying a field → FAILED_PRECONDITION: Protobuf type URL not registered", errorCode(err) == "FAILED_PRECONDITION" &&
		strings.Contains(statusMessage(err), "not registered"), statusText(err))
	c.note("(The 1.0 docs say INTERNAL; the server returns FAILED_PRECONDITION.)")
	must(d.gemfire.Put(d.ctx, &pb.PutRequest{RegionName: d.region, Key: key("note-1"), Value: packed(&notev1.Note{Text: "stray"})}))
	err = d.fails("SELECT COUNT(*) FROM " + r + " p WHERE p.age > 50")
	c.check("one Note among the 200 people breaks EVERY field query on that region", errorCode(err) == "FAILED_PRECONDITION", statusText(err))
	must(d.gemfire.Remove(d.ctx, &pb.RemoveRequest{RegionName: d.region, Key: key("note-1")}))
	res = d.query("SELECT COUNT(*) FROM " + r + " p WHERE p.age > 50")
	c.check("remove it and the same query works again", res.RowCount == 1, show(res))
	c.note("To query Notes, add note/v1/note.proto to the JAR and deploy it again (see README).")
}

// query runs OQL that must succeed and keeps every row.
func (d *Demo) query(oql string, params ...*pb.Value) *QueryResult {
	return must(d.q.run(d.ctx, oql, params, -1))
}

// fails runs OQL that is expected to fail and returns the error (nil if it unexpectedly worked).
func (d *Demo) fails(oql string, params ...*pb.Value) error {
	_, err := d.q.run(d.ctx, oql, params, 0)
	return err
}

// count is how many demo people match.
func count(match func(*personv1.Person) bool) int {
	n := 0
	for i := range demoPeople {
		if match(person(i)) {
			n++
		}
	}
	return n
}

func ageRange(match func(*personv1.Person) bool) (lo, hi int32) {
	lo, hi = 1<<30, -1
	for i := range demoPeople {
		if p := person(i); match(p) {
			lo, hi = min(lo, p.GetAge()), max(hi, p.GetAge())
		}
	}
	return lo, hi
}

func distinctCities() int {
	seen := map[string]bool{}
	for i := range demoPeople {
		if a := person(i).GetAddress(); a != nil {
			seen[a.GetCity()] = true
		}
	}
	return len(seen)
}

// show renders up to 3 rows for the log: [London 1000] [..]
func show(res *QueryResult) string {
	var parts []string
	for i, row := range res.Rows {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("… (%d rows)", res.RowCount))
			break
		}
		var vals []string
		for _, v := range row {
			if a := v.GetAny(); a != nil {
				vals = append(vals, strings.TrimPrefix(a.GetTypeUrl(), "type.googleapis.com/")+"{…}")
				continue
			}
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

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(demoAbort{err})
	}
	return a, b
}

func must0(err error) {
	if err != nil {
		panic(demoAbort{err})
	}
}
