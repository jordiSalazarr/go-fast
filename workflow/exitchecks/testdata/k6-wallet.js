import http from 'k6/http';

export function setup() {
  return { id: http.post(`${__ENV.BASE_URL}/wallets`, '{}').json('id') };
}

export default function (data) {
  http.post(`${__ENV.BASE_URL}/wallets/${data.id}/transactions?trace=1`, '{"amount":5}');
  http.post(http.url`${__ENV.BASE_URL}/wallets/${data.id}/transactions`, '{"amount":5}');
  http.get(`${__ENV.BASE_URL}/wallets/${data.id}`);
  http.post(`${__ENV.BASE_URL}/wallets/${data.id}/transactions`, '{}', { tags: { name: 'renamed' } });
}
