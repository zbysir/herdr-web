'use strict';
// 启动时在终端画个二维码，手机扫一下就不用手打 token 了。
const qrcode = require('qrcode-terminal');

function render(text) {
  let out = null;
  // qrcode-terminal 的 callback 是同步调用的
  qrcode.generate(text, { small: true }, (s) => { out = s; });
  return out ? out.replace(/\n+$/, '').split('\n') : null;
}

module.exports = { render };
