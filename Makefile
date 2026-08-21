# 单二进制：前端产物拷进 internal/webui/dist，go build 用 embed 吃进去。
BIN     := herdr-web
WEBDIST := internal/webui/dist

# 部署配置放在 .env 里（不入版本库）。没有这个文件就用默认值：只听 127.0.0.1、
# 不需要 TLS，够本机自己用。要开局域网或公网看 DEPLOY.md。
# 默认值带着 ./ 是有意的：`.` 这个内建命令在路径不含斜杠时会去 PATH 里找文件。
ENVFILE ?= ./.env

.PHONY: all build web go run run-go dev dev-server test clean release release-dry

all: build

## build —— 出一个自带前端的二进制
build: web go

# GITKEEP 是 internal/webui/dist/.gitkeep 的内容，写在这儿而不是靠 git 取：
# make web 里那个 rm -rf 会删掉它，而它是**入库**的文件 —— 少了它，新 clone 出来的
# 仓库里 dist/ 是空目录，go:embed all:dist 在空目录上直接是编译错误
# （pattern all:dist: no matching files found），CI 和贡献者第一步就卡住。
# 不用 `git show HEAD:...` 是因为那在「刚提交完删除」和「没有 .git 的源码包」里都取不到。
define GITKEEP
占位。`make build` 会把 web/dist 的内容拷到这个目录。

这个文件必须入库：internal/webui/embed.go 的 `go:embed all:dist` 在目录完全为空时是
编译错误，于是新 clone 出来的仓库连 `go build ./...` 都跑不过。有它在，没构建前端也能编，
启动时会提示前端产物缺失。
endef
export GITKEEP

web:
	npm --prefix web ci --silent 2>/dev/null || npm --prefix web install --silent
	npm --prefix web run build
	rm -rf $(WEBDIST)
	mkdir -p $(WEBDIST)
	cp -R web/dist/. $(WEBDIST)/
	@printf '%s\n' "$$GITKEEP" > $(WEBDIST)/.gitkeep

# 版本号：本机构建从 git 取一个描述性的值。**发版不走这里**（走 goreleaser，
# 它注入的是 tag）—— 这里只是为了让 `herdr-web version` 在开发时也有意义。
# 取不到 tag 时是 "dev"，selfupdate 见到 dev 会跳过查更新，正合适。
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
PKG     := github.com/zbysir/herdr-web/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)

go:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/herdr-web
	@echo "→ ./$(BIN)  $(VERSION)  ($$(du -h $(BIN) | cut -f1))"

## run —— 构建 + 起服务，配置从 .env 读
run: build launch

## run-go —— 只重编 Go 再起（前端没动的时候用这个，省掉 npm 那二十秒）
run-go: go launch

# 用 shell 的 `set -a` 而不是 make 的 include：.env 是 shell 语法，引号和带 # 的值
# 都能正常处理，而且不会把 make 自己的变量一起塞进环境。
.PHONY: launch
launch:
	@if [ -f "$(ENVFILE)" ]; then \
	  echo "→ 配置：$(ENVFILE)"; \
	else \
	  echo "→ 没有 $(ENVFILE)，用默认值（只听 127.0.0.1）。"; \
	  echo "  要开局域网 / 公网：cp .env.example .env 改一改，说明见 DEPLOY.md"; \
	fi
	@set -a; [ -f "$(ENVFILE)" ] && . "$(ENVFILE)"; set +a; exec ./$(BIN)

## dev —— 前端热更新（后端另开一个 make dev-server）
dev:
	@echo "另开一个终端跑：make dev-server"
	npm --prefix web run dev

## dev-server —— 不打包前端，直接 go run（同样读 .env）
dev-server:
	@set -a; [ -f "$(ENVFILE)" ] && . "$(ENVFILE)"; set +a; exec go run ./cmd/herdr-web

test:
	go test ./...
	npm --prefix web run typecheck

## release-dry —— 本地把整条发版链跑一遍，不推任何东西
# 干跑能抓到的：goreleaser 配置错、交叉编译不过、npm 包缺文件。抓不到的只有
# 「注册表拒绝」这一类，那个只能真发才知道。
release-dry:
	goreleaser release --snapshot --clean --skip=publish
	@V=$$(ls dist/herdr-web_*_darwin_arm64.tar.gz | sed -E 's/.*herdr-web_(.+)_darwin_arm64.*/\1/'); \
	 echo "→ 打 npm 包：$$V"; \
	 node scripts/npm-build.mjs "$$V"
	node scripts/npm-publish.mjs --dry-run
# 干跑必须**把工作区还回去**：npm-build.mjs 会把版本号写进入库的
# npm/herdr-web/package.json（干跑时是 `0.1.1-next` 这种快照号）。不还回去有两个后果，
# 都很烦：紧接着 `make release` 会说「工作区不干净」（而你什么都没改），或者那个 -next
# 版本号被顺手提交进去。CI 里没有 .git 的话这一句静默跳过，正合适。
	@git checkout -- npm/herdr-web/package.json 2>/dev/null || true
	@echo "→ 干跑完了，工作区已还原（npm/herdr-web/package.json）"

## release —— 打 tag 并推上去，剩下的 GitHub Actions 干（见 .github/workflows/release.yml）
# tag 要推到**装着 release.yml 的那个远端**，也就是 GitHub。
#
# **不能写死 origin**：这个仓库的 origin 指向自建 git（git.huglight.cn），GitHub 是另一个
# 叫 github 的远端。推错了的表现最难查 —— tag 打上去了、命令也成功了，Actions 那边一直
# 没动静，而「没动静」和「还在排队」长得一模一样。所以这里按 push URL 里的 github.com 认，
# 认不出来就直接拒绝发版，不猜。要覆盖：make release V=... RELEASE_REMOTE=xxx
RELEASE_REMOTE ?= $(shell git remote -v | awk '/github\.com.*\(push\)/{print $$1; exit}')

release:
	@test -n "$(V)" || { echo "用法：make release V=v0.1.0"; exit 1; }
	@test -n "$(RELEASE_REMOTE)" || { echo "找不到指向 github.com 的远端（release.yml 在那儿）。用 RELEASE_REMOTE= 指一个"; exit 1; }
	@git diff --quiet HEAD || { echo "工作区不干净，先提交"; exit 1; }
	@echo "→ 远端：$(RELEASE_REMOTE)  $$(git remote get-url $(RELEASE_REMOTE))"
	git tag -a "$(V)" -m "$(V)"
	git push "$(RELEASE_REMOTE)" "$(V)"
	@echo "→ 推上去了。进度：https://github.com/zbysir/herdr-web/actions"

clean:
	rm -f $(BIN)
	rm -rf web/dist $(WEBDIST) dist
	rm -rf npm/herdr-web-*  npm/herdr-web/README.md
	mkdir -p $(WEBDIST)
	@printf '%s\n' "$$GITKEEP" > $(WEBDIST)/.gitkeep
