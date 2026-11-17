// Description: Concurrency sweep: 1, 4, 16 and 64 VUs in a closed loop, 01's 13-op mix on crud-partition, 30s each (one client at a time)
// Examples: 11
// Clients: native-java, grpc-go
// Run: sequential
// Breakdown: vus=1,4,16,64
// Env: STEPS=1,4,16,64 DURATION=30s KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
//
// How throughput and the GemFire call time grow with the number of callers, and whether the gap
// between the clients widens or closes. The clients handle at most HTTP_THREADS (64) requests at once.
import { options as base, picker, mixFromEnv, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';
import { call, preload } from '../../lib/experiments.js';
import { markSlice, sliceScenarios } from '../../lib/slices.js';

const STEPS = (__ENV.STEPS || '1,4,16,64').split(',').map(Number);
const { scenarios, thresholds, length } = sliceScenarios('vus', STEPS, {
  duration: __ENV.DURATION || '30s', perSlice: (v) => ({ vus: v }),
});
export const options = base({ scenarios, thresholds: Object.assign({ http_req_failed: ['rate<0.05'] }, thresholds) });

const next = picker(mixFromEnv());

export function setup() {
  preload({ count: KEY_SPACE, size: SIZE });
}

export function run() {
  markSlice(length, { vus: __ENV.SLICE });
  call(next(), randomKey(), { size: SIZE });
}
