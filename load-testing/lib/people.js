// Shared helpers for the /people scripts (04-structured-values). The client stores Person
// messages packed in google.protobuf.Any and queries their fields with OQL (see
// 04-structured-values/README.md, "HTTP API"). Queries go through lib/query.js's query().
import http from 'k6/http';
import { check, fail } from 'k6';
import { BASE_URL, gemfireCall } from './crud.js';

/** People in the `people` region (loaded in setup()): p-00000.. */
export const PEOPLE = Number(__ENV.PEOPLE || 10000);

/** Fills the `people` region with PEOPLE Person messages (setup). */
export function load() {
  const res = http.post(`${BASE_URL}/people/load?count=${PEOPLE}`, null, { timeout: '300s', tags: { name: 'people/load' } });
  if (res.status !== 200) fail(`load failed: ${res.status ? `${res.status} ${res.body}` : res.error}`);
}

/** Get person i (one Get RPC, unpacked by the client). */
export function get(i) {
  return call('get', http.get(`${BASE_URL}/people/get?i=${i}`, { tags: { name: 'people/get', op: 'get' } }));
}

/** Put person i (one Put RPC with an Any value). */
export function put(i) {
  return call('put', http.post(`${BASE_URL}/people/put?i=${i}`, null, { tags: { name: 'people/put', op: 'put' } }));
}

function call(op, res) {
  check(res, { 'status is 200': (r) => r.status === 200 });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { name: `people/${op}`, op });
  return res;
}
