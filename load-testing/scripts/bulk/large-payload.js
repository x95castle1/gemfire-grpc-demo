// Description: Large values: the throughput cycle with 64 KiB values (chunks capped at ~1 MiB = 16 entries), 1m30s, one client at a time
// Examples: 02
// Clients: native-java, grpc-java
// Run: sequential
// Env: VUS=4 DURATION=1m30s COUNT=2000 SIZE=65536
import { cycle, options as base, reset, vuStart } from '../../lib/bulk.js';

const COUNT = Number(__ENV.COUNT || 2000);
const SIZE = Number(__ENV.SIZE || 65536);

export const options = base({
  scenarios: {
    large: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 4),
      duration: __ENV.DURATION || '1m30s',
    },
  },
});

export function setup() {
  reset();
}

export default function () {
  // BATCH is left at 500; both clients cap each chunk at ~1 MiB of values (16 × 64 KiB), which
  // keeps every gRPC message under the server's 4 MiB max-message-size.
  cycle(vuStart(), { count: COUNT, size: SIZE });
}

export function teardown() {
  reset();
}
