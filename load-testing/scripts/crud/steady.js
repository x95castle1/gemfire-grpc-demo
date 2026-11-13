// Description: Latency at a fixed, realistic rate: 1000 req/s of the full 13-op mix for 5m
// Examples: all
// Clients: native-java, grpc-java, grpc-go
// Run: parallel
// Env: RATE=1000 DURATION=5m KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
import { crud, options as base, picker, mixFromEnv, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

export const options = base({
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 1000),
      timeUnit: '1s',
      duration: __ENV.DURATION || '5m',
      preAllocatedVUs: 50,
      maxVUs: 300,
    },
  },
});

const next = picker(mixFromEnv());

export function setup() {
  preload(KEY_SPACE, SIZE);
}

export default function () {
  crud(next(), randomKey());
}
