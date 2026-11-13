// Description: Quick sanity check: 100 req/s of the full 13-op mix for 30s
// Examples: all
// Clients: native-java, grpc-java, grpc-go
// Run: parallel
// Env: RATE=100 DURATION=30s KEY_SPACE=1000 SIZE=1024
import { crud, options as base, picker, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

export const options = base({
  scenarios: {
    smoke: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 100),
      timeUnit: '1s',
      duration: __ENV.DURATION || '30s',
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
  },
});

const next = picker();

export function setup() {
  preload(Math.min(KEY_SPACE, 1000), SIZE);
}

export default function () {
  crud(next(), randomKey(Math.min(KEY_SPACE, 1000)));
}
