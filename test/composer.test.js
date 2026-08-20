'use strict';
/**
 * 输入框抽取的回归测试。
 *
 * `fixtures/*.ansi` 是**真实抓下来的**屏幕（pane.read 的 format:"ansi" +
 * strip_ansi:false），来自活着的 Claude Code / Codex / zsh pane。
 * 另外一批是合成用例，专门盯 HANDOFF 里记的那三个会静默返回空字符串的坑。
 *
 *   node --test test/
 */
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const { extract, dimStates } = require('../lib/composer');

const fixture = (n) => fs.readFileSync(path.join(__dirname, 'fixtures', `${n}.ansi`), 'utf8');

/* ------------------------------------------------------------ 真实抓屏 */
test('真实抓屏：claude 空框 → 空字符串', () => {
  assert.strictEqual(extract(fixture('claude-empty'), 'claude'), '');
});

test('真实抓屏：claude 有内容 → 原文（含中文标点和「」）', () => {
  assert.strictEqual(extract(fixture('claude-typed'), 'claude'), '帮我看看 README，里面「三种连法」那节。');
});

test('真实抓屏：claude 多行，以 > 开头的续行不被当成起点', () => {
  assert.strictEqual(
    extract(fixture('claude-multiline'), 'claude'),
    '第一行：改这个函数\n> 这行以尖括号开头，不能被当成输入框起点\n第三行结束',
  );
});

test('真实抓屏：codex 空框（有 dim 占位提示）→ 空字符串', () => {
  // 这一屏里 dim 的占位文字是 "Implement {feature}"，纯文本层面和真实输入
  // 无法区分，判据只能是它整段套在 \x1b[2m 里
  assert.strictEqual(extract(fixture('codex-empty'), 'codex'), '');
});

test('真实抓屏：codex 有内容 → 原文', () => {
  assert.strictEqual(extract(fixture('codex-typed'), 'codex'), '看一下 composer.js 的 dim 处理，别改。');
});

test('真实抓屏：codex 多行', () => {
  assert.strictEqual(
    extract(fixture('codex-multiline'), 'codex'),
    '第一行：codex 多行\n> 引用行不能当起点\n第三行',
  );
});

test('真实抓屏：shell pane → 提示符行', () => {
  // zsh 的 ➜ 不在 [❯›❱] 里，所以走「没有 anchor」那条：取最后一行非空
  assert.strictEqual(extract(fixture('shell-empty'), null), '➜  herdr-web git:(master) ✗');
  assert.strictEqual(extract(fixture('shell-typed'), null), '➜  herdr-web git:(master) ✗ echo 你好，世界');
});

test('真实抓屏：codex 的屏幕交给「未知 agent」也能嗅探出来', () => {
  // 未知 agent 先试背景色块，codex 的输入区正好是色块，所以结果应当和显式指定一致
  assert.strictEqual(extract(fixture('codex-typed'), undefined), extract(fixture('codex-typed'), 'codex'));
});

test('真实抓屏：claude 的屏幕交给「未知 agent」也能嗅探出来', () => {
  // claude 的输入框没有背景色块 → 退回横线夹的 box
  assert.strictEqual(extract(fixture('claude-typed'), undefined), extract(fixture('claude-typed'), 'claude'));
});

/* ------------------------------------------------------------ 合成用例：三个坑 */
const DIM = '\x1b[2m';
const OFF = '\x1b[0m';
const RULE = '─'.repeat(40);
const BG = '\x1b[48;2;49;52;57m';   // codex 输入区的背景色

test('坑 1：dim 文字算 chrome 不算内容（纯 dim 占位 → 空）', () => {
  const screen = [`${RULE}`, `❯ ${DIM}Run /review on my current changes${OFF}`, `${RULE}`].join('\n');
  assert.strictEqual(extract(screen, 'claude'), '');
});

test('坑 2：38;2;153;153;153 里的那个 2 不是 dim', () => {
  // 按分号朴素切分会把它读成 dim，而 Claude Code 的横线正好用这个色，
  // 整个输入框会被判成占位、返回空
  const color = '\x1b[38;2;153;153;153m';
  const screen = [
    `${color}${RULE}${OFF}`,
    `${color}❯${OFF} 真实内容，前面挂着 38;2 前景色`,
    `${color}${RULE}${OFF}`,
  ].join('\n');
  assert.strictEqual(extract(screen, 'claude'), '真实内容，前面挂着 38;2 前景色');
});

test('坑 2 的字表：38/48/58 的子参数被整段消费，裸 2 才是 dim', () => {
  assert.deepStrictEqual(dimStates('38;2;153;153;153'), []);      // 全被消费掉
  assert.deepStrictEqual(dimStates('48;5;236'), []);
  assert.deepStrictEqual(dimStates('2'), [true]);
  assert.deepStrictEqual(dimStates('38;2;1;2;3;2'), [true]);      // 消费完之后剩下的那个 2 是 dim
  assert.deepStrictEqual(dimStates('2;22'), [true, false]);
  assert.deepStrictEqual(dimStates(''), [false]);                 // \x1b[m == reset
});

test('坑 3：claude 的空输入框是 ❯+NBSP，不是空格', () => {
  const screen = [`${RULE}`, '❯\u00a0', `${RULE}`].join('\n');
  assert.strictEqual(extract(screen, 'claude'), '');
});

test('坑 3 续：NBSP 不归一的话，内容前面会挂一个 NBSP 一起发出去', () => {
  // 空框那个用例其实不吃劲（rstrip 顺手就把 NBSP 干掉了）；真正吃劲的是有内容时
  // 「去掉提示符后那一个分隔空格」—— NBSP 不归一就匹配不上，会留在正文最前面。
  const screen = [`${RULE}`, '❯\u00a0真实内容', `${RULE}`].join('\n');
  assert.strictEqual(extract(screen, 'claude'), '真实内容');
});

/* ------------------------------------------------------------ 合成用例：收边 */
test('codex：状态栏不在背景色块内，不会被圈进来', () => {
  const screen = [
    '  上面的历史输出',
    `${BG}❯ 输入的内容${OFF}`,
    '  gpt-5 · ~/repo · master · 状态栏在色块外',
  ].join('\n');
  assert.strictEqual(extract(screen, 'codex'), '输入的内容');
});

test('codex：同背景色的连续多行算一个输入区', () => {
  const screen = [
    '  历史输出',
    `${BG}❯ 第一行${OFF}`,
    `${BG}  第二行${OFF}`,
    '  状态栏',
  ].join('\n');
  assert.strictEqual(extract(screen, 'codex'), '第一行\n第二行');
});

test('claude：markdown 引用行不会被当成输入框起点', () => {
  const screen = [
    `${RULE}`,
    '❯ 请看这段引用：',
    '  > 被引用的话',
    '  收尾一句',
    `${RULE}`,
  ].join('\n');
  assert.strictEqual(extract(screen, 'claude'), '请看这段引用：\n> 被引用的话\n收尾一句');
});

test('没有提示符字形、整屏全空 → 空字符串', () => {
  assert.strictEqual(extract('\n\n   \n', null), '');
  assert.strictEqual(extract('', null), '');
  assert.strictEqual(extract(null, null), '');
});
