#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/error-handling.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 10-error-handling/ (Python 3 standard library only). Grafana picks up the new JSON within
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


VIAS = ["failfast", "retry", "waitforready"]
VIA_COLORS = {"failfast": "#3987e5", "retry": "#d95926", "waitforready": "#199e70"}
RUN = 'client="grpc-go", run=~"$run"'
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the GemFire call alone
REQS = "k6_http_reqs_total"
CALLS = "demo_calls_total"                 # every gRPC call, once, by channel and final status code
ATTEMPTS = "demo_call_attempts_total"      # every attempt: a retried call counts each one
OPS = "demo_op_latency_seconds"            # one /crud call's GemFire work, failures (and waits) included
CH = 'channel=~"failfast|retry|waitforready"'   # the three channels to server1 (not the demo's unreachable/*)
ORDERS = 'exported_instance="/orders"'


def per_via(expr_for, legend="{v}"):
    return [target(expr_for(v), legend.format(v=v), chr(65 + i)) for i, v in enumerate(VIAS)]


def bargauge(title, targets, x, y, w, h, desc="", unit="percentunit", maximum=1, colors=None, decimals=0,
             names="left"):
    """Horizontal bars, one per series."""
    return {
        "type": "bargauge", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "targets": [dict(t, instant=True, range=False) for t in targets],
        "fieldConfig": {"defaults": {"unit": unit, "min": 0, "max": maximum, "decimals": decimals,
                                     "color": {"mode": "fixed", "fixedColor": "text"},
                                     "thresholds": {"mode": "absolute", "steps": [{"color": "text", "value": None}]},
                                     "mappings": [NO_DATA]},
                        "overrides": color_overrides(colors or {})},
        "options": {"orientation": "horizontal", "displayMode": "basic", "showUnfilled": True,
                    "valueMode": "color", "namePlacement": names, "sizing": "auto",
                    "text": {"titleSize": 14, "valueSize": 22}, "minVizHeight": 16, "maxVizHeight": 28,
                    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
    }


panels, y = [], 0
panels.append(text(
    "**10 · Error handling.** The grpc-go client reaches server1 over three channels that handle a failure "
    "differently: **failfast** (the default: no connection, fail at once), **retry** (a retry policy: up to 5 "
    "attempts with backoff) and **waitforready** (wait for a connection, up to the 30 s deadline). Fill the top row "
    "with `make select-load-test EX=10 SCRIPT=errors/retries`: 20 req/s per channel for 2 minutes, with server1 "
    "stopped at 40 s and started at 55 s (annotated). The demo at startup fills the status code and low-memory rows.",
    0, y, 24, 3))
y += 3

# --- Retries vs no retries --------------------------------------------------------------------------
panels.append(row("Retries vs no retries: server1 goes away (errors/retries)", y)); y += 1
panels.append(bargauge("Failed requests per channel (k6)", per_via(lambda v:
                       f'(sum(increase({REQS}{{{RUN}, via="{v}", expected_response="false"}}[$__range])) or vector(0))'
                       f' / sum(increase({REQS}{{{RUN}, via="{v}"}}[$__range]))'),
                       0, y, 8, 6, decimals=1, colors=VIA_COLORS,
                       desc="Share of k6 requests that failed over the time range, for the runs in the \"load run\" "
                            "dropdown. failfast fails for as long as server1 can't be reached, retry only saves calls "
                            "that started in the last few seconds of the outage, waitforready fails none (its calls "
                            "wait instead)."))
panels.append(timeseries("Failed calls/s by channel and code",
                         [target(f'sum by (channel, code) (rate({CALLS}{{{CH}, code!="OK"}}[30s])) > 0', "{{channel}} {{code}}")],
                         8, y, 8, 12, unit="reqps", colors=VIA_COLORS,
                         desc="From the client's interceptor, once per call. UNAVAILABLE on failfast and retry while "
                              "server1 can't be reached; DEADLINE_EXCEEDED on waitforready only if a wait outlasts the "
                              "30 s deadline. The demo's catalog adds a few failfast errors at startup."))
panels.append(timeseries("How long a call took, p99 by channel",
                         [target(f'histogram_quantile(0.99, sum by (le, channel) (rate({OPS}_bucket{{{CH}}}[30s])))', "{{channel}}")],
                         16, y, 8, 12, unit="s", colors=VIA_COLORS,
                         desc="Each /crud call's GemFire work, failed calls included. Here the latency *is* the behavior: "
                              "failfast stays fast and fails, retry takes up to ~8 s (5 attempts with backoff) and still "
                              "fails, waitforready waits until server1 is back. Estimated from histogram buckets "
                              "(2.5 s apart above 10 s), so read it to the nearest few seconds."))
y += 6
panels.append(timeseries("Attempts per call",
                         [target(f'sum by (channel) (rate({ATTEMPTS}{{{CH}}}[30s])) / sum by (channel) (rate({CALLS}{{{CH}}}[30s]))', "{{channel}}")],
                         0, y, 4, 6, unit="short", decimals=1, colors=VIA_COLORS,
                         desc="demo_call_attempts_total ÷ demo_calls_total. 1 normally; up to 5 on retry during the outage."))
panels.append(timeseries("Members up",
                         [target('sum by (node) (up{job="gemfire"})', "{{node}}")],
                         4, y, 4, 6, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="Prometheus's scrape of each member: 1 = up. Shows when server1 is stopped."))
y += 6

# --- Status codes -----------------------------------------------------------------------------------
panels.append(row("Status codes: what failed, and what to do about it", y)); y += 1
panels.append(bargauge("gRPC calls by status code (time range)",
                       [target(f'sum by (code) (increase({CALLS}[$__range])) > 0', "{{code}}")],
                       0, y, 10, 9, unit="short", maximum=None, names="top",
                       desc="Every gRPC call the client made in the time range, by final status code: the demo's "
                            "catalog at startup, plus the load tests."))
panels.append(text(
    "| Code | Here, when | What to do |\n|---|---|---|\n"
    "| `INVALID_ARGUMENT` | no key, no value, empty or unparseable OQL | fix the request |\n"
    "| `FAILED_PRECONDITION` | the region doesn't exist | fix the request (or create the region) |\n"
    "| `UNIMPLEMENTED` | an UNDEFINED query column; a method the server doesn't have | give up, report it |\n"
    "| `INTERNAL` | an empty region name; anything unexpected | give up, report it |\n"
    "| `RESOURCE_EXHAUSTED` | low memory (*running low on memory*) | back off, retry |\n"
    "| `RESOURCE_EXHAUSTED` | over the 4 MB message limit (*exceeds maximum size*) | split the request |\n"
    "| `DEADLINE_EXCEEDED` | the client's deadline, or the server's query limit | check the deadline, shrink the work |\n"
    "| `CANCELLED` | the client cancelled the call | nothing |\n"
    "| `UNAVAILABLE` | no connection to the server | retry (with backoff), or wait for ready |\n",
    10, y, 14, 9))
y += 9

# --- Low memory -------------------------------------------------------------------------------------
panels.append(row("Low memory: writes refused above the critical heap threshold", y)); y += 1
panels.append(timeseries("Tenured heap used vs the critical threshold",
                         [target("sum by (node) (gemfire_tenuredHeapUsed{node=~\"server.*\"})", "{{node}}"),
                          target("max(gemfire_criticalThreshold{node=~\"server.*\"})", "critical threshold", "B")],
                         0, y, 12, 8, unit="bytes", colors=dict(NODE_COLORS, **{"critical threshold": "#e5484d"}),
                         desc="ResourceManagerStats. The demo fills the ballast region until a server crosses its "
                              "critical threshold (--critical-heap-percentage of the heap), then clears it. GemFire "
                              "samples every 5 s here, so a short spike can show as a single point."))
panels.append(timeseries("Writes refused for low memory, and critical events",
                         [target(f'sum by (channel) (increase({CALLS}{{code="RESOURCE_EXHAUSTED"}}[1m])) > 0', "refused · {{channel}}"),
                          target("sum by (node) (increase(gemfire_heapCriticalEvents[1m])) > 0", "critical event · {{node}}", "B")],
                         12, y, 12, 8, unit="short", decimals=0,
                         desc="RESOURCE_EXHAUSTED calls (the demo's 5 MB Put counts here too: same code, different cause) "
                              "and each member's heapCriticalEvents."))
y += 8

# --- Performance (collapsed) ----------------------------------------------------------------------
panels.append(row("Performance (k6): not the point of this example", y)); y += 1
panels.append(stat("GemFire call p50 (successful calls)", per_via(lambda v: f'histogram_quantile(0.5, sum(increase({GF}{{{RUN}, via="{v}"}}[$__range])))'),
                   0, y, 6, 8, unit="s", decimals=3, colors=VIA_COLORS))
panels.append(stat("GemFire call p99 (successful calls)", per_via(lambda v: f'histogram_quantile(0.99, sum(increase({GF}{{{RUN}, via="{v}"}}[$__range])))'),
                   6, y, 6, 8, unit="s", decimals=3, colors=VIA_COLORS))
panels.append(timeseries("GemFire call p50 by channel",
                         per_via(lambda v: f'histogram_quantile(0.5, sum(rate({GF}{{{RUN}, via="{v}"}}[30s])))'),
                         12, y, 12, 8, unit="s", colors=VIA_COLORS,
                         desc="With server1 up the three channels are the same: retry and wait-for-ready cost nothing until something fails."))
y += 8

# --- Resource cost (collapsed) ----------------------------------------------------------------------
panels.append(row("GemFire servers and resource cost", y)); y += 1
panels.append(timeseries("Region gets/s per server (/orders)",
                         [target(f"sum by (node) (rate(gemfire_getsCompleted{{{ORDERS}}}[1m]))", "{{node}}")],
                         0, y, 8, 8, unit="ops", colors=NODE_COLORS))
cpu = "sum by (service) (rate(container_cpu_usage_seconds_total{service=~\"%s\"}[1m]))"
panels.append(timeseries("CPU: GemFire members (cores)", [target(cpu % "locator|server1|server2", "{{service}}")],
                         8, y, 8, 8, unit="short", colors=SERVICE_COLORS, decimals=2))
panels.append(timeseries("Heap used per member",
                         [target('sum by (node) (gemfire_usedMemory{exported_instance="vmHeapMemoryStats"})', "{{node}}")],
                         16, y, 8, 8, unit="bytes", colors=NODE_COLORS))
y += 8

# Rows that start collapsed: move each one's panels inside it.
COLLAPSED = {"Performance (k6): not the point of this example", "GemFire servers and resource cost"}
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
    "uid": "error-handling",
    "title": "10 · Error handling: status codes, deadlines, retries, low memory",
    "tags": ["gemfire", "grpc", "10-error-handling"],
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
    }]},
    "annotations": {"list": [{
        "name": "Load tests", "enable": True, "iconColor": "rgba(150, 150, 150, 0.6)",
        "datasource": {"type": "grafana", "uid": "-- Grafana --"},
        "target": {"type": "tags", "tags": ["load-test"], "limit": 100, "matchAny": False},
    }]},
    "panels": panels,
}

out = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else \
    pathlib.Path(__file__).parent / "dashboards" / "error-handling.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
