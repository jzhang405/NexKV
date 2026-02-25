# NexKV

> **⚠️ 项目处于开发早期 · 当前版本 v0.0.0 · 敬请期待！**
>
> **轻量化分布式 KV 存储系统**
> 面向中小规模集群（3-100 节点）的去中心化分布式数据库，支持**单机-分布式一体**架构

---

## ✨ 核心特性

| 特性 | 说明 |
|------|------|
| **单机-分布式一体** | 一套架构同时支持单机和分布式部署，运行时无缝切换 |
| **分层一致性** | 关键变更强一致（Quorum），普通变更最终一致（Gossip） |
| **分片自治** | 每个分片独立管理副本、事务与故障恢复 |
| **无中心化** | 无单点故障，所有节点地位平等 |
| **轻量化部署** | 无外部依赖（ZooKeeper/Etcd），元数据本地存储 |
| **WAL 优化** | 批量写入、组提交、增量恢复，测试覆盖率 72%+ |

---

## 🚀 快速开始

### 前置要求

- Go 1.21+
- 100MB+ 可用磁盘空间

### 构建项目

```bash
# 克隆仓库
git clone https://github.com/jzhang405/NexKV.git
cd NexKV

# 构建二进制文件
go build -o bin/nexkv cmd/nexkv/main.go

# 运行节点
./bin/nexkv --config configs/config.yaml
```

### 单机模式启动

```yaml
# configs/config.yaml (三级配置结构 - PR-037)
cluster:
  name: "nexkv-cluster"
  base_dir: "~/.nexkv"  # 可被 NEXKV_BASE_DIR 环境变量覆盖

  hosts:
    - host_id: "host-1"
      seed_node: "/ip4/127.0.0.1/tcp/9211"

      nodes:
        - node_id: "node-1"
          role: "leaf"

# 注意：元数据目录现在由 {base_dir}/{host_id}/metadata 自动管理
# 数据目录现在由 {base_dir}/{host_id}/{shards|wal|snapshots} 自动管理
```

### 分布式模式扩展

```bash
# 启动第二个节点
./bin/nexkv --config configs/node2.yaml

# 元数据自动同步，后台异步迁移数据
# 无需停机，平滑扩展为分布式集群
```

---

## 📚 核心文档

### 核心架构文档（docs/00_overview/）

| 文档 | 描述 |
|------|------|
| 🏗️ [核心架构概念](docs/00_overview/01_核心架构概念.md) | 三层架构、元数据层、数据层、事务层 |
| ⚖️ [一致性级别定义](docs/00_overview/02_一致性级别定义.md) | 分层一致性模型：Gossip、Quorum、2PC |
| 🔀 [单机-分布式一体](docs/00_overview/03_单机分布式一体.md) | 无缝切换、平滑扩展、故障恢复 |

---

## 🏗️ 架构概述

### 5层 DDD 架构

> ⭐ **最新架构**：基于 DDD（Domain-Driven Design）的 5 层精简架构，47 个统一接口

```mermaid
flowchart TB
    subgraph L5["Layer 5: API 层"]
        API["KVClient<br/>TxClient"]
    end

    subgraph L4["Layer 4: 控制平面层"]
        Cluster["集群管理<br/>分片路由<br/>负载均衡<br/>选举"]
    end

    subgraph L3["Layer 3: 数据平面层"]
        Data["副本管理<br/>事务一致性<br/>Quorum/EC"]
    end

    subgraph L2["Layer 2: 存储引擎层"]
        Storage["双存储引擎<br/>Metadata KV + External KV"]
    end

    subgraph L1["Layer 1: 基础设施层"]
        Infra["网络通信<br/>GoroutineProvider<br/>AsyncOperation[T]<br/>可暂停调度器"]
    end

    API --> Cluster
    Cluster --> Data
    Data --> Storage
    Storage --> Infra

    style L5 fill:#e1f5ff,stroke:#333,stroke-width:2px
    style L4 fill:#fff4e6,stroke:#333,stroke-width:2px
    style L3 fill:#f3e5f5,stroke:#333,stroke-width:2px
    style L2 fill:#e8f5e9,stroke:#333,stroke-width:2px
    style L1 fill:#fff9c4,stroke:#333,stroke-width:2px
```

### 5层架构详情

| 层次 | 接口数 | 核心职责 |
|------|--------|---------|
| **① API 层** | 2 | 对外 KV/Tx 接口 |
| **② 控制平面层** | 14 | 分片路由、选举、负载均衡 |
| **③ 数据平面层** | 6 | 复制、事务 |
| **④ 存储引擎层** | 9 | 双存储引擎（Metadata + External） |
| **⑤ 基础设施层** | 16 | 网络通信、可暂停调度器 |

