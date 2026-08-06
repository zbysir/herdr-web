'use strict';
/**
 * 前端：xterm.js + 一层「补协议」的胶水 + 主机/密钥管理 + 手机软键盘条。
 *
 * herdr 启动时会请求这些能力（实测抓包）：
 *   CSI > 7 u        kitty 键盘协议    xterm.js 不支持 → 这里自己编码
 *   OSC 10;? / 11;?  查询前后景色      xterm.js 不回 → 这里自己回
 *   CSI ? 2031 h     主题变更通知      xterm.js 不支持 → 这里自己发
 *   1049/1000/1002/1003/1006/2004/1004/2026  xterm.js 原生支持
 */

const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const TOKEN = new URLSearchParams(location.search).get('token') || '';
const esc = (s) => String(s == null ? '' : s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

/* ------------------------------------------------------------------ 主题 */
const THEMES = {
  dark: {
    background: '#1b1e24', foreground: '#c8ccd4', cursor: '#61afef', cursorAccent: '#1b1e24',
    selectionBackground: '#3e4451',
    black: '#282c34', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#abb2bf',
    brightBlack: '#5c6370', brightRed: '#ef8b93', brightGreen: '#a9d989', brightYellow: '#efd094',
    brightBlue: '#7fc1f5', brightMagenta: '#d79ae6', brightCyan: '#74ccd6', brightWhite: '#e6e9ef',
  },
  light: {
    background: '#fafafa', foreground: '#383a42', cursor: '#4078f2', cursorAccent: '#fafafa',
    selectionBackground: '#d4d8e0',
    black: '#383a42', red: '#e45649', green: '#50a14f', yellow: '#c18401',
    blue: '#4078f2', magenta: '#a626a4', cyan: '#0184bc', white: '#fafafa',
    brightBlack: '#a0a1a7', brightRed: '#ca4a3f', brightGreen: '#437a3f', brightYellow: '#9a6a00',
    brightBlue: '#3059c4', brightMagenta: '#8a1f88', brightCyan: '#016a99', brightWhite: '#ffffff',
  },
};
let scheme = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';

/* ------------------------------------------------------------------ 终端 */
const term = new Terminal({
  allowProposedApi: true,
  fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, "Cascadia Mono", Consolas, "PingFang SC", monospace',
  fontSize: Number(localStorage.getItem('fontSize')) || (matchMedia('(pointer: coarse)').matches ? 11 : 13),
  lineHeight: 1.12,
  cursorBlink: true,
  cursorStyle: 'block',
  macOptionIsMeta: true,      // alt+1 / alt+g 这类 herdr 快捷键需要
  rightClickSelectsWord: false,
  scrollback: 2000,
  drawBoldTextInBrightColors: false,
  theme: THEMES[scheme],
  linkHandler: { activate: (_e, uri) => window.open(uri, '_blank', 'noopener') },
});

const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.loadAddon(new WebLinksAddon.WebLinksAddon((_e, uri) => window.open(uri, '_blank', 'noopener')));
term.loadAddon(new Unicode11Addon.Unicode11Addon());
term.unicode.activeVersion = '11';
term.loadAddon(new ClipboardAddon.ClipboardAddon());   // OSC 52
term.open($('#term'));

try {
  const webgl = new WebglAddon.WebglAddon();
  webgl.onContextLoss(() => webgl.dispose());
  term.loadAddon(webgl);
} catch { /* 没有 WebGL 就退回 DOM 渲染 */ }

/* ------------------------------------------------------------------ 能力上报面板 */
const KNOWN = {
  25: ['光标显隐', true],
  1000: ['鼠标点击上报', true],
  1002: ['鼠标拖拽上报', true],
  1003: ['鼠标移动上报', true],
  1005: ['UTF-8 鼠标坐标', false],
  1006: ['SGR 鼠标坐标', true],
  1015: ['urxvt 鼠标坐标', false],
  1016: ['像素级鼠标坐标', true],
  1004: ['焦点进出上报', true],
  1049: ['备用屏幕', true],
  2004: ['括号粘贴', true],
  2026: ['同步输出（防撕裂）', true],
  2031: ['主题变更通知', true],
};
const caps = new Map();
function noteCap(key, label, ok) {
  if (caps.has(key)) return;
  caps.set(key, { label, ok });
  renderCaps();
}
function renderCaps() {
  const ul = $('#capsList');
  if (!caps.size) { ul.innerHTML = '<li class="muted">连接后这里会列出程序实际用到的转义序列</li>'; return; }
  ul.innerHTML = [...caps].map(([k, v]) =>
    `<li class="${v.ok ? '' : 'no'}"><code>${esc(k)}</code><span>${esc(v.label)}</span></li>`).join('');
}

/* ------------------------------------------------------------------ 补协议 */
const kitty = { flags: 0, stack: [] };

// CSI ? Pm h（开启私有模式）—— 只做旁听，返回 false 让 xterm.js 继续处理
term.parser.registerCsiHandler({ prefix: '?', final: 'h' }, (params) => {
  for (const p of params) {
    const code = Array.isArray(p) ? p[0] : p;
    const known = KNOWN[code];
    if (known) noteCap(`DEC ${code}`, known[0], known[1]);
    if (code === 2031) sendScheme();   // 程序刚订阅，先告诉它当前是什么主题
  }
  return false;
});

// CSI ? 996 n —— 主动查询当前配色方案
term.parser.registerCsiHandler({ prefix: '?', final: 'n' }, (params) => {
  if ((Array.isArray(params[0]) ? params[0][0] : params[0]) === 996) { sendScheme(); return true; }
  return false;
});

// kitty 键盘协议：> 入栈  < 出栈  = 直接设置  ? 查询
term.parser.registerCsiHandler({ prefix: '>', final: 'u' }, (params) => {
  kitty.stack.push(kitty.flags);
  kitty.flags = (Array.isArray(params[0]) ? params[0][0] : params[0]) || 1;
  noteCap('kitty kbd', `键盘协议 flags=${kitty.flags}（这里实现了消歧子集）`, true);
  return true;
});
term.parser.registerCsiHandler({ prefix: '<', final: 'u' }, () => { kitty.flags = kitty.stack.pop() || 0; return true; });
term.parser.registerCsiHandler({ prefix: '=', final: 'u' }, (params) => {
  kitty.flags = (Array.isArray(params[0]) ? params[0][0] : params[0]) || 0;
  return true;
});
term.parser.registerCsiHandler({ prefix: '?', final: 'u' }, () => { send(`\x1b[?${kitty.flags}u`); return true; });

// OSC 10/11 查询前景 / 背景色
const toRgbSpec = (hex) => {
  const h = hex.replace('#', '');
  const p = (i) => (parseInt(h.slice(i, i + 2), 16) * 257).toString(16).padStart(4, '0');
  return `rgb:${p(0)}/${p(2)}/${p(4)}`;
};
for (const [code, pick] of [[10, 'foreground'], [11, 'background']]) {
  term.parser.registerOscHandler(code, (data) => {
    if (data !== '?') return false;
    noteCap(`OSC ${code}`, code === 10 ? '查询前景色' : '查询背景色（用来判断明暗）', true);
    send(`\x1b]${code};${toRgbSpec(THEMES[scheme][pick])}\x1b\\`);
    return true;
  });
}
// 这两个只登记不处理，返回 false 让内置 / 插件继续
term.parser.registerOscHandler(52, () => { noteCap('OSC 52', '用转义序列写系统剪贴板', true); return false; });
term.parser.registerOscHandler(8, () => { noteCap('OSC 8', '终端超链接（点得开）', true); return false; });

function sendScheme() {
  send(`\x1b[?997;${scheme === 'dark' ? 1 : 2}n`);
}

/* ------------------------------------------------------------------ 键盘 */
// kitty 的 CSI u 编码：只补 legacy 编码表达不了的组合，其余仍走 xterm.js 原生编码。
// （herdr 同时支持两套，所以混用是安全的）
function kittySeq(e) {
  if (!$('#optKitty').checked || !(kitty.flags & 1)) return null;
  if (!e.ctrlKey || e.metaKey) return null;

  let code = null;
  if (/^Key[A-Z]$/.test(e.code)) code = e.code.charCodeAt(3) + 32;        // KeyB -> 'b'
  else if (/^Digit[0-9]$/.test(e.code)) code = e.code.charCodeAt(5);      // Digit1 -> '1'
  else if (e.code === 'Enter' || e.code === 'NumpadEnter') code = 13;
  else if (e.code === 'Backspace') code = 127;
  else if (e.code === 'Tab') code = 9;
  else return null;

  // legacy 已经能唯一表达的（纯 Ctrl+字母 = 控制码）就不插手
  const ambiguous = e.shiftKey || code === 13 || code === 9 || (code >= 48 && code <= 57);
  if (!ambiguous) return null;

  const mods = 1 + (e.shiftKey ? 1 : 0) + (e.altKey ? 2 : 0) + 4;
  return `\x1b[${code};${mods}u`;
}

function copy(text) {
  if (!text) return;
  (navigator.clipboard?.writeText(text) ?? Promise.reject()).catch(() => {
    // http 的局域网地址不是安全上下文，navigator.clipboard 不可用，退回老办法
    const ta = document.createElement('textarea');
    ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta); ta.select();
    document.execCommand('copy'); ta.remove();
    term.focus();
  });
}

