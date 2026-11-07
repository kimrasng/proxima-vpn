import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium, expect } from '@playwright/test';
import { createServer } from 'vite';

// Mount the production page without adding a route or changing the application.
// APIs are intercepted at HTTP boundaries; this does not prove host integration.
let server;
let browser;
let baseURL;
let evidence;
let runtime;
const release = {
  tag: 'v2.4.1', name: 'Proxima 2.4.1',
  published_at: '2026-11-01T12:00:00Z',
  html_url: 'https://github.com/example/proxima/releases/tag/v2.4.1',
};
const watchState = {
  config: { owner: 'example', repo: 'proxima', enabled: true },
  current: { version: 'v2.3.0', repository: 'example/proxima', revision: 'abc123', build_time: '2026-11-01T10:00:00Z', modified: false },
  latest: null, checked_at: null, next_check_at: null, running: true,
  comparison: 'unknown', update_available: null,
};
const hostState = { version: 1, consumed: [], operation: null };
const officialWatch = { ...watchState, config: { owner: 'kimrasng', repo: 'proxima-vpn', enabled: true }, latest: release };
// Repeated reads of one signed approval must retain its exact expiry.
const candidateIssuedAt = 1793894400;
const prepared = (releaseId, secondsFromNow = 3600) => ({
  status: 'prepared', release_id: releaseId, sequence: 2,
  from: images('a'), to: images('b'), rollback_compatible: true,
  expires_at: candidateIssuedAt + secondsFromNow,
});
const images = (suffix) => ({ api: `ghcr.io/example/api@sha256:${suffix.repeat(64)}`, web: `ghcr.io/example/web@sha256:${suffix.repeat(64)}` });
const operation = (phase) => ({
  version: 1, consumed: ['previous-release'], operation: {
    approval: { id: 'previous-release', from: images('a'), to: images('b'), rollback_compatible: true },
    phase, maintenance: true, migration_uncertain: true, error: 'apply_failed',
  },
});

before(async () => {
  evidence = await mkdtemp(join(tmpdir(), 'proxima-server-updates-'));
  runtime = await mkdtemp(join(tmpdir(), 'proxima-server-updates-runtime-'));
  server = await createServer({
    configFile: false,
    cacheDir: join(runtime, 'vite'),
    root: fileURLToPath(new URL('..', import.meta.url)),
    server: { host: '127.0.0.1', port: 0 },
    plugins: [{
      name: 'server-updates-test-harness',
      resolveId(id) {
        if (id === '/__server-updates-entry.tsx') return id;
      },
      load(id) {
        if (id !== '/__server-updates-entry.tsx') return;
        return `import React from 'react';
          import { createRoot } from 'react-dom/client';
          import '@cloudscape-design/global-styles/index.css';
          import i18n from '/src/i18n/index.ts';
          import ServerUpdates from '/src/pages/admin/ServerUpdates.tsx';
          await i18n.changeLanguage(new URLSearchParams(location.search).get('language') || 'ko');
          document.documentElement.lang = i18n.language;
          createRoot(document.getElementById('root')).render(React.createElement(ServerUpdates));`;
      },
      configureServer(vite) {
        vite.middlewares.use(async (req, res, next) => {
          if (req.url?.split('?')[0] !== '/__server-updates') return next();
          const html = await vite.transformIndexHtml('/__server-updates', `<!doctype html>
            <html><head><meta name="viewport" content="width=device-width,initial-scale=1">
            <link rel="icon" href="data:,"><title>Server updates test</title></head>
            <body><main id="root"></main><script type="module" src="/__server-updates-entry.tsx"></script></body></html>`);
          res.setHeader('Content-Type', 'text/html');
          res.end(html);
        });
      },
    }],
    optimizeDeps: { include: ['react', 'react-dom/client', 'react/jsx-dev-runtime'] },
    esbuild: { jsx: 'automatic' },
  });
  await server.listen();
  baseURL = server.resolvedUrls.local[0];
  browser = await chromium.launch({ channel: 'chrome', headless: true });
  console.log(`Browser evidence: ${evidence}`);
});

after(async () => {
  try {
    await browser?.close();
  } finally {
    try {
      await server?.close();
    } finally {
      if (runtime) await rm(runtime, { recursive: true, force: true });
    }
  }
});

async function openPage(t, language = 'en', width = 1440, dark = false, api = {}) {
  const context = await browser.newContext({ viewport: { width, height: 1000 }, isMobile: width < 500, hasTouch: width < 500 });
  t.after(() => context.close());
  await context.addInitScript(() => {
    if (location.pathname === '/__server-updates') localStorage.setItem('proxima_admin_token', 'test-admin-token');
  });
  const page = await context.newPage();
  await page.clock.install({ time: new Date(candidateIssuedAt * 1000) });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error' && !(api.expectedFailure && /Failed to load resource/.test(message.text()))) errors.push(message.text());
  });
  page.on('requestfailed', (request) => errors.push(request.failure()?.errorText));
  t.after(() => assert.deepEqual(errors, [], 'No console, runtime, or failed network requests'));
  await page.route('**/api/v1/admin/system-updates/*', async (route) => {
    const path = new URL(route.request().url()).pathname.split('/').at(-1);
    if (api.handler && await api.handler(route, path)) return;
    const json = { watch: watchState, status: hostState, preflight: { ok: false, reason: 'not_approved' }, candidate: prepared('root-approved-next'), prepare: { status: 'idle' } }[path];
    if (json) return route.fulfill({ json });
    return route.fulfill({ status: 404, json: { error: 'not_found' } });
  });
  await page.goto(`${baseURL}__server-updates?language=${language}`);
  await expect(page.getByPlaceholder('owner/repo')).toBeVisible();
  await expect(page.getByRole('button').filter({ hasText: /Refresh host status|호스트 상태 새로고침|刷新主机状态/ })).toBeEnabled();
  if (dark) await page.evaluate(() => document.body.classList.add('awsui-dark-mode'));
  return page;
}

