const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');

function section(startText, endText) {
  const start = source.indexOf(startText);
  const end = source.indexOf(endText, start);
  assert.ok(start >= 0 && end > start, `missing application section ${startText}`);
  return source.slice(start, end);
}

function domElement(hidden = false) {
  const classes = new Set(hidden ? ['hidden'] : []);
  return {
    style: {}, innerHTML: '', textContent: '', className: '', focus() {},
    classList: {
      add: (...names) => names.forEach((name) => classes.add(name)),
      remove: (...names) => names.forEach((name) => classes.delete(name)),
      contains: (name) => classes.has(name),
    },
  };
}

function authContext() {
  const overlay = domElement(true);
  const requests = [];
  const storage = new Map([['token', 'old']]);
  const context = vm.createContext({
    token: 'old', TOKEN_KEY: 'token', authGeneration: 0,
    dashboardTimer: null, loginRequestSeq: 0, loginHideTimer: null,
    activeConfigTab: 'basic',
    logStream: { stop() {} }, window: {}, clearInterval() {}, clearTimeout() {},
    $: (selector) => selector === '#login-overlay' ? overlay : domElement(),
    localStorage: { removeItem: (key) => storage.delete(key), setItem: (key, value) => storage.set(key, value) },
    fetch: (url, options) => new Promise((resolve) => requests.push({ url, options, resolve })),
    closeLoginModal() {},
  });
  vm.runInContext(section('function authSession()', 'async function apiJSON(') +
    section('function showLogin()', 'function hideLogin()'), context);
  return { overlay, requests, storage, context };
}

function reply(status, invalidToken = false) {
  return { status, ok: status < 400, headers: { get: (name) =>
    name.toLowerCase() === 'x-ddbot-auth' && invalidToken ? 'invalid-token' : null } };
}

test('only authentication rejection invalidates the current token', async () => {
  for (const [status, marked, logout] of [[401, false, true], [403, true, true], [403, false, false], [400, false, false]]) {
    const f = authContext();
    const pending = f.context.api('/api/v1/config');
    assert.equal(f.requests[0].options.headers.Authorization, 'Bearer old');
    f.requests[0].resolve(reply(status, marked));
    await pending.catch(() => {});
    assert.equal(f.context.token, logout ? '' : 'old', `status=${status} marked=${marked}`);
    assert.equal(f.overlay.classList.contains('hidden'), !logout);
    assert.equal(f.storage.has('token'), !logout);
  }
});

test('both unauthorized response forms ignore a replaced login token', async () => {
  for (const [status, marked] of [[401, false], [403, true]]) {
    const f = authContext();
    const pending = f.context.api('/api/v1/config');
    f.context.setAuthToken('new');
    f.requests[0].resolve(reply(status, marked));
    await pending.catch(() => {});
    assert.equal(f.context.token, 'new');
    assert.equal(f.storage.get('token'), 'new');
    assert.equal(f.overlay.classList.contains('hidden'), true);
  }
});

test('a successful login with the same token invalidates the earlier login generation', async () => {
  const f = authContext();
  const pending = f.context.api('/api/v1/config');
  f.context.setAuthToken('old');
  f.requests[0].resolve(reply(403, true));
  await pending.catch(() => {});
  assert.equal(f.context.token, 'old');
  assert.equal(f.storage.get('token'), 'old');
  assert.equal(f.overlay.classList.contains('hidden'), true);
});

function qrContext() {
  const elements = new Map();
  for (const selector of ['#login-modal', '#login-modal-title', '#login-modal-body', '#login-status']) {
    elements.set(selector, domElement(selector === '#login-modal'));
  }
  const requests = [];
  const timers = new Map();
  const effects = [];
  let timerID = 0;
  const context = vm.createContext({
    token: 'old', authGeneration: 0,
    LOGIN_PLATFORM_NAME: { bilibili: 'B站', weibo: '微博' },
    $: (selector) => elements.get(selector),
    esc: (value) => String(value),
    apiJSON: (url, options) => new Promise((resolve, reject) => requests.push({ url, options, resolve, reject })),
    api: async (url, options) => { requests.push({ url, options }); },
    setInterval: (callback) => { timers.set(++timerID, callback); return timerID; },
    clearInterval: (id) => timers.delete(id),
    toast: (...args) => effects.push(['toast', ...args]),
    loadConfig: () => effects.push(['loadConfig']),
    loadPlatformAccount: (...args) => effects.push(['account', ...args]),
  });
  vm.runInContext(section('function authSession()', 'function setAuthToken(') +
    section('let loginSession =', '/* ===== 平台登录态 ===== */'), context);
  return {
    context, requests, timers, effects, elements,
    session: () => vm.runInContext('loginSession', context),
    cancelIDs: () => requests.filter((r) => r.url.endsWith('/cancel')).map((r) => JSON.parse(r.options.body).session),
  };
}

