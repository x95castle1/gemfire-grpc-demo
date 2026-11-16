#!/usr/bin/env python3
"""Generates this example's Grafana dashboard: dashboards/tls.json.

The dashboard JSON is generated, not hand-edited: change this file, then run

    python3 monitoring/grafana/generate-dashboard.py

from 06-tls/ (Python 3 standard library only). Grafana picks up the new JSON within
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


VIAS = ["direct", "xds"]
VIA_COLORS = {"direct": "#3987e5", "xds": "#199e70"}
SERVER_COLORS = {"server1": NODE_COLORS["server1"], "server2": NODE_COLORS["server2"], "none": "#e5484d"}
RUN = 'client="grpc-go", run=~"$run"'
GF = "k6_gemfire_call_duration_seconds"   # X-GemFire-Micros: the GemFire call alone
REQS = "k6_http_reqs_total"
CALLS = "demo_calls_total"                 # every gRPC call, by channel and the server that answered
XDS = 'exported_instance="/tls"'           # PartitionedRegionStats of the load-test region


def per_via(expr_for, legend="{v}"):
    return [target(expr_for(v), legend.format(v=v), chr(65 + i)) for i, v in enumerate(VIAS)]


def bargauge(title, targets, x, y, w, h, desc=""):
    """Horizontal bars, one per server: the share of a channel's calls each server answered."""
    return {
        "type": "bargauge", "id": nid(), "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "targets": [dict(t, instant=True, range=False) for t in targets],
        "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1, "decimals": 0,
                                     "color": {"mode": "fixed", "fixedColor": "text"},
                                     "thresholds": {"mode": "absolute", "steps": [{"color": "text", "value": None}]}},
                        "overrides": color_overrides(SERVER_COLORS)},
        "options": {"orientation": "horizontal", "displayMode": "basic", "showUnfilled": True,
                    "valueMode": "color", "namePlacement": "left", "sizing": "auto",
                    "text": {"titleSize": 14, "valueSize": 22}, "minVizHeight": 16, "maxVizHeight": 28,
                    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
    }


def share(channel):
    """Each server's share of the channel's gRPC calls over the time range (the demo's included)."""
    return (f'sum by (server) (increase({CALLS}{{channel="{channel}"}}[$__range]))'
            f' / scalar(sum(increase({CALLS}{{channel="{channel}"}}[$__range])))')


panels, y = [], 0
panels.append(text(
    "**06 · One-way TLS.** Every member's gRPC endpoint presents a certificate signed by the demo CA "
    "(`./certs`, made at startup) and the grpc-go client verifies it. **direct** dials `dns:///server1:50051` "
    "over TLS; **xds** reaches the locator over TLS and the locator tells it to use TLS for the servers too. "
    "Fill the panels with `make select-load-test EX=06 SCRIPT=xds/balance` (both channels, 1 minute), "
    "or `SCRIPT=xds/failover` to also stop server1 at 40 s and start it at 80 s (annotated).", 0, y, 24, 3))
y += 3

# --- Every call is TLS ----------------------------------------------------------------------------
SECURITY_COLORS = {"tls": "#199e70", "insecure": "#e5484d", "none": "#9085e9"}
panels.append(row("Every call is TLS", y)); y += 1
def security_share(channel):
    return (f'sum by (security) (increase({CALLS}{{channel="{channel}"}}[$__range]))'
            f' / scalar(sum(increase({CALLS}{{channel="{channel}"}}[$__range])))')
for i, v in enumerate(VIAS):
    g = bargauge(f"{v}: share of calls by transport security", [target(security_share(v), "{{security}}")], 8 * i, y, 8, 5,
                 desc="From each call's peer AuthInfo (demo_calls_total{security}): tls = the call's connection was TLS. "
                      "\"none\" = the call never reached a server.")
    g["fieldConfig"]["overrides"] = color_overrides(SECURITY_COLORS)
    panels.append(g)
expiry = stat("Server certificate expires in",
              [target('min by (server) (demo_server_cert_expiry_seconds) - time()', "{{server}}")],
              16, y, 8, 5, unit="s", decimals=0, colors=SERVER_COLORS,
              desc="From the certificate each server presented on its last TLS call (demo_server_cert_expiry_seconds). "
                   "The demo certificate is valid for 365 days; delete ./certs and restart to make new ones.")
expiry["fieldConfig"]["defaults"]["unit"] = "dtdurations"
panels.append(expiry)
y += 5

# --- Balanced vs not balanced ------------------------------------------------------------------
panels.append(row("Where the calls went (as in 05, now over TLS)", y)); y += 1
panels.append(bargauge("direct: share of calls per server", [target(share("direct"), "{{server}}")], 0, y, 12, 5,
                       desc="Not balanced: DNS gives one address, so every call goes to server1. Over the whole time range, "
                            "from the client's interceptor (demo_calls_total), so a failover in the range shows up as "
                            "\"none\" (calls that reached no server). Narrow the range to one run to see just that run."))
panels.append(bargauge("xds: share of calls per server", [target(share("xds"), "{{server}}")], 12, y, 12, 5,
                       desc="Balanced: round robin over the servers the locator reported, so each gets half. "
                            "During a failover the survivor takes everything until the other is back."))