term.attachCustomKeyEventHandler((e) => {
  if (e.type !== 'keydown') return true;

  if (isMac && e.metaKey) {
    const k = e.key.toLowerCase();
    if (k === 'c' && term.hasSelection()) { copy(term.getSelection()); e.preventDefault(); return false; }
    if (k === 'k') { term.clear(); e.preventDefault(); return false; }
    return true;                       // ⌘V 及其它 ⌘ 组合留给浏览器
  }
  if (!isMac && e.ctrlKey && e.shiftKey && e.code === 'KeyC' && term.hasSelection()) {
    copy(term.getSelection()); e.preventDefault(); return false;
  }

  const seq = kittySeq(e);
  if (seq) { send(seq); e.preventDefault(); return false; }
  return true;
});

// 右键让程序自己处理（herdr 开了 1002/1003 鼠标上报）
$('#term').addEventListener('contextmenu', (e) => e.preventDefault());

/* ------------------------------------------------------------------ 触屏滑动 = 滚轮 */
// xterm.js 只把 wheel 事件翻译成鼠标上报，不管 touch。像 herdr 这样
// 「占着备用屏幕 + 开了鼠标上报」的程序，手机上手指划了等于没划：
// 本地没有 scrollback 可滚，程序又收不到滚轮上报。这里自己补这一层。
const screenEl = () => $('#term').querySelector('.xterm-screen');

