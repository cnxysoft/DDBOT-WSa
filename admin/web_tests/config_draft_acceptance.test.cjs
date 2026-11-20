const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../web/js/app.js'), 'utf8');
const decode = (text) => text.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');

// A small DOM double exercises the actual form renderer and all descendants;
// replacing the renderer would miss drafts lost while controls are recreated.
class Element {
  constructor(tag = 'div') {
    this.tagName = tag.toUpperCase(); this.children = []; this.dataset = {}; this.style = { setProperty(name, value) { this[name] = value; } };
    this.value = ''; this.checked = false; this.disabled = false; this.className = ''; this.id = '';
    this.listeners = {}; this.textContent = ''; this.parentElement = null;
    this.classList = {
      contains: (name) => this.className.split(/\s+/).includes(name),
      add: (...names) => { this.className = [...new Set([...this.className.split(/\s+/).filter(Boolean), ...names])].join(' '); },
      remove: (...names) => { this.className = this.className.split(/\s+/).filter((n) => !names.includes(n)).join(' '); },
      toggle: (name, state) => { const add = state ?? !this.classList.contains(name); this.classList[add ? 'add' : 'remove'](name); },
    };
  }
  set innerHTML(html) {
    this.html = html; this.children = [];
    const stack = [this];
    for (const match of html.matchAll(/<\/?[A-Za-z][^>]*>/g)) {
      const token = match[0];
      if (token.startsWith('</')) { if (stack.length > 1) stack.pop(); continue; }
      const tag = /^<([\w-]+)/.exec(token)[1];
      const child = new Element(tag);
      for (const attr of token.slice(tag.length + 1, -1).matchAll(/([\w-]+)(?:="([^"]*)"|='([^']*)'|=([^\s>]+))?/g)) {
        const name = attr[1], value = decode(attr[2] ?? attr[3] ?? attr[4] ?? '');
        child.setAttribute(name, value);
      }
      stack.at(-1).appendChild(child);
      if (!['input', 'img', 'br', 'hr', 'use', 'meta', 'link'].includes(tag.toLowerCase()) && !token.endsWith('/>')) stack.push(child);
    }
  }
  get innerHTML() { return this.html || ''; }
  setAttribute(name, value) {
    if (name.startsWith('data-')) this.dataset[name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
    else if (name === 'class') this.className = value;
    else if (['checked', 'disabled'].includes(name)) this[name] = true;
    else this[name] = value;
  }
  getAttribute(name) { return name === 'class' ? this.className : this[name] ?? null; }
  appendChild(child) { child.parentElement = this; this.children.push(child); return child; }
  replaceChildren(...children) { this.children = []; children.forEach((child) => this.appendChild(child)); }
  contains(child) { return child === this || this.children.some((node) => node.contains(child)); }
  addEventListener(name, callback) { (this.listeners[name] ||= []).push(callback); }
  focus() {}
  remove() { if (this.parentElement) this.parentElement.children = this.parentElement.children.filter((node) => node !== this); }
  matches(selector) {
    if (selector.includes(',')) return selector.split(',').some((part) => this.matches(part.trim()));
    const attr = /\[([^\]=]+)(?:=["']?([^\]"']+)["']?)?\]/.exec(selector);
    const base = selector.replace(/\[[^\]]+\]/g, '');
    if (base.startsWith('#') && this.id !== base.slice(1)) return false;
    if (base.startsWith('.') && !this.classList.contains(base.slice(1))) return false;
    if (base && !base.startsWith('#') && !base.startsWith('.') && this.tagName.toLowerCase() !== base.toLowerCase()) return false;
    if (!attr) return true;
    const value = attr[1].startsWith('data-') ? this.dataset[attr[1].slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] : this[attr[1]];
    return value !== undefined && (attr[2] === undefined || value === attr[2]);
  }
  querySelectorAll(selector) { return this.children.flatMap((child) => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}

function fixture() {
  const document = new Element('document');
  for (const id of ['config-tabs', 'config-panels', 'config-draft-status', 'config-reload-status', 'config-save', 'platform-modal', 'platform-modal-title', 'platform-modal-body', 'login-modal', 'login-modal-body', 'login-modal-title', 'login-overlay', 'login-token', 'login-btn', 'login-error', 'admin-navigation']) {
    const node = new Element(); node.id = id;
    if (id.endsWith('modal') || id === 'login-overlay') node.className = 'hidden';
    document.appendChild(node);
  }
  document.createElement = (tag) => new Element(tag);
  const requests = [], storage = new Map([['ddbot_admin_token', 'token-secret']]), timers = new Map();
  let timerID = 0;
  const context = vm.createContext({
    document, URL, Headers, AbortController, TextDecoder,
    location: { protocol: 'http:', hostname: 'panel.example', host: 'panel.example:15631', origin: 'http://panel.example:15631', href: 'http://panel.example:15631/#/config', hash: '#/config' },
    localStorage: { getItem: (key) => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key) },
    fetch: (url, options) => new Promise((resolve, reject) => requests.push({ url, options, resolve, reject })),
    setTimeout: (callback, delay) => { timers.set(++timerID, { callback, delay }); return timerID; },
    clearTimeout: (id) => timers.delete(id),
    setInterval: (callback, delay) => { timers.set(++timerID, { callback, delay }); return timerID; },
    clearInterval: (id) => timers.delete(id),
    matchMedia: () => ({ addEventListener() {} }), addEventListener() {},
  });
  context.window = context;
  vm.runInContext(source, context, { filename: 'app.js' });
  vm.runInContext(`CONFIG_SCHEMA.splice(0, CONFIG_SCHEMA.length,
    { id:'basic', label:'基础', groups:[{title:'基本',fields:[SCHEMA_FIELDS['bot.commandPrefix'],SCHEMA_FIELDS['customCommandPrefix']]}]},
    { id:'platform', label:'平台', groups:[{site:'bilibili',title:'B站',fields:[SCHEMA_FIELDS['bilibili.interval'],SCHEMA_FIELDS['bilibili.SESSDATA']]}]},
    { id:'advanced', label:'高级', groups:[] });`, context);
  context.loadPermOverview = () => {};
  context.refreshReloadStatus = () => {};
  context.loadPlatformAccount = () => {};
  context.toast = () => {};
  const run = (code) => vm.runInContext(code, context);
  return { context, document, requests, storage, timers, run,
    node: (selector) => document.querySelector(selector), field: (name) => document.querySelector(`[data-path="${name}"]`) };
}

