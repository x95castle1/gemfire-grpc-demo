// Description: A steady mix of five OQL queries (scan, range, count, top 10, key lookup) at 100 req/s for 1m
// Examples: 03
// Clients: grpc-go
// Run: parallel
// Env: RATE=100 DURATION=1m ENTRIES=10000
import { options as base } from '../../lib/crud.js';
import { ENTRIES, itemKey, load, query } from '../../lib/query.js';

export const options = base({
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 100),
      timeUnit: '1s',
      duration: __ENV.DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 100,
    },
  },
});

const rand = (lo, hi) => lo + Math.floor(Math.random() * (hi - lo));

// Each query, with the parameters it gets on every call. Values are ints over 0..9,999, so
// `v < N` matches about N rows per 10,000 entries.
const QUERIES = [
  { name: 'scan', oql: 'SELECT * FROM /query v WHERE v < $1', params: () => [rand(10, 200)] },
  { name: 'range', oql: 'SELECT e.key, e.value FROM /query.entrySet e WHERE e.value >= $1 AND e.value < $2',
    params: () => { const lo = rand(0, 9900); return [lo, lo + 100]; } },
  { name: 'count', oql: 'SELECT COUNT(*) FROM /query v WHERE v < $1', params: () => [rand(0, 10000)] },
  { name: 'top', oql: 'SELECT e.key, e.value FROM /query.entrySet e ORDER BY e.value DESC LIMIT 10', params: () => [] },
  { name: 'key', oql: 'SELECT e.value FROM /query.entrySet e WHERE e.key = $1', params: () => [itemKey(rand(0, ENTRIES))] },
];

export function setup() {
  load();
}

export default function () {
  const q = QUERIES[rand(0, QUERIES.length)];
  query(q.name, q.oql, q.params());
}
