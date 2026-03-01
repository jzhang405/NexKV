# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

# NexKV 项目指南

> **轻量化分布式 KV 存储系统**
> 面向中小规模集群（3-100 节点）的去中心化分布式数据库

---

## 核心特性

- **单机-分布式一体**：一套架构同时支持单机和分布式部署，运行时无缝切换
- **分层一致性**：关键变更强一致（Quorum），普通变更最终一致（Gossip）
- **分片自治**：每个分片独立管理副本、事务与故障恢复
- **无中心化**：无单点故障，所有节点地位平等
- **轻量化部署**：无外部依赖（ZooKeeper/Etcd），元数据本地存储

---

## 常用命令

### 构建

```bash
make build              # 编译 nexkv 和 nexkvd
make build VERSION=1.0.0  # 指定版本号构建
```

### 测试

```bash
# 完整测试流程（提交前必须全部通过）
make test               # 单元测试 + 集成测试
make test-unit          # 单元测试（快速反馈，-short 标志）
make integration-test-race  # 集成测试 + 竞态检测

# 单个测试运行
go test -v -run TestHLCClock_Compare ./internal/domain/model/
go test -v -race ./internal/infrastructure/concurrency/...

# 性能测试
make benchmark          # 运行所有基准测试
go test -bench=. -benchmem ./path/to/package
```

### 代码质量

```bash
# 提交前验证（必须全部通过）
make lint               # golangci-lint 代码质量检查
make fmt                # 格式化代码 + go vet
make vet                # 静态检查

# 一键验证
make lint && make build && make test && make fmt && echo "✅ 所有验证通过"
```

### 运行

```bash
make run                # 运行 nexkv 客户端
make run-daemon         # 运行 nexkvd 守护进程
./bin/nexkvd            # 直接运行守护进程
```

### 清理

```bash
make clean              # 清理所有编译产物
```

---

## 代码库结构

```
NexKV/
├── cmd/                        # 应用入口
│   ├── nexkv/                  # 客户端 CLI
│   └── nexkvd/                 # 服务器守护进程
│
├── internal/                   # 内部包（Clean Architecture + DDD）
│   ├── application/            # 应用层
│   │   └── clock/              # 时钟服务
│   ├── domain/                 # 领域层
│   │   ├── event/              # 领域事件（任务事件）
│   │   ├── model/              # 领域模型（Task, HLC, Peer, Address, Cron）
│   │   └── service/            # 领域服务接口定义
│   └── infrastructure/         # 基础设施层
│       ├── clock/              # HLC 时钟实现
│       ├── concurrency/        # 并发执行器（统一任务调度器）
│       ├── discovery/          # 节点发现
│       ├── id/                 # ID 生成器
│       ├── rpc/                # RPC 通信
│       └── transport/          # libp2p 传输层
│
├── pkg/                        # 公共库（可被外部使用）
│   ├── compressor/             # 压缩工具
│   ├── errors/                 # 错误类型定义
│   └── test/                   # 测试工具
│
├── test/                       # 测试目录
│   └── integration/            # 集成测试场景
│       └── scenarios/          # 集成测试用例
│
├── docs/                       # 文档目录
│   ├── workflow.md             # 📋 PR 工作流程规范（12 步）- **必读**
│   ├── CLAUDE.MD               # 文档中心索引
│   ├── 00_overview/            # 核心架构概念
│   ├── 02_design/              # 架构设计文档
│   ├── 03_development/         # 开发规范
│   ├── 04_test/                # 测试文档
│   ├── 06_PM/                  # 项目管理 + PR 文档归档
│   └── 07_spike/               # 预研究报告
│
├── scripts/                    # 脚本工具
├── Makefile                    # 构建和测试命令
├── go.mod / go.sum             # Go 依赖管理
└── README.md                   # 项目概述
```

---

## 高层架构

### 5 层 DDD 架构

```
┌─────────────────────────────────────────────────────────┐
│  Layer 5: API 层 (2 interfaces)                         │
│  KVClient, TxClient                                     │
└─────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────┐
│  Layer 4: 控制平面层 (14 interfaces)                    │
│  集群管理、分片路由、负载均衡、选举                      │
└─────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────┐
│  Layer 3: 数据平面层 (6 interfaces)                     │
│  副本管理、事务一致性、Quorum/EC                         │
└─────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────┐
│  Layer 2: 存储引擎层 (9 interfaces)                     │
│  双存储引擎：Metadata KV (sync.Map) + External KV        │
└─────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────┐
│  Layer 1: 基础设施层 (16 interfaces)                    │
│  网络通信、GoroutineProvider、AsyncOperation[T]、        │
│  可暂停调度器、Per-Core 执行器                           │
└─────────────────────────────────────────────────────────┘
```

