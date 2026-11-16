# 04 · Structured values: protobuf messages as `google.protobuf.Any`, queried by field (Go)

GemFire for gRPC stores a whole protobuf message as one value: pack it into
`google.protobuf.Any`, put it in a `Value`, and Put it. The server keeps the bytes as they are,
so storing and fetching messages needs no setup. **Querying into their fields**
(`WHERE p.address.city = 'London'`) needs one: a **descriptor JAR**, deployed with gfsh, that
tells the servers what the message types look like.

This example does both from **one Go client**. Like 03, it has no Java or native client to
compare with: it's about what the feature does, where it fails, and what it costs.

```
 k6 (make select-load-test) ──HTTP──▶ grpc-client-go :8080 ──Get/Put/Query :50051──▶ server1 ──▶ server2
 gfsh-init: create regions · deploy person-types.jar (person.desc) · list protobuf-types
 prometheus ◀── client /metrics · members :7070/metrics/ · cadvisor · k6 (remote write)        grafana :3004
```

## Run it

Prerequisites: Docker, plus the licensed files in [`../gemfire-extensions`](../gemfire-extensions/README.md).

```bash
make start EX=04             # cluster, descriptor JAR deployed, the Go client (demo at startup), monitoring
make logs EX=04              # read the demo (grpc-client-go), or gfsh-init for the deploy output
make select-load-test EX=04  # people/steady: Gets, Puts and field queries at 100 req/s
open http://localhost:3004
make stop EX=04
```

Without `make`, run `docker compose up -d --build` in this folder. Smoke test: `make verify EX=04`.

## The schema and the descriptor JAR

[`proto/person/v1/person.proto`](proto/person/v1/person.proto) defines the value:

```protobuf
message Person  { string id; string first_name; string last_name; int32 age;
                  repeated string nicknames; Address address; Role role; bool active; }
message Address { string city; string country; repeated string lines; }
enum Role       { ROLE_UNSPECIFIED; ENGINEER; MANAGER; DESIGNER; }
```

The Go client generates its types from it. [`gemfire/Dockerfile`](gemfire/Dockerfile) compiles the same file into the
descriptor JAR that the gfsh-init service deploys:

```bash
protoc -I proto --include_imports --descriptor_set_out=person.desc person/v1/person.proto
# a JAR is a zip; the .desc must sit at META-INF/gemfire/protobuf/<name>.desc, nothing else is needed
mkdir -p stage/META-INF/gemfire/protobuf && cp person.desc stage/META-INF/gemfire/protobuf/
(cd stage && zip -r ../person-types.jar META-INF)
```

Then, in [`gemfire/init.gfsh`](gemfire/init.gfsh):

```
deploy --jar=/descriptors/person-types.jar
list protobuf-types
```

```
                Type                  | JAR
------------------------------------- | ------------
type.googleapis.com/person.v1.Address | person-types
type.googleapis.com/person.v1.Person  | person-types
```

Messages are registered, including nested ones; the enum isn't listed separately. `list
protobuf-types` needs the gRPC extension in gfsh too, so gfsh-init mounts the `.gfm`.

[`proto/note/v1/note.proto`](proto/note/v1/note.proto) defines a second type, `Note`. The client knows it, but it's
**left out of the JAR on purpose**, so the demo can show what the server does with a type it can't read.

### Undeploy, redeploy, add a type

The gfsh-init image has gfsh and the JAR, so it's the easiest place to run gfsh from:

```bash
gfsh() { docker compose run --rm --no-deps gfsh-init gfsh -e "connect --locator=locator[10334]" "$@"; }