const response = (status, data, etag = '') => ({ status, ok: status < 400, headers: new Headers(etag ? { ETag: etag } : {}), json: async () => data });
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
const initial = { bot: { commandPrefix: '/' }, customCommandPrefix: { Help: '/' }, bilibili: { interval: '25s', SESSDATA: '***REDACTED***' } };

async function load(f, data = initial, etag = '"one"') {
  const pending = f.context.loadConfig();
  const request = f.requests.at(-1);
  assert.equal(request.options.method || 'GET', 'GET');
  request.resolve(response(200, data, etag));
  await pending;
}

test('real form recreation preserves normal and invalid JSON drafts while updating untouched fields', async () => {
  const f = fixture();
  await load(f);
  f.field('bot.commandPrefix').value = '!';
  f.node('#cfg-json').value = '{ invalid unfinished JSON';
  const refreshed = structuredClone(initial); refreshed.customCommandPrefix = { Remote: '#' };
  await load(f, refreshed, '"remote"');
  assert.equal(f.field('bot.commandPrefix').value, '!');
  assert.equal(f.node('#cfg-json').value, '{ invalid unfinished JSON');
  assert.deepEqual(JSON.parse(f.field('customCommandPrefix').value), { Remote: '#' });
  assert.deepEqual(JSON.parse(JSON.stringify(f.context.collectConfigPatch('basic'))), { bot: { commandPrefix: '!' } });
  assert.equal(f.run('configETag'), '"remote"');
});

test('a successful save preserves a newer edit made while its request is in flight', async () => {
  const f = fixture();
  await load(f);
  f.field('bot.commandPrefix').value = '!';
  const save = f.context.saveConfigPatch(f.context.collectConfigPatch('basic'));
  await flush();
  f.field('bot.commandPrefix').value = '#';
  f.node('#cfg-json').value = '{ other unsaved JSON';
  f.requests.at(-1).resolve(response(200, { reload: { revision: 2 } }, '"two"'));
  await save;
  const refreshed = structuredClone(initial); refreshed.bot.commandPrefix = '!';
  await load(f, refreshed, '"two"');
  assert.equal(f.field('bot.commandPrefix').value, '#');
  assert.equal(f.node('#cfg-json').value, '{ other unsaved JSON');
  assert.deepEqual(JSON.parse(JSON.stringify(f.context.collectConfigPatch('basic'))), { bot: { commandPrefix: '#' } });
});

