// Description: Retries vs no retries: 20 req/s on each channel (failfast, retry, waitforready) for 2m; server1 is stopped at 40s and started at 55s
// Examples: 10
// Clients: grpc-go
// Run: parallel
// Breakdown: via=failfast,retry,waitforready
// Events: 40s stop server1, 55s start server1
// Env: RATE=20 DURATION=2m KEY_SPACE=10000 SIZE=1024
import { preload } from '../../lib/crud.js';
import { call, options as errorsOptions } from '../../lib/errors.js';

// Failures are the point here (every channel goes to server1 alone), so they don't fail the run.
export const options = errorsOptions(__ENV.DURATION || '2m', { http_req_failed: ['rate<=1'] });

export function setup() {
  preload();
}

export function failfast() { call('failfast'); }
export function retry() { call('retry'); }
export function waitforready() { call('waitforready'); }
