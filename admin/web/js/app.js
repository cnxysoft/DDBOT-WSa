/* DDBOT-WSa 管理面板 */
'use strict';

/* ===== 状态 ===== */
const TOKEN_KEY = 'ddbot_admin_token';
const THEME_KEY = 'ddbot_theme';
let token = localStorage.getItem(TOKEN_KEY) || '';
let authGeneration = 0;
let loginRequestSeq = 0;
let loginHideTimer = null;
let dashboardTimer = null;
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
function authSession() {
  return { token, generation: authGeneration };
}

function isCurrentAuth(session) {
  return !!session && session.generation === authGeneration && session.token === token;
}

function setAuthToken(nextToken) {
  // 即使再次登录同一个 token，也要废弃上次登录的请求和长连接。
  ++authGeneration;
  token = nextToken;
  if (token) localStorage.setItem(TOKEN_KEY, token);
  else localStorage.removeItem(TOKEN_KEY);
  if (dashboardTimer !== null) { clearInterval(dashboardTimer); dashboardTimer = null; }
  logStream.stop();
  if (window.PushPage) window.PushPage.stop();
  closeLoginModal();
  resetConfigDrafts();
  // Old save callbacks are ignored below, so release their UI controls here.
  $('#config-save').disabled = activeConfigTab === 'perm' || activeConfigTab === 'templates';
  $('#platform-modal').classList.add('hidden');
}

async function api(path, opts = {}, session = authSession()) {
  if (adminNavigation) throw new Error('管理服务地址已变更或已关闭，请按页面提示继续');
  if (!session.token) throw new Error('请先登录');
  const resp = await fetch(path, {
    ...opts,
    headers: {
      ...(opts.headers || {}),
      ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
      Authorization: 'Bearer ' + session.token,
    },
  });
  if (adminNavigation) throw new Error('管理服务地址已变更或已关闭，请按页面提示继续');
  const unauthorized = resp.status === 401 ||
    (resp.status === 403 && resp.headers.get('X-DDBOT-Auth') === 'invalid-token');
  if (unauthorized) {
    if (isCurrentAuth(session)) showLogin();
    throw new Error('登录已失效，请重新登录');
  }
  if (resp.status === 403) { throw new Error('Token 错误或无权限'); }
  return resp;
}

async function apiJSON(path, opts, session = authSession(), acceptStale = false, metadata = null) {
  const r = await api(path, opts, session);
  if (adminNavigation) throw new Error('管理服务地址已变更或已关闭，请按页面提示继续');
  if (!acceptStale && !isCurrentAuth(session)) throw new Error('登录会话已变更，旧请求已取消');
  if (!r.ok) {
    let msg = 'HTTP ' + r.status;
    try { const e = await r.json(); if (e.error) msg = e.error; } catch {}
    const error = new Error(msg);
    error.status = r.status;
    throw error;
  }
  const result = await r.json();
  if (adminNavigation) throw new Error('管理服务地址已变更或已关闭，请按页面提示继续');
  if (!acceptStale && !isCurrentAuth(session)) throw new Error('登录会话已变更，旧请求已取消');
  if (metadata) metadata.etag = r.headers?.get('ETag') || '';
  return result;
}

// 日志与推送流共用鉴权代次：注销/重新登录会终止旧连接及重连定时器。
function createAuthStream(path, onState, onEntry, isActive) {
  let connection = null;
  let retryTimer = null;

  function stop() {
    if (retryTimer !== null) { clearTimeout(retryTimer); retryTimer = null; }
    const old = connection;
    connection = null;
    if (old) {
      old.controller.abort();
      if (old.reader) old.reader.cancel().catch(() => {});
    }
    onState(false);
  }

  async function connect() {
    if (!token || adminNavigation || connection) return;
    if (retryTimer !== null) { clearTimeout(retryTimer); retryTimer = null; }
    const session = authSession();
    const current = { controller: new AbortController(), reader: null };
    connection = current;
    const live = () => connection === current && isCurrentAuth(session);
    try {
      const resp = await api(path, { signal: current.controller.signal }, session);
      if (!live()) {
        if (resp.body) await resp.body.cancel().catch(() => {});
        return;
      }
      if (!resp.ok || !resp.body) throw new Error('HTTP ' + resp.status);
      onState(true);
      const reader = current.reader = resp.body.getReader();
      const dec = new TextDecoder();
      let buf = '';
      while (live()) {
        const { done, value } = await reader.read();
        if (done || !live()) break;
        buf += dec.decode(value, { stream: true });
        let idx;
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          const data = frame.split('\n').find((line) => line.startsWith('data: '));
          if (!data) continue;
          try { onEntry(JSON.parse(data.slice(6))); } catch {}
        }
      }
    } catch (_) { /* A current, authenticated page retries transient failures below. */ }
    finally {
      if (connection === current) {
        connection = null;
        onState(false);
        if (token && !adminNavigation && isCurrentAuth(session)) {
          retryTimer = setTimeout(() => {
            retryTimer = null;
            if (token && !adminNavigation && isCurrentAuth(session) && isActive()) connect();
          }, 3000);
        }
      }
    }
  }

  return { connect, stop };
}

/* ===== 登录 ===== */
function showLogin() {
  ++loginRequestSeq;
  setAuthToken('');
  const app = $('#app');
  if (app) app.style.visibility = 'hidden';
  if (loginHideTimer !== null) { clearTimeout(loginHideTimer); loginHideTimer = null; }
  const ov = $('#login-overlay');
  if (adminNavigation) { ov.classList.add('hidden'); return; }
  ov.style.opacity = '';
  ov.style.transition = '';
  ov.classList.remove('hidden');
  $('#login-btn').disabled = false;
  $('#login-btn').textContent = '登 录';
  $('#login-token').focus();
}

