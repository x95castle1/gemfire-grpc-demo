#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/crud-native-vs-grpc.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 01-crud-basics/ (Python 3 standard library only). Grafana picks up the new JSON within
about 10 seconds; no restart is needed.
"""
import json
import pathlib
import sys

DS = {"type": "prometheus", "uid": "prometheus"}

# Entity colors (validated dark-mode categorical slots; same across all examples).
CLIENT_COLORS = {"native-java": "#3987e5", "grpc-java": "#d95926", "grpc-go": "#199e70"}
NODE_COLORS = {"server1": "#c98500", "server2": "#d55181", "locator": "#9085e9"}
SERVICE_COLORS = {"native-client-java": "#3987e5", "grpc-client-java": "#d95926", "grpc-client-go": "#199e70",
                  "server1": "#c98500", "server2": "#d55181", "locator": "#9085e9"}

_id = 0


def nid():
    global _id
    _id += 1
    return _id


def color_overrides(colors):
    return [{"matcher": {"id": "byRegexp", "options": f"^{name}( .*)?$"},
             "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": c}}]}
            for name, c in colors.items()]


def target(expr, legend="", ref="A", instant=False, fmt=None):
    t = {"datasource": DS, "expr": expr, "legendFormat": legend, "refId": ref}
    if instant:
        t["instant"] = True
        t["range"] = False
    if fmt:
        t["format"] = fmt
    return t


def row(title, y):
    return {"type": "row", "title": title, "id": nid(), "collapsed": False,
            "gridPos": {"h": 1, "w": 24, "x": 0, "y": y}, "panels": []}


def text(content, x, y, w, h):
    return {"type": "text", "id": nid(), "title": "", "gridPos": {"h": h, "w": w, "x": x, "y": y},
            "options": {"mode": "markdown", "content": content}, "transparent": True}


def timeseries(title, targets, x, y, w, h, unit="short", colors=None, desc="", stack=False,
               decimals=None, min0=True):
    defaults = {
        "unit": unit,
        "color": {"mode": "palette-classic"},
        "custom": {"lineWidth": 2, "fillOpacity": 0 if not stack else 60, "showPoints": "never",
                   "spanNulls": True, "axisSoftMin": 0 if min0 else None,
                   "stacking": {"mode": "normal" if stack else "none", "group": "A"},
                   "gradientMode": "none", "axisBorderShow": False},
    }
    if decimals is not None:
        defaults["decimals"] = decimals
    return {
        "type": "timeseries", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y}, "targets": targets,
        "fieldConfig": {"defaults": defaults, "overrides": color_overrides(colors or {})},
        "options": {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
    }


def stat(title, targets, x, y, w, h, unit="short", colors=None, desc="", decimals=None,
         overrides=None, text_mode="value_and_name", orientation="horizontal", text=None):
    defaults = {"unit": unit, "color": {"mode": "fixed", "fixedColor": "text"},
                "thresholds": {"mode": "absolute", "steps": [{"color": "text", "value": None}]}}
    if decimals is not None:
        defaults["decimals"] = decimals
    return {
        "type": "stat", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        # Instant queries: the last point of a range query is often a partial window.
        "targets": [dict(t, instant=True, range=False) for t in targets],
        "fieldConfig": {"defaults": defaults,
                        "overrides": color_overrides(colors or {}) + (overrides or [])},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "orientation": orientation, "textMode": text_mode, "colorMode": "value",
                    "graphMode": "none", "justifyMode": "auto", "wideLayout": True,
                    **({"text": text} if text else {})},
    }


# Shows NaN (no traffic in the window) as a dash instead of "NaN".
NO_DATA = {"type": "special", "options": {"match": "nan", "result": {"text": "–", "color": "text"}}}


def ratio_words():
    """Value mappings that show a ratio (A ÷ B) as words: "22% faster", "254% slower", "same".

    Grafana mappings can't interpolate the value, so there is one range per whole percent (and
    per 10% above 500%). Display only: sorting still uses the underlying ratio."""
    GREEN, RED = "#3fa66b", "#e0524c"
    def rng(lo, hi, text, color):
        return {"type": "range", "options": {"from": lo, "to": hi, "result": {"text": text, "color": color}}}
    m = [NO_DATA, rng(0.995, 1.005, "same", "text")]
    m += [rng(1 - (n + 0.5) / 100, 1 - (n - 0.5) / 100, f"{n}% faster", GREEN) for n in range(1, 100)]
    steps = list(range(1, 500)) + list(range(500, 2001, 10))
    for i, n in enumerate(steps):
        half = 0.5 if n < 500 else 5
        m.append(rng(1 + (n - half) / 100, 1 + (n + half) / 100, f"{n}% slower", RED))
    m.append(rng(1 + 2005 / 100, 1e12, "over 2000% slower", RED))
    return m


LAT = "demo_op_latency_seconds"
W = "[1m]"


def q(p, by="client", filt="", window=W):
    return f'histogram_quantile({p}, sum by (le, {by}) (rate({LAT}_bucket{{{filt}}}{window})))'


panels = []
y = 0
panels.append(text(
    "### 01 · CRUD basics: native Java client vs. GemFire for gRPC (Java and Go)\n"
    "All three clients are small HTTP services with the same `/crud` API. Load comes from **k6** "
    "(`make select-load-test`), and each run shows up as a shaded annotation. "
    "**native-java**: `ClientCache` through the locator, PR single-hop. "
    "**grpc-java** and **grpc-go**: a plaintext channel each, straight to `server1:50051`. "
    "*End-to-end* = the HTTP request as k6 saw it. *GemFire call* = time inside the gRPC RPC / "
    "Region API call, measured in the client.",
    0, y, 24, 3))
y += 3

# --- At a glance -------------------------------------------------------------------------------
panels.append(row("At a glance (GemFire calls made by each client)", y)); y += 1
ORDER = ["native-java", "grpc-java", "grpc-go"]
def per_client(expr_for):
    return [target(expr_for(c), c, chr(65 + i)) for i, c in enumerate(ORDER)]
panels.append(stat("Throughput", per_client(lambda c: f'sum(rate({LAT}_count{{client="{c}"}}{W}))'),
                   0, y, 6, 4, unit="ops", colors=CLIENT_COLORS, decimals=0,
                   desc="Successful GemFire calls per second, per client (driven by k6)."))
panels.append(stat("p50 latency", per_client(lambda c: q(0.5, "client", f'client="{c}"')), 6, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2))
panels.append(stat("p99 latency", per_client(lambda c: q(0.99, "client", f'client="{c}"')), 12, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2))
panels.append(stat(
    "Demo checks",
    [target(f'sum(demo_checks_total{{client="{c}", result="{r}"}})', f"{c} {m}", f"{m2}{i}")
     for i, c in enumerate(ORDER) for r, m, m2 in (("pass", "✓", "P"), ("fail", "✗", "F"))],
    18, y, 6, 4, decimals=0,
    desc="Assertions from the narrated demo (runs at client startup and on POST /demo): ✓ passed, ✗ failed. Any failure turns red.",
    orientation="vertical", text={"titleSize": 13, "valueSize": 30},
    overrides=[{"matcher": {"id": "byRegexp", "options": ".*✗"},
                "properties": [{"id": "thresholds", "value": {"mode": "absolute", "steps": [
                    {"color": "green", "value": None}, {"color": "red", "value": 1}]}},
                    {"id": "color", "value": {"mode": "thresholds"}}]}]))
y += 4

# --- k6 ----------------------------------------------------------------------------------------
K6 = 'run=~"$run"'
panels.append(row("Load test (k6): end-to-end vs. GemFire call", y)); y += 1
def k6q(p, metric):
    return f'histogram_quantile({p}, sum by (client) (rate({metric}{{{K6}}}[1m])))'
dashed = [{"matcher": {"id": "byRegexp", "options": ".* GemFire call"},
           "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 6]}}]}]
p = timeseries("Requests/s (k6)", [target(f"sum by (client) (rate(k6_http_reqs_total{{{K6}}}[30s]))", "{{client}}")],
               0, y, 6, 8, unit="reqps", colors=CLIENT_COLORS,
               desc="Requests k6 completed per second, per target client. Filter runs with the `run` variable.")
panels.append(p)
for i, (pct, label) in enumerate([(0.5, "p50"), (0.99, "p99")]):
    p = timeseries(f"{label}: end-to-end vs. GemFire call",
                   [target(k6q(pct, "k6_http_req_duration_seconds"), "{{client}} end-to-end", "A"),
                    target(k6q(pct, "k6_gemfire_call_duration_seconds"), "{{client}} GemFire call", "B")],
                   6 + i * 6, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                   desc="Solid = end-to-end (k6), dashed = GemFire call (in the client). The gap between a client's two lines is HTTP + app overhead: "
                        "the JDK HttpServer in the Java clients, net/http in grpc-go. Compare clients on the GemFire call.")
    p["fieldConfig"]["overrides"] += dashed
    panels.append(p)
panels.append(timeseries(
    "k6 VUs and failed requests/s",
    [target(f"sum by (client) (k6_vus{{{K6}}})", "{{client}} VUs", "A"),
     target(f'sum by (client) (rate(k6_http_reqs_total{{{K6}, expected_response="false"}}[30s]))', "{{client}} failed/s", "B")],
    18, y, 6, 8, unit="short", colors=CLIENT_COLORS,
    desc="VUs climb when a client can't keep up with an arrival-rate script."))
y += 8

# --- Latency -----------------------------------------------------------------------------------
panels.append(row("GemFire call latency (in the client): native vs gRPC", y)); y += 1
panels.append(timeseries("p50 latency (all ops)", [target(q(0.5), "{{client}}")], 0, y, 8, 8,
                         unit="s", colors=CLIENT_COLORS))
panels.append(timeseries("p99 latency (all ops)", [target(q(0.99), "{{client}}")], 8, y, 8, 8,
                         unit="s", colors=CLIENT_COLORS))
panels.append(timeseries(
    "p50 latency for op: $op", [target(q(0.5, "client", 'op=~"$op"'), "{{client}}")],
    16, y, 8, 8, unit="s", colors=CLIENT_COLORS,
    desc="Pick operations with the `op` variable at the top of the dashboard."))
y += 8

# Per-op comparison table (instant queries over the last 5m, merged on `op`).
w5 = "[5m]"
tbl_targets = [
    target(q(0.5, "op", 'client="native-java"', w5), "", "A", True, "table"),
    target(q(0.5, "op", 'client="grpc-java"', w5), "", "B", True, "table"),
    target(q(0.5, "op", 'client="grpc-go"', w5), "", "C", True, "table"),
    target(q(0.99, "op", 'client="native-java"', w5), "", "D", True, "table"),
    target(q(0.99, "op", 'client="grpc-java"', w5), "", "E", True, "table"),
    target(q(0.99, "op", 'client="grpc-go"', w5), "", "F", True, "table"),
    target(f'sum by (op) (rate({LAT}_count{{client="native-java"}}{w5}))', "", "G", True, "table"),
    target(f'sum by (op) (rate({LAT}_count{{client="grpc-java"}}{w5}))', "", "H", True, "table"),
    target(f'sum by (op) (rate({LAT}_count{{client="grpc-go"}}{w5}))', "", "I", True, "table"),
]
JAVA_RATIO, GO_RATIO = "grpc-java vs native", "grpc-go vs native"
panels.append({
    "type": "table", "id": nid(), "title": "Per-operation latency (last 5 minutes)",
    "description": "java = grpc-java, go = grpc-go. The \"vs native\" columns compare p50s: \"254% slower\" means that gRPC client's median call took 254% longer than native's. Native GetAndPut, "
                   "Remove and GetAndRemove are two round trips: client put()/remove() don't "
                   "return the old value. See the README mapping table.",
    "datasource": DS, "gridPos": {"h": 15, "w": 24, "x": 0, "y": y}, "targets": tbl_targets,
    "transformations": [
        {"id": "merge", "options": {}},
        {"id": "calculateField", "options": {
            "mode": "binary", "alias": JAVA_RATIO,
            "binary": {"left": "Value #B", "operator": "/", "right": "Value #A"},
            "replaceFields": False}},
        {"id": "calculateField", "options": {
            "mode": "binary", "alias": GO_RATIO,
            "binary": {"left": "Value #C", "operator": "/", "right": "Value #A"},
            "replaceFields": False}},
        {"id": "organize", "options": {
            "excludeByName": {"Time": True},
            "indexByName": {"op": 0, "Value #A": 1, "Value #B": 2, "Value #C": 3, JAVA_RATIO: 4,
                            GO_RATIO: 5, "Value #D": 6, "Value #E": 7, "Value #F": 8,
                            "Value #G": 9, "Value #H": 10, "Value #I": 11},
            "renameByName": {"op": "Operation",
                             "Value #A": "native p50", "Value #B": "java p50", "Value #C": "go p50",
                             "Value #D": "native p99", "Value #E": "java p99", "Value #F": "go p99",
                             "Value #G": "native ops/s", "Value #H": "java ops/s", "Value #I": "go ops/s"}}},
        {"id": "sortBy", "options": {"sort": [{"field": JAVA_RATIO, "desc": True}]}},
    ],
    "fieldConfig": {
        "defaults": {"unit": "s", "decimals": 2, "custom": {"align": "auto", "width": 128},
                     "mappings": [NO_DATA]},
        "overrides": [
            {"matcher": {"id": "byRegexp", "options": ".* vs native$"},
             "properties": [{"id": "unit", "value": "none"}, {"id": "custom.width", "value": 165},
                            {"id": "mappings", "value": ratio_words()},
                            {"id": "custom.cellOptions", "value": {"type": "color-text"}}]},
            {"matcher": {"id": "byRegexp", "options": ".*ops/s"},
             "properties": [{"id": "unit", "value": "ops"}, {"id": "decimals", "value": 0}]},
            {"matcher": {"id": "byName", "options": "Operation"},
             "properties": [{"id": "unit", "value": "string"}, {"id": "custom.width", "value": 170}]},
        ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
})
y += 15

# --- Throughput & errors -----------------------------------------------------------------------
panels.append(row("Throughput, tail latency & errors", y)); y += 1
panels.append(timeseries("Ops/s by client", [target(f"sum by (client) (rate({LAT}_count{W}))", "{{client}}")],
                         0, y, 8, 8, unit="ops", colors=CLIENT_COLORS))
panels.append(timeseries(
    "p99 latency for op: $op", [target(q(0.99, "client", 'op=~"$op"'), "{{client}}")],
    8, y, 8, 8, unit="s", colors=CLIENT_COLORS,
    desc="Pick operations with the `op` variable at the top of the dashboard."))
panels.append(timeseries("Errors/s by client and code",
                         [target("sum by (client, code) (rate(demo_errors_total[1m])) or sum by (client) (rate(demo_op_latency_seconds_count[1m])) * 0", "{{client}} {{code}}")],
                         16, y, 8, 8, unit="ops", colors=CLIENT_COLORS,
                         desc="gRPC: status code. Native: exception class. Flat zero = no errors."))
y += 8

# --- GemFire cluster ---------------------------------------------------------------------------
panels.append(row("GemFire servers: what the cluster sees", y)); y += 1
gf = 'exported_instance="/crud"'
panels.append(timeseries("Region gets/s per server (/crud)",
                         [target(f"sum by (node) (rate(gemfire_getsCompleted{{{gf}}}[1m]))", "{{node}}")],
                         0, y, 6, 8, unit="ops", colors=NODE_COLORS,
                         desc="PartitionedRegionStats.getsCompleted, from all clients combined."))
panels.append(timeseries("Region puts/s per server (/crud)",
                         [target(f"sum by (node) (rate(gemfire_putsCompleted{{{gf}}}[1m]))", "{{node}}")],
                         6, y, 6, 8, unit="ops", colors=NODE_COLORS))
panels.append(timeseries(
    "Puts forwarded to the owning server",
    [target(f"sum by (node) (rate(gemfire_putRemoteCompleted{{{gf}}}[1m]))", "{{node}}")],
    12, y, 6, 8, unit="ops", colors=NODE_COLORS,
    desc="PartitionedRegionStats.putRemoteCompleted: puts that arrived at a server that doesn't "
         "own the bucket and had to be forwarded. Both gRPC clients send everything to server1, so "
         "about half their writes take this extra hop. Native single-hop sends each op straight "
         "to the owner."))
panels.append(timeseries(
    "Client connections per server",
    [target("sum by (node) (gemfire_currentClientConnections)", "native {{node}}", "A"),
     target("sum by (node) (gemfire_grpc_server_connections)", "gRPC {{node}}", "B")],
    18, y, 6, 8, unit="short", decimals=0,
    desc="native: CacheServerStats.currentClientConnections (pooled sockets). gRPC: "
         "GrpcServerStats.grpc.server.connections (one multiplexed HTTP/2 connection per gRPC client). The only "
         "statistic the gRPC extension registers."))
y += 8
panels.append(timeseries(
    "Heap used per member",
    [target('sum by (node) (gemfire_usedMemory{exported_instance="vmHeapMemoryStats"})', "{{node}}")],
    0, y, 12, 8, unit="bytes", colors=NODE_COLORS))
panels.append(timeseries(
    "Cache-server (native protocol) requests/s",
    [target("sum by (node) (rate(gemfire_getRequests[1m]))", "get {{node}}", "A"),
     target("sum by (node) (rate(gemfire_putRequests[1m]))", "put {{node}}", "B")],
    12, y, 12, 8, unit="ops",
    desc="CacheServerStats counts only native-protocol traffic on port 40404. gRPC requests "
         "don't pass through the cache server, so they don't show up here."))
y += 8

# --- Resource cost ----------------------------------------------------------------------------
panels.append(row("Resource cost (cAdvisor)", y)); y += 1
cpu = "sum by (service) (rate(container_cpu_usage_seconds_total{service=~\"%s\"}[1m]))"
panels.append(timeseries("CPU: clients (cores)", [target(cpu % ".*-client-.*", "{{service}}")],
                         0, y, 6, 8, unit="short", colors=SERVICE_COLORS, decimals=2))
panels.append(timeseries("CPU: GemFire members (cores)",
                         [target(cpu % "locator|server1|server2", "{{service}}")],
                         6, y, 6, 8, unit="short", colors=SERVICE_COLORS, decimals=2,
                         desc="server1 is the gRPC entry point. It also decodes protobuf and "
                              "forwards requests for buckets it doesn't own."))
per_op = (
    'sum by (client) (label_replace(rate(container_cpu_usage_seconds_total{service=~".*-client-.*"}[1m]),'
    ' "client", "$1-$2", "service", "(.*)-client-(.*)"))'
    f" / sum by (client) (rate({LAT}_count{W}))")
panels.append(timeseries("Client CPU per operation", [target(per_op, "{{client}}")],
                         12, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                         desc="Client container CPU-seconds ÷ ops completed. Lower is cheaper."))
panels.append(timeseries("Memory working set",
                         [target('sum by (service) (container_memory_working_set_bytes{service=~".*-client-.*|locator|server1|server2"})', "{{service}}")],
                         18, y, 6, 8, unit="bytes", colors=SERVICE_COLORS))
y += 8

dashboard = {
    "uid": "crud-native-vs-grpc",
    "title": "01 · CRUD basics: native vs gRPC",
    "tags": ["gemfire", "grpc", "01-crud-basics"],
    "timezone": "browser",
    "schemaVersion": 41,
    "version": 1,
    "editable": True,
    "graphTooltip": 1,
    "time": {"from": "now-15m", "to": "now"},
    "refresh": "10s",
    "templating": {"list": [{
        "name": "run", "label": "load run", "type": "query", "datasource": DS,
        "query": {"query": "label_values(k6_http_reqs_total, run)", "refId": "run"},
        "definition": "label_values(k6_http_reqs_total, run)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 2,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "op", "label": "op", "type": "query", "datasource": DS,
        "query": {"query": f"label_values({LAT}_count, op)", "refId": "op"},
        "definition": f"label_values({LAT}_count, op)",
        "multi": True, "includeAll": True, "refresh": 2, "sort": 1,
        "current": {"selected": True, "text": ["Get"], "value": ["Get"]},
    }]},
    "annotations": {"list": [{
        "name": "Load tests", "enable": True, "iconColor": "rgba(150, 150, 150, 0.6)",
        "datasource": {"type": "grafana", "uid": "-- Grafana --"},
        "target": {"type": "tags", "tags": ["load-test"], "limit": 100, "matchAny": False},
    }]},
    "panels": panels,
}

out = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else \
    pathlib.Path(__file__).parent / "dashboards" / "crud-native-vs-grpc.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} panels)")