test('closing and reopening a platform keeps its credential draft without persisting it to browser storage', async () => {
  const f = fixture();
  await load(f);
  f.context.renderPlatformModal('bilibili');
  f.field('bilibili.SESSDATA').value = 'draft-secret-only-in-memory';
  f.field('bilibili.interval').value = '1m';
  f.context.closePlatformModal();
  assert.equal(f.field('bilibili.SESSDATA'), null);
  f.context.renderPlatformModal('bilibili');
  assert.equal(f.field('bilibili.SESSDATA').value, 'draft-secret-only-in-memory');
  assert.equal(f.field('bilibili.interval').value, '1m');
  assert.equal([...f.storage.values()].some((value) => String(value).includes('draft-secret')), false);
});

test('a 409 preserves drafts and refreshes the version without resubmitting the patch', async () => {
  const f = fixture();
  await load(f);
  f.field('bot.commandPrefix').value = '!';
  const save = f.context.saveConfigPatch(f.context.collectConfigPatch('basic'));
  const rejected = assert.rejects(save, (error) => error.status === 409);
  await flush();
  const post = f.requests.at(-1);
  assert.equal(post.options.headers['If-Match'], '"one"');
  post.resolve(response(409, { error: '配置已更新' }));
  await flush();
  const refresh = f.requests.at(-1);
  assert.equal(refresh.options.method || 'GET', 'GET');
  const remote = structuredClone(initial); remote.bot.commandPrefix = 'remote';
  refresh.resolve(response(200, remote, '"remote"'));
  await rejected;
  await flush();
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 1);
  assert.equal(f.field('bot.commandPrefix').value, '!');
  assert.equal(f.run('configETag'), '"remote"');
  assert.match(f.node('#config-draft-status').textContent, /冲突|核对|草稿/);
});

test('a same-page queued save follows the first accepted ETag and stale loads cannot rewind it', async () => {
  const f = fixture();
  await load(f);
  const oldLoad = f.context.loadConfig();
  const oldRequest = f.requests.at(-1);
  const first = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  const second = f.context.saveConfigPatch({ customCommandPrefix: { Help: '#' } });
  await flush();
  const firstRequest = f.requests.at(-1);
  assert.equal(firstRequest.options.headers['If-Match'], '"one"');
  firstRequest.resolve(response(200, { reload: { revision: 2 } }, '"two"'));
  await first; await flush();
  const secondRequest = f.requests.at(-1);
  assert.equal(secondRequest.options.headers['If-Match'], '"two"');
  secondRequest.resolve(response(200, { reload: { revision: 3 } }, '"three"'));
  await second;
  oldRequest.resolve(response(200, initial, '"one"'));
  await oldLoad;
  assert.equal(f.run('configETag'), '"three"');
});

test('a basic save cannot make an older advanced draft revert the accepted basic value', async () => {
  const f = fixture(); await load(f);
  const draft = structuredClone(initial); draft.customCommandPrefix = { AdvancedDraft: '%' };
  f.node('#cfg-json').value = JSON.stringify(draft);
  f.field('bot.commandPrefix').value = '!';
  const save = f.context.saveConfigPatch(f.context.collectConfigPatch('basic'));
  await flush(); f.requests.at(-1).resolve(response(200, {}, '"two"')); await save;
  const accepted = structuredClone(initial); accepted.bot.commandPrefix = '!';
  await load(f, accepted, '"two"');
  assert.deepEqual(JSON.parse(JSON.stringify(f.context.collectConfigPatch('advanced'))), { customCommandPrefix: { AdvancedDraft: '%' } });
  assert.equal(f.field('bot.commandPrefix').value, '!');
});

test('a queued stale patch cannot use a baseline refreshed after a preceding conflict', async () => {
  const f = fixture(); await load(f);
  const first = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  const second = f.context.saveConfigPatch({ customCommandPrefix: { Draft: '#' } });
  const firstRejected = assert.rejects(first, (error) => error.status === 409);
  const secondRejected = assert.rejects(second);
  await flush(); f.requests.at(-1).resolve(response(409, { error: 'external conflict' }));
  await flush();
  assert.equal(f.requests.at(-1).options.method || 'GET', 'GET');
  f.requests.at(-1).resolve(response(200, initial, '"external"')); await firstRejected; await flush();
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 1,
    'a source change cancels the unconfirmed queued patch instead of rebasing or resubmitting it');
  await secondRejected;
  assert.equal(f.run('configETag'), '"external"');
});

