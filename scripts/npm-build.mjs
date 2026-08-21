#!/usr/bin/env node
// 把 goreleaser 的产物（dist/*.tar.gz）摊成可发布的 npm 包，写到 npm/ 下面。
//
// 出来的东西：
//   npm/herdr-web/                        根包 @bysir/herdr-web（只有一个 JS 壳）
//   npm/herdr-web-<platform>-<arch>/      每个平台一个子包，里面就一个二进制
//
// 为什么拆包而不是一个胖包全塞进去：一个二进制 16MB（前端 embed 进去了），四个平台
// 塞一起是 60 多兆，而每个用户只用得上其中一个。npm 的 os/cpu 字段能让它只下匹配的那个。
//
// 用法：node scripts/npm-build.mjs <version>
//       版本号不带前导 v。CI 里从 tag 传进来。

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const dist = path.join(root, "dist");
const out = path.join(root, "npm");

const version = (process.argv[2] || "").replace(/^v/, "");
if (!/^\d+\.\d+\.\d+/.test(version)) {
  console.error(`用法: node scripts/npm-build.mjs <version>   （拿到的是 ${JSON.stringify(process.argv[2])}）`);
  process.exit(1);
}

// npm 的 platform-arch 和 Go 的 GOOS-GOARCH 名字对不上（x64 vs amd64），这张表是唯一映射处
const TARGETS = [
  { goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
];

const SCOPE = "@bysir";
const rootPkgDir = path.join(out, "herdr-web");

function subPkgName(t) {
  return `${SCOPE}/herdr-web-${t.os}-${t.cpu}`;
}

// 1) 每个平台一个子包
const optional = {};
for (const t of TARGETS) {
  const archive = path.join(dist, `herdr-web_${version}_${t.goos}_${t.goarch}.tar.gz`);
  if (!fs.existsSync(archive)) {
    throw new Error(`缺少 release archive: ${archive}\n（先跑 goreleaser，或者 make release-dry）`);
  }

  const dir = path.join(out, `herdr-web-${t.os}-${t.cpu}`);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(path.join(dir, "bin"), { recursive: true });

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "herdr-web-npm-"));
  try {
    execFileSync("tar", ["-xzf", archive, "-C", tmp], { stdio: "inherit" });
    const src = path.join(tmp, "herdr-web");
    if (!fs.existsSync(src)) throw new Error(`${archive} 里没有 herdr-web`);
    const dest = path.join(dir, "bin", "herdr-web");
    fs.copyFileSync(src, dest);
    // 可执行位必须自己设：npm 打包时保留文件模式，但 copyFileSync 不一定带过来
    fs.chmodSync(dest, 0o755);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }

  fs.writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify(
      {
        name: subPkgName(t),
        version,
        description: `herdr-web 的 ${t.os}-${t.cpu} 二进制（由 @bysir/herdr-web 自动选用）`,
        license: "MIT",
        repository: { type: "git", url: "git+https://github.com/zbysir/herdr-web.git" },
        // 这两个字段是关键：npm 靠它们判断「这个包该不该在这台机器上装」
        os: [t.os],
        cpu: [t.cpu],
        files: ["bin/"],
        preferUnplugged: true, // Yarn PnP：二进制不能被塞进 zip，得摊在磁盘上
      },
      null,
      2,
    ) + "\n",
  );
  optional[subPkgName(t)] = version;
  const mb = (fs.statSync(path.join(dir, "bin", "herdr-web")).size / 1048576).toFixed(1);
  console.log(`  ${subPkgName(t)}  ${mb} MB`);
}

// 2) 根包：填版本号 + optionalDependencies，再把 README 带上
const pkgPath = path.join(rootPkgDir, "package.json");
const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8"));
pkg.version = version;
// 精确钉死版本：范围写法会让 npm 有机会给根包配上别的版本的二进制
pkg.optionalDependencies = optional;
fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
fs.copyFileSync(path.join(root, "README.md"), path.join(rootPkgDir, "README.md"));
console.log(`  ${pkg.name}  ${version}（壳）`);
