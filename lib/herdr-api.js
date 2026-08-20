'use strict';
/**
 * herdr socket API 客户端。
 *
 * 换行分隔 JSON，请求 `{id, method, params}`，响应 `{id, result}` 或 `{id, error:{code,message}}`。
 *
 * 服务端**一个连接只处理一个请求**，不支持 pipeline（同连接塞两个请求，只回第一个的
 * 响应，第二个直接丢）。所以每次调用都开一条新连接 —— unix socket 单次往返亚毫秒，
 * 不值得做连接池，而顺序 await 天然保证了「先清空再提交」的次序。
 */
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const DEFAULT_SOCK = path.join(os.homedir(), '.config', 'herdr', 'herdr.sock');

// server.js 的 DROP_ENV 会把 HERDR_* 从 PTY 环境里清掉（防嵌套启动），而 server 进程
// 自己也可能根本不是从 herdr pane 里起的 —— 所以别依赖 HERDR_SOCKET_PATH 存在，
// 解析不到就退回默认路径。
function socketPath() {
  return process.env.HERDR_WEB_SOCKET || process.env.HERDR_SOCKET_PATH || DEFAULT_SOCK;
}

class HerdrError extends Error {
  constructor(message, code) {
    super(message);
    this.name = 'HerdrError';
    this.code = code;
  }
}

function call(method, params = {}, { timeout = 10000 } = {}) {
  return new Promise((resolve, reject) => {
    const sock = socketPath();
    const s = net.connect(sock);
    let buf = '';
    let done = false;

    const finish = (err, val) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      s.destroy();
      if (err) reject(err); else resolve(val);
    };
    const timer = setTimeout(
      () => finish(new HerdrError(`herdr 无响应（${timeout}ms 超时）`, 'timeout')),
      timeout,
    );

    s.setEncoding('utf8');
    s.on('connect', () => s.write(JSON.stringify({ id: 'web', method, params }) + '\n'));
    s.on('data', (chunk) => {
      buf += chunk;
      const nl = buf.indexOf('\n');
      if (nl < 0) return;
      let msg;
      try { msg = JSON.parse(buf.slice(0, nl)); }
      catch { return finish(new HerdrError('herdr 返回的不是合法 JSON', 'bad_json')); }
      if (msg.error) return finish(new HerdrError(msg.error.message || String(msg.error), msg.error.code));
      finish(null, msg.result);
    });
    s.on('end', () => finish(new HerdrError('herdr 关闭了连接但没给响应', 'no_response')));
    s.on('error', (e) => finish(new HerdrError(
      e.code === 'ENOENT' ? `herdr socket 不存在：${sock}（herdr server 没在跑？）`
        : e.code === 'ECONNREFUSED' ? `herdr socket 拒绝连接：${sock}`
          : e.code === 'EACCES' ? `没权限连 herdr socket：${sock}`
            : `herdr socket 错误：${e.message}`,
      e.code,
    )));
  });
}

module.exports = { call, socketPath, HerdrError, DEFAULT_SOCK };