test('a late body from the previous authentication generation cannot publish its ETag or form data', async () => {
  const f = fixture(); await load(f);
  const pending = f.context.loadConfig();
  const request = f.requests.at(-1);
  let finishBody;
  request.resolve({ status: 200, ok: true, headers: new Headers({ ETag: '"obsolete"' }), json: () => new Promise((resolve) => { finishBody = resolve; }) });
  await flush(); assert.equal(typeof finishBody, 'function');
  f.context.setAuthToken('new-session-token');
  await load(f, initial, '"new-session"');
  finishBody({ bot: { commandPrefix: 'obsolete-value' } }); await pending;
  assert.equal(f.run('configETag'), '"new-session"');
  assert.equal(f.field('bot.commandPrefix').value, '/');
  assert.equal(f.run('configDrafts.size'), 0);
});

test('a navigation response preserves drafts, stops streams and rejects queued old-origin saves', async () => {
  const f = fixture(); await load(f);
  const oldLocation = f.context.location.href;
  f.field('bot.commandPrefix').value = 'unsaved-draft';
  let pushStopped = 0;
  f.context.PushPage = { stop() { pushStopped++; } };
  f.run('dashboardTimer = setInterval(() => {}, 3000)');
  const dashboardTimer = f.run('dashboardTimer');
  const stream = f.context.createAuthStream('/api/v1/logs/stream', () => {}, () => {}, () => true);
  const connecting = stream.connect(); await flush();
  const streamRequest = f.requests.at(-1);
  const save = f.context.saveConfigPatch({ admin: { addr: ':15632' } });
  const queued = f.context.saveConfigPatch({ customCommandPrefix: { Draft: '!' } });
  const queuedRejected = assert.rejects(queued, /地址已变更|已关闭/);
  await flush();
  const post = f.requests.at(-1);
  post.resolve(response(200, { navigation: { enabled: true, addr: '[::]:15632' } }, '"two"'));
  await save; await queuedRejected;
  // A response already in flight must not restart its old-origin stream.
  streamRequest.resolve(response(200, {})); await connecting;
  const count = f.requests.length;
  await assert.rejects(f.context.api('/api/v1/health'), /地址已变更|已关闭/);
  await f.context.loadConfig(); await stream.connect();
  assert.equal(f.requests.length, count);
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 1);
  assert.equal(f.field('bot.commandPrefix').value, 'unsaved-draft');
  assert.equal(f.timers.has(dashboardTimer), false);
  assert.equal(pushStopped, 1);
  assert.equal(f.context.location.href, oldLocation, 'navigation must require a manual action');
  const banner = f.node('#admin-navigation'), link = banner.querySelector('a');
  assert.equal(link.href, 'http://panel.example:15632/#/config');
  assert.equal(link.rel, 'noopener noreferrer');
  assert.equal(link.target, '_blank');
  assert.doesNotMatch(link.href, /token|secret|\?/);
  assert.match(banner.children.map((child) => child.textContent).join(' '), /反向代理/);
});

test('navigation URLs accept listener addresses and never accept executable or credential-bearing input', () => {
  const f = fixture();
  for (const [addr, expected] of [
    [':15632', 'http://panel.example:15632/#/config'],
    ['0.0.0.0:15632', 'http://panel.example:15632/#/config'],
    ['[::]:15632', 'http://panel.example:15632/#/config'],
    ['[::1]:15632', 'http://[::1]:15632/#/config'],
    ['example.test:80', 'http://example.test/#/config'],
  ]) assert.equal(f.context.adminNavigationURL(addr), expected);
  for (const addr of ['javascript:alert(1)', 'https://evil.test:15632', 'user:secret@example.test:15632', 'evil.test:15632/path', 'evil.test:15632?token=secret', 'evil.test:0', 'evil.test:65536', '[bad:ip]:15632']) {
    assert.equal(f.context.adminNavigationURL(addr), null, addr);
  }
  f.context.applyAdminNavigation({ enabled: false });
  const banner = f.node('#admin-navigation');
  assert.equal(banner.querySelector('a'), null);
  assert.match(banner.children.map((child) => child.textContent).join(' '), /admin.enable: true/);
});

