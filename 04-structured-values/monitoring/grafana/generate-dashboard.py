#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/structured-values.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 04-structured-values/ (Python 3 standard library only). Grafana picks up the new JSON within
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

GO = CLIENT_COLORS["grpc-go"]
# k6 metrics carry client, run and op (the query's name) tags, so the latency and throughput
# panels follow the "load run" and "query" dropdowns and match the terminal table.
SEL = 'client="grpc-go", run=~"$run", op=~"$op"'
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the Get, the Put, or the whole Query stream
E2E = "k6_http_req_duration_seconds"      # the whole HTTP request as k6 saw it
REQS = "k6_http_reqs_total"
ROWS = "k6_query_rows_total"
# The client's own metrics (no run tag): every call, the demo's and curl's included.
OPS_M = "demo_op_latency_seconds"
RPC_M = "demo_rpc_duration_seconds"
MSG_M = "demo_stream_messages_total"
CPS = 'exported_instance="cachePerfStats"'   # CachePerfStats: the whole cache, per member
PEOPLE = 'exported_instance="/people"'      # PartitionedRegionStats of the load-test region

panels, y = [], 0
panels.append(text(
    "**04 · Structured values.** The grpc-go client stores `Person` protobuf messages packed in "
    "`google.protobuf.Any` and queries their fields with OQL, which works because gfsh-init deployed "
    "their descriptor JAR. Load comes from `make select-load-test EX=04 SCRIPT=people/steady`: "
    "`get` and `put` (one RPC each) plus three field queries, `by-city`, `age-range` and `nickname` "
    "(the **op** dropdown).", 0, y, 24, 3))
y += 3

# --- At a glance -------------------------------------------------------------------------------
panels.append(row("At a glance: selected load runs over the time range", y)); y += 1
glance = ("Computed over the whole time range for the runs picked in the \"load run\" dropdown, "
          "so the panel keeps its numbers after a run ends. ")
panels.append(stat("Peak req/s", [target(f"max_over_time(sum(rate({REQS}{{{SEL}}}[30s]))[$__range:10s])", "grpc-go")],
                   0, y, 5, 4, unit="suffix: req/s", decimals=0, colors=CLIENT_COLORS,
                   desc=glance + "Highest requests/s k6 sustained (30-second windows): Gets, Puts and queries."))
panels.append(stat("Peak rows/s", [target(f"max_over_time(sum(rate({ROWS}{{{SEL}}}[30s]))[$__range:10s])", "grpc-go")],
                   5, y, 5, 4, unit="suffix: rows/s", decimals=0, colors=CLIENT_COLORS,
                   desc=glance + "Highest rows/s the queries returned (30-second windows)."))
panels.append(stat("GemFire call p50", [target(f"histogram_quantile(0.5, sum(increase({GF}{{{SEL}}}[$__range])))", "grpc-go")],
                   10, y, 5, 4, unit="s", decimals=2, colors=CLIENT_COLORS,
                   desc=glance + "Median time of the GemFire call: a Get, a Put, or a whole Query stream."))
panels.append(stat("GemFire call p99", [target(f"histogram_quantile(0.99, sum(increase({GF}{{{SEL}}}[$__range])))", "grpc-go")],
                   15, y, 5, 4, unit="s", decimals=2, colors=CLIENT_COLORS, desc=glance + "99th percentile."))
calls = f'sum(increase({OPS_M}_count[$__range]))'
failed = f'sum(increase({OPS_M}_count{{code!="OK"}}[$__range]))'
fail_stat = stat("Failed calls", [target(f"({failed} or vector(0)) / {calls}", "grpc-go")], 20, y, 4, 4,
                 unit="percentunit", decimals=2, colors=CLIENT_COLORS,
                 desc="Share of calls that failed, from the client's demo_op_latency_seconds{code!=\"OK\"}. "
                      "It counts every caller (k6, the demo, curl), so it ignores the dropdowns.")
fail_stat["fieldConfig"]["defaults"]["mappings"] = [NO_DATA]
panels.append(fail_stat)
y += 4

# --- Queries by name ----------------------------------------------------------------------------
panels.append(row("Calls by op (op: $op)", y)); y += 1
panels.append(timeseries("GemFire call p50 and p99, by op",
                         [target(f"histogram_quantile(0.5, sum by (op) (rate({GF}{{{SEL}}}[1m])))", "{{op}} p50", "A"),
                          target(f"histogram_quantile(0.99, sum by (op) (rate({GF}{{{SEL}}}[1m])))", "{{op}} p99", "B")],
                         0, y, 12, 8, unit="s",
                         desc="The GemFire call as the client timed it (X-GemFire-Micros): one Get or Put RPC, "
                              "or a whole Query stream. The field queries scan every Person, unpacking each one."))
