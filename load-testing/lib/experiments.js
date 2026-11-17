// Shared helpers for the /experiments scripts (11-experiments). 11's clients serve 01's /crud API
// plus ?region=partition|replicate on every op and ?start= on preload (see 11-experiments/README.md).
import http from 'k6/http';
import { check, fail } from 'k6';
import { BASE_URL, OPS, gemfireCall } from './crud.js';

const READS = new Set(['Get', 'ContainsKey', 'EntryCount']);

/** One CRUD op on key in region (partition|replicate) with a size-byte value for writes. */
export function call(op, key, { size = 1024, region = 'partition' } = {}) {
  let url = `${BASE_URL}/crud/${OPS[op]}?size=${size}&region=${region}`;
  if (op !== 'EntryCount') url += `&key=${key}`;
  const params = { tags: { name: `crud/${op}`, op } };
  const res = READS.has(op) ? http.get(url, params) : http.post(url, null, params);
  check(res, { 'status is 200': (r) => r.status === 200 });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { op });
  return res;
}

/** Writes keys start..start+count-1 of size bytes into region, through the client's bulk API. */
export function preload({ count, size, start = 0, region = 'partition' }) {
  const res = http.post(`${BASE_URL}/crud/preload?count=${count}&size=${size}&start=${start}&region=${region}`, null,
    { timeout: '300s', tags: { name: 'crud/preload' } });
  if (res.status !== 200) fail(`preload failed: ${res.status ? `${res.status} ${res.body}` : res.error}`);
  console.log(`preloaded ${count} keys of ${size} B from ${start} into ${region} (${BASE_URL})`);
}
