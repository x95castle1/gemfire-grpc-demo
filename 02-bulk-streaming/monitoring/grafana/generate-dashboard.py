#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/bulk-native-vs-grpc.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 02-bulk-streaming/ (Python 3 standard library only). Grafana picks up the new JSON within
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
    "### 02 · Bulk streaming: native putAll/getAll/removeAll vs. GemFire for gRPC streams\n"
    "One **call** moves many entries: one PutStream / GetStream / RemoveStream / KeySetStream (grpc-java, straight to "
    "`server1:50051`), or the Region bulk calls doing the same job (native-java, single-hop through the locator). "
    "`mode=unary` does the same work one Put/Get/Remove per entry, the baseline that shows what streaming buys. "
    "Load comes from k6 (`make select-load-test`); the **load run** dropdown picks the runs. "
    "*End-to-end* = the HTTP request as k6 saw it. *GemFire call* = time inside the stream / Region calls.",
    0, y, 24, 4))
y += 4

# Load panels read k6's metrics, which carry client, run, op and mode tags (batch too, in the
# chunk-size sweep), so they follow the "load run" dropdown and match the terminal tables.
ORDER = ["native-java", "grpc-java"]
# Every k6 query follows the "load run" and "mode" dropdowns (mode: stream, unary×16, unary×1).
RUN_ONLY = 'run=~"$run"'
RUN = 'run=~"$run", mode=~"$mode"'
OP = 'op=~"$op"'
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the GemFire call(s) alone
E2E = "k6_http_req_duration_seconds"      # the whole HTTP request as k6 saw it
REQS = "k6_http_reqs_total"
ENTRIES = "k6_bulk_entries_total"         # X-Entries: entries each call moved
# The clients' own metrics: they carry region and code labels, but no run tag.
OPS_M = "demo_op_latency_seconds"
RPC_M = "demo_rpc_duration_seconds"
MSG_M = "demo_stream_messages_total"
REGION = 'region=~"$region"'

CPU_BY_CLIENT = ('sum by (client) (label_replace(rate(container_cpu_usage_seconds_total{service=~".*-client-.*"}[1m]),'
                 ' "client", "$1-$2", "service", "(.*)-client-(.*)"))')
CLIENT_COLOR_MAP = [{"type": "value", "options": {c: {"color": col, "index": i}
                                                  for i, (c, col) in enumerate(CLIENT_COLORS.items())}}]


def by_client_range(pct, metric, by="client"):
    return f'histogram_quantile({pct}, sum by ({by}) (increase({metric}{{{RUN}}}[$__range])))'


def per_client(expr_for, legend="{c}"):
    """One query per client, so legends and stats always read native-java, then grpc-java."""
    return [target(expr_for(c), legend.format(c=c), chr(65 + i)) for i, c in enumerate(ORDER)]


def k6q(pct, metric, c, window="[1m]", extra=""):
    return f'histogram_quantile({pct}, sum(rate({metric}{{{RUN}, client="{c}"{extra}}}{window})))'


def over_range(pct, metric, c, extra=""):
    return f'histogram_quantile({pct}, sum(increase({metric}{{{RUN}, client="{c}"{extra}}}[$__range])))'


def peak_eps(by="client", extra=""):
    """Highest entries/s sustained over a 30-second window in the time range."""
    return f'max_over_time(sum by ({by}) (rate({ENTRIES}{{{RUN}{extra}}}[30s]))[$__range:10s])'


def table(title, targets, x, y, w, h, transformations, overrides, desc=""):
    return {
        "type": "table", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y}, "targets": targets,
        "transformations": transformations,
        "fieldConfig": {"defaults": {"unit": "s", "decimals": 2, "custom": {"align": "auto"}, "mappings": [NO_DATA]},
                        "overrides": overrides},
        "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
    }


def client_col(name="client"):
    return {"matcher": {"id": "byName", "options": name},
            "properties": [{"id": "unit", "value": "string"},
                           {"id": "custom.cellOptions", "value": {"type": "color-text"}},
                           {"id": "mappings", "value": CLIENT_COLOR_MAP}]}


