'use strict';
/**
 * herdr-web —— 浏览器里的终端，通过 PTY 直连本机 shell 或 ssh 到已保存的主机。
 * 目标：在 web 终端里敲 `herdr`，体验和 iTerm 里一致。
 */
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { WebSocketServer } = require('ws');
const pty = require('node-pty');
const store = require('./lib/store');

const HOST = process.env.HERDR_WEB_HOST || '127.0.0.1';
const PORT = Number(process.env.HERDR_WEB_PORT || 7788);
// token 落盘，重启后不变 —— 否则手机上存的书签每次重启都失效
function persistedToken() {
  if (process.env.HERDR_WEB_TOKEN) return process.env.HERDR_WEB_TOKEN;
  const file = path.join(store.DIR, 'token');
  try {
    const t = fs.readFileSync(file, 'utf8').trim();
    if (t) return t;
  } catch { /* 第一次跑 */ }
  const t = crypto.randomBytes(12).toString('hex');
  try {
    fs.mkdirSync(store.DIR, { recursive: true, mode: 0o700 });
    fs.writeFileSync(file, t + '\n', { mode: 0o600 });
  } catch { /* 写不了就退回一次性 token */ }
  return t;
}
const TOKEN = persistedToken();
const LOGIN_SHELL = process.env.HERDR_WEB_SHELL || process.env.SHELL || '/bin/zsh';
const SSH_BIN = process.env.HERDR_WEB_SSH || '/usr/bin/ssh';
const COPY_ID_BIN = process.env.HERDR_WEB_SSH_COPY_ID || '/usr/bin/ssh-copy-id';

const ROOT = __dirname;
const PUBLIC = path.join(ROOT, 'public');
const LOOPBACK = HOST === '127.0.0.1' || HOST === 'localhost' || HOST === '::1';

// ---------------------------------------------------------------- node-pty 修补
// npm 的 prebuild 包偶尔丢掉 spawn-helper 的可执行位，会让 spawn 直接抛
// "posix_spawnp failed"。启动时兜一下。
function fixSpawnHelper() {
  const dir = path.join(ROOT, 'node_modules/node-pty/prebuilds', `${process.platform}-${process.arch}`);
  const helper = path.join(dir, 'spawn-helper');
  try {
    const st = fs.statSync(helper);
    if (!(st.mode & 0o111)) {
      fs.chmodSync(helper, 0o755);
      console.log('[herdr-web] 已修复 node-pty spawn-helper 可执行位');
    }
  } catch { /* Windows / 源码编译的情况没有这个文件 */ }
}

// ---------------------------------------------------------------- 子进程环境
// 关键点：herdr 检测到 HERDR_* 就会拒绝启动（"nested herdr is disabled"）。
// 如果本服务是在 herdr / tmux 的 pane 里起的，必须把这些痕迹清掉。
const DROP_ENV = /^(HERDR_|TMUX|TMUX_|ZELLIJ|STY$|ITERM_|TERM_PROGRAM|TERM_SESSION_ID|TERM_FEATURES|LC_TERMINAL|CLAUDECODE$|CLAUDE_CODE_)/;

function childEnv() {
  const env = {};
  for (const [k, v] of Object.entries(process.env)) {
    if (DROP_ENV.test(k)) continue;
    env[k] = v;
  }
  env.TERM = 'xterm-256color';
  env.COLORTERM = 'truecolor';
  env.TERM_PROGRAM = 'herdr-web';
  if (!env.LANG) env.LANG = 'en_US.UTF-8';
  delete env.NODE_OPTIONS;
  return env;
}

// 临时地址模式：只做基本校验（挡住手抖，不是安全边界 —— 能开这个页面的人本来就有 shell）
const SAFE_ARG = /^[A-Za-z0-9._@:%+/=,[\]-]+$/;
function adhocSshArgs(target) {
  const tokens = String(target).trim().split(/\s+/).filter(Boolean);
  if (!tokens.length) throw new Error('ssh 目标为空');
  for (const t of tokens) if (!SAFE_ARG.test(t)) throw new Error(`ssh 参数含非法字符: ${t}`);
  return ['-tt', '-o', 'ServerAliveInterval=30', ...tokens];
}

