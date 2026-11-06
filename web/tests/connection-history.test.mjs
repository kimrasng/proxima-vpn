import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { createServer } from 'vite';
import { createServer as createHttpServer } from 'node:http';

let server;
let api;
let format;

before(async () => {
  server = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    appType: 'custom',
    server: { middlewareMode: true, hmr: { server: createHttpServer() } },
    optimizeDeps: { noDiscovery: true },
    cacheDir: fileURLToPath(new URL(`../node_modules/.vite-history-${process.pid}`, import.meta.url)),
  });
  api = await server.ssrLoadModule('/src/api/admin.ts');
  format = await server.ssrLoadModule('/src/pages/admin/ConnectionHistoryFormat.ts');
});

after(async () => { await server?.close(); });

function intercept(t, body = {}) {
  const requests = [];
  t.mock.method(globalThis, 'fetch', async (url, config) => {
    requests.push({ url, config });
    return new Response(JSON.stringify(body), { status: 200 });
  });
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: key => key === 'proxima_admin_token' ? 'admin-token' : 'wrong-token' },
  });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, 'localStorage', descriptor);
    else delete globalThis.localStorage;
  });
  return requests;
}

test('bulk collection sends one atomic request with untouched decimal revisions', async t => {
  // Given: multiple nodes, including a revision beyond safe Number precision.
  const requests = intercept(t);
  const nodes = [{ id: 'node-a', expected_revision: '18446744073709551615' }, { id: 'node-b', expected_revision: '0' }];
  // When: the operator requests collection ON.
  await api.setConnectionLogging(nodes, true);
  // Then: one admin-authenticated transaction contains every expected revision.
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/api/v1/admin/nodes/connection-logging');
  assert.equal(requests[0].config.headers.Authorization, 'Bearer admin-token');
  assert.equal(requests[0].config.method, 'POST');
  assert.deepEqual(JSON.parse(requests[0].config.body), { nodes, enabled: true });
});

test('history forwards server filters and opaque cursor without full loading', async t => {
  // Given: a server-owned cursor and all supported investigation filters.
  const result = { items: [], next_cursor: 'opaque-next' };
  const requests = intercept(t, result);
  const query = { node_id: 'node-a', user_id: 'user-a', destination: 'example.test', protocol: 'vless', status: 'interrupted', from: '2026-11-06T00:00:00.000Z', to: '2026-11-07T00:00:00.000Z', cursor: 'opaque+/=', limit: 25 };
  // When: one history page is requested.
  const page = await api.getConnectionHistory('flows', query);
  // Then: the backend receives unchanged filters, no client offset or follow-up request.
  assert.equal(requests.length, 1);
  const url = new URL(requests[0].url, 'http://localhost');
  assert.equal(url.pathname, '/api/v1/admin/connection-history/flows');
  assert.deepEqual(Object.fromEntries(url.searchParams), Object.fromEntries(Object.entries(query).map(([key, value]) => [key, String(value)])));
  assert.deepEqual(page, result);
});

test('detail uses immutable node epoch and encoded entity keys', async t => {
  // Given: a boot-qualified entity with reserved URL characters.
  const requests = intercept(t);
  const row = { node_id: 'node/a', event: { collection_epoch: 'epoch/a', flow_id: 'boot:f:1/?', tunnel_id: 'boot:t:2' } };
  // When: a destination detail is requested.
  await api.getConnectionHistoryDetail('flows', row);
  // Then: every immutable path segment is escaped independently.
  assert.equal(requests[0].url, '/api/v1/admin/connection-history/flows/node%2Fa/epoch%2Fa/boot%3Af%3A1%2F%3F');
});

test('byte display preserves large aggregate integers and distinguishes unknown from zero', () => {
  // Given: counters larger than U64 are legal aggregate values.
  const value = '36893488147419103230';
  // When: formatting the counter for the local display.
  const formatted = format.formatHistoryBytes(value, '?');
  // Then: no integer precision is lost; missing is not presented as zero.
  assert.equal(formatted, `${BigInt(value).toLocaleString()} B`);
  assert.equal(format.formatHistoryBytes('0', '?'), '0 B');
  assert.equal(format.formatHistoryBytes(null, '?'), '?');
});
