#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/experiments.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 11-experiments/ (Python 3 standard library only). Grafana picks up the new JSON within
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


OPS = "demo_op_latency_seconds"      # one /crud call's GemFire work, by client and region (client-reported)
CRUD = 'region=~"crud-.*"'           # the experiment regions (not the demo's)
REGION_COLORS = {"native-java · crud-partition": "#3987e5", "native-java · crud-replicate": "#8fbbf0",
                 "grpc-go · crud-partition": "#199e70", "grpc-go · crud-replicate": "#7fd1b3"}


def q(p, by="client, region"):
    return f'histogram_quantile({p}, sum by (le, {by}) (rate({OPS}_bucket{{{CRUD}}}[30s])))'


panels, y = [], 0
panels.append(text(
    "**11 · Experiments: what drives the gap between native and gRPC.** native-java (single-hop, straight to the "
    "server that owns each key) and grpc-go (everything to server1) run the same load. Each experiment is a "
    "`make select-load-test EX=11 SCRIPT=experiments/…` run whose steps run one after another, so its steps show up "
    "as plateaus over time; the load-test annotations name each run. `worker-threads.sh` restarts the servers "
    "between its runs (watch **members up**). Narrow the time range to one run to read it.",
    0, y, 24, 3))
y += 3

# --- What the clients see -------------------------------------------------------------------------
panels.append(row("What the clients see: calls/s and GemFire call time, by client and region", y)); y += 1
panels.append(timeseries("Calls/s by client and region",
                         [target(f'sum by (client, region) (rate({OPS}_count{{{CRUD}}}[30s])) > 0', "{{client}} · {{region}}")],
                         0, y, 8, 9, unit="reqps", colors=REGION_COLORS,
                         desc="Completed /crud calls per second, from each client's own histogram. One client runs at a time."))
panels.append(timeseries("GemFire call p50", [target(q(0.5) + " > 0", "{{client}} · {{region}}")],
                         8, y, 8, 9, unit="s", colors=REGION_COLORS,
                         desc="The GemFire call alone (no HTTP), from the client: demo_op_latency_seconds. The clearest "
                              "measure of the gap: at moderate load, k6 and HTTP limit calls/s more than GemFire does."))
panels.append(timeseries("GemFire call p99", [target(q(0.99) + " > 0", "{{client}} · {{region}}")],
                         16, y, 8, 9, unit="s", colors=REGION_COLORS))
y += 9

# --- Forwarding -----------------------------------------------------------------------------------
panels.append(row("Forwarding: what server1 sends on to server2 (experiments/region-type)", y)); y += 1
panels.append(timeseries("Forwarded by server1",
                         [target('sum(rate(gemfire_GetMessagesSent{node="server1"}[1m]))', "Gets forwarded (GetMessage)", "A"),
                          target('sum(rate(gemfire_putRemoteCompleted{node="server1", exported_instance="/crud-partition"}[1m]))',
                                 "Puts forwarded (putRemoteCompleted)", "B")],
                         0, y, 12, 8, unit="ops",
                         desc="Operations server1 had to send to server2, because server2 owns the key. Only the gRPC client "
                              "causes this: native single-hop sends each key to its owner. On crud-replicate both lines "
                              "drop to zero for Gets: server1 holds every key."))
panels.append(timeseries("Gets per server, by region",
                         [target('sum by (node, exported_instance) (rate(gemfire_gets{exported_instance=~"RegionStats-crud-replicate|RegionStats-partition-crud-partition"}[1m])) > 0',
                                 "{{node}} · {{exported_instance}}")],
                         12, y, 12, 8, unit="ops",
                         desc="CachePerfStats gets on each server. On crud-partition the gRPC client's Gets land on both "
                              "servers (server1 forwards half); on crud-replicate only on server1. Native spreads both by key."))
y += 8

# --- Server work ----------------------------------------------------------------------------------
panels.append(row("Server work: CPU, connections, restarts (concurrency, worker-threads)", y)); y += 1
cpu = "sum by (service) (rate(container_cpu_usage_seconds_total{service=~\"%s\"}[30s]))"
panels.append(timeseries("CPU by container (cores)", [target(cpu % "server1|server2|native-client-java|grpc-client-go", "{{service}}")],
                         0, y, 12, 8, unit="short", decimals=2, colors=SERVICE_COLORS,
                         desc="Where the work goes. With grpc-go, server1 does the translation and the forwarding; with "
                              "native, the client does the routing and both servers share the work."))
panels.append(timeseries("Members up", [target('sum by (node) (up{job="gemfire"})', "{{node}}")],
                         12, y, 6, 8, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="worker-threads.sh restarts server1 and server2 before each setting: the dips mark them."))
panels.append(timeseries("gRPC connections per server", [target("sum by (node) (gemfire_grpc_server_connections)", "{{node}}")],
                         18, y, 6, 8, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="GrpcServerStats, the only gRPC server statistic the extension exports. grpc-go holds one "
                              "connection, to server1."))
y += 8

# --- Bytes ----------------------------------------------------------------------------------------
panels.append(row("Bytes on the wire (experiments/payload)", y)); y += 1
net = "sum by (service) (rate(container_network_%s_bytes_total{service=~\"native-client-java|grpc-client-go|server1|server2\"}[30s]))"
panels.append(timeseries("Sent by container", [target(net % "transmit", "{{service}}")], 0, y, 12, 8, unit="Bps",
                         colors=SERVICE_COLORS,
                         desc="cAdvisor network bytes. During the payload sweep each step's value size shows as a plateau. "
                              "With grpc-go, server1 also sends half the Puts on to server2."))
panels.append(timeseries("Received by container", [target(net % "receive", "{{service}}")], 12, y, 12, 8, unit="Bps",
                         colors=SERVICE_COLORS))
y += 8

# --- Resources (collapsed) ------------------------------------------------------------------------
panels.append(row("GemFire servers and resource cost", y)); y += 1
panels.append(timeseries("Heap used per member",
                         [target('sum by (node) (gemfire_usedMemory{exported_instance="vmHeapMemoryStats"})', "{{node}}")],
                         0, y, 12, 8, unit="bytes", colors=NODE_COLORS))
panels.append(timeseries("Client memory (working set)",
                         [target('sum by (service) (container_memory_working_set_bytes{service=~"native-client-java|grpc-client-go"})', "{{service}}")],
                         12, y, 12, 8, unit="bytes", colors=SERVICE_COLORS))
y += 8

# Rows that start collapsed: move each one's panels inside it.
COLLAPSED = {"GemFire servers and resource cost"}
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
    "uid": "experiments",
    "title": "11 · Experiments: what drives the gap between native and gRPC",
    "tags": ["gemfire", "grpc", "11-experiments"],
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
    pathlib.Path(__file__).parent / "dashboards" / "experiments.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
