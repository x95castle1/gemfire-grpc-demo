// Shared helpers for the /crud scripts. Every example's clients serve the same /crud API
// (see 01-crud-basics/README.md, "HTTP API"), so these scripts run against any of them.
import http from 'k6/http';
import { check, fail } from 'k6';
import { Trend } from 'k6/metrics';

/** Set per run by scripts/load.sh, e.g. http://grpc-client-java:8080 */
export const BASE_URL = __ENV.BASE_URL || 'http://grpc-client-java:8080';
/** Distinct keys the workload cycles through (preloaded in setup()). */
export const KEY_SPACE = int('KEY_SPACE', 10000);
/** Value size for writes; the client uses the same payload bytes for every write of a size. */
export const SIZE = int('SIZE', 1024);

/**
 * Time spent inside the GemFire call alone, as reported by the client (X-GemFire-Micros).
 * http_req_duration is the end-to-end time k6 saw; the difference is HTTP + app overhead.
 */
export const gemfireCall = new Trend('gemfire_call_duration', true);

/** gRPC RPC name → URL segment. Reads are GET, everything else POST. */
export const OPS = {
  Get: 'get',
  Put: 'put',
  GetAndPut: 'get-and-put',
  Remove: 'remove',
  RemoveIf: 'remove-if',
  GetAndRemove: 'get-and-remove',
  PutIfAbsent: 'put-if-absent',
  PutIfAbsentAndGet: 'put-if-absent-and-get',
  Replace: 'replace',
  ReplaceIf: 'replace-if',
  GetAndReplace: 'get-and-replace',
  ContainsKey: 'contains-key',
  EntryCount: 'entry-count',
};
const READS = new Set(['Get', 'ContainsKey', 'EntryCount']);

/** All 13 ops, weighted roughly like a read-mostly cache. */
export const DEFAULT_MIX = {
  Get: 40, Put: 15, ContainsKey: 10, GetAndPut: 5, PutIfAbsent: 5, PutIfAbsentAndGet: 2,
  Replace: 5, ReplaceIf: 3, GetAndReplace: 2, Remove: 4, RemoveIf: 3, GetAndRemove: 3, EntryCount: 1,
};

/**
 * Options every script starts from: summary stats for the results table, tags that keep
 * Prometheus cardinality low (no per-key URL label), and an error-rate threshold.
 */
export function options(extra) {
  return Object.assign({
    summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
    systemTags: ['status', 'method', 'name', 'scenario', 'expected_response', 'error_code'],
    thresholds: { http_req_failed: ['rate<0.01'] },
    setupTimeout: '5m',
  }, extra);
}

/** Runs one CRUD operation against the client and records both latencies. */
export function crud(op, key, size = SIZE) {
  const segment = OPS[op];
  if (!segment) fail(`unknown op ${op}`);
  let url = `${BASE_URL}/crud/${segment}?size=${size}`;
  if (op !== 'EntryCount') url += `&key=${key}`;
  const params = { tags: { name: `crud/${op}`, op } };
  const res = READS.has(op) ? http.get(url, params) : http.post(url, null, params);
  check(res, { 'status is 200': (r) => r.status === 200 });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { op });
  return res;
}

/** Returns a function that picks an op name according to the weights in `mix`. */
export function picker(mix = DEFAULT_MIX) {
  const table = [];
  for (const [op, weight] of Object.entries(mix)) {
    if (!OPS[op]) fail(`unknown op in mix: ${op}`);
    for (let i = 0; i < weight; i++) table.push(op);
  }
  return () => table[Math.floor(Math.random() * table.length)];
}

export function randomKey(space = KEY_SPACE) {
  return Math.floor(Math.random() * space);
}

/** Writes keys 0..count-1 through the client's bulk API so reads hit existing entries. */
export function preload(count = KEY_SPACE, size = SIZE) {
  const res = http.post(`${BASE_URL}/crud/preload?count=${count}&size=${size}`, null,
    { timeout: '300s', tags: { name: 'crud/preload' } });
  if (res.status !== 200) fail(`preload failed: ${res.status ? `${res.status} ${res.body}` : res.error}`);
  console.log(`preloaded ${count} keys of ${size} B into ${BASE_URL}`);
}

/** Parses an op mix like "Get=90,Put=10" from an env var, falling back to `def`. */
export function mixFromEnv(def = DEFAULT_MIX) {
  const raw = __ENV.MIX;
  if (!raw) return def;
  const mix = {};
  for (const part of raw.split(',')) {
    const [op, w] = part.split('=');
    mix[op.trim()] = Number(w);
  }
  return mix;
}

function int(name, def) {
  return __ENV[name] ? parseInt(__ENV[name], 10) : def;
}
