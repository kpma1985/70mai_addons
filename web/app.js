/* app.js — X800 Addon Installer application logic */
'use strict';

const $ = (id) => document.getElementById(id);
const state = { barTimer: null, gpsLiveTimer: null, gpsLiveBusy: false, lastOverview: null, fileSel: null, fileParent: '/', filesBootstrapped: false, activeTab: 'connection', tsFallbackHost: '', tsCamera: { installed: false, running: false }, wifiRequirementsOk: false };

/** i18n template: t('k', {a:'x'}) not supported — use tr('key', {a:'x'}) with {a} in string */
function tr(key, vars) {
  let s = typeof t === 'function' ? t(key) : key;
  if (vars && typeof vars === 'object') {
    Object.keys(vars).forEach((k) => {
      s = s.split('{' + k + '}').join(String(vars[k]));
    });
  }
  return s;
}

function toast(message, type = 'info') {
  const box = $('toasts');
  const item = document.createElement('div');
  item.className = `toast-item ${type}`;
  item.textContent = message;
  box.appendChild(item);
  setTimeout(() => item.remove(), 5200);
}

function connPayload() {
  return {
    host: $('host').value.trim(),
    transport: 'ssh',
    password: $('password').value,
    ssh_port: parseInt($('ssh_port').value, 10) || 22,
    telnet_port: parseInt($('telnet_port').value, 10) || 23,
    iptables_path: ($('iptables_path') && $('iptables_path').value.trim()) || ''
  };
}

function apiErrorMessage(data) {
  if (!data || typeof data !== 'object') return '';
  return [data.error, data.stderr, data.hint].filter(Boolean).join(' · ');
}

function apiHeaders() {
  const h = {};
  const t = sessionStorage.getItem('x800_addon_api_token');
  if (t) h['X-Addon-Token'] = t;
  return h;
}

async function request(path, options = {}) {
  const headers = { ...apiHeaders(), ...(options.headers || {}) };
  const response = await fetch(path, { ...options, headers });
  const text = await response.text();
  if (response.status === 401) {
    const err = new Error('API-Token erforderlich');
    err.status = 401;
    throw err;
  }
  let data;
  try {
    data = text ? JSON.parse(text) : {};
  } catch (err) {
    data = { raw: text };
  }
  if (!response.ok) {
    const msg = apiErrorMessage(data) || data.raw || response.statusText || `HTTP ${response.status}`;
    throw new Error(msg);
  }
  return data;
}

async function post(path, body) {
  return request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
}

function pretty(value) {
  return JSON.stringify(value, null, 2);
}

function setOutput(id, value) {
  $(id).textContent = typeof value === 'string' ? value : pretty(value);
}

function sysEl(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null && text !== '') e.textContent = text;
  return e;
}

function sysMetricsFromRows(rows) {
  const wrap = document.createElement('div');
  wrap.className = 'sys-metrics';
  rows.forEach(([k, v]) => {
    const cell = document.createElement('div');
    cell.className = 'sys-kv';
    cell.appendChild(sysEl('span', 'k', k));
    cell.appendChild(sysEl('span', 'v', v == null ? '—' : String(v)));
    wrap.appendChild(cell);
  });
  return wrap;
}

function sysRawBlock(full) {
  const wrap = document.createElement('div');
  wrap.className = 'sys-raw';
  const det = document.createElement('details');
  const sum = document.createElement('summary');
  sum.textContent = 'Rohdaten';
  const pre = document.createElement('pre');
  pre.textContent = full || '—';
  det.append(sum, pre);
  wrap.appendChild(det);
  return wrap;
}

function humanKiB(kb) {
  const v = Number(kb);
  if (!Number.isFinite(v)) return String(kb);
  const b = v * 1024;
  if (b >= 1073741824) return (b / 1073741824).toFixed(2) + ' GiB';
  if (b >= 1048576) return (b / 1048576).toFixed(2) + ' MiB';
  if (b >= 1024) return (b / 1024).toFixed(1) + ' KiB';
  return v + ' KiB';
}

function parseMeminfo(text) {
  const out = {};
  String(text).split('\n').forEach((line) => {
    const m = line.match(/^([^:]+):\s+(\d+)\s+kB\s*$/i);
    if (m) out[m[1].trim()] = Number(m[2]);
  });
  return out;
}

function parseIdentity(text) {
  const lines = String(text).split('\n').map((s) => s.trim()).filter(Boolean);
  const rows = [];
  if (lines[0]) rows.push(['Hostname', lines[0]]);
  const uname = lines.find((l) => /Linux version|Linux \d/.test(l) || l.includes('aarch64') || l.includes('arm'));
  if (uname) rows.push(['Kernel', uname]);
  const dt = lines.find((l) => /\d{4}/.test(l) && (/UTC/.test(l) || /:\d{2}:/.test(l)) && l.length < 80);
  if (dt) rows.push(['Zeit', dt]);
  const up = lines.find((l) => /load average/i.test(l) || /^up \d+/i.test(l) || /\d+:\d+:\d+ up /i.test(l));
  if (up) rows.push(['Uptime / Load', up]);
  return rows;
}

function parseCPU(text) {
  const t = String(text);
  const models = [...t.matchAll(/^model name\s*:\s*(.+)$/gim)].map((m) => m[1].trim());
  const procs = (t.match(/^processor\s*:/gim) || []).length;
  const rows = [];
  if (models[0]) rows.push(['CPU', models[0]]);
  if (procs) rows.push(['Kerne', String(procs)]);
  return rows;
}

function parseDF(text) {
  const lines = String(text).trim().split('\n').filter(Boolean);
  if (lines.length < 2) return null;
  const head = lines[0].trim().split(/\s+/);
  const body = lines.slice(1).map((ln) => ln.trim().split(/\s+/));
  return { head, body };
}

function sysTableFromDF(parsed) {
  const wrap = document.createElement('div');
  wrap.className = 'sys-table-wrap';
  const table = document.createElement('table');
  table.className = 'sys-table';
  const trh = document.createElement('tr');
  parsed.head.forEach((h) => trh.appendChild(sysEl('th', null, h)));
  table.appendChild(trh);
  parsed.body.forEach((cols) => {
    const tr = document.createElement('tr');
    cols.forEach((c) => tr.appendChild(sysEl('td', null, c)));
    table.appendChild(tr);
  });
  wrap.appendChild(table);
  return wrap;
}

function parseIfaces(text) {
  const blocks = String(text).split(/\n\n+/).map((b) => b.trim()).filter(Boolean);
  const out = [];
  blocks.forEach((b) => {
    const line0 = b.split('\n')[0] || '';
    const m = line0.match(/^([^\s:]+)/);
    const ifn = m ? m[1] : line0.split(/\s+/)[0] || 'iface';
    const inet = b.match(/inet addr:([0-9.]+)/) || b.match(/\binet ([0-9.]+)\b/);
    const ip = inet ? inet[1] : '—';
    const fl = line0.replace(ifn, '').trim().slice(0, 120);
    out.push({ ifn, ip, fl });
  });
  return out;
}

function sysIfaceGrid(list) {
  const wrap = document.createElement('div');
  wrap.className = 'sys-iface-grid';
  list.forEach((x) => {
    const card = document.createElement('div');
    card.className = 'sys-iface';
    card.appendChild(sysEl('div', 'if', x.ifn));
    card.appendChild(sysEl('div', 'ip', x.ip));
    if (x.fl) card.appendChild(sysEl('div', 'fl', x.fl));
    wrap.appendChild(card);
  });
  return wrap;
}

function buildSystemSection(name, raw) {
  const art = document.createElement('article');
  art.className = 'sys-section';
  const head = document.createElement('div');
  head.className = 'sys-section-head';
  head.appendChild(sysEl('h3', null, name));
  art.appendChild(head);
  const text = String(raw || '').trim();
  let body = null;

  if (name === 'identity') {
    const rows = parseIdentity(text);
    if (rows.length) body = sysMetricsFromRows(rows);
  } else if (name === 'cpu') {
    const rows = parseCPU(text);
    if (rows.length) body = sysMetricsFromRows(rows);
  } else if (name === 'memory') {
    const m = parseMeminfo(text);
    const rows = [];
    if (m.MemTotal != null) rows.push(['RAM gesamt', humanKiB(m.MemTotal)]);
    if (m.MemAvailable != null) rows.push(['Verfügbar', humanKiB(m.MemAvailable)]);
    else if (m.MemFree != null) rows.push(['Frei', humanKiB(m.MemFree)]);
    if (m.SwapTotal != null && m.SwapTotal > 0) rows.push(['Swap', humanKiB(m.SwapTotal)]);
    if (rows.length) body = sysMetricsFromRows(rows);
  } else if (name === 'storage df') {
    const p = parseDF(text);
    if (p) body = sysTableFromDF(p);
  } else if (name === 'network ifconfig') {
    const list = parseIfaces(text);
    if (list.length) body = sysIfaceGrid(list);
  } else if (name === 'routes' || name === 'dns') {
    const lines = text.split('\n').map((s) => s.trim()).filter(Boolean).slice(0, 24);
    if (lines.length) {
      const rows = lines.map((ln, i) => [`#${i + 1}`, ln]);
      body = sysMetricsFromRows(rows);
    }
  } else if (name === 'processes') {
    const n = text.split('\n').length;
    body = sysMetricsFromRows([['Zeilen', String(n)], ['Hinweis', 'Vollständige Liste in Rohdaten']]);
  }

  if (!body) {
    const prev = text.split('\n').filter(Boolean).slice(0, 6).join(' · ');
    body = sysMetricsFromRows([['Kurz', prev || '—']]);
  }
  art.appendChild(body);
  art.appendChild(sysRawBlock(text || '—'));
  return art;
}

function renderSystemStatus(data) {
  const box = $('systemSections');
  box.textContent = '';

  if (data.limited && data.status && typeof data.status === 'object') {
    const keys = Object.keys(data.status).sort();
    const rows = keys.map((k) => {
      const v = data.status[k];
      return [k, typeof v === 'object' ? pretty(v) : String(v)];
    });
    const art = document.createElement('article');
    art.className = 'sys-section';
    const head = document.createElement('div');
    head.className = 'sys-section-head';
    head.appendChild(sysEl('h3', null, 'Eingeschränkter Status'));
    art.appendChild(head);
    art.appendChild(sysMetricsFromRows(rows));
    art.appendChild(sysRawBlock(data.raw || pretty(data.status)));
    box.appendChild(art);
    appLog(pretty(data.status));
    return;
  }

  if (data.sections && Object.keys(data.sections).length) {
    const entries = Object.entries(data.sections).filter(([n, v]) => {
      if (n === 'raw' && Object.keys(data.sections).length > 1) return false;
      return String(v || '').trim() !== '';
    });
    entries.forEach(([name, value]) => {
      box.appendChild(buildSystemSection(name, value));
    });
  }
  appLog(data.raw ? data.raw.slice(0, 1200) : pretty(data));
}

function updateFileActionState() {
  const f = state.fileSel;
  const fileOk = !!(f && f.type === 'file');
  $('btnFilesView').disabled = !fileOk;
  $('btnFilesDownload').disabled = !fileOk;
  const c = connPayload();
  const sd = ($('filePath').value.trim() || '/').startsWith('/mnt/sd');
  $('btnFilesUpload').disabled = !sd;
}

function clearFileSelection(tbody) {
  state.fileSel = null;
  tbody.querySelectorAll('tr.file-sel').forEach((r) => r.classList.remove('file-sel'));
  updateFileActionState();
}

function selectFileRow(row, entry, tbody) {
  tbody.querySelectorAll('tr.file-sel').forEach((r) => r.classList.remove('file-sel'));
  row.classList.add('file-sel');
  state.fileSel = { path: entry.path, type: entry.type, name: entry.name };
  updateFileActionState();
}