panels.append(timeseries("Rows/s, by query", [target(f"sum by (op) (rate({ROWS}{{{SEL}}}[1m]))", "{{op}}")],
                         12, y, 12, 8, unit="short",
                         desc="Rows the field queries returned per second. by-city returns about 1,000 rows, "
                              "age-range one (a COUNT), nickname about 2,000."))
y += 8
# Per-query table over the whole time range.
def by_op(expr, ref):
    return target(expr, "", ref, instant=True, fmt="table")
tbl = {
    "type": "table", "id": nid(), "title": "Per op, over the time range", "datasource": DS,
    "gridPos": {"h": 7, "w": 24, "x": 0, "y": y},
    "description": "Calls, average rows per call (queries only), and the GemFire call's p50/p99 for the selected runs.",
    "targets": [
        by_op(f"sum by (op) (increase({REQS}{{{SEL}}}[$__range]))", "A"),
        by_op(f"sum by (op) (increase({ROWS}{{{SEL}}}[$__range])) / sum by (op) (increase({REQS}{{{SEL}}}[$__range]))", "B"),
        by_op(f"histogram_quantile(0.5, sum by (op) (increase({GF}{{{SEL}}}[$__range])))", "C"),
        by_op(f"histogram_quantile(0.99, sum by (op) (increase({GF}{{{SEL}}}[$__range])))", "D"),
    ],
    "transformations": [
        {"id": "joinByField", "options": {"byField": "op", "mode": "outer"}},
        {"id": "organize", "options": {"excludeByName": {"Time": True, "Time 1": True, "Time 2": True, "Time 3": True, "Time 4": True},
                                       "renameByName": {"op": "op", "Value #A": "calls", "Value #B": "rows per call",
                                                        "Value #C": "p50", "Value #D": "p99"}}},
    ],
    "fieldConfig": {"defaults": {"custom": {"align": "auto"}, "mappings": [NO_DATA]},
                    "overrides": [
                        {"matcher": {"id": "byName", "options": "calls"}, "properties": [{"id": "decimals", "value": 0}]},
                        {"matcher": {"id": "byName", "options": "rows per call"}, "properties": [{"id": "decimals", "value": 1}]},
                        {"matcher": {"id": "byRegexp", "options": "^p(50|99)$"}, "properties": [{"id": "unit", "value": "s"}, {"id": "decimals", "value": 2}]},
                    ]},
    "options": {"showHeader": True, "sortBy": [{"displayName": "op", "desc": False}]},
}
panels.append(tbl)
y += 7

# --- Load test (k6) -----------------------------------------------------------------------------
panels.append(row("Load test (k6): end-to-end vs. GemFire call", y)); y += 1
panels.append(timeseries("Requests/s", [target(f"sum(rate({REQS}{{{SEL}}}[1m]))", "grpc-go")],
                         0, y, 8, 8, unit="reqps", colors=CLIENT_COLORS))
panels.append(timeseries("End-to-end vs. GemFire call (p50)",
                         [target(f"histogram_quantile(0.5, sum(rate({E2E}{{{SEL}}}[1m])))", "end-to-end", "A"),
                          target(f"histogram_quantile(0.5, sum(rate({GF}{{{SEL}}}[1m])))", "GemFire call", "B")],
                         8, y, 8, 8, unit="s",
                         desc="The gap is the HTTP request around the call: k6 → client and back, protojson encoding."))
panels.append(timeseries("Failed requests/s",
                         [target(f'sum(rate({REQS}{{{SEL}, expected_response="false"}}[1m])) or vector(0)', "grpc-go")],
                         16, y, 8, 8, unit="reqps", colors=CLIENT_COLORS))
y += 8

# --- gRPC Query streams (interceptor) -----------------------------------------------------------
panels.append(row("gRPC calls (interceptors): every call, the demo's included", y)); y += 1
q = 'method="Query"'
panels.append(timeseries("gRPC calls/s by method and status code",
                         [target(f"sum by (method, code) (rate({RPC_M}_count[1m])) > 0", "{{method}} {{code}}")],
                         0, y, 8, 8, unit="reqps",
                         desc="From the client's interceptors. Query errors (UNIMPLEMENTED for UNDEFINED, "
                              "FAILED_PRECONDITION for an unregistered type, INTERNAL for an index out of "
                              "bounds) come from the demo and from curl."))
