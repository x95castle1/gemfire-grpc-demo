package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The HTTP API that load tests (k6) and humans call. The same /crud API as 01, plus a channel
// choice:
//
//	GET  /                          endpoint list
//	GET  /health                    200 once the GemFire connection is up
//	GET  /metrics                   Prometheus metrics
//	GET  /demo                      result of the last demo run
//	POST /demo                      run the narrated demo; 200 = all checks passed
//	POST /crud/preload?count=&size= write count keys (bulk) so reads hit existing entries
//	GET  /crud/{op}?key=&size=      read ops:  get, contains-key, entry-count
//	POST /crud/{op}?key=&size=      write ops: put, get-and-put, remove, remove-if, ...
//
// Every /crud op takes via=direct|xds (default xds): the channel the call goes out on. The JSON
// and the X-Server header say which server answered.
//
// Every /crud response carries X-GemFire-Micros: the time spent in the GemFire call alone. k6
// records it next to its own end-to-end timing.

// op is one unary operation: its URL segment, RPC name (the `op` metric label in every client)
// and the JSON field its result goes in ("" for Put, which has no result).
type op struct{ path, rpc, field string }

var ops = []op{
	{"get", "Get", "valueBytes"},
	{"put", "Put", ""},
	{"get-and-put", "GetAndPut", "previousBytes"},
	{"remove", "Remove", "removed"},
	{"remove-if", "RemoveIf", "removed"},
	{"get-and-remove", "GetAndRemove", "removedBytes"},
	{"put-if-absent", "PutIfAbsent", "inserted"},
	{"put-if-absent-and-get", "PutIfAbsentAndGet", "valueBytes"},
	{"replace", "Replace", "replaced"},
	{"replace-if", "ReplaceIf", "replaced"},
	{"get-and-replace", "GetAndReplace", "previousBytes"},
	{"contains-key", "ContainsKey", "contains"},
	{"entry-count", "EntryCount", "size"},
}

const maxPayloadBytes = 16 * 1024 * 1024

type Api struct {
	cfg      Config
	metrics  *Metrics
	crud     *GrpcCrud
	demo     func() Report
	demoMu   sync.Mutex
	lastDemo *Report
}

func newApi(cfg Config, metrics *Metrics, crud *GrpcCrud, demo func() Report) *Api {
	return &Api{cfg: cfg, metrics: metrics, crud: crud, demo: demo}
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
	mux.HandleFunc("/crud/", a.crudHandler)

	// Go serves each connection on its own goroutine. The Java clients serve requests on a fixed
	// pool of HTTP_THREADS threads; this semaphore caps in-flight requests the same way, so
	// concurrency is identical on every side of the comparison.
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s)\n\n", a.cfg.Client, a.cfg.Example)
	sb.WriteString("GET  /health\nGET  /metrics\nGET  /demo\nPOST /demo\n")
	fmt.Fprintf(&sb, "POST /crud/preload?count=10000&size=%d\n", a.cfg.PayloadBytes)
	for _, o := range ops {
		method := "POST "
		if isRead(o.rpc) {
			method = "GET  "
		}
		query := "?key=42"
		if o.rpc == "EntryCount" {
			query = ""
		}
		sb.WriteString(method + "/crud/" + o.path + query + "\n")
	}
	send(w, r, 200, "text/plain", sb.String())
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

func (a *Api) crudHandler(w http.ResponseWriter, r *http.Request) {
	segment := strings.TrimPrefix(r.URL.Path, "/crud/")
	q := r.URL.Query()

	size := a.cfg.PayloadBytes
	if s := q.Get("size"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > maxPayloadBytes {
			badRequest(w, r, fmt.Sprintf("size must be 0..%d", maxPayloadBytes))
			return
		}
		size = n
	}

	if segment == "preload" {
		count, err := strconv.Atoi(cmp.Or(q.Get("count"), "10000"))
		if err != nil {
			badRequest(w, r, "count must be a number")
			return
		}
		start := time.Now()
		written, err := a.crud.preload(count, size)
		if err != nil {
			send(w, r, 500, "application/json", fmt.Sprintf(`{"error":%s}`, jsonString(err.Error())))
			return
		}
		micros := time.Since(start).Microseconds()
		setMicros(w, micros)
		send(w, r, 200, "application/json", fmt.Sprintf(`{"op":"Preload","written":%d,"micros":%d}`, written, micros))
		return
	}

	var o *op
	for i := range ops {
		if ops[i].path == segment {
			o = &ops[i]
		}
	}
	if o == nil {
		send(w, r, 404, "application/json", fmt.Sprintf(`{"error":%s,"ops":"GET /"}`, jsonString("unknown op: "+segment)))
		return
	}
	via := cmp.Or(q.Get("via"), "xds")
	if via != "direct" && via != "xds" {
		badRequest(w, r, "via must be direct or xds")
		return
	}
	key, hasKey := q.Get("key"), q.Has("key")
	if !hasKey && o.rpc != "EntryCount" {
		badRequest(w, r, "key is required")
		return
	}

	start := time.Now()
	result, server, err := a.crud.execute(via, o.rpc, a.cfg.KeyPrefix+key, size)
	elapsed := time.Since(start)
	w.Header().Set("X-Server", server)
	if err != nil {
		code := errorCode(err)
		a.metrics.recordOp(via, o.rpc, code, elapsed) // failures are timed too
		w.Header().Set("X-Error-Code", code)
		send(w, r, 502, "application/json", fmt.Sprintf(`{"op":"%s","via":"%s","server":"%s","error":"%s","message":%s}`,
			o.rpc, via, server, code, jsonString(err.Error())))
		return
	}
	a.metrics.recordOp(via, o.rpc, "OK", elapsed)

	var body strings.Builder
	fmt.Fprintf(&body, `{"op":"%s","via":"%s","server":"%s"`, o.rpc, via, server)
	if hasKey {
		fmt.Fprintf(&body, `,"key":%s`, jsonString(key))
	}
	if o.field != "" {
		if result == nil {
			result = "null"
		}
		fmt.Fprintf(&body, `,"%s":%v`, o.field, result)
	}
	fmt.Fprintf(&body, `,"micros":%d}`, elapsed.Microseconds())
	setMicros(w, elapsed.Microseconds())
	send(w, r, 200, "application/json", body.String())
}

func isRead(rpc string) bool {
	return rpc == "Get" || rpc == "ContainsKey" || rpc == "EntryCount"
}

// setMicros sets X-GemFire-Micros with the same spelling as the Java clients (Header().Set would
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
