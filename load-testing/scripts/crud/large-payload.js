// Description: Big values: 50/50 Put/Get of 64 KiB values at 200 req/s for 2m (try SIZE=5000000 to hit gRPC's 4 MiB limit)
// Examples: 01
// Clients: native-java, grpc-java, grpc-go
// Run: parallel
// Env: SIZE=65536 RATE=200 DURATION=2m KEY_SPACE=1000
import { crud, options as base, picker, preload, randomKey } from '../../lib/crud.js';

const SIZE = Number(__ENV.SIZE || 65536);
const KEYS = Number(__ENV.KEY_SPACE || 1000);

export const options = base({
  scenarios: {
    large: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 200),
      timeUnit: '1s',
      duration: __ENV.DURATION || '2m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  // Oversized values are expected to fail over gRPC; report them instead of aborting.
  thresholds: { http_req_failed: ['rate<1'] },
});

const next = picker({ Put: 50, Get: 50 });

export function setup() {
  preload(KEYS, Math.min(SIZE, 4000000));
}

export default function () {
  crud(next(), randomKey(KEYS), SIZE);
}