def unit_col(name, unit, decimals=None):
    props = [{"id": "unit", "value": unit}]
    if decimals is not None:
        props.append({"id": "decimals", "value": decimals})
    return {"matcher": {"id": "byName", "options": name}, "properties": props}


def words_col(pattern, kind):
    return {"matcher": {"id": "byRegexp", "options": pattern},
            "properties": [{"id": "unit", "value": "none"}, {"id": "mappings", "value": ratio_words(kind)},
                           {"id": "custom.cellOptions", "value": {"type": "color-text"}}]}


EPS = "suffix: entries/s"

# --- At a glance -------------------------------------------------------------------------------
panels.append(row("At a glance: selected load runs over the time range", y)); y += 1
glance = ("Computed over the whole time range for the runs picked in the \"load run\" dropdown, "
          "so the panel keeps its numbers after a run ends. ")
panels.append(stat("Peak entries/s", per_client(lambda c: peak_eps(extra=f', client="{c}"').replace("sum by (client)", "sum")),
                   0, y, 6, 4, unit=EPS, colors=CLIENT_COLORS, decimals=0,
                   desc=glance + "Highest entries/s k6 sustained (30-second windows) through each client, any mode."))
panels.append(stat("Bulk call p50", per_client(lambda c: over_range(0.5, GF, c)), 6, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2,
                   desc=glance + "Median time inside one bulk call (all ops and modes in the selected runs), as each client reported it."))
panels.append(stat("Bulk call p99", per_client(lambda c: over_range(0.99, GF, c)), 12, y, 6, 4, unit="s",
                   colors=CLIENT_COLORS, decimals=2, desc=glance + "99th percentile of one bulk call."))


def failed_share(c):
    calls = f'sum(increase({OPS_M}_count{{client="{c}", {REGION}}}[$__range]))'
    failed = f'sum(increase({OPS_M}_count{{client="{c}", {REGION}, code=~".+", code!="OK"}}[$__range]))'
    return f"({failed} or vector(0)) / {calls}"


fail_stat = stat("Failed calls", per_client(failed_share), 18, y, 6, 4, unit="percentunit", decimals=2,
                 colors=CLIENT_COLORS,
                 desc="Share of /bulk calls that failed, over the time range, from the clients' own "
                      "demo_op_latency_seconds{code!=\"OK\"}. It ignores the load run dropdown; the region dropdown applies.")
fail_stat["fieldConfig"]["defaults"]["mappings"] = [NO_DATA]
panels.append(fail_stat)
y += 4

# --- Run summary table ---------------------------------------------------------------------------
panels.append(row("Run summary: same columns as the terminal table", y)); y += 1
order_q = " or ".join(f'label_replace(vector({i}), "client", "{c}", "", "")' for i, c in enumerate(ORDER))
reqs_total = f"sum by (client) (increase({REQS}{{{RUN}}}[$__range]))"
entries_total = f"sum by (client) (increase({ENTRIES}{{{RUN}}}[$__range]))"
failed_total = f'sum by (client) (increase({REQS}{{{RUN}, expected_response="false"}}[$__range]))'
# CPU-seconds only while the client was under load, so idle time between runs doesn't count.
cpu_active = (f"sum by (client) (sum_over_time(({CPU_BY_CLIENT} and on (client) "
              f"(sum by (client) (rate({REQS}{{{RUN}}}[1m])) > 0))[$__range:15s])) * 15")
summary_names = {"Value #CALLS": "calls", "Value #ENTRIES": "entries", "Value #PEAK": "peak entries/s",
                 "Value #FAILED": "failed", "Value #E2E50": "E2E p50", "Value #E2E99": "E2E p99",
                 "Value #GF50": "GemFire p50", "Value #GF99": "GemFire p99", "Value #CPU": "CPU per 1k entries"}