for (const width of [1440, 390]) {
  for (const dark of [false, true]) {
    test(`prepare detected official release then review and apply exact signed payload at ${width}px ${dark ? 'dark' : 'light'}`, async (t) => {
      const signed = { ...prepared('publisher-approved-next'), payload_sha256: 'c'.repeat(64) };
      let preparation = { status: 'idle' };
      let preparePosts = 0;
      let applyPosts = 0;
      let prepareReads = 0;
      let finishPost;
      const pendingPost = new Promise((resolve) => { finishPost = resolve; });
      const page = await openPage(t, 'ko', width, dark, { handler: async (route, path) => {
        if (path === 'watch') { await route.fulfill({ json: officialWatch }); return true; }
        if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
        if (path === 'candidate') { await route.fulfill({ json: signed }); return true; }
        if (path === 'prepare') {
          if (route.request().method() === 'POST') {
            preparePosts += 1;
            assert.equal(route.request().headers().authorization, 'Bearer test-admin-token');
            assert.deepEqual(route.request().postDataJSON(), { tag: release.tag, password: 'owner-password', totp_code: '123456' });
            await pendingPost;
            preparation = { status: 'preparing', tag: release.tag };
            await route.fulfill({ status: 202, json: preparation });
          } else {
            prepareReads += 1;
            await route.fulfill({ json: preparation });
          }
          return true;
        }
        if (path === 'apply') {
          applyPosts += 1;
          assert.equal(route.request().headers().authorization, 'Bearer test-admin-token');
          assert.deepEqual(route.request().postDataJSON(), { release_id: 'publisher-approved-next', payload_sha256: 'c'.repeat(64), maintenance: true, password: 'apply-password', totp_code: '654321' });
          await route.fulfill({ status: 202, json: operation('accepted') });
          return true;
        }
        return false;
      } });
      const prepareButton = page.getByRole('button', { name: '선택한 릴리스 준비', exact: true });
      await expect(prepareButton).toBeDisabled();
      await page.getByRole('button', { name: '감지된 릴리스 선택' }).click();
      await page.getByRole('option', { name: release.tag, exact: true }).click();
      await prepareButton.click();
      const dialog = page.getByRole('dialog');
      const submit = dialog.getByRole('button', { name: '재인증하고 준비 요청' });
      await expect(submit).toBeDisabled();
      await dialog.getByLabel('관리자 비밀번호', { exact: true }).fill('clear-on-cancel');
      await page.keyboard.press('Escape');
      await prepareButton.click();
      await expect(dialog.getByLabel('관리자 비밀번호', { exact: true })).toHaveValue('');
      await dialog.getByLabel('관리자 비밀번호', { exact: true }).fill('owner-password');
      await dialog.getByLabel('TOTP 인증 코드', { exact: true }).fill('12345');
      await expect(submit).toBeDisabled();
      await dialog.getByLabel('TOTP 인증 코드', { exact: true }).fill('123456');
      await page.screenshot({ path: join(evidence, `prepare-auth-${width}-${dark}.png`), animations: 'disabled' });
      const requested = page.waitForRequest((request) => request.url().endsWith('/prepare') && request.method() === 'POST');
      await submit.click();
      await requested;
      await expect(dialog.getByLabel('관리자 비밀번호', { exact: true })).toBeDisabled();
      await expect(submit).toBeDisabled();
      finishPost();
      await expect(dialog).toBeHidden();
      await expect(page.getByText('릴리스를 준비하고 있습니다.', { exact: false })).toBeVisible();
      await expect(prepareButton).toBeDisabled();
      await expect(page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true })).toBeDisabled();
      await page.getByRole('button', { name: '준비 상태 새로고침', exact: true }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, `preparing-${width}-${dark}.png`) });
      assert.equal(prepareReads, 1, 'Preparation is never polled');
      const { from: _from, rollback_compatible: _rollback, ...summary } = signed;
      preparation = { status: 'prepared', tag: release.tag, candidate: summary };
      await page.getByRole('button', { name: '준비 상태 새로고침', exact: true }).click();
      await expect(page.getByText('릴리스 준비 완료.', { exact: false })).toBeVisible();
      await page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, `prepared-${width}-${dark}.png`), animations: 'disabled' });
      await page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true }).click();
      await expect(dialog.getByLabel('관리자 비밀번호', { exact: true })).toHaveValue('');
      await expect(dialog.getByLabel('TOTP 인증 코드', { exact: true })).toHaveValue('');
      await expect(dialog.getByText(signed.payload_sha256, { exact: true })).toBeVisible();
      await dialog.getByLabel('관리자 비밀번호', { exact: true }).fill('apply-password');
      await dialog.getByLabel('TOTP 인증 코드', { exact: true }).fill('654321');
      const apply = dialog.getByRole('button', { name: '중단에 동의하고 적용 요청' });
      await expect(apply).toBeDisabled();
      await dialog.getByRole('checkbox').check();
      await apply.focus();
      await page.screenshot({ path: join(evidence, `publisher-apply-${width}-${dark}.png`), animations: 'disabled' });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await apply.click();
      await expect(page.getByText('요청이 접수되었습니다(202).', { exact: false })).toBeVisible();
      assert.equal(preparePosts, 1);
      assert.equal(applyPosts, 1);
      assert.equal(prepareReads, 2);
    });
  }
}