async function openFileView(remotePath) {
  const c = connPayload();
  const pre = $('fileViewModal');
  pre.hidden = false;
  pre.textContent = 'Lade…';
  const data = await post('/api/files/read', { ...c, path: remotePath, max_bytes: 512000 });
  if (!data.ok) {
    pre.textContent = data.error || 'Lesen fehlgeschlagen';
    throw new Error(data.error || 'Lesen fehlgeschlagen');
  }
  const bin = atob(data.data_base64 || '');
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  const dec = new TextDecoder('utf-8', { fatal: false }).decode(bytes);
  const ctrl = /[\x00-\x08\x0B\x0C\x0E-\x1F]/.test(dec.slice(0, 4000));
  pre.textContent = ctrl ? `[Binär / nicht vollständig als Text] ${remotePath}\n` + dec.slice(0, 8000) : dec;
}

async function downloadSelectedFile() {
  const f = state.fileSel;
  if (!f || f.type !== 'file') return;
  const data = await post('/api/files/read', { ...connPayload(), path: f.path, max_bytes: 8 * 1024 * 1024 });
  if (!data.ok) throw new Error(data.error || 'Download fehlgeschlagen');
  const bin = atob(data.data_base64 || '');
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  const blob = new Blob([bytes]);
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = f.name || 'download.bin';
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 5000);
}

function renderFileTable(data) {
  const tbody = $('fileTable').querySelector('tbody');
  clearFileSelection(tbody);
  tbody.textContent = '';
  const entries = Array.isArray(data.entries) ? data.entries : [];
  if (!entries.length) {
    const row = document.createElement('tr');
    const cell = document.createElement('td');
    cell.colSpan = 5;
    cell.textContent = data.error || 'Keine Einträge.';
    row.appendChild(cell);
    tbody.appendChild(row);
  }
  entries.forEach((entry) => {
    const row = document.createElement('tr');
    const name = document.createElement('td');
    name.textContent = entry.name;
    name.style.cursor = 'pointer';
    name.title = entry.type === 'dir' ? 'Doppelklick: oeffnen' : 'Doppelklick: Ansicht';
    name.addEventListener('click', () => selectFileRow(row, entry, tbody));
    if (entry.type === 'dir') {
      name.addEventListener('dblclick', (ev) => {
        ev.preventDefault();
        loadFiles(entry.path).catch((err) => toast(err.message, 'error'));
      });
    } else {
      name.addEventListener('dblclick', (ev) => {
        ev.preventDefault();
        openFileView(entry.path).catch((err) => toast(err.message, 'error'));
      });
    }
    [name, entry.type, entry.size, entry.perm, entry.time].forEach((value) => {
      const cell = value instanceof HTMLElement ? value : document.createElement('td');
      if (!(value instanceof HTMLElement)) cell.textContent = value || '-';
      row.appendChild(cell);
    });
    tbody.appendChild(row);
  });
  const bc = $('fileBreadcrumb');
  bc.textContent = '';
  const p = data.path || $('filePath').value.trim() || '/';
  const par = data.parent || '/';
  state.fileParent = par;
  const span = document.createElement('span');
  span.textContent = p;
  bc.appendChild(span);
  $('btnFilesUp').disabled = p === '/' || par === p;
  appLog((data.raw ? data.raw : pretty(data)).slice(0, 400));
}

async function loadFiles(path) {
  if (path) $('filePath').value = path;
  const data = await post('/api/files/list', { ...connPayload(), path: $('filePath').value.trim() || '/' });
  $('filePath').value = data.path || $('filePath').value;
  $('fileViewModal').hidden = true;
  renderFileTable(data);
  if (!data.ok) throw new Error(data.error || 'Verzeichnis konnte nicht geladen werden');
  state.filesBootstrapped = true;
  return data;
}

function setBusy(button, label) {
  if (button.disabled) return () => {};
  const old = button.textContent;
  button.textContent = label;
  button.disabled = true;
  return () => {
    button.textContent = old;
    button.disabled = false;
  };
}

function chip(id, value, status) {
  const el = $(id);
  if (!el) return;
  el.classList.remove('ok', 'warn', 'bad');
  if (status) el.classList.add(status);
  const valEl = el.querySelector('.value') || el.querySelector('.chip-val');
  if (valEl) valEl.textContent = value || '-';
}

const TAB_TITLES = {
  connection: 'tab.connection', system: 'tab.system', files: 'tab.files', firmware: 'tab.firmware',
  wificlient: 'tab.wificlient', tailscale: 'tab.tailscale', streaming: 'tab.streaming',
  tracking: 'tab.tracking'
};

function showTab(name) {
  state.activeTab = name;
  document.querySelectorAll('.panel').forEach((panel) => {
    panel.classList.toggle('active', panel.id === `panel-${name}`);
  });
  document.querySelectorAll('[data-tab]').forEach((tab) => {
    const active = tab.dataset.tab === name;
    tab.classList.toggle('active', active);
    tab.setAttribute('aria-selected', active ? 'true' : 'false');
  });
  const titleEl = $('topNavTitle');
  if (titleEl) {
    const key = TAB_TITLES[name];
    titleEl.textContent = (key && typeof t === 'function') ? t(key) : (key || name);
  }
  // GPS Live stoppen wenn Tracking-Tab verlassen wird
  if (name !== 'tracking' && state.gpsLiveTimer) setGpsLive(false);
  if (name === 'files' && !state.filesBootstrapped) {
    const p = $('filePath').value.trim() || '/mnt/sd';
    loadFiles(p).catch((err) => toast(err.message || String(err), 'error'));
    return;
  }
  // Sofortiges Laden beim Tab-Wechsel
  const c = connPayload();
  if (!c.host) return;
  if (name === 'streaming') { renderStreamingUrls(); return; }
  if (name === 'wificlient') {
    post('/api/wifi/status', c).then(wifiStatusUpdate).catch(() => {});
  } else if (name === 'tracking') {
    refreshGps().catch(() => {});
    post('/api/flespi/daemon', { ...c, action: 'status' }).then(daemonStatusUpdate).catch(() => {});
  } else if (name === 'tailscale') {
    refreshTailscalePanel();
  } else if (name === 'system') {
    post('/api/system/status', c).then(data => {
      $('systemSections').textContent = '';
      renderSystemStatus(data);
    }).catch(() => {});
  }
}

async function refreshGps() {
  const data = await post('/api/gps', connPayload());
  $('gpsAvail').textContent = data.gps_available === true ? 'true' : data.gps_available === false ? 'false' : '-';
  $('gpsLat').textContent = data.gps_lat || '-';
  $('gpsLon').textContent = data.gps_lon || '-';
  $('gpsTime').textContent = data.gps_time || '-';
  if ($('gpsSource')) $('gpsSource').textContent = data.source || '-';
  if ($('gpsDevices')) $('gpsDevices').textContent = summarizeGPSDevices(data.devices || '');
  const mapsLink = $('gpsMapsLink');
  const map = $('gpsMap');
  const mapEmpty = $('gpsMapEmpty');
  if (mapsLink) {
    const lat = data.gps_lat, lon = data.gps_lon;
    const fix = lat && lon && data.gps_available;
    if (fix) {
      mapsLink.hidden = false;
      mapsLink.innerHTML = `<a href="https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}&zoom=15" target="_blank" rel="noopener">OpenStreetMap</a> · <a href="https://maps.google.com/?q=${lat},${lon}" target="_blank" rel="noopener">Google Maps</a>`;
    } else {
      mapsLink.hidden = true;
    }
    updateGpsMap(lat, lon, fix);
  }
  if (!data.ok) throw new Error(data.error || 'GPS-Abfrage fehlgeschlagen');
  return data;
}

function gpsLogLine(data) {
  const fix = data.gps_available === true && data.gps_lat && data.gps_lon;
  const src = data.source || '-';
  const devices = summarizeGPSDevices(data.devices || '');
  if (fix) return `FIX source=${src} lat=${data.gps_lat} lon=${data.gps_lon} time=${data.gps_time || '-'} devices=${devices}`;
  return `NO-FIX source=${src} raw=${data.raw_live || data.hint || '-'} devices=${devices}`;
}

async function gpsDebugToConsole() {
  appLogOpen();
  appLogSeparator('GPS Debug', 'gps');
  const data = await post('/api/gps/debug', connPayload());
  appLog(data.raw_debug || pretty(data), 'gps');
  if (data.stderr) appLog('[stderr] ' + data.stderr, 'ssh');
  if (!data.ok) throw new Error(data.error || 'GPS-Debug fehlgeschlagen');
}

function setGpsLive(active) {
  const btn = $('btnGpsLive');
  if (active) {
    if (state.gpsLiveTimer) return;
    appLogOpen();
    appLogSeparator('GPS Live gestartet', 'gps');
    if (btn) {
      btn.classList.add('active');
      btn.textContent = 'Live stoppen';
    }
    const tick = async () => {
      if (state.gpsLiveBusy) return;
      state.gpsLiveBusy = true;
      try {
        const data = await refreshGps();
        appLog(gpsLogLine(data), 'gps');
      } catch (err) {
        appLog('FEHLER GPS Live: ' + (err.message || String(err)), 'gps');
      } finally {
        state.gpsLiveBusy = false;
      }
    };
    tick();
    state.gpsLiveTimer = setInterval(tick, 5000);
    return;
  }
  if (state.gpsLiveTimer) {
    clearInterval(state.gpsLiveTimer);
    state.gpsLiveTimer = null;
  }
  state.gpsLiveBusy = false;
  if (btn) {
    btn.classList.remove('active');
    btn.textContent = 'Live-Konsole';
  }
  appLogSeparator('GPS Live gestoppt', 'gps');
}

function summarizeGPSDevices(devices) {
  const list = String(devices || '').trim().split(/\s+/).filter(Boolean);
  if (!list.length) return '-';
  if (list.length <= 3) return list.join(' ');
  return `${list.slice(0, 3).join(' ')} +${list.length - 3}`;
}

function updateGpsMap(lat, lon, fix) {
  const map = $('gpsMap');
  const empty = $('gpsMapEmpty');
  if (!map || !empty) return;
  if (!fix) {
    map.hidden = true;
    map.removeAttribute('src');
    empty.hidden = false;
    return;
  }
  const la = Number(lat);
  const lo = Number(lon);
  if (!Number.isFinite(la) || !Number.isFinite(lo)) {
    map.hidden = true;
    empty.hidden = false;
    return;
  }
  const d = 0.006;
  const bbox = [lo - d, la - d, lo + d, la + d].map((v) => v.toFixed(6)).join(',');
  map.src = `https://www.openstreetmap.org/export/embed.html?bbox=${encodeURIComponent(bbox)}&layer=mapnik&marker=${encodeURIComponent(`${la.toFixed(6)},${lo.toFixed(6)}`)}`;
  map.hidden = false;
  empty.hidden = true;
}

async function refreshCurrentTab() {
  await Promise.all([loadLocalTailscale(), refreshStatusBar()]);
  const tab = state.activeTab;
  const c = connPayload();
  try {
    if (tab === 'system') {
      $('systemSections').textContent = '';
      const data = await post('/api/system/status', c);
      renderSystemStatus(data);
    } else if (tab === 'wificlient') {
      const data = await post('/api/wifi/status', c);
      wifiStatusUpdate(data);
    } else if (tab === 'streaming') {
      renderStreamingUrls();
    } else if (tab === 'tracking') {
      await refreshGps();
      const dm = await post('/api/flespi/daemon', { ...c, action: 'status' });
      daemonStatusUpdate(dm);
    } else if (tab === 'tailscale') {
      await refreshTailscalePanel();
    } else if (tab === 'ethernet') {
      await loadEthStatus();
    }
  } catch (err) {
    appLog('FEHLER Refresh: ' + err.message);
  }
}

