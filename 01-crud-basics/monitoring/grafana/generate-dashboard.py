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
                   "spanNulls": 60000, "axisSoftMin": 0 if min0 else None,
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


panels = []
y = 0
panels.append(text(
    "### 01 · CRUD basics: native Java client vs. GemFire for gRPC (Java and Go)\n"
    "**native-java**: `ClientCache` through the locator, single-hop. **grpc-java** / **grpc-go**: a plaintext "
    "channel each, straight to `server1:50051`. Load comes from k6 (`make select-load-test`); each run is a "
    "shaded band, and the **load run** dropdown picks which runs the panels show. "
    "*End-to-end* = the HTTP request as k6 saw it. *GemFire call* = time inside the gRPC RPC / Region API call.",
    0, y, 24, 3))
y += 3

# Everything that measures load reads k6's metrics, which carry client, run and op tags. So every
# latency and throughput panel follows the "load run" dropdown, and the numbers match the
# terminal table `make select-load-test` prints. (The client-side demo_op_latency_seconds
# histogram measures the same GemFire call, but it has no run tag.)
ORDER = ["native-java", "grpc-java", "grpc-go"]
RUN = 'run=~"$run"'
OP = 'op=~"$op"'
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the GemFire call alone
E2E = "k6_http_req_duration_seconds"      # the whole HTTP request as k6 saw it
REQS = "k6_http_reqs_total"


def per_client(expr_for, legend="{c}"):
    """One query per client, so legends and stats always read native-java, grpc-java, grpc-go."""
    return [target(expr_for(c), legend.format(c=c), chr(65 + i)) for i, c in enumerate(ORDER)]


def k6q(pct, metric, c, window="[1m]", extra=""):
    return f'histogram_quantile({pct}, sum(rate({metric}{{{RUN}, client="{c}"{extra}}}{window})))'


def over_range(pct, metric, c, extra=""):
    """A quantile over the whole dashboard time range (native histograms: increase works directly)."""
    return f'histogram_quantile({pct}, sum(increase({metric}{{{RUN}, client="{c}"{extra}}}[$__range])))'


# --- At a glance -------------------------------------------------------------------------------
panels.append(row("At a glance: selected load runs over the time range", y)); y += 1
glance = ("Computed over the whole time range for the runs picked in the \"load run\" dropdown, "
          "so the panel keeps its numbers after a run ends. ")
panels.append(stat("Peak throughput", per_client(lambda c: f'max_over_time(sum(rate({REQS}{{{RUN}, client="{c}"}}[30s]))[$__range:10s])'),
                   0, y, 6, 4, unit="suffix: req/s", colors=CLIENT_COLORS, decimals=0,
                   desc=glance + "Highest requests/s k6 sustained (30-second windows) against each client."))
panels.append(stat("GemFire call p50", per_client(lambda c: over_range(0.5, GF, c)), 6, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2,
                   desc=glance + "Median time inside the GemFire call (gRPC RPC or Region API call), as each client reported it."))
panels.append(stat("GemFire call p99", per_client(lambda c: over_range(0.99, GF, c)), 12, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2, desc=glance + "99th percentile of the GemFire call."))
panels.append(stat(
    "Demo checks",
    [target(f'sum(demo_checks_total{{client="{c}", result="{r}"}})', f"{c} {m}", f"{m2}{i}")
     for i, c in enumerate(ORDER) for r, m, m2 in (("pass", "✓", "P"), ("fail", "✗", "F"))],
    18, y, 6, 4, decimals=0,
    desc="Assertions from the narrated demo (runs at client startup and on POST /demo): ✓ passed, ✗ failed. Any failure turns red. Not tied to load runs.",
    orientation="vertical", text={"titleSize": 13, "valueSize": 30},
    overrides=[{"matcher": {"id": "byRegexp", "options": ".*✗"},
                "properties": [{"id": "thresholds", "value": {"mode": "absolute", "steps": [
                    {"color": "green", "value": None}, {"color": "red", "value": 1}]}},
                    {"id": "color", "value": {"mode": "thresholds"}}]}]))
y += 4

