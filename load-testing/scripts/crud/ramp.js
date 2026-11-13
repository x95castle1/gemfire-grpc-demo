// Description: Find the knee: ramp each client alone from 100 to 8000 req/s, then hold
// Examples: 01
// Clients: native-java, grpc-java, grpc-go
// Run: sequential
// Env: PEAK=8000 KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
import { crud, options as base, picker, mixFromEnv, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

const PEAK = Number(__ENV.PEAK || 8000);

export const options = base({
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 1000,
      stages: [
        { duration: '1m', target: Math.round(PEAK / 4) },
        { duration: '1m', target: Math.round(PEAK / 2) },
        { duration: '1m', target: PEAK },
        { duration: '30s', target: PEAK },
        { duration: '15s', target: 0 },
      ],
    },
  },
  // The point is to push past the limit, so don't fail the run on errors.
  thresholds: { http_req_failed: ['rate<1'] },
});

const next = picker(mixFromEnv());

export function setup() {
  preload(KEY_SPACE, SIZE);
}

export default function () {
  crud(next(), randomKey());
}
