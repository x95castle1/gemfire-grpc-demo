// Description: Who gets in: alice, bob, carol and nobody doing Gets and Puts on two regions, 20 req/s for 1m
// Examples: 08
// Clients: grpc-go
// Run: parallel
// Breakdown: user=alice,bob,carol,none
// Env: RATE=20 DURATION=1m KEY_SPACE=10000 SIZE=1024
import { options as base, preload } from '../../lib/crud.js';
import { REGIONS, USERS, call } from '../../lib/auth.js';

const thresholds = {};
for (const u of USERS) {   // per-user sub-metrics, for the "By user" table
  thresholds[`http_reqs{user:${u}}`] = ['count>=0'];
  thresholds[`http_req_failed{user:${u}}`] = ['rate>=0'];
  thresholds[`gemfire_call_duration{user:${u}}`] = ['p(99)>=0'];
  thresholds[`http_reqs{user:${u},status:401}`] = ['count>=0'];   // UNAUTHENTICATED
  thresholds[`http_reqs{user:${u},status:403}`] = ['count>=0'];   // PERMISSION_DENIED
}

export const options = base({
  scenarios: {
    mixed: {
      executor: 'constant-arrival-rate', rate: Number(__ENV.RATE || 20), timeUnit: '1s',
      duration: __ENV.DURATION || '1m', preAllocatedVUs: 10, maxVUs: 50,
    },
  },
  thresholds,
});

export function setup() {
  preload();   // as alice, into orders
}

export default function () {
  call(USERS[Math.floor(Math.random() * USERS.length)], REGIONS[Math.floor(Math.random() * REGIONS.length)]);
}
