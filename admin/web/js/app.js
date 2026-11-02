/* DDBOT-WSa 管理面板 */
'use strict';

/* ===== 状态 ===== */
const TOKEN_KEY = 'ddbot_admin_token';
const THEME_KEY = 'ddbot_theme';
let token = localStorage.getItem(TOKEN_KEY) || '';
let sseConnected = false;
let logPaused = false;
const logBuffer = [];      // 全量日志（上限 800）
const LOG_LIMIT = 800;
const DASH_LOG_KEEP = 15;  // 仪表盘保留条数

const SITES = ['bilibili', 'douyu', 'huya', 'youtube', 'weibo', 'twitter', 'douyin', 'acfun', 'twitch', 'twitcasting', 'xhs', 'xhh'];
const SUB_TYPES = ['live', 'news'];
const SUB_TYPE_NAMES = { live: '直播', news: '动态' };
// 各平台支持的订阅类型（与后端 concern 注册一致）；订阅弹层按所选平台过滤
const PLATFORM_TYPES = {
  bilibili: ['live', 'news'], douyu: ['live'], huya: ['live'], youtube: ['live', 'news'],
  weibo: ['news'], twitter: ['news'], douyin: ['live', 'news'], acfun: ['live', 'news'],
  twitch: ['live'], twitcasting: ['live'], xhs: ['live', 'news'], xhh: ['news'],
};

/* ===== 工具 ===== */
const $ = (s) => document.querySelector(s);

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function toast(msg, ok = true) {
  const el = document.createElement('div');
  el.className = 'toast ' + (ok ? 'ok' : 'err');
  el.textContent = msg;
  $('#toast-container').appendChild(el);
  setTimeout(() => el.remove(), ok ? 4000 : 5500);
}

function fmtDuration(sec) {
  if (sec == null || isNaN(sec)) return '—';
  sec = Math.floor(sec);
  const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600),
    m = Math.floor(sec % 3600 / 60), s = sec % 60;
  if (d) return `${d} 天 ${h} 小时`;
  if (h) return `${h} 小时 ${m} 分`;
  if (m) return `${m} 分 ${s} 秒`;
  return `${s} 秒`;
}

function fmtTime(iso) {
  try { return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false }); }
  catch { return iso; }
}

/* ===== API 封装 ===== */
async function api(path, opts = {}) {
  const resp = await fetch(path, {
    ...opts,
    headers: {
      ...(opts.headers || {}),
      ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
      Authorization: 'Bearer ' + token,
    },
  });
  if (resp.status === 401) { showLogin(); throw new Error('未授权'); }
  if (resp.status === 403) { throw new Error('Token 错误或无权限'); }
  return resp;
}

async function apiJSON(path, opts) {
  const r = await api(path, opts);
  if (!r.ok) {
    let msg = 'HTTP ' + r.status;
    try { const e = await r.json(); if (e.error) msg = e.error; } catch {}
    throw new Error(msg);
  }
  return r.json();
}

/* ===== 登录 ===== */
function showLogin() {
  localStorage.removeItem(TOKEN_KEY);
  token = '';
  const ov = $('#login-overlay');
  ov.style.opacity = '';
  ov.style.transition = '';
  ov.classList.remove('hidden');
  $('#login-token').focus();
}

function hideLogin() {
  const ov = $('#login-overlay');
  ov.style.transition = 'opacity .25s';
  ov.style.opacity = '0';
  setTimeout(() => {
    ov.classList.add('hidden');
    ov.style.opacity = '';
    ov.style.transition = '';
  }, 250);
}

function showLoginError(msg) {
  const el = $('#login-error');
  el.textContent = msg;
  el.classList.remove('hidden');
  el.style.animation = 'none'; // 强制重播 shake 动画
  void el.offsetHeight;
  el.style.animation = '';
}

async function doLogin(e) {
  e.preventDefault();
  const t = $('#login-token').value.trim();
  if (!t) return;
  $('#login-btn').disabled = true;
  $('#login-btn').textContent = '登录中…';
  try {
    const resp = await fetch('/api/v1/health', {
      headers: { Authorization: 'Bearer ' + t },
    });
    if (resp.status === 200) {
      token = t;
      localStorage.setItem(TOKEN_KEY, t);
      $('#login-error').classList.add('hidden');
      hideLogin();
      boot();
    } else {
      showLoginError(resp.status === 401 || resp.status === 403 ? 'Token 不正确，请重试' : '登录失败：服务暂时不可用，请稍后重试');
    }
  } catch {
    showLoginError('网络连接失败，请检查服务是否在运行');
  } finally {
    $('#login-btn').disabled = false;
    $('#login-btn').textContent = '登 录';
  }
}

/* ===== 路由 ===== */
const PAGE_TITLES = {
  dashboard: '仪表盘', subscriptions: '订阅管理', logs: '实时日志', pushes: '推送记录', config: '配置',
};

function route() {
  let page = (location.hash || '#/dashboard').replace('#/', '') || 'dashboard';
  if (!PAGE_TITLES[page]) page = 'dashboard';
  document.querySelectorAll('.nav-item').forEach((a) =>
    a.classList.toggle('active', a.dataset.page === page));
  document.querySelectorAll('.page').forEach((s) =>
    s.classList.toggle('active', s.id === 'page-' + page));
  $('#page-title').textContent = PAGE_TITLES[page];
  closeSidebar();
  // 切页关闭所有弹层，避免上下文残留
  closeTemplateModal();
  if (typeof closePlatformModal === 'function') closePlatformModal();
  hideSubModal();
  if (page === 'subscriptions') loadSubscriptions();
  if (page === 'config') loadConfig();
  if (page === 'logs') { scrollToBottom(); if (!sseConnected) connectLogs(); }
  if (page === 'pushes' && window.PushPage) PushPage.enter();
}

/* ===== 主题 ===== */
const themeMedia = window.matchMedia('(prefers-color-scheme: light)');
function applyTheme(t) {
  if (t === 'auto') {
    document.documentElement.setAttribute('data-theme', themeMedia.matches ? 'light' : 'dark');
    localStorage.setItem(THEME_KEY, 'auto');
  } else {
    document.documentElement.setAttribute('data-theme', t);
    localStorage.setItem(THEME_KEY, t);
  }
}
// 跟随系统模式下，系统切换时实时跟随
themeMedia.addEventListener('change', () => {
  if ((localStorage.getItem(THEME_KEY) || 'auto') === 'auto') applyTheme('auto');
});

/* ===== 仪表盘 ===== */
const numAnimState = {}; // 数字滚动补间的当前值缓存

function animateNumber(el, to) {
  if (!el) return;
  const from = numAnimState[el.id] ?? 0;
  cancelAnimationFrame(el._raf);
  const start = performance.now(), dur = 650;
  const step = (t) => {
    const p = Math.min((t - start) / dur, 1);
    const ease = 1 - Math.pow(1 - p, 3); // easeOutCubic
    el.textContent = Math.round(from + (to - from) * ease);
    if (p < 1) el._raf = requestAnimationFrame(step);
    else numAnimState[el.id] = to;
  };
  el._raf = requestAnimationFrame(step);
}

function unskeleton(id) {
  const el = $(id);
  if (el) el.classList.remove('skeleton');
}

async function refreshDashboard() {
  try {
    const [ob, st, sum] = await Promise.all([
      apiJSON('/api/v1/onebot/status'),
      apiJSON('/api/v1/status'),
      apiJSON('/api/v1/subs/summary'),
    ]);
    $('#ob-status').className = 'dot ' + (ob.online ? 'on' : 'off');
    $('#ob-text').textContent = ob.online ? '在线' : '离线';
    $('#ob-sub').textContent = (st.concernsStatus ? Object.keys(st.concernsStatus).length : 0) + ' 个平台已加载';
    unskeleton('#uptime');
    $('#uptime').textContent = fmtDuration(st.uptime / 1e9);
    $('#sub-count').textContent = (st.subscriptions || 0) + ' 条订阅';
    unskeleton('#subs-total');
    animateNumber($('#subs-total'), sum.total ?? 0);
    const bySite = sum.bySite || {};
    $('#subs-bysite').innerHTML = Object.keys(bySite).length
      ? Object.entries(bySite).map(([k, v]) => `<span class="site-badge">${esc(k)} ${v}</span>`).join('')
      : '暂无订阅';
  } catch (e) {
    unskeleton('#uptime');
    unskeleton('#subs-total');
    $('#ob-text').textContent = '获取失败';
  }
}

/* ===== 日志（SSE） ===== */
function logVisible(entry) {
  const q = ($('#log-search')?.value || '').toLowerCase();
  const boxes = document.querySelectorAll('#log-levels input:checked');
  const levels = new Set(Array.from(boxes).map((b) => b.value));
  return levels.has(entry.level) && (!q || entry.message.toLowerCase().includes(q));
}

function renderLogLine(entry) {
  const div = document.createElement('div');
  div.className = 'log-line';
  div.innerHTML = `<span class="log-time">${esc(fmtTime(entry.time))}</span>` +
    `<span class="log-level ${esc(entry.level)}">${esc(entry.level)}</span>` +
    `<span class="log-msg">${esc(entry.message)}</span>`;
  return div;
}

function scrollToBottom() {
  if (!$('#log-autoscroll')?.checked || logPaused) return;
  const v = $('#log-stream');
  if (v) v.scrollTop = v.scrollHeight;
}

function appendLog(entry) {
  logBuffer.push(entry);
  if (logBuffer.length > LOG_LIMIT) logBuffer.splice(0, logBuffer.length - LOG_LIMIT);
  // 日志页（仅可见行渲染，控制 DOM 数量）
  const stream = $('#log-stream');
  if (stream && logVisible(entry)) {
    stream.appendChild(renderLogLine(entry));
    while (stream.childElementCount > LOG_LIMIT) stream.removeChild(stream.firstChild);
    scrollToBottom();
  }
  // 仪表盘最近日志
  const dash = $('#dash-logs');
  if (dash) {
    dash.appendChild(renderLogLine(entry));
    while (dash.childElementCount > DASH_LOG_KEEP) dash.removeChild(dash.firstChild);
    if ($('#page-dashboard').classList.contains('active')) dash.scrollTop = dash.scrollHeight;
  }
}

function rerenderLogs() {
  const stream = $('#log-stream');
  if (!stream) return;
  stream.innerHTML = '';
  for (const e of logBuffer) if (logVisible(e)) stream.appendChild(renderLogLine(e));
  scrollToBottom();
}

async function connectLogs(retry) {
  if (!token) return;
  try {
    const resp = await fetch('/api/v1/logs/stream', {
      headers: { Authorization: 'Bearer ' + token },
    });
    if (resp.status === 401) { showLogin(); return; }
    if (!resp.ok || !resp.body) throw new Error('HTTP ' + resp.status);
    setSSE(true);
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const data = frame.split('\n').find((l) => l.startsWith('data: '));
        if (!data) continue;
        try { appendLog(JSON.parse(data.slice(6))); } catch {}
      }
    }
  } catch (e) { /* fallthrough */ }
  setSSE(false);
  // 断线重连：仅日志页可见时保持（避免多标签页/后台页签堆积长连接占满浏览器 6 连接上限）
  setTimeout(() => {
    if ($('#page-logs') && $('#page-logs').classList.contains('active')) connectLogs(true);
  }, 3000);
}

