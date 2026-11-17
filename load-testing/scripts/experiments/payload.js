// Description: Payload sweep: values of 100 B, 1 KiB, 10 KiB, 100 KiB and 1 MiB, 8 VUs, Get 80% / Put 20% on crud-partition, 30s each (one client at a time)
// Examples: 11
// Clients: native-java, grpc-go
// Run: sequential
// Breakdown: size=100,1024,10240,102400,1048576
// Env: SIZES=100,1024,10240,102400,1048576 VUS=8 DURATION=30s KEY_SPACE=10000 BYTES_PER_SIZE=52428800
//
// Whether the gap is per call (it shrinks as values grow) or per byte (it grows). Each size has its
// own key range, preloaded in setup with that size; big values get fewer keys, so each size holds
// at most BYTES_PER_SIZE (50 MiB) per client.
import { options as base, KEY_SPACE } from '../../lib/crud.js';
import { call, preload } from '../../lib/experiments.js';
import { markSlice, sliceScenarios } from '../../lib/slices.js';

const SIZES = (__ENV.SIZES || '100,1024,10240,102400,1048576').split(',').map(Number);
const BYTES_PER_SIZE = Number(__ENV.BYTES_PER_SIZE || 50 * 1024 * 1024);
/** Keys for a size, and where its range starts (ranges never overlap). */
const keys = (size) => Math.max(1, Math.min(KEY_SPACE, Math.floor(BYTES_PER_SIZE / size)));
const start = (i) => i * 1000000;

const { scenarios, thresholds, length } = sliceScenarios('size', SIZES, {
  duration: __ENV.DURATION || '30s', vus: Number(__ENV.VUS || 8),
});
export const options = base({ scenarios, thresholds: Object.assign({ http_req_failed: ['rate<0.05'] }, thresholds) });

export function setup() {
  SIZES.forEach((size, i) => preload({ count: keys(size), size, start: start(i) }));
}

export function run() {
  const size = Number(__ENV.SLICE);
  const i = SIZES.indexOf(size);
  markSlice(length, { size: String(size) });
  const key = start(i) + Math.floor(Math.random() * keys(size));
  call(Math.random() < 0.2 ? 'Put' : 'Get', key, { size });
}
