# NexKV Makefile
# 提供 build、test、clean 等常用命令

.PHONY: all build test clean run fmt vet lint docker-build docker-run proto proto-clean help

# 变量定义
BINARY_NAME=nexkv
MAIN_PATH=./cmd/nexkv
GO=go
GOFLAGS=-v
LDFLAGS=-s -w

# Protobuf 配置
PROTO_DIR=./internal/metadata/proto
PROTOC=protoc
PROTO_OPTS=--go_out=. --go_opt=paths=source_relative

# 默认目标
all: build

## proto: 编译 Protobuf 文件
proto:
	@echo "编译 Protobuf 文件..."
	cd $(PROTO_DIR) && $(PROTOC) $(PROTO_OPTS) *.proto
	@echo "Protobuf 编译完成"

## build: 编译项目
build: proto
	@echo "编译 $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) $(MAIN_PATH)/main.go

## test: 运行所有测试
test:
	@echo "运行测试..."
	$(GO) test -v -race -coverprofile=coverage.out ./...
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
		golangci-lint run ./...; \
	else \
		echo "golangci-lint 未安装，跳过 lint"; \
	fi

## deps: 下载依赖
deps:
	@echo "下载依赖..."
	$(GO) mod download
	$(GO) mod tidy

## proto-clean: 清理生成的 Protobuf Go 代码
proto-clean:
	@echo "清理 Protobuf 生成的文件..."
	find $(PROTO_DIR) -name "*.pb.go" -delete
	@echo "Protobuf 清理完成"

## run: 运行程序
run: build
	@echo "运行 $(BINARY_NAME)..."
	./bin/$(BINARY_NAME) -config configs/config.yaml

## docker-build: 构建 Docker 镜像
docker-build:
	@echo "构建 Docker 镜像..."
	docker build -t nexkv:latest .

## docker-run: 运行 Docker 容器
docker-run:
	@echo "运行 Docker 容器..."
	docker run -p 9211:9211 nexkv:latest

## help: 显示帮助信息
help:
	@echo "可用命令:"
	@echo "  make build         - 编译项目"
	@echo "  make test          - 运行所有测试"
	@echo "  make test-verbose  - 运行详细测试"
	@echo "  make test-coverage - 运行测试并生成覆盖率报告"
	@echo "  make benchmark     - 运行性能基准测试"
	@echo "  make clean         - 清理编译文件"
	@echo "  make fmt           - 格式化代码并运行静态检查"
	@echo "  make vet           - 代码静态检查"
	@echo "  make lint          - 代码质量检查"
	@echo "  make deps          - 下载依赖"
	@echo "  make proto         - 编译 Protobuf 文件"
	@echo "  make proto-clean   - 清理 Protobuf 生成的文件"
	@echo "  make run           - 运行程序"
	@echo "  make docker-build  - 构建 Docker 镜像"
	@echo "  make docker-run    - 运行 Docker 容器"
	@echo "  make help          - 显示此帮助信息"
