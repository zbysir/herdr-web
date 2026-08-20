'use strict';
/** 软键条按键谱的解析。 */
const test = require('node:test');
const assert = require('node:assert');
const { parseSpec, resolve, DEFAULTS } = require('../lib/softkeys');

test('ctrl+ 按终端惯例，不是简单减 96', () => {
  assert.strictEqual(parseSpec('ctrl+b'), '\x02');
  assert.strictEqual(parseSpec('ctrl+c'), '\x03');
  assert.strictEqual(parseSpec('ctrl+u'), '\x15');
  assert.strictEqual(parseSpec('ctrl+B'), '\x02');      // 大小写都行
  assert.strictEqual(parseSpec('^b'), '\x02');          // ^b 是简写
  assert.strictEqual(parseSpec('ctrl+space'), '\x00');
  assert.strictEqual(parseSpec('ctrl+['), '\x1b');
  assert.strictEqual(parseSpec('ctrl+?'), '\x7f');
});

test('具名键', () => {
  assert.strictEqual(parseSpec('esc'), '\x1b');
  assert.strictEqual(parseSpec('tab'), '\t');
  assert.strictEqual(parseSpec('enter'), '\r');
  assert.strictEqual(parseSpec('up'), '\x1b[A');
  assert.strictEqual(parseSpec('pgdn'), '\x1b[6~');
  assert.strictEqual(parseSpec('f5'), '\x1b[15~');
  assert.strictEqual(parseSpec('f1'), '\x1bOP');
  assert.strictEqual(parseSpec('shift+tab'), '\x1b[Z');
});

test('alt+ 就是前面加 ESC，可以套具名键', () => {
  assert.strictEqual(parseSpec('alt+1'), '\x1b1');
  assert.strictEqual(parseSpec('alt+g'), '\x1bg');
  assert.strictEqual(parseSpec('alt+up'), '\x1b\x1b[A');
});

test('空格分隔 = 连发多下（herdr 的前缀键就靠这个）', () => {
  assert.strictEqual(parseSpec('ctrl+b c'), '\x02c');
  assert.strictEqual(parseSpec('ctrl+b n'), '\x02n');
  assert.strictEqual(parseSpec('esc esc'), '\x1b\x1b');
});

test('双引号里的原样发送，可以带空格', () => {
  assert.strictEqual(parseSpec('"herdr" enter'), 'herdr\r');
  assert.strictEqual(parseSpec('"git status" enter'), 'git status\r');
  assert.strictEqual(parseSpec('"a b"'), 'a b');
});

test('单个字面字符', () => {
  assert.strictEqual(parseSpec('c'), 'c');
  assert.strictEqual(parseSpec('/'), '/');
});

test('不认识的东西要报错，而不是静默发出去', () => {
  assert.throws(() => parseSpec('nope'), /不认识的按键/);
  assert.throws(() => parseSpec(''), /空的/);
  assert.throws(() => parseSpec('ctrl+ab'), /只能跟一个字符/);
  assert.strictEqual(parseSpec('ctrl+space'), '\x00');   // 具名键也能套 ctrl
  assert.throws(() => parseSpec('x'.repeat(300)), /太长/);
});

test('出厂配置本身能解析，且和原来写死在 HTML 里的字节一致', () => {
  const r = resolve(DEFAULTS);
  const byLabel = Object.fromEntries(r.filter((k) => k.send).map((k) => [k.label, k.send]));
  assert.strictEqual(byLabel['⌃B 前缀'], '\x02');
  assert.strictEqual(byLabel.Esc, '\x1b');
  assert.strictEqual(byLabel.Tab, '\t');
  assert.strictEqual(byLabel['↑'], '\x1b[A');
  assert.strictEqual(byLabel['↓'], '\x1b[B');
  assert.strictEqual(byLabel['←'], '\x1b[D');
  assert.strictEqual(byLabel['→'], '\x1b[C');
  assert.strictEqual(byLabel.PgUp, '\x1b[5~');
  assert.strictEqual(byLabel.PgDn, '\x1b[6~');
  assert.strictEqual(byLabel['⌃C'], '\x03');
  assert.strictEqual(byLabel['↵'], '\r');
  // 粘滞键和呼出键盘不是 send
  assert.deepStrictEqual(r.filter((k) => k.sticky).map((k) => k.sticky), ['ctrl', 'alt']);
  assert.strictEqual(r.filter((k) => k.act === 'kbd').length, 1);
});

test('校验：三种形态只能占一种，标签有长度上限', () => {
  assert.throws(() => resolve([{ label: 'x' }]), /正好是 send \/ sticky \/ act/);
  assert.throws(() => resolve([{ send: 'esc', sticky: 'ctrl' }]), /正好是 send \/ sticky \/ act/);
  assert.throws(() => resolve([{ sticky: 'shift' }]), /只能是 ctrl 或 alt/);
  assert.throws(() => resolve([{ act: 'nope' }]), /只支持 kbd/);
  assert.throws(() => resolve([{ send: 'esc', label: 'x'.repeat(13) }]), /名字太长/);
  assert.doesNotThrow(() => resolve([{ send: 'esc', label: 'x'.repeat(12) }]));   // 12 是上限，合法
});

test('没写标签就用按键谱兜底', () => {
  assert.strictEqual(resolve([{ send: 'ctrl+b c' }])[0].label, 'ctrl+b c');
});

test('「常用」下拉里每一条都能解析（防止列表里手抖打错按键谱）', () => {
  const { PRESETS } = require('../lib/softkeys');
  const flat = PRESETS.flatMap((g) => g.items.map((it) => ({ ...it, group: g.group })));
  assert.ok(flat.length > 20, '预设太少了，是不是漏了');
  for (const it of flat) {
    assert.doesNotThrow(() => resolve([it]), `预设「${it.group} / ${it.label}」解析失败`);
  }
  // 抽查几条关键的字节，别只验"不抛错"
  const spec = (label) => flat.find((x) => x.label === label).send;
  assert.strictEqual(parseSpec(spec('放大')), '\x02z');          // prefix+z
  assert.strictEqual(parseSpec(spec('关标签')), '\x02X');        // prefix+shift+x
  assert.strictEqual(parseSpec(spec('横分屏')), '\x02-');        // prefix+minus
  assert.strictEqual(parseSpec(spec('下个 pane')), '\x02\t');    // prefix+tab
  assert.strictEqual(parseSpec(spec('敲 herdr')), 'herdr\r');
});
