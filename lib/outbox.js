'use strict';
/**
 * 发件箱：列目标 pane、拉回远端输入框、覆盖式投稿。
 *
 * 「发件箱，不是镜像」—— 不做双向同步。两个缓冲区一个字节流，同步永远追不上。
 * 每次整段覆盖、发完清空本地框，不发增量。
 */
const { call } = require('./herdr-api');
const { extract, screenLines } = require('./composer');

// 要拿到转义序列必须 format:"ansi" + strip_ansi:false —— format:"text" 即使
// strip_ansi:false 也不含 ESC。
const READ = { source: 'visible', format: 'ansi', strip_ansi: false };

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * 两次 pane.read 之间额外等多久（对付快照的一帧延迟）。
 *
 * 默认 0，因为**根本不需要等**：从 node 发一个请求到拿到响应固定要 ~106ms
 * （原因见下面 herdr-api 的说明和 HANDOFF），两次读天然就隔了一个服务端 tick，
 * 第二次读一定是新的一帧。以前这里睡 120ms，纯属白送。
 * 真要调（比如换了个响应更快的 herdr）用 HERDR_WEB_SETTLE_MS。
 */
const SETTLE_MS = Math.max(0, Number(process.env.HERDR_WEB_SETTLE_MS ?? 0));

// 目标可以是具体的 pane_id，也可以是这个哨兵值 —— 意思是「投给我此刻在 herdr 里
// 激活的那个 pane」。默认走这条：一般人是先在 herdr 里点到某个 pane，再去网页说话，
// 不想再选一次。解析放在服务端做，这样投的永远是**按下按钮那一刻**的焦点。
const FOLLOW = '__focused';

async function paneInfo(target) {
  const r = await call('pane.get', { pane_id: target });
  return r.pane || r;
}

/** 把 target 解析成具体 pane：哨兵值 / 空 → 当前激活的 pane。 */
async function resolve(target) {
  if (target && target !== FOLLOW) return { target, info: await paneInfo(target), followed: false };
  const r = await call('pane.current', {});
  const info = r.pane || r;
  if (!info || !info.pane_id) throw new Error('herdr 里没有激活的 pane');
  return { target: info.pane_id, info, followed: true };
}

// 响应嵌在 result.read.text
const readAnsi = async (target) => (await call('pane.read', { pane_id: target, ...READ })).read.text;

/**
 * pane.read 的快照有一帧延迟（写完立刻读会拿到上一帧），所以读两次、取后一次。
 *
 * 拉回 / 轮询也得走这条：单次读会拿到上一帧，表现就是「切了 pane，框里还是旧内容」
 * 或者「远端刚改的字要等下一拍才出现」。更糟的是自动拉回会把这一帧陈旧内容记成
 * 「已对齐」，用户接着在陈旧内容上编辑。
 */
async function readSettled(target) {
  await call('pane.read', { pane_id: target, ...READ });
  if (SETTLE_MS) await sleep(SETTLE_MS);
  return readAnsi(target);
}

const readComposer = async (target, agent) => extract(await readSettled(target), agent);

/** 目标列表：pane.list 的顺序 + workspace / tab 的可读标签。 */
async function listTargets() {
  const panes = (await call('pane.list', {})).panes || [];
  const wsLabel = new Map();
  const tabLabel = new Map();
  try {
    for (const w of (await call('workspace.list', {})).workspaces || []) wsLabel.set(w.workspace_id, w.label || `w${w.number}`);
    for (const t of (await call('tab.list', {})).tabs || []) tabLabel.set(t.tab_id, t.label || `t${t.number}`);
  } catch { /* 标签只是好看，拿不到就退回 id */ }

  return panes.map((p) => ({
    id: p.pane_id,
    agent: p.agent || null,
    status: p.agent_status || 'unknown',
    workspace: wsLabel.get(p.workspace_id) || p.workspace_id,
    tab: tabLabel.get(p.tab_id) || p.tab_id,
    title: p.terminal_title_stripped || '',
    cwd: p.cwd || '',
    focused: !!p.focused,
  }));
}

/** 一个 pane 的可显示身份（workspace / tab 的好看标签由前端用缓存补）。 */
const identify = (info, followed) => ({
  target: info.pane_id,
  followed: !!followed,
  agent: info.agent || null,
  status: info.agent_status || 'unknown',
  workspaceId: info.workspace_id || '',
  tabId: info.tab_id || '',
  title: info.terminal_title_stripped || '',
  cwd: info.cwd || '',
});

