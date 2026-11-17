const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');
const pushes = fs.readFileSync(path.join(__dirname, '../web/js/pushes.js'), 'utf8');

function fixture() {
  const nodes = new Map();
  const storage = new Map([['ddbot_admin_token', 'old-token']]);
  const requests = [];
  const timers = new Map();
  let timerID = 0;
  function node(selector) {
    if (!nodes.has(selector)) {
      const classes = new Set(selector === '#login-overlay' ? ['hidden'] : []);
      nodes.set(selector, {
        value: '', style: {}, dataset: {}, options: [], innerHTML: '', textContent: '',
        addEventListener() {}, appendChild() {}, focus() {},
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
    localStorage: {
      getItem: (key) => storage.get(key) || null,
      setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key),
    },
    document: {
      querySelector: node, querySelectorAll: () => [], addEventListener() {}, createElement: () => node({}),
    },
    matchMedia: () => ({ addEventListener() {} }), addEventListener() {},
    setTimeout: (callback, delay) => { const id = ++timerID; timers.set(id, { callback, delay }); return id; },
    clearTimeout: (id) => timers.delete(id),
    setInterval: (callback, delay) => { const id = ++timerID; timers.set(id, { callback, delay }); return id; },
    clearInterval: (id) => timers.delete(id),
    fetch: (url, options) => new Promise((resolve, reject) => requests.push({ url, options, resolve, reject })),
  });
  context.window = context;
  vm.runInContext(source, context, { filename: 'app.js' });
  vm.runInContext(pushes, context, { filename: 'pushes.js' });
  const run = (code) => vm.runInContext(code, context);
  return { context, storage, requests, timers, node, run };
}

function response(status, marker = '') {
  return { status, ok: status === 200, headers: new Headers({ 'X-DDBOT-Auth': marker }) };
}

test('marked invalid-token 403 reopens authentication and revokes the saved token', async () => {
  const f = fixture();
  const pending = f.context.api('/api/v1/config');
  f.requests[0].resolve(response(403, 'invalid-token'));
  await assert.rejects(pending, /登录已失效/);
  assert.equal(f.node('#login-overlay').classList.contains('hidden'), false);
  assert.equal(f.run('token'), '');
  assert.equal(f.storage.has('ddbot_admin_token'), false);
});

test('business 403 preserves the current login', async () => {
  const f = fixture();
  const pending = f.context.api('/api/v1/config');
  f.requests[0].resolve(response(403));
  await assert.rejects(pending, /无权限/);
  assert.equal(f.run('token'), 'old-token');
  assert.equal(f.node('#login-overlay').classList.contains('hidden'), true);
});

for (const status of [401, 403]) {
  test(`a late ${status} cannot revoke a newer login, including reuse of the same token`, async () => {
    const f = fixture();
    const pending = f.context.api('/api/v1/config');
    f.run('setAuthToken("old-token")');
    f.requests[0].resolve(response(status, status === 403 ? 'invalid-token' : ''));
    await assert.rejects(pending, /登录已失效/);
    assert.equal(f.run('token'), 'old-token');
    assert.equal(f.storage.get('ddbot_admin_token'), 'old-token');
    assert.equal(f.node('#login-overlay').classList.contains('hidden'), true);
  });
}

test('a login response received after logout cannot authenticate the page', async () => {
  const f = fixture();
  f.node('#login-token').value = 'candidate';
  let boots = 0;
  f.context.boot = () => ++boots;
  const pending = f.context.doLogin({ preventDefault() {} });
  f.context.showLogin();
  f.requests[0].resolve(response(200));
  await pending;
  assert.equal(f.run('token'), '');
  assert.equal(boots, 0);
  assert.equal(f.node('#login-overlay').classList.contains('hidden'), false);
});

test('the older of two login attempts cannot replace the newer credentials', async () => {
  const f = fixture();
  f.context.boot = () => {};
  f.node('#login-token').value = 'first';
  const first = f.context.doLogin({ preventDefault() {} });
  f.node('#login-token').value = 'second';
  const second = f.context.doLogin({ preventDefault() {} });
  f.requests[1].resolve(response(200));
  await second;
  f.requests[0].resolve(response(200));
  await first;
  assert.equal(f.run('token'), 'second');
});

test('a pending login fade cannot hide an authentication failure', () => {
  const f = fixture();
  f.context.hideLogin();
  const fade = [...f.timers.values()].find((timer) => timer.delay === 250).callback;
  f.context.showLogin();
  fade(); // A timer callback already queued by the browser can still run after clearTimeout.
  assert.equal(f.node('#login-overlay').classList.contains('hidden'), false);
});

test('background API work after logout does not invalidate an ongoing new login', async () => {
  const f = fixture();
  f.context.showLogin();
  f.context.boot = () => {};
  f.node('#login-token').value = 'new-token';
  const login = f.context.doLogin({ preventDefault() {} });
  await assert.rejects(f.context.api('/api/v1/config/status'), /请先登录/);
  assert.equal(f.requests.length, 1, 'an empty-token background query must not reach the server');
  f.requests[0].resolve(response(200));
  await login;
  assert.equal(f.run('token'), 'new-token');
});

for (const stream of ['logs', 'pushes']) {
  test(`${stream} stream authentication failure uses the same marked-403 recovery`, async () => {
    const f = fixture();
    let pending;
    if (stream === 'logs') pending = f.context.connectLogs();
    else f.context.PushPage.enter();
    const request = f.requests.find((request) => request.url === `/api/v1/${stream}/stream`);
    request.resolve(response(403, 'invalid-token'));
    if (pending) await pending;
    else await new Promise(setImmediate);
    assert.equal(f.run('token'), '');
    assert.equal(f.node('#login-overlay').classList.contains('hidden'), false);
    assert.equal([...f.timers.values()].filter((timer) => timer.delay === 3000).length, 0);
  });
}

test('logout cancels a log reader and its late frame cannot reach the new session', async () => {
  const f = fixture();
  let resolveRead;
  let cancelled = 0;
  const pending = f.context.connectLogs();
  f.requests[0].resolve({
    ...response(200), body: {
      getReader: () => ({
        read: () => new Promise((resolve) => { resolveRead = resolve; }),
        cancel: async () => { ++cancelled; },
      }),
    },
  });
  await new Promise(setImmediate);
  f.run('setAuthToken("new-token")');
  resolveRead({ done: false, value: new TextEncoder().encode('data: {"message":"stale","level":"info"}\n\n') });
  await pending;
  assert.equal(cancelled, 1);
  assert.equal(f.run('logBuffer.length'), 0);
  assert.equal([...f.timers.values()].filter((timer) => timer.delay === 3000).length, 0);
});

test('an old scheduled reconnect cannot open a stream under a new login', async () => {
  const f = fixture();
  f.node('#page-logs').classList.add('active');
  const pending = f.context.connectLogs();
  f.requests[0].reject(new Error('offline'));
  await pending;
  const retry = [...f.timers.values()].find((timer) => timer.delay === 3000).callback;
  f.run('setAuthToken("new-token")');
  retry();
  assert.equal(f.requests.length, 1);
});

test('concurrent connects share a pending log connection', () => {
  const f = fixture();
  f.context.connectLogs();
  f.context.connectLogs();
  assert.equal(f.requests.length, 1);
});

test('a configuration body completed under a new login cannot replace the new form cache', async () => {
  const f = fixture();
  let resolveBody;
  let renders = 0;
  f.context.renderConfigForm = () => ++renders;
  const pending = f.context.loadConfig();
  f.requests[0].resolve({
    ...response(200), json: () => new Promise((resolve) => { resolveBody = resolve; }),
  });
  await new Promise(setImmediate);
  f.run('setAuthToken("new-token"); configCache = {bot: {commandPrefix: "new-session"}}');
  resolveBody({ bot: { commandPrefix: 'old-session' } });
  await pending;
  assert.equal(f.run('configCache.bot.commandPrefix'), 'new-session');
  assert.equal(renders, 0);
  assert.equal([...f.timers.values()].filter((timer) => timer.delay === 1500).length, 0);
});

test('a login transition releases controls owned by an obsolete save', () => {
  const f = fixture();
  f.node('#config-save').disabled = true;
  f.node('#platform-modal').classList.remove('hidden');
  f.run('setAuthToken("new-token")');
  assert.equal(f.node('#config-save').disabled, false);
  assert.equal(f.node('#platform-modal').classList.contains('hidden'), true);
});