function hideLogin() {
  const session = authSession();
  if (loginHideTimer !== null) clearTimeout(loginHideTimer);
  const ov = $('#login-overlay');
  ov.style.transition = 'opacity .25s';
  ov.style.opacity = '0';
  const app = $('#app');
  if (app) app.style.visibility = '';
  loginHideTimer = setTimeout(() => {
    loginHideTimer = null;
    if (!isCurrentAuth(session) || !token) return;
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
  if (adminNavigation) { showLoginError('管理服务已变更，请按页面上的持久提示手动打开管理地址'); return; }
  const t = $('#login-token').value.trim();
  if (!t) return;
  const request = ++loginRequestSeq;
  const session = authSession();
  const current = () => !adminNavigation && request === loginRequestSeq && isCurrentAuth(session);
  $('#login-btn').disabled = true;
  $('#login-btn').textContent = '登录中…';
  try {
    const resp = await fetch('/api/v1/health', {
      headers: { Authorization: 'Bearer ' + t },
    });
    if (!current()) return;
    if (resp.status === 200) {
      setAuthToken(t);
      $('#login-error').classList.add('hidden');
      hideLogin();
      boot();
    } else {
      showLoginError(resp.status === 401 || resp.status === 403 ? 'Token 不正确，请重试' : '登录失败：服务暂时不可用，请稍后重试');
    }
  } catch {
    if (current()) showLoginError('网络连接失败，请检查服务是否在运行');
  } finally {
    if (request === loginRequestSeq) {
      $('#login-btn').disabled = false;
      $('#login-btn').textContent = '登 录';
    }
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
  if (!token || adminNavigation) return;
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

/* ===== 自定义背景（ui.json 服务端持久化） ===== */
const UI_API = '/api/v1/ui';
const UI_SAVE_API = '/api/v1/ui/save';
const DEFAULT_UI = {
  backgroundUrl: '',
  backgroundBlur: 0,
  backgroundOpacity: 85,
  backgroundMask: 35,
  backgroundPosition: 'center',
  fontCustom: false,
  fontColor: '',
};
// 当前生效的 UI 外观（来自 ui.json；保存后同步更新）
let uiSettings = { ...DEFAULT_UI };
// 弹层里新选择的本地图片（压缩后的 data URL）；保存前只存在内存
let pendingBgData = '';

function uiInt(value, def, min, max) {
  const n = Number.parseInt(value, 10);
  if (!Number.isFinite(n)) return def;
  return Math.max(min, Math.min(max, n));
}

// 旧版本把背景/字体写进了 localStorage；迁移到 ui.json 后清掉旧键。
function cleanupLegacyUI() {
  [
    'ddbot_bg_url', 'ddbot_bg_blur', 'ddbot_bg_opacity', 'ddbot_bg_mask',
    'ddbot_bg_position', 'ddbot_bg_remember', 'ddbot_fg_custom', 'ddbot_fg_color',
  ].forEach((k) => {
    localStorage.removeItem(k);
    sessionStorage.removeItem(k);
  });
}

// 登录页也要显示背景，所以 GET 允许匿名；保存失败时由调用方提示。
async function loadUISettings() {
  try {
    const resp = await fetch(UI_API, { cache: 'no-store' });
    if (resp.ok) {
      const data = await resp.json();
      uiSettings = { ...DEFAULT_UI, ...data };
    }
  } catch (e) { /* 使用默认值 */ }
  applyBackground();
  applyAppearance();
}

async function saveUISettings(settings) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers.Authorization = 'Bearer ' + token;
  const resp = await fetch(UI_SAVE_API, {
    method: 'POST',
    headers,
    body: JSON.stringify(settings),
  });
  if (!resp.ok) {
    let msg = 'HTTP ' + resp.status;
    if (resp.status === 401 || resp.status === 403) {
      msg = '请先登录（远端修改外观需要 admin.token）';
    } else {
      try { const e = await resp.json(); if (e.error) msg = e.error; } catch { /* 无 JSON 错误体 */ }
    }
    throw new Error(msg);
  }
  return resp.json().catch(() => ({}));
}

function applyBackground() {
  const layer = $('#bg-layer');
  const img = $('#bg-image');
  if (!layer || !img) return;
  const url = (uiSettings.backgroundUrl || '').trim();
  const blur = uiInt(uiSettings.backgroundBlur, 0, 0, 24);
  const opacity = uiInt(uiSettings.backgroundOpacity, 85, 20, 100);
  const mask = uiInt(uiSettings.backgroundMask, 35, 0, 80);
  const position = uiSettings.backgroundPosition || 'center';
  if (!url) {
    layer.classList.add('hidden');
    document.body.classList.remove('has-custom-bg');
    img.removeAttribute('src');
    return;
  }
  layer.style.setProperty('--bg-blur', blur + 'px');
  layer.style.setProperty('--bg-opacity', String(opacity / 100));
  layer.style.setProperty('--bg-mask', String(mask / 100));
  img.style.objectPosition = position;
  img.src = url;
  layer.classList.remove('hidden');
  document.body.classList.add('has-custom-bg');
}

// 字体颜色：未自定义时完全交还给主题变量。
function applyAppearance() {
  const root = document.documentElement;
  const custom = !!uiSettings.fontCustom;
  const color = (uiSettings.fontColor || '').trim();
  if (custom && color) {
    root.style.setProperty('--text', color);
    root.style.setProperty('--muted', `color-mix(in srgb, ${color} 62%, transparent)`);
  } else {
    root.style.removeProperty('--text');
    root.style.removeProperty('--muted');
  }
}

function bgShowError(msg) {
  const el = $('#bg-error');
  if (!el) return;
  if (!msg) { el.classList.add('hidden'); return; }
  el.textContent = msg;
  el.classList.remove('hidden');
}

// 弹层里当前要预览的地址：本地图片优先，其次是图床输入框
function bgPreviewURL() {
  return pendingBgData || ($('#bg-url')?.value || '').trim();
}

function syncBgPreview() {
  const preview = $('#bg-preview');
  const url = bgPreviewURL();
  if (preview) {
    preview.style.backgroundImage = url ? 'url(' + JSON.stringify(url) + ')' : '';
    preview.style.backgroundPosition = $('#bg-position')?.value || 'center';
    preview.classList.toggle('has-image', !!url);
  }
  bgShowError('');
}

function openBgModal() {
  if (!$('#bg-modal')) return;
  const stored = (uiSettings.backgroundUrl || '').trim();
  // 已保存的是本地压缩图时，不把它塞进 URL 输入框（那串 base64 太长）
  pendingBgData = stored.startsWith('data:image/') ? stored : '';
  $('#bg-url').value = pendingBgData ? '' : stored;
  $('#bg-file').value = '';
  $('#bg-file-name').textContent = pendingBgData ? '当前使用本地上传图片' : '';
  $('#bg-blur').value = String(uiInt(uiSettings.backgroundBlur, 0, 0, 24));
  $('#bg-opacity').value = String(uiInt(uiSettings.backgroundOpacity, 85, 20, 100));
  $('#bg-mask').value = String(uiInt(uiSettings.backgroundMask, 35, 0, 80));
  $('#bg-position').value = uiSettings.backgroundPosition || 'center';
  const customFg = !!uiSettings.fontCustom;
  $('#fg-custom').checked = customFg;
  $('#fg-color').value = (uiSettings.fontColor || '').trim()
    || (document.documentElement.getAttribute('data-theme') === 'light' ? '#3f2b4a' : '#f7f2ff');
  syncBgPreview();
  $('#bg-modal').classList.remove('hidden');
}

function closeBgModal() {
  $('#bg-modal')?.classList.add('hidden');
}

// 图床直链校验：先看协议，再实际加载一次图片，避免保存一个永远显示不出来的地址。
function testBgImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.referrerPolicy = 'no-referrer';
    const timer = setTimeout(() => {
      img.onload = img.onerror = null;
      img.src = '';
      reject(new Error('图片加载超时，请检查图床直链是否可访问'));
    }, 8000);
    img.onload = () => { clearTimeout(timer); resolve(); };
    img.onerror = () => { clearTimeout(timer); reject(new Error('图片加载失败：请确认是可直接访问的图床直链')); };
    img.src = url;
  });
}

// 本地图片压缩：最长边 1920，优先 webp，再退 jpeg。
// 背景图存 localStorage，超过几 MB 会触发配额错误，所以这里主动压小。
function compressImageFile(file) {
  return new Promise((resolve, reject) => {
    if (!file.type.startsWith('image/')) { reject(new Error('请选择图片文件')); return; }
    if (file.size > 12 * 1024 * 1024) { reject(new Error('图片太大（最大 12MB）')); return; }
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('读取本地图片失败'));
    reader.onload = () => {
      const img = new Image();
      img.onerror = () => reject(new Error('图片格式不支持'));
      img.onload = () => {
        const max = 1920;
        const scale = Math.min(1, max / Math.max(img.width, img.height));
        const w = Math.max(1, Math.round(img.width * scale));
        const h = Math.max(1, Math.round(img.height * scale));
        const canvas = document.createElement('canvas');
        canvas.width = w;
        canvas.height = h;
        const ctx = canvas.getContext('2d');
        // 透明图转 jpeg 时用白底，避免变黑
        ctx.fillStyle = '#fff';
        ctx.fillRect(0, 0, w, h);
        ctx.drawImage(img, 0, 0, w, h);
        let data = canvas.toDataURL('image/webp', 0.85);
        if (!data.startsWith('data:image/webp')) data = canvas.toDataURL('image/jpeg', 0.85);
        if (data.length > 3_800_000) data = canvas.toDataURL('image/jpeg', 0.62);
        if (data.length > 4_500_000) { reject(new Error('图片压缩后仍然过大，请换一张小一点的图片')); return; }
        resolve(data);
      };
      img.src = reader.result;
    };
    reader.readAsDataURL(file);
  });
}

async function saveBackground() {
  const url = bgPreviewURL();
  const saveBtn = $('#bg-save');
  if (url && !url.startsWith('data:image/')) {
    let parsed;
    try { parsed = new URL(url); } catch { bgShowError('请输入合法的图片 URL（https://...）'); return; }
    if (!/^https?:$/.test(parsed.protocol)) { bgShowError('只支持 http / https 图床直链'); return; }
    saveBtn.disabled = true;
    saveBtn.textContent = '加载中…';
    try {
      await testBgImage(url);
    } catch (e) {
      bgShowError(e.message);
      return;
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = '应用背景';
    }
  }

  const payload = {
    backgroundUrl: url,
    backgroundBlur: uiInt($('#bg-blur').value, 0, 0, 24),
    backgroundOpacity: uiInt($('#bg-opacity').value, 85, 20, 100),
    backgroundMask: uiInt($('#bg-mask').value, 35, 0, 80),
    backgroundPosition: $('#bg-position').value || 'center',
    fontCustom: !!$('#fg-custom').checked,
    fontColor: ($('#fg-color').value || '').trim(),
  };
  saveBtn.disabled = true;
  saveBtn.textContent = '保存中…';
  try {
    await saveUISettings(payload);
  } catch (e) {
    bgShowError('保存到 ui.json 失败：' + e.message);
    return;
  } finally {
    saveBtn.disabled = false;
    saveBtn.textContent = '应用背景';
  }
  uiSettings = { ...DEFAULT_UI, ...payload };
  pendingBgData = '';
  applyBackground();
  applyAppearance();
  closeBgModal();
  toast('外观已保存到 ui.json');
}

