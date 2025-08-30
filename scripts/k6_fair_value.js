import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: 20,
  duration: '60s',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300'],
  },
};

const API_BASE = __ENV.API_BASE || 'http://localhost:8080';
const DEMO_KEY = __ENV.DEMO_KEY || '';

export default function () {
  const res = http.get(`${API_BASE}/api/v1/fair-value/AAPL`, { headers: { 'X-API-Key': DEMO_KEY } });
  check(res, {
    'status is 200': (r) => r.status === 200,
  });
  sleep(0.05);
}