test('arbitrary discovery cannot supply an install tag and invalid official tags stay unselectable', async (t) => {
  let posts = 0;
  const page = await openPage(t, 'en', 390, false, { handler: async (route, path) => {
    if (route.request().method() === 'POST') posts += 1;
    if (path === 'latest') { await route.fulfill({ json: { ...release, tag: new URL(route.request().url()).searchParams.get('owner') === 'kimrasng' ? '../unsafe' : release.tag } }); return true; }
    return false;
  } });
  for (const repository of ['other/proxima-vpn', 'kimrasng/proxima-vpn']) {
    await page.getByPlaceholder('owner/repo').fill(repository);
    const response = page.waitForResponse('**/latest?**');
    await page.getByRole('button', { name: 'Check latest release' }).click();
    await response;
    await expect(page.getByRole('button', { name: 'Select a detected release' })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Prepare selected release', exact: true })).toBeDisabled();
  }
  assert.equal(posts, 0);
});

for (const outcome of ['reauthentication_failed', 'prepare_failed', 'invalid_response', 'missing_api', 'unknown_post']) {
  test(`preparation ${outcome} clears credentials and cannot silently apply stale candidate`, async (t) => {
    let posts = 0;
    let preparation = { status: 'idle' };
    const page = await openPage(t, 'en', 390, false, { expectedFailure: true, handler: async (route, path) => {
      if (path === 'watch') { await route.fulfill({ json: officialWatch }); return true; }
      if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
      if (path !== 'prepare') return false;
      if (outcome === 'missing_api') { await route.fulfill({ status: 404, json: { error: 'not_found' } }); return true; }
      if (route.request().method() === 'POST') {
        posts += 1;
        if (outcome === 'reauthentication_failed') await route.fulfill({ status: 403, json: { error: outcome } });
        else if (outcome === 'unknown_post') await route.fulfill({ status: 503, json: { error: 'updater_unavailable' } });
        else { preparation = { status: 'preparing', tag: release.tag }; await route.fulfill({ status: 202, json: preparation }); }
      } else await route.fulfill({ json: preparation });
      return true;
    } });
    const prepareButton = page.getByRole('button', { name: 'Prepare selected release', exact: true });
    if (outcome === 'missing_api') {
      await expect(page.getByText('Preparation status unavailable.', { exact: false })).toBeVisible();
      await expect(prepareButton).toBeDisabled();
      await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeEnabled();
      assert.equal(posts, 0);
      return;
    }
    await page.getByRole('button', { name: 'Select a detected release' }).click();
    await page.getByRole('option', { name: release.tag, exact: true }).click();
    await prepareButton.click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Admin password', { exact: true }).fill('owner-password');
    await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
    await dialog.getByRole('button', { name: 'Reauthenticate and request preparation' }).click();
    await expect(dialog).toBeHidden();
    if (outcome === 'reauthentication_failed') {
      await expect(page.getByText('Reauthentication failed. Check your password and TOTP code before preparing again.', { exact: true })).toBeVisible();
      assert.equal(await page.evaluate(() => localStorage.getItem('proxima_admin_token')), 'test-admin-token');
      await prepareButton.click();
      await expect(dialog.getByLabel('Admin password', { exact: true })).toHaveValue('');
      await expect(dialog.getByLabel('TOTP authentication code', { exact: true })).toHaveValue('');
    } else if (outcome === 'unknown_post') {
      await expect(page.getByText('Preparation outcome unknown.', { exact: false })).toBeVisible();
      await expect(prepareButton).toBeDisabled();
      await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeDisabled();
      await page.getByRole('button', { name: 'Refresh preparation status', exact: true }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, `${outcome}-390.png`), animations: 'disabled' });
    } else {
      preparation = outcome === 'prepare_failed' ? { status: 'failed', tag: release.tag, error: 'signature_invalid' } : { status: 'prepared', tag: release.tag };
      await page.getByRole('button', { name: 'Refresh preparation status', exact: true }).click();
      await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeDisabled();
      await expect(page.getByText(outcome === 'prepare_failed' ? 'signature_invalid' : 'Preparation response missing or invalid.', { exact: outcome === 'prepare_failed' })).toBeVisible();
      if (outcome === 'invalid_response') await expect(prepareButton).toBeDisabled();
      await page.getByRole('button', { name: 'Refresh preparation status', exact: true }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, `${outcome}-390.png`) });
    }
    assert.equal(posts, 1, 'No automatic preparation retries');
  });
}

test('official discovery supplies only the detected tag and editing the repository invalidates selection', async (t) => {
  const page = await openPage(t);
  await page.route('**/api/v1/admin/system-updates/latest?**', (route) => route.fulfill({ json: release }));
  await page.getByPlaceholder('owner/repo').fill('kimrasng/proxima-vpn');
  await page.getByRole('button', { name: 'Check latest release' }).click();
  await page.getByRole('button', { name: 'Select a detected release' }).click();
  await expect(page.getByRole('option')).toHaveCount(1);
  await page.getByRole('option', { name: release.tag, exact: true }).click();
  const prepare = page.getByRole('button', { name: 'Prepare selected release', exact: true });
  await expect(prepare).toBeEnabled();
  await page.getByPlaceholder('owner/repo').fill('other/proxima-vpn');
  await expect(prepare).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Select a detected release' })).toBeDisabled();
});

