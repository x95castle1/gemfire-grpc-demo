#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/oql-query.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 03-oql-query/ (Python 3 standard library only). Grafana picks up the new JSON within
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
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the whole Query stream
E2E = "k6_http_req_duration_seconds"      # the whole HTTP request as k6 saw it
REQS = "k6_http_reqs_total"
ROWS = "k6_query_rows_total"
# The client's own metrics (no run tag): every Query call, the demo's and curl's included.
OPS_M = "demo_op_latency_seconds"
RPC_M = "demo_rpc_duration_seconds"
MSG_M = "demo_stream_messages_total"
CPS = 'exported_instance="cachePerfStats"'   # CachePerfStats: the whole cache, per member

panels, y = [], 0
panels.append(text(
    "**03 · OQL query over gRPC.** The grpc-go client runs OQL with the `Query` RPC: the server "
    "executes it and streams the rows back, up to `gemfire.grpc.query-batch-size` per message. "
    "Load comes from `make select-load-test EX=03 SCRIPT=query/steady`, which mixes five queries "
    "(the **query** dropdown): `scan`, `range`, `count`, `top` and `key`.", 0, y, 24, 3))
y += 3

# --- At a glance -------------------------------------------------------------------------------
panels.append(row("At a glance: selected load runs over the time range", y)); y += 1
glance = ("Computed over the whole time range for the runs picked in the \"load run\" dropdown, "
          "so the panel keeps its numbers after a run ends. ")
panels.append(stat("Peak queries/s", [target(f"max_over_time(sum(rate({REQS}{{{SEL}}}[30s]))[$__range:10s])", "grpc-go")],
                   0, y, 5, 4, unit="suffix: req/s", decimals=0, colors=CLIENT_COLORS,
                   desc=glance + "Highest queries/s k6 sustained (30-second windows)."))
panels.append(stat("Peak rows/s", [target(f"max_over_time(sum(rate({ROWS}{{{SEL}}}[30s]))[$__range:10s])", "grpc-go")],
                   5, y, 5, 4, unit="suffix: rows/s", decimals=0, colors=CLIENT_COLORS,
                   desc=glance + "Highest rows/s the queries returned (30-second windows)."))
panels.append(stat("Query p50", [target(f"histogram_quantile(0.5, sum(increase({GF}{{{SEL}}}[$__range])))", "grpc-go")],
                   10, y, 5, 4, unit="s", decimals=2, colors=CLIENT_COLORS,
                   desc=glance + "Median time of the whole Query call: from sending the request to the last row."))
panels.append(stat("Query p99", [target(f"histogram_quantile(0.99, sum(increase({GF}{{{SEL}}}[$__range])))", "grpc-go")],
                   15, y, 5, 4, unit="s", decimals=2, colors=CLIENT_COLORS, desc=glance + "99th percentile."))
calls = f'sum(increase({OPS_M}_count[$__range]))'
failed = f'sum(increase({OPS_M}_count{{code!="OK"}}[$__range]))'
fail_stat = stat("Failed queries", [target(f"({failed} or vector(0)) / {calls}", "grpc-go")], 20, y, 4, 4,
                 unit="percentunit", decimals=2, colors=CLIENT_COLORS,
                 desc="Share of /query calls that failed, from the client's demo_op_latency_seconds{code!=\"OK\"}. "
                      "It counts every caller (k6, the demo, curl), so it ignores the dropdowns.")
fail_stat["fieldConfig"]["defaults"]["mappings"] = [NO_DATA]
panels.append(fail_stat)
y += 4

# --- Queries by name ----------------------------------------------------------------------------
panels.append(row("Queries by name (query: $op)", y)); y += 1
panels.append(timeseries("Query time p50 and p99, by query",
                         [target(f"histogram_quantile(0.5, sum by (op) (rate({GF}{{{SEL}}}[1m])))", "{{op}} p50", "A"),
                          target(f"histogram_quantile(0.99, sum by (op) (rate({GF}{{{SEL}}}[1m])))", "{{op}} p99", "B")],
                         0, y, 12, 8, unit="s",
                         desc="The whole Query call, as the client timed it (X-GemFire-Micros)."))
panels.append(timeseries("Rows/s, by query", [target(f"sum by (op) (rate({ROWS}{{{SEL}}}[1m]))", "{{op}}")],
                         12, y, 12, 8, unit="short",
                         desc="Rows the queries returned per second. scan and range return about 100 rows each, "
                              "count one row, top 10 rows, key one row."))
y += 8
# Per-query table over the whole time range.
def by_op(expr, ref):
    return target(expr, "", ref, instant=True, fmt="table")