test('navigation suppresses even direct login and setup probes to the former origin', async () => {
  const f = fixture(); await load(f);
  f.context.applyAdminNavigation({ enabled: true, addr: '127.0.0.1:15632' });
  const count = f.requests.length;
  f.node('#login-token').value = 'token-secret';
  // These functions use fetch directly rather than the guarded api() helper.
  const login = f.context.doLogin({ preventDefault() {} });
  const setup = f.context.checkSetupThenStart();
  assert.equal(f.requests.length, count, 'no direct fetch may contact the obsolete listener');
  await Promise.all([login, setup]);
});

test('a health login response already in flight cannot clear retained drafts after navigation', async () => {
  const f = fixture(); await load(f);
  f.field('bot.commandPrefix').value = 'retained-after-move';
  f.node('#login-token').value = 'replacement-token';
  const login = f.context.doLogin({ preventDefault() {} });
  const request = f.requests.at(-1);
  f.context.applyAdminNavigation({ enabled: true, addr: ':15632' });
  request.resolve(response(200, {})); await login;
  assert.equal(f.field('bot.commandPrefix')?.value, 'retained-after-move');
  assert.equal(f.run('token'), 'token-secret');
  assert.equal(f.run('configETag'), '"one"');
});

test('an external restoration of a previously saved version starts a usable fresh save baseline', async () => {
  const f = fixture(); await load(f);
  const first = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  await flush(); f.requests.at(-1).resolve(response(200, {}, '"two"')); await first;
  // A user can restore an original file from backup, returning its exact ETag.
  await load(f, initial, '"one"');
  assert.equal(f.run('configETag'), '"one"');
  const fresh = f.context.saveConfigPatch({ bot: { commandPrefix: '#' } });
  await flush();
  const post = f.requests.at(-1);
  assert.equal(post.options.headers['If-Match'], '"one"', 'an earlier own-save mapping must not poison a new external baseline');
  post.resolve(response(200, {}, '"three"')); await fresh;
});

test('an external A-B-A restoration cancels an old A-based queued patch before sending it', async () => {
  const f = fixture(); await load(f);
  const first = f.context.saveConfigPatch({ bot: { commandPrefix: '!' } });
  const second = f.context.saveConfigPatch({ customCommandPrefix: { Second: '#' } });
  const third = f.context.saveConfigPatch({ bot: { commandPrefix: 'stale-old-A-draft' } });
  const secondRejected = assert.rejects(second, (error) => error.status === 409);
  const thirdRejected = assert.rejects(third);
  await flush(); f.requests.at(-1).resolve(response(200, {}, '"two"')); await first; await flush();
  const secondPost = f.requests.at(-1);
  assert.equal(secondPost.options.headers['If-Match'], '"two"');
  // While the second request is pending, a backup restoration is explicitly read.
  await load(f, initial, '"one"');
  secondPost.resolve(response(409, { error: 'restored externally' })); await flush();
  f.requests.at(-1).resolve(response(200, initial, '"one"')); await secondRejected; await flush();
  assert.equal(f.requests.filter((request) => request.options.method === 'POST').length, 2,
    'the old A-based queued patch must not silently match and overwrite the restored file');
  await thirdRejected;
  const fresh = f.context.saveConfigPatch({ bot: { commandPrefix: 'manually-reconfirmed' } });
  await flush(); const freshPost = f.requests.at(-1);
  assert.equal(freshPost.options.headers['If-Match'], '"one"');
  freshPost.resolve(response(200, {}, '"three"')); await fresh;
});

test('a delayed successful save body cannot replace a newer external source baseline', async () => {
  const f = fixture(); await load(f);
  f.field('bot.commandPrefix').value = '!';
  const save = f.context.saveConfigPatch(f.context.collectConfigPatch('basic'));
  const rejected = assert.rejects(save);
  await flush(); let finishBody;
  f.requests.at(-1).resolve({ status: 200, ok: true, headers: new Headers({ ETag: '"saved-before-remote"' }),
    json: () => new Promise((resolve) => { finishBody = resolve; }) });
  await flush(); assert.equal(typeof finishBody, 'function');
  const remote = structuredClone(initial); remote.bot.commandPrefix = 'external-later';
  await load(f, remote, '"external-later"');
  finishBody({ reload: { revision: 2 } }); await rejected;
  assert.equal(f.run('configETag'), '"external-later"');
  assert.equal(f.run('configCache.bot.commandPrefix'), 'external-later');
  assert.equal(f.field('bot.commandPrefix').value, '!', 'the unconfirmed input remains a draft for manual review');
});
