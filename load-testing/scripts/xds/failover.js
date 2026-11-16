// Description: Failover: light traffic over both channels (20 req/s each, 2m); server1 is stopped at 40s and started at 80s
// Examples: 05, 06, 07, 09
// Clients: grpc-go
// Run: parallel
// Breakdown: via=direct,xds
// Events: 40s stop server1, 80s start server1
// Env: RATE=20 DURATION=2m KEY_SPACE=10000 SIZE=1024
import { preload } from '../../lib/crud.js';
import { call, options as xdsOptions } from '../../lib/xds.js';

// Failures are the point here (direct loses its only server), so they don't fail the run.
export const options = xdsOptions(__ENV.DURATION || '2m', { http_req_failed: ['rate<=1'] });

export function setup() {
  preload();
}

export function direct() { call('direct'); }
export function xds() { call('xds'); }