**总计**：47 个统一接口 | 89 个实现文件

### 核心模块

| 模块 | 位置 | 职责 |
|------|------|------|
| **统一任务调度器** | `internal/infrastructure/concurrency/` | GoroutineProvider 接口体系、Per-Core 执行器、CPU 亲和性、优先级调度 |
| **领域模型** | `internal/domain/model/` | Task, HLC, Peer, Address, Cron 等核心类型 |
| **领域服务** | `internal/domain/service/` | RPC、Transport、Discovery、Cron 等接口定义 |
| **基础设施** | `internal/infrastructure/` | Transport、RPC、时钟、ID 生成器等实现 |
| **测试框架** | `pkg/test/` | 集成测试辅助工具 |

---

## 开发流程

### PR 工作流程（12 步）⚠️ **强制要求**

```
1. 建立新分支 feature/XXX
   ↓
2. 编写 Pre 文档（需求+设计+风险评估）
   ↓
3. 等待架构师评审 Pre ⚠️ **关键门禁**
   ↓ (通过)
4. 启动开发
   ↓
5. 编写 Post 文档（成果+测试报告）
   ↓
6. 等待架构师评审 Post ⚠️ **关键门禁**
   ↓ (通过)
7. 本地验证（lint + build + test + fmt + clean）
   ↓
8. 提交代码
   ↓
9. 推送并等待 CI
   ↓ (通过)
10. 通知架构师 "make ci passed"
    ↓
11. 等待架构师确认后合并 ⚠️ **禁止自动合并**
    ↓
12. 文档归档
```

**关键原则**：
- ✅ Pre 文档未通过评审前，**禁止启动开发**
- ✅ Post 文档未通过评审前，**禁止推送代码**
- ✅ 本地验证必须全部通过
- ✅ **禁止直接合并到 main 分支**

> 📖 **详细流程**：参考 `docs/workflow.md`

### 预研究模式（Spike）

**适用场景**：
- 需求不明确，需要先探索可行性
- 技术方案不确定，需要验证架构设计
- 验证性任务（POC、原型开发）

**分支命名**：`spike/技术方向-简述`

**与 PR 流程的区别**：
- Spike 分支**不直接合并**到 main
- 预研究完成后必须**明确下一步**（转入 feature 或归档）
- 调研笔记归档到 `docs/07_spike/` 目录

---

## 提交规范

### Conventional Commits

```
<type>(<scope>): <subject>

type: feat | fix | docs | style | refactor | test | chore
scope: 模块名称
subject: 简短描述（不超过 50 字符）
```

### 提交前验证

```bash
# 完整验证流程
make lint       # 必须通过：0 issues
make build      # 必须通过：编译成功
make test       # 必须通过：所有测试通过（包含竞态检测）
make fmt        # 必须通过：格式化完成
make clean      # 必须通过：清理完成

# 一键验证
make lint && make build && make test && make fmt && make clean && echo "✅ 所有验证通过"
```

---

## 关键文件位置

| 用途 | 文件路径 |
|------|---------|
| PR 工作流程 | `docs/workflow.md` |
| 文档索引 | `docs/CLAUDE.MD` |
| 编码规范 | `docs/03_development/01_编码规范文档.md` |
| 架构概览 | `docs/00_overview/01_核心架构概念.md` |
| PR 模板 | `docs/06_PM/templates/` |
| PR 文档归档 | `docs/06_PM/{feature,fix,refactor}/` |
| 统一任务调度器 | `internal/infrastructure/concurrency/` |
| 领域模型 | `internal/domain/model/` |
| 领域服务接口 | `internal/domain/service/` |
| Makefile 命令 | `Makefile` |

---

## 测试覆盖率要求

- **最低目标**：80%
- **核心模块**：85%+
- **检查命令**：`make test-coverage`

---

## 技术栈

| 组件 | 技术选型 |
|------|---------|
| 语言 | Go 1.21+ |
| 存储 | Metadata KV (sync.Map) + External KV (Bf-Tree) |
| 并发管理 | ants + GoroutineProvider + Per-Core 执行器 |
| 传输层 | libp2p |
| 编解码 | MessagePack |
| DI | Wire |
| 日志 | Zap |
| 测试 | testify |

---

## 许可证

本项目基于 **AGPL-3.0** 许可证开源。

---

**文档版本**：v3.0
**最后更新**：2026-03-01
**维护者**：NexKV 开发团队