gfsh -e "list protobuf-types"
gfsh -e "undeploy --jar=person-types.jar"            # field queries now fail with FAILED_PRECONDITION
gfsh -e "deploy --jar=/descriptors/person-types.jar" # and work again
```

To make `Note` queryable, add `note/v1/note.proto` to the `protoc` line in `gemfire/Dockerfile`, then
`docker compose build gfsh-init` and deploy again. Deploying the same JAR name replaces it: new
types are registered and removed ones unregistered.

## HTTP API

| Endpoint | What it does |
|---|---|
| `GET /` | endpoint list |
| `GET /health` | 200 once the HTTP API is up (after the startup demo) |
| `POST /demo` | runs the narrated demo; 200 means every assertion passed (also runs at startup) |
| `GET /demo` | result of the last demo run, e.g. `{"ran":true,"passed":29,"failed":0}` |
| `POST /people/load?count=10000` | fills the `people` region with `person(0..count-1)` (PutStream, Any values) |
| `GET /people/get?i=42` | Get `p-00042` and unpack it: `{"found":true,"bytes":105,"person":{…}}` |
| `POST /people/put?i=42` | Put `person(42)` |
| `POST /query?p=…&p=…` | runs the OQL in the request body, as in 03; whole messages come back unpacked |

`person(i)` is deterministic:
- ages run from 18 to 90; there are 10 cities, 0–2 nicknames, and a role and an active flag that vary with `i`;
- **every 10th person (`i % 10 == 9`) has no address**.

```bash
curl -X POST 'localhost:8304/query?p=London&rows=2' --data 'SELECT p.first_name, p.age FROM /people p WHERE p.address.city = $1'
curl -X POST 'localhost:8304/query?p=p-00042' --data 'SELECT p.id, p.address FROM /people p WHERE p.id = $1'
```

```json
{"fieldNames":["id","address"],"types":["STRING","ANY"],"rowCount":1,
 "rows":[["p-00042",{"@type":"type.googleapis.com/person.v1.Address","city":"Paris","country":"France","lines":["43 Main Street"]}]], …}