function cellBox() {
  const el = screenEl();
  if (!el || !term.rows || !term.cols) return null;
  const r = el.getBoundingClientRect();
  return { r, w: r.width / term.cols, h: r.height / term.rows };
}

// 鼠标上报开着的时候，滚动得发给程序；否则滚本地 scrollback
const mouseReporting = () => term.modes.mouseTrackingMode !== 'none';

function cellAt(x, y, box) {
  return {
    col: Math.min(term.cols, Math.max(1, Math.floor((x - box.r.left) / box.w) + 1)),
    row: Math.min(term.rows, Math.max(1, Math.floor((y - box.r.top) / box.h) + 1)),
  };
}

const kbdEl = () => $('#term').querySelector('.xterm-helper-textarea');
const kbdUp = () => document.activeElement === kbdEl();
// 系统键盘也可能被用户自己收起，所以状态跟着 textarea 的 focus/blur 走，别自己记
function syncKbdBtn() {
  for (const b of $$('.sk[data-act="kbd"]')) b.classList.toggle('on', kbdUp());
}
function toggleKeyboard() {
  if (kbdUp()) term.blur(); else term.focus();
  syncKbdBtn();
}

let touch = null;
let lastTapAt = 0;

$('#term').addEventListener('touchstart', (e) => {
  if (e.touches.length !== 1) { touch = null; return; }
  const t = e.touches[0];
  touch = { x: t.clientX, y: t.clientY, x0: t.clientX, y0: t.clientY, acc: 0, at: Date.now(), owned: mouseReporting() };
  // 有程序在收鼠标上报（herdr 这种）时独占手势：
  // 不让浏览器聚焦隐藏的 textarea（点一下就弹键盘的根源）、
  // 不让长按弹出选择气泡、也不补发兼容鼠标事件。
  if (touch.owned && e.cancelable) e.preventDefault();
}, { passive: false });

