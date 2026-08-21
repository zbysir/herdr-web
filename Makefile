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

web:
	npm --prefix web ci --silent 2>/dev/null || npm --prefix web install --silent
	npm --prefix web run build
	rm -rf $(WEBDIST)
	mkdir -p $(WEBDIST)
	cp -R web/dist/. $(WEBDIST)/

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

## release —— 打 tag 并推上去，剩下的 GitHub Actions 干（见 .github/workflows/release.yml）
release:
	@test -n "$(V)" || { echo "用法：make release V=v0.1.0"; exit 1; }
	@git diff --quiet || { echo "工作区不干净，先提交"; exit 1; }
	git tag -a "$(V)" -m "$(V)"
	git push origin "$(V)"
	@echo "→ 推上去了。进度：https://github.com/zbysir/herdr-web/actions"

clean:
	rm -f $(BIN)
	rm -rf web/dist $(WEBDIST) dist
	rm -rf npm/herdr-web-*  npm/herdr-web/README.md
	mkdir -p $(WEBDIST)
	@echo "占位：make build 会把 web/dist 拷到这里" > $(WEBDIST)/.gitkeep