function tailscaleIPFromOverview() {
  const o = state.lastOverview;
  if (!o || !o.tailscale_cam) return '';
  const ip = o.tailscale_cam.tailscale_ip;
  return ip && String(ip).trim() ? String(ip).trim() : '';
}

/** True if URL is HTTP(S) and likely MJPEG / multipart still image API. */
function streamLooksLikeMjpegHttp(url) {
  if (!url || !/^https?:\/\//i.test(url)) return false;
  const u = url.toLowerCase();
  return u.includes('mjpg') || u.includes('mjpeg') || u.includes('axis-cgi') || u.includes('video.cgi');
}

function resetStreamPlayer() {
  const vid = $('rtspPreview');
  const img = $('mjpegStreamPreview');
  const ph = $('streamPlayerPlaceholder');
  const err = $('streamPlayerError');
  if (err) {
    err.hidden = true;
    err.textContent = '';
  }
  if (vid) {
    vid.removeAttribute('src');
    try {
      vid.load();
    } catch (_) {}
    vid.hidden = true;
  }
  if (img) {
    img.onload = null;
    img.onerror = null;
    img.removeAttribute('src');
    img.hidden = true;
  }
  if (ph) ph.hidden = false;
}

function setStreamSource(url) {
  const vid = $('rtspPreview');
  const img = $('mjpegStreamPreview');
  const ph = $('streamPlayerPlaceholder');
  const err = $('streamPlayerError');
  if (!vid || !ph) return;
  if (err) {
    err.hidden = true;
    err.textContent = '';
  }
  const labelRtspErr = typeof t === 'function' ? t('stream.player_error_rtsp') : 'RTSP wird hier nicht unterstützt.';
  const labelMjpegErr = typeof t === 'function' ? t('stream.player_error_mjpeg') : 'MJPEG-Fehler';

  if (streamLooksLikeMjpegHttp(url) && img) {
    vid.removeAttribute('src');
    try {
      vid.load();
    } catch (_) {}
    vid.hidden = true;
    img.onload = () => {
      if (err) err.hidden = true;
    };
    img.onerror = () => {
      if (err) err.textContent = labelMjpegErr;
      if (err) err.hidden = false;
    };
    img.src = url;
    img.hidden = false;
    ph.hidden = true;
    return;
  }

  if (img) {
    img.removeAttribute('src');
    img.hidden = true;
  }
  vid.hidden = false;
  ph.hidden = true;
  vid.src = url;
  vid.play().catch(() => {
    toast(labelRtspErr, 'warn');
    if (err) err.textContent = labelRtspErr;
    if (err) err.hidden = false;
  });
}

function wireStreamHtml5Player() {
  const vid = $('rtspPreview');
  if (!vid || vid.dataset.streamWired === '1') return;
  vid.dataset.streamWired = '1';
  const labelRtspErr = () => (typeof t === 'function' ? t('stream.player_error_rtsp') : 'RTSP wird hier nicht unterstützt.');
  vid.addEventListener('error', () => {
    const el = $('streamPlayerError');
    if (!el) return;
    el.textContent = labelRtspErr();
    el.hidden = false;
  });
  vid.addEventListener('playing', () => {
    const el = $('streamPlayerError');
    if (el) el.hidden = true;
  });
  vid.addEventListener('loadeddata', () => {
    const el = $('streamPlayerError');
    if (el) el.hidden = true;
  });
}

function renderStreamingUrls() {
  const host = $('host').value.trim() || '192.168.0.1';
  const ts = tailscaleIPFromOverview();
  const items = [
    { label: 'RTSP A', url: `rtsp://${host}:554/livestream/12` },
    { label: 'RTSP B', url: `rtsp://${host}/liveRTSP/av4` }
  ];
  if (ts) {
    items.push({ label: 'RTSP A (Tailscale)', url: `rtsp://${ts}:554/livestream/12` });
    items.push({ label: 'RTSP B (Tailscale)', url: `rtsp://${ts}/liveRTSP/av4` });
  }
  const box = $('streamList');
  box.textContent = '';
  resetStreamPlayer();

  const playLabel = typeof t === 'function' ? t('stream.play') : 'Abspielen';
  const copyLabel = typeof t === 'function' ? t('stream.copy') : 'Kopieren';

  items.forEach((row) => {
    const wrap = document.createElement('div');
    wrap.className = 'stream-row';
    const span = document.createElement('span');
    span.textContent = row.label;
    const code = document.createElement('code');
    code.textContent = row.url;
    const play = document.createElement('button');
    play.type = 'button';
    play.className = 'btn-small';
    play.textContent = playLabel;
    play.title = playLabel;
    play.addEventListener('click', () => setStreamSource(row.url));
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn-small';
    btn.textContent = copyLabel;
    btn.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(row.url);
        toast(typeof t === 'function' ? (document.documentElement.lang === 'de' ? 'URL kopiert' : 'URL copied') : 'URL kopiert', 'success');
      } catch (e) {
        toast(typeof t === 'function' ? (document.documentElement.lang === 'de' ? 'Kopieren fehlgeschlagen' : 'Copy failed') : 'Kopieren fehlgeschlagen', 'error');
      }
    });
    wrap.append(span, code, play, btn);
    box.appendChild(wrap);
  });
}

async function saveConn() {
  const c = connPayload();
  await postConfig({
    host: c.host,
    transport: 'ssh',
    password: c.password,
    ssh_port: c.ssh_port,
    telnet_port: 23,
    iptables_path: '',
    auto_connect: $('autoConnect') ? $('autoConnect').checked : false,
    tailscale_auth_key: $('ts_key').value.trim(),
    tailscale_hostname: $('ts_hostname').value.trim(),
    tailscale_routes: $('ts_routes').value.trim(),
    tailscale_exit_node: $('ts_exit_node').checked,
    tailscale_accept_routes: $('ts_accept_routes').checked,
    clear_tailscale_key_after_up: $('clear_ts_key').checked,
    flespi_token: $('flespi_token').value.trim(),
    flespi_host: $('flespi_host').value.trim(),
    flespi_device_id: $('flespi_device_id') ? $('flespi_device_id').value.trim() : '',
    flespi_device_type_id: $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : '',
    flespi_interval: flespiIntervalValue(),
    flespi_autostart: $('flespi_autostart') ? $('flespi_autostart').checked : false
  });
  toast('Konfiguration gespeichert.', 'success');
  return true;
}

async function postConfig(config) {
  return post('/api/config', config);
}

function syncPeerSelect() {
  const select = $('tsPeerSelect');
  const host = $('host').value.trim();
  let found = false;
  for (let i = 0; i < select.options.length; i += 1) {
    if (select.options[i].value === host) {
      select.selectedIndex = i;
      found = true;
      break;
    }
  }
  if (!found) select.selectedIndex = 0;
}

function ssbadge(id, state, label, tooltip) {
  const el = $(id);
  if (!el) return;
  el.classList.remove('ok', 'bad', 'none');
  el.classList.add(state || 'none');
  if (label !== undefined) {
    const lbl = el.querySelector('.ssb-label');
    if (lbl) lbl.textContent = label;
  }
  if (tooltip !== undefined) {
    el.title = tooltip || '';
  }
}

async function loadLocalTailscale() {
  let data;
  try {
    data = await request('/api/local/tailscale');
  } catch (err) {
    data = { ok: false, running: false, error: err.message };
  }

  const select = $('tsPeerSelect');
  const box = $('peerBox');
  while (select.options.length > 1) select.remove(1);

  if (data.running && Array.isArray(data.peers) && data.peers.length) {
    box.classList.add('visible');
    data.peers.forEach((peer) => {
      const option = document.createElement('option');
      option.value = peer.host || '';
      option.textContent = peer.label || peer.host || peer.dns_name || (typeof t === 'function' ? t('conn.peer_unknown') : '?');
      if (peer.dns_name) option.title = peer.dns_name;
      select.appendChild(option);
    });
  } else {
    box.classList.remove('visible');
  }
  syncPeerSelect();
  // tsFallbackHost merken (erste Peer-IP)
  if (data.running && Array.isArray(data.peers) && data.peers.length) {
    state.tsFallbackHost = data.peers[0].host || '';
  }
  const installBtn = $('btnTsInstall');
  if (installBtn) {
    if (data.running || data.installed) {
      installBtn.textContent = typeof t === 'function' ? t('ts.btn.reinstall') : 'Reinstall';
      installBtn.classList.remove('btn-success');
      installBtn.classList.add('btn-default');
    } else {
      installBtn.textContent = typeof t === 'function' ? t('ts.btn.install') : 'Install';
      installBtn.classList.remove('btn-default');
      installBtn.classList.add('btn-success');
    }
  }
}