for (const mismatch of ['release_id', 'payload_sha256', 'sequence', 'expires_at', 'to']) {
  test(`prepared summary ${mismatch} mismatch cannot offer an applicable candidate`, async (t) => {
    const signed = { ...prepared('summary-release'), payload_sha256: 'c'.repeat(64) };
    const overrides = { release_id: 'different-release', payload_sha256: 'd'.repeat(64), sequence: 3, expires_at: signed.expires_at + 1, to: images('e') };
    const page = await openPage(t, 'en', 390, false, { handler: async (route, path) => {
      if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
      if (path === 'candidate') { await route.fulfill({ json: signed }); return true; }
      if (path === 'prepare') { await route.fulfill({ json: { status: 'prepared', tag: release.tag, candidate: { ...signed, [mismatch]: overrides[mismatch] } } }); return true; }
      return false;
    } });
    await expect(page.getByText('Release prepared.', { exact: false })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeDisabled();
  });
}

for (const invalid of [{ payload_sha256: '' }, { payload_sha256: null }, { to: null }, { sequence: 0 }, { expires_at: 'tomorrow' }]) {
  test(`invalid candidate ${JSON.stringify(invalid)} fails closed without a render crash`, async (t) => {
    const page = await openPage(t, 'en', 390, false, { handler: async (route, path) => {
      if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
      if (path === 'candidate') { await route.fulfill({ json: { ...prepared('invalid-release'), ...invalid } }); return true; }
      return false;
    } });
    await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeDisabled();
    await expect(page.getByRole('main').getByText('invalid-release', { exact: true })).toHaveCount(0);
  });
}

for (const changed of ['d'.repeat(64), undefined, 'invalid']) {
  test(`changed or removed payload hash (${changed ?? 'absent'}) prevents apply`, async (t) => {
    let reads = 0;
    let posts = 0;
    const page = await openPage(t, 'en', 1440, false, { handler: async (route, path) => {
      if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
      if (path === 'candidate') {
        reads += 1;
        await route.fulfill({ json: { ...prepared('same-release'), ...(reads <= 2 ? { payload_sha256: 'c'.repeat(64) } : changed === undefined ? {} : { payload_sha256: changed }) } });
        return true;
      }
      if (path === 'apply') { posts += 1; await route.fulfill({ status: 202, json: operation('accepted') }); return true; }
      return false;
    } });
    await page.getByRole('button', { name: 'Review maintenance update' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Admin password', { exact: true }).fill('owner-password');
    await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
    await dialog.getByRole('checkbox').check();
    await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText(changed === 'invalid' ? 'The displayed candidate has expired.' : 'The candidate changed or disappeared while being checked.', { exact: false })).toBeVisible();
    assert.equal(posts, 0);
  });
}

test('invalid repositories do not send discovery requests', async (t) => {
  const page = await openPage(t);
  const requests = [];
  await page.route('**/api/**', (route) => {
    requests.push(route.request().url());
    return route.fulfill({ json: release });
  });
  for (const value of ['', 'https://github.com/example/proxima', 'owner/../repo', 'owner/..']) {
    await page.getByPlaceholder('owner/repo').fill(value);
    await page.getByRole('button', { name: 'Check latest release' }).click();
    await expect(page.getByPlaceholder('owner/repo')).toHaveAttribute('aria-invalid', 'true');
  }
  assert.deepEqual(requests, []);
});

test('discovery uses admin auth and an encoded repository, renders metadata, and clears stale results', async (t) => {
  const page = await openPage(t);
  let complete;
  const pending = new Promise((resolve) => { complete = resolve; });
  await page.route('**/api/v1/admin/system-updates/latest?**', async (route) => {
    const request = route.request();
    assert.equal(request.method(), 'GET');
    assert.equal(request.headers().authorization, 'Bearer test-admin-token');
    assert.equal(new URL(request.url()).search, '?owner=example&repo=proxima');
    await pending;
    await route.fulfill({ json: release });
  });
  await page.getByPlaceholder('owner/repo').fill(' example/proxima ');
  const requested = page.waitForRequest('**/api/v1/admin/system-updates/latest?**');
  await page.getByRole('button', { name: 'Check latest release' }).click();
  await requested;
  await expect(page.getByPlaceholder('owner/repo')).toBeDisabled();
  await page.screenshot({ path: join(evidence, 'loading-desktop.png'), fullPage: true });
  complete();
  await expect(page.getByText(release.tag, { exact: true })).toBeVisible();
  await expect(page.getByText(release.name, { exact: true })).toBeVisible();
  await expect(page.getByRole('link')).toHaveAttribute('href', release.html_url);
  await page.getByPlaceholder('owner/repo').fill('example/other');
  await expect(page.getByRole('link')).toHaveCount(0);
  await expect(page.getByText(release.tag, { exact: true })).toHaveCount(0);
});

test('API failure can be retried without losing repository input', async (t) => {
  const page = await openPage(t);
  // An HTTP error is expected in this scenario, not a failed network request.
  page.removeAllListeners('console');
  let attempts = 0;
  await page.route('**/api/v1/admin/system-updates/latest?**', (route) => {
    attempts += 1;
    return attempts === 1
      ? route.fulfill({ status: 503, json: { error: 'controller unavailable' } })
      : route.fulfill({ json: release });
  });
  await page.getByPlaceholder('owner/repo').fill('example/proxima');
  const failed = page.waitForResponse((response) => response.status() === 503);
  await page.getByRole('button', { name: 'Check latest release' }).click();
  await failed;
  await expect(page.getByPlaceholder('owner/repo')).toBeEnabled();
  await expect(page.getByPlaceholder('owner/repo')).toHaveValue('example/proxima');
  await page.screenshot({ path: join(evidence, 'error-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Check latest release' }).click();
  await expect(page.getByRole('link')).toHaveAttribute('href', release.html_url);
});

test('non-GitHub release URLs are not actionable', async (t) => {
  const page = await openPage(t);
  await page.route('**/api/v1/admin/system-updates/latest?**', (route) => route.fulfill({
    json: { ...release, html_url: 'javascript:alert(1)' },
  }));
  await page.getByPlaceholder('owner/repo').fill('example/proxima');
  await page.getByRole('button', { name: 'Check latest release' }).click();
  await expect(page.getByText(release.tag, { exact: true })).toBeVisible();
  await expect(page.getByRole('link')).toHaveCount(0);
});

test('watch loads actual build metadata and persists the selected configuration with admin auth', async (t) => {
  const page = await openPage(t);
  await expect(page.getByLabel('Watched repository', { exact: true })).toHaveValue('example/proxima');
  await expect(page.getByText('abc123', { exact: true })).toBeVisible();
  await expect(page.getByText('v2.3.0', { exact: true })).toBeVisible();
  let saved;
  await page.route('**/api/v1/admin/system-updates/watch', async (route) => {
    assert.equal(route.request().method(), 'PUT');
    assert.equal(route.request().headers().authorization, 'Bearer test-admin-token');
    saved = route.request().postDataJSON();
    await route.fulfill({ json: { ...watchState, config: saved, running: false, comparison: 'current', update_available: false } });
  });
  await page.getByLabel('Watched repository', { exact: true }).fill('owner/other');
  await page.getByRole('checkbox', { name: 'Enable automatic watch' }).uncheck();
  const savedResponse = page.waitForResponse((response) => response.request().method() === 'PUT');
  await page.getByRole('button', { name: 'Save watch settings' }).click();
  await savedResponse;
  assert.deepEqual(saved, { owner: 'owner', repo: 'other', enabled: false });
  await expect(page.getByText('Up to date', { exact: true })).toBeVisible();
  await expect(page.getByRole('checkbox', { name: 'Enable automatic watch' })).not.toBeChecked();
});

for (const capability of ['watch', 'status', 'preflight', 'candidate']) {
  test(`absent ${capability} API does not fabricate capability or editable state`, async (t) => {
    const page = await openPage(t, 'en', 390, false, { expectedFailure: true, handler: async (route, path) => {
      if (path !== capability) return false;
      await route.fulfill({ status: 404, json: { error: 'not_found' } });
      return true;
    } });
    if (capability === 'watch') {
      await expect(page.getByLabel('Watched repository', { exact: true })).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'Save watch settings' })).toHaveCount(0);
    } else {
      if (capability === 'candidate') {
        await expect(page.getByText('No prepared signed update is available. Refresh after host root approves a release.', { exact: true })).toBeVisible();
      }
      await expect(page.getByLabel('Signed release ID to apply', { exact: true })).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'Review maintenance update' })).toHaveCount(0);
      if (capability === 'status') await expect(page.getByText('No recorded host operation', { exact: true })).toHaveCount(0);
    }
    await page.screenshot({ path: join(evidence, `unavailable-${capability}.png`), fullPage: true });
  });
}

