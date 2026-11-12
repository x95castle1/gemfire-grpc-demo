// Description: Cache-style traffic: 90% Get / 10% Put at 2000 req/s for 3m
// Examples: all
// Clients: native-java, grpc-java
// Run: parallel
// Env: RATE=2000 DURATION=3m KEY_SPACE=10000 SIZE=1024
import { crud, options as base, picker, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

export const options = base({
  scenarios: {
    reads: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 2000),
      timeUnit: '1s',
      duration: __ENV.DURATION || '3m',
      preAllocatedVUs: 50,
      maxVUs: 400,
    },
  },
});

const next = picker({ Get: 90, Put: 10 });

export function setup() {
  preload(KEY_SPACE, SIZE);
}

export default function () {
  crud(next(), randomKey());
}