test('reversed QR create responses retain the latest session and cancel the stale server session', async () => {
  const f = qrContext();
  const first = f.context.startPlatformLogin('bilibili');
  const second = f.context.startPlatformLogin('weibo');
  f.requests[1].resolve({ session: 'newest', image: 'new-image', platform: 'weibo' });
  await second;
  const body = f.elements.get('#login-modal-body').innerHTML;
  f.requests[0].resolve({ session: 'obsolete', image: 'old-image', platform: 'bilibili' });
  await first;
  assert.equal(f.session(), 'newest');
  assert.equal(f.elements.get('#login-modal-body').innerHTML, body);
  assert.equal(f.timers.size, 1);
  assert.deepEqual(f.cancelIDs(), ['obsolete']);
});

test('closing an active QR session ignores its late success and never resumes polling', async () => {
  const f = qrContext();
  const creating = f.context.startPlatformLogin('bilibili');
  f.requests[0].resolve({ session: 'active', image: 'old-image', platform: 'bilibili' });
  await creating;
  const checking = f.context.pollLoginStatus('active');
  const statusRequest = f.requests.find((r) => r.url.includes('/status?'));
  f.context.closeLoginModal();
  const body = f.elements.get('#login-modal-body').innerHTML;
  statusRequest.resolve({ state: 'confirmed', message: 'late confirmed', platform: 'bilibili' });
  await checking;
  assert.equal(f.session(), null);
  assert.equal(f.timers.size, 0);
  assert.equal(f.elements.get('#login-modal').classList.contains('hidden'), true);
  assert.equal(f.elements.get('#login-modal-body').innerHTML, body);
  assert.deepEqual(f.effects, []);
  assert.deepEqual(f.cancelIDs(), ['active']);
});

test('close then reopen while the first QR creation is pending cannot leak a hidden session', async () => {
  const f = qrContext();
  const first = f.context.startPlatformLogin('bilibili');
  f.context.closeLoginModal();
  const second = f.context.startPlatformLogin('bilibili');
  f.requests[1].resolve({ session: 'reopened', image: 'new-image', platform: 'bilibili' });
  await second;
  f.requests[0].resolve({ session: 'closed', image: 'old-image', platform: 'bilibili' });
  await first;
  assert.equal(f.session(), 'reopened');
  assert.equal(f.timers.size, 1);
  assert.deepEqual(f.cancelIDs(), ['closed']);
  assert.match(f.elements.get('#login-modal-body').innerHTML, /new-image/);
  assert.doesNotMatch(f.elements.get('#login-modal-body').innerHTML, /old-image/);
});

test('queued configuration saves are abandoned when their authentication generation is replaced', async () => {
  const requests = [];
  const context = vm.createContext({
    token: 'old', authGeneration: 0,
    apiJSON: (url, options, session) => new Promise((resolve) => requests.push({ url, options, session, resolve })),
  });
  vm.runInContext(section('function authSession()', 'function setAuthToken(') +
    section('let reloadStatusRequest =', 'setInterval(() => {'), context);
  const first = context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  const second = context.saveConfigPatch({ bot: { commandPrefix: '#' } });
  const firstRejected = assert.rejects(first);
  const secondRejected = assert.rejects(second);
  await Promise.resolve();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].session.token, 'old');
  context.token = 'new';
  context.authGeneration++;
  requests[0].resolve({ reload: { revision: 2 } });
  await Promise.all([firstRejected, secondRejected]);
  assert.equal(requests.length, 1, 'the old queued patch must never be sent with new credentials');

  const fresh = context.saveConfigPatch({ bot: { commandPrefix: '?' } });
  await Promise.resolve();
  assert.equal(requests.length, 2, 'a discarded queue must still accept a new-session save');
  assert.equal(requests[1].session.token, 'new');
  requests[1].resolve({ reload: { revision: 3 } });
  assert.equal((await fresh).reload.revision, 3);
});
