import assert from 'node:assert/strict';
import { after, before, beforeEach, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { createServer } from 'vite';

let server;
let client;
let userApi;
let postLogin;
let storage;
let location;

before(async () => {
  // Use Vite's real TypeScript/import.meta.env transform without starting a
  // browser or requiring a backend or an additional test framework.
  server = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    appType: 'custom',
    server: { middlewareMode: true },
    optimizeDeps: { noDiscovery: true },
  });
  client = await server.ssrLoadModule('/src/api/client.ts');
  userApi = await server.ssrLoadModule('/src/api/user.ts');
  postLogin = await server.ssrLoadModule('/src/utils/postLogin.ts');
});

after(async () => {
  await server?.close();
});

function stubGlobal(t, name, value) {
  const original = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, { configurable: true, value });
  t.after(() => {
    if (original) Object.defineProperty(globalThis, name, original);
    else delete globalThis[name];
  });
}

beforeEach((t) => {
  storage = new Map([
    ['proxima_admin_token', 'admin-token'],
    ['proxima_user_token', 'user-token'],
  ]);
  location = { href: '/current-page', pathname: '/current-page', search: '' };
  stubGlobal(t, 'localStorage', {
    getItem: (key) => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, value),
    removeItem: (key) => storage.delete(key),
  });
  stubGlobal(t, 'sessionStorage', {
    getItem: (key) => storage.get(`session:${key}`) ?? null,
    setItem: (key, value) => storage.set(`session:${key}`, value),
    removeItem: (key) => storage.delete(`session:${key}`),
  });
  stubGlobal(t, 'window', { location });
  client.setClientTokenType('user');
});

function interceptFetch(t) {
  const requests = [];
  t.mock.method(globalThis, 'fetch', (url, config) => new Promise((resolve) => {
    requests.push({ url, config, resolve });
  }));
  return requests;
}

function assertUnauthorized(error) {
  assert.ok(error instanceof client.ApiError);
  assert.equal(error.status, 401);
  return true;
}

test('getOrderReceipt selects the user token even when the admin client was selected last', async (t) => {
  const requests = interceptFetch(t);
  client.setClientTokenType('admin');

  const receiptPromise = userApi.getOrderReceipt('order-123');
  assert.equal(requests.length, 1);
  const [request] = requests;
  assert.ok(request.url.endsWith('/api/v1/user/orders/order-123/receipt'));
  assert.equal(request.config.method, 'GET');
  assert.equal(request.config.headers.Authorization, 'Bearer user-token');

  const receipt = { order_id: 'order-123', status: 'paid' };
  request.resolve(new Response(JSON.stringify(receipt), { status: 200 }));
  assert.deepEqual(await receiptPromise, receipt);
  assert.equal(storage.get('proxima_admin_token'), 'admin-token');
  assert.equal(storage.get('proxima_user_token'), 'user-token');
  assert.equal(location.href, '/current-page');
});

for (const expiredType of ['user', 'admin']) {
  const otherType = expiredType === 'user' ? 'admin' : 'user';

  test(`${expiredType} 401 only clears its own session after an overlapping ${otherType} request`, async (t) => {
    const requests = interceptFetch(t);
    const startRequest = (type) => {
      if (type === 'user') return userApi.getOrderReceipt('order-123');
      client.setClientTokenType('admin');
      return client.get('/api/v1/admin/orders');
    };

    const expiredRequest = startRequest(expiredType);
    const rejection = assert.rejects(expiredRequest, assertUnauthorized);
    const otherRequest = startRequest(otherType);

    assert.equal(requests.length, 2);
    assert.equal(requests[0].config.headers.Authorization, `Bearer ${expiredType}-token`);
    assert.equal(requests[1].config.headers.Authorization, `Bearer ${otherType}-token`);

    // Resolve the 401 while the shared client is selected for the other role.
    requests[0].resolve(new Response(null, { status: 401 }));
    await rejection;
    assert.equal(storage.has(`proxima_${expiredType}_token`), false);
    assert.equal(storage.get(`proxima_${otherType}_token`), `${otherType}-token`);
    assert.equal(location.href, expiredType === 'admin' ? '/admin/login' : '/login?continue=%2Fcurrent-page');

    requests[1].resolve(new Response(null, { status: 204 }));
    assert.equal(await otherRequest, undefined);
    assert.equal(storage.get(`proxima_${otherType}_token`), `${otherType}-token`);
  });
}

for (const loginPath of ['/api/v1/auth/login', '/api/v1/auth/passkey/login/finish']) {
  test(`${loginPath} 401 does not delete sessions or redirect`, async (t) => {
    const requests = interceptFetch(t);
    client.setClientTokenType('user');
    const loginRequest = client.post(loginPath, {});
    const rejection = assert.rejects(loginRequest, assertUnauthorized);
    client.setClientTokenType('admin');

    requests[0].resolve(new Response(JSON.stringify({ error: 'invalid credentials' }), { status: 401 }));
    await rejection;
    assert.equal(storage.get('proxima_admin_token'), 'admin-token');
    assert.equal(storage.get('proxima_user_token'), 'user-token');
    assert.equal(location.href, '/current-page');
  });
}

test('promo login target preserves an encoded campaign code', () => {
  const search = '?promo=' + encodeURIComponent('VIP & SAVE/20');
  assert.equal(postLogin.consumePostLogin('/portal/dashboard', search), '/portal/dashboard?promo=VIP+%26+SAVE%2F20');
});

test('an explicit OAuth continuation takes precedence over a promo', () => {
  const search = '?continue=%2Foauth%2Fauthorize%3Fclient_id%3Dabc&promo=VIP';
  assert.equal(postLogin.consumePostLogin('/portal/dashboard', search), '/oauth/authorize?client_id=abc');
});

test('the session fallback retains the promo across registration navigation', () => {
  postLogin.rememberPostLogin(postLogin.continueTarget('?promo=VIP'));
  assert.equal(postLogin.consumePostLogin('/portal/dashboard', ''), '/portal/dashboard?promo=VIP');
  assert.equal(storage.has('session:proxima_post_login'), false);
});

test('an unsafe continuation cannot redirect off-site and falls back to the promo', () => {
  assert.equal(postLogin.continueTarget('?continue=%2F%2Fevil.example&promo=VIP'), '/portal/dashboard?promo=VIP');
});

test('a saved promotion survives a fresh sign-in without URL or session state', (t) => {
  t.mock.method(Date, 'now', () => 1_800_000_000_000);
  storage.set('proxima_pending_promo', JSON.stringify({ code: 'WELCOME', savedAt: Date.now() }));
  assert.equal(postLogin.consumePostLogin('/portal/dashboard', ''), '/portal/dashboard?promo=WELCOME');
});

test('an expired saved promotion does not reopen checkout', (t) => {
  t.mock.method(Date, 'now', () => 1_800_000_000_000);
  storage.set('proxima_pending_promo', JSON.stringify({ code: 'EXPIRED', savedAt: Date.now() - 31 * 86400_000 }));
  assert.equal(postLogin.consumePostLogin('/portal/dashboard', ''), '/portal/dashboard');
  assert.equal(storage.has('proxima_pending_promo'), false);
});
