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

/* ------------------------------------------------------------------ 渲染看门狗 */
// xterm.js 6.0 的 RenderService 有三条「收下重绘请求但不画」的路：
//   1. DEC 2026 同步输出开着时，refreshRows() 把范围攒进 SynchronizedOutputHandler，
//      等 `CSI ? 2026 l`（ESU）或 1s 兜底超时才真画；
//   2. 真正的绘制在 requestAnimationFrame 里，后台标签页里 rAF 完全不跑；
//   3. IntersectionObserver 判定终端不可见时直接 `_isPaused` 挂起。
// herdr 三条全踩：它常驻开着 2026，一帧几 KB 还会被 WebSocket 和 xterm.js 的写队列
// 拆成多次 write，跨好几个 rAF —— 攒漏一次，屏幕上就留一块没画上的空白。
// 缓冲区里字一直是好的（滚一下或改字号强制重绘就回来了），所以这里只补重绘：
// 数据流停下来之后强制画一次；2026 卡在开着的状态就先自己补个 ESU，
// 不然 refresh() 会被一样攒起来。
let paintTimer = null;
let paintHeals = 0;

const armRepaint = () => { clearTimeout(paintTimer); paintTimer = setTimeout(repaint, 180); };

function repaint() {
  clearTimeout(paintTimer);
  if (term.modes.synchronizedOutputMode) {
    // 流都停了还开着 = 这一帧的 ESU 没等到。自己收尾（xterm.js 自带的 1s 兜底太慢了），
    // 这一句写下去本身就会触发全屏重绘。
    paintHeals++;
    term.write('\x1b[?2026l');
    noteHeal();
    return;
  }
  term.refresh(0, term.rows - 1);
}

function noteHeal() {
  const el = $('#renderInfo');
  el.classList.remove('hidden');
  el.textContent = `同步输出补过 ${paintHeals} 次收尾：herdr 的 2026 帧没等到 ESU，`
    + '重绘被攒住了（缓冲区没坏，只是没画上）。频繁出现就把上面的同步输出关掉。';
}

// 后台标签页里 rAF 不跑，攒下的那一帧要回到前台才画 —— 回来立刻补一次，别等下一批数据
addEventListener('focus', repaint);
document.addEventListener('visibilitychange', () => { if (!document.hidden) repaint(); });

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
  // 面板里关掉同步输出时就地吞掉，别让 xterm.js 进「只攒不画」的状态（见渲染看门狗）。
  // 只吞单独的 `CSI ? 2026 h`（herdr 就是这么发的），混在别的参数里一律放过去。
  const only = params.length === 1 && (Array.isArray(params[0]) ? params[0][0] : params[0]);
  if (only === 2026 && !$('#opt2026').checked) return true;
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

/* ---- 软键条的按键从服务端下发，在网页上编辑（存服务端，多设备共用一份）---- */
let skKeys = [];

function renderSoftkeys() {
  $('#skRow').innerHTML = skKeys.map((k) => {
    const attr = k.sticky ? `data-sticky="${esc(k.sticky)}"`
      : k.act ? `data-act="${esc(k.act)}"`
        : `data-send="${esc(k.send)}"`;
    return `<button class="sk${k.wide ? ' wide' : ''}" ${attr} title="${esc(k.spec || k.sticky || k.act || '')}">${esc(k.label)}</button>`;
  }).join('');
  renderSticky();
  syncKbdBtn();
}

async function loadSoftkeys() {
  try {
    const r = await api.get('/softkeys');
    skKeys = r.keys || [];
    skPresets = r.presets || [];
  } catch {
    skKeys = [];                 // 拿不到就先空着，面板里还能改
  }
  renderSoftkeys();
}

// 编辑器：改的是一份草稿，保存成功才生效
let skDraft = [];
const skKind = (k) => (k.sticky ? `sticky:${k.sticky}` : k.act ? `act:${k.act}` : (k.spec ?? k.send ?? ''));

