// Shared helpers for the /bulk scripts (02-bulk-streaming). Both of 02's clients serve the same
// /bulk API (see 02-bulk-streaming/README.md, "HTTP API"); one call moves COUNT entries.
import http from 'k6/http';
import { check, fail } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';
import { BASE_URL, gemfireCall, options as crudOptions } from './crud.js';

/** Entries per bulk call. */
export const COUNT = int('COUNT', 10000);
/** Chunk size: entries per stream message (gRPC) = per putAll/getAll/removeAll call (native). */
export const BATCH = int('BATCH', 500);
/** Value size in bytes for puts. */
export const SIZE = int('SIZE', 1024);

/** Entries moved (GetStream / Get: entries found). k6 counts requests; this counts entries. */
export const entries = new Counter('bulk_entries');

/**
 * Seconds each breakdown slice ran (one per mode or batch size), recorded once per scenario.
 * A Counter's rate is over the whole test, so the per-slice entries/s that scripts/load.sh prints
 * is bulk_entries{slice} ÷ bulk_slice_seconds{slice}.
 */
export const sliceSeconds = new Counter('bulk_slice_seconds');

const NAMES = {
  put: { stream: 'PutStream', unary: 'Put' },
  get: { stream: 'GetStream', unary: 'Get' },
  remove: { stream: 'RemoveStream', unary: 'Remove' },
  keyset: { stream: 'KeySetStream' },
};

/** Mode tag value: "stream", or "unary×N" for N unary calls in flight. */
export function modeTag(mode, inflight) {
  return mode === 'unary' ? `unary×${inflight}` : 'stream';
}

/** Options every bulk script starts from: the crud defaults, with room for long calls. */
export function options(extra) {
  return crudOptions(Object.assign({ setupTimeout: '10m', teardownTimeout: '5m' }, extra));
}

/**
 * One bulk call: op is put | get | remove | keyset. Records the GemFire time (X-GemFire-Micros)
 * and the entries moved (X-Entries), tagged with op (the RPC name), mode and any extra tags.
 * record: false skips both (setup's loading shouldn't count as throughput).
 */
export function bulk(op, { start = 0, count = COUNT, batch = BATCH, size = SIZE, mode = 'stream', inflight = 1, tags = {}, record = true } = {}) {
  const rpc = NAMES[op] && NAMES[op][mode];
  if (!rpc) fail(`unknown bulk op ${op} (mode ${mode})`);
  const t = Object.assign({ name: `bulk/${op}`, op: rpc, mode: modeTag(mode, inflight) }, tags);
  const params = { tags: t, timeout: '300s' };
  let res;
  if (op === 'keyset') {
    res = http.get(`${BASE_URL}/bulk/keyset`, params);
  } else {
    const q = `start=${start}&count=${count}&batch=${batch}&size=${size}&mode=${mode}&inflight=${inflight}`;
    res = http.post(`${BASE_URL}/bulk/${op}?${q}`, null, params);
  }
  check(res, { 'status is 200': (r) => r.status === 200 });
  if (!record) return res;
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, t);
  const n = res.headers['X-Entries'];
  if (n !== undefined) entries.add(Number(n), t);
  return res;
}

/** put, get and remove the same COUNT keys: one full cycle that leaves the region as it was. */
export function cycle(start, opts = {}) {
  bulk('put', Object.assign({ start }, opts));
  bulk('get', Object.assign({ start }, opts));
  bulk('remove', Object.assign({ start }, opts));
}

/** First key of this VU's own range, so concurrent VUs never touch each other's keys. */
export function vuStart() {
  return (exec.vu.idInTest - 1) * 1000000;
}

/** Records this scenario's length once, tagged with its breakdown slice (see sliceSeconds). */
export function markSlice(seconds, tags) {
  if (exec.scenario.iterationInTest === 0) sliceSeconds.add(seconds, tags);
}

/** Empties the client's bulk region (setup/teardown), so every run starts from the same state. */
export function reset() {
  const res = http.post(`${BASE_URL}/bulk/reset`, null, { timeout: '300s', tags: { name: 'bulk/reset' } });
  if (res.status !== 200) fail(`reset failed: ${res.status ? `${res.status} ${res.body}` : res.error}`);
  return res.json('removed');
}

/** "90s", "2m", "1m30s" → seconds. */
export function seconds(duration) {
  let total = 0;
  for (const [, n, unit] of duration.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g)) {
    total += Number(n) * { ms: 0.001, s: 1, m: 60, h: 3600 }[unit];
  }
  return total;
}

function int(name, def) {
  return __ENV[name] ? parseInt(__ENV[name], 10) : def;
}