panels.append(table("", [
    target(order_q, "", "ORDER", True, "table"),
    target(reqs_total, "", "CALLS", True, "table"),
    target(entries_total, "", "ENTRIES", True, "table"),
    target(peak_eps(), "", "PEAK", True, "table"),
    target(f"({failed_total} or {reqs_total} * 0) / {reqs_total}", "", "FAILED", True, "table"),
    target(by_client_range(0.5, E2E), "", "E2E50", True, "table"),
    target(by_client_range(0.99, E2E), "", "E2E99", True, "table"),
    target(by_client_range(0.5, GF), "", "GF50", True, "table"),
    target(by_client_range(0.99, GF), "", "GF99", True, "table"),
    target(f"{cpu_active} / {entries_total} * 1000", "", "CPU", True, "table"),
], 0, y, 24, 5, [
    {"id": "merge", "options": {}},
    {"id": "sortBy", "options": {"sort": [{"field": "Value #ORDER"}]}},
    {"id": "organize", "options": {
        "excludeByName": {"Time": True, "Value #ORDER": True},
        "indexByName": {"client": 0, **{k: i + 1 for i, k in enumerate(summary_names)}},
        "renameByName": {"client": "client", **summary_names}}},
], [client_col(), unit_col("calls", "none", 0), unit_col("entries", "short", 1), unit_col("peak entries/s", EPS, 0),
    unit_col("failed", "percentunit", 1), unit_col("CPU per 1k entries", "s", 2)],
    desc="One row per client, for the selected runs over the time range (all ops and modes). "
         "CPU per 1k entries counts the client container's CPU only while it was under load."))
y += 5

