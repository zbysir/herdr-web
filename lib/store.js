'use strict';
/**
 * 主机 + 托管密钥的落盘存储。
 *
 * 目录：~/.herdr-web/            0700
 *        ├─ hosts.json           0600
 *        └─ keys/<name>          0600  私钥
 *           keys/<name>.pub      0644  公钥
 *
 * 私钥内容永远不出这台机器：HTTP 接口只返回指纹和公钥。
 * passphrase 和登录密码一概不存 —— 系统 ssh 会在网页终端里当场问。
 */
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFile } = require('node:child_process');

const DIR = process.env.HERDR_WEB_DIR || path.join(os.homedir(), '.herdr-web');
const KEY_DIR = path.join(DIR, 'keys');
const HOSTS_FILE = path.join(DIR, 'hosts.json');
const SSH_KEYGEN = fs.existsSync('/usr/bin/ssh-keygen') ? '/usr/bin/ssh-keygen' : 'ssh-keygen';

// 名字会拼进文件路径，必须严格
const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$/;
// 会拼进 ssh 命令行的字段（不经 shell，但仍然挡住手抖和注入形状）
const ARG_RE = /^[A-Za-z0-9._@:%+/=,[\]-]{1,200}$/;

function init() {
  fs.mkdirSync(KEY_DIR, { recursive: true, mode: 0o700 });
  fs.chmodSync(DIR, 0o700);
  fs.chmodSync(KEY_DIR, 0o700);
  if (!fs.existsSync(HOSTS_FILE)) writeAll({ hosts: [] });
}

function run(file, args, opts = {}) {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: 20000, ...opts }, (err, stdout, stderr) => {
      if (err) { err.stderr = String(stderr || ''); return reject(err); }
      resolve(String(stdout));
    });
  });
}

/* ------------------------------------------------------------------ hosts */
function readAll() {
  try { return JSON.parse(fs.readFileSync(HOSTS_FILE, 'utf8')); } catch { return { hosts: [] }; }
}
function writeAll(data) {
  fs.writeFileSync(HOSTS_FILE, JSON.stringify(data, null, 2), { mode: 0o600 });
}

function listHosts() {
  return readAll().hosts;
}
function getHost(id) {
  return readAll().hosts.find((h) => h.id === id) || null;
}

function normalizeHost(input, base) {
  const h = base ? { ...base } : { id: 'h_' + crypto.randomBytes(5).toString('hex') };
  const str = (v) => (v == null ? '' : String(v).trim());

  h.name = str(input.name) || str(input.host);
  h.host = str(input.host);
  h.user = str(input.user);
  h.jump = str(input.jump);
  h.keyRef = str(input.keyRef);            // '' | managed:<name> | path:<abs>
  h.port = input.port ? Number(input.port) : 0;
  h.acceptNew = !!input.acceptNew;         // 首次连接自动信任主机指纹
  h.note = str(input.note).slice(0, 200);

  if (!h.name) throw new Error('名称不能为空');
  if (!h.host) throw new Error('主机不能为空');
  for (const [label, v] of [['主机', h.host], ['用户', h.user], ['跳板', h.jump]]) {
    if (v && !ARG_RE.test(v)) throw new Error(`${label}含非法字符：${v}`);
  }
  if (h.port && !(h.port >= 1 && h.port <= 65535)) throw new Error('端口不合法');
  if (h.keyRef) keyPathOf(h.keyRef);       // 顺手校验引用是否有效
  return h;
}

function addHost(input) {
  const data = readAll();
  const h = normalizeHost(input);
  data.hosts.push(h);
  writeAll(data);
  return h;
}

function updateHost(id, input) {
  const data = readAll();
  const i = data.hosts.findIndex((x) => x.id === id);
  if (i < 0) throw new Error('主机不存在');
  data.hosts[i] = normalizeHost(input, data.hosts[i]);
  writeAll(data);
  return data.hosts[i];
}

