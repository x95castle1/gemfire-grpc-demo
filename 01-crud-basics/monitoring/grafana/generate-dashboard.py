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


def ratio_words(kind="time"):
    """Value mappings that show a ratio (A ÷ B) as words, the same wording as the load-test tables.

    Under 2×: "22% faster" / "60% slower". From 2× up: "3.1× slower", "12× faster". kind="cpu"
    says less/more, kind="rate" (throughput, where higher is better) says more/fewer.

    Grafana mappings can't interpolate the value, so there is one range per whole percent under
    2×, per 0.1× from 2× to 10×, and per 1× from 10× to 100×. Display only: sorting still uses
    the underlying ratio."""
    GREEN, RED = "#3fa66b", "#e0524c"
    up, down = {"time": ("slower", "faster"), "cpu": ("more", "less"), "rate": ("more", "fewer")}[kind]
    up_color, down_color = (GREEN, RED) if kind == "rate" else (RED, GREEN)

    def rng(lo, hi, text, color):
        return {"type": "range", "options": {"from": lo, "to": hi, "result": {"text": text, "color": color}}}

    m = [NO_DATA, rng(0.995, 1.005, "same", "text")]
    # Under 2×, as percentages: A ÷ B = 1 ± n/100.
    m += [rng(1 - (n + 0.5) / 100, 1 - (n - 0.5) / 100, f"{n}% {down}", down_color) for n in range(1, 50)]
    m += [rng(1 + (n - 0.5) / 100, 1 + (n + 0.5) / 100, f"{n}% {up}", up_color) for n in range(1, 100)]
    # From 2×, as multiples: x = A ÷ B (up) or B ÷ A (down).
    steps = [(round(x / 10, 1), 0.05, f"{x / 10:.1f}") for x in range(20, 100)] + [(x, 0.5, str(x)) for x in range(10, 101)]
    for x, half, label in steps:
        lo = 1.995 if x == 2.0 else x - half   # 2.0× picks up where "99% slower" ends
        m.append(rng(lo, x + half, f"{label}× {up}", up_color))
        m.append(rng(1 / (x + half), 0.505 if x == 2.0 else 1 / (x - half), f"{label}× {down}", down_color))
    m.append(rng(100.5, 1e12, f"over 100× {up}", up_color))
    m.append(rng(1e-12, 1 / 100.5, f"over 100× {down}", down_color))
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
# The clients' own metrics: they carry region and code labels, but no run tag.
OPS_M = "demo_op_latency_seconds"     # one /crud operation, failures included (code != "OK")
RPC_M = "demo_rpc_duration_seconds"   # every gRPC call, from the gRPC clients' interceptors
REGION = 'region=~"$region"'


CPU_BY_CLIENT = ('sum by (client) (label_replace(rate(container_cpu_usage_seconds_total{service=~".*-client-.*"}[1m]),'
                 ' "client", "$1-$2", "service", "(.*)-client-(.*)"))')


def by_client_range(pct, metric):
    return f'histogram_quantile({pct}, sum by (client) (increase({metric}{{{RUN}}}[$__range])))'


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
def failed_share(c):
    calls = f'sum(increase({OPS_M}_count{{client="{c}", {REGION}}}[$__range]))'
    failed = f'sum(increase({OPS_M}_count{{client="{c}", {REGION}, code=~".+", code!="OK"}}[$__range]))'
    return f"({failed} or vector(0)) / {calls}"
fail_stat = stat("Failed calls", per_client(failed_share), 18, y, 6, 4, unit="percentunit", decimals=2,
                 colors=CLIENT_COLORS,
                 desc="Share of /crud operations whose GemFire call failed, over the time range, from the "
                      "clients' own demo_op_latency_seconds{code!=\"OK\"}. It counts every caller (k6 and curl alike), "
                      "so it ignores the load run dropdown; the region dropdown applies.")
fail_stat["fieldConfig"]["defaults"]["mappings"] = [NO_DATA]
panels.append(fail_stat)
y += 4

# --- Run summary table (same columns as the terminal table) --------------------------------------
panels.append(row("Run summary: same columns as the terminal table", y)); y += 1
order_q = " or ".join(f'label_replace(vector({i}), "client", "{c}", "", "")' for i, c in enumerate(ORDER))
reqs_total = f"sum by (client) (increase({REQS}{{{RUN}}}[$__range]))"
failed_total = f'sum by (client) (increase({REQS}{{{RUN}, expected_response="false"}}[$__range]))'
# CPU-seconds only while the client was under load, so idle time between runs doesn't count.
cpu_active = (f"sum by (client) (sum_over_time(({CPU_BY_CLIENT} and on (client) "
              f"(sum by (client) (rate({REQS}{{{RUN}}}[1m])) > 0))[$__range:15s])) * 15")
