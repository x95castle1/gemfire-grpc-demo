// Shared helpers for the /xds scripts (05-xds-discovery). The client reaches the cluster over two
// channels, direct (dns:///server1:50051) and xds (xds:///gemfire_grpc), chosen per call with
// ?via=. Every call is tagged with its channel, so the results split by `via`.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, KEY_SPACE, SIZE, gemfireCall, options as crudOptions } from './crud.js';

export const VIAS = ['direct', 'xds'];
/** Requests/s per channel. Light on purpose: these scripts show where calls go, not how fast. */
const RATE = Number(__ENV.RATE || 20);

/** One Get (80%) or Put (20%) of a random key over the given channel. */
export function call(via) {
  const put = Math.random() < 0.2;
  const op = put ? 'Put' : 'Get';
  const key = Math.floor(Math.random() * KEY_SPACE);
  const url = `${BASE_URL}/crud/${put ? 'put' : 'get'}?key=${key}&size=${SIZE}&via=${via}`;
  const tags = { name: `crud/${op}`, op, via };
  const res = put ? http.post(url, null, { tags }) : http.get(url, { tags });
  check(res, { 'status is 200': (r) => r.status === 200 }, { via });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { op, via });
  return res;
}

/**
 * Options for a script: one constant-rate scenario per channel, running at the same time.
 * Thresholds on the per-channel sub-metrics put them in the summary export, which
 * scripts/load.sh reads for its "By via" table.
 */
export function options(duration, thresholds = {}) {
  const scenarios = {};
  const t = {};
  for (const via of VIAS) {
    scenarios[via] = {
      executor: 'constant-arrival-rate', exec: via, rate: RATE, timeUnit: '1s', duration,
      preAllocatedVUs: 20, maxVUs: 200, tags: { via },
    };
    t[`http_reqs{via:${via}}`] = ['count>=0'];
    t[`http_req_failed{via:${via}}`] = ['rate>=0'];
    t[`gemfire_call_duration{via:${via}}`] = ['p(99)>=0'];
  }
  return crudOptions({ scenarios, thresholds: Object.assign({ http_req_failed: ['rate<0.01'] }, t, thresholds) });
}