```

## The narrated demo

The client runs this at startup (`make logs EX=04`) on its own regions: `demo-grpc-go` holds 200 people, and `demo-notes` holds Notes.

| # | Section | What it shows |
|---|---|---|
| 1 | Put and Get an Any | `person(42)` round-trips; the `type_url` is `type.googleapis.com/person.v1.Person`; the server stores the bytes as they are |
| 2 | Query into the fields | `WHERE p.address.city = $1`; `field_names` are the proto field names; string/int32/bool come back as STRING/INT32/BOOL; MIN/MAX over a nested filter |
| 3 | Enums, repeated fields, GROUP BY | enums compare and project **by name**; `$1 IN p.nicknames`; `.size`; `p.nicknames[0]` on an empty list → INTERNAL, and how to guard it; `GROUP BY p.address.city` |
| 4 | Missing values | an unset nested message is **NULL**; a field inside it is UNDEFINED → UNIMPLEMENTED; `IS_DEFINED`; proto3 defaults stay defaults |
| 5 | `SELECT *` | each row is the stored Any, which the client unpacks |
| 6 | A type with no descriptor | `Note` round-trips and `SELECT *` works, but a field query → FAILED_PRECONDITION, and one Note in a region breaks every field query there |

## How fields come across

| In the message | In OQL | As a result column |
|---|---|---|
| `string`, `int32`, `bool` | `p.first_name`, `p.age`, `p.active` | STRING, INT32, BOOL |
| nested message | `p.address.city` (dot path) | `p.address` → ANY, the nested message with its own type URL; **NULL when it isn't set** |
| enum | `p.role = 'ENGINEER'` (by name; `= 1` matches nothing) | STRING, the value's name |
| `repeated` | `p.nicknames[0]`, `p.nicknames.size`, `$1 IN p.nicknames` | the whole list → UNIMPLEMENTED (`java.util.ArrayList`) |
| the whole value | `SELECT * FROM /people p` | ANY, the stored Any |
| a field the type doesn't have | no match in `WHERE` | UNDEFINED → UNIMPLEMENTED |

Without `AS`, a column is named after its last path segment (`p.address.city` → `city`). Indexed
elements get generated names (`p.address.lines[0]` → `field$0`).

## Findings

1. **Unregistered types fail with FAILED_PRECONDITION, not INTERNAL.** The 1.0 docs say a field
   query over a type with no descriptor surfaces as `INTERNAL`. The server returns
   `FAILED_PRECONDITION: Failed to parse Protobuf Message type …: Protobuf type URL not registered`.
2. **One unreadable value breaks every field query on its region.** A single `Note` among 200
   `Person`s makes `SELECT COUNT(*) … WHERE p.age > 50` fail, not just skip that entry. Keep one
   message type per region, or deploy descriptors for everything you store.
3. **An out-of-range index fails the whole query, even in `WHERE`.** `p.nicknames[0] = 'Ace'`
   returns `INTERNAL: Index 0 out of bounds for length 0` as soon as one person has no nicknames.
   Guard it with `p.nicknames.size > 0 AND p.nicknames[0] = $1`, or use `$1 IN p.nicknames`.
4. **Here's the NULL 03 couldn't produce.** An unset nested message (`p.address` on someone with
   no address) comes back as an unset `QueryValue`, which the contract calls NULL. A field
   *inside* it (`p.address.city`) is UNDEFINED and fails the query with UNIMPLEMENTED, so filter
   with `IS_DEFINED(p.address.city)` or `p.address != NULL` first. proto3 scalars at their default
   (`active = false`, `age = 0`) are the default value, never NULL.
5. **Enums are names in OQL.** Compare with `'ENGINEER'`, not `1`, and expect the name back.
6. **Field queries cost real work on the servers.** Each one unpacks every `Person` it scans. In
   `people/steady` a field query over 10,000 people took a median of about 5 ms, against 0.6 ms
   for a Get. The servers' query execution time is on the dashboard.

## Load test results

`make select-load-test EX=04 SCRIPT=people/steady` (100 req/s for 1 minute, 10,000 people, Docker Desktop on an Apple Silicon Mac):

| op | Share | What it does | Rows | p50 | p99 |
|---|---|---|---|---|---|
| `get` | 50% | Get one Person (one RPC) and unpack it | | 0.64 ms | 1.93 ms |
| `put` | 20% | Put one Person as an Any | | 0.84 ms | 2.74 ms |
| `age-range` | 10% | `SELECT COUNT(*) … WHERE p.age >= $1 AND p.age < $2` | 1 | 4.58 ms | 7.96 ms |
| `nickname` | 5% | `SELECT p.id … WHERE $1 IN p.nicknames` | ~2,000 | 5.26 ms | 8.87 ms |
| `by-city` | 15% | `SELECT p.first_name, p.age … WHERE p.address.city = $1` | ~900 | 5.58 ms | 9.65 ms |

Overall the GemFire call's p50 was 0.9 ms and its p99 8.4 ms, with 0% failed. The client used
about 0.7 ms of CPU per request.

## What to look for in Grafana

The dashboard (`:3004`) follows the **load run** and **op** dropdowns.
- **Calls by op:** p50/p99 per op, rows/s per query, and a table over the time range. Gets and Puts stay under a millisecond, while field queries take several.
- **gRPC calls:** every RPC by method and status code. The demo's FAILED_PRECONDITION, UNIMPLEMENTED and INTERNAL show up here.
- **GemFire servers:**
  - Region gets and puts on `/people`.
  - Query executions on both servers, since every query on the partitioned region runs on both.
  - Average execution time, which includes unpacking each Person.

## Layout

```
04-structured-values/
├── .env                      # image, extension paths, ports (3004/9004/8304)
├── docker-compose.yml        # locator, server1, server2, gfsh-init (built), grpc-client-go, prometheus, grafana, cadvisor
├── proto/
│   ├── person/v1/person.proto   # the value type: in the client AND in the descriptor JAR
│   └── note/v1/note.proto       # in the client only (no descriptor deployed)
├── gemfire/
│   ├── Dockerfile            # gfsh-init image: builds person-types.jar from proto/
│   ├── start-member.sh       # starts a locator/server with the gRPC extension
│   └── init.gfsh             # regions, deploy the JAR, list protobuf-types
├── grpc-client-go/           # main · api (HTTP) · people (Any put/get/load) · query · demo · metrics · interceptors
└── monitoring/
    ├── prometheus.yml
    └── grafana/
        ├── generate-dashboard.py   # python3 monitoring/grafana/generate-dashboard.py
        ├── dashboards/structured-values.json
        └── provisioning/
```
