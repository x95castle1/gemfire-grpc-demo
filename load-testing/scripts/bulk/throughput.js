// Description: Bulk throughput: 4 VUs loop put → get → remove of 10,000 entries in chunks of 500, for 2m (one client at a time)
// Examples: 02
// Clients: native-java, grpc-java
// Run: sequential
// Env: VUS=4 DURATION=2m COUNT=10000 BATCH=500 SIZE=1024
import { cycle, options as base, reset, vuStart } from '../../lib/bulk.js';

export const options = base({
  scenarios: {
    throughput: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 4),
      duration: __ENV.DURATION || '2m',
    },
  },
});

export function setup() {
  reset();
}

export default function () {
  cycle(vuStart());
}

export function teardown() {
  reset();
}
