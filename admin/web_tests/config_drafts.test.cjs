const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');

function fixture() {
  const nodes = new Map(), controls = [], requests = [], timers = new Map();
  let timerID = 0;
  function element(selector) {
    const classes = new Set(['hidden']);
    return {
      id: typeof selector === 'string' ? selector.replace(/^#/, '') : '',
      value: '', checked: false, style: {}, dataset: {}, children: [], textContent: '', innerHTML: '',
      classList: {
        add: (x) => classes.add(x), remove: (x) => classes.delete(x), contains: (x) => classes.has(x),
        toggle: (x, on) => on ? classes.add(x) : classes.delete(x),
      },
      appendChild(child) { this.children.push(child); }, replaceChildren() { this.children = []; },
      querySelectorAll: () => [], querySelector: () => null, focus() {}, addEventListener() {},
    };
  }
  function node(selector) { if (!nodes.has(selector)) nodes.set(selector, element(selector)); return nodes.get(selector); }
  const context = vm.createContext({
    AbortController, TextDecoder, URL, location: { hostname: 'example.test', pathname: '/', protocol: 'https:' },
    localStorage: { getItem: () => 'secret-admin', setItem() {}, removeItem() {} },
    document: { querySelector: node, querySelectorAll: () => controls, createElement: element, addEventListener() {} },
    matchMedia: () => ({ addEventListener() {} }),
    setTimeout: (fn, ms) => { timers.set(++timerID, { fn, ms }); return timerID; }, clearTimeout: (id) => timers.delete(id),
    setInterval: (fn, ms) => { timers.set(++timerID, { fn, ms }); return timerID; }, clearInterval: (id) => timers.delete(id),
    fetch: (url, options) => new Promise((resolve) => requests.push({ url, options, resolve })),
  });
  context.window = context;
  vm.runInContext(source, context);
  const run = (code) => vm.runInContext(code, context);
  context.renderConfigForm = () => {
    for (const el of controls) {
      const value = run(`getPath(configCache, ${JSON.stringify(el.dataset.path)})`);
      el.value = String(value ?? ''); el.checked = !!value;
    }
    node('#cfg-json').value = run('JSON.stringify(configCache, null, 2)');
    run('advBase = cloneConfig(configCache)');
    context.restoreConfigDrafts();
  };
  context.loadPermOverview = () => {};
  context.refreshReloadStatus = () => {};
  function field(path, value) {
    const el = element(); el.dataset.path = path; el.value = value;
    el.dataset.draftOriginal = JSON.stringify({ value, checked: false, cleared: false });
    controls.push(el); return el;
  }
  function reply(value, status = 200, etag = '') {
    return { status, ok: status < 400, headers: new Headers(etag ? { ETag: etag } : {}), json: async () => value };
  }
  return { context, run, node, controls, field, requests, reply, timers };
}

test('refresh preserves drafts and reports an external change to their baseline', async () => {
  const f = fixture();
  f.run('configCache = {bot:{commandPrefix:"/"}}');
  const field = f.field('bot.commandPrefix', '/'); field.value = '!draft';
  const load = f.context.loadConfig();
  f.requests[0].resolve(f.reply({ bot: { commandPrefix: '#external' } }, 200, '"v2"'));
  await load;
  assert.equal(field.value, '!draft');
  assert.match(f.node('#config-draft-status').textContent, /配置来源已有更新/);
  assert.equal(f.run('configETag'), '"v2"');
});

test('a successful save preserves later typing and an unrelated field draft', async () => {
  const f = fixture();
  f.run('configCache = {bot:{commandPrefix:"/"},logLevel:"info"}; configETag = "v1"');
  const prefix = f.field('bot.commandPrefix', '/'), log = f.field('logLevel', 'info');
  prefix.value = '!submitted'; log.value = 'debug';
  const save = f.context.saveConfigPatch({ bot: { commandPrefix: '!submitted' } });
  await Promise.resolve();
  prefix.value = '!later';
  f.requests[0].resolve(f.reply({ reload: { revision: 2 } }, 200, 'v2'));
  await save;
  assert.equal(prefix.value, '!later'); assert.equal(log.value, 'debug');
  assert.equal(f.run('configDrafts.size'), 2);
  assert.equal(f.requests[0].options.headers['If-Match'], 'v1');
});

test('an unchanged submitted field is clean while an invalid advanced draft survives', async () => {
  const f = fixture();
  f.run('configCache = {logLevel:"info"}; advBase = cloneConfig(configCache)');
  const log = f.field('logLevel', 'info'); log.value = 'debug';
  const advanced = f.node('#cfg-json'); advanced.dataset.draftOriginal = '{"logLevel":"info"}'; advanced.value = '{invalid';
  const save = f.context.saveConfigPatch({ logLevel: 'debug' });
  await Promise.resolve(); f.requests[0].resolve(f.reply({ reload: { revision: 2 } })); await save;
  assert.equal(f.run('configDrafts.size'), 0);
  assert.equal(f.run('advancedDraft.raw'), '{invalid');
});

test('a 409 retains drafts, refreshes the baseline, and never retries a POST', async () => {
  const f = fixture();
  f.run('configCache = {logLevel:"info"}; configETag = "v1"');
  const field = f.field('logLevel', 'info'); field.value = 'debug';
  const save = f.context.saveConfigPatch({ logLevel: 'debug' });
  await Promise.resolve(); f.requests[0].resolve(f.reply({ error: 'conflict' }, 409));
  await new Promise(setImmediate);
  assert.equal(f.requests[1].options.method, undefined);
  f.requests[1].resolve(f.reply({ logLevel: 'warn' }, 200, 'v2'));
  await assert.rejects(save, /conflict/);
  assert.equal(field.value, 'debug');
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 1);
  assert.match(f.node('#config-draft-status').textContent, /冲突/);
});

