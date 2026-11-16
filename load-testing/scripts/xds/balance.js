// Description: Balanced vs not: light traffic over the direct and the xds channel at once (20 req/s each, 1m) for the dashboard
// Examples: 05, 06
// Clients: grpc-go
// Run: parallel
// Breakdown: via=direct,xds
// Env: RATE=20 DURATION=1m KEY_SPACE=10000 SIZE=1024
import { preload } from '../../lib/crud.js';
import { call, options as xdsOptions } from '../../lib/xds.js';

export const options = xdsOptions(__ENV.DURATION || '1m');

export function setup() {
  preload();
}

export function direct() { call('direct'); }
export function xds() { call('xds'); }