y += 5
for i, v in enumerate(VIAS):
    panels.append(timeseries(f"{v}: calls/s by server",
                             [target(f'sum by (server) (rate({CALLS}{{channel="{v}"}}[30s])) > 0', "{{server}}")],
                             12 * i, y, 12, 8, unit="reqps", colors=SERVER_COLORS, stack=True,
                             desc="Each call's peer address, mapped to server1/server2. \"none\" means the call never "
                                  "reached a server (UNAVAILABLE). "
                                  + ("direct only ever reaches server1." if v == "direct" else
                                     "xds alternates between the servers, and moves everything to the survivor when one stops.")))
y += 8
panels.append(timeseries("gRPC connections per server",
                         [target("sum by (node) (gemfire_grpc_server_connections)", "{{node}}")],
                         0, y, 12, 7, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="GrpcServerStats.grpc.server.connections. It should read 2 on server1 (direct + xds) and 1 on "
                              "server2, but with TLS it drifts down: every refused handshake (each demo run makes one plaintext "
                              "attempt) subtracts 1 without ever adding it, so server1 can even go negative. A bug in the "
                              "extension's statistic, not real connections."))
panels.append(timeseries("Puts forwarded to the primary (/tls)",
                         [target(f"sum by (node) (rate(gemfire_putRemoteCompleted{{{XDS}}}[1m]))", "{{node}}")],
                         12, y, 12, 7, unit="ops", colors=NODE_COLORS,
                         desc="Balanced isn't partition-aware: round robin ignores which server owns a key. Every server "
                              "holds a copy of every bucket here, so Gets are answered locally, but a Put must go to the "
                              "bucket's primary, so about half are forwarded (PartitionedRegionStats.putRemoteCompleted)."))
y += 7

# --- Failover ---------------------------------------------------------------------------------------
panels.append(row("Failover: a server goes away (xds/failover)", y)); y += 1
panels.append(timeseries("Members up",
                         [target('sum by (node) (up{job="gemfire"})', "{{node}}")],
                         0, y, 8, 8, unit="short", decimals=0, colors=NODE_COLORS,
                         desc="Prometheus's scrape of each member: 1 = up. Shows when server1 is stopped."))
panels.append(timeseries("Failed calls/s by channel and code",
                         [target(f'sum by (channel, code) (rate({CALLS}{{code!="OK"}}[30s])) > 0', "{{channel}} {{code}}")],
                         8, y, 8, 8, unit="reqps",
                         desc="UNAVAILABLE while a channel has no server to send to: direct for as long as server1 is down, "
                              "xds only for the calls in flight when it stopped."))
fail = stat("Failed requests (k6)", per_via(lambda v:
            f'(sum(increase({REQS}{{{RUN}, via="{v}", expected_response="false"}}[$__range])) or vector(0))'
            f' / sum(increase({REQS}{{{RUN}, via="{v}"}}[$__range]))'),
            16, y, 8, 8, unit="percentunit", decimals=2, colors=VIA_COLORS,
            desc="Share of k6 requests that failed over the time range, for the runs in the \"load run\" dropdown.")
fail["fieldConfig"]["defaults"]["mappings"] = [NO_DATA]
panels.append(fail)
y += 8

# --- Performance (collapsed) ----------------------------------------------------------------------
panels.append(row("Performance (k6): not the point of this example", y)); y += 1
panels.append(stat("GemFire call p50", per_via(lambda v: f'histogram_quantile(0.5, sum(increase({GF}{{{RUN}, via="{v}"}}[$__range])))'),
                   0, y, 6, 8, unit="s", decimals=2, colors=VIA_COLORS))
panels.append(stat("GemFire call p99", per_via(lambda v: f'histogram_quantile(0.99, sum(increase({GF}{{{RUN}, via="{v}"}}[$__range])))'),
                   6, y, 6, 8, unit="s", decimals=2, colors=VIA_COLORS))
panels.append(timeseries("GemFire call p50 by channel",
                         per_via(lambda v: f'histogram_quantile(0.5, sum(rate({GF}{{{RUN}, via="{v}"}}[30s])))'),
                         12, y, 12, 8, unit="s", colors=VIA_COLORS,
                         desc="Close once both servers are warm. On a fresh cluster xds starts slower: server2 never served "
                              "gRPC before and its code is still being JIT-compiled."))
y += 8

# --- Resource cost (collapsed) ----------------------------------------------------------------------
panels.append(row("GemFire servers and resource cost", y)); y += 1
panels.append(timeseries("Region gets/s per server (/xds)",
                         [target(f"sum by (node) (rate(gemfire_getsCompleted{{{XDS}}}[1m]))", "{{node}}")],
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
    "uid": "tls",
    "title": "06 · One-way TLS",
    "tags": ["gemfire", "grpc", "06-tls"],
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
    pathlib.Path(__file__).parent / "dashboards" / "tls.json"
out.write_text(json.dumps(dashboard, indent=2) + "\n")
print(f"wrote {out} ({len(panels)} top-level panels, {sum(len(p.get('panels', [])) for p in panels)} in collapsed rows)")
