#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/auth.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 08-token-auth-rbac/ (Python 3 standard library only). Grafana picks up the new JSON within
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


USERS = ["alice", "bob", "carol", "none"]
USER_COLORS = {"alice": "#3987e5", "bob": "#d95926", "carol": "#9085e9", "none": "#8b8d98"}
CODE_COLORS = {"OK": "#199e70", "PERMISSION_DENIED": "#d95926", "UNAUTHENTICATED": "#e5484d"}
REGION_COLORS = {"orders": "#3987e5", "customers": "#d55181"}
RUN = 'client="grpc-go", run=~"$run"'
GF = "k6_gemfire_call_duration_seconds"
REQS = "k6_http_reqs_total"
CALLS = "demo_calls_total"     # every gRPC call, by the user whose token it carried, region and code


def bargauge(title, targets, x, y, w, h, colors, desc=""):
    return {
        "type": "bargauge", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "targets": [dict(t, instant=True, range=False) for t in targets],
        "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1, "decimals": 0,
                                     "color": {"mode": "fixed", "fixedColor": "text"},
                                     "thresholds": {"mode": "absolute", "steps": [{"color": "text", "value": None}]}},
                        "overrides": color_overrides(colors)},
        "options": {"orientation": "horizontal", "displayMode": "basic", "showUnfilled": True,
                    "valueMode": "color", "namePlacement": "left", "sizing": "auto",
                    "text": {"titleSize": 13, "valueSize": 18}, "minVizHeight": 14, "maxVizHeight": 24,
                    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
    }


def code_share(user):
    """Each status code's share of a user's gRPC calls over the time range (the demo's included)."""
    return (f'sum by (code) (increase({CALLS}{{user="{user}"}}[$__range]))'
            f' / scalar(sum(increase({CALLS}{{user="{user}"}}[$__range])))')


panels, y = [], 0
panels.append(text(
    "**08 · Tokens and permissions.** Every gRPC call carries `authorization: Bearer <JWT>`. The cluster's "
    "SecurityManager (`security/`) checks the token (**UNAUTHENTICATED** if it's missing, expired or forged) and "
    "whether one of its `perms` implies what the call needs (**PERMISSION_DENIED** if not). The grpc-go client acts as "
    "alice, bob, carol or nobody. Fill the panels with `make select-load-test EX=08 SCRIPT=auth/mixed`.", 0, y, 24, 3))
y += 3

# --- Who got in ---------------------------------------------------------------------------------
panels.append(row("Who got in: each user's calls by result", y)); y += 1
perms = {"alice": "DATA:READ, DATA:WRITE (everything)", "bob": "DATA:READ (read-only)",
         "carol": "DATA:READ:orders, DATA:WRITE:orders (orders only)", "none": "no token"}
for i, u in enumerate(USERS):
    panels.append(bargauge(f"{u}: {perms[u]}", [target(code_share(u), "{{code}}")], 6 * i, y, 6, 5, CODE_COLORS,
                           desc=f"Share of {u}'s gRPC calls by status code over the time range, from the client's "
                                f"interceptor (demo_calls_total). Includes the demo's calls."))
y += 5
panels.append(timeseries("Calls/s by user and result",
                         [target(f'sum by (user, code) (rate({CALLS}[30s])) > 0', "{{user}} {{code}}")],
                         0, y, 16, 8, unit="reqps",
                         desc="Every gRPC call, by the user whose token it carried and its status code."))
panels.append(text(
    "| user | token `perms` | gets |\n|---|---|---|\n"
    "| alice | `DATA:READ`, `DATA:WRITE` | everything |\n"
    "| bob | `DATA:READ` | reads; writes → PERMISSION_DENIED |\n"
    "| carol | `DATA:READ:orders`, `DATA:WRITE:orders` | orders; customers → PERMISSION_DENIED |\n"
    "| none | (no token) | UNAUTHENTICATED |\n\n"
    "Get/Query need `DATA:READ`, Put/Remove `DATA:WRITE`, GetAndPut and the other GetAnd* both. "
    "The server checks them per region and key, e.g. `DATA:WRITE:orders:grpc-go:42`.", 16, y, 8, 8))
y += 8

# --- By region -----------------------------------------------------------------------------------
panels.append(row("By region", y)); y += 1
for i, r in enumerate(["orders", "customers"]):
    panels.append(timeseries(f"{r}: calls/s by result",
                             [target(f'sum by (code) (rate({CALLS}{{region="{r}"}}[30s])) > 0', "{{code}}")],
                             12 * i, y, 12, 7, unit="reqps", colors=CODE_COLORS, stack=True,
                             desc=("carol's calls here succeed; bob's writes are denied." if r == "orders" else
                                   "carol is denied everything here; bob's writes are denied.")))
y += 7

# --- Performance (collapsed) ----------------------------------------------------------------------
panels.append(row("Performance (k6): not the point of this example", y)); y += 1
panels.append(timeseries("GemFire call p50 by user (allowed calls)",
                         [target(f'histogram_quantile(0.5, sum by (user) (rate({GF}{{{RUN}}}[30s])))', "{{user}}")],
                         0, y, 12, 8, unit="s", colors=USER_COLORS,
                         desc="Checking a token and its permissions happens on every call; it doesn't show here."))
panels.append(timeseries("Requests/s by HTTP status",
                         [target(f'sum by (status) (rate({REQS}{{{RUN}}}[30s]))', "{{status}}")],
                         12, y, 12, 8, unit="reqps",
                         desc="The client answers 401 for UNAUTHENTICATED and 403 for PERMISSION_DENIED."))
y += 8

# --- GemFire servers (collapsed) --------------------------------------------------------------------
panels.append(row("GemFire servers and resource cost", y)); y += 1
panels.append(timeseries("Members up (Prometheus logs in as the 'metrics' user)",
                         [target('sum by (node) (up{job="gemfire"})', "{{node}}")],
                         0, y, 8, 8, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="With a SecurityManager on, each member's /metrics endpoint needs a login. Prometheus uses "
                              "the SecurityManager's metrics user, which only has CLUSTER:READ."))
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
    "uid": "auth",
    "title": "08 · Tokens and permissions",
    "tags": ["gemfire", "grpc", "08-token-auth-rbac"],
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
    pathlib.Path(__file__).parent / "dashboards" / "auth.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