for (const width of [1440, 390]) {
  test(`signed candidate supplies the exact release ID after credentials and maintenance consent at ${width}px`, async (t) => {
    let posts = 0;
    let statusRequests = 0;
    let candidateRequests = 0;
    let current = hostState;
    const page = await openPage(t, 'en', width, false, { handler: async (route, path) => {
      if (path === 'candidate') {
        candidateRequests += 1;
        await route.fulfill({ json: prepared('root-approved-next') });
        return true;
      }
      if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
      if (path === 'status') { statusRequests += 1; await route.fulfill({ json: current }); return true; }
      if (path === 'apply') {
        posts += 1;
        assert.equal(route.request().headers().authorization, 'Bearer test-admin-token');
        assert.deepEqual(route.request().postDataJSON(), { release_id: 'root-approved-next', maintenance: true, password: 'test-password', totp_code: '123456' });
        current = operation('applying');
        await route.fulfill({ status: 202, json: operation('accepted') });
        return true;
      }
      return false;
    } });
    const review = page.getByRole('button', { name: 'Review maintenance update' });
    await expect(page.getByRole('main').getByText('root-approved-next', { exact: true })).toBeVisible();
    await expect(page.getByText(images('b').api, { exact: true }).first()).toBeVisible();
    await expect(page.getByLabel('Signed release ID to apply', { exact: true })).toHaveCount(0);
    await review.click();
    const dialog = page.getByRole('dialog');
    const confirm = dialog.getByRole('button', { name: 'Acknowledge downtime and request update' });
    await expect(confirm).toBeDisabled();
    await dialog.getByLabel('Admin password', { exact: true }).fill('must-be-cleared');
    await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('654321');
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await review.click();
    await expect(dialog.getByLabel('Admin password', { exact: true })).toHaveValue('');
    await expect(dialog.getByLabel('TOTP authentication code', { exact: true })).toHaveValue('');
    await dialog.getByLabel('Admin password', { exact: true }).fill('test-password');
    await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
    await expect(confirm).toBeDisabled();
    await dialog.getByRole('checkbox').check();
    await expect(confirm).toBeEnabled();
    await page.screenshot({ path: join(evidence, `candidate-confirm-${width}.png`), fullPage: true });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const accepted = page.waitForResponse((response) => response.request().method() === 'POST');
    await confirm.click();
    await accepted;
    await expect(dialog).toBeHidden();
    await expect(page.getByText('Request accepted (202)', { exact: false })).toBeVisible();
    await expect(page.getByText('Update completed', { exact: true })).toHaveCount(0);
    await expect(review).toBeDisabled();
    const refreshed = page.waitForResponse('**/api/v1/admin/system-updates/status');
    await page.getByRole('button', { name: 'Refresh host status' }).click();
    await refreshed;
    await expect(page.getByText('Applying update', { exact: true })).toBeVisible();
    await expect(page.getByText(images('b').api, { exact: true }).first()).toBeVisible();
    assert.equal(posts, 1);
    assert.equal(statusRequests, 2, 'Only initial load and explicit refresh read host status');
    assert.equal(candidateRequests, 5, 'Initial load, both dialog openings, submission, and explicit host refresh each check the current candidate');
  });
}