**总计**: 47 个统一接口 | 89 个实现文件

---

## 🛠️ 技术栈

| 层级 | 技术选型 | 版本要求 | 理由 |
|------|---------|---------|------|
| **语言** | Go | >= 1.21 | 原生泛型、高性能并发 |
| **存储** | 双引擎架构 | - | Metadata KV (sync.Map) + External KV (Bf-Tree) |
| **并发管理** | ants + 泛型 | - | Goroutine Pool + 类型安全异步 |
| **Async** | AsyncOperation[T] | v19.0 | 基于 GoroutineProvider 的精化异步接口 |
| **Transport** | libp2p | - | 去中心化、NAT 穿透 |
| **编解码** | MessagePack | - | 高性能、自描述 |
| **DI** | Wire | - | 编译时检查 |
| **日志** | Zap | - | 结构化日志 |
| **测试** | testify | latest | 功能丰富、BDD 支持 |
| **覆盖率** | 80%+ | - | 高质量代码保障 |

### 统一执行器架构

> ⭐ **核心能力**：为 M2 存储引擎提供异步能力基础

| 组件 | 说明 | 性能目标 |
|------|------|---------|
| **接口拆分** | GoroutineProvider 13 → 7原子 + 3组合 + 4可暂停调度器 | 提升可测试性 |
| **Per-Core 执行器** | 每核单 goroutine，绑核无锁执行 | ≥ 2M ops/s，P99 < 10μs |
| **可暂停调度器** | 支持任务暂停/恢复/迁移 | 跨节点迁移支持 |

---

## 📊 最新进展（2026-01-19）

### ✅ 已完成功能

#### PR-001: WAL 优化与增强

**核心实现**：
- ✅ **WALGroupCommit** - 组提交机制，批量 fsync 优化吞吐量
- ✅ **WALBatchReader** - 批量 WAL 读取，优化恢复性能
- ✅ **WALCodec** - 统一的编解码接口，支持 JSON/MessagePack
- ✅ **WALRotation** - WAL 文件轮转，防止单文件过大
- ✅ **快照优化** - 快照命名优化为 `.snap` 扩展名

**测试改进**：
- 新增 12 个测试用例，覆盖批量操作场景
- 新增 20+ 编解码性能基准测试
- 测试覆盖率从 56% → **72%**

**性能对比**（编解码 Benchmark）：
| 操作 | JSON | MessagePack | 提升 |
|------|------|------------|------|
| 编码 | 1754 ns/op | 700 ns/op | **2.5x** |
| 解码 | 9652 ns/op | 656 ns/op | **14.7x** |
| 编码后大小 | 2153 bytes | 1609 bytes | **25%** |

---

## 🤝 贡献指南

### 开发流程

1. Fork 项目到你的 GitHub 账号
2. 创建功能分支：`git checkout -b feature/your-feature`
3. 遵循 PR 全流程（Pre 文档 → 开发 → 测试 → Post 文档）
4. 提交更改：`git commit -m 'feat: add some feature'`
5. 推送分支：`git push origin feature/your-feature`
6. 创建 Pull Request

### 提交规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

```
<type>(<scope>): <subject>

type: feat | fix | docs | style | refactor | test | chore
scope: 模块名称
subject: 简短描述（不超过 50 字符）
```

### 代码规范

- 遵循 `docs/03_development/01_编码规范文档.md` 定义的编码规范
- 所有公开接口必须有注释
- 复杂逻辑必须有解释说明
- 禁止使用魔法数字和魔法字符串
- 错误处理必须显式，禁止忽略
- CI/CD 通过：所有测试、lint、格式化检查

---

## 📈 性能指标

| 指标 | 目标值 | 测试方法 |
|------|--------|---------|
| **元数据查询延迟** | < 1ms | 本地读取 |
| **Gossip 扩散延迟** | < 10 秒（7 节点） | 集成测试 |
| **Quorum 确认延迟** | < 50ms（3 节点） | 集成测试 |
| **WAL 批量写入吞吐** | > 10000 entries/s | 基准测试 |
| **测试覆盖率** | > 70% | `go test -cover` |

---

## 📄 许可证

本项目基于 **AGPL-3.0** 许可证开源。详见 [LICENSE](LICENSE) 文件。

---

## 📞 联系方式

- **Issues**: [GitHub Issues](https://github.com/jzhang405/NexKV/issues)
- **Discussions**: [GitHub Discussions](https://github.com/jzhang405/NexKV/discussions)

---

**文档版本**: v1.2
**当前版本**: v0.0.0（开发早期）
**最后更新**: 2026-01-22
**维护者**: NexKV 开发团队
**测试覆盖率**: 72.0%