# --- k6 ----------------------------------------------------------------------------------------
panels.append(row("Load test (k6): end-to-end vs. GemFire call", y)); y += 1
dashed = [{"matcher": {"id": "byRegexp", "options": ".* GemFire call"},
           "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 6]}}]}]
panels.append(timeseries("Requests/s", per_client(lambda c: f'sum(rate({REQS}{{{RUN}, client="{c}"}}[30s]))'),
                         0, y, 6, 8, unit="reqps", colors=CLIENT_COLORS,
                         desc="Requests k6 completed per second against each client."))
for i, (pct, label) in enumerate([(0.5, "p50"), (0.99, "p99")]):
    p = timeseries(f"{label}: end-to-end vs. GemFire call",
                   per_client(lambda c: k6q(pct, E2E, c), "{c} end-to-end")
                   + [dict(t, refId=t["refId"] + "2") for t in per_client(lambda c: k6q(pct, GF, c), "{c} GemFire call")],
                   6 + i * 6, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                   desc="Solid = end-to-end (k6), dashed = GemFire call (in the client). The gap between a client's two lines is HTTP + app overhead: "
                        "the JDK HttpServer in the Java clients, net/http in grpc-go. Compare clients on the GemFire call.")
    p["fieldConfig"]["overrides"] += dashed
    panels.append(p)
panels.append(timeseries(
    "k6 VUs and failed requests/s",
    per_client(lambda c: f'sum(k6_vus{{{RUN}, client="{c}"}})', "{c} VUs")
    + [dict(t, refId=t["refId"] + "2") for t in per_client(
        lambda c: f'sum(rate({REQS}{{{RUN}, client="{c}", expected_response="false"}}[30s]))', "{c} failed/s")],
    18, y, 6, 8, unit="short", colors=CLIENT_COLORS,
    desc="VUs climb when a client can't keep up with an arrival-rate script."))
y += 8

# --- Latency -----------------------------------------------------------------------------------
panels.append(row("GemFire call latency: native vs gRPC (op: $op)", y)); y += 1
for i, (pct, label) in enumerate([(0.5, "p50"), (0.99, "p99")]):
    panels.append(timeseries(f"GemFire call {label} (op: $op)",
                             per_client(lambda c: k6q(pct, GF, c, extra=f", {OP}")),
                             i * 8, y, 8, 8, unit="s", colors=CLIENT_COLORS,
                             desc="Time inside the GemFire call, per client. Narrow it to some operations with the `op` dropdown."))
panels.append(timeseries(
    "Errors/s by client and code",
    [target("sum by (client, code) (rate(demo_errors_total[1m])) or sum by (client) (rate(demo_op_latency_seconds_count[1m])) * 0", "{{client}} {{code}}")],
    16, y, 8, 8, unit="ops", colors=CLIENT_COLORS,
    desc="Failed GemFire calls, from the clients' own counters. gRPC: status code. Native: exception class. Flat zero = no errors."))
y += 8

# Per-op comparison table: quantiles over the whole time range for the selected runs.
tbl_targets = [target(over_range(pct, GF, c).replace("sum(", "sum by (op) (", 1), "", ref, True, "table")
               for ref, (pct, c) in zip("ABCDEF", [(0.5, c) for c in ORDER] + [(0.99, c) for c in ORDER])]
JAVA_RATIO, GO_RATIO = "grpc-java vs native", "grpc-go vs native"
panels.append({
    "type": "table", "id": nid(), "title": "Per-operation GemFire call latency (selected runs, whole time range)",
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
                            GO_RATIO: 5, "Value #D": 6, "Value #E": 7, "Value #F": 8},
            "renameByName": {"op": "Operation",
                             "Value #A": "native p50", "Value #B": "grpc-java p50", "Value #C": "grpc-go p50",
                             "Value #D": "native p99", "Value #E": "grpc-java p99", "Value #F": "grpc-go p99"}}},
        {"id": "sortBy", "options": {"sort": [{"field": JAVA_RATIO, "desc": True}]}},
    ],
    "fieldConfig": {
        "defaults": {"unit": "s", "decimals": 2, "custom": {"align": "auto"},
                     "mappings": [NO_DATA]},
        "overrides": [
            {"matcher": {"id": "byRegexp", "options": ".* vs native$"},
             "properties": [{"id": "unit", "value": "none"},
                            {"id": "mappings", "value": ratio_words()},
                            {"id": "custom.cellOptions", "value": {"type": "color-text"}}]},
            {"matcher": {"id": "byName", "options": "Operation"},
             "properties": [{"id": "unit", "value": "string"}]},
        ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
})
y += 15

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
    f' / sum by (client) (rate({REQS}{{{RUN}}}[1m]))')
panels.append(timeseries("Client CPU per request", [target(per_op, "{{client}}")],
                         12, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                         desc="Client container CPU-seconds per request k6 completed (selected runs). Lower is cheaper. Empty between runs."))
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
    "time": {"from": "now-30m", "to": "now"},
    "refresh": "10s",
    "templating": {"list": [{
        "name": "run", "label": "load run", "type": "query", "datasource": DS,
        "query": {"query": "label_values(k6_http_reqs_total, run)", "refId": "run"},
        "definition": "label_values(k6_http_reqs_total, run)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 2,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "op", "label": "op", "type": "query", "datasource": DS,
        "query": {"query": f"label_values({GF}, op)", "refId": "op"},
        "definition": f"label_values({GF}, op)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 1,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
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
