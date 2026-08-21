#!/usr/bin/env node
// 发 npm。**顺序有意义**：平台子包必须先发，根包最后发 —— 反了的话根包已经在注册表上
// 但它的 optionalDependencies 还不存在，那段时间里 npm install 会装出一个没有二进制的壳。
//
// 用法：node scripts/npm-publish.mjs [--dry-run]

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const out = path.join(root, "npm");
const dry = process.argv.includes("--dry-run");

const dirs = fs
  .readdirSync(out, { withFileTypes: true })
  .filter((e) => e.isDirectory() && fs.existsSync(path.join(out, e.name, "package.json")))
  .map((e) => e.name)
  // 根包（herdr-web）排最后
  .sort((a, b) => (a === "herdr-web" ? 1 : b === "herdr-web" ? -1 : a.localeCompare(b)));

for (const d of dirs) {
  const dir = path.join(out, d);
  const { name, version } = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
  console.log(`\n=== npm publish ${name}@${version} ===`);
  const args = ["publish", "--access", "public"];
  // provenance 要 CI 的 OIDC token，本地跑不了。不判断的话本地 dry-run 直接报错。
  if (process.env.CI) args.push("--provenance");
  // 预发布版本（1.0.0-rc1）必须显式给 dist-tag，npm 不许它们默认占住 latest。
  // 不加这一条的话 `make release V=v1.0.0-rc1` 会在最后一步挂掉。
  if (version.includes("-")) args.push("--tag", "next");
  if (dry) args.push("--dry-run");
  execFileSync("npm", args, { cwd: dir, stdio: "inherit" });
}