function removeHost(id) {
  const data = readAll();
  data.hosts = data.hosts.filter((x) => x.id !== id);
  writeAll(data);
}

function touchHost(id) {
  const data = readAll();
  const h = data.hosts.find((x) => x.id === id);
  if (!h) return;
  h.lastUsedAt = new Date().toISOString();
  writeAll(data);
}

/* ------------------------------------------------------------------ keys */
function keyPathOf(ref) {
  if (!ref) return null;
  if (ref.startsWith('managed:')) {
    const name = ref.slice(8);
    if (!NAME_RE.test(name)) throw new Error(`密钥名不合法：${name}`);
    const p = path.join(KEY_DIR, name);
    if (!fs.existsSync(p)) throw new Error(`托管密钥不存在：${name}`);
    return p;
  }
  if (ref.startsWith('path:')) {
    const p = path.resolve(ref.slice(5).replace(/^~(?=$|\/)/, os.homedir()));
    if (!fs.existsSync(p)) throw new Error(`密钥文件不存在：${p}`);
    return p;
  }
  throw new Error(`密钥引用不合法：${ref}`);
}

async function describeKey(file) {
  const out = { fingerprint: '', type: '', bits: 0, encrypted: false, publicKey: '' };
  try {
    // -l 读的是私钥文件里明文保存的公钥部分，加密私钥也能拿到指纹
    const line = (await run(SSH_KEYGEN, ['-l', '-f', file])).trim();
    const m = line.match(/^(\d+)\s+(\S+)\s+.*\((\w+)\)$/);
    if (m) { out.bits = Number(m[1]); out.fingerprint = m[2]; out.type = m[3]; }
  } catch { /* 不是密钥文件 */ }
  try {
    out.publicKey = (await run(SSH_KEYGEN, ['-y', '-P', '', '-f', file])).trim();
  } catch {
    out.encrypted = true;   // -y 需要解密，失败说明带 passphrase
    try { out.publicKey = fs.readFileSync(file + '.pub', 'utf8').trim(); out.encrypted = true; } catch { /* 没有 .pub */ }
  }
  return out;
}

