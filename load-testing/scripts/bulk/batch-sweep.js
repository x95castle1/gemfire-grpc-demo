// Description: Chunk size sweep: the throughput cycle at BATCH 10, 100, 500, 1000 and 5000 entries per chunk (30s each, one client at a time)
// Examples: 02
// Clients: native-java, grpc-java
// Run: sequential
// Breakdown: batch=10,100,500,1000,5000
// Env: VUS=4 DURATION=30s COUNT=10000 SIZE=1024 BATCHES=10,100,500,1000,5000
import { cycle, markSlice, options as base, reset, seconds, vuStart } from '../../lib/bulk.js';

const DURATION = __ENV.DURATION || '30s';
const SLICE = seconds(DURATION);
const GAP = 10;
const BATCHES = (__ENV.BATCHES || '10,100,500,1000,5000').split(',').map(Number);

const scenarios = {};
const thresholds = { http_req_failed: ['rate<0.01'] };
BATCHES.forEach((b, i) => {
  scenarios[`batch${b}`] = {
    executor: 'constant-vus',
    vus: Number(__ENV.VUS || 4),
    duration: DURATION,
    startTime: `${i * (SLICE + GAP)}s`,
    gracefulStop: `${GAP}s`,
    exec: 'run',
    env: { SLICE_BATCH: String(b) },
  };
  const tag = `batch:${b}`;
  for (const m of ['bulk_entries', 'bulk_slice_seconds', 'http_reqs']) thresholds[`${m}{${tag}}`] = ['count>=0'];
  thresholds[`gemfire_call_duration{${tag}}`] = ['max>=0'];
});

export const options = base({ scenarios, thresholds });

export function setup() {
  reset();
}

export function run() {
  const batch = Number(__ENV.SLICE_BATCH);
  markSlice(SLICE, { batch: String(batch) });
  cycle(vuStart(), { batch, tags: { batch: String(batch) } });
}

export function teardown() {
  reset();
}