$('#term').addEventListener('touchmove', (e) => {
  if (!touch || e.touches.length !== 1) return;
  const box = cellBox();
  if (!box) return;
  const t = e.touches[0];
  touch.acc += t.clientY - touch.y;
  touch.y = t.clientY;

  // 没独占的时候（普通 shell），越过阈值再吃掉默认行为，避免页面橡皮筋
  if (!touch.owned && Math.abs(touch.y - touch.y0) > 6 && e.cancelable) e.preventDefault();

  const steps = Math.trunc(touch.acc / box.h);
  if (!steps) return;
  touch.acc -= steps * box.h;

  const dir = steps > 0 ? -1 : 1;            // 手指下滑 = 看更早的内容 = 往上滚
  const n = Math.min(Math.abs(steps), 8);
  if (mouseReporting()) {
    const { col, row } = cellAt(touch.x, t.clientY, box);
    const btn = dir < 0 ? 64 : 65;           // SGR：64 上滚，65 下滚
    for (let i = 0; i < n; i++) send(`\x1b[<${btn};${col};${row}M`);
  } else {
    term.scrollLines(dir * n);
  }
}, { passive: false });

$('#term').addEventListener('touchend', () => {
  const tc = touch;
  touch = null;
  if (!tc) return;
  if (Math.abs(tc.y - tc.y0) > 8 || Math.abs(tc.x - tc.x0) > 8) return;   // 是滑动，已经处理过了

  const now = Date.now();
  const isDouble = now - lastTapAt < 320;
  lastTapAt = now;

  if (isDouble) return toggleKeyboard();     // 双击 = 显示 / 收起键盘
  if (now - tc.at > 500) return;             // 长按 = 什么都不做（重点是别弹键盘）

  if (tc.owned) {
    const box = cellBox();
    if (!box) return;
    const { col, row } = cellAt(tc.x, tc.y, box);
    send(`\x1b[<0;${col};${row}M`);          // 单击照样发给程序，只是不抢焦点
    send(`\x1b[<0;${col};${row}m`);
  } else {
    term.focus();                            // 普通 shell 里点一下就是想打字
  }
}, { passive: true });

term.onSelectionChange(() => {
  if ($('#optCopySel').checked && term.hasSelection()) copy(term.getSelection());
});

/* ------------------------------------------------------------------ 软键盘条（手机） */
const sticky = { ctrl: false, alt: false };

function renderSticky() {
  for (const b of $$('.sk[data-sticky]')) b.classList.toggle('on', sticky[b.dataset.sticky]);
}

// 手机的虚拟键盘不一定给出可靠的 keydown，所以粘滞修饰键在数据层做
function applySticky(d) {
  if (!sticky.ctrl && !sticky.alt) return d;
  if (d.length !== 1) { sticky.ctrl = sticky.alt = false; renderSticky(); return d; }
  let out = d;
  if (sticky.ctrl) {
    const c = d.toLowerCase().charCodeAt(0);
    if (c >= 97 && c <= 122) out = String.fromCharCode(c - 96);          // ctrl+a..z
    else if (d === ' ') out = '\x00';
    else if ('[\\]^_'.includes(d)) out = String.fromCharCode(d.charCodeAt(0) - 64);
    else if (d === '?') out = '\x7f';
  }
  if (sticky.alt) out = '\x1b' + out;
  sticky.ctrl = sticky.alt = false;
  renderSticky();
  return out;
}

$('#softkeys').addEventListener('click', (e) => {
  const b = e.target.closest('.sk');
  if (!b) return;
  if (b.dataset.act === 'kbd') return toggleKeyboard();   // 这一个不能顺手 focus，否则没法收起
  if (b.dataset.sticky) {
    sticky[b.dataset.sticky] = !sticky[b.dataset.sticky];
    renderSticky();
  } else if (b.dataset.send) {
    send(b.dataset.send);
  }
  // 键盘已经弹着就保持，没弹就别硬弹 —— 软键条本身就是为了不弹键盘也能操作
  if (kbdUp()) term.focus();
});

function toggleSoftkeys(show) {
  $('#softkeys').classList.toggle('hidden', !show);
  $('#keysBtn').classList.toggle('on', show);
  localStorage.setItem('softkeys', show ? '1' : '0');
  relayout();
}
$('#keysBtn').onclick = () => toggleSoftkeys($('#softkeys').classList.contains('hidden'));

/* ------------------------------------------------------------------ 连接 */
let ws = null;
let alive = false;
let exited = false;   // 收到过 exit 就别再被 onclose 的通用提示盖掉

function send(data) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: 'i', d: data }));
}
term.onData((d) => send(applySticky(d)));
term.onBinary((d) => {
  if (ws?.readyState !== WebSocket.OPEN) return;
  const buf = new Uint8Array(d.length);
  for (let i = 0; i < d.length; i++) buf[i] = d.charCodeAt(i) & 255;
  ws.send(buf);
});

