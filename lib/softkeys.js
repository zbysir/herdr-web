'use strict';
/**
 * 软键条的配置：存在 `~/.herdr-web/softkeys.json`，在网页上编辑。
 *
 * 存服务端而不是 localStorage，是为了手机 / 平板 / 电脑共用一份 —— 和 token 落盘
 * 同一个道理，改一次到处生效。
 *
 * 每个按键三种形态之一：
 *   { send: "<按键谱>" }  按一下发一串字节
 *   { sticky: "ctrl" }    粘滞修饰键（点亮之后下一个字母组合成 ctrl+x）
 *   { act: "kbd" }        显示 / 收起系统键盘
 *
 * 「按键谱」是空格分隔的记号，服务端解析成字节后下发给前端，前端只管照发。
 * 支持多个 token 连发，所以一个按键可以是一串操作 —— 比如 `ctrl+b c` 就是
 * herdr 的「前缀 + c」，一下点出来。
 */
const fs = require('node:fs');
const path = require('node:path');
const store = require('./store');

const FILE = path.join(store.DIR, 'softkeys.json');

// 具名键 → 字节。方向键用 CSI 形式（和原来写死在 HTML 里的一致）
const NAMED = {
  esc: '\x1b', tab: '\t', enter: '\r', cr: '\r', lf: '\n', space: ' ',
  bs: '\x7f', backspace: '\x7f', del: '\x1b[3~', delete: '\x1b[3~', ins: '\x1b[2~',
  up: '\x1b[A', down: '\x1b[B', right: '\x1b[C', left: '\x1b[D',
  home: '\x1b[H', end: '\x1b[F', pgup: '\x1b[5~', pgdn: '\x1b[6~',
  f1: '\x1bOP', f2: '\x1bOQ', f3: '\x1bOR', f4: '\x1bOS',
  f5: '\x1b[15~', f6: '\x1b[17~', f7: '\x1b[18~', f8: '\x1b[19~',
  f9: '\x1b[20~', f10: '\x1b[21~', f11: '\x1b[23~', f12: '\x1b[24~',
};

/** ctrl+<单字符> → 控制码。按终端惯例，不是简单减 96。 */
function ctrlOf(ch) {
  const c = ch.toLowerCase();
  if (c >= 'a' && c <= 'z') return String.fromCharCode(c.charCodeAt(0) - 96);
  if (c === ' ' || c === '@') return '\x00';
  if ('[\\]^_'.includes(c)) return String.fromCharCode(c.charCodeAt(0) - 64);
  if (c === '?') return '\x7f';
  throw new Error(`ctrl+ 不支持 ${ch}`);
}

function token(t) {
  const low = t.toLowerCase();
  if (low.startsWith('ctrl+') || low.startsWith('^')) {
    const rest = low.startsWith('^') ? t.slice(1) : t.slice(5);
    // 允许 ctrl+space 这种写法：具名键先解出来，只要落到单个字符就能再套 ctrl
    const base = rest.length === 1 ? rest : NAMED[rest.toLowerCase()];
    if (!base || base.length !== 1) throw new Error(`ctrl+ 后面只能跟一个字符（或 space）：${t}`);
    return ctrlOf(base);
  }
  if (low.startsWith('alt+')) return '\x1b' + token(t.slice(4));
  if (low === 'shift+tab') return '\x1b[Z';
  if (NAMED[low] !== undefined) return NAMED[low];
  if (t.length === 1) return t;                 // 单个字面字符
  throw new Error(`不认识的按键：${t}`);
}

/**
 * 解析按键谱。双引号里的内容原样发送（可以带空格）。
 * 例：`ctrl+b c` `esc` `"herdr" enter` `up` `alt+1`
 */
function parseSpec(spec) {
  const s = String(spec ?? '').trim();
  if (!s) throw new Error('按键谱是空的');
  if (s.length > 200) throw new Error('按键谱太长');

  let out = '';
  // 先切出带引号的字面串，其余按空白切
  for (const m of s.match(/"[^"]*"|\S+/g) || []) {
    out += m.startsWith('"') ? m.slice(1, -1) : token(m);
  }
  if (!out) throw new Error('按键谱解析出来是空的');
  return out;
}

// 出厂配置 —— 就是原来写死在 index.html 里的那一排
const DEFAULTS = [
  { act: 'kbd', label: '⌨' },
  { send: 'ctrl+b', label: '⌃B 前缀', wide: true },
  { sticky: 'ctrl', label: 'Ctrl' },
  { sticky: 'alt', label: 'Alt' },
  { send: 'esc', label: 'Esc' },
  { send: 'tab', label: 'Tab' },
  { send: 'up', label: '↑' },
  { send: 'down', label: '↓' },
  { send: 'left', label: '←' },
  { send: 'right', label: '→' },
  { send: 'pgup', label: 'PgUp' },
  { send: 'pgdn', label: 'PgDn' },
  { send: 'ctrl+c', label: '⌃C' },
  { send: 'enter', label: '↵' },
];

/**
 * 编辑器里的「常用」下拉。按键谱抄的是 `herdr --default-config` 里的 [keys] 默认值，
 * 所以改过 herdr keybinding 的人得自己手输 —— 下拉只是省事，不是全部。
 *
 * 注意 `prefix+shift+x` 这类要写成 `ctrl+b X`（大写字母就是 shift），
 * `prefix+minus` 写成 `ctrl+b -`。
 */