async function resetBackground() {
  const resetBtn = $('#bg-reset');
  if (resetBtn) resetBtn.disabled = true;
  try {
    await saveUISettings({ ...DEFAULT_UI });
  } catch (e) {
    bgShowError('恢复默认失败：' + e.message);
    return;
  } finally {
    if (resetBtn) resetBtn.disabled = false;
  }
  uiSettings = { ...DEFAULT_UI };
  pendingBgData = '';
  if ($('#bg-url')) $('#bg-url').value = '';
  if ($('#bg-file')) $('#bg-file').value = '';
  if ($('#bg-file-name')) $('#bg-file-name').textContent = '';
  if ($('#bg-blur')) $('#bg-blur').value = '0';
  if ($('#bg-opacity')) $('#bg-opacity').value = '85';
  if ($('#bg-mask')) $('#bg-mask').value = '35';
  if ($('#bg-position')) $('#bg-position').value = 'center';
  if ($('#fg-custom')) $('#fg-custom').checked = false;
  if ($('#fg-color')) $('#fg-color').value = document.documentElement.getAttribute('data-theme') === 'light' ? '#3f2b4a' : '#f7f2ff';
  syncBgPreview();
  applyBackground();
  applyAppearance();
  closeBgModal();
  toast('已恢复默认外观并写回 ui.json');
}

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
  if (!token || adminNavigation) return;
  const session = authSession();
  try {
    const [ob, st, sum] = await Promise.all([
      apiJSON('/api/v1/onebot/status'),
      apiJSON('/api/v1/status'),
      apiJSON('/api/v1/subs/summary'),
    ]);
    if (!isCurrentAuth(session)) return;
    $('#ob-status').className = 'dot ' + (ob.online ? 'on' : 'off');
    $('#ob-text').textContent = ob.online ? '在线' : '离线';
    // 接入方式由后端 adapter.mode 决定：同一份面板可以跑 OneBot / QQ 开放平台 / Satori
    const adapterName = st.adapterMode === 'qqofficial' ? 'QQ 开放平台'
      : st.adapterMode === 'satori' ? 'Satori' : 'OneBot 11';
    $('#ob-label').textContent = adapterName + ' 连接';
    const adapterBadge = $('#adapter-badge');
    if (adapterBadge) adapterBadge.textContent = adapterName;
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
    if (!isCurrentAuth(session)) return;
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

const logStream = createAuthStream('/api/v1/logs/stream', setSSE, appendLog,
  () => $('#page-logs')?.classList.contains('active'));

function connectLogs() { return logStream.connect(); }

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
const configDrafts = new Map();
let advancedDraft = null;
let configETag = '';
let configSourceEpoch = 0;
const ownConfigVersions = new Map();
let configConflictMessage = '';
let adminNavigation = null;

function cloneConfig(value) { return value === undefined ? undefined : JSON.parse(JSON.stringify(value)); }
function sameConfig(a, b) { return JSON.stringify(a) === JSON.stringify(b); }
function draftControls(container = document) { return Array.from(container.querySelectorAll('[data-path]')); }
function draftRaw(el) { return { value: el.value || '', checked: !!el.checked, cleared: el.dataset.cleared === '1' }; }
function configFieldSchema(path) {
  return SCHEMA_FIELDS[path] || (/^platform\.[^.]+\.enable$/.test(path) ? { path, type: 'bool', def: true } : null);
}

function captureConfigDrafts() {
  for (const el of draftControls()) {
    if (!el.dataset.draftOriginal) continue;
    const path = el.dataset.path, raw = draftRaw(el);
    if (sameConfig(raw, JSON.parse(el.dataset.draftOriginal))) { configDrafts.delete(path); continue; }
    const old = configDrafts.get(path);
    configDrafts.set(path, { raw, base: old ? old.base : cloneConfig(getPath(configCache, path)) });
  }
  const adv = $('#cfg-json');
  if (adv && adv.dataset.draftOriginal !== undefined) {
    advancedDraft = adv.value === adv.dataset.draftOriginal ? null : {
      raw: adv.value, base: advancedDraft ? advancedDraft.base : cloneConfig(advBase),
    };
  }
}

function restoreConfigDrafts(container = document) {
  for (const el of draftControls(container)) {
    const path = el.dataset.path;
    el.dataset.draftOriginal = JSON.stringify(draftRaw(el));
    const draft = configDrafts.get(path);
    if (!draft) continue;
    el.value = draft.raw.value;
    el.checked = draft.raw.checked;
    if (draft.raw.cleared) el.dataset.cleared = '1'; else delete el.dataset.cleared;
    const clear = el.parentElement?.querySelector('.cred-clear');
    if (clear) clear.textContent = draft.raw.cleared ? '撤销清除' : '清除';
    if (draft.raw.cleared) el.placeholder = '保存后将清除该凭据';
  }
  const adv = $('#cfg-json');
  if (adv && (container === document || container.id === 'config-panels')) {
    adv.dataset.draftOriginal = adv.value;
    if (advancedDraft) { adv.value = advancedDraft.raw; advBase = cloneConfig(advancedDraft.base); }
  }
  renderConfigDraftStatus();
}

function renderConfigDraftStatus() {
  const el = $('#config-draft-status');
  if (!el) return;
  const changed = [...configDrafts].filter(([path, draft]) => !sameConfig(draft.base, getPath(configCache, path)))
    .map(([path]) => path);
  if (advancedDraft && !sameConfig(advancedDraft.base, configCache)) changed.push('高级 JSON');
  const count = configDrafts.size + (advancedDraft ? 1 : 0);
  el.textContent = configConflictMessage || (changed.length
    ? '配置来源已有更新，已保留你的草稿。请核对后手动保存：' + changed.join('、')
    : count ? `有 ${count} 处未保存草稿；刷新配置和扫码登录不会清除。` : '');
  el.classList.toggle('hidden', !el.textContent);
}

function resetConfigDrafts() {
  configDrafts.clear(); advancedDraft = null; configCache = null; advBase = null;
  configETag = ''; ++configSourceEpoch; ownConfigVersions.clear(); configConflictMessage = '';
  $('#config-panels').innerHTML = '';
  $('#platform-modal-body').innerHTML = '';
  renderConfigDraftStatus();
}

function patchHasPath(patch, path) {
  let current = patch;
  for (const key of path.split('.')) {
    if (current === null || typeof current !== 'object' || !Object.hasOwn(current, key)) return false;
    current = current[key];
  }
  return true;
}

function mergeConfigPatch(base, patch) {
  const out = cloneConfig(base) || {};
  for (const [key, value] of Object.entries(patch)) {
    out[key] = value && typeof value === 'object' && !Array.isArray(value) && key !== 'customCommandPrefix'
      ? mergeConfigPatch(out[key], value) : cloneConfig(value);
  }
  return out;
}

function configSaveSnapshot(patch, advanced) {
  captureConfigDrafts();
  return { fields: new Map(draftControls().filter((el) => patchHasPath(patch, el.dataset.path))
    .filter((el) => {
      const f = configFieldSchema(el.dataset.path);
      if (!f) return false;
      let value = f.type === 'bool' ? el.checked : f.type === 'number' ? Number(el.value) : el.value;
      if (f.type === 'credential' && el.dataset.cleared === '1') value = '';
      if (f.type === 'jsonmap') { try { value = JSON.parse(el.value || '{}'); } catch { return false; } }
      return sameConfig(value, getPath(patch, el.dataset.path));
    }).map((el) => [el.dataset.path, draftRaw(el)])), advanced: advanced && advancedDraft ? cloneConfig(advancedDraft) : null };
}

function acknowledgeConfigSave(patch, sent) {
  captureConfigDrafts();
  const safePatch = cloneConfig(patch);
  for (const [path, field] of Object.entries(SCHEMA_FIELDS)) {
    if (field.type === 'credential' && patchHasPath(safePatch, path)) {
      setPath(safePatch, path, getPath(safePatch, path) ? '***REDACTED***' : '');
    }
  }
  configCache = mergeConfigPatch(configCache, safePatch);
  for (const [path, submitted] of sent.fields) {
    const draft = configDrafts.get(path);
    if (!draft || sameConfig(draft.raw, submitted)) configDrafts.delete(path);
    else draft.base = cloneConfig(getPath(configCache, path));
  }
  for (const el of draftControls()) {
    const path = el.dataset.path, submitted = sent.fields.get(path);
    if (!submitted) continue;
    const raw = draftRaw(el), credential = configFieldSchema(path)?.type === 'credential';
    const baseline = credential ? { value: '', checked: false, cleared: false } : submitted;
    el.dataset.draftOriginal = JSON.stringify(baseline);
    if (sameConfig(raw, submitted)) {
      configDrafts.delete(path);
      if (credential) { el.value = ''; delete el.dataset.cleared; }
    } else configDrafts.set(path, { raw, base: cloneConfig(getPath(configCache, path)) });
  }
  if (sent.advanced && advancedDraft) {
    if (advancedDraft.raw === sent.advanced.raw) {
      advancedDraft = null;
      const adv = $('#cfg-json');
      if (adv) { adv.value = JSON.stringify(configCache, null, 2); adv.dataset.draftOriginal = adv.value; }
      advBase = cloneConfig(configCache);
    } else {
      advancedDraft.base = mergeConfigPatch(advancedDraft.base, safePatch);
      advBase = cloneConfig(advancedDraft.base);
    }
  }
  configConflictMessage = '';
  renderConfigDraftStatus();
}

function adminNavigationURL(addr, current = location) {
  const match = /^(?:\[([0-9a-fA-F:.]+)\]|([a-zA-Z0-9.-]*)):(\d+)$/.exec(addr || '');
  if (!match || Number(match[3]) < 1 || Number(match[3]) > 65535) return null;
  let host = match[1] ?? match[2];
  if (!host || host === '0.0.0.0' || host === '::') host = current.hostname.replace(/^\[|\]$/g, '');
  if (!/^[a-zA-Z0-9.:-]+$/.test(host)) return null;
  try {
    const url = new URL('http://' + (host.includes(':') ? '[' + host + ']' : host) + ':' + match[3] + '/');
    url.hash = '/config';
    return url.href;
  } catch { return null; }
}

function applyAdminNavigation(navigation, fromSave = true) {
  const action = fromSave ? '配置已保存并生效。' : '配置已重载。';
  captureConfigDrafts();
  closeLoginModal();
  adminNavigation = { enabled: !!navigation.enabled, addr: String(navigation.addr || '') };
  ++loginRequestSeq;
  ++reloadStatusRequest; ++configLoadRequest;
  if (dashboardTimer !== null) { clearInterval(dashboardTimer); dashboardTimer = null; }
  logStream.stop();
  if (window.PushPage) window.PushPage.stop();
  const banner = $('#admin-navigation');
  banner.replaceChildren();
  const title = document.createElement('strong');
  title.textContent = adminNavigation.enabled ? '管理服务监听地址已变更' : '管理服务已关闭';
  banner.appendChild(title);
  const message = document.createElement('p');
  message.textContent = adminNavigation.enabled
    ? action + '本页面已停止请求旧地址；请核对新地址后手动打开，未保存草稿仍保留在此页。'
    : action + '本页面已停止连接。重新启用时，请在 application.yaml 中设置 admin.enable: true；配置文件热更新会重新开启管理服务。未保存草稿仍保留在此页。';
  banner.appendChild(message);
  if (adminNavigation.enabled) {
    const addr = document.createElement('p');
    addr.textContent = '当前监听：' + adminNavigation.addr;
    banner.appendChild(addr);
    const href = adminNavigationURL(adminNavigation.addr);
    if (href) {
      const link = document.createElement('a');
      link.href = href; link.target = '_blank'; link.rel = 'noopener noreferrer';
      link.className = 'btn sm'; link.textContent = '手动打开新管理地址';
      banner.appendChild(link);
    }
    const proxy = document.createElement('p');
    proxy.textContent = '以上链接用于 HTTP 直连。若通过 HTTPS、反向代理或子路径访问，请先检查代理目标与公开访问地址，再手动打开；登录凭据不会放入链接。';
    banner.appendChild(proxy);
  }
  banner.classList.remove('hidden');
  $('#config-save').disabled = true;
}

/* 字段类型：text / number / bool(toggle) / select / credential(只写凭据)。
 * 平台凭据只展示脱敏状态，允许覆盖或显式清除；管理及 WebSocket token 禁止通过 API 修改。 */
function normalizeImageMergeOption(value) {
  const normalized = String(value).trim().toLowerCase();
  if (normalized === 'false') return 'off';
  if (normalized === 'true') return 'always';
  return normalized || 'inherit';
}

function normalizeImageMergeDimension(value) {
  const text = String(value).trim();
  const dimension = /^\+?\d+$/.test(text) ? Number(text) : NaN;
  return Number.isInteger(dimension) && dimension >= 0 ? Math.min(dimension, 29999) : 0;
}

function imageMergeGroup(site) {
  const prefix = site ? site + '.imageMerge' : 'imageMerge';
  return { title: site ? '图片合并' : '图片合并（全局覆盖）', ...(site ? { site } : {}), fields: [
    { path: prefix + '.mode', label: '合并条件', type: 'select', def: 'inherit',
      options: ['inherit', 'off', 'auto', 'only9', 'always'], normalize: normalizeImageMergeOption,
      optionLabels: { inherit: site ? '继承默认' : '按站点设置', off: '关闭', auto: '自动判断', only9: '仅九张', always: '两张起合并' },
      hint: site ? '全局合并条件优先。继承默认时，B站沿用旧版合并设置，其他站点关闭。自动判断沿用B站规则。' :
        '默认按站点设置；选择其他值将覆盖所有站点的合并条件。自动判断沿用B站规则。原始单图和GIF保持原有行为。' },
    { path: prefix + '.layout', label: '合并布局', type: 'select', def: 'inherit',
      options: ['inherit', 'grid', 'long'], normalize: normalizeImageMergeOption,
      optionLabels: { inherit: site ? '继承默认（九宫格）' : '按站点设置', grid: '九宫格', long: '长图' },
      hint: (site ? '全局布局优先；开启合并后生效。' : '与合并条件分别覆盖；开启合并后生效。') +
        '九宫格裁成方形；长图保留完整内容，窄图居中且不放大，超限自动分页。每张小于30MB，官方通道还受5MiB直传预算约束。' },
    { path: prefix + '.maxWidth', label: '最大宽度（高级）', type: 'number', def: 0, min: 0, max: 29999,
      normalize: normalizeImageMergeDimension,
      hint: (site ? '全局非零值优先。' : '非零值覆盖各站点。') +
        '0为继承，最终默认1440像素；最大29999。长图不放大窄图，宽图按比例缩小。' },
    { path: prefix + '.maxHeight', label: '每页最大高度（高级）', type: 'number', def: 0, min: 0, max: 29999,
      normalize: normalizeImageMergeDimension,
      hint: (site ? '全局非零值优先。' : '非零值覆盖各站点。') +
        '0为继承，最终默认16000像素；可按需调到28000，最大29999。较大尺寸增加内存开销；每页仍受像素及30MB预算约束，可能提前分页。' },
  ]};
}

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
    imageMergeGroup(),
  ]},
  { id: 'network', label: '网络', groups: [
    { title: '接入方式', fields: [
      { path: 'adapter.mode', label: '接入方式', type: 'select',
        options: ['onebot-v11', 'qqofficial', 'satori'], def: 'onebot-v11',
        optionLabels: {
          'onebot-v11': 'OneBot 11（NapCat / LLOneBot / Lagrange）',
          qqofficial: 'QQ 开放平台（官方机器人）',
          satori: 'Satori（兼容实现端）',
        },
        hint: '决定 Bot 用什么协议连 IM。同协议的连接配置可在线更新；跨协议切换需迁移运行数据。' },
    ]},
    { title: 'OneBot 11 / Satori 连接', fields: [
      { path: 'websocket.mode', label: '连接方向', type: 'select', options: ['ws-server','ws-reverse'], def: 'ws-server',
        optionLabels: { 'ws-server': '正向连接（Bot 监听）', 'ws-reverse': '反向连接（Bot 主动连）' } },
      { path: 'websocket.ws-server', label: '正向监听地址', type: 'text', def: '127.0.0.1:15630' },
      { path: 'websocket.ws-reverse', label: '反向连接地址', type: 'text', def: 'ws://localhost:3001' },
      { path: 'websocket.token', label: 'Access Token', type: 'credential', hint: '正向监听非回环地址时必须配置，否则 Bot 会拒绝启动' },
    ]},
    { title: 'QQ 开放平台', qqofficial: true, fields: [
      { path: 'qqofficial.app-id', label: 'AppID', type: 'text', def: '', hint: '机器人管理端（q.qq.com）的 AppID，纯数字' },
      { path: 'qqofficial.app-secret', label: 'AppSecret', type: 'credential', hint: '等同凭据：读取时脱敏，留空保持不变；扫码绑定会自动写入' },
      { path: 'qqofficial.mode', label: '连接方式', type: 'select', options: ['websocket'], def: 'websocket',
        optionLabels: { websocket: 'WebSocket 长连接（当前唯一实现）' }, hint: 'webhook 尚未实现，不要手改成 webhook' },
      { path: 'qqofficial.qrlogin', label: '显示扫码绑定', type: 'bool', def: true, hint: '关掉后只保留手动填写凭据' },
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
      { path: 'bilibili.imageMergeMode', label: '旧版图片合并模式', type: 'select', options: ['auto','only9','always','off','inherit'], def: 'auto',
        normalize: normalizeImageMergeOption, hint: '兼容已有配置；仅在全局及B站新的合并条件均为继承时生效。' },
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
      { path: 'twitter.mode', label: '模式', type: 'select', options: ['mirror','api'], def: 'mirror', hint: 'mirror 默认无需账号，使用可配置的 Nitter 实例，失败时自动切换并暂时跳过故障实例；api 需账号 Cookie' },
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
    ...SITES.map(imageMergeGroup),
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

// 接入方式展示名：配置页顶部提示条与仪表盘徽章共用
const ADAPTER_NAMES = { 'onebot-v11': 'OneBot 11', qqofficial: 'QQ 开放平台', satori: 'Satori' };

function adapterBannerHTML(mode) {
  const key = String(mode || 'onebot-v11');
  const name = ADAPTER_NAMES[key] || key;
  const icon = key === 'qqofficial' ? 'i-robot' : 'i-plug';
  return `<span class="adapter-banner-icon"><svg class="btn-ic"><use href="#${icon}"/></svg></span>` +
    `<span class="adapter-banner-main">配置中的接入方式：<b>${esc(name)}</b><small>同协议的连接配置可在线更新；跨协议切换涉及运行数据迁移</small></span>` +
    `<span class="adapter-banner-tag">${key === 'qqofficial' ? '官方机器人' : key === 'satori' ? 'Satori 协议' : 'OneBot 协议'}</span>`;
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

function configFieldValue(f) {
  const original = getPath(configCache, f.path);
  return original !== undefined && f.normalize ? f.normalize(original) : original;
}

function renderField(f) {
  const row = document.createElement('div');
  // set-row：一条一项的设置行——左侧标签列（含 hint），右侧控件列，行间分隔线
  row.className = 'form-row set-row';
  // 开着扫码登录时不渲染凭据输入框：这时候用户关心的是「现在登的是谁」，
  // 那两串 base64 摆在这儿没意义（登录态卡片在平台弹层顶部）。
  // 把扫码登录关掉，字段就回来。
  if (f.qrLoginOf && getPath(configCache, f.qrLoginOf + '.qrlogin') === true) return null;

  const original = configFieldValue(f);
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
      op.value = o;
      op.textContent = f.optionLabels?.[o] ?? o;
      input.appendChild(op);
    }
    input.value = original !== undefined ? String(original) : f.def;
  } else {
    input = document.createElement('input');
    input.type = f.type === 'number' ? 'number' : 'text';
    input.value = original !== undefined ? String(original) : (f.def ?? '');
    if (f.min !== undefined) input.min = f.min;
    if (f.max !== undefined) input.max = f.max;
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
        // 平台卡片的粉彩渐变由 --pc/--pc2 驱动；不设的话所有卡片都会退化成主题色
        card.style.setProperty('--pc', c.pc);
        card.style.setProperty('--pc2', c.pc2);
        card.innerHTML =
          `<svg class="p-watermark"><use href="#${meta.icon}"/></svg>` +
          `<div class="p-row">` +
          `<div class="p-id">` +
          `<svg class="p-icon"><use href="#${meta.icon}"/></svg>` +
          `<span class="p-name">${esc(meta.name)}</span>` +
          `</div>` +
          `<label class="toggle" title="停用后该平台不再推送（订阅与轮询保留，恢复后继续）">` +
          `<input type="checkbox" data-platform="${esc(site)}" data-path="platform.${esc(site)}.enable" ${enabled ? 'checked' : ''}>` +
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
      if (tab.id === 'network') {
        // 顶部提示条：把「当前生效的是哪套接入方式」摆在最显眼的位置，
        // 免得改完 OneBot 地址才发现 Bot 其实跑在 QQ 开放平台上。
        const banner = document.createElement('div');
        banner.className = 'adapter-banner';
        banner.id = 'adapter-banner';
        banner.innerHTML = adapterBannerHTML(getPath(configCache, 'adapter.mode'));
        panel.appendChild(banner);
      }
      for (const g of tab.groups) {
        const box = document.createElement('div');
        box.className = 'form-group';
        box.innerHTML = `<h4 class="form-group-title">${esc(g.title)}</h4>`;
        // QQ 官方：顶部挂登录态卡片 + 扫码绑定入口（与平台弹层同一套账号卡片）
        const showQQAccount = g.qqofficial && getPath(configCache, 'qqofficial.qrlogin') !== false;
        if (showQQAccount) {
          const acct = document.createElement('div');
          acct.id = 'qqofficial-account';
          acct.className = 'qq-account';
          box.appendChild(acct);
        }
        const grid = document.createElement('div');
        grid.className = 'form-grid';
        for (const f of g.fields) { const el = renderField(f); if (el) grid.appendChild(el); }
        box.appendChild(grid);
        panel.appendChild(box);
        // panel 本身要等循环结束才挂进 #config-panels，这里必须延后一拍，
        // 否则 getElementById 拿不到刚创建的账号卡片（表现为卡片空白）。
        if (showQQAccount) setTimeout(() => loadPlatformAccount('qqofficial', 'qqofficial-account'), 0);
      }
    }
    panelsEl.appendChild(panel);
  }
  const adv = $('#cfg-json');
  if (adv && configCache) {
    adv.value = JSON.stringify(configCache, null, 2);
    advBase = JSON.parse(adv.value);
  }
  restoreConfigDrafts(panelsEl);
}

let reloadStatusRequest = 0;
let configLoadRequest = 0;
let configSaveQueue = Promise.resolve();

function renderReloadStatus(status) {
  const target = $('#config-reload-status');
  if (!target || !status) return;
  const pending = status.restartRequired || [];
  const details = pending.length ? '需重启：' + pending.join('、') : '支持热更新的配置已应用；轮询间隔在下一轮调度时生效';
  target.textContent = status.lastError || `配置版本 ${status.revision}；${details}`;
}

async function refreshReloadStatus() {
  if (adminNavigation || !token) return;
  const request = ++reloadStatusRequest;
  const session = authSession();
  try {
    const status = await apiJSON('/api/v1/config/status', {}, session);
    if (!adminNavigation && request === reloadStatusRequest && isCurrentAuth(session)) renderReloadStatus(status);
  } catch (_) { /* Keep the last known state on transient errors. */ }
}

function saveConfigPatch(patch, options = {}) {
  const session = authSession();
  const sent = configSaveSnapshot(patch, options.advanced);
  const originalETag = configETag;
  const sourceEpoch = configSourceEpoch;
  // A save invalidates status queries that started before it.
  ++reloadStatusRequest;
  ++configLoadRequest;
  const body = JSON.stringify({ config: patch });
  // Serialize all configuration forms and platform toggles in user action order.
  const save = configSaveQueue.then(() => {
    if (!isCurrentAuth(session)) throw new Error('登录会话已变更，旧配置保存已取消');
    if (adminNavigation) throw new Error('管理服务地址已变更或已关闭，请按页面提示继续');
    if (sourceEpoch !== configSourceEpoch) {
      throw new Error('配置来源已变更，已取消排队中的旧保存；草稿已保留，请核对后手动保存');
    }
    ++reloadStatusRequest;
    ++configLoadRequest;
    const metadata = {};
    const etag = ownConfigVersions.get(originalETag) || originalETag;
    const headers = etag ? { 'If-Match': etag } : {};
    return apiJSON('/api/v1/config', { method: 'POST', headers, body }, session, false, metadata).then((result) => {
      if (!isCurrentAuth(session)) throw new Error('登录会话已变更，旧配置响应已忽略');
      if (sourceEpoch !== configSourceEpoch && metadata.etag !== configETag) {
        throw new Error('配置来源已变更，旧保存响应已忽略；草稿已保留，请核对当前配置');
      }
      ++reloadStatusRequest; ++configLoadRequest;
      if (metadata.etag) {
        for (const [base, accepted] of ownConfigVersions) if (accepted === etag) ownConfigVersions.set(base, metadata.etag);
        ownConfigVersions.set(etag, metadata.etag);
        ownConfigVersions.set(originalETag, metadata.etag);
        configETag = metadata.etag;
      }
      acknowledgeConfigSave(patch, sent);
      if (result.navigation) applyAdminNavigation(result.navigation);
      return result;
    }).catch(async (error) => {
      if (error.status === 409 && isCurrentAuth(session)) {
        configConflictMessage = '保存发生冲突：配置已在其他位置更新。草稿已保留，正在刷新配置基线，请核对后手动再次保存。';
        renderConfigDraftStatus();
        await loadConfig();
      }
      throw error;
    });
  });
  configSaveQueue = save.catch(() => {});
  return save;
}

setInterval(() => {
  const page = $('#page-config');
  if (!adminNavigation && page && page.classList.contains('active') && !document.hidden) refreshReloadStatus();
}, 5000);

function showConfigSaved(result, label = '配置已保存') {
  // Also invalidate queries that started while the save was in progress.
  ++reloadStatusRequest;
  ++configLoadRequest;
  const status = result && result.reload;
  renderReloadStatus(status);
  if (result?.navigation) { toast(label + '，请按页面提示继续访问管理服务'); return; }
  if (!status) { toast(label + '，请刷新查看生效状态'); return; }
  const pending = status.restartRequired || [];
  toast(pending.length ? label + '；需重启：' + pending.slice(0, 6).join('、') + (pending.length > 6 ? '等' : '') : label + '并已应用（轮询间隔在下一轮调度生效）');
}

async function loadConfig() {
  if (adminNavigation) return;
  const request = ++configLoadRequest;
  const session = authSession();
  try {
    const metadata = {};
    const cfg = await apiJSON('/api/v1/config', {}, session, false, metadata);
    if (adminNavigation || request !== configLoadRequest || !isCurrentAuth(session)) return;
    captureConfigDrafts();
    configCache = cfg;
    if (metadata.etag && metadata.etag !== configETag) {
      ++configSourceEpoch;
      ownConfigVersions.clear();
      configETag = metadata.etag;
    }
    loadConfig._retried = false;
    renderConfigForm();
    const modal = $('#platform-modal');
    if (!modal.classList.contains('hidden') && $('#platform-modal-body').dataset.site) {
      renderPlatformModal($('#platform-modal-body').dataset.site);
    }
    refreshReloadStatus();
    loadPermOverview(); // 权限面板数据（面板由 renderConfigForm 生成）
  } catch (e) {
    if (adminNavigation || request !== configLoadRequest || !isCurrentAuth(session)) return;
    if (e.message.includes('401')) { showLogin(); return; } // 登录失效直接回登录层
    // 网络抖动自动重试一次，重试再失败才提示。
    // _retried 只能在「成功」或「最终失败」时复位：原来放在定时器回调开头复位，
    // 递归调用永远看到 false，于是变成无限重试，用户永远等不到失败提示。
    if (!loadConfig._retried) {
      loadConfig._retried = true;
      setTimeout(() => { if (isCurrentAuth(session) && request === configLoadRequest) loadConfig(); }, 1500);
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

// 接入方式切到 QQ 开放平台时，AppID / AppSecret 必须齐全。
// 后端也会拒绝，但前端先提示能少一次「保存成功、重启却起不来」。
function validateQQOfficialPatch(patch) {
  const current = configCache || {};
  const qqPatch = patch.qqofficial || {};
  const adapterPatch = patch.adapter || {};
  const mode = String(adapterPatch.mode !== undefined ? adapterPatch.mode : ((current.adapter || {}).mode || 'onebot-v11'));
  if (mode !== 'qqofficial') return;
  const fallback = current.qqofficial || {};
  const appId = String(qqPatch['app-id'] !== undefined ? qqPatch['app-id'] : (fallback['app-id'] || '')).trim();
  const secret = String(qqPatch['app-secret'] !== undefined ? qqPatch['app-secret'] : (fallback['app-secret'] || '')).trim();
  if (!appId) throw new Error('接入方式为 QQ 开放平台时必须填写 AppID，或改回 OneBot 11');
  if (!secret) throw new Error('接入方式为 QQ 开放平台时必须填写 AppSecret（扫码绑定会自动写入），或改回 OneBot 11');
}

document.addEventListener('DOMContentLoaded', () => {
  const trackDraft = (event) => {
    if (event.target.matches('[data-path], #cfg-json')) { captureConfigDrafts(); renderConfigDraftStatus(); }
  };
  document.addEventListener('input', trackDraft);
  document.addEventListener('change', trackDraft);
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
    captureConfigDrafts();
    renderConfigDraftStatus();
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
  const syncSaveBtn = () => { saveBtn.disabled = !!adminNavigation || activeConfigTab === 'perm' || activeConfigTab === 'templates'; };
  syncSaveBtn();

  $('#config-save').addEventListener('click', async () => {
    const session = authSession();
    let patch;
    try {
      patch = collectConfigPatch(activeConfigTab);
      validateQQOfficialPatch(patch);
    } catch (e) {
      toast('配置校验未通过：' + e.message, false);
      return;
    }
    if (!Object.keys(patch).length) {
      toast('没有需要保存的修改');
      return;
    }
    $('#config-save').disabled = true;
    try {
      const result = await saveConfigPatch(patch, { advanced: activeConfigTab === 'advanced' });
      if (!isCurrentAuth(session)) return;
      showConfigSaved(result);
      loadConfig();
    } catch (e) {
      if (isCurrentAuth(session)) toast('保存失败：' + e.message, false);
    } finally {
      if (isCurrentAuth(session)) $('#config-save').disabled = !!adminNavigation;
    }
  });

  $('#config-reload')?.addEventListener('click', async () => {
    if (adminNavigation) return;
    const session = authSession();
    const btn = $('#config-reload');
    btn.disabled = true;
    btn.textContent = '重载中…';
    try {
      const result = await apiJSON('/api/v1/config/reload', { method: 'POST' }, session);
      if (!isCurrentAuth(session)) return;
      if (result.navigation) applyAdminNavigation(result.navigation, false);
      showConfigSaved(result, '配置已重载');
      await loadConfig();
    } catch (e) {
      if (isCurrentAuth(session)) toast('热重载失败：' + e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = '热重载';
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
  captureConfigDrafts();
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
  restoreConfigDrafts(body);
}

function closePlatformModal() {
  captureConfigDrafts();
  $('#platform-modal').classList.add('hidden');
  $('#platform-modal-body').innerHTML = '';
  delete $('#platform-modal-body').dataset.site;
}

// collectPatchFromContainer 从任意容器（配置面板/平台弹层）收集 diff 补丁
function collectPatchFromContainer(container) {
  const patch = {};
  container.querySelectorAll('[data-path]').forEach((el) => {
    const path = el.dataset.path;
    const f = configFieldSchema(path);
    if (!f) return;
    const original = configFieldValue(f);
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
      if (f.min !== undefined && (!Number.isInteger(current) || current < f.min || current > f.max)) {
        throw new Error('「' + f.label + '」必须是' + f.min + '到' + f.max + '之间的整数');
      }
    } else {
      current = el.value;
      baseline = original !== undefined ? String(original) : String(f.def ?? '');
    }
    if (current !== baseline) setPath(patch, path, current);
  });
  return patch;
}

// setPlatformEnabled 保存并同步应用 platform.<site>.enable，
// lsp/notify.go 消费出口即时跳过停用平台的推送（订阅数据保留，恢复后继续）
async function setPlatformEnabled(site, enabled) {
  const session = authSession();
  const patch = {};
  setPath(patch, 'platform.' + site + '.enable', enabled);
  try {
    const result = await saveConfigPatch(patch);
    if (!isCurrentAuth(session)) return;
    showConfigSaved(result, `${enabled ? '已启用' : '已停用推送'}：${(PLATFORM_META[site] || {}).name || site}`);
    if (configCache) setPath(configCache, 'platform.' + site + '.enable', enabled);
  } catch (e) {
    if (!isCurrentAuth(session)) return;
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
const LOGIN_PLATFORM_NAME = { bilibili: 'B站', weibo: '微博', qqofficial: 'QQ 开放平台' };

let loginSession = null; // 服务端会话 id
let loginSessionAuth = null;
let loginTimer = null;   // 轮询定时器
let loginReqSeq = 0;     // 建会话请求的序号，用来丢掉过期请求的迟到响应
let loginSite = null;    // 当前平台，供「重新获取二维码」用
let loginSetupMode = false; // true = 初始化向导里的匿名 QQ 绑定（还没有 admin.token）

// 初始化向导出现在 admin.token 设置之前，这时走不了需要鉴权的 /api/v1/login/qrcode，
// 因此服务端为 QQ 官方预留了一套同源、匿名、初始化完成即 404 的 setup 入口。
const LOGIN_ENDPOINTS = {
  normal: {
    start: '/api/v1/login/qrcode',
    status: '/api/v1/login/qrcode/status',
    cancel: '/api/v1/login/qrcode/cancel',
  },
  setup: {
    start: '/api/v1/setup/qqofficial/qrcode',
    status: '/api/v1/setup/qqofficial/qrcode/status',
    cancel: '/api/v1/setup/qqofficial/qrcode/cancel',
  },
};

// 统一扫码接口调用：普通模式走带 token 的 apiJSON；setup 模式没有 token，直接 fetch。
async function loginRequest(path, opts, session, acceptStale, setup) {
  if (!setup) return apiJSON(path, opts, session, acceptStale);
  const resp = await fetch(path, {
    ...opts,
    headers: {
      ...(opts && opts.headers ? opts.headers : {}),
      ...(opts && opts.body ? { 'Content-Type': 'application/json' } : {}),
    },
  });
  if (!resp.ok) {
    let msg = 'HTTP ' + resp.status;
    try { const e = await resp.json(); if (e.error) msg = e.error; } catch { /* 无 JSON 错误体 */ }
    throw new Error(msg);
  }
  return resp.json();
}

// done=true 表示这次登录已经有结果了：按钮换成「重新扫码」+「关闭」。
function loginActionsHTML(done, site) {
  return '<div class="login-actions">' +
    (done
      ? '<button class="btn" data-login-platform="' + esc(site || '') + '"' +
        (loginSetupMode ? ' data-login-setup="1"' : '') + '>重新扫码</button>'
      : '<button class="btn" id="login-refresh">重新获取二维码</button>') +
    '<button class="btn' + (done ? ' primary' : '') + '" id="login-cancel">' + (done ? '关闭' : '取消') + '</button>' +
    '</div>' +
    (done ? '' : '<p class="config-note">' + (loginSite === 'qqofficial'
      ? '扫码成功后会保存凭据。当前使用 QQ 开放平台时可在线应用，其他协议会继续运行。'
      : '扫码成功后会保存并在线应用凭据，不需要再点保存。') + '</p>');
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

function cancelLoginSession(sid = loginSession, session = loginSessionAuth, setup = loginSetupMode) {
  if (!sid) return;
  if (sid === loginSession) { loginSession = null; loginSessionAuth = null; }
  const path = (setup ? LOGIN_ENDPOINTS.setup : LOGIN_ENDPOINTS.normal).cancel;
  const request = { method: 'POST', body: JSON.stringify({ session: sid }) };
  if (setup) loginRequest(path, request, session, false, true).catch(() => {});
  else api(path, request, session || authSession()).catch(() => {});
}

function stopLoginPolling() {
  if (loginTimer !== null) { clearInterval(loginTimer); loginTimer = null; }
}

function closeLoginModal() {
  ++loginReqSeq;
  loginSite = null;
  $('#login-modal').classList.add('hidden');
  stopLoginPolling();
  cancelLoginSession();
}

async function startPlatformLogin(site, opts = {}) {
  const setup = !!opts.setup;
  if (!setup && !token) { showLogin(); return; }
  if (setup && site !== 'qqofficial') return;
  const ep = setup ? LOGIN_ENDPOINTS.setup : LOGIN_ENDPOINTS.normal;
  const name = LOGIN_PLATFORM_NAME[site] || site;
  stopLoginPolling();
  cancelLoginSession();
  loginSite = site;
  loginSetupMode = setup;

  $('#login-modal-title').textContent = name + (loginSetupMode ? ' · 扫码绑定' : ' 扫码登录');
  renderLoginBody('<div class="login-qr-box"><div class="login-qr-loading">正在获取二维码…</div></div>');
  $('#login-modal').classList.remove('hidden');

  const seq = ++loginReqSeq;
  const session = authSession();
  const current = () => seq === loginReqSeq && loginSite === site && isCurrentAuth(session);
  let resp;
  try {
    resp = await loginRequest(ep.start, {
      method: 'POST',
      body: JSON.stringify({ platform: site }),
    }, session, true, setup); // 迟到响应仍要读取 session id，随后显式取消服务端任务。
  } catch (e) {
    // 已经有更新的一次点击/切平台了，这次的结果作废
    if (!current()) return;
    renderLoginBody('<div class="login-qr-box"><div class="login-qr-err">' + esc(e.message) + '</div></div>' + loginActionsHTML());
    return;
  }

  if (!current()) { cancelLoginSession(resp.session, session, setup); return; }
  // 连点两次扫码时两个请求会先后回来：后一个把 loginSession 覆盖掉，
  // 前一个挂的定时器就再也没人清了。这里先停一次再挂新的。
  stopLoginPolling();
  loginSession = resp.session;
  loginSessionAuth = session;
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
  const session = loginSessionAuth;
  if (!isCurrentAuth(session)) return;
  let r;
  try {
    const setup = loginSetupMode;
    const ep = setup ? LOGIN_ENDPOINTS.setup : LOGIN_ENDPOINTS.normal;
    r = await loginRequest(ep.status + '?session=' + encodeURIComponent(sid), {}, session, false, setup);
  } catch (e) {
    return; // 网络抖动，等下一次
  }
  if (loginSession !== sid || !isCurrentAuth(session)) return; // 等响应期间被切走/关掉了
  const el = $('#login-status');
  if (el && r.message) el.textContent = r.message;

  if (r.state === 'confirmed') {
    stopLoginPolling();
    loginSession = null; // 服务端已经收尾，不用再发 cancel
    if (loginSetupMode) {
      // 向导里扫码只负责「拿到 AppID + 把 AppSecret 写进配置」，
      // 真正的保存动作留给「完成初始化」那一下（adapter.mode 也会在那时落盘）。
      setupQQ.bound = true;
      setupQQ.appId = r.name || setupQQ.appId;
      setupQQ.appSecret = '';
      loginSetupMode = false;
      $('#login-modal').classList.add('hidden');
      syncQQSetupUI();
      toast('QQ 开放平台绑定成功' + (r.name ? '：' + r.name : ''));
      return;
    }
    renderLoginBody(loginResultHTML('ok', r.message || '登录成功，凭据已写入配置', r.platform));
    // 服务端在登录成功时已经把账号缓存失效了，这里拿到的是新账号
    loadPlatformAccount(r.platform, 'login-result-account');
    toast((LOGIN_PLATFORM_NAME[r.platform] || r.platform) + ' 登录成功，配置已更新');
    loadConfig();
  } else if (r.state === 'failed' || r.state === 'expired') {
    stopLoginPolling();
    loginSession = null;
    loginSessionAuth = null;
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
  const isQQ = site === 'qqofficial';
  const initial = (acc.name || label).slice(0, 1);
  const avatar = acc.avatar
    ? '<img class="acct-avatar" alt="" src="' + esc(acc.avatar) + '" referrerpolicy="no-referrer">'
    : '<div class="acct-avatar acct-avatar-empty">' + esc(initial) + '</div>';

  if (!acc.loggedIn) {
    return '<div class="acct-card">' + avatar +
      '<div class="acct-main"><div class="acct-name">' + (isQQ ? '未绑定' : '未登录') + '</div>' +
      '<div class="acct-sub">' + esc(acc.message || (isQQ ? '尚未绑定 QQ 官方机器人' : '尚未登录')) + '</div></div>' +
      '<button class="btn ghost sm" data-login-platform="' + esc(site) + '">' + (isQQ ? '扫码绑定' : '扫码登录') + '</button>' +
      '</div>';
  }

  const meta = [];
  if (acc.uid) meta.push('UID ' + acc.uid);
  if (acc.mode) meta.push({ login: '登录模式', guest: '访客模式', api: 'API 模式' }[acc.mode] || acc.mode);
  if (acc.level) meta.push('LV' + acc.level);
  if (acc.vip) meta.push(acc.vip);
  // QQ 官方：AppID 就是账号标识，不显示 UID / 等级一类的空字段
  return '<div class="acct-card ok">' + avatar +
    '<div class="acct-main"><div class="acct-name">' + esc(acc.name || (isQQ ? '已绑定' : '已登录')) + '</div>' +
    '<div class="acct-sub">' + esc(meta.join(' · ') || acc.message || (isQQ ? 'AppSecret 已写入配置' : '已登录')) + '</div></div>' +
    '<button class="btn ghost sm" data-login-platform="' + esc(site) + '">' + (isQQ ? '重新绑定' : '重新登录') + '</button>' +
    '</div>';
}

/* ===== 启动 ===== */
function boot() {
  if (adminNavigation) return;
  refreshDashboard();
  if (dashboardTimer !== null) clearInterval(dashboardTimer);
  dashboardTimer = setInterval(refreshDashboard, 5000);
  if (!sseConnected) connectLogs();
  route();
}

document.addEventListener('DOMContentLoaded', () => {
  applyTheme(localStorage.getItem(THEME_KEY) || 'auto');
  cleanupLegacyUI();
  // 外观配置在服务器 ui.json；登录页也要显示背景，GET 允许匿名
  loadUISettings();

  // 自定义背景（图床直链 / 本地图片）
  document.querySelectorAll('[data-bg-toggle]').forEach((b) => b.addEventListener('click', openBgModal));
  $('#bg-modal-close')?.addEventListener('click', closeBgModal);
  $('#bg-cancel')?.addEventListener('click', closeBgModal);
  $('#bg-reset')?.addEventListener('click', resetBackground);
  $('#bg-save')?.addEventListener('click', saveBackground);
  $('#bg-url')?.addEventListener('input', () => {
    pendingBgData = '';
    if ($('#bg-file-name')) $('#bg-file-name').textContent = '';
    syncBgPreview();
  });
  $('#bg-position')?.addEventListener('change', () => {
    syncBgPreview();
    const img = $('#bg-image');
    if (img) img.style.objectPosition = $('#bg-position').value;
  });
  $('#fg-reset')?.addEventListener('click', () => {
    if ($('#fg-custom')) $('#fg-custom').checked = false;
    if ($('#fg-color')) {
      $('#fg-color').value = document.documentElement.getAttribute('data-theme') === 'light' ? '#3f2b4a' : '#f7f2ff';
    }
  });
  $('#bg-file')?.addEventListener('change', async (e) => {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    bgShowError('');
    try {
      pendingBgData = await compressImageFile(file);
      $('#bg-url').value = '';
      $('#bg-file-name').textContent = file.name + '（已压缩）';
      syncBgPreview();
    } catch (err) {
      pendingBgData = '';
      $('#bg-file-name').textContent = '';
      bgShowError(err.message);
      e.target.value = '';
    }
  });
  ['bg-blur', 'bg-opacity', 'bg-mask'].forEach((id) => {
    document.getElementById(id)?.addEventListener('input', () => {
      const layer = $('#bg-layer');
      if (!layer) return;
      if (id === 'bg-blur') layer.style.setProperty('--bg-blur', $('#bg-blur').value + 'px');
      if (id === 'bg-opacity') layer.style.setProperty('--bg-opacity', String(Number($('#bg-opacity').value) / 100));
      if (id === 'bg-mask') layer.style.setProperty('--bg-mask', String(Number($('#bg-mask').value) / 100));
    });
  });
  $('#bg-modal')?.addEventListener('click', (e) => {
    if (e.target === $('#bg-modal')) closeBgModal();
  });

  $('#login-form').addEventListener('submit', doLogin);
  $('#logout-btn').addEventListener('click', showLogin);
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
    // 顶部提示条跟随未保存的接入方式选项。
    const adapterSel = e.target.closest('select[data-path="adapter.mode"]');
    if (adapterSel) {
      const banner = $('#adapter-banner');
      if (banner) banner.innerHTML = adapterBannerHTML(adapterSel.value);
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
  const savePlatformModal = async () => {
    const session = authSession();
    const saveBtn = $('#platform-modal-save');
    if (!saveBtn) return;
    saveBtn.disabled = true;
    saveBtn.textContent = '保存中…';
    try {
      const patch = collectPatchFromContainer($('#platform-modal-body'));
      validateQQOfficialPatch(patch);
      if (Object.keys(patch).length) {
        const result = await saveConfigPatch(patch);
        if (!isCurrentAuth(session)) return;
        showConfigSaved(result, '平台配置已保存');
      } else {
        toast('没有修改需要保存');
      }
      closePlatformModal();
      await loadConfig();
    } catch (e) {
      if (isCurrentAuth(session)) toast('保存失败：' + e.message, false);
    } finally {
      if (isCurrentAuth(session)) {
        saveBtn.disabled = false;
        saveBtn.textContent = '保存';
      }
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
    if (startBtn) { startPlatformLogin(startBtn.dataset.loginPlatform, { setup: startBtn.dataset.loginSetup === '1' }); return; }
    if (e.target.closest('#login-refresh')) { startPlatformLogin(loginSite, { setup: loginSetupMode }); return; }
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
    else if ($('#bg-modal') && !$('#bg-modal').classList.contains('hidden')) closeBgModal();
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

  bindSetupWizard();
  // 启动入口：先探测是否需要初始化（admin.token 未设置），需要则向导优先于登录层
  checkSetupThenStart();
});

// 登录页提示随初始化状态切换：已初始化看默认文案，未初始化且当前来源不能初始化时
// 明确告诉用户去哪里完成，别让它对着登录框猜一个不存在的密码。
function setLoginHint(text) {
  const el = document.getElementById('login-hint');
  if (el) el.textContent = text;
}

async function checkSetupThenStart() {
  if (adminNavigation) return;
  const session = authSession();
  try {
    const r = await fetch('/api/v1/setup/status');
    const st = await r.json();
    if (adminNavigation || !isCurrentAuth(session)) return;
    if (st && st.needed) {
      setupCurrentAdapter = st.adapterMode || 'onebot-v11';
      setupAdapter = st.adapterMode ? '' : 'onebot-v11';
      setupQQ.bound = !!st.qqSecretSet;
      setupQQ.appId = st.qqAppId || '';
      showSetup();
      return;
    }
    if (st && st.initialized === false) {
      setLoginHint('还没有设置 admin.token：请在运行 bot 的机器上打开 http://127.0.0.1:15631 完成初始化');
    }
  } catch { /* 探测失败按常规流程走 */ }
  if (adminNavigation) return;
  if (!isCurrentAuth(session)) return;
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
let setupAdapter = 'onebot-v11'; // 空值表示保留当前协议
let setupCurrentAdapter = 'onebot-v11';
const setupQQ = { bound: false, appId: '', appSecret: '' };

function showSetup() {
  const ov = $('#setup-overlay');
  ov.classList.remove('hidden');
  document.querySelector('#app').style.visibility = 'hidden';
  syncSetupAdapterUI();
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

// 接入方式卡片与下方表单联动：始终只展示当前方式需要的字段，
// 避免「选了 QQ 开放平台，下面却还摆着 OneBot 地址」的错位感。
function syncSetupAdapterUI() {
  document.querySelectorAll('#setup-adapter .adapter-card').forEach((b) => {
    b.classList.toggle('active', b.dataset.adapter === setupAdapter);
  });
  const current = $('#setup-current-adapter');
  if (current) current.textContent = ADAPTER_NAMES[setupCurrentAdapter] || setupCurrentAdapter;
  const onebot = $('#setup-onebot');
  const qq = $('#setup-qq');
  if (onebot) onebot.classList.toggle('hidden', setupAdapter !== 'onebot-v11');
  if (qq) qq.classList.toggle('hidden', setupAdapter !== 'qqofficial');
  syncQQSetupUI();
}

function syncQQSetupUI() {
  const status = $('#setup-qq-status');
  if (!status) return;
  if (setupQQ.bound) {
    status.className = 'setup-qq-status ok';
    status.innerHTML = '已绑定 AppID <b>' + esc(setupQQ.appId || '（扫码完成）') + '</b>';
  } else if (setupQQ.appId) {
    status.className = 'setup-qq-status';
    status.textContent = 'AppID：' + setupQQ.appId;
  } else {
    status.className = 'setup-qq-status';
    status.textContent = '还没有绑定机器人';
  }
  const appIdInput = $('#setup-qq-appid');
  if (appIdInput && !appIdInput.value && setupQQ.appId) appIdInput.value = setupQQ.appId;
}

function renderSetupSummary() {
  const pass = $('#setup-pass').value;
  let html = '<div class="ss-row"><span>管理密码</span><b>' + (pass ? '✓ 已设置' : '未设置') + '</b></div>';
  if (setupAdapter === 'qqofficial') {
    const appId = ($('#setup-qq-appid').value.trim() || setupQQ.appId);
    html += '<div class="ss-row"><span>接入方式</span><b>QQ 开放平台</b></div>';
    html += appId
      ? '<div class="ss-row"><span>AppID</span><b class="mono">' + esc(appId) + '</b></div>'
      : '<div class="ss-row"><span>AppID / AppSecret</span><b>尚未填写</b></div>';
  } else if (setupAdapter === 'onebot-v11') {
    const addr = $('#setup-ob-addr').value.trim();
    const modeName = setupMode === 'ws-server' ? '正向（Bot 监听）' : '反向（Bot 主动连）';
    html += '<div class="ss-row"><span>接入方式</span><b>OneBot 11</b></div>';
    html += addr
      ? '<div class="ss-row"><span>' + modeName + '</span><b class="mono">' + esc(addr) + '</b></div>'
      : '<div class="ss-row"><span>OneBot 连接</span><b>使用默认配置</b></div>';
  } else {
    html += '<div class="ss-row"><span>接入方式</span><b>保留 ' + esc(ADAPTER_NAMES[setupCurrentAdapter] || setupCurrentAdapter) + '</b></div>';
  }
  $('#setup-summary').innerHTML = html + '<p class="ss-note">当前协议的连接配置可在线应用；切换协议需要另行迁移运行数据。</p>';
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

// validateSetupProtocol 在向导第 1 步做客户端预检：
// 服务端仍会完整校验（最终以它为准），这里只是把明显填错的地方当场指出来。
function validateSetupProtocol() {
  if (setupAdapter === 'qqofficial') {
    const appId = $('#setup-qq-appid').value.trim();
    const secret = $('#setup-qq-secret').value.trim();
    if (!appId && !setupQQ.bound) { setupError(1, '请填写 AppID，或点「扫码绑定」自动获取'); return false; }
    if (appId && !/^\d+$/.test(appId)) { setupError(1, 'AppID 必须是纯数字，请到 QQ 开放平台核对'); return false; }
    if (!secret && !setupQQ.bound) { setupError(1, '请填写 AppSecret，或点「扫码绑定」自动写入'); return false; }
    if (setupQQ.bound && appId && appId !== setupQQ.appId && !secret) {
      setupError(1, '更换 AppID 时请同时填写对应的 AppSecret，或重新扫码绑定');
      return false;
    }
    return true;
  }
  return true;
}

function bindSetupWizard() {
  // 步骤导航：第 1 步协议端、第 2 步密码，其余步骤直接放行。
  document.querySelectorAll('.setup-next').forEach((b) => b.addEventListener('click', () => {
    if (setupStep === 1) {
      if (!validateSetupProtocol()) return;
      setupError(1, '');
      gotoSetupStep(2);
      return;
    }
    if (setupStep === 2) {
      const p = $('#setup-pass').value, p2 = $('#setup-pass2').value;
      if (p.length < 6) { setupError(2, '管理密码至少 6 位'); return; }
      if (p !== p2) { setupError(2, '两次输入的密码不一致'); return; }
      setupError(2, '');
      gotoSetupStep(3);
      return;
    }
    setupError(setupStep, '');
    gotoSetupStep(+b.dataset.goto);
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

  // 接入方式切换（OneBot 11 / QQ 开放平台）
  document.querySelectorAll('#setup-adapter .adapter-card').forEach((b) => b.addEventListener('click', () => {
    setupAdapter = b.dataset.adapter;
    syncSetupAdapterUI();
    setupError(1, '');
  }));

  // OneBot 正向 / 反向
  const syncObAddrPlaceholder = () => {
    $('#setup-ob-addr').placeholder = setupMode === 'ws-server' ? '127.0.0.1:15630' : 'ws://127.0.0.1:3001';
  };
  document.querySelectorAll('#setup-mode .setup-mode-btn').forEach((b) => b.addEventListener('click', () => {
    setupMode = b.dataset.mode;
    document.querySelectorAll('#setup-mode .setup-mode-btn').forEach((x) => x.classList.toggle('active', x === b));
    syncObAddrPlaceholder();
    setupError(1, '');
  }));
  syncObAddrPlaceholder(); // 初始同步：index.html 的静态 placeholder 是旧值

  // QQ 开放平台：在向导里直接扫码绑定（走匿名 setup 接口，完成后自动填 AppID）
  const scanBtn = $('#setup-qq-scan');
  if (scanBtn) scanBtn.addEventListener('click', () => startPlatformLogin('qqofficial', { setup: true }));

  syncSetupAdapterUI();

  // 提交
  $('#setup-submit').addEventListener('click', async () => {
    if (adminNavigation) return;
    const session = authSession();
    const setupToken = $('#setup-pass').value;
    const btn = $('#setup-submit');
    btn.disabled = true;
    btn.textContent = '初始化中…';
    const isQQ = setupAdapter === 'qqofficial';
    const isOneBot = setupAdapter === 'onebot-v11';
    const obAddr = isOneBot ? $('#setup-ob-addr').value.trim() : '';
    try {
      const resp = await fetch('/api/v1/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token: setupToken,
          adapterMode: setupAdapter,
          obMode: obAddr ? setupMode : '',
          obAddr,
          obToken: isOneBot ? $('#setup-ob-token').value : '',
          qqAppId: isQQ ? ($('#setup-qq-appid').value.trim() || setupQQ.appId) : '',
          qqAppSecret: isQQ ? $('#setup-qq-secret').value : '',
        }),
      });
      if (adminNavigation || !isCurrentAuth(session)) return;
      if (!resp.ok) {
        let msg = '初始化失败（' + resp.status + '）';
        try { const e = await resp.json(); if (e.error) msg = e.error; } catch {}
        throw new Error(msg);
      }
      const result = await resp.json();
      if (adminNavigation || !isCurrentAuth(session)) return;
      // 成功：展示带「接下来去哪」指引的完成页，用户读完手动进入
      setAuthToken(setupToken);
      for (let i = 1; i <= 4; i++) $('#setup-pane-' + i).classList.add('hidden');
      const done = $('#setup-pane-done');
      done.classList.remove('hidden');
      done.classList.remove('pane-in'); void done.offsetHeight; done.classList.add('pane-in');
      const enter = () => {
        $('#setup-enter').removeEventListener('click', enter);
        hideSetup();
        hideLogin();
        showConfigSaved(result, '初始化完成');
        boot();
      };
      $('#setup-enter').addEventListener('click', enter);
    } catch (e) {
      if (adminNavigation || !isCurrentAuth(session)) return;
      // 协议端/凭据错误回第 1 步，密码错误回第 2 步，别让用户在第 4 步发懵
      if (/AppID|AppSecret|接入方式|连接模式|Access Token/.test(e.message)) {
        gotoSetupStep(1);
        setupError(1, e.message);
      } else if (/管理密码/.test(e.message)) {
        gotoSetupStep(2);
        setupError(2, e.message);
      } else {
        setupError(4, e.message);
      }
    } finally {
      btn.disabled = false;
      btn.textContent = '完成初始化';
    }
  });
}