/** 拉回：远端输入框内容（mode='screen' 时给整屏纯文本，纯调试用）。 */
async function pull(target, mode) {
  const { target: id, info, followed } = await resolve(target);
  const ansi = await readSettled(id);
  const out = identify(info, followed);
  if (mode === 'screen') out.screen = screenLines(ansi);
  else out.text = extract(ansi, info.agent || null);
  return out;
}

/**
 * 轮询用：一次拿到「现在焦点在哪个 pane」+「那个 pane 输入框里是什么」。
 * 合成一个请求是为了让前端的自动拉回只花两次 socket 往返。
 */
const sync = (target) => pull(target, null);

/**
 * 把草稿推到远端输入框，但**不回车**。给「双向同步」的本地→远端那半边用。
 *
 * 只对有 agent 的 pane 干这件事：agent 有真正的输入框，写进去就只是文本。
 * 普通 pane 里跑的可能是 vim / 某个选择器，那里的字符是**命令**不是文本，
 * 跟着焦点乱推会直接触发操作。
 */
async function draft(target, text) {
  const { target: id, info, followed } = await resolve(target);
  const agent = info.agent || null;
  if (!agent) return { ...identify(info, followed), skipped: 'not-agent' };

  const body = String(text ?? '');
  const cleared = await clearComposer(id, agent);
  if (cleared.empty === false) return { ...identify(info, followed), skipped: 'busy' };
  if (body) await call('pane.send_input', { pane_id: id, text: body });
  return { ...identify(info, followed), pushed: [...body].length };
}

/**
 * 清空远端输入行。
 *
 * `agent.prompt` / `pane.send_text` 都是**追加**语义，不是「把输入框设为这段文字」。
 * 用户在远端按过 Tab 补全或上下键历史之后，输入行上已经有残留，直接投就变成
 * `残留 + 新文本` 一起回车。
 *
 * 实测：N 行输入需要 **2N−1 次** ctrl+u（一次删掉本行内容，再一次删掉这个空行），
 * Claude Code 和 Codex 都是这个规律。所以固定次数只够两行 —— 这里按实际残留
 * 动态收敛，读一次、按行数补够、再读一次确认。
 */
async function clearComposer(target, agent, rounds = 6) {
  if (!agent) {
    // 普通 shell：extract 返回的是提示符行本身，没法拿「空」当判据；
    // 而 zsh / bash 的 ctrl+u 一次就干掉整个 buffer，打 3 次足够。
    await call('pane.send_input', { pane_id: target, keys: ['ctrl+u', 'ctrl+u', 'ctrl+u'] });
    return { rounds: 1, empty: null };
  }
  for (let i = 1; i <= rounds; i++) {
    const cur = await readComposer(target, agent);
    if (!cur) return { rounds: i - 1, empty: true };
    const need = Math.min(cur.split('\n').length * 2 - 1, 24);
    await call('pane.send_input', { pane_id: target, keys: Array(need).fill('ctrl+u') });
  }
  return { rounds, empty: !(await readComposer(target, agent)) };
}

/**
 * 覆盖式投稿：先清空，再整段提交。
 *
 * 清空和提交是两次连接（服务端一个连接只处理一个请求），顺序 await 天然有序，
 * 不需要 sleep。
 */
async function say(target, text) {
  const body = String(text ?? '').replace(/\s+$/, '');
  if (!body) throw new Error('空文本，不发');

  const { target: id, info, followed } = await resolve(target);
  const agent = info.agent || null;
  const cleared = await clearComposer(id, agent);

  // 清不空就别投。追加语义下投进去就是「残留 + 新文本」一起回车。
  // 实测最常见的原因是那个 pane 正开着一个选择框 / 确认框（agent 会把它画在
  // 输入框那块区域里），此时 agent_status 仍然是 idle，光看状态区分不出来 ——
  // 「清不空」反而是个可靠信号。
  if (cleared.empty === false) {
    throw new Error('远端输入框清不空，没敢投：那个 pane 可能正开着选择框 / 确认框。先去按 Esc 收掉再投。');
  }

  if (agent) {
    // agent.prompt 会按 pane 当前的 bracketed-paste 模式正确编码 Enter，
    // 别自己拼 \r。
    await call('agent.prompt', { target: id, text: body });
  } else {
    // send_input 一个请求里的顺序是 text 先、keys 后，所以 text+enter 可以合成一次
    await call('pane.send_input', { pane_id: id, text: body, keys: ['enter'] });
  }
  return { ...identify(info, followed), cleared, chars: [...body].length, lines: body.split('\n').length };
}

module.exports = { listTargets, pull, sync, draft, say, clearComposer, readComposer, FOLLOW, SETTLE_MS };
