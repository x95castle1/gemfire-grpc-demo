// Description: A steady mix of Person Gets and Puts (as Any) and field queries at 100 req/s for 1m
// Examples: 04
// Clients: grpc-go
// Run: parallel
// Env: RATE=100 DURATION=1m PEOPLE=10000
import { options as base } from '../../lib/crud.js';
import { PEOPLE, get, load, put } from '../../lib/people.js';
import { query } from '../../lib/query.js';

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
const CITIES = ['London', 'Paris', 'Berlin', 'Madrid', 'Rome', 'Boston', 'Austin', 'Toronto', 'Tokyo', 'Sydney'];
const NICKNAMES = ['Ace', 'Bear', 'Chip', 'Dot', 'Echo'];

// Weighted mix: each entry runs `weight` times out of 100.
const MIX = [
  { weight: 50, run: () => get(rand(0, PEOPLE)) },
  { weight: 20, run: () => put(rand(0, PEOPLE)) },
  { weight: 15, run: () => query('by-city', 'SELECT p.first_name, p.age FROM /people p WHERE p.address.city = $1',
    [CITIES[rand(0, CITIES.length)]]) },
  { weight: 10, run: () => { const lo = rand(18, 80); return query('age-range',
    'SELECT COUNT(*) FROM /people p WHERE p.age >= $1 AND p.age < $2', [lo, lo + 10]); } },
  { weight: 5, run: () => query('nickname', 'SELECT p.id FROM /people p WHERE $1 IN p.nicknames',
    [NICKNAMES[rand(0, NICKNAMES.length)]]) },
];

export function setup() {
  load();
}

export default function () {
  let pick = rand(0, 100);
  for (const m of MIX) {
    if (pick < m.weight) return m.run();
    pick -= m.weight;
  }
}