test('same-page queued saves advance only through their own accepted ETags', async () => {
  const f = fixture(); f.run('configETag = "v1"');
  const first = f.context.saveConfigPatch({ logLevel: 'debug' });
  const second = f.context.saveConfigPatch({ template: { enable: true } });
  await Promise.resolve(); f.requests[0].resolve(f.reply({}, 200, 'v2')); await first;
  await new Promise(setImmediate);
  assert.equal(f.requests[1].options.headers['If-Match'], 'v2');
  f.requests[1].resolve(f.reply({}, 200, 'v3')); await second;
});

test('a third save queued after the first success follows the second accepted ETag', async () => {
  const f = fixture(); f.run('configETag = "v1"');
  const first = f.context.saveConfigPatch({ logLevel: 'debug' });
  const second = f.context.saveConfigPatch({ template: { enable: true } });
  await Promise.resolve(); f.requests[0].resolve(f.reply({}, 200, 'v2')); await first;
  await new Promise(setImmediate);
  const third = f.context.saveConfigPatch({ imagePool: { type: 'off' } });
  f.requests[1].resolve(f.reply({}, 200, 'v3')); await second;
  await new Promise(setImmediate);
  assert.equal(f.requests[2].options.headers['If-Match'], 'v3');
  f.requests[2].resolve(f.reply({}, 200, 'v4')); await third;
});

test('a saved field closed while the request is pending is cleared from the draft map', async () => {
  const f = fixture(); f.run('configCache={logLevel:"info"}');
  const field = f.field('logLevel', 'info'); field.value = 'debug';
  const save = f.context.saveConfigPatch({ logLevel: 'debug' });
  f.context.captureConfigDrafts(); f.controls.length = 0;
  await Promise.resolve(); f.requests[0].resolve(f.reply({}, 200)); await save;
  assert.equal(f.run('configDrafts.size'), 0);
});

test('a conflict refresh cannot silently rebase a queued stale patch', async () => {
  const f = fixture(); f.run('configETag = "v1"');
  const first = f.context.saveConfigPatch({ logLevel: 'debug' });
  const second = f.context.saveConfigPatch({ template: { enable: true } });
  const rejectedFirst = assert.rejects(first), rejectedSecond = assert.rejects(second);
  await Promise.resolve(); f.requests[0].resolve(f.reply({ error: 'conflict' }, 409));
  await new Promise(setImmediate);
  f.requests[1].resolve(f.reply({ logLevel: 'warn' }, 200, 'remote'));
  await rejectedFirst; await rejectedSecond;
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 1);
});

test('advanced save preserves continued invalid typing and removes only acknowledged edits', async () => {
  const f = fixture(); f.run('configCache = {logLevel:"info"}; advBase = cloneConfig(configCache)');
  const advanced = f.node('#cfg-json');
  advanced.dataset.draftOriginal = '{"logLevel":"info"}'; advanced.value = '{"logLevel":"debug"}';
  const pending = f.context.saveConfigPatch({ logLevel: 'debug' }, { advanced: true });
  await Promise.resolve(); advanced.value = '{still editing';
  f.requests[0].resolve(f.reply({}, 200, 'v2')); await pending;
  assert.equal(advanced.value, '{still editing');
  assert.equal(f.run('advancedDraft.base.logLevel'), 'debug');
  assert.equal(f.run('advancedDraft.raw'), '{still editing');
});

test('a GET started during persistence cannot rewind a completed save or its ETag', async () => {
  const f = fixture(); f.run('configETag = "v1"');
  const save = f.context.saveConfigPatch({ logLevel: 'debug' });
  await Promise.resolve();
  const load = f.context.loadConfig();
  f.requests[0].resolve(f.reply({}, 200, 'v2')); await save;
  f.requests[1].resolve(f.reply({ logLevel: 'info' }, 200, 'v1')); await load;
  assert.equal(f.run('configETag'), 'v2');
  assert.equal(f.run('configCache.logLevel'), 'debug');
});

test('navigation guidance stops old connections and offers a safe manual link', async () => {
  const f = fixture();
  f.context.applyAdminNavigation({ enabled: true, addr: '[::]:15632' });
  const banner = f.node('#admin-navigation');
  const link = banner.children.find((child) => child.href);
  assert.equal(link.href, 'http://example.test:15632/#/config');
  assert.doesNotMatch(link.href, /secret|token|\?/);
  assert.equal(banner.classList.contains('hidden'), false);
  await assert.rejects(f.context.api('/api/v1/status'), /地址已变更/);
  await f.context.loadConfig();
  assert.equal(f.requests.length, 0);
  assert.match(banner.children.map((child) => child.textContent).join(''), /反向代理/);
  assert.equal(f.context.adminNavigationURL('javascript:alert(1)'), null);
});

test('disabling admin shows durable recovery instructions and no address link', () => {
  const f = fixture(); f.context.applyAdminNavigation({ enabled: false, addr: '' });
  const banner = f.node('#admin-navigation');
  assert.match(banner.children.map((child) => child.textContent).join(''), /admin.enable: true/);
  assert.equal(banner.children.some((child) => child.href), false);
});

test('a late unauthorized response after navigation cannot clear the retained drafts', async () => {
  const f = fixture();
  const pending = f.context.api('/api/v1/status');
  f.run('configDrafts.set("logLevel", {raw:{value:"debug"},base:"info"})');
  f.context.applyAdminNavigation({ enabled: false });
  f.requests[0].resolve(f.reply({}, 401));
  await assert.rejects(pending, /地址已变更/);
  assert.equal(f.run('configDrafts.size'), 1);
  assert.equal(f.run('token'), 'secret-admin');
});