function setStatus(text, cls) {
  $('#label').textContent = text;
  $('#dot').className = `dot ${cls || ''}`;
}
function showOverlay(msg, btn) {
  $('#ovMsg').innerHTML = msg;
  $('#ovBtn').textContent = btn || '连接';
  $('#overlay').classList.remove('hidden');
}
function toast(msg, ms = 2200) {
  const t = $('#toast');
  t.textContent = msg;
  t.classList.remove('hidden');
  clearTimeout(toast._t);
  toast._t = setTimeout(() => t.classList.add('hidden'), ms);
}

// 连不上的两种原因表现一样（WS 都是直接 close），用一次 HTTP 请求区分开
async function diagnose() {
  let r;
  try {
    r = await fetch(`/api/state?token=${encodeURIComponent(TOKEN)}`);
  } catch {
    return showOverlay('后端没在跑。到 herdr-web 目录里执行 <code>npm start</code>（或 <code>npm run lan</code>）。', '重试');
  }
  if (r.status === 401) {
    return showOverlay('后端在跑，但地址栏里的 <b>token 不对</b>。<br>换成 server 启动时打印的那个链接 —— token 现在存在 <code>~/.herdr-web/token</code>，重启也不会变了。', '重试');
  }
  showOverlay('后端在跑、token 也对，但 WebSocket 建不起来。中间有反代的话确认它转发了 Upgrade 头。', '重试');
}

function currentTarget() {
  const mode = $('#modeSeg .on').dataset.mode;
  if (mode === 'local') return { mode: 'local' };
  const sel = $('#hostSel').value;
  if (sel === '__adhoc') {
    const target = $('#target').value.trim();
    return target ? { mode: 'ssh', target } : null;
  }
  return sel ? { mode: 'host', hostId: sel } : null;
}

