// Shared helpers for the /auth scripts (08-token-auth-rbac). The client acts as alice, bob, carol
// or nobody (?user=), and the cluster's SecurityManager decides per call: 401 = UNAUTHENTICATED,
// 403 = PERMISSION_DENIED. Those are expected outcomes here, not failures.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, KEY_SPACE, SIZE, gemfireCall } from './crud.js';

export const USERS = ['alice', 'bob', 'carol', 'none'];
export const REGIONS = ['orders', 'customers'];

// 401 and 403 are answers, not errors: only other statuses count in http_req_failed.
http.setResponseCallback(http.expectedStatuses(200, 401, 403));

/** One Get (70%) or Put (30%) of a random key in region, as user. */
export function call(user, region) {
  const put = Math.random() < 0.3;
  const op = put ? 'Put' : 'Get';
  const key = Math.floor(Math.random() * KEY_SPACE);
  const url = `${BASE_URL}/crud/${put ? 'put' : 'get'}?key=${key}&size=${SIZE}&user=${user}&region=${region}`;
  const tags = { name: `crud/${op}`, op, user, region };
  const res = put ? http.post(url, null, { tags }) : http.get(url, { tags });
  check(res, { 'answered (200, 401 or 403)': (r) => [200, 401, 403].includes(r.status) });
  const micros = res.headers['X-Gemfire-Micros'];
  if (micros !== undefined) gemfireCall.add(Number(micros) / 1000, { op, user });
  return res;
}