// 「常用」下拉：选一条就把这一行的名字和按键谱填好，不用记按键谱怎么写。
// 列表由服务端下发（照 herdr 的默认 keybinding 抄的），生僻的仍然手输。
let skPresets = [];
let skFlat = [];                 // 拍平之后按下标取

// option 的 value 就是拍平后的下标。别用「组名 + 分隔符 + 序号」拼字符串 ——
// 分隔符本身就是个坑（踩过：本该是空格的那个字节写成了 NUL，整个文件都变成 binary）。
function skPresetOptions() {
  skFlat = [];
  return skPresets.map((g) => {
    const opts = g.items.map((it) => {
      skFlat.push(it);
      return `<option value="${skFlat.length - 1}">${esc(it.label)}${it.send ? ` \u2014 ${esc(it.send)}` : ''}</option>`;
    }).join('');
    return `<optgroup label="${esc(g.group)}">${opts}</optgroup>`;
  }).join('');
}

function renderSkEditor() {
  const opts = skPresetOptions();
  $('#skList').innerHTML = skDraft.map((k, i) => `
    <div class="item sk-edit-row" data-i="${i}">
      <input class="sk-label" value="${esc(k.label)}" placeholder="名字" maxlength="12">
      <input class="sk-spec mono" value="${esc(skKind(k))}" placeholder="ctrl+b c">
      <select class="sk-preset" title="从常用里挑一个填进这一行"><option value="">常用…</option>${opts}</select>
      <label class="sk-wide" title="占宽一点"><input type="checkbox" ${k.wide ? 'checked' : ''}>宽</label>
      <div class="item-act">
        <button class="btn tiny" data-mv="-1" title="上移（往左）">↑</button>
        <button class="btn tiny" data-mv="1" title="下移（往右）">↓</button>
        <button class="btn tiny danger" data-del="1" title="删掉">×</button>
      </div>
    </div>`).join('') || '<p class="empty">一个按键都没有，点「+ 加一个」</p>';
}

// 从输入框里收回草稿（每次改动前先同步，免得丢掉正在编辑的内容）
function skCollect() {
  for (const row of $$('#skList .sk-edit-row')) {
    const i = Number(row.dataset.i);
    const spec = row.querySelector('.sk-spec').value.trim();
    const k = { label: row.querySelector('.sk-label').value, wide: row.querySelector('.sk-wide input').checked };
    const m = spec.match(/^(sticky|act):(.+)$/);
    if (m) k[m[1]] = m[2].trim(); else k.send = spec;
    skDraft[i] = k;
  }
}

function openSkPanel() {
  $('#caps').classList.add('hidden');
  $('#hosts').classList.add('hidden');
  const panel = $('#skeys');
  panel.classList.toggle('hidden');
  if (panel.classList.contains('hidden')) return;
  skDraft = skKeys.map((k) => ({ label: k.label, wide: !!k.wide, ...(k.sticky ? { sticky: k.sticky } : k.act ? { act: k.act } : { send: k.spec ?? k.send }) }));
  $('#skErr').textContent = '';
  renderSkEditor();
}