summary_targets = [
    target(order_q, "", "ORDER", True, "table"),
    target(reqs_total, "", "REQS", True, "table"),
    target(f"max_over_time(sum by (client) (rate({REQS}{{{RUN}}}[30s]))[$__range:10s])", "", "PEAK", True, "table"),
    target(f"({failed_total} or {reqs_total} * 0) / {reqs_total}", "", "FAILED", True, "table"),
    target(by_client_range(0.5, E2E), "", "E2E50", True, "table"),
    target(by_client_range(0.95, E2E), "", "E2E95", True, "table"),
    target(by_client_range(0.99, E2E), "", "E2E99", True, "table"),
    target(by_client_range(0.5, GF), "", "GF50", True, "table"),
    target(by_client_range(0.99, GF), "", "GF99", True, "table"),
    target(f"{cpu_active} / {reqs_total}", "", "CPU", True, "table"),
]
names = {"Value #REQS": "requests", "Value #PEAK": "peak req/s", "Value #FAILED": "failed",
         "Value #E2E50": "E2E p50", "Value #E2E95": "E2E p95", "Value #E2E99": "E2E p99",
         "Value #GF50": "GemFire p50", "Value #GF99": "GemFire p99", "Value #CPU": "CPU per request"}
panels.append({
    "type": "table", "id": nid(), "title": "", "datasource": DS,
    "description": "One row per client, for the selected runs over the time range. CPU per request counts the client container's CPU only while it was under load.",
    "gridPos": {"h": 5, "w": 24, "x": 0, "y": y}, "targets": summary_targets,
    "transformations": [
        {"id": "merge", "options": {}},
        {"id": "sortBy", "options": {"sort": [{"field": "Value #ORDER"}]}},
        {"id": "organize", "options": {
            "excludeByName": {"Time": True, "Value #ORDER": True},
            "indexByName": {"client": 0, **{k: i + 1 for i, k in enumerate(names)}},
            "renameByName": {"client": "client", **names}}},
    ],
    "fieldConfig": {
        "defaults": {"unit": "s", "decimals": 2, "custom": {"align": "auto"}, "mappings": [NO_DATA]},
        "overrides": [
            {"matcher": {"id": "byName", "options": "client"},
             "properties": [{"id": "unit", "value": "string"},
                            {"id": "custom.cellOptions", "value": {"type": "color-text"}},
                            {"id": "mappings", "value": [{"type": "value", "options": {
                                c: {"color": col, "index": i} for i, (c, col) in enumerate(CLIENT_COLORS.items())}}]}]},
            {"matcher": {"id": "byName", "options": "requests"},
             "properties": [{"id": "unit", "value": "none"}, {"id": "decimals", "value": 0}]},
            {"matcher": {"id": "byName", "options": "peak req/s"},
             "properties": [{"id": "unit", "value": "suffix: req/s"}, {"id": "decimals", "value": 0}]},
            {"matcher": {"id": "byName", "options": "failed"},
             "properties": [{"id": "unit", "value": "percentunit"}, {"id": "decimals", "value": 1}]},
            {"matcher": {"id": "byName", "options": "CPU per request"},
             "properties": [{"id": "unit", "value": "s"}, {"id": "decimals", "value": 2}]},
        ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
})
y += 5

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
                             i * 6, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                             desc="Time inside the GemFire call, per client. Narrow it to some operations with the `op` dropdown."))
panels.append(timeseries(
    "Failed calls/s by client and code",
    [target(f'sum by (client, code) (rate({OPS_M}_count{{{REGION}, code=~".+", code!="OK"}}[1m])) '
            f'or sum by (client) (rate({OPS_M}_count{{{REGION}}}[1m])) * 0', "{{client}} {{code}}")],
    12, y, 6, 8, unit="ops", colors=CLIENT_COLORS,
    desc="/crud operations whose GemFire call failed, from the clients' own demo_op_latency_seconds{code!=\"OK\"}. "
         "gRPC: status code. Native: exception class. Flat zero = no failures."))
panels.append(timeseries(
    "Latency of failed calls (p99)",
    [target(f'histogram_quantile(0.99, sum by (le, client, code) (rate({OPS_M}_bucket{{{REGION}, code=~".+", code!="OK"}}[1m])))',
            "{{client}} {{code}}")],
    18, y, 6, 8, unit="s", colors=CLIENT_COLORS,
    desc="How long failed calls took before they failed. A timeout (DEADLINE_EXCEEDED) is a slow failure; "
         "most other errors fail fast. Empty while nothing fails."))
y += 8

# Per-op comparison table: quantiles over the whole time range for the selected runs.
tbl_targets = [target(over_range(pct, GF, c).replace("sum(", "sum by (op) (", 1), "", ref, True, "table")
               for ref, (pct, c) in zip("ABCDEF", [(0.5, c) for c in ORDER] + [(0.99, c) for c in ORDER])]