async function refreshStatusBar() {
  const c = connPayload();
  if (!c.host) {
    chip('chipCam', t('chip.host_missing'), 'warn');
    chip('chipWifi', '-');
    $('errorBanner').hidden = true;
    $('errorBanner').textContent = '';
    state.lastOverview = null;
    ssbadge('sbBadgeCam', 'none');
    return;
  }

  chip('chipCam', t('chip.checking'), 'warn');
  let data;
  try {
    data = await post('/api/overview', c);
  } catch (err) {
    data = { status_ok: false, tailscale_ok: false, errors: { status: err.message } };
  }
  state.lastOverview = data;
  const eb = $('errorBanner');
  if (data.errors && (data.errors.status || data.errors.tailscale)) {
    const parts = [];
    if (data.errors.status) parts.push(`${t('status.banner.status_lbl')}: ${data.errors.status}`);
    if (data.errors.tailscale) parts.push(`${t('status.banner.ts_lbl')}: ${data.errors.tailscale}`);
    eb.textContent = parts.join(' · ');
    eb.hidden = false;
  } else {
    eb.textContent = '';
    eb.hidden = true;
  }

  if (data.status_ok) {
    const source = String(data.status_source || data.camera_transport || c.transport || 'ssh').toLowerCase();
    const sourceLabel = source === 'ssh' ? 'SSH' : source.toUpperCase();
    chip('chipCam', tr('chip.cam_ok', { src: sourceLabel, host: data.camera_host || c.host }), 'ok');
    const camTsIpNow = data.tailscale_cam && data.tailscale_cam.tailscale_ip
      ? String(data.tailscale_cam.tailscale_ip).trim() : '';
    const connIp = camTsIpNow || data.camera_host || c.host || '';
    const connLabel = camTsIpNow ? tr('badge.via_ts', { src: sourceLabel }) : tr('badge.via_wifi', { src: sourceLabel });
    ssbadge('sbBadgeCam', 'ok', connLabel, connIp);
  } else {
    const statusErr = data.errors && data.errors.status;
    chip('chipCam', statusErr ? tr('chip.disconnected_err', { err: statusErr }) : t('chip.disconnected'), 'bad');
    ssbadge('sbBadgeCam', 'bad', t('badge.offline'), statusErr || '');
  }
  const uptimeSec = data.uptime_raw ? parseFloat(data.uptime_raw.split(' ')[0]) : NaN;
  if (data.status_ok && !isNaN(uptimeSec)) {
    const ud = Math.floor(uptimeSec / 86400);
    const uh = Math.floor((uptimeSec % 86400) / 3600);
    const um = Math.floor((uptimeSec % 3600) / 60);
    const us = Math.floor(uptimeSec % 60);
    const shortLabel = ud > 0 ? `${ud}d ${uh}h` : uh > 0 ? `${uh}h ${um}m` : `${um}m`;
    const longLabel = ud > 0
      ? `${ud}d ${uh}h ${um}m ${us}s`
      : uh > 0 ? `${uh}h ${um}m ${us}s` : `${um}m ${us}s`;
    ssbadge('sbBadgeUptime', 'ok', tr('badge.uptime_line', { short: shortLabel }), longLabel);
  } else {
    ssbadge('sbBadgeUptime', 'none', t('badge.uptime_short'), '');
  }
  try {
    const dm = await post('/api/flespi/daemon', { ...c, action: 'status' });
    updateFlespiBadgeFromDaemon(dm);
  } catch (err) {
    ssbadge('sbBadgeFlespi', 'none', t('badge.flespi.unknown'), tr('tooltip.flespi.err_fetch', { err: err.message || t('tooltip.ts.unknown_generic') }));
  }

  const ap = data.ap_summary || '-';
  let wifiLabel = ap;
  let wifiStatus = 'warn';
  if (ap.startsWith('client:')) {
    wifiLabel = `${t('chip.client_prefix')} ${ap.slice('client:'.length).trim()}`;
    wifiStatus = 'ok';
  } else if (ap.startsWith('ap:')) {
    wifiLabel = `${t('chip.ap_prefix')} ${ap.slice('ap:'.length).trim()}`;
    wifiStatus = 'warn';
  } else if (ap !== '-') {
    wifiStatus = 'ok';
  }
  chip('chipWifi', wifiLabel, wifiStatus);

  const camTsIp = tailscaleIPFromOverview();
  const tsCam = data.tailscale_cam || {};
  const tsRunning = tsCam.running === true || tsCam.running === 'true';
  const tsInstalled = tsCam.installed === true || tsCam.installed === 'true';
  const tsJoined = tsCam.joined === true || tsCam.joined === 'true';
  const tsBackend = String(tsCam.backend_state || '').trim();
  const tsSuf = tsBackend ? ' · ' + tsBackend : '';
  if (camTsIp) {
    chip('chipTs', `${t('chip.ts.cam_prefix')} ${camTsIp}${tsSuf}`, 'ok');
    ssbadge('sbBadgeTs', 'ok', t('badge.ts.running'), tr('tooltip.ts.running_cam', { ip: camTsIp, suffix: tsSuf }));
  } else if (tsRunning || tsJoined) {
    const body = tsBackend || t('chip.ts.tailscaled_running');
    chip('chipTs', `${t('chip.ts.cam_prefix')} ${body}`, 'ok');
    ssbadge('sbBadgeTs', 'ok', t('badge.ts.running'), tr('tooltip.ts.running_plain', { detail: body }));
  } else if (tsInstalled && data.tailscale_ok) {
    chip('chipTs', `${t('chip.ts.cam_prefix')} ${t('chip.ts.installed_stopped')}`, 'bad');
    ssbadge('sbBadgeTs', 'bad', t('badge.ts.not_running'), t('tooltip.ts.installed_stopped'));
  } else if (data.tailscale_ok) {
    chip('chipTs', `${t('chip.ts.cam_prefix')} ${t('chip.ts.not_installed')}`, 'warn');
    ssbadge('sbBadgeTs', 'none', t('badge.ts.not_installed'), t('tooltip.ts.not_installed'));
  } else {
    const tsErr = data.errors && data.errors.tailscale;
    chip('chipTs', tsErr ? `${t('chip.ts.cam_prefix')} ${tr('chip.ts.err_body', { err: tsErr })}` : `${t('chip.ts.cam_prefix')} ${t('chip.ts.unknown_body')}`, 'warn');
    ssbadge('sbBadgeTs', 'none', t('badge.ts.unknown'), tsErr ? tr('tooltip.ts.unknown_cam', { err: tsErr }) : t('tooltip.ts.unknown_generic'));
  }
}

// Sets a coloured status dot in a card's x_title (id = element holding the dot).
function setCardStatus(dotId, st) {
  const el = $(dotId);
  if (!el) return;
  el.className = `card-status-dot ${st}`;
}

function updateWifiConnectButton() {
  const btn = $('btnWifiConnect');
  const ssidEl = $('wifiSSID');
  if (!btn) return;
  const hasSSID = !!(ssidEl && ssidEl.value.trim());
  btn.disabled = !state.wifiRequirementsOk || !hasSSID;
}

function wifiStatusUpdate(d) {
  const bar    = $('wifiStatusBar');
  const dot    = $('wifiDot');
  const label  = $('wifiLabel');
  const detail = $('wifiDetail');
  const setState = (st, lbl, det) => {
    bar.className    = `fwd-status-bar ${st}`;
    dot.className    = `status-dot ${st}`;
    label.className  = `fwd-label ${st}`;
    label.textContent = lbl;
    detail.textContent = det;
    setCardStatus('cardDotWifi', st);
  };
  if (!d || !d.ok) {
    state.wifiRequirementsOk = false;
    setState('unknown', 'FEHLER', d?.error || 'Status konnte nicht abgerufen werden');
    const lk0 = $('wifiClientLock');
    if (lk0) {
      lk0.hidden = false;
      const t = lk0.querySelector('div');
      if (t) t.textContent = 'Status fehlgeschlagen — Anforderungen prüfen.';
    }
    updateWifiConnectButton();
    return;
  }
  let parsed = {};
  try { parsed = JSON.parse(d.raw || '{}'); } catch (_) { parsed = {}; }
  const mode    = parsed.mode || 'unknown';
  const ssid    = parsed.ssid || '';
  const ip      = parsed.ip   || '?';
  const wrapper = parsed.wrapper_installed;
  const confSD  = parsed.wpa_conf_on_sd;
  const wpalib  = parsed.wpalib_on_sd;

  $('wpalibStatus').textContent = wpalib ? 'OK — /mnt/sd/wpalib' : 'fehlt';
  $('wpalibStatus').style.color = wpalib ? '#9ae6b4' : 'var(--warn)';

  $('wifiWrapperStatus').textContent = wrapper ? 'OK — main_app Wrapper' : 'fehlt';
  $('wifiWrapperStatus').style.color = wrapper ? '#9ae6b4' : 'var(--muted)';

  const reqsOk = !!wpalib && !!wrapper;
  state.wifiRequirementsOk = reqsOk;
  const lk = $('wifiClientLock');
  if (lk) lk.hidden = reqsOk;
  updateWifiConnectButton();

  const installReqBtn = $('btnInstallRequirements');
  if (installReqBtn) {
    if (reqsOk) {
      installReqBtn.textContent = 'Entfernen';
      installReqBtn.classList.replace('btn-success', 'btn-danger');
    } else {
      installReqBtn.textContent = 'Installieren';
      installReqBtn.classList.replace('btn-danger', 'btn-success');
    }
  }

  if (mode === 'client') {
    setState('active', 'CLIENT-MODE', `${ssid} · IP: ${ip}${confSD ? '' : ' · wpa_conf fehlt'}`);
  } else if (mode === 'ap') {
    setState('inactive', 'AP-MODE', `SSID: ${ssid} · ${ip}${wrapper ? ' · Wrapper bereit' : ' · Wrapper fehlt'}`);
  } else {
    setState('unknown', 'UNBEKANNT', `mode=${mode} · ip=${ip}`);
  }
}

async function tsAutostartRefresh() {
  try {
    const d = await post('/api/tailscale/autostart', { ...connPayload(), action: 'status' });
    let parsed = {};
    try { parsed = JSON.parse(d.raw || '{}'); } catch (_) {}
    const el = $('tsAutostartStatus');
    const checkbox = $('ts_autostart');
    if (!el && !checkbox) return;
    if (parsed.installed && parsed.wrapper_ok) {
      if (el) {
        el.textContent = 'Autostart aktiv — startet beim nächsten Boot';
        el.style.color = 'var(--success, #48bb78)';
      }
      if (checkbox) checkbox.checked = true;
    } else if (parsed.installed) {
      if (el) {
        el.textContent = 'Autostart-Sentinel gesetzt, aber main_app-Wrapper fehlt';
        el.style.color = 'var(--warn, #d2aa5f)';
      }
      if (checkbox) checkbox.checked = true;
    } else {
      if (el) {
        el.textContent = parsed.wrapper_ok ? 'Autostart inaktiv' : 'Autostart inaktiv (main_app-Wrapper fehlt)';
        el.style.color = 'var(--muted)';
      }
      if (checkbox) checkbox.checked = false;
    }
  } catch (_) {}
}

async function refreshTailscalePanel() {
  const c = connPayload();
  let status = null;
  try {
    status = await post('/api/tailscale/status', c);
  } catch (err) {
    status = { ok: false, error: err.message };
  }
  let parsed = {};
  try { parsed = JSON.parse(status.raw || '{}'); } catch (_) {}
  const installed = parsed.installed === true || parsed.installed === 'true';
  const running = parsed.running === true || parsed.running === 'true';
  const tsIP = parsed.tailscale_ip || parsed.ts_ip || '';
  state.tsCamera = { installed, running };
  updateTailscaleGate(installed, running, tsIP, status.error || parsed.error || '');
  if (parsed.advertise_exit_node === true || parsed.advertise_exit_node === 'true') $('ts_exit_node').checked = true;
  if (parsed.advertise_routes && !$('ts_routes').value.trim()) $('ts_routes').value = parsed.advertise_routes;
  await refreshTailscaleRoutes(parsed.advertise_routes || $('ts_routes').value.trim());
  await tsAutostartRefresh();
}

async function refreshTailscaleRoutes(selectedValue = '') {
  const box = $('tsRouteChoices');
  if (!box) return;
  const selected = new Set(String(selectedValue || '').split(',').map((v) => v.trim()).filter(Boolean));
  try {
    const data = await post('/api/tailscale/routes', connPayload());
    let parsed = {};
    try { parsed = JSON.parse(data.raw || '{}'); } catch (_) {}
    const routes = Array.isArray(parsed.routes) ? parsed.routes : [];
    box.textContent = '';
    if (!routes.length) {
      const hint = document.createElement('span');
      hint.className = 'hint';
      hint.textContent = 'Keine lokalen Netze erkannt.';
      box.appendChild(hint);
      return;
    }
    routes.forEach((route) => {
      const cidr = String(route.cidr || '').trim();
      if (!cidr) return;
      const label = document.createElement('label');
      label.className = 'check';
      const input = document.createElement('input');
      input.type = 'checkbox';
      input.className = 'ts-route-choice ts-connect-control';
      input.value = cidr;
      input.checked = selected.has(cidr);
      input.disabled = !state.tsCamera.installed;
      input.addEventListener('change', syncSelectedTailscaleRoutes);
      const span = document.createElement('span');
      span.textContent = route.label || cidr;
      label.append(input, span);
      box.appendChild(label);
    });
    syncSelectedTailscaleRoutes();
  } catch (err) {
    box.textContent = '';
    const hint = document.createElement('span');
    hint.className = 'hint';
    hint.textContent = 'Routen konnten nicht erkannt werden: ' + (err.message || String(err));
    box.appendChild(hint);
  }
}

function syncSelectedTailscaleRoutes() {
  const values = Array.from(document.querySelectorAll('.ts-route-choice:checked')).map((el) => el.value);
  if ($('ts_routes')) $('ts_routes').value = values.join(',');
}

