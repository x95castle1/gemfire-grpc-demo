// Description: Maximum throughput: 64 VUs in a closed loop (no pacing) for 2m, each client alone
// Examples: all
// Clients: native-java, grpc-java, grpc-go
// Run: sequential
// Env: VUS=64 DURATION=2m KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
import { crud, options as base, picker, mixFromEnv, preload, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';

export const options = base({
  scenarios: {
    saturate: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 64),
      duration: __ENV.DURATION || '2m',
    },
  },
  thresholds: { http_req_failed: ['rate<0.05'] },
});

const next = picker(mixFromEnv());

export function setup() {
  preload(KEY_SPACE, SIZE);
}

export default function () {
  crud(next(), randomKey());
}
