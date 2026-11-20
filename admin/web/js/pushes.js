/* 推送记录页（依赖 app.js 的 $/esc/apiJSON/token/showLogin/PLATFORM_META） */
window.PushPage = (() => {
  let sseConnected = false;
  let inited = false;
  let buffer = []; // 前端缓冲（SSE 回放 + 实时），上限 500

  const STATUS_META = {
    sent: { text: '已发送', cls: 'ok' },
    failed: { text: '发送失败', cls: 'bad' },
    skipped: { text: '已跳过', cls: 'warn' },
  };

  // 时间格式：跨天记录带日期（30 天窗口内区分）
  function fmtRowTime(iso) {
    const t = new Date(iso);
    const hh = [t.getHours(), t.getMinutes(), t.getSeconds()]
      .map((n) => String(n).padStart(2, '0')).join(':');
    const now = new Date();
    if (t.toDateString() === now.toDateString()) return hh;
    return (t.getMonth() + 1) + '-' + String(t.getDate()).padStart(2, '0') + ' ' + hh;
  }

  // 平台筛选静态预置全部支持的平台（无数据时也可按平台筛）
  function presetSiteOptions() {
    const sel = $('#push-site-filter');
    const seen = new Set(Array.from(sel.options).map((o) => o.value));
    for (const [site, meta] of Object.entries(PLATFORM_META)) {
      if (!seen.has(site)) {
        seen.add(site);
        const o = document.createElement('option');
        o.value = site;
        o.textContent = meta.name || site;
        sel.appendChild(o);
      }
    }
  }

  function row(e) {
    const meta = STATUS_META[e.status] || { text: e.status, cls: 'warn' };
    return '<tr>'
      + '<td class="mono dim">' + fmtRowTime(e.time) + '</td>'
      + '<td><span class="site-badge">' + esc((PLATFORM_META[e.site] || {}).name || e.site) + '</span></td>'
      + '<td class="dim">' + esc(window.SUB_TYPE_NAMES?.[e.type] || e.type || '') + '</td>'
      + '<td class="mono">' + esc(e.groupCode) + '</td>'
      + '<td class="mono">' + esc(e.target || '') + '</td>'
      + '<td class="push-summary">' + esc(e.summary || '') + '</td>'
      + '<td><span class="badge ' + meta.cls + '">' + meta.text + '</span></td>'
      + '</tr>';
  }

  function fillSiteOptions() {
    presetSiteOptions(); // 静态预置；历史数据可能含额外平台，下面继续补录
    const sel = $('#push-site-filter');
    const seen = new Set(Array.from(sel.options).map((o) => o.value));
    for (const e of buffer) {
      if (e.site && !seen.has(e.site)) {
        seen.add(e.site);
        const o = document.createElement('option');
        o.value = e.site;
        o.textContent = (PLATFORM_META[e.site] || {}).name || e.site;
        sel.appendChild(o);
      }
    }
  }

  function render() {
    const tbody = $('#push-tbody');
    if (!tbody) return;
    fillSiteOptions();
    const siteF = $('#push-site-filter').value;
    const statusF = $('#push-status-filter').value;
    const rows = buffer.filter((e) => (!siteF || e.site === siteF) && (!statusF || e.status === statusF));
    tbody.innerHTML = rows.length
      ? rows.map(row).join('')
      : '<tr><td colspan="7" class="empty">暂无推送记录</td></tr>';
  }

  function append(e) {
    buffer.push(e);
    if (buffer.length > 500) buffer.shift();
    render();
  }

  function setSSE(on) {
    sseConnected = on;
    const b = $('#push-conn');
    if (b) { b.className = 'badge ' + (on ? 'on' : 'off'); b.textContent = on ? '实时' : '重连中'; }
  }

  function init() {
    if (inited) return;
    inited = true;
    $('#push-site-filter').addEventListener('change', render);
    $('#push-status-filter').addEventListener('change', render);
  }

  async function connectStream() {
    if (!token) return;
    try {
      const resp = await fetch('/api/v1/pushes/stream', {
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
          try { append(JSON.parse(data.slice(6))); } catch {}
        }
      }
    } catch (e) { /* fallthrough */ }
    setSSE(false);
    setTimeout(() => {
      if ($('#page-pushes') && $('#page-pushes').classList.contains('active')) connectStream();
    }, 3000);
  }

  // dedupeKey：SSE 回放可能与 REST 历史重叠，按内容特征去重
  function dedupeKey(e) {
    return [e.site, e.type, e.groupCode, e.target, e.summary].join('|');
  }

  async function loadHistory() {
    try {
      const data = await apiJSON('/api/v1/pushes?limit=500');
      const seen = new Set(buffer.map(dedupeKey));
      for (const e of data.pushes || []) {
        if (!seen.has(dedupeKey(e))) { buffer.push(e); seen.add(dedupeKey(e)); }
      }
      buffer.sort((a, b) => new Date(a.time) - new Date(b.time));
      if (buffer.length > 500) buffer = buffer.slice(-500);
      render();
    } catch (e) { /* 静默：SSE 仍工作 */ }
  }

  function enter() {
    init();
    loadHistory();
    if (!sseConnected) connectStream();
  }

  return { enter };
})();