function updateTailscaleGate(installed, running, tsIP, errorText) {
  const bar = $('tsStatusBar');
  const dot = $('tsDot');
  const label = $('tsLabel');
  const detail = $('tsDetail');
  const lock = $('tsConnectLock');
  const setState = (st, lbl, det) => {
    if (bar) bar.className = `fwd-status-bar ${st}`;
    if (dot) dot.className = `status-dot ${st}`;
    if (label) { label.className = `fwd-label ${st}`; label.textContent = lbl; }
    if (detail) detail.textContent = det;
    setCardStatus('cardDotTs', st);
  };
  if (!installed) {
    setState('unknown', 'NICHT INSTALLIERT', errorText || '/mnt/sd/tailscale fehlt');
  } else if (running) {
    setState('active', 'VERBUNDEN', tsIP ? `Tailscale-IP: ${tsIP}` : 'tailscaled läuft');
  } else {
    setState('inactive', 'INSTALLIERT', 'tailscaled ist gestoppt oder noch nicht verbunden');
  }
  if (lock) {
    lock.hidden = installed;
    const msg = lock.querySelector('div');
    if (msg) msg.textContent = 'Tailscale ist auf der Kamera noch nicht installiert. Erst installieren, dann verbinden.';
  }
  document.querySelectorAll('.ts-connect-control').forEach((el) => { el.disabled = !installed; });
  document.querySelectorAll('.ts-route-choice').forEach((el) => { el.disabled = !installed; });
  if ($('btnTsStart')) $('btnTsStart').disabled = !installed || running;
  if ($('btnTsUp')) $('btnTsUp').textContent = running ? 'tailscale set' : 'tailscale up';
  const installBtn = $('btnTsInstall');
  if (installBtn) {
    installBtn.textContent = installed ? 'Neu installieren' : 'Installieren';
    installBtn.classList.toggle('btn-success', !installed);
    installBtn.classList.toggle('btn-default', installed);
  }
}

function flespiIntervalValue() {
  const raw = $('flespi_interval') ? Number.parseInt($('flespi_interval').value, 10) : 30;
  if (!Number.isFinite(raw) || raw <= 0) return 30;
  return Math.min(3600, Math.max(5, raw));
}

function flespiDaemonPayload(action) {
  return {
    ...connPayload(),
    action,
    token: $('flespi_token') ? $('flespi_token').value.trim() : '',
    device_id: $('flespi_device_id') ? $('flespi_device_id').value.trim() : '',
    device_type_id: $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : '',
    interval: flespiIntervalValue(),
    autostart: $('flespi_autostart') ? $('flespi_autostart').checked : false
  };
}

async function refreshFlespiDaemonStatus() {
  const status = await post('/api/flespi/daemon', { ...connPayload(), action: 'status' });
  daemonStatusUpdate(status);
  updateFlespiBadgeFromDaemon(status);
  return status;
}

function parseFlespiDaemonStatus(d) {
  let parsed = {};
  try { parsed = JSON.parse(d && d.raw ? d.raw : '{}'); } catch (_) {}
  if (d && typeof d === 'object') {
    ['running', 'installed', 'conf_ok', 'device_ok', 'device_id', 'pid', 'autostart', 'interval', 'last_log'].forEach((key) => {
      if (parsed[key] === undefined && d[key] !== undefined) parsed[key] = d[key];
    });
  }
  if (parsed.running === true || parsed.running === 'true') parsed.installed = true;
  return parsed;
}

function daemonStatusUpdate(d) {
  const bar    = $('daemonStatusBar');
  const dot    = $('daemonDot');
  const label  = $('daemonLabel');
  const detail = $('daemonDetail');
  if (!bar) return;
  const parsed = parseFlespiDaemonStatus(d);
  const running   = parsed.running === true || parsed.running === 'true';
  const installed = parsed.installed === true || parsed.installed === 'true';
  const confOK    = parsed.conf_ok === true || parsed.conf_ok === 'true';
  const deviceOK  = parsed.device_ok === true || parsed.device_ok === 'true';
  const deviceID  = parsed.device_id || '';
  const autostart = parsed.autostart === true || parsed.autostart === 'true';
  const interval = Number.parseInt(parsed.interval, 10);
  const lastLog   = (parsed.last_log || '').replace(/\|/g, ' · ');
  const st = running ? 'active' : (installed ? 'inactive' : 'unknown');
  bar.className   = `fwd-status-bar compact ${st}`;
  dot.className   = `status-dot ${st}`;
  label.className = `fwd-label ${st}`;
  setCardStatus('cardDotDaemon', st);
  const installBtn = $('btnDaemonInstall');
  const startBtn   = $('btnDaemonStart');
  const stopBtn    = $('btnDaemonStop');
  const intervalInput = $('flespi_interval');
  const autostartInput = $('flespi_autostart');
  if (installBtn) installBtn.textContent = installed ? 'Neu deployen' : 'Deployen';
  if (startBtn)  startBtn.disabled  = running || !installed;
  if (stopBtn)   stopBtn.disabled   = !running;
  if (installed && intervalInput && Number.isFinite(interval) && document.activeElement !== intervalInput) intervalInput.value = String(interval);
  if (installed && autostartInput) autostartInput.checked = autostart;
  if (running) {
    label.textContent  = 'AKTIV';
    if (!deviceOK) {
      detail.textContent = `Device-ID ungültig${deviceID ? `: ${deviceID}` : ''}`;
      setCardStatus('cardDotDaemon', 'unknown');
    } else if (/FEHLER curl|curl rc=|FEHLER HTTP|curl_error/i.test(lastLog)) {
      detail.textContent = humanizeFlespiLog(lastLog);
      setCardStatus('cardDotDaemon', 'unknown');
    } else {
      detail.textContent = lastLog || 'läuft';
    }
  } else if (installed) {
    label.textContent  = 'GESTOPPT';
    if (!confOK) detail.textContent = 'Token fehlt in flespi.conf';
    else if (!deviceOK) detail.textContent = `Device-ID fehlt/ungültig${deviceID ? `: ${deviceID}` : ''}`;
    else detail.textContent = 'Konfig OK · nicht gestartet';
  } else {
    label.textContent  = 'NICHT DEPLOYT';
    detail.textContent = 'Deployment erforderlich';
  }
}

function updateFlespiBadgeFromDaemon(d) {
  const parsed = parseFlespiDaemonStatus(d);
  const running = parsed.running === true || parsed.running === 'true';
  const installed = parsed.installed === true || parsed.installed === 'true';
  const confOK = parsed.conf_ok === true || parsed.conf_ok === 'true';
  const deviceOK = parsed.device_ok === true || parsed.device_ok === 'true';
  const deviceID = parsed.device_id || '';
  const lastLog = (parsed.last_log || '').replace(/\|/g, ' · ');
  if (running) {
    const detail = !deviceOK
      ? tr('tooltip.flespi.running_bad_dev', { id: deviceID || '—' })
      : (lastLog || t('tooltip.flespi.running_default'));
    ssbadge('sbBadgeFlespi', 'ok', t('badge.flespi.running'), detail);
    return;
  }
  if (installed) {
    const detail = !confOK
      ? t('tooltip.flespi.stopped_no_token')
      : !deviceOK
        ? tr('tooltip.flespi.stopped_bad_dev', { id: deviceID || '—' })
        : t('tooltip.flespi.stopped_idle');
    ssbadge('sbBadgeFlespi', 'bad', t('badge.flespi.not_running'), detail);
    return;
  }
  if (d && d.ok === false) {
    ssbadge('sbBadgeFlespi', 'none', t('badge.flespi.unknown'), tr('tooltip.flespi.err_fetch', { err: d.error || t('tooltip.ts.unknown_generic') }));
    return;
  }
  ssbadge('sbBadgeFlespi', 'none', t('badge.flespi.not_deployed'), t('tooltip.flespi.need_deploy'));
}

function humanizeFlespiLog(line) {
  const text = String(line || '').replace(/\s+/g, ' ').trim();
  if (/Device .*ftp|Device-ID.*ftp|device_id.*ftp/i.test(text)) {
    return 'Flespi Device-ID ist falsch: "ftp" ist kein numerisches Gerät.';
  }
  if (/Could not resolve|resolve host|Name or service|DNS/i.test(text)) {
    return 'Flespi nicht erreichbar: DNS/Internet auf der Kamera prüfen.';
  }
  if (/Connection timed out|timed out|No route to host|Network is unreachable/i.test(text)) {
    return 'Flespi nicht erreichbar: Route/UP04/Tailscale/Internet prüfen.';
  }
  if (/HTTP 401|HTTP 403/i.test(text)) return 'Flespi lehnt ab: Token/Rechte prüfen.';
  if (/HTTP 404/i.test(text)) return 'Flespi Device-ID existiert nicht oder Endpoint ist falsch.';
  if (/FEHLER curl|curl rc=|curl_error/i.test(text)) return text;
  return text || 'Flespi-Status unbekannt';
}

function startStatusPolling() {
  if (state.barTimer) clearInterval(state.barTimer);
  state.barTimer = setInterval(refreshCurrentTab, 15000);
}

let _logUnread = 0;

function appLogOpen() {
  const dock = $('logDock');
  if (dock.hidden) {
    dock.hidden = false;
    _logUnread = 0;
    const badge = $('logBadge');
    if (badge) badge.hidden = true;
  }
}

function appLogToggle() {
  const dock = $('logDock');
  if (dock.hidden) {
    appLogOpen();
  } else {
    dock.hidden = true;
    _logUnread = 0;
  }
}

function appLogClear() {
  const box = $('appLogConsole');
  box.textContent = '';
  const empty = document.createElement('span');
  empty.className = 'log-empty';
  empty.textContent = '— Log geleert —';
  box.appendChild(empty);
  _logUnread = 0;
  const badge = $('logBadge');
  if (badge) badge.hidden = true;
}

function appLogSeparator(label, facility = 'app') {
  const box = $('appLogConsole');
  const empty = box.querySelector('.log-empty');
  if (empty) empty.remove();
  const row = document.createElement('div');
  row.className = 'log-separator';
  row.dataset.facility = facility;
  row.textContent = label;
  box.appendChild(row);
  applyLogFacilityFilter();
  box.scrollTop = box.scrollHeight;
}

function appLog(text, facility = 'app') {
  const box = $('appLogConsole');
  const empty = box.querySelector('.log-empty');
  if (empty) empty.remove();
  const now = new Date().toTimeString().slice(0, 8);
  const fac = String(facility || 'app').toLowerCase();
  let added = 0;
  String(text).split('\n').forEach((rawLine) => {
    const line = rawLine.trimEnd();
    if (!line.trim()) return;
    if (/^---+/.test(line.trim())) {
      appLogSeparator(line.replace(/^-+\s*/, '').replace(/\s*-+$/, '') || fac, fac);
      return;
    }
    let cls = 'info';
    const t = line.trim();
    if (t.startsWith('###'))                                        cls = 'header';
    else if (t.startsWith('---'))                                   cls = 'step';
    else if (/fehler|error|FEHLER/i.test(t))                        cls = 'error';
    else if (/OK\b|aktiv|gestartet|deployen|gesetzt|flush/i.test(t)) cls = 'ok';
    else if (/warn|revert|watchdog/i.test(t))                       cls = 'warn';
    else if (t.startsWith('  ') || t.startsWith('\t'))             cls = 'dim';
    const row = document.createElement('div');
    row.className = 'log-line';
    row.dataset.facility = fac;
    const ts = document.createElement('span');
    ts.className = 'log-ts';
    ts.textContent = now;
    const facilityEl = document.createElement('span');
    facilityEl.className = 'log-facility';
    facilityEl.textContent = fac;
    const msg = document.createElement('span');
    msg.className = `log-msg ${cls}`;
    msg.textContent = line;
    row.append(ts, facilityEl, msg);
    box.appendChild(row);
    added++;
  });
  applyLogFacilityFilter();
  box.scrollTop = box.scrollHeight;
  if (added && $('logDock').hidden) {
    _logUnread += added;
    const badge = $('logBadge');
    if (badge) { badge.textContent = _logUnread > 99 ? '99+' : _logUnread; badge.hidden = false; }
  }
}