panels.append(timeseries("Query call duration p50 / p99",
                         [target(f"histogram_quantile(0.5, sum by (le) (rate({RPC_M}_bucket{{{q}}}[1m])))", "p50", "A"),
                          target(f"histogram_quantile(0.99, sum by (le) (rate({RPC_M}_bucket{{{q}}}[1m])))", "p99", "B")],
                         8, y, 8, 8, unit="s"))
panels.append(timeseries("QueryResponse messages per call",
                         [target(f'sum(rate({MSG_M}{{{q}, direction="received"}}[1m])) / sum(rate({RPC_M}_count{{{q}}}[1m]))', "messages per call")],
                         16, y, 8, 8, unit="short", decimals=2,
                         desc="About rows ÷ query-batch-size, and never less than 1: an empty result is still one message."))
y += 8

# --- GemFire servers ------------------------------------------------------------------------------
panels.append(row("GemFire servers: what the cluster sees", y)); y += 1
panels.append(timeseries("Region gets and puts/s per server (/people)",
                         [target(f"sum by (node) (rate(gemfire_getsCompleted{{{PEOPLE}}}[1m]))", "get {{node}}", "A"),
                          target(f"sum by (node) (rate(gemfire_putsCompleted{{{PEOPLE}}}[1m]))", "put {{node}}", "B")],
                         0, y, 6, 8, unit="ops",
                         desc="PartitionedRegionStats on /people. The Any bytes are stored as they are, so a Get "
                              "or Put costs the same as for any other value of that size."))
panels.append(timeseries("Query executions/s per server",
                         [target(f"sum by (node) (rate(gemfire_queryExecutions{{{CPS}}}[1m]))", "{{node}}")],
                         6, y, 6, 8, unit="ops", colors=NODE_COLORS,
                         desc="CachePerfStats.queryExecutions. On a partitioned region every query runs on both "
                              "servers (each on its own buckets), so both count it, not only server1."))
panels.append(timeseries("Average execution time per server",
                         [target(f"sum by (node) (rate(gemfire_queryExecutionTime{{{CPS}}}[1m])) / 1e9"
                                 f" / sum by (node) (rate(gemfire_queryExecutions{{{CPS}}}[1m]))", "{{node}}")],
                         12, y, 6, 8, unit="s", colors=NODE_COLORS,
                         desc="CachePerfStats.queryExecutionTime (nanoseconds) ÷ queryExecutions: the server's "
                              "share of a query, which includes unpacking every Person to read its fields."))
panels.append(timeseries("Partitioned-query messages sent",
                         [target("sum by (node) (rate(gemfire_cache_partitioned_QueryMessagesSent[1m]))", "{{node}}")],
                         18, y, 6, 8, unit="ops", colors=NODE_COLORS,
                         desc="QueryMessages a server sends to the other server, asking it to run the query on "
                              "its own buckets. The gRPC client talks only to server1, so server1 sends them."))
y += 8
panels.append(timeseries("Heap used per member",
                         [target('sum by (node) (gemfire_usedMemory{exported_instance="vmHeapMemoryStats"})', "{{node}}")],
                         0, y, 12, 8, unit="bytes", colors=NODE_COLORS))
panels.append(timeseries("gRPC connections per server",
                         [target("sum by (node) (gemfire_grpc_server_connections)", "{{node}}")],
                         12, y, 12, 8, unit="short", decimals=0, colors=NODE_COLORS))
y += 8

# --- Resource cost ----------------------------------------------------------------------------
panels.append(row("Resource cost (cAdvisor)", y)); y += 1
cpu = "sum by (service) (rate(container_cpu_usage_seconds_total{service=~\"%s\"}[1m]))"
panels.append(timeseries("CPU: client (cores)", [target(cpu % ".*-client-.*", "{{service}}")],
                         0, y, 8, 8, unit="short", colors=SERVICE_COLORS, decimals=2))
panels.append(timeseries("CPU: GemFire members (cores)", [target(cpu % "locator|server1|server2", "{{service}}")],
                         8, y, 8, 8, unit="short", colors=SERVICE_COLORS, decimals=2))
panels.append(timeseries("Memory working set",
                         [target('sum by (service) (container_memory_working_set_bytes{service=~".*-client-.*|locator|server1|server2"})', "{{service}}")],
                         16, y, 8, 8, unit="bytes", colors=SERVICE_COLORS))
y += 8

# Rows that start collapsed: move each one's panels inside it.
COLLAPSED = set()
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
    "uid": "structured-values",
    "title": "04 · Structured values (Any + descriptors)",
    "tags": ["gemfire", "grpc", "04-structured-values"],
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
    pathlib.Path(__file__).parent / "dashboards" / "structured-values.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