JAVA_RATIO, GO_RATIO = "grpc-java vs native", "grpc-go vs native"
panels.append({
    "type": "table", "id": nid(), "title": "Per-operation GemFire call latency (selected runs, whole time range)",
    "description": "java = grpc-java, go = grpc-go. The \"vs native\" columns compare p50s: \"3.5× slower\" means that gRPC client's median call took 3.5 times as long as native's; under 2× it's a percentage (\"60% slower\"). Native GetAndPut, "
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

# --- gRPC calls (interceptor) -------------------------------------------------------------------
# What the gRPC library actually did: every RPC on each gRPC client's channel, streams and the
# demo's calls included. The native client has no interceptors, so it isn't in this row.
GRPC_CLIENTS = ["grpc-java", "grpc-go"]


def per_grpc_client(expr_for, legend="{c}"):
    return [target(expr_for(c), legend.format(c=c), chr(65 + i)) for i, c in enumerate(GRPC_CLIENTS)]


panels.append(row("gRPC calls (interceptor): every RPC, streams included", y)); y += 1
panels.append(timeseries("RPCs/s",
                         per_grpc_client(lambda c: f'sum(rate({RPC_M}_count{{client="{c}", {REGION}}}[1m]))'),
                         0, y, 8, 8, unit="ops", colors=CLIENT_COLORS,
                         desc="gRPC calls per second per client, from demo_rpc_duration_seconds (an interceptor on each channel)."))
panels.append(timeseries("RPC p99", per_grpc_client(
    lambda c: f'histogram_quantile(0.99, sum by (le) (rate({RPC_M}_bucket{{client="{c}", {REGION}}}[1m])))'),
                         8, y, 8, 8, unit="s", colors=CLIENT_COLORS,
                         desc="99th percentile of a gRPC call, start to close. For a stream that's the whole stream."))
panels.append(timeseries("RPCs/s by status code",
                         [target(f'sum by (client, code) (rate({RPC_M}_count{{{REGION}}}[1m]))', "{{client}} {{code}}")],
                         16, y, 8, 8, unit="ops", colors=CLIENT_COLORS,
                         desc="Every gRPC call by its final status code, OK included."))
y += 8
# Per-method table over the whole time range.
rpc_q = lambda fn: f"sum by (method) (increase({RPC_M}_{fn}{{client=\"%s\", {REGION}}}[$__range]))"
rpc_targets = []
for i, c in enumerate(GRPC_CLIENTS):
    rpc_targets.append(target(rpc_q("count") % c, "", f"N{i}", True, "table"))
    rpc_targets.append(target(f'histogram_quantile(0.5, sum by (le, method) (increase({RPC_M}_bucket{{client="{c}", {REGION}}}[$__range])))', "", f"P{i}", True, "table"))
    rpc_targets.append(target(f'histogram_quantile(0.99, sum by (le, method) (increase({RPC_M}_bucket{{client="{c}", {REGION}}}[$__range])))', "", f"Q{i}", True, "table"))
    rpc_targets.append(target(f'sum by (method) (increase({RPC_M}_count{{client="{c}", {REGION}, code=~".+", code!="OK"}}[$__range])) '
                              f'or sum by (method) (increase({RPC_M}_count{{client="{c}", {REGION}}}[$__range])) * 0', "", f"F{i}", True, "table"))
rpc_names = {}
for i, c in enumerate(GRPC_CLIENTS):
    rpc_names.update({f"Value #N{i}": f"{c} calls", f"Value #P{i}": f"{c} p50",
                      f"Value #Q{i}": f"{c} p99", f"Value #F{i}": f"{c} failed"})
panels.append({
    "type": "table", "id": nid(), "title": "gRPC calls by method (whole time range)", "datasource": DS,
    "description": "Every gRPC method each client called, from its interceptor: count, latency and failures. "
                   "PutStream is the preload; the demo's calls count too.",
    "gridPos": {"h": 15, "w": 24, "x": 0, "y": y}, "targets": rpc_targets,
    "transformations": [
        {"id": "merge", "options": {}},
        {"id": "organize", "options": {
            "excludeByName": {"Time": True},
            "indexByName": {"method": 0, **{k: i + 1 for i, k in enumerate(rpc_names)}},
            "renameByName": {"method": "Method", **rpc_names}}},
        {"id": "sortBy", "options": {"sort": [{"field": "grpc-java calls", "desc": True}]}},
    ],
    "fieldConfig": {
        "defaults": {"unit": "s", "decimals": 2, "custom": {"align": "auto"}, "mappings": [NO_DATA]},
        "overrides": [
            {"matcher": {"id": "byRegexp", "options": ".* (calls|failed)$"},
             "properties": [{"id": "unit", "value": "none"}, {"id": "decimals", "value": 0}]},
            {"matcher": {"id": "byName", "options": "Method"},
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

# Rows that start collapsed: move each one's panels inside it.
COLLAPSED = {"gRPC calls (interceptor): every RPC, streams included"}
flat, panels, current = panels, [], None
for p in flat:
    if p["type"] == "row":
        current = p if p["title"] in COLLAPSED else None
        if current:
            p["collapsed"] = True
        panels.append(p)
    elif current is not None:
        current["panels"].append(p)
    else:
        panels.append(p)

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
    }, {
        "name": "region", "label": "region", "type": "query", "datasource": DS,
        "description": "Filters the panels that read the clients' own metrics (failed calls, the gRPC calls row). k6 doesn't know regions.",
        "query": {"query": f'label_values({{__name__=~"{OPS_M}_count|{RPC_M}_count"}}, region)', "refId": "region"},
        "definition": f'label_values({{__name__=~"{OPS_M}_count|{RPC_M}_count"}}, region)',
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
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
