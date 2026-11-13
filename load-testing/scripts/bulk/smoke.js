// Description: Quick sanity check: put/get/remove 1,000 entries in each mode (stream, unary×16, unary×1), plus keyset
// Examples: 02
// Clients: native-java, grpc-java
// Run: parallel
// Env: COUNT=1000 BATCH=500 SIZE=1024
import { bulk, cycle, options as base, reset, vuStart } from '../../lib/bulk.js';

const COUNT = Number(__ENV.COUNT || 1000);

export const options = base({
  scenarios: {
    smoke: { executor: 'per-vu-iterations', vus: 1, iterations: 3, maxDuration: '5m' },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
  },
});

export function setup() {
  reset();
}

export default function () {
  const start = vuStart();
  cycle(start, { count: COUNT });
  cycle(start, { count: COUNT, mode: 'unary', inflight: 16 });
  cycle(start, { count: COUNT, mode: 'unary', inflight: 1 });
  bulk('put', { start, count: COUNT });
  bulk('keyset');
  bulk('remove', { start, count: COUNT });
}