test('candidate change between review and submission clears credentials and never sends apply', async (t) => {
  let posts = 0;
  let candidateCalls = 0;
  const page = await openPage(t, 'en', 1440, false, { handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'candidate') {
      candidateCalls += 1;
      await route.fulfill({ json: candidateCalls === 1 ? prepared('approved-old') : prepared('approved-new') });
      return true;
    }
    if (path === 'apply') {
      posts += 1;
      await route.fulfill({ status: 202, json: operation('accepted') });
      return true;
    }
    return false;
  } });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  await expect(page.getByRole('dialog')).toBeHidden();
  await expect(page.getByText('The candidate changed or disappeared while being checked.', { exact: false })).toBeVisible();
  await expect(page.getByRole('main').getByText('approved-new', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Admin password', { exact: true }).fill('test-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  await expect(dialog.getByLabel('Admin password', { exact: true })).toHaveValue('');
  await expect(dialog.getByLabel('TOTP authentication code', { exact: true })).toHaveValue('');
  assert.equal(posts, 0);
});

test('expired candidate is no longer selectable and apply is never attempted', async (t) => {
  let posts = 0;
  const page = await openPage(t, 'en', 390, false, { handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'candidate') { await route.fulfill({ json: prepared('expired-next', 0) }); return true; }
    if (path === 'apply') {
      posts += 1;
      await route.fulfill({ status: 202, json: operation('accepted') });
      return true;
    }
    return false;
  } });
  await expect(page.getByRole('button', { name: 'Review maintenance update' })).toHaveCount(0);
  await expect(page.getByText('The displayed candidate has expired.', { exact: false })).toBeVisible();
  await page.screenshot({ path: join(evidence, 'candidate-expired-mobile.png'), fullPage: true });
  assert.equal(posts, 0);
});

test('final candidate refresh failure preserves credentials requirement without sending apply', async (t) => {
  let posts = 0;
  let candidateRequests = 0;
  const page = await openPage(t, 'en', 1440, false, { expectedFailure: true, handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'candidate') {
      candidateRequests += 1;
      await route.fulfill(candidateRequests <= 2
        ? { json: prepared('approved-next') }
        : { status: 503, json: { error: 'updater_unavailable' } });
      return true;
    }
    if (path === 'apply') {
      posts += 1;
      await route.fulfill({ status: 202, json: operation('accepted') });
      return true;
    }
    return false;
  } });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Admin password', { exact: true }).fill('test-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
  await dialog.getByRole('checkbox').check();
  const failed = page.waitForResponse((response) => response.url().includes('/candidate') && response.status() === 503);
  await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
  await failed;
  await expect(dialog).toBeHidden();
  await expect(page.getByText('The displayed candidate has expired.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Review maintenance update' })).toBeDisabled();
  assert.equal(posts, 0);
});