function spawnSession({ mode, target, hostId, cols, rows }) {
  const opts = {
    name: 'xterm-256color',
    cols: cols || 120,
    rows: rows || 34,
    cwd: os.homedir(),
    env: childEnv(),
  };

  if (mode === 'host' || mode === 'copyid') {
    const h = store.getHost(hostId);
    if (!h) throw new Error('主机不存在，可能已被删除');
    store.touchHost(h.id);
    const where = h.user ? `${h.user}@${h.host}` : h.host;
    if (mode === 'copyid') {
      return { proc: pty.spawn(COPY_ID_BIN, store.copyIdArgsFor(h), opts), label: `ssh-copy-id → ${h.name}` };
    }
    return { proc: pty.spawn(SSH_BIN, store.sshArgsFor(h), opts), label: `${h.name} (${where})` };
  }
  if (mode === 'ssh') {
    return { proc: pty.spawn(SSH_BIN, adhocSshArgs(target), opts), label: `ssh ${target}` };
  }
  return { proc: pty.spawn(LOGIN_SHELL, ['-l'], opts), label: path.basename(LOGIN_SHELL) };
}

// ---------------------------------------------------------------- 静态资源
const MIME = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.map': 'application/json' };
const VENDOR = {
  'xterm.js': '@xterm/xterm/lib/xterm.js',
  'xterm.css': '@xterm/xterm/css/xterm.css',
  'addon-fit.js': '@xterm/addon-fit/lib/addon-fit.js',
  'addon-webgl.js': '@xterm/addon-webgl/lib/addon-webgl.js',
  'addon-web-links.js': '@xterm/addon-web-links/lib/addon-web-links.js',
  'addon-unicode11.js': '@xterm/addon-unicode11/lib/addon-unicode11.js',
  'addon-clipboard.js': '@xterm/addon-clipboard/lib/addon-clipboard.js',
};

function serveFile(res, file) {
  fs.readFile(file, (err, buf) => {
    if (err) { res.writeHead(404); return res.end('not found'); }
    res.writeHead(200, { 'content-type': MIME[path.extname(file)] || 'application/octet-stream', 'cache-control': 'no-cache' });
    res.end(buf);
  });
}

// ---------------------------------------------------------------- API
function tokenOk(given) {
  const a = Buffer.from(String(given || ''));
  const b = Buffer.from(TOKEN);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

function readBody(req, limit = 256 * 1024) {
  return new Promise((resolve, reject) => {
    let n = 0;
    const chunks = [];
    req.on('data', (c) => {
      n += c.length;
      if (n > limit) { reject(new Error('请求体过大')); req.destroy(); return; }
      chunks.push(c);
    });
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8');
      if (!raw) return resolve({});
      try { resolve(JSON.parse(raw)); } catch { reject(new Error('请求体不是合法 JSON')); }
    });
    req.on('error', reject);
  });
}