function applyLogFacilityFilter() {
  const select = $('logFacilityFilter');
  const box = $('appLogConsole');
  if (!select || !box) return;
  const value = select.value || 'all';
  box.querySelectorAll('[data-facility]').forEach((row) => {
    row.classList.toggle('is-hidden', value !== 'all' && row.dataset.facility !== value);
  });
}

function startOutputSpinner(id, headline) {
  let secs = 0;
  const el = $(id);
  const tick = () => {
    el.textContent = headline + '\n' + secs + ' s...';
    secs++;
  };
  tick();
  const timer = setInterval(tick, 1000);
  return () => clearInterval(timer);
}

async function runButton(button, busyLabel, work, onError) {
  const done = setBusy(button, busyLabel);
  try {
    return await work();
  } catch (err) {
    if (onError) onError(err);
    toast(err.message || String(err), 'error');
    return null;
  } finally {
    done();
  }
}

async function loadConfig() {
  const config = await request('/api/config');
  $('host').value = config.host || '192.168.0.1';
  $('password').value = config.password || '';
  $('ssh_port').value = config.ssh_port || 22;
  $('ts_key').value = config.tailscale_auth_key || '';
  $('ts_hostname').value = config.tailscale_hostname || '';
  $('ts_routes').value = config.tailscale_routes || '';
  $('ts_exit_node').checked = !!config.tailscale_exit_node;
  $('ts_accept_routes').checked = !!config.tailscale_accept_routes;
  $('clear_ts_key').checked = !!config.clear_tailscale_key_after_up;
  $('flespi_token').value = config.flespi_token || '';
  $('flespi_host').value = config.flespi_host || '';
  if ($('flespi_device_id')) $('flespi_device_id').value = config.flespi_device_id || '';
  if ($('flespi_device_type_id')) $('flespi_device_type_id').value = config.flespi_device_type_id || '';
  if ($('flespi_interval')) $('flespi_interval').value = config.flespi_interval || 30;
  if ($('flespi_autostart')) $('flespi_autostart').checked = !!config.flespi_autostart;
  if ($('autoConnect')) $('autoConnect').checked = !!config.auto_connect;
  syncPeerSelect();
  return config;
}

function wireApiTokenGate() {
  $('apiTokenSave').addEventListener('click', async () => {
    const t = $('apiTokenInput').value.trim();
    if (!t) {
      toast('Token eingeben', 'error');
      return;
    }
    sessionStorage.setItem('x800_addon_api_token', t);
    if (window.__apiTokenResume) {
      window.__apiTokenResume();
      window.__apiTokenResume = null;
      return;
    }
    try {
      await loadConfig();
      $('apiTokenGate').hidden = true;
      toast('API freigeschaltet', 'success');
    } catch (e) {
      if (e.status === 401) {
        sessionStorage.removeItem('x800_addon_api_token');
        toast('Token ungültig', 'error');
      } else {
        toast(e.message || String(e), 'error');
      }
    }
  });
}

