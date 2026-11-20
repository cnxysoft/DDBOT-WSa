const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');

function fixture() {
  const nodes = new Map();
  const requests = [];
  const intervals = new Map();
  let nextID = 0;
  function node(selector) {
    if (!nodes.has(selector)) {
      const classes = new Set(['hidden']);
      nodes.set(selector, {
        style: {}, dataset: {}, value: '', innerHTML: '', textContent: '', focus() {},
        classList: {
          add: (name) => classes.add(name), remove: (name) => classes.delete(name),
          contains: (name) => classes.has(name),
          toggle: (name, on) => on ? classes.add(name) : classes.delete(name),
        },
      });
    }
    return nodes.get(selector);
  }
  const context = vm.createContext({
    AbortController, TextDecoder,
    localStorage: { getItem: () => 'admin-token', setItem() {}, removeItem() {} },
    document: { querySelector: node, querySelectorAll: () => [], addEventListener() {} },
    matchMedia: () => ({ addEventListener() {} }),
    setInterval: (callback) => { const id = ++nextID; intervals.set(id, callback); return id; },
    clearInterval: (id) => intervals.delete(id), setTimeout: () => ++nextID, clearTimeout() {},
    fetch: (url, options) => new Promise((resolve) => requests.push({ url, options, resolve })),
  });
  context.window = context;
  vm.runInContext(source, context, { filename: 'app.js' });
  const run = (code) => vm.runInContext(code, context);
  const qr = (sid, site = 'bilibili') => ({
    status: 200, ok: true, headers: new Headers(),
    json: async () => ({ session: sid, platform: site, image: 'data:image/png;base64,AQ==' }),
  });
  const cancellations = () => requests.filter((request) => request.url.endsWith('/cancel'))
    .map((request) => JSON.parse(request.options.body).session);
  return { context, node, requests, intervals, run, qr, cancellations };
}

test('closing while a QR is being created cancels its late session without polling', async () => {
  const f = fixture();
  const initialIntervals = f.intervals.size;
  const pending = f.context.startPlatformLogin('bilibili');
  f.context.closeLoginModal();
  f.requests[0].resolve(f.qr('late'));
  await pending;
  assert.equal(f.node('#login-modal').classList.contains('hidden'), true);
  assert.equal(f.run('loginSession'), null);
  assert.equal(f.intervals.size, initialIntervals);
  assert.deepEqual(f.cancellations(), ['late']);
});

test('a superseded creation response is cancelled without replacing the newer QR', async () => {
  const f = fixture();
  const first = f.context.startPlatformLogin('bilibili');
  const second = f.context.startPlatformLogin('weibo');
  f.requests[1].resolve(f.qr('current', 'weibo'));
  await second;
  const currentTimer = f.run('loginTimer');
  f.requests[0].resolve(f.qr('obsolete'));
  await first;
  assert.equal(f.run('loginSession'), 'current');
  assert.equal(f.run('loginTimer'), currentTimer);
  assert.equal(f.node('#login-modal').classList.contains('hidden'), false);
  assert.deepEqual(f.cancellations(), ['obsolete']);
});

test('refreshing a displayed QR cancels the old server session', async () => {
  const f = fixture();
  const first = f.context.startPlatformLogin('bilibili');
  f.requests[0].resolve(f.qr('first'));
  await first;
  const oldTimer = f.run('loginTimer');
  const second = f.context.startPlatformLogin('bilibili');
  assert.deepEqual(f.cancellations(), ['first']);
  assert.equal(f.intervals.has(oldTimer), false);
  f.requests.find((request, index) => index > 0 && request.url === '/api/v1/login/qrcode').resolve(f.qr('second'));
  await second;
  assert.equal(f.run('loginSession'), 'second');
});

test('closing a QR invalidates its pending successful status response', async () => {
  const f = fixture();
  const created = f.context.startPlatformLogin('bilibili');
  f.requests[0].resolve(f.qr('first'));
  await created;
  let successEffects = 0;
  f.context.toast = f.context.loadConfig = f.context.loadPlatformAccount = () => ++successEffects;
  const pending = f.context.pollLoginStatus('first');
  const statusRequest = f.requests.find((request) => request.url.includes('/status?'));
  f.context.closeLoginModal();
  statusRequest.resolve({
    status: 200, ok: true, headers: new Headers(),
    json: async () => ({ state: 'confirmed', platform: 'bilibili' }),
  });
  await pending;
  assert.equal(successEffects, 0);
  assert.equal(f.run('loginSession'), null);
  assert.equal(f.node('#login-modal').classList.contains('hidden'), true);
});

test('authentication expiry closes the QR, cancels its server session and stops polling', async () => {
  const f = fixture();
  const initialIntervals = f.intervals.size;
  const created = f.context.startPlatformLogin('bilibili');
  f.requests[0].resolve(f.qr('first'));
  await created;
  const pending = f.context.api('/api/v1/config/status');
  f.requests.find((request) => request.url === '/api/v1/config/status').resolve({
    status: 403, headers: new Headers({ 'X-DDBOT-Auth': 'invalid-token' }),
  });
  await assert.rejects(pending, /登录已失效/);
  assert.equal(f.intervals.size, initialIntervals);
  assert.equal(f.run('loginSession'), null);
  assert.equal(f.node('#login-modal').classList.contains('hidden'), true);
  assert.deepEqual(f.cancellations(), ['first']);
});

test('late QR creation cleanup retains its original credentials after another login', async () => {
  const f = fixture();
  const pending = f.context.startPlatformLogin('bilibili');
  f.run('setAuthToken("new-admin-token")');
  f.requests[0].resolve(f.qr('obsolete'));
  await pending;
  const cancel = f.requests.find((request) => request.url.endsWith('/cancel'));
  assert.equal(cancel.options.headers.Authorization, 'Bearer admin-token');
  cancel.resolve({ status: 403, headers: new Headers({ 'X-DDBOT-Auth': 'invalid-token' }) });
  await Promise.resolve(); await Promise.resolve();
  assert.equal(f.run('token'), 'new-admin-token');
});
