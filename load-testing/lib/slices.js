// Sequential slices for request scripts: one scenario per value of a breakdown tag, run one after
// another, each tagged with its value. scripts/load.sh sees slice_seconds{tag} in the summary and
// prints REQ/S per value (calls ÷ the time that slice ran) instead of assuming the values ran at
// the same time. (02's bulk scripts have their own bulk_slice_seconds, in lib/bulk.js.)
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';

/** How long each slice ran, recorded once per slice and tagged with its value. */
export const sliceSeconds = new Counter('slice_seconds');

/** Records this scenario's length once (its first iteration), tagged with its slice. */
export function markSlice(seconds, tags) {
  if (exec.scenario.iterationInTest === 0) sliceSeconds.add(seconds, tags);
}

/** "90s", "2m", "1m30s" → seconds. */
export function seconds(duration) {
  let total = 0;
  for (const [, n, unit] of duration.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g)) {
    total += Number(n) * { ms: 0.001, s: 1, m: 60, h: 3600 }[unit];
  }
  return total;
}

/**
 * One constant-vus scenario per value, one after another with a GAP between them, all running
 * `exec`. Each gets tags {tag: value} and env SLICE (the value) plus whatever `perSlice(value)`
 * returns (e.g. { vus: 16 }). Returns { scenarios, thresholds }: the thresholds put each value's
 * sub-metrics into the summary export that scripts/load.sh reads.
 */
export function sliceScenarios(tag, values, { duration, gap = 10, vus = 8, exec: fn = 'run', perSlice = () => ({}) }) {
  const scenarios = {};
  const thresholds = {};
  const len = seconds(duration);
  values.forEach((v, i) => {
    scenarios[`${tag}_${v}`] = Object.assign({
      executor: 'constant-vus',
      vus,
      duration,
      startTime: `${i * (len + gap)}s`,
      gracefulStop: `${gap}s`,
      exec: fn,
      tags: { [tag]: String(v) },
      env: { SLICE: String(v) },
    }, perSlice(v));
    const t = `${tag}:${v}`;
    thresholds[`http_reqs{${t}}`] = ['count>=0'];
    thresholds[`http_req_failed{${t}}`] = ['rate>=0'];
    thresholds[`gemfire_call_duration{${t}}`] = ['p(99)>=0'];
    thresholds[`slice_seconds{${t}}`] = ['count>=0'];
  });
  return { scenarios, thresholds, length: len };
}
