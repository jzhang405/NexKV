# NexKV Makefile
# 提供 build、test、clean 等常用命令

.PHONY: all build test clean run fmt vet lint docker-build docker-run help version

# 变量定义
BINARY_NAME=nexkv
DAEMON_NAME=nexkvd
NEXKV_PATH=./cmd/nexkv
NEXKVD_PATH=./cmd/nexkvd
GO=go
GOFLAGS=-v

# 版本信息（构建时注入）
VERSION ?= 0.0.1
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%S 2>/dev/null || echo "unknown")

# nexkv 版本信息（变量在 main 包中，通过 SetVersionInfo 传递给 commands 包）
NEXKV_LDFLAGS := -s -w \
	-X 'main.Version=$(VERSION)' \
	-X 'main.GitCommit=$(GIT_COMMIT)' \
	-X 'main.BuildTime=$(BUILD_TIME)'

# nexkvd 版本信息（变量在 main 包中）
NEXKVD_LDFLAGS := -s -w \
	-X 'main.Version=$(VERSION)' \
	-X 'main.GitCommit=$(GIT_COMMIT)' \
	-X 'main.BuildTime=$(BUILD_TIME)'

# 默认目标
all: build

## build: 编译项目（nexkv 和 nexkvd）
build:
	@echo "编译 $(BINARY_NAME) 和 $(DAEMON_NAME)..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -ldflags "$(NEXKV_LDFLAGS)" -o bin/$(BINARY_NAME) $(NEXKV_PATH)/main.go
	$(GO) build $(GOFLAGS) -ldflags "$(NEXKVD_LDFLAGS)" -o bin/$(DAEMON_NAME) $(NEXKVD_PATH)/main.go

## test: 运行所有测试
test:
	@echo "运行测试..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## test-race: 运行带竞态检测的测试
test-race:
	@echo "运行带竞态检测的测试..."
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## test-verbose: 运行详细测试
test-verbose:
	@echo "运行详细测试..."
	$(GO) test -v ./...

## test-coverage: 运行测试并生成覆盖率报告
test-coverage:
	@echo "运行测试并生成覆盖率报告..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

## benchmark: 运行性能基准测试
benchmark:
	@echo "运行性能基准测试..."
	$(GO) test -bench=. -benchmem ./...

## clean: 清理编译文件
clean:
	@echo "清理编译文件..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	rm -rf data/

## fmt: 格式化代码并运行静态检查
fmt:
	@echo "格式化代码..."
	$(GO) fmt ./...
	@echo "运行 go vet..."
	$(GO) vet ./...

## vet: 代码静态检查
vet:
	@echo "运行 go vet..."
	$(GO) vet ./...

## lint: 代码质量检查
lint:
	@echo "运行 golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run  ./...; \
	else \
		echo "golangci-lint 未安装，跳过 lint"; \
	fi

## deps: 下载依赖
deps:
	@echo "下载依赖..."
	$(GO) mod download
	$(GO) mod tidy

## run: 运行 nexkv 客户端
run: build
	@echo "运行 $(BINARY_NAME)..."
	./bin/$(BINARY_NAME) --addr 127.0.0.1:9211

## run-daemon: 运行 nexkvd 守护进程
run-daemon: build
	@echo "运行 $(DAEMON_NAME)..."
	./bin/$(DAEMON_NAME) --config configs/config.yaml --env dev

## docker-build: 构建 Docker 镜像
docker-build:
	@echo "构建 Docker 镜像..."
	docker build -t nexkv:latest .

## docker-run: 运行 Docker 容器
docker-run:
	@echo "运行 Docker 容器..."
	docker run -p 9211:9211 nexkv:latest

## version: 显示构建版本信息
version:
	@echo "版本信息:"
	@echo "  VERSION:     $(VERSION)"
	@echo "  GIT_COMMIT:  $(GIT_COMMIT)"
	@echo "  BUILD_TIME:  $(BUILD_TIME)"
	@echo ""
	@echo "提示: 可以通过 VERSION 变量覆盖版本号"
	@echo "  示例: make build VERSION=1.0.0"

## help: 显示帮助信息
help:
	@echo "可用命令:"
	@echo "  make build         - 编译项目（nexkv 和 nexkvd）"
	@echo "  make test          - 运行所有测试"
	@echo "  make test-race     - 运行带竞态检测的测试"
	@echo "  make test-verbose  - 运行详细测试"
	@echo "  make test-coverage - 运行测试并生成覆盖率报告"
	@echo "  make benchmark     - 运行性能基准测试"
	@echo "  make clean         - 清理编译文件"
	@echo "  make fmt           - 格式化代码并运行静态检查"
	@echo "  make vet           - 代码静态检查"
	@echo "  make lint          - 代码质量检查"
	@echo "  make deps          - 下载依赖"
	@echo "  make run           - 运行 nexkv 客户端"
	@echo "  make run-daemon    - 运行 nexkvd 守护进程"
	@echo "  make docker-build  - 构建 Docker 镜像"
	@echo "  make docker-run    - 运行 Docker 容器"
	@echo "  make version       - 显示构建版本信息"
	@echo "  make help          - 显示此帮助信息"
	@echo ""
	@echo "版本信息覆盖:"
	@echo "  make build VERSION=1.0.0  - 使用指定版本号构建"
