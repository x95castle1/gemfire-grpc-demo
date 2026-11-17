// Shared helpers for the /errors scripts (10-error-handling). The client reaches server1 over three
// channels that handle a failure differently, chosen per call with ?via=:
//   failfast      the grpc-go default: no connection, fail at once (UNAVAILABLE → HTTP 503)
//   retry         a retry policy: up to 5 attempts with backoff, then UNAVAILABLE
//   waitforready  wait for a connection, up to the call's 30 s deadline (DEADLINE_EXCEEDED → 504)
// Every call is tagged with its channel, so the results split by `via`.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, KEY_SPACE, SIZE, gemfireCall, options as crudOptions } from './crud.js';

export const VIAS = ['failfast', 'retry', 'waitforready'];
/** Requests/s per channel. Light on purpose: these scripts show what fails, not how fast. */
const RATE = Number(__ENV.RATE || 20);

/** One Get (80%) or Put (20%) of a random key over the given channel. */
export function call(via) {
  const put = Math.random() < 0.2;
  const op = put ? 'Put' : 'Get';
  const key = Math.floor(Math.random() * KEY_SPACE);
  const url = `${BASE_URL}/crud/${put ? 'put' : 'get'}?key=${key}&size=${SIZE}&via=${via}`;
  // waitforready calls can wait up to their 30 s deadline; give HTTP more than that.
  const params = { tags: { name: `crud/${op}`, op, via }, timeout: '45s' };
  const res = put ? http.post(url, null, params) : http.get(url, params);
  check(res, { 'status is 200': (r) => r.status === 200 }, { via });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { op, via });
  return res;
}

/**
 * Options for a script: one constant-rate scenario per channel, running at the same time.
 * waitforready parks a call for as long as server1 is unreachable (retry, for up to ~8 s), so each
 * scenario starts with room for RATE × 30 s of parked calls: k6 adds VUs too slowly to keep up
 * with a sudden pile-up, and would drop the iterations it can't start. Thresholds on the per-channel sub-metrics put them in the summary export,
 * which scripts/load.sh reads for its "By via" table.
 */
export function options(duration, thresholds = {}) {
  const scenarios = {};
  const t = {};
  for (const via of VIAS) {
    scenarios[via] = {
      executor: 'constant-arrival-rate', exec: via, rate: RATE, timeUnit: '1s', duration,
      preAllocatedVUs: RATE * 30, maxVUs: RATE * 40, tags: { via },
    };
    t[`http_reqs{via:${via}}`] = ['count>=0'];
    t[`http_req_failed{via:${via}}`] = ['rate>=0'];
    t[`gemfire_call_duration{via:${via}}`] = ['p(99)>=0'];
  }
  return crudOptions({ scenarios, thresholds: Object.assign({ http_req_failed: ['rate<0.01'] }, t, thresholds) });
}