function setSSE(on) {
  sseConnected = on;
  const b = $('#log-conn');
  if (b) { b.className = 'badge ' + (on ? 'on' : 'off'); b.textContent = on ? '已连接' : '重连中'; }
  // 顶栏连接徽章同步（与日志流共用 SSE 生命周期）
  const cb = $('#conn-badge');
  if (cb) { cb.className = 'badge ' + (on ? 'on' : 'off'); cb.textContent = on ? '已连接' : '连接断开'; }
}

/* ===== 订阅管理 ===== */
let subsCache = [];

async function loadSubscriptions() {
  const tbody = $('#subs-tbody');
  try {
    const subs = await apiJSON('/api/v1/subs/list');
    subsCache = subs || [];
    if (!subsCache.length) {
      tbody.innerHTML = '<tr><td colspan="6" class="empty"><svg class="btn-ic" style="width:32px;height:32px;opacity:.45"><use href="#i-list"/></svg><br>暂无订阅，点击右上角「添加订阅」</td></tr>';
      return;
    }
    tbody.innerHTML = subsCache.map((s, i) => `
      <tr>
        <td><span class="site-badge">${esc((PLATFORM_META[s.site] || {}).name || s.site)}</span></td>
        <td class="mono">${esc(s.id)}</td>
        <td>${esc(s.name || '—')}</td>
        <td class="mono">${esc(s.groupCode)}</td>
        <td><span class="site-badge">${esc(SUB_TYPE_NAMES[s.type] || s.type)}</span></td>
        <td class="td-actions">
          <button class="btn ghost sm" data-act="edit" data-i="${i}">编辑</button>
          <button class="btn ghost sm danger" data-act="del" data-i="${i}">删除</button>
        </td>
      </tr>`).join('');
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="6" class="empty">加载失败：${esc(e.message)}</td></tr>`;
  }
}

document.addEventListener('DOMContentLoaded', () => {
  $('#subs-tbody').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-act]');
    if (!btn) return;
    const s = subsCache[+btn.dataset.i];
    if (!s) return;
    if (btn.dataset.act === 'edit') openEditSub(s);
    else removeSub(s);
  });
  $('#add-sub-btn').addEventListener('click', openAddSub);
  $('#sub-modal-close').addEventListener('click', hideSubModal);
});

function hideSubModal() { $('#sub-modal').classList.add('hidden'); }

function subFormHTML(site, id, group, types) {
  return `
    <div class="form-group">
      <h4 class="form-group-title">订阅对象</h4>
      <div class="form-grid sub-grid">
        <div class="form-row"><label>平台</label>
          <select id="sub-site" class="input" ${id ? 'disabled' : ''}>${SITES.map((s) => `<option value="${s}" ${s === site ? 'selected' : ''}>${esc((PLATFORM_META[s] || {}).name || s)}</option>`).join('')}</select></div>
        <div class="form-row"><label>ID（房间号 / 用户 UID）</label>
          <input id="sub-id" class="input" value="${esc(id)}" ${id ? 'readonly' : ''} placeholder="例如 22625025"></div>
      </div>
    </div>
    <div class="form-group">
      <h4 class="form-group-title">推送目标</h4>
      <div class="form-row"><label>群号</label>
        <input id="sub-group" class="input" value="${esc(group)}" placeholder="例如 123456789"></div>
    </div>
    <div class="form-group">
      <h4 class="form-group-title">订阅类型</h4>
      <div class="type-chips">
      ${(PLATFORM_TYPES[site] || SUB_TYPES).map((t) => `<label class="type-chip"><input type="checkbox" class="sub-type" value="${t}" ${types.includes(t) ? 'checked' : ''}>${esc(SUB_TYPE_NAMES[t] || t)}</label>`).join('')}
      </div>
      ${id ? '<p class="config-note">编辑时勾选/取消勾选类型将分别执行添加/移除操作。</p>' : ''}
    </div>
    <div class="modal-actions">
      <button class="btn" id="sub-cancel">取消</button>
      <button class="btn primary" id="sub-save">保存</button>
    </div>`;
}

function openAddSub() {
  $('#sub-modal-title').textContent = '添加订阅';
  $('#sub-modal-body').innerHTML = subFormHTML('bilibili', '', '', []);
  bindSubModal(null);
  $('#sub-modal').classList.remove('hidden');
}

function openEditSub(s) {
  // 该 (site,id,group) 已有的类型集合
  const types = subsCache
    .filter((x) => x.site === s.site && String(x.id) === String(s.id) && String(x.groupCode) === String(s.groupCode))
    .map((x) => x.type);
  $('#sub-modal-title').textContent = '编辑订阅';
  $('#sub-modal-body').innerHTML = subFormHTML(s.site, s.id, s.groupCode, types);
  bindSubModal(types);
  $('#sub-modal').classList.remove('hidden');
}

function bindSubModal(existingTypes) {
  $('#sub-cancel').addEventListener('click', hideSubModal);
  // 切换平台时按其支持的类型重渲染 chips（保留已勾选中仍受支持的）
  $('#sub-site').addEventListener('change', () => {
    const site = $('#sub-site').value;
    const checked = new Set(Array.from(document.querySelectorAll('.sub-type:checked')).map((b) => b.value));
    const chips = document.querySelector('#sub-modal .type-chips');
    if (chips) {
      chips.innerHTML = (PLATFORM_TYPES[site] || SUB_TYPES).map((t) =>
        `<label class="type-chip"><input type="checkbox" class="sub-type" value="${t}" ${checked.has(t) ? 'checked' : ''}>${esc(SUB_TYPE_NAMES[t] || t)}</label>`).join('');
    }
  });
  $('#sub-save').addEventListener('click', async () => {
    const site = $('#sub-site').value;
    const id = $('#sub-id').value.trim();
    const group = parseInt($('#sub-group').value.trim(), 10);
    const wanted = Array.from(document.querySelectorAll('.sub-type:checked')).map((b) => b.value);
    if (!id || !group) { toast('请填写 ID 和群号', false); return; }
    if (!wanted.length) { toast('请至少选择一种订阅类型', false); return; }
    const toAdd = existingTypes ? wanted.filter((t) => !existingTypes.includes(t)) : wanted;
    const toRemove = existingTypes ? existingTypes.filter((t) => !wanted.includes(t)) : [];
    try {
      for (const t of toAdd) {
        await apiJSON('/api/v1/subs/add', {
          method: 'POST',
          body: JSON.stringify({ site, id, type: t, groupCode: group }),
        });
      }
      for (const t of toRemove) {
        await apiJSON('/api/v1/subs/remove', {
          method: 'POST',
          body: JSON.stringify({ site, id, type: t, groupCode: group }),
        });
      }
      toast(toAdd.length + toRemove.length ? '订阅已更新' : '无变更');
      hideSubModal();
      loadSubscriptions();
    } catch (e) {
      toast('操作失败：' + e.message, false);
    }
  });
}

async function removeSub(s) {
  if (!confirm(`确认删除 ${(PLATFORM_META[s.site] || {}).name || s.site} / ${s.id}（群 ${s.groupCode}）的 ${SUB_TYPE_NAMES[s.type] || s.type} 订阅？`)) return;
  try {
    await apiJSON('/api/v1/subs/remove', {
      method: 'POST',
      body: JSON.stringify({ site: s.site, id: s.id, type: s.type, groupCode: s.groupCode }),
    });
    toast('已删除');
    loadSubscriptions();
  } catch (e) {
    toast('删除失败：' + e.message, false);
  }
}

/* ===== 配置（schema 驱动表单） ===== */
let configCache = null;
let activeConfigTab = 'basic';
// 高级页 textarea 渲染时的快照，作为该页 diff 的基准。
// 不能用 configCache 当基准：textarea 是加载时的快照，期间别处（平台开关即时保存、
// 基础页表单）改过的键在 configCache 里已经变了，拿它比会把这些旧值当成「用户改的」发出去。
let advBase = null;

/* 字段类型：text / number / bool(toggle) / select / credential(只读状态)。
 * 凭据类字段（cookie/token/password）后端 API 禁止修改（防 token 泄露后篡改凭据），
 * 因此只展示配置状态，引导用户在 application.yaml 中修改。 */
const CONFIG_SCHEMA = [
  { id: 'basic', label: '基础', groups: [
    { title: 'Bot 行为', fields: [
      { path: 'bot.commandPrefix', label: '命令前缀', type: 'text', def: '/' },
      { path: 'customCommandPrefix', label: '自定义命令前缀', type: 'jsonmap', def: {},
        hint: 'JSON 对象，形如 {"签到": ""}：给单个命令单独指定前缀，优先于全局前缀；前缀留空表示该命令不需要前缀。整表替换，删掉条目即取消。若框里显示的是带引号的字符串（如 "签到: "），说明 YAML 里冒号写成了全角「：」，整块退化成了字符串，改回半角冒号后重填即可。' },
      { path: 'bot.onJoinGroup.rename', label: '进群自动改名', type: 'text', def: '【bot】', hint: '留空则不改名' },
      { path: 'bot.onDisconnected', label: '断线后动作', type: 'select', options: ['', 'exit'], def: '', hint: 'exit=退出程序，留空=仅记录日志' },
      { path: 'bot.offlineQueue.enable', label: '离线消息缓存', type: 'bool', def: false, hint: '离线时暂存消息，上线补发（期间不能重启）' },
      { path: 'bot.offlineQueue.expire', label: '离线消息有效期', type: 'text', def: '30m' },
    ]},
    { title: '推送调度', fields: [
      { path: 'concern.emitInterval', label: '推送发射间隔', type: 'text', def: '5s' },
      { path: 'notify.parallel', label: '并行推送数量', type: 'number', def: 1 },
      { path: 'dispatch.largeNotifyLimit', label: '大通知分段阈值', type: 'number', def: 50 },
    ]},
  ]},
  { id: 'network', label: '网络', groups: [
    { title: 'OneBot WebSocket', fields: [
      { path: 'websocket.mode', label: '模式', type: 'select', options: ['ws-server','ws-reverse'], def: 'ws-server' },
      { path: 'websocket.ws-server', label: '正向监听地址', type: 'text', def: '127.0.0.1:15630' },
      { path: 'websocket.ws-reverse', label: '反向连接地址', type: 'text', def: 'ws://localhost:3001' },
      { path: 'websocket.token', label: 'Access Token', type: 'credential' },
    ]},
    { title: '代理', fields: [
      { path: 'proxy.type', label: '代理方式', type: 'select', options: ['systemProxy','localProxyPool','pyProxyPool','off'], def: 'systemProxy' },
    ]},
    { title: 'Telegram', fields: [
      { path: 'telegram.enable', label: '启用', type: 'bool', def: false },
    ]},
  ]},
  { id: 'platform', label: '平台', groups: [
    { title: '账号凭据', site: 'bilibili', fields: [
      { path: 'bilibili.SESSDATA', label: 'SESSDATA Cookie', type: 'credential', qrLoginOf: 'bilibili' },
      { path: 'bilibili.bili_jct', label: 'bili_jct Cookie', type: 'credential', qrLoginOf: 'bilibili' },
      { path: 'bilibili.qrlogin', label: '二维码登录', type: 'bool', def: true, hint: 'Cookie 失效后可扫码重登' },
    ]},
    { title: '轮询与推送', site: 'bilibili', fields: [
      { path: 'bilibili.interval', label: '检测间隔', type: 'text', def: '25s' },
      { path: 'bilibili.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
      { path: 'bilibili.imageMergeMode', label: '图片合并模式', type: 'select', options: ['auto','only9','off'], def: 'auto' },
      { path: 'bilibili.autoParsePosts', label: '自动解析专栏', type: 'bool', def: false },
      { path: 'bilibili.secAnalysis', label: '动态二次解析', type: 'bool', def: false },
    ]},
    { title: '关注策略', site: 'bilibili', fields: [
      { path: 'bilibili.unsub', label: '取消订阅时取关', type: 'bool', def: false },
      { path: 'bilibili.hiddenSub', label: '悄悄关注', type: 'bool', def: false },
      { path: 'bilibili.disableSub', label: '禁止自动关注', type: 'bool', def: false },
      { path: 'bilibili.minFollowerCap', label: '最低粉丝数门槛', type: 'number', def: 0, hint: '0 不限制；-1 表示无限制' },
    ]},
    { title: '运行模式', site: 'weibo', fields: [
      { path: 'weibo.mode', label: '模式', type: 'select', options: ['guest','login','api'], def: 'guest', hint: 'guest 访客 / login 账号登录 / api 外部 API' },
      { path: 'weibo.sub', label: 'SUB Cookie', type: 'credential', hint: 'login 模式凭据', qrLoginOf: 'weibo' },
      { path: 'weibo.qrlogin', label: '二维码登录', type: 'bool', def: true, hint: '仅 login 模式' },
      { path: 'weibo.autorefresh', label: 'SUB 自动刷新', type: 'bool', def: false, hint: 'login/api 模式生效' },
    ]},
    { title: 'API 模式', site: 'weibo', fields: [
      { path: 'weibo.apiModeBaseURL', label: 'API 服务地址', type: 'text', def: 'http://127.0.0.1:5000' },
    ]},
    { title: 'Cookie 告警', site: 'weibo', fields: [
      { path: 'weibo.disableCookieAlert', label: '关闭失效告警', type: 'bool', def: true },
    ]},
    { title: '推送', site: 'weibo', fields: [
      { path: 'weibo.interval', label: '访客模式刷新间隔', type: 'text', def: '30s' },
      { path: 'weibo.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: true },
    ]},
    { title: '运行模式', site: 'twitter', fields: [
      { path: 'twitter.mode', label: '模式', type: 'select', options: ['api','mirror'], def: 'api', hint: 'api 需真实账号（缺省，未配置 cookie 会私聊告警）；mirror 走 nitter 镜像、无需账号，但镜像生态已基本不可用' },
      { path: 'twitter.interval', label: '查询间隔', type: 'text', def: '30s' },
      { path: 'twitter.unsub', label: '取消订阅时取关', type: 'bool', def: false },
      { path: 'twitter.retweetFullText', label: '转发显示全文', type: 'bool', def: false },
      { path: 'twitter.userAgent', label: 'User-Agent', type: 'text', def: '' },
    ]},
    { title: 'API 模式凭据', site: 'twitter', fields: [
      { path: 'twitter.auth_token', label: 'auth_token Cookie', type: 'credential' },
      { path: 'twitter.ct0', label: 'ct0 Cookie', type: 'credential' },
      { path: 'twitter.bearerToken', label: 'Bearer Token', type: 'credential' },
      { path: 'twitter.screenName', label: '账号 screen_name', type: 'text', def: '' },
    ]},
    { title: '自动翻译', site: 'twitter', fields: [
      { path: 'twitter.translate.enabled', label: '启用推文翻译', type: 'bool', def: false },
      { path: 'twitter.translate.targetLang', label: '目标语言', type: 'text', def: 'zh' },
      { path: 'twitter.translate.minInterval', label: '翻译最小间隔', type: 'text', def: '5s' },
    ]},
    { title: 'ACFun', site: 'acfun', fields: [
      { path: 'acfun.account', label: '账号', type: 'text', def: '' },
      { path: 'acfun.password', label: '密码', type: 'credential' },
      { path: 'acfun.interval', label: '检测间隔', type: 'text', def: '25s' },
      { path: 'acfun.unsub', label: '取消订阅时取关', type: 'bool', def: false },
      { path: 'acfun.disableSub', label: '禁止自动关注', type: 'bool', def: false },
      { path: 'acfun.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
    ]},
    { title: '抖音', site: 'douyin', fields: [
      { path: 'douyin.acSignature', label: '__ac_signature Cookie', type: 'credential' },
      { path: 'douyin.acNonce', label: '__ac_nonce Cookie', type: 'credential' },
      { path: 'douyin.sessionId', label: 'sessionId Cookie', type: 'credential' },
      { path: 'douyin.userAgent', label: 'User-Agent', type: 'text', def: '' },
      { path: 'douyin.interval', label: '检测间隔', type: 'text', def: '30s' },
      { path: 'douyin.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
    ]},
    { title: '小红书', site: 'xhs', fields: [
      { path: 'xhs.cookies.a1', label: 'a1 Cookie', type: 'credential' },
      { path: 'xhs.interval', label: '检测间隔', type: 'text', def: '15s' },
      { path: 'xhs.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
    ]},
    { title: 'Twitch', site: 'twitch', fields: [
      { path: 'twitch.clientId', label: 'Client ID', type: 'text', def: '' },
      { path: 'twitch.clientSecret', label: 'Client Secret', type: 'credential' },
      { path: 'twitch.interval', label: '检测间隔', type: 'text', def: '30s' },
      { path: 'twitch.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
    ]},
    { title: 'TwitCasting', site: 'twitcasting', fields: [
      { path: 'twitcasting.clientId', label: 'Client ID', type: 'text', def: '' },
      { path: 'twitcasting.clientSecret', label: 'Client Secret', type: 'credential' },
      { path: 'twitcasting.nameStrategy', label: '名称策略', type: 'select', options: ['name','userid','both'], def: 'name' },
      { path: 'twitcasting.broadcaster.title', label: '通知含直播标题', type: 'bool', def: false },
      { path: 'twitcasting.broadcaster.created', label: '通知含开播时间', type: 'bool', def: false },
      { path: 'twitcasting.broadcaster.image', label: '通知含直播封面', type: 'bool', def: false },
    ]},
    { title: '小黑盒', site: 'xhh', fields: [
      { path: 'heybox.x_xhh_tokenid', label: 'tokenid', type: 'credential', hint: '留空自动生成' },
      { path: 'heybox.interval', label: '检测间隔', type: 'text', def: '30s' },
      { path: 'heybox.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: false },
    ]},
    { title: '斗鱼', site: 'douyu', fields: [
      { path: 'douyu.interval', label: '检测间隔', type: 'text', def: '60s' },
    ]},
    { title: '虎牙', site: 'huya', fields: [
      { path: 'huya.interval', label: '检测间隔', type: 'text', def: '60s' },
    ]},
    { title: 'YouTube', site: 'youtube', fields: [
      { path: 'youtube.onlyOnlineNotify', label: '仅推送在线期间更新', type: 'bool', def: true },
    ]},
  ]},
  { id: 'system', label: '系统', groups: [
    { title: '日志', fields: [
      { path: 'logLevel', label: '日志等级', type: 'select', options: ['trace','debug','info','warn','error'], def: 'info' },
      { path: 'qq-logs.enable', label: 'QQ 消息日志', type: 'bool', def: true },
    ]},
    { title: '功能', fields: [
      { path: 'template.enable', label: '模板功能', type: 'bool', def: false, hint: '定时任务与自动回复依赖此功能' },
      { path: 'imagePool.type', label: '图片池', type: 'select', options: ['loliconPool', 'localPool', 'off'], def: 'loliconPool' },
    ]},
  ]},
  { id: 'perm', label: '权限', perm: true },
  { id: 'templates', label: '模板', templates: true },
  { id: 'advanced', label: '高级', groups: null },
];

const SCHEMA_FIELDS = {};
for (const t of CONFIG_SCHEMA) {
  if (!t.groups) continue;
  for (const g of t.groups) for (const f of g.fields) SCHEMA_FIELDS[f.path] = f;
}

function getPath(obj, path) {
  let cur = obj;
  for (const k of path.split('.')) {
    if (cur == null || typeof cur !== 'object') return undefined;
    cur = cur[k];
  }
  return cur;
}

function setPath(obj, path, val) {
  const keys = path.split('.');
  let cur = obj;
  for (let i = 0; i < keys.length - 1; i++) {
    if (typeof cur[keys[i]] !== 'object' || cur[keys[i]] == null) cur[keys[i]] = {};
    cur = cur[keys[i]];
  }
  cur[keys[keys.length - 1]] = val;
}

function renderField(f) {
  const row = document.createElement('div');
  // set-row：一条一项的设置行——左侧标签列（含 hint），右侧控件列，行间分隔线
  row.className = 'form-row set-row';
  // 开着扫码登录时不渲染凭据输入框：这时候用户关心的是「现在登的是谁」，
  // 那两串 base64 摆在这儿没意义（登录态卡片在平台弹层顶部）。
  // 把扫码登录关掉，字段就回来。
  if (f.qrLoginOf && getPath(configCache, f.qrLoginOf + '.qrlogin') === true) return null;

  const original = getPath(configCache, f.path);
  const hintHTML = f.hint ? `<div class="field-hint">${esc(f.hint)}</div>` : '';
  const labelCol = `<div class="label-col"><label>${esc(f.label)}</label>${hintHTML}</div>`;

  if (f.type === 'credential') {
    // 可写不可读：GET 永远脱敏；留空=保持不变，输入新值=覆盖，清除=写入空串
    const configured = typeof original === 'string' && original !== '';
    const box = document.createElement('div');
    box.className = 'credential-box';
    box.innerHTML = `<input type="password" class="input" data-path="${esc(f.path)}" data-credential="1" autocomplete="new-password" ` +
      `placeholder="${configured ? '已配置（脱敏显示）—— 留空保持不变' : '未配置 —— 输入凭据后保存'}">` +
      (configured ? `<button type="button" class="btn ghost sm danger cred-clear" title="清除该凭据">清除</button>` : '');
    row.innerHTML = labelCol;
    row.appendChild(box);
    return row;
  }
  if (f.type === 'bool') {
    // 开关与输入控件同起点（控件列左缘对齐）
    row.className = 'form-row set-row set-row-bool';
    const def = original !== undefined ? original : f.def;
    row.innerHTML = labelCol;
    const wrap = document.createElement('span');
    wrap.className = 'toggle';
    wrap.innerHTML = `<input type="checkbox" data-path="${esc(f.path)}" ${def ? 'checked' : ''}><span class="toggle-slider"></span>`;
    row.appendChild(wrap);
    return row;
  }
  if (f.type === 'jsonmap') {
    // 子映射（如 customCommandPrefix）：用 JSON 文本编辑。浏览器端没有 YAML 解析器，
    // 与「高级」页整份 JSON 编辑保持同一套写法。
    const val = original !== undefined ? original : (f.def ?? {});
    const ta = document.createElement('textarea');
    ta.className = 'input code-area';
    ta.rows = 4;
    ta.spellcheck = false;
    ta.dataset.path = f.path;
    ta.value = JSON.stringify(val, null, 2);
    // 记下渲染时的原文：collect 时若没被编辑过就整块跳过。
    // 当前值可能本来就是坏的（例如全角冒号让它退化成字符串），
    // 强行解析会把「保存同页其他设置」也一起挡下来。
    ta.dataset.jsonmapOriginal = ta.value;
    row.innerHTML = labelCol;
    row.appendChild(ta);
    return row;
  }
  let input;
  if (f.type === 'select') {
    input = document.createElement('select');
    for (const o of f.options) {
      const op = document.createElement('option');
      op.value = op.textContent = o;
      input.appendChild(op);
    }
    input.value = original !== undefined ? String(original) : f.def;
  } else {
    input = document.createElement('input');
    input.type = f.type === 'number' ? 'number' : 'text';
    input.value = original !== undefined ? String(original) : (f.def ?? '');
  }
  input.className = 'input';
  input.dataset.path = f.path;
  row.innerHTML = labelCol;
  row.appendChild(input);
  return row;
}

function renderConfigForm() {
  const tabsEl = $('#config-tabs');
  const panelsEl = $('#config-panels');
  tabsEl.innerHTML = '';
  panelsEl.innerHTML = '';
  for (const tab of CONFIG_SCHEMA) {
    const btn = document.createElement('button');
    btn.className = 'tab' + (tab.id === activeConfigTab ? ' active' : '');
    btn.dataset.tab = tab.id;
    btn.textContent = tab.label;
    tabsEl.appendChild(btn);

    const panel = document.createElement('div');
    panel.className = 'card config-panel' + (tab.id === activeConfigTab ? '' : ' hidden');
    panel.id = 'config-' + tab.id;
    if (tab.perm) {
      renderPermPanel(panel);
    } else if (tab.templates) {
      renderTemplatesPanel(panel);
    } else if (tab.id === 'advanced') {
      panel.innerHTML = '<div class="tmpl-off-warn">⚠ 直接编辑完整 JSON：语法错误会导致保存失败，键名写错可能影响启动。不确定时优先使用上方分区表单。</div>' +
        '<p class="config-note">当前完整配置（敏感值已脱敏）。保存时只提交你改过的键，采用合并写入：新增和修改会生效，<strong>删掉整个键不会生效</strong>（要删键请直接编辑配置文件）。</p>' +
        '<textarea id="cfg-json" class="code-area" rows="18" spellcheck="false"></textarea>';
    } else if (tab.groups.some((g) => g.site)) {
      // 平台栏：卡片网格（左半图标区 + 右半操作区），细化配置经「配置」按钮进弹层
      const bySite = new Map(); // site -> groups[]
      for (const g of tab.groups) {
        if (!bySite.has(g.site)) bySite.set(g.site, []);
        bySite.get(g.site).push(g);
      }
      const grid = document.createElement('div');
      grid.className = 'platform-grid';
      for (const [site] of bySite) {
        const meta = PLATFORM_META[site] || { name: site, icon: 'p-generic' };
        const enabled = getPath(configCache, 'platform.' + site + '.enable') !== false; // 缺省=启用
        const card = document.createElement('div');
        card.className = 'platform-card' + (enabled ? '' : ' disabled');
        const c = PLATFORM_COLORS[site] || { pc: '#8b8fa3', pc2: '#b3b8c4' };
        card.innerHTML =
          `<svg class="p-watermark"><use href="#${meta.icon}"/></svg>` +
          `<div class="p-row">` +
          `<div class="p-id">` +
          `<svg class="p-icon"><use href="#${meta.icon}"/></svg>` +
          `<span class="p-name">${esc(meta.name)}</span>` +
          `</div>` +
          `<label class="toggle" title="停用后该平台不再推送（订阅与轮询保留，恢复后继续）">` +
          `<input type="checkbox" data-platform="${esc(site)}" ${enabled ? 'checked' : ''}>` +
          `<span class="toggle-slider"></span></label>` +
          `</div>` +
          `<div class="p-ops">` +
          (LOGIN_PLATFORM_NAME[site] ? `<button class="btn ghost sm" data-login-platform="${esc(site)}">扫码登录</button>` : '') +
          `<button class="btn ghost sm" data-edit-platform="${esc(site)}">配置</button>` +
          `</div>`;
        grid.appendChild(card);
      }
      panel.appendChild(grid);
    } else {
      for (const g of tab.groups) {
        const box = document.createElement('div');
        box.className = 'form-group';
        box.innerHTML = `<h4 class="form-group-title">${esc(g.title)}</h4>`;
        const grid = document.createElement('div');
        grid.className = 'form-grid';
        for (const f of g.fields) { const el = renderField(f); if (el) grid.appendChild(el); }
        box.appendChild(grid);
        panel.appendChild(box);
      }
    }
    panelsEl.appendChild(panel);
  }
  const adv = $('#cfg-json');
  if (adv && configCache) {
    adv.value = JSON.stringify(configCache, null, 2);
    advBase = JSON.parse(adv.value);
  }
}

async function loadConfig() {
  try {
    const cfg = await apiJSON('/api/v1/config');
    configCache = cfg;
    loadConfig._retried = false;
    renderConfigForm();
    loadPermOverview(); // 权限面板数据（面板由 renderConfigForm 生成）
  } catch (e) {
    if (e.message.includes('401')) { showLogin(); return; } // 登录失效直接回登录层
    // 网络抖动自动重试一次，重试再失败才提示。
    // _retried 只能在「成功」或「最终失败」时复位：原来放在定时器回调开头复位，
    // 递归调用永远看到 false，于是变成无限重试，用户永远等不到失败提示。
    if (!loadConfig._retried) {
      loadConfig._retried = true;
      setTimeout(loadConfig, 1500);
      return;
    }
    loadConfig._retried = false;
    toast('配置加载失败：' + e.message, false);
  }
}

// diffPatch 取 next 相对 base 的变化子集（递归；数组整体比较）。高级页编辑的是整份 JSON，
// 但只能发真正改过的键：整份回发等于拿页面加载时的旧快照，把本会话里通过别处
// （平台开关是即时保存的、基础页表单）做过的改动一起回退掉。
function diffPatch(base, next) {
  const out = {};
  for (const k of Object.keys(next)) {
    const b = base == null ? undefined : base[k];
    const n = next[k];
    if (n !== null && typeof n === 'object' && !Array.isArray(n) &&
        b !== null && typeof b === 'object' && !Array.isArray(b)) {
      const sub = diffPatch(b, n);
      if (Object.keys(sub).length) out[k] = sub;
    } else if (JSON.stringify(n) !== JSON.stringify(b)) {
      out[k] = n;
    }
  }
  return out;
}

function collectConfigPatch(tabId) {
  if (tabId === 'advanced') {
    const full = JSON.parse($('#cfg-json').value.trim() || '{}');
    if (full === null || typeof full !== 'object' || Array.isArray(full)) {
      throw new Error('高级页必须是一个 JSON 对象');
    }
    const base = advBase || configCache;
    const patch = diffPatch(base, full);
    // customCommandPrefix 后端是整表替换（不做递归合并），不能走上面那套「只发变化的子键」：
    // 子键变空（写成 {} 或把整个键删掉）在 diff 里等于「没变」，会变成
    // 「提示保存成功、条目却还在」。只要和快照不同就整表发；键被删掉按清空处理。
    if (base && JSON.stringify(base.customCommandPrefix) !== JSON.stringify(full.customCommandPrefix)) {
      patch.customCommandPrefix = full.customCommandPrefix === undefined ? {} : full.customCommandPrefix;
    }
    return patch;
  }
  return collectPatchFromContainer($('#config-' + tabId));
}

document.addEventListener('DOMContentLoaded', () => {
  // 凭据「清除/撤销」按钮（动态生成，委托到 document——覆盖配置面板与平台弹层）
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('.cred-clear');
    if (!btn) return;
    const input = btn.parentElement.querySelector('input[data-credential]');
    if (!input) return;
    if (btn.dataset.cleared !== '1') {
      btn.dataset.cleared = '1';
      btn.textContent = '撤销清除';
      input.dataset.cleared = '1';
      input.value = '';
      input.placeholder = '保存后将清除该凭据';
    } else {
      delete btn.dataset.cleared;
      delete input.dataset.cleared;
      btn.textContent = '清除';
      input.placeholder = '已配置（脱敏显示）—— 留空保持不变，输入新值覆盖';
    }
  });

  // tabs 为动态生成，用容器级事件委托
  $('#config-tabs').addEventListener('click', (e) => {
    const tab = e.target.closest('.tab');
    if (!tab) return;
    activeConfigTab = tab.dataset.tab;
    document.querySelectorAll('#config-tabs .tab').forEach((t) => t.classList.toggle('active', t === tab));
    document.querySelectorAll('.config-panel').forEach((p) => p.classList.add('hidden'));
    $('#config-' + activeConfigTab).classList.remove('hidden');
    syncSaveBtn();
  });

  // 功能开关 Tab 即时保存，保存按钮在该 Tab 下停用
  const saveBtn = $('#config-save');
  const syncSaveBtn = () => { saveBtn.disabled = activeConfigTab === 'perm' || activeConfigTab === 'templates'; };
  syncSaveBtn();

  $('#config-save').addEventListener('click', async () => {
    let patch;
    try {
      patch = collectConfigPatch(activeConfigTab);
    } catch (e) {
      toast('配置格式有误：' + e.message, false);
      return;
    }
    if (!Object.keys(patch).length) {
      toast('没有需要保存的修改');
      return;
    }
    $('#config-save').disabled = true;
    try {
      await apiJSON('/api/v1/config', {
        method: 'POST',
        body: JSON.stringify({ config: patch }),
      });
      toast('配置已保存（自动热生效；连接类配置需重启 Bot）');
      loadConfig();
    } catch (e) {
      toast('保存失败：' + e.message, false);
    } finally {
      $('#config-save').disabled = false;
    }
  });
});


/* ===== 模板编辑 ===== */
const TEMPLATE_CATEGORY_NAMES = { notify: '推送消息', command: '命令回复', trigger: '事件提醒', other: '其他' };
const TEMPLATE_CATEGORY_ORDER = ['notify', 'command', 'trigger', 'other'];
const TEMPLATE_CATEGORY_DESC = {
  notify: '订阅对象有更新时（开播/动态）推送到群里的消息格式',
  command: '群里发对应命令时 bot 的回复格式',
  trigger: '群/私聊事件（戳一戳、进退群等）触发的提醒消息格式',
};

// 模板友好名：notify.group.bilibili.live → B站 · 直播推送
function templateDisplayName(name) {
  const parts = name.split('.');
  if (parts[0] === 'notify' && parts.length >= 4) {
    const site = (PLATFORM_META[parts[2]] || {}).name || parts[2];
    const typeMap = { live: '直播', news: '动态' };
    return site + ' · ' + (typeMap[parts[3]] || parts[3]) + '推送';
  }
  const typeMap2 = { live: '直播', news: '动态' };
  if (parts[0] === 'trigger') {
    const evMap = { poke: '戳一戳', member_in: '新人进群', member_out: '成员退群', group_invited: '邀请入群', new_friend_added: '新好友' };
    const scopeMap = { group: '群内', private: '私聊' };
    const ev = parts.slice(2).map((p) => evMap[p] || p).join(' ');
    return (scopeMap[parts[1]] || parts[1]) + '事件 · ' + ev;
  }
  if (parts[0] === 'command') {
    const scopeMap = { group: '群命令', private: '私聊命令' };
    const cmd = parts.slice(2).join(' ');
    return (scopeMap[parts[1]] || parts[1]) + '「' + cmd + '」回复';
  }
  return name;
}

// 模板何时触发的中文说明
function templateWhenDesc(name) {
  const parts = name.split('.');
  if (parts[0] === 'notify' && parts[3] === 'live') {
    return '订阅的主播开播/下播时，推送到订阅群的消息格式';
  }
  if (parts[0] === 'notify' && parts[3] === 'news') {
    return '订阅的作者发布动态/视频时，推送到订阅群的消息格式';
  }
  return TEMPLATE_CATEGORY_DESC[parts[0]] || '消息模板';
}

// 常用变量中文对照（从默认模板自动提取变量名，这里给出中文含义；未收录的显示原名）
const TEMPLATE_VARS = {
  living: '是否正在直播', name: '名称/昵称', title: '标题', url: '链接', cover: '封面图',
  parent_area_name: '一级分区', area_name: '二级分区', live_time: '开播时间', content: '正文内容',
  Date: '时间', Name: '名称', User: '用户', Type: '类型', Desc: '描述', Title: '标题', Url: '链接',
  Pics: '图片列表', DescViewPictures: '配图', Src: '来源', msg: '消息', score: '积分', success: '是否成功',
  dynamic: '动态数据', Quote: '引用', Retweet: '转发', Video: '视频信息', Vote: '投票',
  OriginUser: '原作者', WithOrigin: '带原文', Base: '基础信息', Detail: '详情', CoverUrl: '封面链接',
};

// 从默认模板内容提取用到的变量（{{ .xxx }} 形式，含字段链首段）
function extractTemplateVars(defaultContent) {
  const found = new Set();
  const re = /\.([a-zA-Z_][a-zA-Z0-9_]*)/g;
  let m;
  while ((m = re.exec(defaultContent)) !== null) found.add(m[1]);
  return Array.from(found).slice(0, 24); // 展示上限，避免动态类模板几十个变量糊屏
}

function renderTemplatesPanel(panel) {
  const tplEnabled = configCache && getPath(configCache, 'template.enable');
  panel.innerHTML =
    (tplEnabled ? '' : '<div class="tmpl-off-warn">⚠ 模板功能未启用（template.enable=false）：自定义模板保存后不会生效。请到「系统 → 功能 → 模板功能」开启。</div>') +
    '<p class="config-note">各类消息的格式模板，点击任意一条进入编辑。保存后立即热生效（无需重启）。</p>' +
    '<div id="tmpl-list" class="tmpl-list"><p class="empty">加载中…</p></div>';
  loadTemplates(panel);
}

async function loadTemplates(scope) {
  const listEl = (scope || document).querySelector('#tmpl-list');
  if (!listEl) return;
  try {
    const data = await apiJSON('/api/v1/templates');
    const rows = (data.templates || []).slice().sort((a, b) => a.name.localeCompare(b.name));
    // 按类别分组渲染
    const groups = {};
    for (const t of rows) (groups[t.category] = groups[t.category] || []).push(t);
    let html = '';
    for (const cat of TEMPLATE_CATEGORY_ORDER) {
      const items = groups[cat];
      if (!items || !items.length) continue;
      html += '<div class="form-group"><h4 class="form-group-title">' + esc(TEMPLATE_CATEGORY_NAMES[cat] || cat) +
        ' <span class="perm-note">' + esc(TEMPLATE_CATEGORY_DESC[cat] || '') + '</span></h4>';
      html += items.map((t) =>
        '<div class="tmpl-item" data-tmpl="' + esc(t.name) + '">' +
        '  <div class="tmpl-item-main"><span class="tmpl-item-name">' + esc(templateDisplayName(t.name)) + '</span>' +
        '    <span class="tmpl-raw dim">' + esc(t.name) + '</span></div>' +
        '  ' + (t.customized ? '<span class="badge warn">已自定义</span>' : '<span class="badge off">默认</span>') +
        '  <svg class="btn-ic dim"><use href="#i-edit"/></svg>' +
        '</div>').join('');
      html += '</div>';
    }
    listEl.innerHTML = html || '<p class="empty">暂无模板</p>';
  } catch (e) {
    listEl.innerHTML = '<p class="empty">加载失败：' + esc(e.message) + '</p>';
  }
}

async function openTemplateModal(name) {
  const modal = $('#template-modal');
  const body = $('#template-modal-body');
  modal.classList.remove('hidden');
  modal.dataset.tmpl = name;
  body.innerHTML = '<p class="empty">加载中…</p>';
  try {
    const d = await apiJSON('/api/v1/templates/detail?name=' + encodeURIComponent(name));
    const vars = extractTemplateVars(d.default);
    $('#template-modal-title').textContent = templateDisplayName(name);
    body.innerHTML =
      '<p class="tmpl-when">' + esc(templateWhenDesc(name)) +
      ' <span class="tmpl-raw">' + esc(name) + '</span>' +
      ' ' + (d.customized ? '<span class="badge warn">已自定义</span>' : '<span class="badge off">默认</span>') + '</p>' +
      '<div class="tmpl-preview-row">' +
      '  <div class="tmpl-pane-title">效果预览 <span class="perm-note">示例数据 · 修改即时刷新</span></div>' +
      '  <div class="tmpl-chat"><div class="tmpl-msg">' +
      '    <div class="tmpl-avatar">🤖</div>' +
      '    <div class="tmpl-msg-main">' +
      '      <div class="tmpl-msg-nick">DDBOT<span class="tmpl-msg-time">' + new Date().toTimeString().slice(0, 5) + '</span></div>' +
      '      <div class="tmpl-chat-bubble" id="tmpl-preview">预览中…</div>' +
      '    </div></div></div>' +
      '</div>' +
      '<div class="tmpl-edit-row">' +
      '  <div class="tmpl-pane-title">模板内容' +
      (d.customized ? '' : ' <span class="perm-note">（默认）</span>') + '</div>' +
      '  <textarea class="input tmpl-textarea" id="tmpl-content" spellcheck="false">' + esc(d.content) + '</textarea>' +
      (vars.length ? '<details class="tmpl-vars-box"><summary>插入变量（' + vars.length + ' 个，点击插入光标处）</summary>' +
        '<div class="tmpl-vars">' +
        vars.map((v) => '<button class="chip chip-var" data-tmpl-var="' + esc(v) + '">' +
          '<code>{{ .' + esc(v) + ' }}</code><span class="chip-var-cn">' + esc(TEMPLATE_VARS[v] || v) + '</span></button>').join('') +
        '</div></details>' : '') +
      '  <div class="tmpl-editor-ops">' +
      '    <button class="btn sm primary" data-tmpl-save>保存并生效</button>' +
      (d.customized ? '<button class="btn sm ghost" data-tmpl-reset>恢复默认</button>' : '') +
      '    <button class="btn sm ghost" data-tmpl-default>填入默认模板</button>' +
      '  </div>' +
      '  <details class="tmpl-default-view"><summary>默认模板原文</summary><pre>' + esc(d.default) + '</pre></details>' +
      '</div>';
    // 打开即预览一次；编辑时防抖自动刷新
    runTemplatePreview();
    body.querySelector('#tmpl-content').addEventListener('input', scheduleTemplatePreview);
  } catch (e) {
    body.innerHTML = '<p class="empty">加载失败：' + esc(e.message) + '</p>';
  }
}

function closeTemplateModal() {
  $('#template-modal').classList.add('hidden');
  if (window.__tmplPreviewTimer) { clearTimeout(window.__tmplPreviewTimer); window.__tmplPreviewTimer = null; }
}

// 图片占位框（聊天图片消息样式）
const IMG_FRAME = '<span class="tmpl-img-frame"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="9" cy="9" r="1.6"/><path d="M21 15l-4.5-4.5L7 20"/></svg></span>';

// 预览文本 → 安全 HTML：先转义，再替换图片标记为图框、CQ 码折叠为可读标记
function renderPreviewHTML(text) {
  let html = esc(text);
  html = html.replace(/\[img\]/g, IMG_FRAME);
  html = html.replace(/\[CQ:([a-zA-Z]+)[^\]]*\]/g, (m, kind) => {
    const names = { image: '图片', face: '表情', at: '@某人', record: '语音', video: '视频', forward: '合并转发', reply: '引用回复' };
    if (kind === 'image') return IMG_FRAME;
    return '[' + (names[kind] || kind) + ']';
  });
  return html;
}

// 防抖刷新预览：编辑输入 600ms 后自动重新渲染
function scheduleTemplatePreview() {
  if (window.__tmplPreviewTimer) clearTimeout(window.__tmplPreviewTimer);
  window.__tmplPreviewTimer = setTimeout(runTemplatePreview, 600);
}

async function runTemplatePreview() {
  const modal = $('#template-modal');
  const out = modal.querySelector('#tmpl-preview');
  const content = modal.querySelector('#tmpl-content')?.value;
  if (!out || content == null) return;
  try {
    const r = await apiJSON('/api/v1/templates/preview', { method: 'POST', body: JSON.stringify({ name: modal.dataset.tmpl, content }) });
    out.innerHTML = r.rendered ? renderPreviewHTML(r.rendered) : '（此模板为空——不发送任何消息）';
    out.classList.remove('tmpl-preview-err');
  } catch (e) {
    out.textContent = e.message;
    out.classList.add('tmpl-preview-err');
  }
}
/* ===== 权限管理 ===== */

function renderPermPanel(panel) {
  panel.innerHTML =
    `<div class="form-group">` +
    `<h4 class="form-group-title">管理员</h4>` +
    `<div class="perm-chips" id="perm-admins"></div>` +
    `<div class="perm-add-row"><input class="input" id="perm-admin-input" type="text" inputmode="numeric" placeholder="QQ 号"` +
    `><button class="btn sm" data-perm-admin-add>添加管理员</button></div>` +
    `</div>` +
    `<div class="form-group">` +
    `<h4 class="form-group-title">黑名单 <span class="perm-note">（被拉黑的用户无法与 bot 交互，条目到时自动解除）</span></h4>` +
    `<div class="perm-chips" id="perm-blocks"></div>` +
    `<div class="perm-add-row"><input class="input" id="perm-block-input" type="text" inputmode="numeric" placeholder="QQ 号">` +
    `<select class="input" id="perm-block-dur" style="width:140px">` +
    `<option value="3600">1 小时</option><option value="86400" selected>1 天</option>` +
    `<option value="604800">7 天</option><option value="2592000">30 天</option></select>` +
    `<button class="btn sm" data-perm-block-add>拉黑</button></div>` +
    `</div>` +
    `<div class="form-group">` +
    `<h4 class="form-group-title">全局静默</h4>` +
    `<div class="form-row set-row set-row-bool"><div class="label-col"><label>开启全局静默</label>` +
    `<div class="field-hint">开启后 bot 在所有群停止推送，命令仍可响应</div></div>` +
    `<span class="toggle"><input type="checkbox" id="perm-silence-global"><span class="toggle-slider"></span></span></div>` +
    `</div>` +
    `<div class="form-group">` +
    `<h4 class="form-group-title">群管理</h4>` +
    `<div class="perm-add-row"><input class="input" id="perm-group-input" type="text" inputmode="numeric" placeholder="群号">` +
    `<button class="btn sm" data-perm-group-load>查看</button></div>` +
    `<div id="perm-group-area" class="hidden"></div>` +
    `</div>`;
}

function permChip(qq, delAttr) {
  return `<span class="chip">${qq}<button class="chip-x" ${delAttr}="${qq}" title="移除">×</button></span>`;
}

async function loadPermOverview() {
  try {
    const o = await apiJSON('/api/v1/permissions/overview');
    const admins = $('#perm-admins');
    if (admins) admins.innerHTML = (o.admins || []).map((qq) => permChip(qq, 'data-perm-admin-del')).join('') ||
      '<span class="perm-empty">暂无管理员（grant 命令亦可授权）</span>';
    const blocks = $('#perm-blocks');
    if (blocks) blocks.innerHTML = (o.blockList || []).map((qq) => permChip(qq, 'data-perm-block-del')).join('') ||
      '<span class="perm-empty">黑名单为空</span>';
    const gs = $('#perm-silence-global');
    if (gs) gs.checked = !!o.globalSilence;
  } catch (e) {
    toast('权限信息加载失败：' + e.message, false);
  }
}

async function loadPermGroup() {
  const groupCode = Number($('#perm-group-input').value);
  if (!groupCode) { toast('请输入群号', false); return; }
  const area = $('#perm-group-area');
  area.classList.remove('hidden');
  area.dataset.group = groupCode;
  try {
    const [ga, cmds] = await Promise.all([
      apiJSON('/api/v1/permissions/group-admins?groupCode=' + groupCode),
      apiJSON('/api/v1/commands?groupCode=' + groupCode),
    ]);
    area.innerHTML =
      `<div class="perm-sub">` +
      `<h4 class="form-group-title">群管理员</h4>` +
      `<div class="perm-chips">${(ga.groupAdmins || []).map((qq) => permChip(qq, 'data-perm-gadmin-del')).join('') || '<span class="perm-empty">暂无</span>'}</div>` +
      `<div class="perm-add-row"><input class="input" id="perm-gadmin-input" type="text" inputmode="numeric" placeholder="QQ 号">` +
      `<button class="btn sm" data-perm-gadmin-add>添加群管理员</button></div>` +
      `</div>` +
      `<div class="perm-sub">` +
      `<h4 class="form-group-title">群静默</h4>` +
      `<div class="form-row set-row set-row-bool"><div class="label-col"><label>开启本群静默</label>` +
      `<div class="field-hint">开启后本群停止推送</div></div>` +
      `<span class="toggle"><input type="checkbox" data-perm-silence-group ${ga.groupSilence ? 'checked' : ''}><span class="toggle-slider"></span></span></div>` +
      `</div>` +
      `<div class="perm-sub">` +
      `<h4 class="form-group-title">命令开关 <span class="perm-note">（全局列=所有群统一禁用；群列=仅本群的显式开关）</span></h4>` +
      `<div class="perm-cmd-table">` +
      `<div class="perm-cmd-row perm-cmd-head"><span>命令</span><span>全局</span><span>本群</span></div>` +
      (cmds.group || []).map((c) =>
        `<div class="perm-cmd-row"><span class="perm-cmd-name">${esc(c.command)}</span>` +
        `<label class="toggle sm-toggle"><input type="checkbox" data-perm-cmd-global="${esc(c.command)}" ${c.globalDisabled ? '' : 'checked'}><span class="toggle-slider"></span></label>` +
        `<label class="toggle sm-toggle"><input type="checkbox" data-perm-cmd-group="${esc(c.command)}" ${c.groupDisabled ? '' : 'checked'}><span class="toggle-slider"></span></label>` +
        `</div>`).join('') +
      `</div></div>`;
  } catch (e) {
    toast('群信息加载失败：' + e.message, false);
  }
}
/* ===== 平台卡片 ===== */
const PLATFORM_META = {
  bilibili: { name: 'B站', icon: 'p-bilibili' },
  weibo: { name: '微博', icon: 'p-weibo' },
  twitter: { name: '推特', icon: 'p-twitter' },
  youtube: { name: 'YouTube', icon: 'p-youtube' },
  douyu: { name: '斗鱼', icon: 'p-douyu' },
  huya: { name: '虎牙', icon: 'p-huya' },
  acfun: { name: 'ACFun', icon: 'p-acfun' },
  douyin: { name: '抖音', icon: 'p-douyin' },
  xhs: { name: '小红书', icon: 'p-xhs' },
  twitch: { name: 'Twitch', icon: 'p-twitch' },
  twitcasting: { name: 'TwitCasting', icon: 'p-twitcasting' },
  xhh: { name: '小黑盒', icon: 'p-heybox' },
};
const PLATFORM_COLORS = {
  bilibili: { pc: '#fb7299', pc2: '#ff9ec4' },
  weibo: { pc: '#e6162d', pc2: '#ff7a59' },
  twitter: { pc: '#1d9bf0', pc2: '#71c9ff' },
  youtube: { pc: '#ff0000', pc2: '#ff6b6b' },
  douyu: { pc: '#ff7c39', pc2: '#ffb56b' },
  huya: { pc: '#fd7e14', pc2: '#ffc04d' },
  acfun: { pc: '#fd4c5c', pc2: '#ff92a3' },
  douyin: { pc: '#fe2c55', pc2: '#25f4ee' },
  xhs: { pc: '#ff2442', pc2: '#ff8494' },
  twitch: { pc: '#9146ff', pc2: '#c9a0ff' },
  twitcasting: { pc: '#00b8e6', pc2: '#6fdcff' },
  xhh: { pc: '#5a67d8', pc2: '#8b95f0' },
};

// renderPlatformModal 打开某平台的细化配置弹层（字段组复用 renderField）
function renderPlatformModal(site) {
  const tab = CONFIG_SCHEMA.find((t) => t.id === 'platform');
  const groups = tab.groups.filter((g) => g.site === site);
  if (!groups.length) return;
  const meta = PLATFORM_META[site] || { name: site };
  $('#platform-modal-title').textContent = meta.name + ' 平台配置';
  const body = $('#platform-modal-body');
  body.innerHTML = '';
  body.dataset.site = site;
  // 顶部先放登录态卡片；下面被 qrLoginOf 标记的凭据字段会自动跳过
  const acctBox = document.createElement('div');
  acctBox.id = 'platform-account';
  body.appendChild(acctBox);
  loadPlatformAccount(site, 'platform-account');
  for (const g of groups) {
    const box = document.createElement('div');
    box.className = 'form-group';
    box.innerHTML = `<h4 class="form-group-title">${esc(g.title)}</h4>`;
    const grid = document.createElement('div');
    grid.className = 'form-grid';
    for (const f of g.fields) { const el = renderField(f); if (el) grid.appendChild(el); }
    box.appendChild(grid);
    body.appendChild(box);
  }
  const actions = document.createElement('div');
  actions.className = 'modal-actions';
  actions.innerHTML = '<button class="btn" id="platform-modal-cancel">取消</button>' +
    '<button class="btn primary" id="platform-modal-save">保存</button>';
  body.appendChild(actions);
  $('#platform-modal').classList.remove('hidden');
}

// collectPatchFromContainer 从任意容器（配置面板/平台弹层）收集 diff 补丁
function collectPatchFromContainer(container) {
  const patch = {};
  container.querySelectorAll('[data-path]').forEach((el) => {
    const path = el.dataset.path;
    const f = SCHEMA_FIELDS[path];
    if (!f) return;
    const original = getPath(configCache, path);
    if (f.type === 'jsonmap') {
      if (el.value === el.dataset.jsonmapOriginal) return;
      let obj;
      try {
        obj = el.value.trim() === '' ? {} : JSON.parse(el.value);
      } catch (e) {
        throw new Error('「' + f.label + '」不是合法 JSON：' + e.message);
      }
      if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) {
        throw new Error('「' + f.label + '」必须是一个 JSON 对象，例如 {"签到": ""}');
      }
      const base = original !== undefined ? original : (f.def ?? {});
      if (JSON.stringify(obj) !== JSON.stringify(base)) setPath(patch, path, obj);
      return;
    }
    if (f.type === 'credential') {
      if (el.dataset.cleared === '1') setPath(patch, path, '');
      else if (el.value !== '') setPath(patch, path, el.value);
      return;
    }
    let current, baseline;
    if (f.type === 'bool') {
      current = el.checked;
      baseline = original !== undefined ? !!original : !!f.def;
    } else if (f.type === 'number') {
      current = Number(el.value);
      baseline = original !== undefined ? Number(original) : f.def;
    } else {
      current = el.value;
      baseline = original !== undefined ? String(original) : String(f.def ?? '');
    }
    if (current !== baseline) setPath(patch, path, current);
  });
  return patch;
}

// setPlatformEnabled 写入 platform.<site>.enable——viper WatchConfig 热重载，
// lsp/notify.go 消费出口即时跳过停用平台的推送（订阅数据保留，恢复后继续）
async function setPlatformEnabled(site, enabled) {
  const patch = {};
  setPath(patch, 'platform.' + site + '.enable', enabled);
  try {
    await apiJSON('/api/v1/config', { method: 'POST', body: JSON.stringify({ config: patch }) });
    toast(`${enabled ? '已启用' : '已停用推送'}：${(PLATFORM_META[site] || {}).name || site}${enabled ? '' : '（订阅与轮询保留，恢复开关后继续推送）'}`);
    if (configCache) setPath(configCache, 'platform.' + site + '.enable', enabled);
  } catch (e) {
    toast('设置失败：' + e.message, false);
    loadConfig();
  }
}

/* ===== 移动端侧边栏 ===== */
function closeSidebar() {
  $('#sidebar').classList.remove('open');
  $('#sidebar-backdrop').classList.remove('show');
}

/* ===== 扫码登录 ===== */
// 面板侧只做出码与轮询：真正的登录流程在服务端（B站走 lsp/bilibili 的原语，
// 微博直接接 lsp/weibo 的 RunQRLoginForQQ），成功后由服务端写配置文件。
const LOGIN_PLATFORM_NAME = { bilibili: 'B站', weibo: '微博' };

let loginSession = null; // 服务端会话 id
let loginTimer = null;   // 轮询定时器
let loginReqSeq = 0;     // 建会话请求的序号，用来丢掉过期请求的迟到响应
let loginSite = null;    // 当前平台，供「重新获取二维码」用

// done=true 表示这次登录已经有结果了：按钮换成「重新扫码」+「关闭」。
function loginActionsHTML(done, site) {
  return '<div class="login-actions">' +
    (done
      ? '<button class="btn" data-login-platform="' + esc(site || '') + '">重新扫码</button>'
      : '<button class="btn" id="login-refresh">重新获取二维码</button>') +
    '<button class="btn' + (done ? ' primary' : '') + '" id="login-cancel">' + (done ? '关闭' : '取消') + '</button>' +
    '</div>' +
    (done ? '' : '<p class="config-note">扫码成功后服务端会直接把凭据写入配置文件并热生效，不需要再点保存。</p>');
}

// 有结果之后就把二维码换掉：码已经没用了，留着会让人以为还没走完。
// 成功时再挂一张登录态卡片，让用户能确认自己扫的是不是想登的那个号。
function loginResultHTML(kind, message, site) {
  return '<div class="login-result ' + kind + '">' +
    '<div class="login-result-icon">' + (kind === 'ok' ? '&#10003;' : '&#10005;') + '</div>' +
    '<div class="login-result-text">' + esc(message) + '</div>' +
    '</div>' +
    (kind === 'ok' && site ? '<div id="login-result-account"></div>' : '') +
    loginActionsHTML(true, site);
}

function renderLoginBody(html) {
  $('#login-modal-body').innerHTML = html;
}

function cancelLoginSession() {
  if (!loginSession) return;
  const sid = loginSession;
  loginSession = null;
  api('/api/v1/login/qrcode/cancel', { method: 'POST', body: JSON.stringify({ session: sid }) }).catch(() => {});
}

function stopLoginPolling() {
  if (loginTimer) { clearInterval(loginTimer); loginTimer = null; }
}

function closeLoginModal() {
  $('#login-modal').classList.add('hidden');
  stopLoginPolling();
  cancelLoginSession();
}

async function startPlatformLogin(site) {
  const name = LOGIN_PLATFORM_NAME[site] || site;
  stopLoginPolling();
  cancelLoginSession();
  loginSite = site;

  $('#login-modal-title').textContent = name + ' 扫码登录';
  renderLoginBody('<div class="login-qr-box"><div class="login-qr-loading">正在获取二维码…</div></div>');
  $('#login-modal').classList.remove('hidden');

  const seq = ++loginReqSeq;
  let resp;
  try {
    resp = await apiJSON('/api/v1/login/qrcode', {
      method: 'POST',
      body: JSON.stringify({ platform: site }),
    });
  } catch (e) {
    // 已经有更新的一次点击/切平台了，这次的结果作废
    if (seq !== loginReqSeq || loginSite !== site) return;
    renderLoginBody('<div class="login-qr-box"><div class="login-qr-err">' + esc(e.message) + '</div></div>' + loginActionsHTML());
    return;
  }

  if (seq !== loginReqSeq || loginSite !== site) return;
  // 连点两次扫码时两个请求会先后回来：后一个把 loginSession 覆盖掉，
  // 前一个挂的定时器就再也没人清了。这里先停一次再挂新的。
  stopLoginPolling();
  loginSession = resp.session;
  renderLoginBody(
    '<div class="login-qr-box"><img class="login-qr" alt="登录二维码" src="' + resp.image + '"></div>' +
    '<div class="login-status" id="login-status">' + esc(resp.message || '请扫码') + '</div>' +
    loginActionsHTML());
  // 把会话 id 绑进定时器：轮询回调要靠它认出「这不是我该管的会话」
  const sid = resp.session;
  loginTimer = setInterval(() => pollLoginStatus(sid), 2000);
}

// 轮询要认准自己的会话 id：切平台、重新扫码、关掉弹层之后，旧会话的迟到响应
// 不能再来动界面 —— 否则它会劫持弹层、掐掉新会话的轮询，还弹一条莫名其妙的成功提示。
async function pollLoginStatus(sid) {
  if (!sid || loginSession !== sid) return;
  let r;
  try {
    r = await apiJSON('/api/v1/login/qrcode/status?session=' + encodeURIComponent(sid));
  } catch (e) {
    return; // 网络抖动，等下一次
  }
  if (loginSession !== sid) return; // 等响应期间被切走/关掉了
  const el = $('#login-status');
  if (el && r.message) el.textContent = r.message;

  if (r.state === 'confirmed') {
    stopLoginPolling();
    loginSession = null; // 服务端已经收尾，不用再发 cancel
    renderLoginBody(loginResultHTML('ok', r.message || '登录成功，凭据已写入配置', r.platform));
    // 服务端在登录成功时已经把账号缓存失效了，这里拿到的是新账号
    loadPlatformAccount(r.platform, 'login-result-account');
    toast((LOGIN_PLATFORM_NAME[r.platform] || r.platform) + ' 登录成功，配置已更新');
    loadConfig();
  } else if (r.state === 'failed' || r.state === 'expired') {
    stopLoginPolling();
    loginSession = null;
    renderLoginBody(loginResultHTML('err', r.message || '登录未完成，请重新扫码', r.platform));
  } else if (r.state === 'scanned') {
    if (el) el.className = 'login-status scanned';
  }
}

/* ===== 平台登录态 ===== */
// 开着扫码登录时用它替掉凭据输入框：显示当前登的是谁，而不是那两串 base64。
async function loadPlatformAccount(site, mountId) {
  const box = document.getElementById(mountId);
  if (!box) return;
  let acc;
  try {
    acc = await apiJSON('/api/v1/platform/account?site=' + encodeURIComponent(site));
  } catch (e) {
    box.innerHTML = '<div class="acct-card err"><div class="acct-main">' +
      '<div class="acct-name">登录态查询失败</div><div class="acct-sub">' + esc(e.message) + '</div></div></div>';
    return;
  }
  box.innerHTML = accountCardHTML(site, acc);
}

function accountCardHTML(site, acc) {
  const label = LOGIN_PLATFORM_NAME[site] || site;
  const initial = (acc.name || label).slice(0, 1);
  const avatar = acc.avatar
    ? '<img class="acct-avatar" alt="" src="' + esc(acc.avatar) + '" referrerpolicy="no-referrer">'
    : '<div class="acct-avatar acct-avatar-empty">' + esc(initial) + '</div>';

  if (!acc.loggedIn) {
    return '<div class="acct-card">' + avatar +
      '<div class="acct-main"><div class="acct-name">未登录</div>' +
      '<div class="acct-sub">' + esc(acc.message || '尚未登录') + '</div></div>' +
      '<button class="btn ghost sm" data-login-platform="' + esc(site) + '">扫码登录</button>' +
      '</div>';
  }

  const meta = [];
  if (acc.uid) meta.push('UID ' + acc.uid);
  if (acc.mode) meta.push({ login: '登录模式', guest: '访客模式', api: 'API 模式' }[acc.mode] || acc.mode);
  if (acc.level) meta.push('LV' + acc.level);
  if (acc.vip) meta.push(acc.vip);
  return '<div class="acct-card ok">' + avatar +
    '<div class="acct-main"><div class="acct-name">' + esc(acc.name || '已登录') + '</div>' +
    '<div class="acct-sub">' + esc(meta.join(' · ') || acc.message || '已登录') + '</div></div>' +
    '<button class="btn ghost sm" data-login-platform="' + esc(site) + '">重新登录</button>' +
    '</div>';
}

/* ===== 启动 ===== */
function boot() {
  refreshDashboard();
  setInterval(refreshDashboard, 5000);
  if (!sseConnected) connectLogs();
}

document.addEventListener('DOMContentLoaded', () => {
  applyTheme(localStorage.getItem(THEME_KEY) || 'auto');

  $('#login-form').addEventListener('submit', doLogin);
  $('#logout-btn').addEventListener('click', () => { localStorage.removeItem(TOKEN_KEY); showLogin(); });
  $('#theme-toggle').addEventListener('click', () => {
    applyTheme(document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light');
  });
  $('#menu-toggle').addEventListener('click', () => {
    $('#sidebar').classList.toggle('open');
    $('#sidebar-backdrop').classList.toggle('show');
  });
  $('#sidebar-backdrop').addEventListener('click', closeSidebar);

  // 平台级开关：即时保存（委托到静态容器，覆盖动态生成的平台卡片开关）
  $('#config-panels').addEventListener('change', async (e) => {
    const input = e.target.closest('input[data-platform]');
    if (input) {
      setPlatformEnabled(input.dataset.platform, input.checked);
      const card = input.closest('.platform-card');
      if (card) card.classList.toggle('disabled', !input.checked);
      return;
    }
    // ---- 权限：静默与命令开关 ----
    const t = e.target;
    const post = (url, o) => apiJSON(url, { method: 'POST', body: JSON.stringify(o) });
    try {
      if (t.id === 'perm-silence-global') {
        await post('/api/v1/permissions/silence', { scope: 'global', action: t.checked ? 'on' : 'off' });
        toast(t.checked ? '已全局静默（所有群停止推送）' : '已解除全局静默');
      } else if (t.hasAttribute('data-perm-silence-group')) {
        const groupCode = Number($('#perm-group-input').value);
        await post('/api/v1/permissions/silence', { scope: 'group', groupCode, action: t.checked ? 'on' : 'off' });
        toast(t.checked ? '已对本群静默' : '已解除本群静默');
      } else if (t.hasAttribute('data-perm-cmd-global')) {
        // toggle 开=启用（解除全局禁用），关=全局禁用
        await post('/api/v1/commands/global', { command: t.dataset.permCmdGlobal, action: t.checked ? 'enable' : 'disable' });
        toast(`命令「${t.dataset.permCmdGlobal}」已${t.checked ? '恢复全局启用' : '全局禁用'}`);
        const area = $('#perm-group-area');
        if (area && !area.classList.contains('hidden')) await loadPermGroup(); // 全局态影响群列
      } else if (t.hasAttribute('data-perm-cmd-group')) {
        const groupCode = Number($('#perm-group-input').value);
        await post('/api/v1/commands/group', { groupCode, command: t.dataset.permCmdGroup, action: t.checked ? 'enable' : 'disable' });
        toast(`本群命令「${t.dataset.permCmdGroup}」已${t.checked ? '启用' : '禁用'}`);
      }
    } catch (err) {
      toast('操作失败：' + err.message, false);
      const area = $('#perm-group-area');
      if (area && !area.classList.contains('hidden')) await loadPermGroup(); // 失败回滚 UI
    }
  });

  // 平台卡片「配置」按钮 → 打开细化配置弹层（委托，卡片动态生成）
  $('#config-panels').addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-edit-platform]');
    if (btn) { renderPlatformModal(btn.dataset.editPlatform); return; }
    // ---- 模板列表行点击 → 弹层（弹层内操作在 #template-modal 自己的监听器里） ----
    const tmplRow = e.target.closest('.tmpl-item');
    if (tmplRow) { openTemplateModal(tmplRow.dataset.tmpl); return; }
    // ---- 权限管理（同样动态生成，统一委托） ----
    const target = e.target.closest('[data-perm-admin-add],[data-perm-admin-del],[data-perm-block-add],[data-perm-block-del],[data-perm-group-load],[data-perm-gadmin-add],[data-perm-gadmin-del]');
    if (!target) return;
    const sm = { '': '' };
    const body = (o) => ({ method: 'POST', body: JSON.stringify(o) });
    const g = () => Number($('#perm-group-input').value);
    try {
      if (target.hasAttribute('data-perm-admin-add')) {
        const qq = Number($('#perm-admin-input').value);
        if (!qq) { toast('请输入 QQ 号', false); return; }
        await apiJSON('/api/v1/permissions/admin', body({ qq, action: 'grant' }));
        $('#perm-admin-input').value = '';
      } else if (target.hasAttribute('data-perm-admin-del')) {
        await apiJSON('/api/v1/permissions/admin', body({ qq: Number(target.dataset.permAdminDel), action: 'ungrant' }));
      } else if (target.hasAttribute('data-perm-block-add')) {
        const qq = Number($('#perm-block-input').value);
        if (!qq) { toast('请输入 QQ 号', false); return; }
        const durationSec = Number($('#perm-block-dur').value);
        await apiJSON('/api/v1/permissions/blocklist', body({ qq, action: 'add', durationSec }));
        $('#perm-block-input').value = '';
      } else if (target.hasAttribute('data-perm-block-del')) {
        await apiJSON('/api/v1/permissions/blocklist', body({ qq: Number(target.dataset.permBlockDel), action: 'remove' }));
      } else if (target.hasAttribute('data-perm-group-load')) {
        await loadPermGroup();
        return; // loadPermGroup 自己渲染
      } else if (target.hasAttribute('data-perm-gadmin-add')) {
        const qq = Number($('#perm-gadmin-input').value);
        if (!qq) { toast('请输入 QQ 号', false); return; }
        await apiJSON('/api/v1/permissions/group-admin', body({ groupCode: g(), qq, action: 'grant' }));
        $('#perm-gadmin-input').value = '';
      } else if (target.hasAttribute('data-perm-gadmin-del')) {
        await apiJSON('/api/v1/permissions/group-admin', body({ groupCode: g(), qq: Number(target.dataset.permGadminDel), action: 'ungrant' }));
      }
      // 刷新对应区域
      await loadPermOverview();
      const area = $('#perm-group-area');
      if (area && !area.classList.contains('hidden')) await loadPermGroup();
      toast('操作成功');
    } catch (err) {
      toast('操作失败：' + err.message, false);
    }
  });

  // 平台弹层：关闭/保存全部委托到静态容器 #platform-modal
  // （cancel/save 按钮是弹层 body 动态生成的，初始化时不存在，不能直接绑定）
  const closePlatformModal = () => $('#platform-modal').classList.add('hidden');
  const savePlatformModal = async () => {
    const saveBtn = $('#platform-modal-save');
    if (!saveBtn) return;
    saveBtn.disabled = true;
    saveBtn.textContent = '保存中…';
    try {
      const patch = collectPatchFromContainer($('#platform-modal-body'));
      if (Object.keys(patch).length) {
        await apiJSON('/api/v1/config', { method: 'POST', body: JSON.stringify({ config: patch }) });
        toast('平台配置已保存');
      } else {
        toast('没有修改需要保存');
      }
      closePlatformModal();
      await loadConfig();
    } catch (e) {
      toast('保存失败：' + e.message, false);
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = '保存';
    }
  };
  $('#platform-modal').addEventListener('click', (e) => {
    if (e.target.closest('#platform-modal-close') || e.target.closest('#platform-modal-cancel')) closePlatformModal();
    else if (e.target.closest('#platform-modal-save')) savePlatformModal();
    else if (e.target === $('#platform-modal')) closePlatformModal();
  });

  // 扫码登录：平台卡片上的按钮 + 弹层里的操作，统一委托到 document
  document.addEventListener('click', (e) => {
    const startBtn = e.target.closest('[data-login-platform]');
    if (startBtn) { startPlatformLogin(startBtn.dataset.loginPlatform); return; }
    if (e.target.closest('#login-refresh')) { startPlatformLogin(loginSite); return; }
    if (e.target.closest('#login-modal-close') || e.target.closest('#login-cancel') || e.target === $('#login-modal')) {
      closeLoginModal();
    }
  });
  $('#template-modal').addEventListener('click', async (e) => {
    if (e.target.closest('#template-modal-close') || e.target === $('#template-modal')) { closeTemplateModal(); return; }
    const modal = $('#template-modal');
    const act = e.target.closest('[data-tmpl-save],[data-tmpl-reset],[data-tmpl-default],[data-tmpl-var]');
    if (!act) return;
    try {
      if (act.hasAttribute('data-tmpl-save')) {
        const content = $('#tmpl-content').value;
        await apiJSON('/api/v1/templates/save', { method: 'POST', body: JSON.stringify({ name: modal.dataset.tmpl, content }) });
        toast('模板已保存并热生效');
        loadTemplates();
        openTemplateModal(modal.dataset.tmpl); // 刷新弹层（customized 徽章/恢复默认按钮）
      } else if (act.hasAttribute('data-tmpl-reset')) {
        await apiJSON('/api/v1/templates/reset?name=' + encodeURIComponent(modal.dataset.tmpl), { method: 'DELETE' });
        toast('已恢复默认模板');
        loadTemplates();
        openTemplateModal(modal.dataset.tmpl);
      } else if (act.hasAttribute('data-tmpl-default')) {
        const dv = modal.querySelector('.tmpl-default-view pre');
        const ta = modal.querySelector('#tmpl-content');
        if (dv && ta) { ta.value = dv.textContent; ta.dispatchEvent(new Event('input', { bubbles: true })); }
      } else if (act.hasAttribute('data-tmpl-var')) {
        // 变量 chip 点击 → 在光标处插入 {{ .var }}
        const ta = $('#tmpl-content');
        const ins = '{{ .' + act.dataset.tmplVar + ' }}';
        const pos = ta.selectionStart == null ? ta.value.length : ta.selectionStart;
        ta.value = ta.value.slice(0, pos) + ins + ta.value.slice(ta.selectionEnd == null ? pos : ta.selectionEnd);
        ta.focus();
        ta.selectionStart = ta.selectionEnd = pos + ins.length;
      }
    } catch (err) {
      toast('操作失败：' + err.message, false);
    }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (!$('#platform-modal').classList.contains('hidden')) closePlatformModal();
    else if (!$('#login-modal').classList.contains('hidden')) closeLoginModal();
  });

  $('#log-search').addEventListener('input', rerenderLogs);
  document.querySelectorAll('#log-levels input').forEach((b) => b.addEventListener('change', rerenderLogs));
  $('#log-pause').addEventListener('click', () => {
    logPaused = !logPaused;
    $('#log-pause').textContent = logPaused ? '继续' : '暂停';
  });
  $('#log-clear').addEventListener('click', () => {
    logBuffer.length = 0;
    $('#log-stream').innerHTML = '';
    $('#dash-logs').innerHTML = '';
  });

  window.addEventListener('hashchange', route);
  route();

  bindSetupWizard();
  // 启动入口：先探测是否需要初始化（admin.token 未设置），需要则向导优先于登录层
  checkSetupThenStart();
});

async function checkSetupThenStart() {
  try {
    const r = await fetch('/api/v1/setup/status');
    const st = await r.json();
    if (st && st.needed) { showSetup(); return; }
  } catch { /* 探测失败按常规流程走 */ }
  if (token) {
    hideLogin();
    boot();
  } else {
    showLogin();
  }
}

/* ===== 初始化向导 ===== */
let setupStep = 1;
let setupMode = 'ws-server';

function showSetup() {
  const ov = $('#setup-overlay');
  ov.classList.remove('hidden');
  document.querySelector('#app').style.visibility = 'hidden';
  renderSetupSummary();
}

function hideSetup() {
  $('#setup-overlay').classList.add('hidden');
  document.querySelector('#app').style.visibility = '';
}

function setupError(step, msg) {
  const el = $('#setup-err-' + step);
  if (!el) { if (msg) toast(msg, false); return; } // 该步没有错误展示位时回退到 toast，避免静默失败
  if (!msg) { el.classList.add('hidden'); return; }
  el.textContent = msg;
  el.classList.remove('hidden');
}

function renderSetupSummary() {
  const pass = $('#setup-pass').value;
  const addr = $('#setup-ob-addr').value.trim();
  const modeName = setupMode === 'ws-server' ? '正向（Bot 监听）' : '反向（Bot 主动连）';
  $('#setup-summary').innerHTML =
    '<div class="ss-row"><span>管理密码</span><b>' + (pass ? '✓ 已设置' : '未设置') + '</b></div>' +
    (addr ? '<div class="ss-row"><span>OneBot ' + modeName + '</span><b class="mono">' + esc(addr) + '</b></div>'
          : '<div class="ss-row"><span>OneBot 连接</span><b>稍后在配置页设置</b></div>') +
    '<p class="ss-note">OneBot 连接配置写入后需重启 Bot 生效。</p>';
}

function gotoSetupStep(n) {
  setupStep = n;
  for (let i = 1; i <= 4; i++) {
    const pane = $('#setup-pane-' + i);
    pane.classList.toggle('hidden', i !== n);
    if (i === n) { pane.classList.remove('pane-in'); void pane.offsetHeight; pane.classList.add('pane-in'); }
  }
  document.querySelectorAll('#setup-steps .setup-step').forEach((el) => {
    el.classList.toggle('active', +el.dataset.step <= n);
    el.classList.toggle('current', +el.dataset.step === n);
  });
  if (n === 4) renderSetupSummary();
}

function bindSetupWizard() {
  // 步骤导航（校验只在第 1 步，其余步直接放行到 data-goto 指定的下一步）
  document.querySelectorAll('.setup-next').forEach((b) => b.addEventListener('click', () => {
    if (setupStep === 1) {
      const p = $('#setup-pass').value, p2 = $('#setup-pass2').value;
      if (p.length < 6) { setupError(1, '管理密码至少 6 位'); return; }
      if (p !== p2) { setupError(1, '两次输入的密码不一致'); return; }
      setupError(1, '');
      gotoSetupStep(2);
    } else {
      setupError(setupStep, '');
      gotoSetupStep(+b.dataset.goto);
    }
  }));
  document.querySelectorAll('.setup-prev').forEach((b) => b.addEventListener('click', () => gotoSetupStep(+b.dataset.goto)));

  // 密码可见性切换
  document.querySelectorAll('.pass-eye').forEach((b) => b.addEventListener('click', () => {
    const inp = $('#' + b.dataset.eye);
    const show = inp.type === 'password';
    inp.type = show ? 'text' : 'password';
    b.classList.toggle('on', show);
  }));

  // 主题选择：点击即时生效（向导期间就能预览亮暗）
  const syncThemeBtns = () => {
    const cur = localStorage.getItem(THEME_KEY) || 'dark';
    document.querySelectorAll('#setup-theme .setup-theme-btn').forEach((b) => {
      b.classList.toggle('active', b.dataset.themeChoice === cur);
    });
  };
  document.querySelectorAll('#setup-theme .setup-theme-btn').forEach((b) => b.addEventListener('click', () => {
    applyTheme(b.dataset.themeChoice);
    syncThemeBtns();
  }));
  syncThemeBtns();

  // 模式切换
  const syncObAddrPlaceholder = () => {
    $('#setup-ob-addr').placeholder = setupMode === 'ws-server' ? '127.0.0.1:15630' : 'ws://127.0.0.1:3001';
  };
  document.querySelectorAll('#setup-mode .setup-mode-btn').forEach((b) => b.addEventListener('click', () => {
    setupMode = b.dataset.mode;
    document.querySelectorAll('#setup-mode .setup-mode-btn').forEach((x) => x.classList.toggle('active', x === b));
    syncObAddrPlaceholder();
    setupError(2, '');
  }));
  syncObAddrPlaceholder(); // 初始同步：index.html 的静态 placeholder 是旧值

  // 提交
  $('#setup-submit').addEventListener('click', async () => {
    const btn = $('#setup-submit');
    btn.disabled = true;
    btn.textContent = '初始化中…';
    try {
      const resp = await fetch('/api/v1/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token: $('#setup-pass').value,
          obMode: $('#setup-ob-addr').value.trim() ? setupMode : '',
          obAddr: $('#setup-ob-addr').value.trim(),
          obToken: $('#setup-ob-token').value,
        }),
      });
      if (!resp.ok) {
        let msg = '初始化失败（' + resp.status + '）';
        try { const e = await resp.json(); if (e.error) msg = e.error; } catch {}
        throw new Error(msg);
      }
      // 成功：展示带「接下来去哪」指引的完成页，用户读完手动进入
      token = $('#setup-pass').value;
      localStorage.setItem(TOKEN_KEY, token);
      for (let i = 1; i <= 4; i++) $('#setup-pane-' + i).classList.add('hidden');
      const done = $('#setup-pane-done');
      done.classList.remove('hidden');
      done.classList.remove('pane-in'); void done.offsetHeight; done.classList.add('pane-in');
      const enter = () => {
        $('#setup-enter').removeEventListener('click', enter);
        hideSetup();
        hideLogin();
        boot();
        toast('初始化完成，欢迎使用');
      };
      $('#setup-enter').addEventListener('click', enter);
    } catch (e) {
      setupError(4, e.message); // 提交按钮在第 4 步，错误必须显示在该步
    } finally {
      btn.disabled = false;
      btn.textContent = '完成初始化';
    }
  });
}
