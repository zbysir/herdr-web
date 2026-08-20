# 单二进制：前端产物拷进 internal/webui/dist，go build 用 embed 吃进去。
BIN     := herdr-web
WEBDIST := internal/webui/dist

.PHONY: all build web go run dev test clean

all: build

## build —— 出一个自带前端的二进制
build: web go

web:
	npm --prefix web ci --silent 2>/dev/null || npm --prefix web install --silent
	npm --prefix web run build
	rm -rf $(WEBDIST)
	mkdir -p $(WEBDIST)
	cp -R web/dist/. $(WEBDIST)/

go:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/herdr-web
	@echo "→ ./$(BIN)  ($$(du -h $(BIN) | cut -f1))"

run: build
	./$(BIN)

## dev —— 前端热更新；后端单独跑，vite 把 /api 和 /pty 转过去
dev:
	@echo "另开一个终端跑：go run ./cmd/herdr-web"
	npm --prefix web run dev

test:
	go test ./...
	npm --prefix web run typecheck

clean:
	rm -f $(BIN)
	rm -rf web/dist $(WEBDIST)
	mkdir -p $(WEBDIST)
	@echo "占位：make build 会把 web/dist 拷到这里" > $(WEBDIST)/.gitkeep
