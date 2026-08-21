#!/bin/sh
# herdr-web 安装脚本。给「机器上没有 node」的情况用（服务器上很常见）；
# 有 node 的话 npm install -g @bysir/herdr-web 更省事，升级也交给 npm。
#
#   curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
#
# 环境变量：
#   HERDR_WEB_INSTALL_DIR   装到哪（默认 ~/.local/bin）
#   HERDR_WEB_INSTALL_VER   装哪一版（默认最新，写法 v1.2.3）
#
# 管道用法下变量要给 sh，不是给 curl —— 给错了不报错，只是静默用默认值：
#   curl -fsSL …/install.sh | HERDR_WEB_INSTALL_DIR=/opt/bin sh    # 对
#   HERDR_WEB_INSTALL_DIR=/opt/bin curl -fsSL …/install.sh | sh    # 错
set -eu

REPO=zbysir/herdr-web
DIR=${HERDR_WEB_INSTALL_DIR:-$HOME/.local/bin}

say() { printf '  %s\n' "$*"; }
die() { printf '  ✗ %s\n' "$*" >&2; exit 1; }

# ---- 平台 ----
os=$(uname -s)
case "$os" in
  Darwin) goos=darwin ;;
  Linux)  goos=linux ;;
  MINGW*|MSYS*|CYGWIN*)
    die "Windows 原生环境跑不了（终端要 PTY，herdr 走 unix socket）。在 WSL 里装。" ;;
  *) die "没有 $os 的预编译包。自己编：go install github.com/$REPO/cmd/herdr-web@latest" ;;
esac
case $(uname -m) in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) die "没有 $(uname -m) 的预编译包。自己编：go install github.com/$REPO/cmd/herdr-web@latest" ;;
esac

command -v curl >/dev/null 2>&1 || die "需要 curl"
command -v tar  >/dev/null 2>&1 || die "需要 tar"

# ---- 版本 ----
ver=${HERDR_WEB_INSTALL_VER:-}
if [ -z "$ver" ]; then
  say "查最新版本…"
  # 不用 jq：装脚本不该要求额外工具
  ver=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$ver" ] || die "查不到最新版本（$REPO 发过 release 吗？）"
fi
num=${ver#v}

name="herdr-web_${num}_${goos}_${goarch}.tar.gz"
base="https://github.com/$REPO/releases/download/$ver"
say "版本  $ver"
say "平台  $goos/$goarch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "下载  $name"
curl -fSL --progress-bar "$base/$name" -o "$tmp/$name" || die "下载失败：$base/$name"

# ---- 校验 ----
# 校验是刻意不做成可选的：这东西装上去之后后面挂着一个登录 shell。
if command -v sha256sum >/dev/null 2>&1; then sum() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum   >/dev/null 2>&1; then sum() { shasum -a 256 "$1" | cut -d' ' -f1; }
else die "找不到 sha256sum / shasum，没法校验下载 —— 不装"; fi

curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || die "下载 checksums.txt 失败"
want=$(grep " \*\{0,1\}$name\$" "$tmp/checksums.txt" | cut -d' ' -f1 | head -1)
[ -n "$want" ] || die "checksums.txt 里没有 $name"
got=$(sum "$tmp/$name")
[ "$got" = "$want" ] || die "sha256 不对（算出 $got，应当是 $want）"
say "校验  ok"

# ---- 装 ----
tar -xzf "$tmp/$name" -C "$tmp"
[ -f "$tmp/herdr-web" ] || die "archive 里没有 herdr-web"
mkdir -p "$DIR"
# 先写临时文件再 mv：mv 是原子的，中途失败不会留下半截二进制
cp "$tmp/herdr-web" "$DIR/.herdr-web.new"
chmod 755 "$DIR/.herdr-web.new"
mv "$DIR/.herdr-web.new" "$DIR/herdr-web"
say "装到  $DIR/herdr-web"

echo
case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    say "⚠️  $DIR 不在 PATH 里。加一行到 ~/.zshrc 或 ~/.bashrc："
    say "    export PATH=\"$DIR:\$PATH\""
    echo ;;
esac
say "接下来："
say "  herdr-web                     直接跑（只听 127.0.0.1）"
say "  herdr-web service install     装成开机自启的常驻服务"
say "  herdr-web update              升级"
echo
