// Description: Write-dominated traffic: puts, conditional writes and removes at 2000 req/s for 3m
// Examples: all
// Clients: native-java, grpc-java, grpc-go
// Run: parallel
// Env: RATE=2000 DURATION=3m KEY_SPACE=10000 SIZE=1024
import { crud, options as base, picker, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

export const options = base({
  scenarios: {
    writes: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 2000),
      timeUnit: '1s',
      duration: __ENV.DURATION || '3m',
      preAllocatedVUs: 50,
      maxVUs: 400,
    },
  },
});

const next = picker({ Put: 50, PutIfAbsent: 10, Replace: 10, GetAndPut: 10, Remove: 10, Get: 10 });

export function setup() {
  preload(KEY_SPACE, SIZE);
}

export default function () {
  crud(next(), randomKey());
}