$('#skEdit').onclick = openSkPanel;
$('#skAdd').onclick = () => { skCollect(); skDraft.push({ label: '', send: '' }); renderSkEditor(); };
// 选了「常用」里的一条 -> 填进这一行（名字、按键谱、宽窄一起带过来）
$('#skList').addEventListener('change', (e) => {
  const sel = e.target.closest('.sk-preset');
  if (!sel || sel.value === '') return;
  const row = sel.closest('.sk-edit-row');
  const it = skFlat[Number(sel.value)];
  if (!row || !it) return;
  skCollect();
  skDraft[Number(row.dataset.i)] = { ...it };
  renderSkEditor();
});
$('#skList').addEventListener('click', (e) => {
  const row = e.target.closest('.sk-edit-row');
  const btn = e.target.closest('button');
  if (!row || !btn) return;
  skCollect();
  const i = Number(row.dataset.i);
  if (btn.dataset.del) skDraft.splice(i, 1);
  else {
    const j = i + Number(btn.dataset.mv);
    if (j < 0 || j >= skDraft.length) return;
    [skDraft[i], skDraft[j]] = [skDraft[j], skDraft[i]];
  }
  renderSkEditor();
});
$('#skSave').onclick = async () => {
  skCollect();
  $('#skErr').textContent = '';
  try {
    skKeys = (await api.put('/softkeys', { keys: skDraft })).keys;
    renderSoftkeys();
    toast('软键条已保存');
  } catch (e) {
    $('#skErr').textContent = e.message;      // 服务端会指出是第几个按键、哪里不认
  }
};
$('#skReset').onclick = async () => {
  try {
    skKeys = (await api.del('/softkeys')).keys;
    renderSoftkeys();
    skDraft = skKeys.map((k) => ({ label: k.label, wide: !!k.wide, ...(k.sticky ? { sticky: k.sticky } : k.act ? { act: k.act } : { send: k.spec }) }));
    renderSkEditor();
    toast('已恢复默认');
  } catch (e) { $('#skErr').textContent = e.message; }
};