test('previous operation is not the next approval and blocked preflight never offers apply', async (t) => {
  const page = await openPage(t, 'en', 1440, false, { handler: async (route, path) => {
    if (path !== 'status') return false;
    await route.fulfill({ json: operation('succeeded') });
    return true;
  } });
  await expect(page.getByText('Update completed', { exact: true })).toBeVisible();
  await expect(page.getByText(images('a').web, { exact: true })).toBeVisible();
  await expect(page.getByLabel('Signed release ID to apply', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Review maintenance update' })).toHaveCount(0);
  await expect(page.getByRole('main').getByText('root-approved-next', { exact: true })).toHaveCount(0);
});

test('rejected reauthentication keeps the admin session and permits corrected credentials', async (t) => {
  let posts = 0;
  let reads = 0;
  const page = await openPage(t, 'en', 390, false, { expectedFailure: true, handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'status') { reads += 1; await route.fulfill({ json: hostState }); return true; }
    if (path === 'apply') {
      posts += 1;
      await route.fulfill(posts === 1
        ? { status: 403, json: { error: 'reauthentication_failed' } }
        : { status: 202, json: operation('accepted') });
      return true;
    }
    return false;
  } });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Admin password', { exact: true }).fill('wrong-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
  await dialog.getByRole('checkbox').check();
  const rejected = page.waitForResponse((response) => response.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
  assert.equal((await rejected).status(), 403);
  await expect(dialog).toBeHidden();
  await expect(page.getByText('Reauthentication failed.', { exact: false })).toBeVisible();
  await expect(page.getByText('Update outcome unknown.', { exact: false })).toHaveCount(0);
  assert.equal(await page.evaluate(() => localStorage.getItem('proxima_admin_token')), 'test-admin-token');
  assert.equal(new URL(page.url()).pathname, '/__server-updates');
  assert.equal(posts, 1);
  assert.equal(reads, 1);
  await page.screenshot({ path: join(evidence, 'reauth-rejected-mobile.png'), fullPage: true });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  await expect(dialog.getByLabel('Admin password', { exact: true })).toHaveValue('');
  await expect(dialog.getByLabel('TOTP authentication code', { exact: true })).toHaveValue('');
  await expect(dialog.getByRole('checkbox')).not.toBeChecked();
  await dialog.getByLabel('Admin password', { exact: true }).fill('correct-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('654321');
  await dialog.getByRole('checkbox').check();
  const accepted = page.waitForResponse((response) => response.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
  assert.equal((await accepted).status(), 202);
  await expect(page.getByText('Request accepted (202)', { exact: false })).toBeVisible();
  assert.equal(posts, 2, 'Only the explicit corrected submission may send another apply');
  assert.equal(reads, 1);
});

test('expired admin session still redirects on apply 401', async (t) => {
  const page = await openPage(t, 'en', 1440, false, { expectedFailure: true, handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'apply') { await route.fulfill({ status: 401, json: { error: 'admin_required' } }); return true; }
    return false;
  } });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Admin password', { exact: true }).fill('test-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
  await dialog.getByRole('checkbox').check();
  const redirected = page.waitForURL('**/admin/login');
  await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
  await redirected;
  assert.equal(await page.evaluate(() => localStorage.getItem('proxima_admin_token')), null);
});

test('uncertain apply and failed status refresh discard stale success without repeating apply', async (t) => {
  let posts = 0;
  let reads = 0;
  const page = await openPage(t, 'en', 390, true, { expectedFailure: true, handler: async (route, path) => {
    if (path === 'preflight') { await route.fulfill({ json: { ok: true, reason: '' } }); return true; }
    if (path === 'status') {
      reads += 1;
      await route.fulfill(reads === 1 ? { json: operation('succeeded') } : { status: 503, json: { error: 'updater_unavailable' } });
      return true;
    }
    if (path === 'apply') {
      posts += 1;
      await route.fulfill({ status: 503, json: { outcome: 'unknown', check_status: true, error: 'updater_unavailable' } });
      return true;
    }
    return false;
  } });
  await page.getByRole('button', { name: 'Review maintenance update' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Admin password', { exact: true }).fill('test-password');
  await dialog.getByLabel('TOTP authentication code', { exact: true }).fill('123456');
  await dialog.getByRole('checkbox').check();
  const failed = page.waitForResponse((response) => response.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Acknowledge downtime and request update' }).click();
  await failed;
  await expect(dialog).toBeHidden();
  await expect(page.getByText('Update outcome unknown.', { exact: false })).toBeVisible();
  await expect(page.getByText('Reauthentication failed.', { exact: false })).toHaveCount(0);
  await expect(page.getByText('Update completed', { exact: true })).toHaveCount(0);
  const refresh = page.waitForResponse('**/api/v1/admin/system-updates/status');
  await page.getByRole('button', { name: 'Refresh host status' }).click();
  await refresh;
  await expect(page.getByText('Update completed', { exact: true })).toHaveCount(0);
  await expect(page.getByText('previous-release', { exact: true })).toHaveCount(0);
  assert.equal(posts, 1);
  assert.equal(reads, 2);
  await page.screenshot({ path: join(evidence, 'unknown-after-apply-mobile.png'), fullPage: true });
});

for (const language of ['ko', 'en', 'zh']) {
  for (const width of [1440, 390]) {
    for (const dark of [false, true]) {
      test(`release screen reflows in ${language} at ${width}px in ${dark ? 'dark' : 'light'} mode`, async (t) => {
        const page = await openPage(t, language, width, dark);
        await page.route('**/api/v1/admin/system-updates/latest?**', (route) => route.fulfill({ json: release }));
        const suffix = `${language}-${width}-${dark ? 'dark' : 'light'}`;
        await page.screenshot({ path: join(evidence, `empty-${suffix}.png`), fullPage: true });
        await page.getByPlaceholder('owner/repo').fill('example/proxima');
        const discoverButton = page.getByRole('button', { name: /^(Check latest release|최신 릴리스 조회|查询最新版本)$/ });
        await discoverButton.click();
        await expect(page.getByRole('link')).toHaveAttribute('href', release.html_url);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
        await page.screenshot({ path: join(evidence, `release-${suffix}.png`), fullPage: true });
        await page.getByPlaceholder('owner/repo').focus();
        await page.keyboard.press('Tab');
        await expect(discoverButton).toBeFocused();
        await page.screenshot({ path: join(evidence, `focus-${suffix}.png`), fullPage: true });
      });
    }
  }
}

for (const [language, watchLabel, hostLabel] of [
  ['ko', '릴리스를 조회하지 못했습니다. 연결과 API 서버를 확인하고 다시 조회하세요.', '업데이트 적용 실패'],
  ['en', 'Could not check the release. Check your connection and API server, then try again.', 'Update failed'],
  ['zh', '无法查询版本。请检查网络连接和 API 服务器后重试。', '更新失败'],
]) {
  test(`watch and host errors display localized explanations in ${language}`, async (t) => {
    const page = await openPage(t, language, 390, false, { handler: async (route, path) => {
      if (path === 'watch') {
        await route.fulfill({ json: { ...watchState, error: 'release_unavailable' } });
        return true;
      }
      if (path === 'status') {
        await route.fulfill({ json: operation('manual_recovery') });
        return true;
      }
      return false;
    } });
    await expect(page.getByText(watchLabel, { exact: true })).toBeVisible();
    await expect(page.getByText(hostLabel, { exact: true })).toBeVisible();
    await expect(page.getByText('release_unavailable', { exact: true })).toHaveCount(0);
    await expect(page.getByText('apply_failed', { exact: true })).toHaveCount(0);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    await page.screenshot({ path: join(evidence, `localized-errors-${language}-390.png`), fullPage: true });
  });
}

for (const width of [390, 1440, 1920]) {
  for (const dark of [false, true]) {
    test(`Korean QA states and reauthentication remain usable at ${width}px ${dark ? 'dark' : 'light'}`, async (t) => {
      let comparison = 'ahead';
      let reason = 'topology_mismatch';
      let phase = 'manual_recovery';
      let ready = false;
      let posts = 0;
      const page = await openPage(t, 'ko', width, dark, { expectedFailure: true, handler: async (route, path) => {
        if (path === 'watch') { await route.fulfill({ json: { ...watchState, comparison } }); return true; }
        if (path === 'status') { await route.fulfill({ json: operation(phase) }); return true; }
        if (path === 'preflight') { await route.fulfill({ json: { ok: ready, reason } }); return true; }
        if (path === 'apply') { posts += 1; await route.fulfill({ status: 403, json: { error: 'reauthentication_failed' } }); return true; }
        if (path === 'latest') { await route.fulfill({ json: release }); return true; }
        return false;
      } });
      const suffix = `qa-ko-${width}-${dark ? 'dark' : 'light'}`;
      await expect(page.getByText('공개 릴리스보다 최신 빌드', { exact: true })).toBeVisible();
      await expect(page.getByText('수동 복구 필요', { exact: true })).toBeVisible();
      await expect(page.getByText('호스트 사전 점검: 적용 불가: 호스트 구성이 승인된 구성과 다릅니다.', { exact: true })).toBeVisible();
      await page.getByPlaceholder('owner/repo').fill('example/proxima');
      await page.getByRole('button', { name: '최신 릴리스 조회', exact: true }).click();
      await expect(page.getByRole('link')).toHaveAttribute('href', release.html_url);
      const build = page.getByText('v2.3.0', { exact: true });
      const repository = page.getByText('example/proxima', { exact: true }).first();
      const buildBox = await build.boundingBox();
      const repoBox = await repository.boundingBox();
      if (width === 390) {
        assert.equal(buildBox.x, repoBox.x, 'Mobile build values share a single column');
        assert.ok(repoBox.y > buildBox.y, 'Mobile repository follows build version');
      } else {
        assert.ok(repoBox.x > buildBox.x, 'Desktop retains two columns');
      }
      assert.equal(await build.evaluate((element) => getComputedStyle(element).wordBreak), 'keep-all');
      assert.equal(await page.getByText('호스트 사전 점검: 적용 불가: 호스트 구성이 승인된 구성과 다릅니다.', { exact: true }).evaluate((element) => getComputedStyle(element).wordBreak), 'keep-all');
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.screenshot({ path: join(evidence, `${suffix}-operation.png`), fullPage: true });
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
      await page.screenshot({ path: join(evidence, `${suffix}-end.png`) });
      for (const [value, label] of [['unknown', '비교할 수 없음'], ['current', '최신 버전'], ['update_available', '새 버전 있음'], ['future_state', '미확인']]) {
        comparison = value;
        await page.getByRole('button', { name: '감시 상태 새로고침', exact: true }).click();
        await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
        await expect(page.getByText(value, { exact: true })).toHaveCount(0);
      }
      for (const [value, label] of [['busy', '다른 작업이 진행 중입니다.'], ['manual_recovery', '호스트에서 직접 복구해야 합니다.'], ['not_approved', '유효한 호스트 사전 승인이 없습니다.'], ['unavailable', '호스트 실행 환경을 확인할 수 없습니다.'], ['future_reason', '미확인']]) {
        reason = value;
        await page.getByRole('button', { name: '호스트 상태 새로고침', exact: true }).click();
        await expect(page.getByText(`호스트 사전 점검: 적용 불가: ${label}`, { exact: true })).toBeVisible();
        await expect(page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true })).toHaveCount(0);
      }
      ready = true;
      phase = 'succeeded';
      await page.getByRole('button', { name: '호스트 상태 새로고침', exact: true }).click();
      await page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true }).click();
      const dialog = page.getByRole('dialog');
      await dialog.getByLabel('관리자 비밀번호', { exact: true }).fill('test-password');
      await dialog.getByLabel('TOTP 인증 코드', { exact: true }).fill('123456');
      await dialog.getByRole('checkbox').check();
      const confirm = dialog.getByRole('button', { name: '중단에 동의하고 적용 요청', exact: true });
      await expect(confirm).toBeEnabled();
      if (width === 390) {
        assert.ok((await confirm.boundingBox()).height >= 44, 'Mobile confirmation has a touch-sized target');
        assert.ok((await dialog.getByLabel('관리자 비밀번호', { exact: true }).boundingBox()).height >= 44);
      }
      await page.screenshot({ path: join(evidence, `${suffix}-confirm.png`) });
      const rejected = page.waitForResponse((response) => response.request().method() === 'POST');
      await confirm.click();
      assert.equal((await rejected).status(), 403);
      await expect(dialog).toBeHidden();
      await expect(page.getByText('재인증에 실패했습니다.', { exact: false })).toBeVisible();
      assert.equal(await page.getByText('재인증에 실패했습니다.', { exact: false }).evaluate((element) => getComputedStyle(element).wordBreak), 'keep-all');
      assert.equal(posts, 1, '403 never automatically resends an update');
      assert.equal(await page.evaluate(() => localStorage.getItem('proxima_admin_token')), 'test-admin-token');
      await page.screenshot({ path: join(evidence, `${suffix}-reauth.png`), fullPage: true });
      await page.getByRole('button', { name: '유지보수 업데이트 확인', exact: true }).click();
      await expect(dialog.getByLabel('관리자 비밀번호', { exact: true })).toHaveValue('');
      await expect(dialog.getByLabel('TOTP 인증 코드', { exact: true })).toHaveValue('');
      await expect(dialog.getByRole('checkbox')).not.toBeChecked();
      await page.keyboard.press('Escape');
    });
  }
}