tbl = {
    "type": "table", "id": nid(), "title": "Per query, over the time range", "datasource": DS,
    "gridPos": {"h": 7, "w": 24, "x": 0, "y": y},
    "description": "Queries, average rows per query, and the Query call's p50/p99 for the selected runs.",
    "targets": [
        by_op(f"sum by (op) (increase({REQS}{{{SEL}}}[$__range]))", "A"),
        by_op(f"sum by (op) (increase({ROWS}{{{SEL}}}[$__range])) / sum by (op) (increase({REQS}{{{SEL}}}[$__range]))", "B"),
        by_op(f"histogram_quantile(0.5, sum by (op) (increase({GF}{{{SEL}}}[$__range])))", "C"),
        by_op(f"histogram_quantile(0.99, sum by (op) (increase({GF}{{{SEL}}}[$__range])))", "D"),
    ],
    "transformations": [
        {"id": "joinByField", "options": {"byField": "op", "mode": "outer"}},
        {"id": "organize", "options": {"excludeByName": {"Time": True, "Time 1": True, "Time 2": True, "Time 3": True, "Time 4": True},
                                       "renameByName": {"op": "query", "Value #A": "queries", "Value #B": "rows per query",
                                                        "Value #C": "p50", "Value #D": "p99"}}},
    ],
    "fieldConfig": {"defaults": {"custom": {"align": "auto"}, "mappings": [NO_DATA]},
                    "overrides": [
                        {"matcher": {"id": "byName", "options": "queries"}, "properties": [{"id": "decimals", "value": 0}]},
                        {"matcher": {"id": "byName", "options": "rows per query"}, "properties": [{"id": "decimals", "value": 1}]},
                        {"matcher": {"id": "byRegexp", "options": "^p(50|99)$"}, "properties": [{"id": "unit", "value": "s"}, {"id": "decimals", "value": 2}]},
                    ]},
    "options": {"showHeader": True, "sortBy": [{"displayName": "query", "desc": False}]},
}
panels.append(tbl)
y += 7

# --- Load test (k6) -----------------------------------------------------------------------------
panels.append(row("Load test (k6): end-to-end vs. Query call", y)); y += 1
panels.append(timeseries("Queries/s", [target(f"sum(rate({REQS}{{{SEL}}}[1m]))", "grpc-go")],
                         0, y, 8, 8, unit="reqps", colors=CLIENT_COLORS))
panels.append(timeseries("End-to-end vs. Query call (p50)",
                         [target(f"histogram_quantile(0.5, sum(rate({E2E}{{{SEL}}}[1m])))", "end-to-end", "A"),
                          target(f"histogram_quantile(0.5, sum(rate({GF}{{{SEL}}}[1m])))", "Query call", "B")],
                         8, y, 8, 8, unit="s",
                         desc="The gap is the HTTP request around the query: k6 → client and back, JSON encoding."))
panels.append(timeseries("Failed requests/s",
                         [target(f'sum(rate({REQS}{{{SEL}, expected_response="false"}}[1m])) or vector(0)', "grpc-go")],
                         16, y, 8, 8, unit="reqps", colors=CLIENT_COLORS))
y += 8

# --- gRPC Query streams (interceptor) -----------------------------------------------------------
panels.append(row("gRPC Query streams (interceptor): every call, the demo's included", y)); y += 1
q = 'method="Query"'
panels.append(timeseries("Query calls/s by status code",
                         [target(f"sum by (code) (rate({RPC_M}_count{{{q}}}[1m]))", "{{code}}")],
                         0, y, 8, 8, unit="reqps",
                         desc="From the client's stream interceptor. Errors such as INVALID_ARGUMENT, "
                              "FAILED_PRECONDITION or UNIMPLEMENTED come from the demo and from curl."))
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
panels.append(timeseries("Query executions/s per server",
                         [target(f"sum by (node) (rate(gemfire_queryExecutions{{{CPS}}}[1m]))", "{{node}}")],
                         0, y, 8, 8, unit="ops", colors=NODE_COLORS,
                         desc="CachePerfStats.queryExecutions. On a partitioned region a query runs on every "
                              "server that hosts its buckets, and each server counts its per-bucket executions too, "
                              "so this is far higher than queries/s. Use it for the trend and the server1/server2 "
                              "balance, not as a query count."))
panels.append(timeseries("Average execution time per server",
                         [target(f"sum by (node) (rate(gemfire_queryExecutionTime{{{CPS}}}[1m])) / 1e9"
                                 f" / sum by (node) (rate(gemfire_queryExecutions{{{CPS}}}[1m]))", "{{node}}")],
                         8, y, 8, 8, unit="s", colors=NODE_COLORS,
                         desc="CachePerfStats.queryExecutionTime (nanoseconds) ÷ queryExecutions: the average "
                              "per execution as the server counts them (per bucket on a partitioned region), so "
                              "it's much shorter than the whole Query call."))
panels.append(timeseries("Partitioned-query messages sent",
                         [target("sum by (node) (rate(gemfire_cache_partitioned_QueryMessagesSent[1m]))", "{{node}}")],
                         16, y, 8, 8, unit="ops", colors=NODE_COLORS,
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
    "uid": "oql-query",
    "title": "03 · OQL query over gRPC",
    "tags": ["gemfire", "grpc", "03-oql-query"],
    "timezone": "browser",
    "schemaVersion": 41,
    "version": 1,
    "editable": True,
    "graphTooltip": 1,
    "time": {"from": "now-30m", "to": "now"},
    "refresh": "10s",
    "templating": {"list": [{
        "name": "run", "label": "load run", "type": "query", "datasource": DS,
        "query": {"query": "label_values(k6_query_rows_total, run)", "refId": "run"},
        "definition": "label_values(k6_query_rows_total, run)",
        "multi": True, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 2,
        "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
    }, {
        "name": "op", "label": "query", "type": "query", "datasource": DS,
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
    pathlib.Path(__file__).parent / "dashboards" / "oql-query.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