function connect(override) {
  const spec = override || currentTarget();
  if (!spec) {
    if ($('#hostSel').value === '__adhoc') $('#target').focus();
    else toast('先添加一台主机');
    return;
  }
  if (ws) { try { ws.close(); } catch { /* noop */ } ws = null; }

  exited = false;
  term.reset();          // 每次连接从干净屏幕开始
  fit.fit();
  const q = new URLSearchParams({ token: TOKEN, cols: term.cols, rows: term.rows, ...spec });
  ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/pty?${q}`);
  ws.binaryType = 'arraybuffer';
  setStatus('连接中…', '');
  $('#overlay').classList.add('hidden');

  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') { term.write(new Uint8Array(ev.data)); return; }
    const m = JSON.parse(ev.data);
    if (m.t === 'ready') {
      alive = true;
      caps.clear(); renderCaps();
      setStatus(`${m.label} · pid ${m.pid} · ${term.cols}×${term.rows}`, 'on');
      // 触屏设备不自动抢焦点：一连上就顶出系统键盘很烦，要打字点软键条上的 ⌨
      if (!matchMedia('(pointer: coarse)').matches) term.focus();
    } else if (m.t === 'exit') {
      exited = true;
      showOverlay(`会话已结束（exit ${m.code}）。屏幕上是它最后的输出。<br>herdr 的 session 还在后台，重连后敲 <code>herdr</code> 就回到原来的工作区。`, '重新连接');
    } else if (m.t === 'fatal') {
      exited = true;
      showOverlay(`启动失败：${esc(m.msg)}`, '重试');
    }
  };
  ws.onclose = () => {
    ws = null;
    setStatus(alive ? '已断开' : '连接失败', 'err');
    if (!exited) {
      if (alive) showOverlay('连接断开了。herdr 的 session 还在后台，重连后敲 <code>herdr</code> 就回到原来的工作区。', '重新连接');
      else diagnose();   // 分清是后端没起还是 token 不对
    }
    alive = false;
  };
  ws.onerror = () => setStatus('连接出错', 'err');
}

/* ------------------------------------------------------------------ 尺寸 */
let resizeTimer = null;
function relayout() {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    try { fit.fit(); } catch { /* 容器还没测量出来 */ }
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ t: 'r', cols: term.cols, rows: term.rows }));
      if (alive) setStatus($('#label').textContent.replace(/\d+×\d+$/, `${term.cols}×${term.rows}`), 'on');
    }
  }, 80);
}
new ResizeObserver(relayout).observe($('#wrap'));
addEventListener('orientationchange', relayout);
visualViewport?.addEventListener('resize', relayout);   // 手机弹出虚拟键盘

/* ------------------------------------------------------------------ 主机 / 密钥管理 */
const api = {
  async req(method, p, body) {
    const r = await fetch(`/api${p}${p.includes('?') ? '&' : '?'}token=${encodeURIComponent(TOKEN)}`, {
      method,
      headers: body ? { 'content-type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }));
    if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
    return j;
  },
  get: (p) => api.req('GET', p),
  post: (p, b) => api.req('POST', p, b),
  put: (p, b) => api.req('PUT', p, b),
  del: (p) => api.req('DELETE', p),
};

let state = { hosts: [], keys: [], sshKeys: [], sshConfig: [], secureContext: true };

const keyLabel = (ref) => {
  if (!ref) return '默认';
  const k = [...state.keys, ...state.sshKeys].find((x) => x.ref === ref);
  return k ? k.name : ref.replace(/^(managed|path):/, '');
};

function renderHostSelect() {
  const sel = $('#hostSel');
  const prev = localStorage.getItem('lastHost') || sel.value;
  sel.innerHTML = state.hosts.map((h) => `<option value="${esc(h.id)}">${esc(h.name)}</option>`).join('')
    + '<option value="__adhoc">— 临时地址 —</option>';
  if (prev && [...sel.options].some((o) => o.value === prev)) sel.value = prev;
  else if (state.hosts.length) sel.value = state.hosts[0].id;
  else sel.value = '__adhoc';
  onHostSelChange();
}
function onHostSelChange() {
  localStorage.setItem('lastHost', $('#hostSel').value);
  syncModeUi();
}

function renderHosts() {
  const el = $('#hostList');
  if (!state.hosts.length) {
    el.innerHTML = '<div class="empty">还没有主机。点「+ 新增」，或者从 ~/.ssh/config 一键导入。</div>';
    return;
  }
  el.innerHTML = state.hosts.map((h) => {
    const where = (h.user ? `${h.user}@` : '') + h.host + (h.port ? `:${h.port}` : '');
    const bits = [where, `密钥 ${keyLabel(h.keyRef)}`];
    if (h.jump) bits.push(`跳板 ${h.jump}`);
    return `<div class="item" data-id="${esc(h.id)}">
      <div class="item-main">
        <div class="item-name">${esc(h.name)}</div>
        <div class="item-sub">${esc(bits.join(' · '))}</div>
      </div>
      <div class="item-act">
        <button class="btn tiny" data-act="conn">连接</button>
        <button class="btn tiny" data-act="copyid" title="ssh-copy-id：把这台主机绑定的公钥装到远端 authorized_keys">装公钥</button>
        <button class="btn tiny" data-act="edit">改</button>
        <button class="btn tiny danger" data-act="del">删</button>
      </div>
    </div>`;
  }).join('');
}

function renderKeys() {
  const el = $('#keyList');
  el.innerHTML = state.keys.length
    ? state.keys.map((k) => `<div class="item" data-name="${esc(k.name)}">
        <div class="item-main">
          <div class="item-name">${esc(k.name)} <span class="badge">${esc(k.type || '?')}${k.bits ? ' ' + k.bits : ''}</span>${k.encrypted ? '<span class="badge warn">已加密</span>' : ''}</div>
          <div class="item-sub">${esc(k.fingerprint || '')}</div>
        </div>
        <div class="item-act">
          <button class="btn tiny" data-act="pub" ${k.publicKey ? '' : 'disabled'}>复制公钥</button>
          <button class="btn tiny danger" data-act="del">删</button>
        </div>
      </div>`).join('')
    : '<div class="empty">没有托管密钥。生成一把新的，或者把已有私钥导进来。</div>';
  $('#keyHint').textContent = `私钥存在 ${state.keyDir || '~/.herdr-web/keys'}（0600），只在本机使用，接口不会把私钥内容发给浏览器。passphrase 和登录密码一概不存 —— ssh 会在终端里当场问你。`;

  const sk = $('#sshKeyList');
  sk.innerHTML = state.sshKeys.length
    ? state.sshKeys.map((k) => `<div class="item">
        <div class="item-main">
          <div class="item-name">${esc(k.name)} <span class="badge">${esc(k.type || '?')}</span>${k.encrypted ? '<span class="badge warn">已加密</span>' : ''}</div>
          <div class="item-sub">${esc(k.fingerprint || '')}</div>
        </div>
      </div>`).join('')
    : '<div class="empty">~/.ssh 下没找到私钥。</div>';

  const sel = $('#keyRefSel');
  const keep = sel.value;
  sel.innerHTML = '<option value="">默认（ssh-agent / ssh_config）</option>'
    + state.keys.map((k) => `<option value="${esc(k.ref)}">托管 · ${esc(k.name)}</option>`).join('')
    + state.sshKeys.map((k) => `<option value="${esc(k.ref)}">~/.ssh · ${esc(k.name)}</option>`).join('');
  if ([...sel.options].some((o) => o.value === keep)) sel.value = keep;
}

async function refresh() {
  state = await api.get('/state');
  renderHostSelect();
  renderHosts();
  renderKeys();
  $('#hostsBtn').title = `主机与密钥管理（${state.hosts.length} 台 / ${state.keys.length} 把）`;
}

/* 主机表单 */
function openHostForm(h) {
  const f = $('#hostForm');
  f.classList.remove('hidden');
  f.id.value = h?.id || '';
  f.name.value = h?.name || '';
  f.host.value = h?.host || '';
  f.user.value = h?.user || '';
  f.port.value = h?.port || '';
  f.jump.value = h?.jump || '';
  f.acceptNew.checked = !!h?.acceptNew;
  $('#keyRefSel').value = h?.keyRef || '';
  $('#hostErr').textContent = '';
  f.name.focus();
}
$('#hostAdd').onclick = () => openHostForm(null);
$('#hostCancel').onclick = () => $('#hostForm').classList.add('hidden');

$('#hostForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = e.target;
  const body = {
    name: f.name.value, host: f.host.value, user: f.user.value,
    port: f.port.value, jump: f.jump.value, keyRef: $('#keyRefSel').value,
    acceptNew: f.acceptNew.checked,
  };
  try {
    if (f.id.value) await api.put(`/hosts/${encodeURIComponent(f.id.value)}`, body);
    else await api.post('/hosts', body);
    f.classList.add('hidden');
    await refresh();
    toast('已保存');
  } catch (err) { $('#hostErr').textContent = err.message; }
});

$('#hostImport').onclick = async () => {
  try {
    const { added } = await api.post('/hosts/import-config', {});
    await refresh();
    toast(added.length ? `从 ssh_config 导入 ${added.length} 台` : '没有可导入的新别名');
  } catch (err) { toast(err.message); }
};

$('#hostList').addEventListener('click', async (e) => {
  const b = e.target.closest('button[data-act]');
  if (!b) return;
  const id = b.closest('.item').dataset.id;
  const h = state.hosts.find((x) => x.id === id);
  if (b.dataset.act === 'conn') {
    $('#hostSel').value = id; onHostSelChange();
    setMode('host');
    $('#hosts').classList.add('hidden');
    connect({ mode: 'host', hostId: id });
  } else if (b.dataset.act === 'copyid') {
    if (!h.keyRef) return toast('这台主机没绑定密钥，先在「改」里选一把');
    $('#hosts').classList.add('hidden');
    connect({ mode: 'copyid', hostId: id });
    toast('远端会问你登录密码，直接在终端里输');
  } else if (b.dataset.act === 'edit') {
    openHostForm(h);
  } else if (b.dataset.act === 'del') {
    if (!confirm(`删除主机「${h.name}」？密钥文件不会被删。`)) return;
    await api.del(`/hosts/${encodeURIComponent(id)}`);
    await refresh();
  }
});

/* 密钥表单 */
function openKeyForm(kind) {
  const f = $('#keyForm');
  f.dataset.kind = kind;
  f.classList.remove('hidden');
  f.name.value = '';
  f.privateKey.value = '';
  $('#pemWrap').classList.toggle('hidden', kind !== 'import');
  $('#keyErr').textContent = '';
  f.name.focus();
}
$('#keyGen').onclick = () => openKeyForm('generate');
$('#keyImp').onclick = () => openKeyForm('import');
$('#keyCancel').onclick = () => $('#keyForm').classList.add('hidden');

$('#keyForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = e.target;
  try {
    const kind = f.dataset.kind;
    const { key } = kind === 'generate'
      ? await api.post('/keys/generate', { name: f.name.value })
      : await api.post('/keys/import', { name: f.name.value, privateKey: f.privateKey.value });
    f.privateKey.value = '';
    f.classList.add('hidden');
    await refresh();
    if (kind === 'generate' && key.publicKey) {
      copy(key.publicKey);
      toast('已生成，公钥已复制 —— 贴到远端 authorized_keys 即可');
    } else {
      toast('已导入');
    }
  } catch (err) { $('#keyErr').textContent = err.message; }
});

$('#keyList').addEventListener('click', async (e) => {
  const b = e.target.closest('button[data-act]');
  if (!b) return;
  const name = b.closest('.item').dataset.name;
  const k = state.keys.find((x) => x.name === name);
  if (b.dataset.act === 'pub') {
    copy(k.publicKey);
    toast('公钥已复制');
  } else if (b.dataset.act === 'del') {
    if (!confirm(`删除密钥「${name}」？引用它的主机会连不上。`)) return;
    await api.del(`/keys/${encodeURIComponent(name)}`);
    await refresh();
  }
});

/* ------------------------------------------------------------------ 顶栏交互 */
// 点顶栏 / 面板不能把键盘焦点从终端抢走，否则点完「敲 herdr」还得再点一下终端才能打字
document.addEventListener('mousedown', (e) => {
  if (!e.target.closest('.bar, .panel, .softkeys')) return;
  if (e.target.closest('input:not([type=checkbox]), textarea, select')) return;
  e.preventDefault();
});

// 顶栏里 SSH 相关控件的显隐由「当前模式 + 选中的主机」共同决定，统一从这里走
function syncModeUi() {
  const ssh = $('#modeSeg .on')?.dataset.mode !== 'local';
  $('#hostSel').classList.toggle('hidden', !ssh);
  $('#hostsBtn').classList.toggle('hidden', !ssh);
  $('#target').classList.toggle('hidden', !ssh || $('#hostSel').value !== '__adhoc');
  relayout();
}
function setMode(mode) {
  for (const x of $('#modeSeg').children) x.classList.toggle('on', x.dataset.mode === mode);
  syncModeUi();
}
$('#modeSeg').addEventListener('click', (e) => {
  const b = e.target.closest('button');
  if (b) setMode(b.dataset.mode);
});
$('#hostSel').onchange = onHostSelChange;
$('#target').addEventListener('keydown', (e) => { if (e.key === 'Enter') connect(); });
$('#connect').onclick = () => connect();
$('#ovBtn').onclick = () => connect();
$('#runHerdr').onclick = () => { send('herdr\r'); term.focus(); };

function setFont(size) {
  term.options.fontSize = Math.max(7, Math.min(28, size));
  localStorage.setItem('fontSize', term.options.fontSize);
  relayout();
}
$('#fontUp').onclick = () => { setFont(term.options.fontSize + 1); term.focus(); };
$('#fontDown').onclick = () => { setFont(term.options.fontSize - 1); term.focus(); };

$('#themeBtn').onclick = () => {
  scheme = scheme === 'dark' ? 'light' : 'dark';
  term.options.theme = THEMES[scheme];
  document.documentElement.classList.toggle('light', scheme === 'light');
  if (caps.has('DEC 2031')) sendScheme();   // 程序订阅过就通知它重绘
};
matchMedia('(prefers-color-scheme: light)').addEventListener('change', (e) => {
  const next = e.matches ? 'light' : 'dark';
  if (next !== scheme) $('#themeBtn').click();
});

$('#capsBtn').onclick = () => {
  $('#hosts').classList.add('hidden');
  $('#caps').classList.toggle('hidden');
};
$('#hostsBtn').onclick = () => {
  $('#caps').classList.add('hidden');
  $('#hosts').classList.toggle('hidden');
  if (!$('#hosts').classList.contains('hidden')) refresh().catch((e) => toast(e.message));
};
for (const b of $$('[data-close]')) b.onclick = () => $(b.dataset.close).classList.add('hidden');
$('#optMeta').onchange = (e) => { term.options.macOptionIsMeta = e.target.checked; };

/* ------------------------------------------------------------------ 启动 */
document.documentElement.classList.toggle('light', scheme === 'light');
for (const ev of ['focus', 'blur']) kbdEl()?.addEventListener(ev, syncKbdBtn);
renderCaps();
renderSticky();
setMode('local');
toggleSoftkeys(localStorage.getItem('softkeys') === '1'
  || (localStorage.getItem('softkeys') === null && matchMedia('(pointer: coarse)').matches));
fit.fit();

if (!TOKEN) {
  showOverlay('URL 里缺 token。复制 <code>node server.js</code> 打印的那个链接（手机可以直接扫终端里的二维码）。', '仍然试试');
} else {
  refresh().catch((e) => toast('读取主机列表失败：' + e.message));
}
