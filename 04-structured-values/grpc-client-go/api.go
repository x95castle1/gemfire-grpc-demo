package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "grpc-client-go/gen/gemfirev1"

	"google.golang.org/protobuf/encoding/protojson"
)

// The HTTP API that load tests (k6) and humans call:
//
//	GET  /                              endpoint list
//	GET  /health                        200 once the HTTP API is up (after the startup demo)
//	GET  /metrics                       Prometheus metrics
//	GET  /demo                          result of the last demo run
//	POST /demo                          run the narrated demo; 200 = all checks passed
//	POST /people/load?count=10000       fill the `people` region: p-00000.. → Person messages (as Any)
//	GET  /people/get?i=42               Get p-00042 and unpack it (JSON)
//	POST /people/put?i=42               Put person(42) as an Any
//	POST /query?p=..&p=..&name=&rows=   run the OQL in the request body; p binds $1, $2, ...
//
// A p that parses as an int32 is bound as one, anything else as a string ("int64:", "double:"
// and "string:" prefixes force a type). name labels the query in metrics (default "Query"), and
// rows is how many rows to return in the JSON (default 20; every row is still read and counted).
//
// Every /people and /query response carries X-GemFire-Micros (the GemFire call alone, as k6's
// GemFire time). /query responses also carry X-Rows and X-Messages. A query result that is a
// whole message (SELECT * over Person values) comes back in the JSON as the unpacked message.

const (
	defaultQueryName = "Query"
	defaultRows      = 20
	maxLoad          = 1_000_000
)

// validName keeps the metric label to short, tidy names (k6 passes by-city, age-range, ...).
var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

type Api struct {
	cfg      Config
	metrics  *Metrics
	people   *People
	queries  *GrpcQuery
	demo     func() Report
	demoMu   sync.Mutex
	lastDemo *Report
}

func newApi(cfg Config, metrics *Metrics, people *People, queries *GrpcQuery, demo func() Report) *Api {
	return &Api{cfg: cfg, metrics: metrics, people: people, queries: queries, demo: demo}
}

// runDemo runs the demo now (used at startup) and remembers the result for GET /demo.
func (a *Api) runDemo() Report {
	a.demoMu.Lock()
	defer a.demoMu.Unlock()
	r := a.demo()
	a.lastDemo = &r
	return r
}

func (a *Api) start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.index)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { send(w, r, 200, "text/plain", "ok") })
	mux.Handle("/metrics", a.metrics.handler())
	mux.HandleFunc("/demo", a.demoHandler)
	mux.HandleFunc("/query", a.queryHandler)
	mux.HandleFunc("/people/load", a.loadHandler)
	mux.HandleFunc("/people/get", a.getHandler)
	mux.HandleFunc("/people/put", a.putHandler)

	// Go serves each connection on its own goroutine; this semaphore caps in-flight requests at
	// HTTP_THREADS, like the other examples' clients.
	slots := make(chan struct{}, a.cfg.HTTPThreads)
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slots <- struct{}{}
		defer func() { <-slots }()
		mux.ServeHTTP(w, r)
	})

	fmt.Printf("[%s] HTTP API listening on :%d (%d concurrent requests)\n", a.cfg.Client, a.cfg.Port, a.cfg.HTTPThreads)
	srv := &http.Server{Addr: fmt.Sprintf(":%d", a.cfg.Port), Handler: limited, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

func (a *Api) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		send(w, r, 404, "application/json", `{"error":"not found"}`)
		return
	}
	text := fmt.Sprintf("%s (%s), region %s\n\n", a.cfg.Client, a.cfg.Example, a.cfg.Region) +
		"GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n" +
		"POST /people/load?count=10000\nGET  /people/get?i=42\nPOST /people/put?i=42\n" +
		"POST /query?p=London     body: SELECT p.first_name, p.age FROM /" + a.cfg.Region + " p WHERE p.address.city = $1\n\n" +
		"e.g. curl -X POST 'localhost:8304/query?p=London' --data 'SELECT p.first_name, p.age FROM /" + a.cfg.Region + " p WHERE p.address.city = $1'\n"
	send(w, r, 200, "text/plain", text)
}

func (a *Api) demoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		rep := a.runDemo()
		code := 200
		if rep.Failed > 0 {
			code = 500
		}
		send(w, r, code, "text/plain; charset=utf-8", rep.Text)
		return
	}
	a.demoMu.Lock()
	last := a.lastDemo
	a.demoMu.Unlock()
	body := `{"ran":false}`
	if last != nil {
		body = fmt.Sprintf(`{"ran":true,"passed":%d,"failed":%d}`, last.Passed, last.Failed)
	}
	send(w, r, 200, "application/json", body)
}