async function handleApi(req, res, url) {
  if (!tokenOk(url.searchParams.get('token'))) {
    res.writeHead(401, { 'content-type': 'application/json' });
    return res.end(JSON.stringify({ error: 'token 不对' }));
  }
  const seg = url.pathname.split('/').filter(Boolean).slice(1);   // 去掉 'api'
  const method = req.method;
  const json = (code, body) => {
    res.writeHead(code, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
    res.end(JSON.stringify(body));
  };

  try {
    // GET /api/state —— 一次拿齐主机、密钥、ssh 目录、config 候选
    if (method === 'GET' && seg[0] === 'state') {
      return json(200, {
        hosts: store.listHosts(),
        keys: await store.listKeys(),
        sshKeys: await store.scanSshDir(),
        sshConfig: store.parseSshConfig(),
        shell: path.basename(LOGIN_SHELL),
        user: os.userInfo().username,
        hostname: os.hostname(),
        keyDir: store.KEY_DIR,
        secureContext: LOOPBACK,
      });
    }

    if (seg[0] === 'hosts') {
      if (method === 'GET' && !seg[1]) return json(200, { hosts: store.listHosts() });
      if (method === 'POST' && !seg[1]) return json(200, { host: store.addHost(await readBody(req)) });
      if (method === 'PUT' && seg[1]) return json(200, { host: store.updateHost(seg[1], await readBody(req)) });
      if (method === 'DELETE' && seg[1]) { store.removeHost(seg[1]); return json(200, { ok: true }); }
      // POST /api/hosts/import-config —— 把 ~/.ssh/config 里的别名批量存成主机
      if (method === 'POST' && seg[1] === 'import-config') {
        const { names } = await readBody(req);
        const want = new Set(Array.isArray(names) ? names : []);
        const exist = new Set(store.listHosts().map((h) => h.name));
        const added = [];
        for (const e of store.parseSshConfig()) {
          if (want.size && !want.has(e.alias)) continue;
          if (exist.has(e.name)) continue;
          // 别名本身就够 ssh 找到全部配置，所以 user/port/key 都留空
          added.push(store.addHost({ name: e.name, host: e.alias, note: '来自 ~/.ssh/config' }));
        }
        return json(200, { added });
      }
    }

    if (seg[0] === 'keys') {
      if (method === 'GET' && !seg[1]) return json(200, { keys: await store.listKeys() });
      if (method === 'POST' && seg[1] === 'generate') {
        const { name, comment } = await readBody(req);
        return json(200, { key: await store.generateKey(name, comment) });
      }
      if (method === 'POST' && seg[1] === 'import') {
        const { name, privateKey } = await readBody(req);
        return json(200, { key: await store.importKey(name, privateKey) });
      }
      if (method === 'DELETE' && seg[1]) { store.removeKey(decodeURIComponent(seg[1])); return json(200, { ok: true }); }
    }

    return json(404, { error: '没有这个接口' });
  } catch (err) {
    return json(400, { error: err.message });
  }
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://x');
  let p = url.pathname;

  if (p.startsWith('/api/')) return void handleApi(req, res, url);
  if (p === '/') p = '/index.html';

  if (p.startsWith('/vendor/')) {
    const rel = VENDOR[p.slice('/vendor/'.length)];
    if (!rel) { res.writeHead(404); return res.end('not found'); }
    return serveFile(res, path.join(ROOT, 'node_modules', rel));
  }
  const file = path.join(PUBLIC, path.normalize(p).replace(/^(\.\.[/\\])+/, ''));
  if (!file.startsWith(PUBLIC)) { res.writeHead(403); return res.end('forbidden'); }
  serveFile(res, file);
});

// ---------------------------------------------------------------- WebSocket
const wss = new WebSocketServer({ noServer: true });

function originOk(req) {
  const origin = req.headers.origin;
  if (!origin) return true; // 非浏览器客户端
  try { return new URL(origin).host === req.headers.host; } catch { return false; }
}

server.on('upgrade', (req, socket, head) => {
  const url = new URL(req.url, 'http://x');
  if (url.pathname !== '/pty' || !tokenOk(url.searchParams.get('token')) || !originOk(req)) {
    socket.write('HTTP/1.1 401 Unauthorized\r\n\r\n');
    return socket.destroy();
  }
  wss.handleUpgrade(req, socket, head, (ws) => wss.emit('connection', ws, req));
});