function wireEvents() {
  document.querySelectorAll('[data-tab]').forEach((button) => {
    button.addEventListener('click', () => showTab(button.dataset.tab));
  });

  $('tsPeerSelect').addEventListener('change', () => {
    if ($('tsPeerSelect').value) {
      $('host').value = $('tsPeerSelect').value;
      syncPeerSelect();
    }
  });

  $('btnTsPeersRefresh').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Aktualisiere...', loadLocalTailscale);
  });

  $('btnTest').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Verbinde...', async () => {
      const c = connPayload();
      let data = await post('/api/test', c);
      if (!data.ok && state.tsFallbackHost && state.tsFallbackHost !== c.host) {
        appLog(`Direkt fehlgeschlagen, versuche Tailscale: ${state.tsFallbackHost}`);
        data = await post('/api/test', { ...c, host: state.tsFallbackHost });
        if (data.ok) {
          $('host').value = state.tsFallbackHost;
          toast(`Verbunden via Tailscale: ${state.tsFallbackHost}`, 'success');
        }
      }
      if (!data.ok) throw new Error(data.error || 'Verbindung fehlgeschlagen');
      toast(`Verbindung OK (${data.transport || c.transport})`, 'success');
      await refreshStatusBar();
    });
  });

  $('btnTestSave').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Teste...', async () => {
      const c = connPayload();
      let data = await post('/api/test', c);
      if (!data.ok && state.tsFallbackHost && state.tsFallbackHost !== c.host) {
        appLog(`Direkt fehlgeschlagen, versuche Tailscale: ${state.tsFallbackHost}`);
        data = await post('/api/test', { ...c, host: state.tsFallbackHost });
        if (data.ok) {
          $('host').value = state.tsFallbackHost;
          toast(`Verbunden via Tailscale: ${state.tsFallbackHost}`, 'success');
        }
      }
      if (!data.ok) throw new Error(data.error || 'Verbindung fehlgeschlagen');
      await saveConn();
      await refreshStatusBar();
    });
  });

  $('btnRemoveConn').addEventListener('click', async () => {
    if (!confirm('Konfiguration löschen?')) return;
    const _rm = { host: '192.168.0.1', transport: 'ssh', password: '', ssh_port: 22, telnet_port: 23 };
    await postConfig(_rm);
    await loadConfig();
    toast('Konfiguration zurückgesetzt.', 'success');
  });

  // btnSystemStatus / btnDockRefresh entfernt — Refresh über Sidebar (btnSidebarRefresh)

  if ($('btnSystemReboot')) {
    $('btnSystemReboot').addEventListener('click', (event) => {
      if (!confirm('Kamera jetzt neu starten?')) return;
      runButton(event.currentTarget, 'Reboot...', async () => {
        appLogOpen();
        appLog('--- System Reboot ---');
        const data = await post('/api/system/reboot', connPayload());
        appLog(data.raw || pretty(data));
        if (data.stderr) appLog('[stderr] ' + data.stderr);
        if (!data.ok) throw new Error(data.error || 'Reboot fehlgeschlagen');
        toast('Kamera startet neu...', 'success');
      }, (err) => appLog('FEHLER: ' + err.message));
    });
  }

  $('btnFilesList').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Lade Verzeichnis...', () => loadFiles(), (err) => {
      renderFileTable({ ok: false, error: err.message, entries: [], raw: err.message });
    });
  });

  $('filePath').addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      $('btnFilesList').click();
    }
  });
  $('filePath').addEventListener('change', updateFileActionState);

  $('btnFilesUp').addEventListener('click', async () => {
    const par = state.fileParent || '/';
    runButton($('btnFilesUp'), '...', () => loadFiles(par), (err) => toast(err.message, 'error'));
  });

  $('btnFilesView').addEventListener('click', (event) => {
    const f = state.fileSel;
    if (!f || f.type !== 'file') return;
    runButton(event.currentTarget, '...', () => openFileView(f.path), (err) => toast(err.message, 'error'));
  });

  $('btnFilesDownload').addEventListener('click', (event) => {
    runButton(event.currentTarget, '...', () => downloadSelectedFile(), (err) => toast(err.message, 'error'));
  });

  $('btnFilesUpload').addEventListener('click', () => {
    if ($('btnFilesUpload').disabled) return;
    $('fileUploadInput').click();
  });
  $('fileUploadInput').addEventListener('change', async (ev) => {
    const file = ev.target.files && ev.target.files[0];
    ev.target.value = '';
    if (!file) return;
    if (file.size > 48 * 1024 * 1024) {
      toast('Datei zu gross (max. 48 MB)', 'error');
      return;
    }
    const c = connPayload();
    const dir = $('filePath').value.trim() || '/mnt/sd';
    if (!dir.startsWith('/mnt/sd')) {
      toast('Upload nur unter /mnt/sd', 'error');
      return;
    }
    const fd = new FormData();
    fd.append('conn', JSON.stringify(c));
    fd.append('target_dir', dir);
    fd.append('file', file, file.name);
    appLogOpen();
    appLog(`--- Upload ${file.name} → ${dir} ---`);
    try {
      const headers = { ...apiHeaders() };
      const res = await fetch('/api/files/upload', { method: 'POST', headers, body: fd });
      const text = await res.text();
      let data = {};
      try { data = text ? JSON.parse(text) : {}; } catch (_) { data = { raw: text }; }
      if (!res.ok) throw new Error(data.error || text || `HTTP ${res.status}`);
      appLog(pretty(data));
      toast('Upload OK', 'success');
      await loadFiles(dir);
    } catch (e) {
      appLog('FEHLER: ' + (e.message || String(e)));
      toast(e.message || String(e), 'error');
    }
  });

  $('btnSidebarConsole').addEventListener('click', appLogToggle);
  $('btnLogClear').addEventListener('click', appLogClear);
  if ($('logFacilityFilter')) $('logFacilityFilter').addEventListener('change', applyLogFacilityFilter);

  $('btnSidebarRefresh').addEventListener('click', () => {
    const btn = $('btnSidebarRefresh');
    const active = btn.getAttribute('aria-pressed') === 'true';
    if (active) {
      if (state.barTimer) { clearInterval(state.barTimer); state.barTimer = null; }
      btn.setAttribute('aria-pressed', 'false');
    } else {
      startStatusPolling();
      refreshCurrentTab();
      btn.setAttribute('aria-pressed', 'true');
    }
  });

  if ($('btnFirmwareUpload')) {
    $('btnFirmwareUpload').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Lade hoch...', async () => {
        const input = $('firmwareFile');
        const file = input.files && input.files[0];
        if (!file) throw new Error('Firmware-Datei fehlt');
        if (!file.name.toLowerCase().endsWith('.bin')) throw new Error('Firmware muss eine .bin-Datei sein');
        if (file.size > 170 * 1024 * 1024) throw new Error('Firmware-Datei zu groß');
        const method = $('firmwareMethod').value || 'ssh';
        const fd = new FormData();
        fd.append('conn', JSON.stringify(connPayload()));
        fd.append('method', method);
        fd.append('reboot', $('firmwareReboot').checked ? 'true' : 'false');
        fd.append('firmware', file, file.name);
        appLogOpen();
        appLog(`--- Firmware Upload ${file.name} (${file.size} bytes) via ${method.toUpperCase()} → /mnt/sd/FW98530A.bin ---`);
        const headers = { ...apiHeaders() };
        const res = await fetch('/api/firmware/upload', { method: 'POST', headers, body: fd });
        const text = await res.text();
        let data = {};
        try { data = text ? JSON.parse(text) : {}; } catch (_) { data = { raw: text }; }
        appLog(pretty(data));
        const result = $('firmwareResult');
        if (result) {
          result.hidden = false;
          result.textContent = pretty(data);
        }
        const sd = data.sd_check || {};
        if (sd.errors) {
          toast(`SD-Check blockiert Reboot: ${sd.errors}`, 'error');
        } else if (sd.warnings || data.warning) {
          toast(sd.warnings || data.warning, 'error');
        }
        if (!res.ok || !data.ok) throw new Error(data.error || text || `HTTP ${res.status}`);
        if (data.warning) toast(data.warning, 'error');
        if (!sd.errors) toast(data.reboot ? 'Firmware hochgeladen, SD geprüft, Reboot angestoßen' : 'Firmware hochgeladen und SD geprüft', 'success');
      }, (err) => appLog('FEHLER Firmware: ' + err.message));
    });
  }

  // ── WiFi Modus ───────────────────────────────────────────────────

  $('btnInstallRequirements').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Installiere...', async () => {
      appLogOpen();
      appLog('--- Anforderungen: wpalib (~9 MB) ---');
      const u = await post('/api/wifi/upload-wpalib', connPayload());
      appLog(u.raw || pretty(u));
      if (u.stderr) appLog('[stderr] ' + u.stderr);
      if (!u.ok) throw new Error(u.error || 'wpalib fehlgeschlagen');
      appLog('--- Anforderungen: main_app Wrapper ---');
      const w = await post('/api/wifi/wrapper', { ...connPayload(), action: 'install' });
      appLog(w.raw || pretty(w));
      if (w.stderr) appLog('[stderr] ' + w.stderr);
      if (!w.ok) throw new Error(w.error || 'Wrapper fehlgeschlagen');
      toast('Anforderungen installiert', 'success');
      const s = await post('/api/wifi/status', connPayload());
      wifiStatusUpdate(s);
      if (confirm('Anforderungen installiert. Jetzt Kamera neu starten? Der Reboot ist nötig, damit der main_app-Wrapper aktiv wird.')) {
        appLog('--- Reboot nach Anforderungen ---');
        const rb = await post('/api/system/reboot', connPayload());
        appLog(rb.raw || pretty(rb));
        if (rb.stderr) appLog('[stderr] ' + rb.stderr);
        if (!rb.ok) throw new Error(rb.error || 'Reboot fehlgeschlagen');
        toast('Kamera startet neu...', 'success');
      }
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnWifiConnect').addEventListener('click', (event) => {
    const ssid = $('wifiSSID').value.trim();
    if (!ssid) { toast('SSID fehlt', 'error'); return; }
    if ($('btnWifiConnect').disabled) { toast('Zuerst Anforderungen installieren', 'error'); return; }
    runButton(event.currentTarget, 'Schreibe conf & Reboot...', async () => {
      appLogOpen();
      appLog(`--- Verbinde mit "${ssid}" ---`);
      appLog('--- 70mai WiFi immer aktiv prüfen ---');
      const vendor = await post('/api/wifi/vendor-check', connPayload());
      appLog(vendor.raw || pretty(vendor));
      if (!vendor.ok) {
        const msg = vendor.hint || vendor.error || '70mai-WiFi-Status nicht prüfbar';
        toast(msg, 'error');
        throw new Error(msg);
      }
      const data = await post('/api/wifi/connect', {
        ...connPayload(),
        ssid,
        pwd:      $('wifiPWD').value,
        staticip: $('wifiStaticIP').value.trim(),
        mask:     $('wifiMask').value.trim(),
        gw:       $('wifiGW').value.trim(),
        dns:      $('wifiDNS').value.trim(),
      });
      appLog(data.raw || pretty(data));
      if (data.stderr) appLog('[stderr] ' + data.stderr);
      if (!data.ok) throw new Error(data.error || 'Verbindung fehlgeschlagen');
      toast('wpa_supplicant.conf geschrieben — Kamera startet neu...', 'success');
      wifiStatusUpdate({ ok: true, raw: JSON.stringify({ mode: 'client', ssid, ip: 'DHCP...', wrapper_installed: true, wpa_conf_on_sd: true }) });
    }, (err) => appLog('FEHLER: ' + err.message));
  });
  if ($('wifiSSID')) {
    $('wifiSSID').addEventListener('input', updateWifiConnectButton);
  }

  $('btnWifiDisconnect').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Trenne & Reboot...', async () => {
      appLogOpen();
      appLog('--- Trennen → AP-Mode ---');
      const data = await post('/api/wifi/disconnect', connPayload());
      appLog(data.raw || pretty(data));
      if (data.stderr) appLog('[stderr] ' + data.stderr);
      if (!data.ok) throw new Error(data.error || 'Trennen fehlgeschlagen');
      toast('wpa_supplicant.conf gelöscht — Kamera startet neu in AP-Mode...', 'success');
      wifiStatusUpdate({ ok: true, raw: JSON.stringify({ mode: 'ap', ssid: '', ip: '192.168.0.1', wrapper_installed: true, wpa_conf_on_sd: false }) });
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  if ($('btnWifiHealth')) {
    $('btnWifiHealth').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Prüfe...', async () => {
        appLogOpen();
        appLog('--- WiFi Health-Check auf Kamera ---');
        const data = await post('/api/wifi/health', connPayload());
        appLog(data.raw || pretty(data));
        if (data.stderr) appLog('[stderr] ' + data.stderr);
        if (!data.ok) throw new Error(data.error || 'Health-Check fehlgeschlagen');
        let parsed = {};
        try { parsed = JSON.parse(data.raw || '{}'); } catch (_) {}
        const resultEl = $('wifiHealthResult');
        const internetOK = parsed.internet_ok === true || parsed.internet_ok === 'true';
        const gwOK = parsed.gateway_ok === true || parsed.gateway_ok === 'true';
        const rows = Array.isArray(parsed.results) ? parsed.results : [];
        const summary = rows.map((r) => `${r.label || r.target}: ${r.ok ? 'OK' : 'FAIL'}${r.ms ? ` ${r.ms} ms` : ''}`).join(' · ');
        if (resultEl) {
          resultEl.textContent = `${internetOK ? 'Internet OK' : 'Internet nicht erreichbar'} · Gateway ${gwOK ? 'OK' : 'FAIL'} · ${summary}`;
          resultEl.style.color = internetOK ? '#9ae6b4' : 'var(--warn)';
        }
        toast(internetOK ? 'WiFi Health OK' : 'WiFi Health: Internet nicht erreichbar', internetOK ? 'success' : 'error');
      }, (err) => appLog('FEHLER Health: ' + err.message));
    });
  }

  $('btnWifiScan').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Scanne...', async () => {
      const statusEl = $('wifiScanStatus');
      const resultsEl = $('wifiScanResults');
      const scanBtn = event.currentTarget;
      if (statusEl) statusEl.textContent = 'Scanne Netzwerke…';
      if (resultsEl) resultsEl.style.display = 'none';
      const data = await post('/api/wifi/scan', connPayload());
      if (!data.ok) throw new Error(data.error || 'Scan fehlgeschlagen');
      let nets = [];
      try { nets = JSON.parse(data.raw || '[]'); } catch (_) { nets = []; }
      if (!Array.isArray(nets) || nets.length === 0) {
        if (statusEl) statusEl.textContent = 'Keine Netzwerke gefunden';
        return;
      }
      if (statusEl) statusEl.textContent = `${nets.length} Netzwerk(e) gefunden`;
      if (resultsEl) {
        resultsEl.textContent = '';
        nets.forEach((n) => {
          const item = document.createElement('div');
          item.className = 'wifi-scan-item';
          const ssidSpan = document.createElement('span');
          ssidSpan.className = 'scan-ssid';
          ssidSpan.textContent = n.ssid || '(hidden)';
          const metaSpan = document.createElement('span');
          metaSpan.className = 'scan-meta';
          const sig = n.signal != null ? `${n.signal} dBm` : '';
          const freq = n.freq ? ` · ${n.freq}` : '';
          metaSpan.textContent = sig + freq;
          item.append(ssidSpan, metaSpan);
          if (n.ssid) {
            item.addEventListener('click', () => {
              $('wifiSSID').value = n.ssid;
              updateWifiConnectButton();
              $('wifiPWD').focus();
            });
          }
          resultsEl.appendChild(item);
        });
        resultsEl.style.display = '';
      }
      scanBtn.disabled = true;
      setTimeout(() => { scanBtn.disabled = false; }, 45000);
    }, (err) => {
      const statusEl = $('wifiScanStatus');
      if (statusEl) statusEl.textContent = 'Fehler: ' + err.message;
    });
  });

  $('btnTsInstall').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Lade Binaries...', async () => {
      appLogOpen();
      const data = await post('/api/tailscale/download', connPayload());
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Installation fehlgeschlagen');
      toast('Tailscale installiert. tailscaled kann jetzt gestartet werden.', 'success');
      await refreshTailscalePanel();
      await refreshStatusBar();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnTsStart').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Starte tailscaled...', async () => {
      appLogOpen();
      const data = await post('/api/tailscale/start', connPayload());
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Start fehlgeschlagen');
      await refreshTailscalePanel();
      await refreshStatusBar();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnTsAutostartStatus').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Prüfe Status...', async () => {
      appLogOpen();
      const data = await post('/api/tailscale/status', connPayload());
      appLog(data.raw || pretty(data));
      await refreshTailscalePanel();
      await refreshStatusBar();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('ts_autostart').addEventListener('change', (event) => {
    const checked = event.currentTarget.checked;
    event.currentTarget.disabled = true;
    (async () => {
      try {
        appLogOpen();
        const action = checked ? 'deploy' : 'remove';
        const data = await post('/api/tailscale/autostart', { ...connPayload(), action });
        appLog(data.raw || pretty(data));
        if (!data.ok) throw new Error(data.error || 'Autostart fehlgeschlagen');
        toast(checked ? 'Autostart aktiviert.' : 'Autostart deaktiviert.', 'success');
      } catch (err) {
        toast(err.message || String(err), 'error');
        appLog('FEHLER: ' + (err.message || String(err)));
      } finally {
        event.currentTarget.disabled = false;
        await tsAutostartRefresh();
      }
    })();
  });

  $('btnTsKeySave').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Speichere...', async () => {
      const current = await request('/api/config');
      current.tailscale_auth_key = $('ts_key').value.trim();
      current.tailscale_hostname = $('ts_hostname').value.trim();
      current.tailscale_routes = $('ts_routes').value.trim();
      current.tailscale_exit_node = $('ts_exit_node').checked;
      current.tailscale_accept_routes = $('ts_accept_routes').checked;
      current.clear_tailscale_key_after_up = $('clear_ts_key').checked;
      current.flespi_token = $('flespi_token').value.trim();
      current.flespi_host = $('flespi_host').value.trim();
      current.flespi_device_type_id = $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : '';
      await postConfig(current);
      toast('Tailscale-Konfig gespeichert.', 'success');
    });
  });

  $('btnTsUp').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Verbinde Tailscale...', async () => {
      appLogOpen();
      const body = {
        ...connPayload(),
        authkey: $('ts_key').value.trim(),
        hostname: $('ts_hostname').value.trim(),
        routes: $('ts_routes').value.trim(),
        exit_node: $('ts_exit_node').checked,
        accept_routes: $('ts_accept_routes').checked,
        clear_tailscale_key_after_up: $('clear_ts_key').checked
      };
      const data = await post('/api/tailscale/up', body);
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'tailscale up/set fehlgeschlagen');
      let parsed = {};
      try { parsed = JSON.parse(data.raw || '{}'); } catch (_) {}
      toast(parsed.msg === 'set' ? 'tailscale set ausgeführt.' : 'tailscale up ausgeführt.', 'success');
      await loadConfig();
      await refreshTailscalePanel();
      await refreshStatusBar();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  // btnStreamingRefresh entfernt — Refresh über Top-Leiste (btnDockRefresh)

  if ($('btnFlespiSave')) $('btnFlespiSave').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Speichere...', async () => {
      const current = await request('/api/config');
      current.flespi_token = $('flespi_token').value.trim();
      current.flespi_host = $('flespi_host').value.trim();
      current.flespi_device_id = $('flespi_device_id') ? $('flespi_device_id').value.trim() : '';
      current.flespi_device_type_id = $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : '';
      current.flespi_interval = flespiIntervalValue();
      current.flespi_autostart = $('flespi_autostart') ? $('flespi_autostart').checked : false;
      await postConfig(current);
      toast('Flespi gespeichert.', 'success');
    });
  });

  $('btnFlespiTest').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Teste...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/test', {
        token: $('flespi_token').value.trim(),
        host: $('flespi_host').value.trim()
      });
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Flespi-Test fehlgeschlagen');
      toast('Flespi Token OK', 'success');
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  if ($('btnFlespiDevice')) $('btnFlespiDevice').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Prüfe Device...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/device', {
        token: $('flespi_token').value.trim(),
        host: $('flespi_host').value.trim(),
        device_type_id: $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : ''
      });
      appLog(data.preview || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Device konnte nicht angelegt werden');
      if ($('flespi_device_id')) $('flespi_device_id').value = data.device_id || '';
      toast(data.created ? `Flespi Device angelegt: ${data.device_id}` : `Flespi Device gefunden: ${data.device_id}`, 'success');
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnFlespiDeploy').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Deploy...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/deploy', {
        ...connPayload(),
        include_token: true,
        token: $('flespi_token').value.trim(),
        device_id: $('flespi_device_id') ? $('flespi_device_id').value.trim() : '',
        device_type_id: $('flespi_device_type_id') ? $('flespi_device_type_id').value.trim() : '',
        interval: flespiIntervalValue(),
        autostart: $('flespi_autostart') ? $('flespi_autostart').checked : false
      });
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Deploy fehlgeschlagen');
      if (data.device_id && $('flespi_device_id')) $('flespi_device_id').value = data.device_id;
      await refreshFlespiDaemonStatus();
      toast('Flespi-Daemon deployed', 'success');
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  if ($('btnGpsRefresh')) {
    $('btnGpsRefresh').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Lade GPS...', async () => {
        const data = await refreshGps();
        appLogOpen();
        appLog(gpsLogLine(data), 'gps');
      }, (err) => appLog('FEHLER GPS: ' + err.message, 'gps'));
    });
  }

  if ($('btnGpsDebug')) {
    $('btnGpsDebug').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Debug...', gpsDebugToConsole, (err) => appLog('FEHLER GPS Debug: ' + err.message, 'gps'));
    });
  }

  if ($('btnGpsLive')) {
    $('btnGpsLive').addEventListener('click', () => setGpsLive(!state.gpsLiveTimer));
  }

  // ── Flespi Daemon ─────────────────────────────────────────────────
  if ($('btnDaemonStatus')) $('btnDaemonStatus').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Prüfe...', async () => {
      const data = await refreshFlespiDaemonStatus();
      appLog(data.raw || pretty(data));
    }, (err) => appLog('FEHLER Daemon: ' + err.message));
  });

  if ($('btnDaemonInstall')) $('btnDaemonInstall').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Deploye...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/daemon', flespiDaemonPayload('install'));
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Deploy fehlgeschlagen');
      toast('flespi-daemon.sh deployt', 'success');
      await refreshFlespiDaemonStatus();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnDaemonStart').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Starte...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/daemon', { ...connPayload(), action: 'start' });
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Start fehlgeschlagen');
      toast('flespi-daemon gestartet', 'success');
      await refreshFlespiDaemonStatus();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  $('btnDaemonStop').addEventListener('click', (event) => {
    runButton(event.currentTarget, 'Stoppe...', async () => {
      appLogOpen();
      const data = await post('/api/flespi/daemon', { ...connPayload(), action: 'stop' });
      appLog(data.raw || pretty(data));
      if (!data.ok) throw new Error(data.error || 'Stop fehlgeschlagen');
      toast('flespi-daemon gestoppt', 'success');
      await refreshFlespiDaemonStatus();
    }, (err) => appLog('FEHLER: ' + err.message));
  });

  // ── Ethernet ───────────────────────────────────────────────────────
  async function loadEthStatus() {
    const data = await post('/api/ethernet', { ...connPayload(), action: 'status' });
    const dot = $('ethDot'), label = $('ethLabel'), detail = $('ethDetail');
    if (!data.ok || !data.raw) {
      if (dot) { dot.className = 'status-dot bad'; }
      if (label) { label.textContent = 'FEHLER'; label.className = 'fwd-label bad'; }
      if (detail) detail.textContent = data.error || 'Status fehlgeschlagen';
      if ($('cardDotEth')) $('cardDotEth').className = 'card-status-dot bad';
      return data;
    }
    let parsed = {};
    try { parsed = JSON.parse(data.raw); } catch (_) {}
    const exists = parsed.exists === true;
    const up = parsed.up === true;
    const ip = parsed.ip || '-';
    const drv = parsed.driver_loaded === true;
    const autostart = parsed.autostart === true;
    const link = String(parsed.link) || '0';
    if (exists && up) {
      if (dot) { dot.className = 'status-dot ok'; }
      if (label) { label.textContent = `ETH ${ip}`; label.className = 'fwd-label ok'; }
      if (detail) detail.textContent = `Link ${link} · Driver ${drv ? '✓' : '✗'} · MAC ${parsed.mac || '-'}`;
      if ($('cardDotEth')) $('cardDotEth').className = 'card-status-dot ok';
    } else if (exists) {
      if (dot) { dot.className = 'status-dot warn'; }
      if (label) { label.textContent = 'ETH down'; label.className = 'fwd-label warn'; }
      if (detail) detail.textContent = `Link ${link} · Driver ${drv ? '✓' : '✗'}`;
      if ($('cardDotEth')) $('cardDotEth').className = 'card-status-dot warn';
    } else if (drv) {
      if (dot) { dot.className = 'status-dot warn'; }
      if (label) { label.textContent = 'Kein eth0'; label.className = 'fwd-label warn'; }
      if (detail) detail.textContent = 'Treiber geladen, aber kein eth0-Device (USB-Kabel prüfen)';
      if ($('cardDotEth')) $('cardDotEth').className = 'card-status-dot warn';
    } else {
      if (dot) { dot.className = 'status-dot unknown'; }
      if (label) { label.textContent = 'Kein Treiber'; label.className = 'fwd-label unknown'; }
      if (detail) detail.textContent = 'AX88179-Treiber nicht geladen (Hardware nicht erkannt)';
      if ($('cardDotEth')) $('cardDotEth').className = 'card-status-dot none';
    }
    if ($('ethAutostart')) $('ethAutostart').checked = autostart;
    appLog(data.raw || pretty(data), 'ssh');
    return data;
  }

  if ($('btnEthEnable')) {
    $('btnEthEnable').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Aktiviere...', async () => {
        appLogOpen();
        appLog('--- Ethernet aktivieren ---');
        const data = await post('/api/ethernet', { ...connPayload(), action: 'enable' });
        appLog(data.raw || pretty(data), 'ssh');
        if (!data.ok) throw new Error(data.error || 'Enable fehlgeschlagen');
        await loadEthStatus();
        toast('Ethernet aktiviert', 'success');
      }, (err) => appLog('FEHLER: ' + err.message));
    });
  }

  if ($('btnEthDisable')) {
    $('btnEthDisable').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Deaktiviere...', async () => {
        appLogOpen();
        appLog('--- Ethernet deaktivieren ---');
        const data = await post('/api/ethernet', { ...connPayload(), action: 'disable' });
        appLog(data.raw || pretty(data), 'ssh');
        if (!data.ok) throw new Error(data.error || 'Disable fehlgeschlagen');
        await loadEthStatus();
        toast('Ethernet deaktiviert', 'success');
      }, (err) => appLog('FEHLER: ' + err.message));
    });
  }

  if ($('btnEthStatus')) {
    $('btnEthStatus').addEventListener('click', (event) => {
      runButton(event.currentTarget, 'Prüfe...', async () => {
        await loadEthStatus();
      }, (err) => appLog('FEHLER: ' + err.message));
    });
  }

  if ($('ethAutostart')) {
    $('ethAutostart').addEventListener('change', (event) => {
      const checked = event.currentTarget.checked;
      event.currentTarget.disabled = true;
      (async () => {
        try {
          appLogOpen();
          const action = checked ? 'autostart-deploy' : 'autostart-remove';
          const data = await post('/api/ethernet', { ...connPayload(), action });
          appLog(data.raw || pretty(data), 'ssh');
          if (!data.ok) throw new Error(data.error || 'Autostart fehlgeschlagen');
          toast(checked ? 'Ethernet-Autostart aktiviert' : 'Ethernet-Autostart deaktiviert', 'success');
        } catch (err) {
          toast(err.message || String(err), 'error');
          appLog('FEHLER: ' + (err.message || String(err)));
        } finally {
          event.currentTarget.disabled = false;
        }
      })();
    });
  }

  if ($('btnFlespiReset')) {
    $('btnFlespiReset').addEventListener('click', (event) => {
      const confirmMsg = typeof t === 'function' ? t('flespi.reset_confirm') : 'Flespi zurücksetzen?';
      if (!window.confirm(confirmMsg)) return;
      const busy = typeof t === 'function' ? t('flespi.reset_busy') : '…';
      runButton(event.currentTarget, busy, async () => {
        const c = connPayload();
        const removeCam = $('flespi_reset_camera') && $('flespi_reset_camera').checked;
        const data = await post('/api/flespi/reset', { ...c, remove_from_camera: removeCam });
        if (!data.ok) throw new Error(data.error || 'Reset fehlgeschlagen');
        appLogOpen();
        appLog(pretty(data));
        if (data.camera_error) appLog(`Kamera: ${data.camera_error}`);
        await loadConfig();
        await refreshFlespiDaemonStatus();
        await refreshStatusBar();
        const doneMsg = typeof t === 'function' ? t('flespi.reset_done') : 'OK';
        toast(doneMsg, 'success');
      }, (err) => appLog('FEHLER Reset: ' + err.message));
    });
  }

  // ── Theme toggle (Sidebar Slider) ────────────────────────────────
  const themeCheck = $('themeSwitchCheck');
  if (themeCheck) {
    themeCheck.addEventListener('change', () => {
      const dark = themeCheck.checked;
      document.documentElement.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
      const icon = $('themeIcon');
      if (icon) icon.className = dark ? 'bi bi-moon-stars' : 'bi bi-sun';
    });
  }

  // ── Language toggle (Sidebar Slider) ─────────────────────────────
  const langCheck = $('langSwitchCheck');
  if (langCheck) {
    langCheck.addEventListener('change', () => {
      const wantDe = langCheck.checked;
      const curDe = typeof globalThis !== 'undefined' && globalThis.x800_lang === 'de';
      if (wantDe !== curDe && typeof toggleLang === 'function') {
        toggleLang();
      }
      showTab(state.activeTab || 'connection');
      refreshStatusBar();
    });
  }

  wireStreamHtml5Player();

  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) refreshStatusBar();
  });
}