$('#softkeys').addEventListener('click', (e) => {
  if (e.target.closest('#skEdit')) return;      // ⚙ 不是按键，它开配置面板
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
  sentSize = `${term.cols}×${term.rows}`;   // PTY 就是按这个尺寸开的，别再重复发一次
  const q = new URLSearchParams({ token: TOKEN, cols: term.cols, rows: term.rows, ...spec });
  ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/pty?${q}`);
  ws.binaryType = 'arraybuffer';
  setStatus('连接中…', '');
  $('#overlay').classList.add('hidden');

  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') { term.write(new Uint8Array(ev.data)); armRepaint(); return; }
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
// ResizeObserver / visualViewport 会为了跟行列数无关的布局变化（软键条、发件箱开合、
// 手机上滚一下地址栏）反复触发。尺寸没变还发 resize，等于白给 herdr 一个 SIGWINCH，
// 它会把整屏重画一遍 —— 纯粹的重绘噪音，所以这里比一下再发。
let resizeTimer = null;
let sentSize = '';
function relayout() {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    try { fit.fit(); } catch { /* 容器还没测量出来 */ }
    const size = `${term.cols}×${term.rows}`;
    if (ws?.readyState === WebSocket.OPEN && size !== sentSize) {
      sentSize = size;
      ws.send(JSON.stringify({ t: 'r', cols: term.cols, rows: term.rows }));
      if (alive) setStatus($('#label').textContent.replace(/\d+×\d+$/, size), 'on');
    }
  }, 80);
}
new ResizeObserver(relayout).observe($('#wrap'));
addEventListener('orientationchange', relayout);
/**
 * 手机弹出虚拟键盘时把页面高度定到 visualViewport 上。
 *
 * 光调 fit.fit() 不够：iOS 的键盘**从不**缩布局视口，Android 也要 viewport meta 里
 * 的 interactive-widget 才缩。html/body 写 height:100% 的话，100% 指的是没缩过的
 * 布局视口，于是键盘直接盖住底下的软键条和发件箱 —— 终端重排了也看不见。
 */
function applyViewportHeight() {
  const vv = visualViewport;
  if (!vv) return;
  document.documentElement.style.setProperty('--vvh', `${Math.round(vv.height)}px`);
  // 键盘弹出时浏览器有时会把整页顶上去，拉回来，否则顶栏跑出屏幕
  if (vv.offsetTop || scrollY) scrollTo(0, 0);
}
visualViewport?.addEventListener('resize', () => { applyViewportHeight(); relayout(); });
visualViewport?.addEventListener('scroll', applyViewportHeight);
applyViewportHeight();

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
  cApplyCfg(state.compose);
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

/* ------------------------------------------------------------------ 语音投稿发件箱 */
// 为什么是这么一个框而不是直接对着终端说：终端是字节流，没有 selection 语义，
// IME 只能往里灌字符。「框选重说」需要一个真正的可编辑字段 —— 有文本模型、有选区，
// 选中后 IME 提交会覆盖选区。xterm.js 的隐藏 textarea 不算，它只转发按键。
//
// 发件箱，不是镜像：不做双向同步（两个缓冲区一个字节流，同步永远追不上），
// 每次整段覆盖，发完清空本地框。远端的 Tab 补全 / 上下键历史属于「直接操作终端」
// 那条通道，要历史就在发件箱这侧留 —— 就是下面的 hist。
const FOLLOW = '__focused';          // 「跟着 herdr 当前激活的 pane 走」

/**
 * 发件箱的节奏。两个都是轮询/防抖的毫秒数：
 *   poll —— 「现在焦点在哪个 pane + 那个输入框里是什么」多久对一次
 *   push —— 开着「双向」时，停手多久把草稿写进远端输入框
 *
 * 默认值来自服务端（`HERDR_WEB_POLL_MS` / `HERDR_WEB_PUSH_MS`），URL 上加
 * `?poll=600&push=400` 可以当场覆盖，方便在平板上试手感。
 */
const cCfg = { poll: 500, push: 700 };
for (const k of ['poll', 'push']) {
  const v = Number(new URLSearchParams(location.search).get(k));
  if (v > 0) cCfg[k] = Math.max(100, v);
}
const cCfgFromUrl = new URLSearchParams(location.search);

// 服务端下发的默认值；URL 上显式写了的不覆盖
function cApplyCfg(c) {
  if (!c) return;
  for (const [k, v] of [['poll', c.pollMs], ['push', c.pushMs]]) {
    if (!cCfgFromUrl.has(k) && v > 0) cCfg[k] = Math.max(100, v);
  }
}
let cPanes = [];
let cHist = [];
let cHistIdx = -1;
let cResolved = '';                  // 上一次轮询解析出来的真实 pane
let cSynced = null;                  // 远端最后一次读到的文本，只用来发现「远端变了」
let cOwn = false;                    // 框里装的是**用户自己写的**东西
let cPinned = '';                    // 草稿归属的 pane
let cPushTimer = null;
let cInFlight = false;               // 有请求在飞时暂停轮询，免得自己追自己
try { cHist = JSON.parse(localStorage.getItem('composeHist') || '[]'); } catch { /* 存坏了就算了 */ }

const cVal = () => $('#cText').value;

// 「这是我自己写的」和「远端现在是什么」必须分成两个变量。
//
// 一开始只用「文本 !== 上次对齐的文本」判断草稿，结果开着「双向」时：草稿被推到
// 远端之后，对齐文本就等于草稿本身，于是草稿看起来「没改过」→ 解锁目标 → 下一拍
// 把用户正在写的东西直接覆盖掉。cSynced 那时候同时在干两件事（发现远端变化、
// 保护本地草稿），冲突不可避免。现在 cOwn 单独负责所有权。

/**
 * 这段草稿到底该投给谁。
 *
 * 「跟随焦点」不能一路跟到按下按钮那一刻：你为 A pane 写了一段话，中途焦点漂到了
 * B（herdr 自己会因为 agent 状态变化换焦点，人也可能顺手点一下），投出去就落到 B 了。
 * 所以**自己改过的草稿**会把目标锁定在当初瞄准的那个 pane，改动投出去之后才重新跟随。
 *
 * 判据是 cOwn 而不是「框里有没有字」：**自动拉回来还没动过**的内容不算草稿，
 * 那时候切 pane 就该跟着换成新 pane 的内容。用「有没有字」当判据的话，只要框里
 * 有东西（哪怕是刚拉回来的）目标就被钉死，切 pane 后 input 再也不更新。
 */
function cAimed() {
  const sel = $('#cTarget').value;
  if (sel !== FOLLOW) return sel;
  return cPinned && cOwn ? cPinned : FOLLOW;
}

function cSetInfo(msg, bad) {
  const el = $('#cInfo');
  el.textContent = msg || '';
  el.classList.toggle('bad', !!bad);
  el.title = `轮询 ${cCfg.poll}ms · 双向防抖 ${cCfg.push}ms（URL 加 ?poll=&push= 可临时改）`;
}

// 把服务端给的 pane 身份写成一行；workspace / tab 的好看标签用列表缓存补
function cLabel(r) {
  const cached = cPanes.find((p) => p.id === r.target);
  const where = cached ? `${cached.workspace}/${cached.tab}` : (r.workspaceId || '');
  return `${r.followed ? '⟳ ' : ''}${r.target}${where ? ` · ${where}` : ''}`
    + ` · ${r.agent ? `${r.agent} ${r.status}` : 'shell'}`;
}

function cRenderTargets() {
  const sel = $('#cTarget');
  const prev = sel.value || localStorage.getItem('composeTarget') || FOLLOW;
  const opt = (p) => `<option value="${esc(p.id)}">${esc(
    `${p.agent ? `${p.agent} · ` : ''}${p.workspace}/${p.tab} · ${p.id}${p.title ? ` · ${p.title}` : ''}`,
  )}</option>`;
  const agents = cPanes.filter((p) => p.agent);
  const shells = cPanes.filter((p) => !p.agent);
  sel.innerHTML =
    `<option value="${FOLLOW}">跟随 herdr 当前 pane</option>`
    + (agents.length ? `<optgroup label="Agent pane">${agents.map(opt).join('')}</optgroup>` : '')
    + (shells.length ? `<optgroup label="Shell pane">${shells.map(opt).join('')}</optgroup>` : '');
  sel.value = prev === FOLLOW || cPanes.some((p) => p.id === prev) ? prev : FOLLOW;
}

async function cLoadTargets(quiet) {
  try {
    cPanes = (await api.get('/herdr/panes')).panes || [];
    cRenderTargets();
  } catch (e) {
    cPanes = [];
    cRenderTargets();
    // socket 在跑 herdr server 的那台机器上，不一定是跑 herdr-web 的这台
    cSetInfo(`连不上 herdr：${e.message}`, true);
    if (!quiet) toast('连不上 herdr：' + e.message, 3200);
  }
}

const cBusy = (on) => $('#compose').classList.toggle('busy', on);

/** 把远端内容放进框里：这是远端的东西，不是用户的草稿（cOwn = false）。 */
function cAdopt(text, pane) {
  const ta = $('#cText');
  ta.value = text || '';
  cSynced = ta.value;
  cOwn = false;
  cPinned = pane || cPinned;
  if (document.activeElement === ta) ta.setSelectionRange(ta.value.length, ta.value.length);
}

async function cPullBack() {
  cBusy(true);
  try {
    const r = await api.get(`/herdr/pull?target=${encodeURIComponent(cAimed())}`);
    cResolved = r.target;
    cAdopt(r.text, r.target);
    // 手动点「拉回」是明确的意图：拿过来编辑。所以算用户的东西，锁定在这个 pane，
    // 别让下一次焦点变化把它冲掉。
    cOwn = !!r.text;
    $('#cText').focus();
    cSetInfo(`${cLabel(r)} · ${r.text ? `已拉回 ${[...r.text].length} 字` : '输入框是空的'}`);
  } catch (e) {
    cSetInfo('拉回失败：' + e.message, true);
  } finally {
    cBusy(false);
  }
}

/**
 * 自动拉回的一拍：谁是当前 pane、它输入框里是什么。
 *   切了 pane  → 本地干净就直接换成新 pane 的内容；脏就留着草稿只提示
 *   同一个 pane → 远端变了且本地干净才跟着变
 */
async function cTick() {
  if (cInFlight || $('#compose').classList.contains('hidden')) return;
  const aimed = cAimed();
  let r;
  try {
    r = await api.get(`/herdr/sync?target=${encodeURIComponent(aimed)}`);
  } catch (e) {
    cSetInfo('herdr：' + e.message, true);
    return;
  }
  const switched = r.target !== cResolved;
  cResolved = r.target;
  const pinNote = aimed === FOLLOW ? '' : ' · 草稿已锁定这个 pane';

  if (switched) {
    // 焦点换了 pane：框里是远端来的就直接换成新 pane 的内容，是自己写的就留着
    if (cOwn) cSetInfo(`${cLabel(r)} · 本地有草稿，没自动拉回（点「拉回」覆盖）`);
    else { cAdopt(r.text, r.target); cSetInfo(cLabel(r)); }
    return;
  }
  if (!cOwn && (r.text || '') !== cSynced) {
    cAdopt(r.text, r.target);
    cSetInfo(`${cLabel(r)} · 已跟随远端改动`);
    return;
  }
  cSetInfo(`${cLabel(r)}${cOwn ? ' · 本地草稿未投' : ''}${pinNote}`);
}

/** 双向同步的本地→远端那半边：停手 700ms 后把草稿写进远端输入框（不回车）。 */
function cSchedulePush() {
  // 只推**用户自己写的**东西。自动拉回来的内容远端本来就有，推回去纯属多余，
  // 而且中间只要焦点动一下，就会把 A 的内容写进 B 的输入框。
  if (!$('#cLive').checked || !cOwn) return;
  clearTimeout(cPushTimer);
  cPushTimer = setTimeout(async () => {
    const text = cVal();
    cInFlight = true;
    try {
      const r = await api.post('/herdr/draft', { target: cAimed(), text });
      if (r.skipped === 'not-agent') cSetInfo(`${cLabel(r)} · 这个 pane 没有 agent 输入框，没往里推`);
      else if (r.skipped === 'busy') cSetInfo(`${cLabel(r)} · 远端正忙，这次没推`);
      else { cSynced = text; cPinned = r.target; cSetInfo(`${cLabel(r)} · 已同步 ${r.pushed} 字到远端`); }
    } catch (e) {
      cSetInfo('同步失败：' + e.message, true);
    } finally {
      cInFlight = false;
    }
  }, cCfg.push);
}

/* ---- 传图片：落盘到 herdr 那台机器，把绝对路径插进提示词 ---- */
// agent 读不了「剪贴板里的图」，但都能读磁盘上的图片文件（claude / codex 实测都行）。
// 所以图片这条路和文本是同一条：最后都变成投出去的那段文字。

// 手机照片动辄 4000px / 几 MB。能解码就先缩到长边 2400 再传，顺便把 HEIC 这种
// agent 读不了的格式统一成 PNG / JPEG。解不了就原样传，让服务端按魔数去认。
async function cNormalize(file) {
  const MAX_EDGE = 2400;
  const png = /png/i.test(file.type);
  try {
    const bmp = await createImageBitmap(file);
    const scale = Math.min(1, MAX_EDGE / Math.max(bmp.width, bmp.height));
    if (scale === 1 && (png || /jpe?g/i.test(file.type))) { bmp.close?.(); return file; }
    const w = Math.round(bmp.width * scale);
    const h = Math.round(bmp.height * scale);
    const cv = document.createElement('canvas');
    cv.width = w; cv.height = h;
    cv.getContext('2d').drawImage(bmp, 0, 0, w, h);
    bmp.close?.();
    return await new Promise((r) => cv.toBlob(r, png ? 'image/png' : 'image/jpeg', 0.92)) || file;
  } catch {
    return file;
  }
}

function cInsert(text) {
  const ta = $('#cText');
  const at = ta.selectionStart ?? ta.value.length;
  const before = ta.value.slice(0, at);
  const chunk = (before && !/\s$/.test(before) ? ' ' : '') + text + ' ';
  ta.value = before + chunk + ta.value.slice(at);
  const pos = at + chunk.length;
  ta.setSelectionRange(pos, pos);
  ta.dispatchEvent(new Event('input'));    // 走一遍「钉住目标 + 双向推送」
}

async function cAttach(files) {
  const imgs = [...files].filter((f) => f.type.startsWith('image/') || /\.(png|jpe?g|gif|webp|heic)$/i.test(f.name || ''));
  if (!imgs.length) return;
  cBusy(true);
  try {
    for (let i = 0; i < imgs.length; i++) {
      cSetInfo(`上传第 ${i + 1}/${imgs.length} 张…`);
      const blob = await cNormalize(imgs[i]);
      const r = await fetch(`/api/herdr/upload?token=${encodeURIComponent(TOKEN)}`, {
        method: 'POST',
        headers: { 'content-type': blob.type || 'application/octet-stream' },
        body: blob,
      });
      const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }));
      if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
      cInsert(j.path);
      cSetInfo(`已插入 ${j.name}（${(j.bytes / 1024).toFixed(0)} KB）· 路径已在框里，agent 会去读这个文件`);
    }
    $('#cText').focus();
  } catch (e) {
    cSetInfo('传图失败：' + e.message, true);
    toast('传图失败：' + e.message, 3600);
  } finally {
    cBusy(false);
  }
}

async function cSay() {
  const text = cVal();
  if (!text.trim()) return toast('框里是空的');
  clearTimeout(cPushTimer);
  cBusy(true);
  cInFlight = true;
  cSetInfo('投递中…');
  try {
    const r = await api.post('/herdr/say', { target: cAimed(), text });
    cHist = [text, ...cHist.filter((x) => x !== text)].slice(0, 30);
    cHistIdx = -1;
    localStorage.setItem('composeHist', JSON.stringify(cHist));
    $('#cText').value = '';                       // 发完就清空，不做增量同步
    cSynced = '';
    cOwn = false;
    cPinned = '';                                 // 框空了，重新跟随焦点
    cResolved = r.target;
    cSetInfo(`已投给 ${r.target}[${r.agent || 'shell'}] · ${r.chars} 字`);
  } catch (e) {
    cSetInfo('投稿失败：' + e.message, true);
    toast('投稿失败：' + e.message, 3600);
  } finally {
    cInFlight = false;
    cBusy(false);
  }
}

function cRecall(dir) {
  if (!cHist.length) return;
  cHistIdx = Math.max(-1, Math.min(cHist.length - 1, cHistIdx + dir));
  const ta = $('#cText');
  ta.value = cHistIdx < 0 ? '' : cHist[cHistIdx];
  ta.setSelectionRange(ta.value.length, ta.value.length);
}

$('#cTarget').onchange = () => {
  localStorage.setItem('composeTarget', $('#cTarget').value);
  cResolved = '';        // 逼下一拍当成「切了 pane」处理
  cTick();
};
$('#cReload').onclick = () => cLoadTargets();
$('#cPull').onclick = () => cPullBack();
$('#cSend').onclick = () => cSay();
$('#cLive').onchange = (e) => {
  localStorage.setItem('composeLive', e.target.checked ? '1' : '0');
  if (e.target.checked) cSchedulePush();
};
$('#cText').addEventListener('input', () => {
  cOwn = !!cVal();                                  // 框空了就把控制权交回「跟随焦点」
  if (cOwn && !cPinned && cResolved) cPinned = cResolved;   // 一开始打字就把目标钉住
  cSchedulePush();
});

// 传图三条路：点按钮（手机上会给相机 / 相册）、粘贴（电脑上截图完 ⌘V）、拖进来
$('#cAttachBtn').onclick = () => $('#cFile').click();
$('#cFile').onchange = (e) => { cAttach(e.target.files); e.target.value = ''; };
$('#cText').addEventListener('paste', (e) => {
  const files = [...(e.clipboardData?.files || [])];
  if (files.length) { e.preventDefault(); cAttach(files); }
});
for (const ev of ['dragover', 'drop']) {
  $('#compose').addEventListener(ev, (e) => {
    if (![...(e.dataTransfer?.types || [])].includes('Files')) return;
    e.preventDefault();
    $('#compose').classList.toggle('drop', ev === 'dragover');
    if (ev === 'drop') cAttach(e.dataTransfer.files);
  });
}
$('#compose').addEventListener('dragleave', () => $('#compose').classList.remove('drop'));
$('#cText').addEventListener('keydown', (e) => {
  // Enter 必须留给换行（语音口述常是多行），提交走 ⌘↵ / Ctrl↵
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); cSay(); return; }
  if (e.key === 'ArrowUp' && !cVal()) { e.preventDefault(); cRecall(1); return; }
  if (e.key === 'ArrowDown' && cHistIdx >= 0) { e.preventDefault(); cRecall(-1); }
});

// 自动拉回的心跳。只在发件箱开着的时候干活（cTick 自己会判）。
// 用自排队的 setTimeout 而不是 setInterval：一拍要打 3 次 socket（pane.current +
// 两次 pane.read），间隔调小或者网络一慢，setInterval 会把请求叠起来。
(function cLoop() {
  setTimeout(() => { Promise.resolve(cTick()).finally(cLoop); }, cCfg.poll);
})();
// 从别处切回这个页面 / 这个标签页时立刻对一次，别等下一拍
addEventListener('focus', cTick);
document.addEventListener('visibilitychange', () => { if (!document.hidden) cTick(); });

// focus 只在用户主动点开时给：启动时不抢焦点（和「连上不自动抢焦点」一致）
function toggleCompose(show, focus) {
  $('#compose').classList.toggle('hidden', !show);
  $('#composeBtn').classList.toggle('on', show);
  localStorage.setItem('compose', show ? '1' : '0');
  relayout();
  if (!show) return;
  if (!cPanes.length) cLoadTargets(true).then(cTick);
  else cTick();
  if (focus) $('#cText').focus();
}
$('#composeBtn').onclick = () => toggleCompose($('#compose').classList.contains('hidden'), true);

/* ------------------------------------------------------------------ 顶栏交互 */
// 点顶栏 / 面板不能把键盘焦点从终端抢走，否则点完「敲 herdr」还得再点一下终端才能打字。
// 发件箱同理：点「投稿」不该把焦点从 textarea 抢走（textarea / select 本身在下一行放行）。
document.addEventListener('mousedown', (e) => {
  if (!e.target.closest('.bar, .panel, .softkeys, .compose')) return;
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
$('#opt2026').onchange = (e) => {
  localStorage.setItem('sync2026', e.target.checked ? '1' : '0');
  // 关掉的时候把当前可能正开着的那一帧收尾，否则要等下一个 BSU 才生效
  if (!e.target.checked && term.modes.synchronizedOutputMode) term.write('\x1b[?2026l');
};

/* ------------------------------------------------------------------ 启动 */
document.documentElement.classList.toggle('light', scheme === 'light');
for (const ev of ['focus', 'blur']) kbdEl()?.addEventListener(ev, syncKbdBtn);
renderCaps();
renderSticky();
loadSoftkeys();          // 按键从服务端来
setMode('local');
toggleSoftkeys(localStorage.getItem('softkeys') === '1'
  || (localStorage.getItem('softkeys') === null && matchMedia('(pointer: coarse)').matches));
// 发件箱默认开着：这是语音投稿的主入口，也方便在电脑上直接调试
$('#opt2026').checked = localStorage.getItem('sync2026') !== '0';
$('#cLive').checked = localStorage.getItem('composeLive') === '1';
toggleCompose(localStorage.getItem('compose') !== '0');
fit.fit();

if (!TOKEN) {
  showOverlay('URL 里缺 token。复制 <code>node server.js</code> 打印的那个链接（手机可以直接扫终端里的二维码）。', '仍然试试');
} else {
  refresh().catch((e) => toast('读取主机列表失败：' + e.message));
}