let seq = 0;
wss.on('connection', (ws, req) => {
  const url = new URL(req.url, 'http://x');
  const q = url.searchParams;
  const mode = ['ssh', 'host', 'copyid'].includes(q.get('mode')) ? q.get('mode') : 'local';
  const cols = Number(q.get('cols')) || 120;
  const rows = Number(q.get('rows')) || 34;
  const id = ++seq;

  let session;
  try {
    session = spawnSession({ mode, target: q.get('target') || '', hostId: q.get('hostId') || '', cols, rows });
  } catch (err) {
    ws.send(JSON.stringify({ t: 'fatal', msg: err.message }));
    return ws.close();
  }
  const { proc, label } = session;
  console.log(`[herdr-web] #${id} 打开 ${label} (pid ${proc.pid}, ${cols}x${rows})`);
  ws.send(JSON.stringify({ t: 'ready', label, pid: proc.pid, mode }));

  // PTY 输出走二进制帧，控制消息走文本帧，前端靠帧类型区分
  proc.onData((d) => { if (ws.readyState === ws.OPEN) ws.send(Buffer.from(d, 'utf8')); });
  proc.onExit(({ exitCode, signal }) => {
    console.log(`[herdr-web] #${id} 退出 code=${exitCode} signal=${signal}`);
    if (ws.readyState === ws.OPEN) {
      ws.send(JSON.stringify({ t: 'exit', code: exitCode, signal }));
      ws.close();
    }
  });

  ws.on('message', (raw, isBinary) => {
    if (isBinary) { proc.write(Buffer.from(raw).toString('binary')); return; }
    let m;
    try { m = JSON.parse(raw.toString()); } catch { return; }
    if (m.t === 'i') proc.write(m.d);
    else if (m.t === 'r' && m.cols > 0 && m.rows > 0) {
      try { proc.resize(Math.min(m.cols, 1000), Math.min(m.rows, 1000)); } catch { /* 进程已死 */ }
    }
  });

  const ping = setInterval(() => { if (ws.readyState === ws.OPEN) ws.ping(); }, 25000);
  ws.on('close', () => {
    clearInterval(ping);
    try { proc.kill(); } catch { /* 已退出 */ }
    console.log(`[herdr-web] #${id} 前端断开，关闭 PTY`);
  });
});

// ---------------------------------------------------------------- 启动
// 机器上虚拟网卡一大堆（OrbStack / VPN / bridge），挑出手机真能连上的那个
function lanAddresses() {
  const out = [];
  for (const [name, list] of Object.entries(os.networkInterfaces())) {
    for (const ni of list || []) {
      if (ni.family !== 'IPv4' || ni.internal) continue;
      let score = 0;
      if (/^en\d/.test(name)) score += 10;                                 // 无线 / 有线
      if (/^(bridge|utun|vmnet|llw|awdl|anpi|ap\d|docker|veth|tap|tun)/.test(name)) score -= 10;
      if (/^198\.(18|19)\./.test(ni.address)) score -= 20;                 // benchmark 段，OrbStack 在用
      if (ni.address.endsWith('.0')) score -= 5;                           // 虚拟网卡常见的怪地址
      if (/^(192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.)/.test(ni.address)) score += 2;
      out.push({ name, address: ni.address, score });
    }
  }
  return out.sort((a, b) => b.score - a.score);
}

// 极小的 QR（byte 模式 + L 级纠错），只为了让手机扫一下就进，不追求通用性
function qrLines(text) {
  try { return require('./lib/qr').render(text); } catch { return null; }
}

store.init();
fixSpawnHelper();
server.listen(PORT, HOST, () => {
  const nics = LOOPBACK ? [] : lanAddresses();
  const url = (h) => `http://${h}:${PORT}/?token=${TOKEN}`;

  console.log('');
  console.log('  herdr-web 已启动');
  console.log(`  ${url('127.0.0.1')}`);
  nics.forEach((n, i) => console.log(`  ${url(n.address)}   ${n.name}${i === 0 ? '  ← 手机用这个' : ''}`));
  console.log(`  shell：${LOGIN_SHELL}   数据目录：${store.DIR}`);

  if (nics.length) {
    const qr = qrLines(url(nics[0].address));
    if (qr) { console.log(''); for (const l of qr) console.log('  ' + l); }
    console.log('');
    console.log('  ⚠️  正在监听 ' + HOST + '：局域网里任何拿到 token 的人都能拿到你的 shell。');
    console.log('     临时试用可以，别长期这么放着；要长期用请套 TLS + 真身份认证的反代。');
    console.log('     注意 http 不是安全上下文，手机上剪贴板相关功能会退化。');
  }
  console.log('');
});
