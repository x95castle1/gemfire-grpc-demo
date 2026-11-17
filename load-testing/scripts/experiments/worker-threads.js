// Description: Saturation for the worker-threads runner: 64 VUs in a closed loop, 01's 13-op mix on crud-partition, 1m (run by 11-experiments/worker-threads.sh, once per setting)
// Examples: 11
// Clients: native-java, grpc-go
// Run: sequential
// Env: VUS=64 DURATION=1m KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
//
// On its own it's just a saturation run. 11-experiments/worker-threads.sh restarts the servers with a
// different gemfire.grpc.worker-threads before each run and puts the results side by side.
import { options as base, picker, mixFromEnv, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';
import { call, preload } from '../../lib/experiments.js';

export const options = base({
  scenarios: { saturate: { executor: 'constant-vus', vus: Number(__ENV.VUS || 64), duration: __ENV.DURATION || '1m', exec: 'run' } },
  thresholds: { http_req_failed: ['rate<0.05'] },
});

const next = picker(mixFromEnv());

export function setup() {
  preload({ count: KEY_SPACE, size: SIZE });
}

export function run() {
  call(next(), randomKey(), { size: SIZE });
}
