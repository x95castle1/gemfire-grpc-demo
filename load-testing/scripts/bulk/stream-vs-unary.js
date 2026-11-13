// Description: Streaming vs one call per entry: the same put/get/remove of 2,000 entries as a stream, 16 unary calls in flight, then 1 at a time (45s each, one client at a time)
// Examples: 02
// Clients: native-java, grpc-java
// Run: sequential
// Breakdown: mode=stream,unary×16,unary×1
// Env: VUS=4 DURATION=45s COUNT=2000 BATCH=500 SIZE=1024
import { cycle, markSlice, modeTag, options as base, reset, seconds, vuStart } from '../../lib/bulk.js';

const DURATION = __ENV.DURATION || '45s';
const SLICE = seconds(DURATION);
const GAP = 10;   // lets the last iterations of one slice finish before the next starts
const COUNT = Number(__ENV.COUNT || 2000);
const SLICES = [
  { name: 'stream', mode: 'stream', inflight: 1 },
  { name: 'unary16', mode: 'unary', inflight: 16 },
  { name: 'unary1', mode: 'unary', inflight: 1 },
];

const scenarios = {};
const thresholds = { http_req_failed: ['rate<0.01'] };
SLICES.forEach((s, i) => {
  scenarios[s.name] = {
    executor: 'constant-vus',
    vus: Number(__ENV.VUS || 4),
    duration: DURATION,
    startTime: `${i * (SLICE + GAP)}s`,
    gracefulStop: `${GAP}s`,
    exec: 'run',
    env: { MODE: s.mode, INFLIGHT: String(s.inflight) },
  };
  // Thresholds on tagged sub-metrics put them in the summary export, for load.sh's by-mode table.
  const tag = `mode:${modeTag(s.mode, s.inflight)}`;
  for (const m of ['bulk_entries', 'bulk_slice_seconds', 'http_reqs']) thresholds[`${m}{${tag}}`] = ['count>=0'];
  thresholds[`gemfire_call_duration{${tag}}`] = ['max>=0'];
});

export const options = base({ scenarios, thresholds });

export function setup() {
  reset();
}

export function run() {
  const mode = __ENV.MODE;
  const inflight = Number(__ENV.INFLIGHT);
  markSlice(SLICE, { mode: modeTag(mode, inflight) });
  cycle(vuStart(), { count: COUNT, mode, inflight });
}

export function teardown() {
  reset();
}
