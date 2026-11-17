const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');
const start = source.indexOf('let reloadStatusRequest =');
const end = source.indexOf('// diffPatch', start);
assert.ok(start >= 0 && end > start, 'status handling must come from the actual application');

function fixture() {
  const target = { textContent: '' };
  const requests = [];
  const toasts = [];
  const context = vm.createContext({
    $: () => target,
    toast: (...args) => toasts.push(args),
    setInterval: () => 0,
    setTimeout: () => 0,
    configCache: null,
    renderConfigForm: () => {},
    loadPermOverview: () => {},
    showLogin: () => {},
    authSession: () => ({ generation: 0, token: 'test' }),
    isCurrentAuth: () => true,
    apiJSON: (url, options) => new Promise((resolve, reject) => {
      requests.push({ url, options, resolve, reject });
    }),
  });
  vm.runInContext(source.slice(start, end), context, { filename: 'app.js' });
  return { target, requests, toasts, context };
}

test('late status query cannot conceal restart requirements from a completed save', async () => {
  const f = fixture();
  const query = f.context.refreshReloadStatus();
  f.context.showConfigSaved({ reload: { revision: 4, restartRequired: ['proxy.type'] } });
  f.requests[0].resolve({ revision: 3, restartRequired: [] });
  await query;
  assert.match(f.target.textContent, /版本 4.*proxy\.type/);
  assert.match(f.toasts[0][0], /需重启/);
});

test('same-generation queries respect request order when an external reload fails', async () => {
  const f = fixture();
  const oldQuery = f.context.refreshReloadStatus();
  const newQuery = f.context.refreshReloadStatus();
  f.requests[1].resolve({ revision: 4, lastError: '配置重载失败，保留上次有效配置' });
  await newQuery;
  f.requests[0].resolve({ revision: 4, restartRequired: [] });
  await oldQuery;
  assert.match(f.target.textContent, /重载失败/);
});

test('save invalidates queries started both before and during persistence', async () => {
  const f = fixture();
  const before = f.context.refreshReloadStatus();
  const save = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  await Promise.resolve();
  f.requests[0].resolve({ revision: 2, restartRequired: [] });
  await before;
  assert.equal(f.target.textContent, '');
  const during = f.context.refreshReloadStatus();
  f.requests[1].resolve({ reload: { revision: 3, restartRequired: ['websocket.mode'] } });
  f.context.showConfigSaved(await save);
  f.requests[2].resolve({ revision: 2, restartRequired: [] });
  await during;
  assert.match(f.target.textContent, /版本 3.*websocket\.mode/);
  assert.equal(f.requests[1].options.method, 'POST');
  assert.deepEqual(JSON.parse(f.requests[1].options.body), { config: { bot: { commandPrefix: '!' } } });
});

test('configuration saves run in action order and a failed save does not block the queue', async () => {
  const f = fixture();
  const first = f.context.saveConfigPatch({ platform: { bilibili: { enable: false } } });
  const second = f.context.saveConfigPatch({ platform: { bilibili: { enable: true } } });
  await Promise.resolve();
  assert.equal(f.requests.length, 1, 'second save must not start while first is pending');
  f.requests[0].reject(new Error('save failed'));
  await assert.rejects(first, /save failed/);
  await Promise.resolve();
  assert.equal(f.requests.length, 2);
  assert.deepEqual(JSON.parse(f.requests[1].options.body), { config: { platform: { bilibili: { enable: true } } } });
  f.requests[1].resolve({ reload: { revision: 6, restartRequired: ['proxy.type'] } });
  f.context.showConfigSaved(await second);
  assert.match(f.target.textContent, /版本 6.*proxy\.type/);
});

test('a configuration GET started before saving cannot restore a stale form cache', async () => {
  const f = fixture();
  const oldLoad = f.context.loadConfig();
  const save = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  await Promise.resolve();
  f.requests[1].resolve({ reload: { revision: 2, restartRequired: [] } });
  f.context.showConfigSaved(await save);
  f.requests[0].resolve({ bot: { commandPrefix: '/' } });
  await oldLoad;
  assert.equal(f.context.configCache, null);
  const newLoad = f.context.loadConfig();
  f.requests[2].resolve({ bot: { commandPrefix: '!' } });
  await newLoad;
  assert.equal(f.context.configCache.bot.commandPrefix, '!');
  f.requests[3].resolve({ revision: 2, restartRequired: [] });
});

test('two overlapping configuration loads keep the latest response', async () => {
  const f = fixture();
  const first = f.context.loadConfig();
  const second = f.context.loadConfig();
  f.requests[1].resolve({ bot: { commandPrefix: '!' } });
  await second;
  f.requests[0].resolve({ bot: { commandPrefix: '/' } });
  await first;
  assert.equal(f.context.configCache.bot.commandPrefix, '!');
  f.requests[2].resolve({ revision: 2, restartRequired: [] });
});

test('a new process may restart its revision counter without freezing the UI', async () => {
  const f = fixture();
  f.context.showConfigSaved({ reload: { revision: 50, restartRequired: ['proxy.type'] } });
  const query = f.context.refreshReloadStatus();
  f.requests[0].resolve({ revision: 1, restartRequired: [] });
  await query;
  assert.match(f.target.textContent, /版本 1；/);
  assert.doesNotMatch(f.target.textContent, /proxy\.type/);
});

test('a failed query preserves the last known status and renders errors as text', async () => {
  const f = fixture();
  f.context.renderReloadStatus({ revision: 1, lastError: '<img src=x onerror=alert(1)>' });
  const query = f.context.refreshReloadStatus();
  f.requests[0].reject(new Error('network unavailable'));
  await query;
  assert.equal(f.target.textContent, '<img src=x onerror=alert(1)>');
  assert.equal(f.target.innerHTML, undefined);
});