func (a *Api) loadHandler(w http.ResponseWriter, r *http.Request) {
	count, err := strconv.Atoi(cmp.Or(r.URL.Query().Get("count"), "10000"))
	if err != nil || count < 0 || count > maxLoad {
		badRequest(w, r, fmt.Sprintf("count must be 0..%d", maxLoad))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	if err := a.people.load(ctx, a.cfg.Region, count); err != nil {
		w.Header().Set("X-Error-Code", errorCode(err))
		send(w, r, 502, "application/json", fmt.Sprintf(`{"error":"%s","message":%s}`, errorCode(err), jsonString(err.Error())))
		return
	}
	micros := time.Since(start).Microseconds()
	setMicros(w, micros)
	send(w, r, 200, "application/json", fmt.Sprintf(`{"op":"Load","region":%s,"written":%d,"micros":%d}`, jsonString(a.cfg.Region), count, micros))
}

// personIndex reads ?i= (0..maxLoad).
func personIndex(w http.ResponseWriter, r *http.Request) (int, bool) {
	i, err := strconv.Atoi(r.URL.Query().Get("i"))
	if err != nil || i < 0 || i > maxLoad {
		badRequest(w, r, fmt.Sprintf("i must be 0..%d", maxLoad))
		return 0, false
	}
	return i, true
}

func (a *Api) getHandler(w http.ResponseWriter, r *http.Request) {
	i, ok := personIndex(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	start := time.Now()
	p, size, err := a.people.get(ctx, a.cfg.Region, i)
	elapsed := time.Since(start)
	if a.failed(w, r, "get", err, elapsed) {
		return
	}
	a.metrics.recordOp("get", "OK", elapsed)
	person := []byte("null")
	if p != nil {
		person, _ = protojson.Marshal(p)
	}
	setMicros(w, elapsed.Microseconds())
	send(w, r, 200, "application/json", fmt.Sprintf(`{"op":"Get","id":%s,"found":%t,"bytes":%d,"person":%s,"micros":%d}`,
		jsonString(personID(i)), p != nil, size, person, elapsed.Microseconds()))
}

func (a *Api) putHandler(w http.ResponseWriter, r *http.Request) {
	i, ok := personIndex(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	start := time.Now()
	err := a.people.put(ctx, a.cfg.Region, i)
	elapsed := time.Since(start)
	if a.failed(w, r, "put", err, elapsed) {
		return
	}
	a.metrics.recordOp("put", "OK", elapsed)
	setMicros(w, elapsed.Microseconds())
	send(w, r, 200, "application/json", fmt.Sprintf(`{"op":"Put","id":%s,"micros":%d}`, jsonString(personID(i)), elapsed.Microseconds()))
}

// failed answers 502 with X-Error-Code when a GemFire call failed (and times the failure).
func (a *Api) failed(w http.ResponseWriter, r *http.Request, op string, err error, elapsed time.Duration) bool {
	if err == nil {
		return false
	}
	code := errorCode(err)
	a.metrics.recordOp(op, code, elapsed)
	w.Header().Set("X-Error-Code", code)
	send(w, r, 502, "application/json", fmt.Sprintf(`{"op":%s,"error":"%s","message":%s}`, jsonString(op), code, jsonString(statusMessage(err))))
	return true
}

func (a *Api) queryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		badRequest(w, r, "POST the OQL as the request body")
		return
	}
	q := r.URL.Query()
	name := cmp.Or(q.Get("name"), defaultQueryName)
	if !validName.MatchString(name) {
		badRequest(w, r, "name must be 1-32 letters, digits, - or _")
		return
	}
	keep := defaultRows
	if s := q.Get("rows"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			badRequest(w, r, "rows must be a number >= 0")
			return
		}
		keep = n
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	oql := strings.TrimSpace(string(body))
	if err != nil || oql == "" {
		badRequest(w, r, "POST the OQL as the request body")
		return
	}
	var params []*pb.Value
	for _, p := range q["p"] {
		params = append(params, param(p))
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	start := time.Now()
	res, err := a.queries.run(ctx, oql, params, keep)
	elapsed := time.Since(start)
	if err != nil {
		code := errorCode(err)
		a.metrics.recordOp(name, code, elapsed) // failures are timed too
		w.Header().Set("X-Error-Code", code)
		send(w, r, 502, "application/json", fmt.Sprintf(`{"query":%s,"error":"%s","message":%s}`,
			jsonString(name), code, jsonString(statusMessage(err))))
		return
	}
	a.metrics.recordOp(name, "OK", elapsed)
	a.metrics.recordRows(name, res.RowCount)

	rows := make([][]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		values := make([]any, 0, len(row))
		for _, v := range row {
			values = append(values, queryValue(v))
		}
		rows = append(rows, values)
	}
	out, _ := json.Marshal(struct {
		Query          string   `json:"query"`
		FieldNames     []string `json:"fieldNames"`
		Types          []string `json:"types"`
		RowCount       int      `json:"rowCount"`
		Messages       int      `json:"messages"`
		Batches        []int    `json:"batches"`
		FirstRowMicros int64    `json:"firstRowMicros"`
		Micros         int64    `json:"micros"`
		Rows           [][]any  `json:"rows"`
	}{name, nonNil(res.FieldNames), columnTypes(res.Rows), res.RowCount, res.Messages, nonNil(res.Batches),
		res.FirstBatch.Microseconds(), elapsed.Microseconds(), rows})
	setMicros(w, elapsed.Microseconds())
	w.Header().Set("X-Rows", strconv.Itoa(res.RowCount))
	w.Header().Set("X-Messages", strconv.Itoa(res.Messages))
	send(w, r, 200, "application/json", string(out))
}

// columnTypes names each column's QueryValue case (INT32, STRING, ..., NULL) from the first row
// returned, so a reader can see how GemFire's Java types came across.
func columnTypes(rows [][]*pb.QueryValue) []string {
	types := []string{}
	if len(rows) > 0 {
		for _, v := range rows[0] {
			types = append(types, queryCase(v))
		}
	}
	return types
}

// nonNil makes an empty slice encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// setMicros sets X-GemFire-Micros with the same spelling as the Java clients in 01/02 (Header().Set would
// canonicalize it to X-Gemfire-Micros; header names are case-insensitive either way).
func setMicros(w http.ResponseWriter, micros int64) {
	w.Header()["X-GemFire-Micros"] = []string{strconv.FormatInt(micros, 10)}
}

func badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	send(w, r, 400, "application/json", fmt.Sprintf(`{"error":%s}`, jsonString(msg)))
}

func send(w http.ResponseWriter, r *http.Request, status int, contentType, body string) {
	// Drain any request body so keep-alive connections can be reused.
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
