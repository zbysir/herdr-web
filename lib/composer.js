'use strict';
/**
 * 从 pane 的 ANSI 快照里抽出输入框内容。（reference/herdr_composer.py 的移植）
 *
 * 按 agent 类型分派，与 herdr 自己的 agent-detection manifest 保持一致：
 *   claude → prompt_box_body / after_last_horizontal_rule（输入框是上下横线夹的 box）
 *   codex  → after_last_prompt_marker（提示符之后；这里额外用背景色块收边，
 *            因为我们要的是精确内容而不是一个供 regex 求值的区域）
 *   其他   → 通用嗅探（manifest 里另外 17 个 agent 也没有输入区规则，
 *            whole_recent / osc_title / bottom_non_empty_lines 而已，无从照抄）
 *
 * dim（SGR 2）的文字算占位提示，不算内容 —— Codex 空框的
 * "Run /review on my current changes" 就是这么渲染的；实测两家的真实输入都不带 dim。
 */

// OSC（\x1b] ... BEL/ST）、CSI、字符集切换。`.` 不跨行，和 Python 版一致 ——
// 反正调用方已经按行切开了。
const ESC = /\x1b\].*?(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Za-z0-9]/g;
const SGR = /\x1b\[([0-9;]*)m/g;
const BG = /48;(?:2;\d+;\d+;\d+|5;\d+)/g;
const RULE = /^\s*[─━┄┅┈┉]{4,}\s*$/;
const MARKER = /^[ \t]{0,4}[❯›❱]/;   // 不认裸 > ，否则 markdown 引用行会被当成起点

// Claude Code 的空输入框是 `❯\xa0`（NBSP）而不是空格，判空之前先归一。
const normalize = (s) => s.replace(ESC, '').replace(/\r/g, '').replace(/\u00a0/g, ' ');
const rstrip = (s) => s.replace(/\s+$/, '');

/**
 * 按 SGR 规则解析参数，返回这一串里依次出现的 dim 状态。
 * 38/48/58 的 `2;r;g;b` 与 `5;n` 子参数必须整段消费掉，否则
 * `38;2;153;153;153` 里的那个 `2` 会被误判成 dim —— Claude Code 的横线正好
 * 用这个色，整个输入框会被判成占位、返回空字符串。
 */
function dimStates(params) {
  const parts = params ? params.split(';') : ['0'];
  const out = [];
  let i = 0;
  while (i < parts.length) {
    const p = parts[i] || '0';
    if (p === '38' || p === '48' || p === '58') {
      const nxt = i + 1 < parts.length ? parts[i + 1] : null;
      i += nxt === '2' ? 5 : nxt === '5' ? 3 : 1;
      continue;
    }
    if (p === '0' || p === '22') out.push(false);
    else if (p === '2') out.push(true);
    i += 1;
  }
  return out;
}

/** 剔除 dim 段之后的纯文本。 */
function visibleText(line) {
  const chunks = [];
  let dim = false;
  let pos = 0;
  for (const m of line.matchAll(SGR)) {
    if (!dim) chunks.push(line.slice(pos, m.index));
    for (const s of dimStates(m[1])) dim = s;
    pos = m.index + m[0].length;
  }
  if (!dim) chunks.push(line.slice(pos));
  return normalize(chunks.join(''));
}

/** 保留 dim 的纯文本，用于定位（横线和 marker 本身可能是 dim 的）。 */
const plainText = (line) => normalize(line);

/** 最后一个提示符行的下标，没有则 -1。 */
function anchorOf(plain) {
  for (let i = plain.length - 1; i >= 0; i--) if (MARKER.test(plain[i])) return i;
  return -1;
}

/** claude 式：最近的上下两条横线之间。 */
function boxBounds(plain, anchor) {
  let u = anchor - 1;
  while (u >= 0 && !RULE.test(plain[u])) u -= 1;
  let d = anchor + 1;
  while (d < plain.length && !RULE.test(plain[d])) d += 1;
  return (u >= 0 && d < plain.length) ? [u + 1, d - 1] : [anchor, anchor];
}

/** codex 式：与 marker 行同背景色的连续段（状态栏不在色块内）。 */
function bandBounds(raw, anchor) {
  const key = (l) => [...new Set(l.match(BG) || [])].sort().join(',');
  const bg = raw.map(key);
  if (!bg[anchor]) return [anchor, anchor];
  let lo = anchor;
  let hi = anchor;
  while (lo - 1 >= 0 && bg[lo - 1] === bg[anchor]) lo -= 1;
  while (hi + 1 < raw.length && bg[hi + 1] === bg[anchor]) hi += 1;
  return [lo, hi];
}

/** 去掉提示符字形、去掉两格缩进、掐掉首尾空行。 */
function finish(vis, lo, hi) {
  const out = vis.slice(lo, hi + 1);
  for (let k = 0; k < out.length; k++) {
    if (MARKER.test(out[k])) {
      out[k] = out[k].replace(MARKER, '');
      if (out[k].slice(0, 1) === ' ') out[k] = out[k].slice(1);
      break;
    }
  }
  for (let k = 0; k < out.length; k++) {
    out[k] = rstrip(out[k].slice(0, 2) === '  ' ? out[k].slice(2) : out[k]);
  }
  while (out.length && !out[0].trim()) out.shift();
  while (out.length && !out[out.length - 1].trim()) out.pop();
  return out.join('\n');
}

/**
 * @param {string} ansiText  pane.read 的 `format:"ansi"` + `strip_ansi:false` 结果
 * @param {string} [agent]   pane.get 的 agent 字段（"claude" / "codex" / 无）
 * @returns {string} 输入框里的内容，空框返回 ''
 */
function extract(ansiText, agent) {
  const raw = String(ansiText ?? '').split('\n');
  const plain = raw.map(plainText);
  const vis = raw.map(visibleText);

  const anchor = anchorOf(plain);
  if (anchor < 0) {
    // 普通 shell：没有提示符字形，取最后一行非空
    for (let i = plain.length - 1; i >= 0; i--) if (plain[i].trim()) return rstrip(plain[i]);
    return '';
  }

  let lo;
  let hi;
  if (agent === 'claude') {
    [lo, hi] = boxBounds(plain, anchor);
  } else if (agent === 'codex') {
    [lo, hi] = bandBounds(raw, anchor);
  } else {
    // 未知 agent：先色块，再横线，最后单行
    [lo, hi] = bandBounds(raw, anchor);
    if (lo === anchor && hi === anchor) [lo, hi] = boxBounds(plain, anchor);
  }
  return finish(vis, lo, hi);
}

/** 整屏纯文本（压掉空行），给「原始屏幕」调试视图用。 */
function screenLines(ansiText) {
  return String(ansiText ?? '').split('\n').map((l) => rstrip(plainText(l))).filter((l) => l.trim());
}

module.exports = { extract, screenLines, plainText, visibleText, dimStates };
