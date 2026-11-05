# pixsy 构建脚本：前端产物 → 后端静态单文件（含 Linux/Windows 交叉编译）。
# 依赖：Go 1.22+、Node.js 18+（构建前端）；目标见 `make help`。

BINARY      := pixsy
CMD         := ./cmd/pixsy
WEB_DIR     := web
BIN_DIR     := bin
DIST_DIR    := deploy
# 与设计文档 §5.2 Dockerfile 的构建参数保持一致：去除路径信息、裁剪符号表。
BUILD_FLAGS := -trimpath -ldflags "-s -w"

.PHONY: help web build linux windows all package docker test vet clean

help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

web: ## 构建前端产物到 web/dist（go:embed 依赖，编译后端前必须存在）
	npm --prefix $(WEB_DIR) install
	npm --prefix $(WEB_DIR) run build

build: web ## 编译当前平台单文件（CGO_ENABLED=0）
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY) $(CMD)

linux: web ## 交叉编译 linux-amd64 / linux-arm64 静态单文件到 bin/
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) -o $(BIN_DIR)/$(BINARY)-linux-arm64 $(CMD)

windows: web ## 交叉编译 windows-amd64 单文件到 bin/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) -o $(BIN_DIR)/$(BINARY)-windows-amd64.exe $(CMD)

all: linux windows ## 构建全部分发产物

package: linux windows ## 打包 Linux(amd64/arm64)+Windows(amd64) 安装包到 deploy/
	go run ./scripts/package

docker: ## 构建 Docker 镜像（多阶段构建，容器内完成前端与后端编译）
	docker build -t $(BINARY) .

test: ## 运行后端测试
	go test ./...

vet: ## 静态检查
	go vet ./...

clean: ## 清理构建产物
	rm -rf $(BIN_DIR) $(BINARY) $(BINARY).exe
	rm -f $(DIST_DIR)/*.tar.gz $(DIST_DIR)/*.zip
