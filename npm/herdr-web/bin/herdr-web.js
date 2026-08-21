#!/usr/bin/env node
// 这个壳只做一件事：找到当前平台的那个二进制，然后把自己换成它。
//
// 为什么不用 postinstall 去下载：npm 的 provenance 只能保证「这个包是那个 CI 构建的」，
// 保证不了 postinstall 从网上抓回来的东西。二进制走 optionalDependencies 里的平台子包，
// npm 按 os/cpu 只装匹配的那一个 —— 校验、缓存、离线安装全都是 npm 自己的机制。
"use strict";

const { spawnSync } = require("node:child_process");

const PLATFORMS = {
  "darwin-arm64": "@bysir/herdr-web-darwin-arm64",
  "darwin-x64": "@bysir/herdr-web-darwin-x64",
  "linux-arm64": "@bysir/herdr-web-linux-arm64",
  "linux-x64": "@bysir/herdr-web-linux-x64",
};

function resolveBinary() {
  const key = `${process.platform}-${process.arch}`;

  // Windows 单独说清楚。发不了不是懒：终端要 PTY（Go 那边用 creack/pty，windows 上是
  // 个空壳），herdr 自己也走 unix socket。在 WSL 里装就是 linux 版，一切正常。
  if (process.platform === "win32") {
    console.error(
      [
        "herdr-web 不支持 Windows 原生环境。",
        "",
        "  原因：浏览器里那个终端需要一个真 PTY，herdr 之间通信走的是 unix socket，",
        "  这两样 Windows 原生都没有。",
        "",
        "  在 WSL 里装（那边就是 Linux 版，功能完整）：",
        "    wsl",
        "    npm install -g @bysir/herdr-web",
        "",
        "  装好之后 Windows 上的浏览器直接开 http://localhost:7788/ 就能用 ——",
        "  客户端是浏览器，本来就跨平台。",
      ].join("\n"),
    );
    process.exit(1);
  }

  const pkg = PLATFORMS[key];
  if (!pkg) {
    console.error(
      `herdr-web 没有 ${key} 的预编译包。\n` +
        `  自己编：go install github.com/zbysir/herdr-web/cmd/herdr-web@latest`,
    );
    process.exit(1);
  }

  try {
    return require.resolve(`${pkg}/bin/herdr-web`);
  } catch {
    console.error(
      [
        `装不上 ${key} 的二进制（${pkg} 没找到）。`,
        "",
        "  常见原因：装的时候带了 --no-optional 或 --omit=optional，",
        "  平台子包被跳过了。重装一次：",
        `    npm install -g @bysir/herdr-web --include=optional`,
      ].join("\n"),
    );
    process.exit(1);
  }
}

const binary = resolveBinary();
const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });

if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
// 被信号打断时把信号原样反映出来（Ctrl-C 该表现成 Ctrl-C，不该是 exit 0）
if (result.signal) {
  process.kill(process.pid, result.signal);
}
process.exit(result.status === null ? 1 : result.status);