async function listKeys() {
  let names = [];
  try {
    names = fs.readdirSync(KEY_DIR).filter((f) => !f.endsWith('.pub') && NAME_RE.test(f));
  } catch { return []; }
  const out = [];
  for (const name of names) {
    const file = path.join(KEY_DIR, name);
    out.push({ name, ref: `managed:${name}`, ...(await describeKey(file)) });
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

async function generateKey(name, comment) {
  if (!NAME_RE.test(name)) throw new Error('密钥名只能用字母数字和 . _ -');
  const file = path.join(KEY_DIR, name);
  if (fs.existsSync(file)) throw new Error(`已存在同名密钥：${name}`);
  await run(SSH_KEYGEN, ['-t', 'ed25519', '-N', '', '-C', comment || `herdr-web@${os.hostname()}`, '-f', file]);
  fs.chmodSync(file, 0o600);
  return { name, ref: `managed:${name}`, ...(await describeKey(file)) };
}

async function importKey(name, pem) {
  if (!NAME_RE.test(name)) throw new Error('密钥名只能用字母数字和 . _ -');
  const file = path.join(KEY_DIR, name);
  if (fs.existsSync(file)) throw new Error(`已存在同名密钥：${name}`);
  const body = String(pem || '').replace(/\r\n/g, '\n').trim();
  if (!/^-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(body)) {
    throw new Error('这不像一个私钥（应该以 -----BEGIN ... PRIVATE KEY----- 开头）');
  }
  fs.writeFileSync(file, body + '\n', { mode: 0o600 });
  const info = await describeKey(file);
  if (!info.fingerprint) { fs.unlinkSync(file); throw new Error('ssh-keygen 认不出这个私钥'); }
  if (info.publicKey) fs.writeFileSync(file + '.pub', info.publicKey + '\n', { mode: 0o644 });
  return { name, ref: `managed:${name}`, ...info };
}

function removeKey(name) {
  if (!NAME_RE.test(name)) throw new Error('密钥名不合法');
  for (const p of [path.join(KEY_DIR, name), path.join(KEY_DIR, name + '.pub')]) {
    try { fs.unlinkSync(p); } catch { /* 不存在就算了 */ }
  }
}

/* ------------------------------------------------------------------ ~/.ssh 探测 */
// 列出 ~/.ssh 下现成的私钥，可以直接被主机以 path: 引用，不用重新导入
async function scanSshDir() {
  const dir = path.join(os.homedir(), '.ssh');
  let files = [];
  try { files = fs.readdirSync(dir); } catch { return []; }
  const out = [];
  for (const f of files) {
    if (f.endsWith('.pub') || ['config', 'known_hosts', 'known_hosts.old', 'authorized_keys', 'agent'].includes(f)) continue;
    const file = path.join(dir, f);
    try { if (!fs.statSync(file).isFile()) continue; } catch { continue; }
    const info = await describeKey(file);
    if (info.fingerprint) out.push({ name: f, ref: `path:${file}`, path: file, ...info });
  }
  return out;
}

// 解析 ~/.ssh/config 里的 Host 段，供一键导入
function parseSshConfig() {
  let text = '';
  try { text = fs.readFileSync(path.join(os.homedir(), '.ssh', 'config'), 'utf8'); } catch { return []; }
  const entries = [];
  let cur = null;
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const m = line.match(/^(\w+)\s+(.*)$/);
    if (!m) continue;
    const key = m[1].toLowerCase();
    const val = m[2].trim();
    if (key === 'host') {
      const alias = val.split(/\s+/)[0];
      cur = alias.includes('*') || alias.includes('?') ? null : { name: alias, alias };
      if (cur) entries.push(cur);
    } else if (cur) {
      if (key === 'hostname') cur.hostname = val;
      else if (key === 'user') cur.user = val;
      else if (key === 'port') cur.port = Number(val);
      else if (key === 'proxyjump') cur.jump = val;
    }
  }
  return entries;
}

/* ------------------------------------------------------------------ ssh 命令行 */
function sshArgsFor(host) {
  const a = ['-tt', '-o', 'ServerAliveInterval=30', '-o', 'ConnectTimeout=20'];
  // 默认不加 StrictHostKeyChecking：让 ssh 用原本的 ask 行为，
  // 指纹确认提示会直接出现在网页终端里，用户自己敲 yes。
  if (host.acceptNew) a.push('-o', 'StrictHostKeyChecking=accept-new');
  if (host.port) a.push('-p', String(host.port));
  if (host.jump) a.push('-J', host.jump);
  if (host.keyRef) a.push('-i', keyPathOf(host.keyRef), '-o', 'IdentitiesOnly=yes');
  a.push(host.user ? `${host.user}@${host.host}` : host.host);
  return a;
}

// ssh-copy-id：把托管公钥装到远端 authorized_keys，密码在网页终端里当场输
function copyIdArgsFor(host) {
  if (!host.keyRef) throw new Error('这台主机没有绑定密钥，先绑一个');
  const priv = keyPathOf(host.keyRef);
  const pub = priv + '.pub';
  if (!fs.existsSync(pub)) throw new Error(`找不到公钥文件 ${path.basename(pub)}`);
  const a = ['-i', pub];
  if (host.port) a.push('-p', String(host.port));
  if (host.jump) a.push('-o', `ProxyJump=${host.jump}`);
  a.push(host.user ? `${host.user}@${host.host}` : host.host);
  return a;
}

module.exports = {
  DIR, KEY_DIR, init,
  listHosts, getHost, addHost, updateHost, removeHost, touchHost,
  listKeys, generateKey, importKey, removeKey, keyPathOf,
  scanSshDir, parseSshConfig,
  sshArgsFor, copyIdArgsFor,
};