async function boot() {
  wireEvents();
  wireApiTokenGate();
  syncPeerSelect();
  updateFileActionState();
  if (typeof applyI18n === 'function') applyI18n();
  try {
    const versionInfo = await request('/api/version');
    if ($('appVersion') && versionInfo.version) $('appVersion').textContent = `v${versionInfo.version}`;
  } catch (_) {}
  let loadedConfig = null;
  for (;;) {
    try {
      loadedConfig = await loadConfig();
      $('apiTokenGate').hidden = true;
      break;
    } catch (e) {
      if (e.status === 401) {
        $('apiTokenGate').hidden = false;
        await new Promise((resolve) => {
          window.__apiTokenResume = resolve;
        });
        continue;
      }
      toast(e.message || String(e), 'error');
      break;
    }
  }
  await Promise.all([loadLocalTailscale(), refreshStatusBar()]);
  // AutoConnect: wenn gesetzt und Host nicht leer, Verbindung testen
  if (loadedConfig && loadedConfig.auto_connect && connPayload().host) {
    try {
      await post('/api/test', connPayload());
      await refreshStatusBar();
    } catch (_) {}
  }
  startStatusPolling();
  if ($('btnSidebarRefresh')) $('btnSidebarRefresh').setAttribute('aria-pressed', 'true');
}

boot().catch((err) => toast(err.message || String(err), 'error'));
