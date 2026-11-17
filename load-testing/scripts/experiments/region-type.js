// Description: REPLICATE vs PARTITION: 16 VUs, 01's 13-op mix, 45s on crud-partition then 45s on crud-replicate (one client at a time)
// Examples: 11
// Clients: native-java, grpc-go
// Run: sequential
// Breakdown: region=partition,replicate
// Env: VUS=16 DURATION=45s KEY_SPACE=10000 SIZE=1024 MIX=Get=40,Put=15,...
//
// On crud-partition each key lives on one server: the native client's single-hop sends it there, the
// gRPC client sends everything to server1, which forwards what it doesn't own. On crud-replicate every
// server holds every key, so gRPC Gets are never forwarded. What's left of the gap is translation.
import { options as base, picker, mixFromEnv, randomKey, KEY_SPACE, SIZE } from '../../lib/crud.js';
import { call, preload } from '../../lib/experiments.js';
import { markSlice, sliceScenarios } from '../../lib/slices.js';

const REGIONS = ['partition', 'replicate'];
const { scenarios, thresholds, length } = sliceScenarios('region', REGIONS, {
  duration: __ENV.DURATION || '45s', vus: Number(__ENV.VUS || 16),
});
export const options = base({ scenarios, thresholds: Object.assign({ http_req_failed: ['rate<0.05'] }, thresholds) });

const next = picker(mixFromEnv());

export function setup() {
  for (const region of REGIONS) preload({ count: KEY_SPACE, size: SIZE, region });
}

export function run() {
  const region = __ENV.SLICE;
  markSlice(length, { region });
  call(next(), randomKey(), { size: SIZE, region });
}
