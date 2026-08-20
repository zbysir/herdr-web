'use strict';
/**
 * 图片上传：手机把图存到跑 herdr 的那台机器上，然后**把绝对路径当文本投给 agent**。
 *
 * 为什么是这条路：herdr 的 socket API 里没有任何图片概念，能投的只有文本。而
 * claude 和 codex 都能直接读磁盘上的图片文件（实测：给一张 320×200 左红右蓝中间
 * 绿带的 PNG，两边都描述对了，codex 还会打一行 `Viewed Image`）。所以「上传」＝
 * 落盘 + 在提示词里带上路径，跟 HANDOFF 里「长文本写临时文件」是同一个套路。
 */
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const store = require('./store');

const DIR = path.join(store.DIR, 'uploads');
const MAX_BYTES = 25 * 1024 * 1024;

// 按魔数认类型，不信客户端给的 content-type 和文件名 —— 那两个都是随便填的。
// 只收 agent 真读得懂的那几种。
const KINDS = [
  { ext: 'png', ok: (b) => b.length > 8 && b.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) },
  { ext: 'jpg', ok: (b) => b.length > 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff },
  { ext: 'gif', ok: (b) => b.length > 6 && ['GIF87a', 'GIF89a'].includes(b.subarray(0, 6).toString('latin1')) },
  { ext: 'webp', ok: (b) => b.length > 12 && b.subarray(0, 4).toString('latin1') === 'RIFF' && b.subarray(8, 12).toString('latin1') === 'WEBP' },
];

// iPhone 直接给的 HEIC，agent 读不了；前端会先用 canvas 转成 PNG/JPEG，
// 转不了才会原样传上来，这里给一句能看懂的错，而不是"不认识的类型"。
const isHeic = (b) => b.length > 12 && b.subarray(4, 8).toString('latin1') === 'ftyp'
  && /heic|heif|mif1|msf1/.test(b.subarray(8, 12).toString('latin1'));

function init() {
  fs.mkdirSync(DIR, { recursive: true, mode: 0o700 });
  fs.chmodSync(DIR, 0o700);
}

const stamp = () => {
  const d = new Date();
  const p = (n, w = 2) => String(n).padStart(w, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
};

/**
 * 落盘，返回可以直接塞进提示词的绝对路径。
 * @param {Buffer} buf 原始字节
 */
function save(buf) {
  if (!buf || !buf.length) throw new Error('空文件');
  if (buf.length > MAX_BYTES) throw new Error(`图片太大（${(buf.length / 1048576).toFixed(1)} MB，上限 25 MB）`);

  const kind = KINDS.find((k) => k.ok(buf));
  if (!kind) {
    throw new Error(isHeic(buf)
      ? 'HEIC 图 agent 读不了，而这台浏览器也没能把它转成 PNG。到相册里导出成 JPEG 再传。'
      : '不认识这个文件类型，只收 png / jpg / gif / webp');
  }

  init();
  const name = `${stamp()}-${crypto.randomBytes(3).toString('hex')}.${kind.ext}`;
  const file = path.join(DIR, name);
  fs.writeFileSync(file, buf, { mode: 0o600 });
  return { path: file, name, bytes: buf.length, kind: kind.ext, dir: DIR };
}

module.exports = { save, init, DIR, MAX_BYTES };