const PRESETS = [
  {
    group: '前缀 / 通用',
    items: [
      { label: '⌃B 前缀', send: 'ctrl+b', wide: true },
      { label: '帮助', send: 'ctrl+b ?' },
      { label: '设置', send: 'ctrl+b s' },
      { label: '侧边栏', send: 'ctrl+b b' },
      { label: '跳转', send: 'ctrl+b g' },
      { label: '断开', send: 'ctrl+b q' },
    ],
  },
  {
    group: '标签',
    items: [
      { label: '新标签', send: 'ctrl+b c' },
      { label: '下个标签', send: 'ctrl+b n' },
      { label: '上个标签', send: 'ctrl+b p' },
      { label: '关标签', send: 'ctrl+b X' },
      { label: '标签 1', send: 'ctrl+b 1' },
      { label: '标签 2', send: 'ctrl+b 2' },
      { label: '标签 3', send: 'ctrl+b 3' },
    ],
  },
  {
    group: 'Pane',
    items: [
      { label: '竖分屏', send: 'ctrl+b v' },
      { label: '横分屏', send: 'ctrl+b -' },
      { label: '关 pane', send: 'ctrl+b x' },
      { label: '放大', send: 'ctrl+b z' },
      { label: '下个 pane', send: 'ctrl+b tab' },
      { label: 'pane ←', send: 'ctrl+b h' },
      { label: 'pane ↓', send: 'ctrl+b j' },
      { label: 'pane ↑', send: 'ctrl+b k' },
      { label: 'pane →', send: 'ctrl+b l' },
      { label: '调大小', send: 'ctrl+b r' },
      { label: '改名', send: 'ctrl+b P' },
    ],
  },
  {
    group: '工作区',
    items: [
      { label: '工作区', send: 'ctrl+b w' },
      { label: '新工作区', send: 'ctrl+b N' },
      { label: '改名', send: 'ctrl+b W' },
      { label: '关工作区', send: 'ctrl+b D' },
    ],
  },
  {
    group: '终端按键',
    items: [
      { label: 'Esc', send: 'esc' },
      { label: 'Tab', send: 'tab' },
      { label: '↵', send: 'enter' },
      { label: '↑', send: 'up' },
      { label: '↓', send: 'down' },
      { label: '←', send: 'left' },
      { label: '→', send: 'right' },
      { label: 'PgUp', send: 'pgup' },
      { label: 'PgDn', send: 'pgdn' },
      { label: 'Home', send: 'home' },
      { label: 'End', send: 'end' },
      { label: '⌃C', send: 'ctrl+c' },
      { label: '⌃D', send: 'ctrl+d' },
      { label: '⌃L 清屏', send: 'ctrl+l' },
      { label: '⌃U 清行', send: 'ctrl+u' },
      { label: '⌃R 搜索', send: 'ctrl+r' },
      { label: '⌃Z', send: 'ctrl+z' },
      { label: 'Shift+Tab', send: 'shift+tab' },
    ],
  },
  {
    group: '特殊 / 文本',
    items: [
      { label: '⌨ 键盘', act: 'kbd' },
      { label: 'Ctrl', sticky: 'ctrl' },
      { label: 'Alt', sticky: 'alt' },
      { label: '敲 herdr', send: '"herdr" enter', wide: true },
      { label: 'git status', send: '"git status" enter', wide: true },
    ],
  },
];

const MAX_KEYS = 40;

/** 校验并规整一条。抛错的话消息是给用户看的。 */
function normalize(k, i) {
  const at = `第 ${i + 1} 个按键`;
  if (!k || typeof k !== 'object') throw new Error(`${at} 不是对象`);

  const label = String(k.label ?? '').trim();
  if (label.length > 12) throw new Error(`${at} 的名字太长（最多 12 个字符）`);

  const kinds = ['send', 'sticky', 'act'].filter((x) => k[x]);
  if (kinds.length !== 1) throw new Error(`${at} 必须正好是 send / sticky / act 中的一种`);

  const out = { label, wide: !!k.wide };
  if (k.sticky) {
    if (!['ctrl', 'alt'].includes(k.sticky)) throw new Error(`${at} 的 sticky 只能是 ctrl 或 alt`);
    out.sticky = k.sticky;
  } else if (k.act) {
    if (k.act !== 'kbd') throw new Error(`${at} 的 act 目前只支持 kbd`);
    out.act = 'kbd';
  } else {
    out.spec = String(k.send).trim();
    out.send = parseSpec(out.spec);              // 解析失败就抛，前端直接显示
  }
  if (!out.label) out.label = out.spec || out.sticky || out.act;
  return out;
}

function load() {
  let raw;
  try { raw = JSON.parse(fs.readFileSync(FILE, 'utf8')); } catch { return resolve(DEFAULTS); }
  if (!Array.isArray(raw?.keys)) return resolve(DEFAULTS);
  try { return resolve(raw.keys); } catch { return resolve(DEFAULTS); }   // 存坏了就退回默认
}

function save(keys) {
  if (!Array.isArray(keys)) throw new Error('keys 必须是数组');
  if (keys.length > MAX_KEYS) throw new Error(`最多 ${MAX_KEYS} 个按键`);
  const out = resolve(keys);                     // 先全部校验通过再落盘
  fs.mkdirSync(store.DIR, { recursive: true, mode: 0o700 });
  fs.writeFileSync(FILE, JSON.stringify({ keys: keys.map(strip) }, null, 2), { mode: 0o600 });
  return out;
}

// 落盘只存用户写的东西，不存解析结果（下次读的时候重新解析）
const strip = (k) => {
  const o = { label: String(k.label ?? '').trim() };
  if (k.wide) o.wide = true;
  if (k.sticky) o.sticky = k.sticky;
  else if (k.act) o.act = k.act;
  else o.send = String(k.send ?? k.spec ?? '').trim();
  return o;
};

const resolve = (keys) => keys.map(normalize);

module.exports = { load, save, parseSpec, DEFAULTS, PRESETS, resolve, FILE, MAX_KEYS };
