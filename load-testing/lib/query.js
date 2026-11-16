// Shared helpers for the /query scripts (03-oql-query). The client runs the OQL in the request
// body over the Query RPC (see 03-oql-query/README.md, "HTTP API").
import http from 'k6/http';
import { check, fail } from 'k6';
import { Counter } from 'k6/metrics';
import { BASE_URL, gemfireCall } from './crud.js';

/** Entries in the `query` region (loaded in setup()). Values are ints spread over 0..9,999. */
export const ENTRIES = Number(__ENV.ENTRIES || 10000);

/** Rows the queries returned. k6 counts requests; this counts rows. */
export const rows = new Counter('query_rows');

/** item-00042, the key format the client loads. */
export function itemKey(i) {
  return `item-${String(i).padStart(5, '0')}`;
}

/** Fills the `query` region with ENTRIES entries (setup). */
export function load() {
  const res = http.post(`${BASE_URL}/query/load?count=${ENTRIES}`, null, { timeout: '300s', tags: { name: 'query/load' } });
  if (res.status !== 200) fail(`load failed: ${res.status ? `${res.status} ${res.body}` : res.error}`);
}

/**
 * Runs one named OQL query with its parameters (each bound as an int32 if it's a number,
 * otherwise as a string). Records the GemFire time (X-GemFire-Micros) and the rows returned,
 * tagged with the query's name.
 */
export function query(name, oql, params = []) {
  const q = [`name=${name}`, 'rows=0', ...params.map((p) => `p=${encodeURIComponent(p)}`)].join('&');
  const tags = { name: `query/${name}`, op: name };
  const res = http.post(`${BASE_URL}/query?${q}`, oql, { tags, headers: { 'Content-Type': 'text/plain' } });
  check(res, { 'status is 200': (r) => r.status === 200 });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, tags);
  const n = res.headers['X-Rows'];
  if (n !== undefined) rows.add(Number(n), tags);
  return res;
}
