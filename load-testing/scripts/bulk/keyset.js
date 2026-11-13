// Description: Key set of a big region: load 500,000 keys, then fetch the whole key set over and over for 1m, one client at a time
// Examples: 02
// Clients: native-java, grpc-java
// Run: sequential
// Env: KEYS=500000 VUS=2 DURATION=1m
import { bulk, options as base, reset } from '../../lib/bulk.js';

const KEYS = Number(__ENV.KEYS || 500000);

export const options = base({
  scenarios: {
    keyset: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 2),
      duration: __ENV.DURATION || '1m',
    },
  },
});

export function setup() {
  reset();
  // Small values: this test is about keys. Loaded with the stream/putAll path, 1,000 per chunk.
  const res = bulk('put', { start: 0, count: KEYS, batch: 1000, size: 64, tags: { name: 'bulk/setup' }, record: false });
  if (res.status !== 200) throw new Error(`loading ${KEYS} keys failed: ${res.status} ${res.body}`);
  console.log(`preloaded ${KEYS} keys`);
}

export default function () {
  bulk('keyset');
}

export function teardown() {
  reset();
}