# --- Streaming vs one call per entry ------------------------------------------------------------
panels.append(row("Streaming vs one call per entry (bulk/stream-vs-unary)", y)); y += 1
MODE_STYLES = [{"matcher": {"id": "byRegexp", "options": ".* unary×16$"},
                "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 6]}}]},
               {"matcher": {"id": "byRegexp", "options": ".* unary×1$"},
                "properties": [{"id": "custom.lineStyle", "value": {"fill": "dot", "dash": [2, 6]}}]}]
SVU = ', script="bulk/stream-vs-unary"'   # this row reads that script's runs only
p = timeseries("Entries/s by mode",
               [target(f'sum by (client, mode) (rate({ENTRIES}{{{RUN}{SVU}}}[30s]))', "{{client}} {{mode}}")],
               0, y, 10, 9, unit=EPS, colors=CLIENT_COLORS,
               desc="Solid = stream (PutStream/GetStream/RemoveStream, or putAll/getAll/removeAll). Dashed = 16 unary "
                    "calls in flight. Dotted = one unary call at a time. The same put → get → remove cycle every time.")
p["fieldConfig"]["overrides"] += MODE_STYLES
panels.append(p)
peak_mode = peak_eps("client, mode", SVU)
stream_peak = peak_eps("client", SVU + ', mode="stream"')
mode_names = {"Value #PEAK": "peak entries/s", "Value #VS": "vs stream", "Value #P50": "GemFire call p50",
              "Value #P99": "GemFire call p99"}
panels.append(table("Each client against its own stream", [
    target(peak_mode, "", "PEAK", True, "table"),
    target(f"{peak_mode} / on (client) group_left () {stream_peak}", "", "VS", True, "table"),
    target(by_client_range(0.5, GF, "client, mode").replace(f"{{{RUN}}}", f"{{{RUN}{SVU}}}"), "", "P50", True, "table"),
    target(by_client_range(0.99, GF, "client, mode").replace(f"{{{RUN}}}", f"{{{RUN}{SVU}}}"), "", "P99", True, "table"),
], 10, y, 14, 9, [
    {"id": "merge", "options": {}},
    {"id": "organize", "options": {
        "excludeByName": {"Time": True},
        "indexByName": {"client": 0, "mode": 1, **{k: i + 2 for i, k in enumerate(mode_names)}},
        "renameByName": mode_names}},
    {"id": "sortBy", "options": {"sort": [{"field": "client"}]}},
], [client_col(), unit_col("mode", "string"), unit_col("peak entries/s", EPS, 0), words_col("^vs stream$", "rate")],
    desc="Peak entries/s (30-second windows) per client and mode, and how each mode compares with the same "
         "client's stream: \"13× fewer\" = a thirteenth of the stream's throughput. Call latencies are for one "
         "call of the cycle (2,000 entries by default)."))
y += 9
# Chunk size sweep (bulk/batch-sweep).
peak_batch = peak_eps("client, batch", ', batch!=""')
batch_names = {"Value #PEAK": "peak entries/s", "Value #P50": "GemFire call p50", "Value #P99": "GemFire call p99"}
p = timeseries("Entries/s by chunk size (bulk/batch-sweep)",
               [target(f'sum by (client, batch) (rate({ENTRIES}{{{RUN}, batch!=""}}[30s]))', "{{client}} batch {{batch}}")],
               0, y, 10, 8, unit=EPS, colors=CLIENT_COLORS,
               desc="Entries per stream message (gRPC) = entries per putAll/getAll/removeAll call (native). Each chunk size runs on its own.")
panels.append(p)
panels.append(table("Throughput by chunk size", [
    target(peak_batch, "", "PEAK", True, "table"),
    target(by_client_range(0.5, GF, "client, batch").replace(f"{{{RUN}}}", f'{{{RUN}, batch!=""}}'), "", "P50", True, "table"),
    target(by_client_range(0.99, GF, "client, batch").replace(f"{{{RUN}}}", f'{{{RUN}, batch!=""}}'), "", "P99", True, "table"),
], 10, y, 14, 8, [
    {"id": "merge", "options": {}},
    {"id": "convertFieldType", "options": {"conversions": [{"targetField": "batch", "destinationType": "number"}]}},
    {"id": "organize", "options": {
        "excludeByName": {"Time": True},
        "indexByName": {"client": 0, "batch": 1, **{k: i + 2 for i, k in enumerate(batch_names)}},
        "renameByName": {"batch": "chunk size", **batch_names}}},
    {"id": "sortBy", "options": {"sort": [{"field": "chunk size"}]}},
], [client_col(), unit_col("chunk size", "none", 0), unit_col("peak entries/s", EPS, 0)],
    desc="Peak entries/s per client and chunk size. Empty until bulk/batch-sweep has run."))
y += 8

# --- k6 ----------------------------------------------------------------------------------------
panels.append(row("Load test (k6): end-to-end vs. GemFire call", y)); y += 1
dashed = [{"matcher": {"id": "byRegexp", "options": ".* GemFire call"},
           "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 6]}}]}]
panels.append(timeseries("Entries/s", per_client(lambda c: f'sum(rate({ENTRIES}{{{RUN}, client="{c}"}}[30s]))'),
                         0, y, 6, 8, unit=EPS, colors=CLIENT_COLORS,
                         desc="Entries moved per second through each client (GetStream / Get: entries found)."))
for i, (pct, label) in enumerate([(0.5, "p50"), (0.99, "p99")]):
    p = timeseries(f"Bulk call {label}: end-to-end vs. GemFire call",
                   per_client(lambda c: k6q(pct, E2E, c), "{c} end-to-end")
                   + [dict(t, refId=t["refId"] + "2") for t in per_client(lambda c: k6q(pct, GF, c), "{c} GemFire call")],
                   6 + i * 6, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                   desc="Solid = end-to-end (k6), dashed = GemFire call (in the client). For bulk calls the two are "
                        "close: HTTP overhead is tiny next to moving thousands of entries.")
    p["fieldConfig"]["overrides"] += dashed
    panels.append(p)
panels.append(timeseries(
    "k6 VUs and failed calls/s",
    per_client(lambda c: f'sum(k6_vus{{{RUN_ONLY}, client="{c}"}})', "{c} VUs")
    + [dict(t, refId=t["refId"] + "2") for t in per_client(
        lambda c: f'sum(rate({REQS}{{{RUN}, client="{c}", expected_response="false"}}[30s]))', "{c} failed/s")],
    18, y, 6, 8, unit="short", colors=CLIENT_COLORS))
y += 8

# --- Per operation -----------------------------------------------------------------------------
panels.append(row("Bulk calls by operation (op: $op)", y)); y += 1
for i, (pct, label) in enumerate([(0.5, "p50"), (0.99, "p99")]):
    panels.append(timeseries(f"GemFire call {label} by op (op: $op)",
                             [target(f'histogram_quantile({pct}, sum by (client, op) (rate({GF}{{{RUN}, {OP}}}[1m])))', "{{client}} {{op}}")],
                             i * 8, y, 8, 8, unit="s", colors=CLIENT_COLORS,
                             desc="One bulk call per op. Unary-mode ops are named after the unary RPC (Put, Get, Remove); "
                                  "the call covers every entry."))
panels.append(timeseries("Entries/s by op (op: $op)",
                         [target(f'sum by (client, op) (rate({ENTRIES}{{{RUN}, {OP}}}[30s]))', "{{client}} {{op}}")],
                         16, y, 8, 8, unit=EPS, colors=CLIENT_COLORS))
y += 8
op_targets = [target(over_range(pct, GF, c).replace("sum(", "sum by (op) (", 1), "", ref, True, "table")
              for ref, (pct, c) in zip("ABCD", [(0.5, "native-java"), (0.5, "grpc-java"), (0.99, "native-java"), (0.99, "grpc-java")])]
RATIO = "grpc-java vs native"
panels.append(table("Per-operation GemFire call latency (selected runs, whole time range)", op_targets, 0, y, 24, 9, [
    {"id": "merge", "options": {}},
    {"id": "calculateField", "options": {"mode": "binary", "alias": RATIO,
                                         "binary": {"left": "Value #B", "operator": "/", "right": "Value #A"},
                                         "replaceFields": False}},
    {"id": "organize", "options": {
        "excludeByName": {"Time": True},
        "indexByName": {"op": 0, "Value #A": 1, "Value #B": 2, RATIO: 3, "Value #C": 4, "Value #D": 5},
        "renameByName": {"op": "Operation", "Value #A": "native p50", "Value #B": "grpc-java p50",
                         "Value #C": "native p99", "Value #D": "grpc-java p99"}}},
    {"id": "sortBy", "options": {"sort": [{"field": RATIO, "desc": True}]}},
], [words_col(f"^{RATIO}$", "time"), unit_col("Operation", "string")],
    desc="Compares p50s: \"5.0× slower\" = grpc-java's median call took 5 times as long as native's. GetStream is the "
         "outlier: server1 fetches every key it doesn't host from server2 one at a time (see the GemFire servers row)."))
y += 9

# --- gRPC streams (interceptor) --------------------------------------------------------------------
panels.append(row("gRPC streams (interceptor): what grpc-java put on the wire", y)); y += 1
G = 'client="grpc-java"'
panels.append(timeseries("Call duration p50 by method",
                         [target(f'histogram_quantile(0.5, sum by (le, method) (rate({RPC_M}_bucket{{{G}, {REGION}}}[1m])))', "{{method}}")],
                         0, y, 6, 8, unit="s",
                         desc="demo_rpc_duration_seconds from the interceptor: a stream from start to the server's close."))
panels.append(timeseries("Messages/s by method",
                         [target(f'sum by (method, direction) (rate({MSG_M}{{{G}}}[1m]))', "{{method}} {{direction}}")],
                         6, y, 6, 8, unit="short",
                         desc="Every message the interceptor saw. A PutStream sends one StreamConfig, then one message per "
                              "chunk, and receives one. A GetStream receives entries back while it is still sending."))
panels.append(timeseries("Calls/s by status code",
                         [target(f'sum by (method, code) (rate({RPC_M}_count{{{G}, {REGION}}}[1m]))', "{{method}} {{code}}")],
                         12, y, 6, 8, unit="ops", desc="Every gRPC call by its final status code, OK included."))
panels.append(timeseries("Messages per call",
                         [target(f'sum by (method) (rate({MSG_M}{{{G}, direction="sent"}}[1m])) / sum by (method) (rate({RPC_M}_count{{{G}}}[1m]))', "{{method}} sent"),
                          target(f'sum by (method) (rate({MSG_M}{{{G}, direction="received"}}[1m])) / sum by (method) (rate({RPC_M}_count{{{G}}}[1m]))', "{{method}} received", "B")],
                         18, y, 6, 8, unit="short", decimals=1,
                         desc="Average messages per gRPC call: 1 for unary calls; for a stream, the chunk count + 1 sent."))
y += 8
mq = lambda fn, extra="": f"sum by (method) (increase({fn}{{{G}{extra}}}[$__range]))"
method_names = {"Value #N": "calls", "Value #P": "p50", "Value #Q": "p99", "Value #F": "failed",
                "Value #S": "messages sent", "Value #R": "messages received"}
panels.append(table("gRPC calls by method (whole time range)", [
    target(mq(f"{RPC_M}_count", f", {REGION}"), "", "N", True, "table"),
    target(f'histogram_quantile(0.5, sum by (le, method) (increase({RPC_M}_bucket{{{G}, {REGION}}}[$__range])))', "", "P", True, "table"),
    target(f'histogram_quantile(0.99, sum by (le, method) (increase({RPC_M}_bucket{{{G}, {REGION}}}[$__range])))', "", "Q", True, "table"),
    target(f'{mq(f"{RPC_M}_count", f", {REGION}, code=~\".+\", code!=\"OK\"")} or {mq(f"{RPC_M}_count", f", {REGION}")} * 0', "", "F", True, "table"),
    target(mq(MSG_M, ', direction="sent"'), "", "S", True, "table"),
    target(mq(MSG_M, ', direction="received"'), "", "R", True, "table"),
], 0, y, 24, 10, [
    {"id": "merge", "options": {}},
    {"id": "organize", "options": {
        "excludeByName": {"Time": True},
        "indexByName": {"method": 0, **{k: i + 1 for i, k in enumerate(method_names)}},
        "renameByName": {"method": "Method", **method_names}}},
    {"id": "sortBy", "options": {"sort": [{"field": "calls", "desc": True}]}},
], [{"matcher": {"id": "byRegexp", "options": "^(calls|failed|messages .*)$"},
     "properties": [{"id": "unit", "value": "short"}, {"id": "decimals", "value": 0}]},
    unit_col("Method", "string")],
    desc="Every gRPC method grpc-java called, from its interceptor. The demo's calls count too; messages ignore the region dropdown."))
y += 10

# --- GemFire servers ---------------------------------------------------------------------------
panels.append(row("GemFire servers: what the cluster sees", y)); y += 1
panels.append(timeseries(
    "Messages to the other server",
    [target("sum by (node) (rate(gemfire_GetMessagesSent[1m]))", "{{node}} get (one per key)", "A"),
     target("sum by (node) (rate(gemfire_PutAllPRMessagesSent[1m]))", "{{node}} putAll (per bucket)", "B"),
     target("sum by (node) (rate(gemfire_RemoveAllPRMessagesSent[1m]))", "{{node}} removeAll (per bucket)", "C")],
    0, y, 8, 8, unit="ops", colors=NODE_COLORS,
    desc="Peer-to-peer messages a server sends to the server that owns a bucket. The gRPC client talks only to "
         "server1, so server1 forwards: a putAll/removeAll as one message per bucket, but a getAll as one "
         "GetMessage per key it doesn't host. That's why GetStream is the slowest stream. Native single-hop "
         "sends each server only its own keys, so it adds almost nothing here."))
panels.append(timeseries(
    "Bulk operations per server",
    [target('sum by (node) (rate(gemfire_putalls{exported_instance="cachePerfStats"}[1m]))', "{{node}} putAll (all)", "A"),
     target('sum by (node) (rate(gemfire_removeAlls{exported_instance="cachePerfStats"}[1m]))', "{{node}} removeAll (all)", "B"),
     target("sum by (node) (rate(gemfire_putAllRequests[1m]))", "{{node}} putAll (native)", "C"),
     target("sum by (node) (rate(gemfire_getAllRequests[1m]))", "{{node}} getAll (native)", "D")],
    8, y, 8, 8, unit="ops", colors=NODE_COLORS,
    desc="CachePerfStats putalls / removeAlls count every bulk operation the server ran, the gRPC streams' too "
         "(each stream becomes putAll/removeAll operations of at most bulk-batch-size entries). CacheServerStats "
         "*Requests count only native-protocol requests on port 40404."))
panels.append(timeseries(
    "Entries per region",
    [target('sum by (exported_instance) (gemfire_dataStoreEntryCount{exported_instance=~"/(bulk|demo)-.*"})', "{{exported_instance}}")],
    16, y, 8, 8, unit="short",
    desc="PartitionedRegionStats.dataStoreEntryCount, summed over both servers. Each client has its own bulk region."))
y += 8
panels.append(timeseries(
    "Heap used per member",
    [target('sum by (node) (gemfire_usedMemory{exported_instance="vmHeapMemoryStats"})', "{{node}}")],
    0, y, 12, 8, unit="bytes", colors=NODE_COLORS))
panels.append(timeseries(
    "Client connections per server",
    [target("sum by (node) (gemfire_currentClientConnections)", "native {{node}}", "A"),
     target("sum by (node) (gemfire_grpc_server_connections)", "gRPC {{node}}", "B")],
    12, y, 12, 8, unit="short", decimals=0,
    desc="native: CacheServerStats.currentClientConnections (pooled sockets). gRPC: one multiplexed HTTP/2 connection."))
y += 8

# --- Resource cost ----------------------------------------------------------------------------
panels.append(row("Resource cost (cAdvisor)", y)); y += 1
cpu = "sum by (service) (rate(container_cpu_usage_seconds_total{service=~\"%s\"}[1m]))"
panels.append(timeseries("CPU: clients (cores)", [target(cpu % ".*-client-.*", "{{service}}")],
                         0, y, 6, 8, unit="short", colors=SERVICE_COLORS, decimals=2))
panels.append(timeseries("CPU: GemFire members (cores)", [target(cpu % "locator|server1|server2", "{{service}}")],
                         6, y, 6, 8, unit="short", colors=SERVICE_COLORS, decimals=2,
                         desc="server1 is the gRPC entry point: it decodes protobuf and forwards to server2."))
per_k = f"{CPU_BY_CLIENT} / sum by (client) (rate({ENTRIES}{{{RUN}}}[1m])) * 1000"
panels.append(timeseries("Client CPU per 1k entries", [target(per_k, "{{client}}")],
                         12, y, 6, 8, unit="s", colors=CLIENT_COLORS,
                         desc="Client container CPU-seconds per 1,000 entries moved (selected runs). Lower is cheaper. Empty between runs."))
panels.append(timeseries("Memory working set",
                         [target('sum by (service) (container_memory_working_set_bytes{service=~".*-client-.*|locator|server1|server2"})', "{{service}}")],
                         18, y, 6, 8, unit="bytes", colors=SERVICE_COLORS,
                         desc="Watch the clients during bulk/keyset: keySetOnServer builds the whole key set in one response; "
                              "KeySetStream sends it in pages."))
y += 8

dashboard = {
    "uid": "bulk-native-vs-grpc",
    "title": "02 · Bulk streaming: native vs gRPC",
    "tags": ["gemfire", "grpc", "02-bulk-streaming"],
    "timezone": "browser",
    "schemaVersion": 41,
    "version": 1,
    "editable": True,
    "graphTooltip": 1,
    "time": {"from": "now-30m", "to": "now"},
    "refresh": "10s",
    "templating": {"list": [{
        "name": "run", "label": "load run", "type": "query", "datasource": DS,
        "query": {"query": f"label_values({ENTRIES}, run)", "refId": "run"},
        "definition": f"label_values({ENTRIES}, run)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 2,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "mode", "label": "mode", "type": "query", "datasource": DS,
        "description": "stream = one PutStream/GetStream/RemoveStream/KeySetStream (native: putAll/getAll/removeAll/keySetOnServer). "
                       "unary×N = the same work one Put/Get/Remove per entry, N calls in flight. Applies to every k6 panel.",
        "query": {"query": f"label_values({ENTRIES}, mode)", "refId": "mode"},
        "definition": f"label_values({ENTRIES}, mode)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 1,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "op", "label": "op", "type": "query", "datasource": DS,
        "query": {"query": f'label_values({GF}{{mode=~"$mode"}}, op)', "refId": "op"},
        "definition": f'label_values({GF}{{mode=~"$mode"}}, op)',
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 1,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "region", "label": "region", "type": "query", "datasource": DS,
        "description": "Filters the panels that read the clients' own metrics (failed calls, the gRPC row). k6 doesn't know regions.",
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
    pathlib.Path(__file__).parent / "dashboards" / "bulk-native-vs-grpc.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} panels)")
