# NexKV DDD 架构实施 - Pre 文档

> **PR 类型**: feature（重大架构升级）
> **创建日期**: 2026-02-18
> **最后更新**: 2026-02-18
> **文档版本**: v1.5（架构师评审 v1.4 后的最终批准版本）⭐
> **状态**: ✅ 已批准（架构师评审通过 2026-02-18）
> **Spike 文档**:
> - `docs/07_spike/2026-02-18_spike-nexkv-ddd-roadmap.md`（实施路线图）
> - `docs/07_spike/2026-02-18_spike-nexkv-ddd-implement.md`（实施方案）
> - `docs/07_spike/2026-02-18_spike-nexkv-ddd-interface.md`（接口定义 v18.0）

---

## 一、需求概述

### 1.1 背景

NexKV 项目当前处于架构升级阶段，需要基于 DDD（领域驱动设计）+ DI（依赖倒置）思想，落地一套完整的分布式 KV 数据库系统。经过预研究阶段（Spike），已完成以下核心设计：

- ✅ **47个统一接口** 完整定义（v18.0）
- ✅ **5层精简架构** 设计
- ✅ **AsyncOperation[T]** 精化异步接口
- ✅ **29-31周分阶段实施路线图** 规划（v1.4 修订）⭐

### 1.2 核心目标

构建一个**高性能、分布式、DDD架构**的 KV 存储系统：

| 目标 | 描述 | 可验证标准（分级目标） |
|------|------|----------------------|
| **单机-分布式一体** | 一套架构同时支持单机和分布式部署，运行时无缝切换 | **单机性能**：P0≥30万/P1≥50万/P2≥75万 ops/sec ⭐<br/>**集群吞吐**：P0≥100万/P1≥300万/P2≥500万 ops/sec |
| **5层精简架构** | API → 控制平面 → 数据平面 → 存储引擎 → 基础设施 | 47个接口，89个实现文件 |
| **统一异步接口** | AsyncOperation[T] + Callback + Channel 三种模式 | 19个核心接口支持完整异步 |
| **生产级质量** | 80%+ 测试覆盖率，无数据丢失，高可用 | 单元测试 ≥ 80%<br/>**故障转移 RTO**：P0<60秒/P1<30秒/P2<10秒 |

### 1.3 业务价值

| 价值点 | 说明 |
|--------|------|
| **架构清晰** | 5层分层架构，依赖倒置，易于维护和扩展 |
| **技术先进** | libp2p 去中心化、Bf-Tree 高性能存储、泛型异步接口 |
| **质量保证** | DDD 聚合根、领域事件、资源管理防止泄漏 |
| **快速开发** | 89个实现文件明确分配，30-32周分阶段交付 ⭐ |

---

## 二、技术方案

### 2.1 架构设计（5层架构）

**详细文档**：参见 `docs/07_spike/2026-02-18_spike-nexkv-ddd-roadmap.md`

### 2.2 技术选型

| 组件 | 选型 | 说明 |
|------|------|------|
| **语言** | Go 1.21+ | 泛型支持、高性能并发 |
| **Transport** | libp2p | 去中心化、局域网直连 ⭐ v1.2 修订 |
| **KVStore** | Bf-Tree (Go port) | 高性能 B+tree、WAL 优化 |
| **序列化** | MessagePack | 高性能、自描述 |
| **DI容器** | Wire | 编译时检查 |
| **日志** | Zap | 结构化日志 |

**⭐ v1.2 部署环境说明**：
- **当前环境**：局域网（LAN）部署，节点间直连
- **不支持**：NAT 穿透、DHT 节点发现（公网环境）
- **节点发现**：基于静态配置或 mDNS（局域网广播）
- **未来扩展**：如需公网部署，后续可增加 NAT 穿透支持

### 2.3 实施计划（30-32周，分3个阶段）⭐ v1.5 修订

#### 总体时间线

- **总周期**：30-32周（约7-8个月）⭐ v1.5 恢复
- **分阶段交付**：3个阶段，每个阶段可独立验收
- **关键路径**：Bf-Tree 移植（与 Phase 1 并行启动）
- **前期培训**：Week 0（3天）⭐ v1.5 新增

#### Phase 规划

- **Week 0**: 团队培训（3天）⭐ v1.5 新增
- **Phase 1**: 基础设施层（4周，Week 1-4）⭐ 包含 POC 验证
- **Phase 2**: 存储引擎层（10-12周，Week 5-16）
- **Phase 3**: 数据平面层（4周，Week 17-20）
- **Phase 4**: 控制平面层（4周，Week 21-24）
- **Phase 5**: API层（2周，Week 25-26）
- **Phase 6**: 集成测试与优化（6周，Week 27-32）⭐ v1.5 恢复 6周

#### Week 0: 团队培训（3天）⭐ v1.5 新增

**目标**：降低团队学习曲线，统一技术理解

| 培训内容 | 时长 | 目标 |
|---------|------|------|
| **DDD 架构原则** | 0.5天 | 理解 5层架构、依赖倒置、聚合根设计 |
| **Go 泛型编程** | 0.5天 | 掌握 AsyncOperation[T] 泛型接口设计 |
| **libp2p 基础** | 1天 | 理解去中心化通信、mDNS 节点发现、Stream 多路复用 |
| **Bf-Tree 原理** | 1天 | 理解 Bf-Tree 数据结构、WAL 机制、性能优势 |

**验收标准**：
- [ ] 团队成员完成培训签到
- [ ] 培训资料归档（PPT + 示例代码）
- [ ] 培训后测试通过率 ≥ 80%

---

#### 分阶段交付计划

| 阶段 | 时间 | 里程碑 | 交付成果 | 可独立运行 |
|------|------|--------|----------|-----------|
| **第一阶段** | Week 1-16 | M1-M2 | 基础设施 + 存储引擎 | ✅ 单机版可用 |
| **第二阶段** | Week 17-26 | M3-M5 | 数据平面 + 控制平面 + API | ✅ 分布式版可用 |
| **第三阶段** | Week 27-32 | M6 | 集成测试 + 性能优化 | ✅ 生产就绪版 |

**详细规划**：参见 `docs/07_spike/2026-02-18_spike-nexkv-ddd-roadmap.md`

#### ⭐ v1.5 时间线调整说明

**主要变更**：
1. ✅ **添加 Week 0 培训**：3天团队培训（DDD + Go 泛型 + libp2p + Bf-Tree）
2. ✅ **优化 POC 验证**：分为 Week 1-2 核心 POC 和 Week 3-4 扩展验证
3. ✅ **恢复 Phase 6 缓冲期**：5周 → 6周（v1.4 调整过于激进）
4. ✅ **总周期调整**：29-31周 → 30-32周

**POC 验证策略**（v1.5 优化）：
- ✅ **Week 1-2：核心 POC 验证**（在单元测试中）：
  - mDNS 节点发现（局域网）
  - 节点间直连通信
  - RPC 调用（延迟 < 5ms）
  - 验收标准：核心功能单元测试通过

- ✅ **Week 3-4：扩展 POC 验证**（在集成测试中）：
  - 3 节点集群通信
  - 性能基准测试（吞吐量 ≥ 10K ops/sec）
  - 连接建立时间（< 2秒）
  - 验收标准：M1 里程碑通过

**优势**：
- ✅ 分阶段验证降低风险
- ✅ POC 验证与实际开发紧密结合，更贴近实际需求
- ✅ 通过单元测试和集成测试保证质量，无需单独阶段

### 2.4 DDD 文件目录设计 ⭐ v1.3 新增

#### 2.4.1 当前目录结构（功能模块划分）

```
nexkv/
├── cmd/kvserver/main.go
└── internal/
    ├── clock/           # HLC 时钟
    ├── config/          # 配置管理
    ├── metadata/        # 元数据管理
    │   ├── api/
    │   ├── cluster/
    │   ├── consistency/
    │   ├── gossip/
    │   ├── kvstore/
    │   ├── partition/
    │   └── quorum/
    ├── rpc/             # RPC 通信
    ├── transport/       # 网络传输
    └── wal/             # WAL 日志
```

**问题**：
- ❌ 按功能模块划分，缺少清晰的层次边界
- ❌ 依赖关系不明确（如 rpc 直接依赖 metadata）
- ❌ 难以实现依赖倒置（DIP）

#### 2.4.2 DDD 目标目录结构（5层架构）

```
nexkv/
├── cmd/
│   └── kvserver/
│       └── main.go              # 启动入口（DI 组装）
│
├── internal/
│   │
│   │── ① domain/                # 领域层（无外部依赖）⭐ 核心
│   │   ├── model/               # 领域模型（12个文件）
│   │   │   ├── errors.go        # 结构化错误（分层错误码）
│   │   │   ├── value_objects.go # 值对象（NodeID/ShardID/TxID）
│   │   │   ├── kv.go            # KV 模型
│   │   │   ├── tx.go            # 事务模型
│   │   │   ├── node.go          # 节点模型
│   │   │   ├── shard.go         # 分片模型
│   │   │   ├── replica.go       # 副本模型
│   │   │   ├── message.go       # 消息模型
│   │   │   ├── block.go         # 块设备模型
│   │   │   ├── futures.go       # 统一 AsyncOperation[T]
│   │   │   ├── async_result.go  # 异步结果模型
│   │   │   └── event.go         # 事件模型（Channel 模式）
│   │   │
│   │   ├── repository/          # 仓库接口（3个文件）
│   │   │   ├── node_repository.go   # NodeRepository 接口
│   │   │   ├── shard_repository.go  # ShardRepository 接口
│   │   │   └── tx_repository.go     # TxRepository 接口
│   │   │
│   │   └── service/             # 47个接口定义（按5层组织）⭐
│   │       │
│   │       │—— ① API层接口 (2个)
│   │       │   └── client.go            # KVClient, TxClient
│   │       │
│   │       │—— ② 控制平面层接口 (14个)
│   │       │   ├── cluster.go           # 7个接口
│   │       │   ├── shard.go             # ShardManager, ShardRouter
│   │       │   ├── partition.go         # Partitioner
│   │       │   ├── election.go          # Election
│   │       │   ├── balance.go           # LoadBalancer
│   │       │   ├── broadcast.go         # Broadcaster
│   │       │   └── security.go          # SecurityLayer
│   │       │
│   │       │—— ③ 数据平面层接口 (6个)
│   │       │   ├── replication.go       # 4个接口
│   │       │   └── tx.go                # TxManager, TxCoordinator
│   │       │
│   │       │—— ④ 存储引擎层接口 (9个)
│   │       │   ├── storage.go           # 5个接口
│   │       │   └── blockdevice.go       # 4个接口
│   │       │
│   │       └—— ⑤ 基础设施层接口 (16个)
│   │           ├── transport.go         # 6个接口
│   │           ├── middleware.go        # 2个接口
│   │           ├── performance.go       # 3个接口
│   │           ├── resilience.go        # 3个接口
│   │           └── extension.go         # 2个接口
│   │
│   │—— ② application/            # 应用层（只依赖 domain）
│   │   ├── service/              # 5个应用服务
│   │   │   ├── kv_service.go     # KV 应用服务
│   │   │   ├── tx_service.go     # 事务应用服务
│   │   │   ├── cluster_service.go # 集群应用服务
│   │   │   ├── shard_service.go  # 分片应用服务
│   │   │   └── admin_service.go  # 管理应用服务
│   │   │
│   │   ├── dto/                  # 4个 DTO
│   │   │   ├── kv_dto.go         # KV DTO
│   │   │   ├── tx_dto.go         # 事务 DTO
│   │   │   ├── cluster_dto.go    # 集群 DTO
│   │   │   └── metrics_dto.go    # 监控 DTO
│   │   │
│   │   └── usecase/              # 5个用例
│   │       ├── put_kv.go         # Put KV 用例
│   │       ├── get_kv.go         # Get KV 用例
│   │       ├── commit_tx.go      # 提交事务用例
│   │       ├── migrate_shard.go  # 分片迁移用例
│   │       └── add_node.go       # 添加节点用例
│   │
│   └—— ③ infrastructure/         # 基础设施层（接口实现）
│       │
│       │—— ① API层实现 (8个文件)
│       │   └── client/
│       │       ├── kv_client_impl.go     # KVClient 实现
│       │       ├── client_tx_impl.go     # TxClient 实现
│       │       ├── client_future_impl.go # ClientFuture 实现
│       │       ├── batch_future_impl.go  # BatchGetFuture 实现
│       │       ├── router.go             # 客户端路由器
│       │       ├── failover.go           # 故障转移
│       │       ├── retry.go              # 重试策略
│       │       └── event_channel.go      # 事件 Channel
│       │
│       │—— ② 控制平面层实现 (23个文件)
│       │   ├── cluster/           # 14个文件
│       │   │   ├── tree_coordinator_impl.go
│       │   │   ├── node_manager_impl.go
│       │   │   ├── topology_impl.go
│       │   │   ├── ha_controller_impl.go
│       │   │   ├── metadata_store_impl.go
│       │   │   ├── heartbeat_impl.go
│       │   │   ├── group_manager_impl.go
│       │   │   ├── partitioner_impl.go   # ⭐ v17.0 新增
│       │   │   ├── election_impl.go      # ⭐ v17.0 新增
│       │   │   ├── loadbalancer_impl.go  # ⭐ v17.0 新增
│       │   │   └── ... (其他文件)
│       │   │
│       │   └── shard/             # 9个文件
│       │       ├── shard_manager_impl.go
│       │       ├── shard_router_impl.go
│       │       ├── shard_future_impl.go
│       │       ├── split_future_impl.go
│       │       ├── merge_future_impl.go
│       │       ├── move_future_impl.go
│       │       ├── migrator.go
│       │       ├── splitter.go
│       │       └── event_channel.go
│       │
│       │—— ③ 数据平面层实现 (20个文件)
│       │   ├── replication/       # 12个文件
│       │   │   ├── replicator_impl.go
│       │   │   ├── replication_future_impl.go
│       │   │   ├── quorum.go
│       │   │   ├── hlc/hlc.go
│       │   │   ├── conflict_resolver.go
│       │   │   ├── catchup.go
│       │   │   ├── ec_strategy_impl.go
│       │   │   ├── event_channel.go
│       │   │   └── ... (其他文件)
│       │   │
│       │   └── tx/                # 8个文件
│       │       ├── tx_manager_impl.go
│       │       ├── tx_coordinator_impl.go
│       │       ├── transaction_impl.go
│       │       ├── tx_future_impl.go
│       │       ├── commit_future_impl.go
│       │       ├── lock_manager.go
│       │       ├── tx_logger.go
│       │       └── event_channel.go
│       │
│       │—— ④ 存储引擎层实现 (19个文件)
│       │   ├── storage/           # 11个文件
│       │   │   ├── bftree_store.go         # Bf-Tree 实现
│       │   │   ├── storage_future_impl.go  # AsyncOperation[T]
│       │   │   ├── wal_impl.go             # WAL 实现
│       │   │   ├── btree_impl.go           # B+树实现
│       │   │   ├── iterator_impl.go        # 迭代器实现
│       │   │   ├── local_tx_impl.go        # 本地事务实现
│       │   │   └── ... (其他文件)
│       │   │
│       │   └── blockdevice/       # 8个文件
│       │       ├── local_storage_impl.go   # 本地存储实现
│       │       ├── block_future_impl.go    # AsyncOperation[T]
│       │       ├── cloud_storage_impl.go   # 云存储实现
│       │       ├── distributed_storage_impl.go
│       │       └── ... (其他文件)
│       │
│       └—— ⑤ 基础设施层实现 (16个文件)
│           ├── transport/         # 10个文件
│           │   ├── libp2p_transport_impl.go  # libp2p 实现
│           │   ├── message_impl.go           # Message 实现
│           │   ├── stream_impl.go            # Stream 实现
│           │   ├── channel_impl.go           # Channel 实现
│           │   ├── requestor_impl.go         # Requestor 实现
│           │   ├── codec_impl.go             # MessagePack 实现
│           │   ├── security_layer_impl.go    # TLS/Noise 实现
│           │   ├── tls_manager_impl.go       # TLS 专用
│           │   ├── noise_manager_impl.go     # Noise 专用
│           │   └── middleware_impl.go        # 中间件链
│           │
│           ├── performance/       # 3个文件
│           │   ├── batch_replicator_impl.go
│           │   ├── pipeline_impl.go
│           │   └── cache_layer_impl.go
│           │
│           ├── resilience/        # 3个文件
│           │   ├── circuit_breaker_impl.go
│           │   ├── retry_policy_impl.go
│           │   └── chaos_monkey_impl.go
│           │
│           └── extension/         # 2个文件
│               ├── plugin_impl.go
│               └── dynamic_config_impl.go
│
├── pkg/                         # 公共库（可选）
│   └── utils/
│       ├── logger.go            # 日志工具
│       └── metrics.go           # 监控工具
│
└── wire/                        # DI 依赖注入
    ├── wire.go                  # Wire 定义
    └── wire_gen.go              # Wire 生成
```

#### 2.4.3 目录设计原则

**1. 依赖倒置原则（DIP）**
```
domain/（无外部依赖）
   ↑
application/（只依赖 domain）
   ↑
infrastructure/（实现 domain 接口）
```

**2. 单一职责原则（SRP）**
- 每个文件只负责一个接口或一个实现
- 文件大小控制在 200-400 行（最大 800 行）

**3. 接口与实现分离**
```
domain/service/transport.go      # Transport 接口定义
infrastructure/transport/libp2p_transport_impl.go  # Transport 实现
```

**4. 按领域划分，不按技术划分**
```
❌ 错误：internal/rpc/、internal/tcp/
✅ 正确：domain/service/transport.go、infrastructure/transport/
```

---

### 2.5 现有代码迁移与重构策略 ⭐ v1.3 新增

#### 2.5.1 迁移策略：渐进式重构（Strangler Pattern）

**核心原则**：不进行一次性大重构，而是分阶段渐进迁移，保证系统始终可运行。

#### 2.5.2 迁移阶段规划

**阶段 0：准备阶段（Week 0，1周）**

**目标**：建立 DDD 目录结构，不影响现有代码

**任务**：
1. [ ] 创建 DDD 目录结构
   ```bash
   mkdir -p internal/domain/{model,repository,service}
   mkdir -p internal/application/{service,dto,usecase}
   mkdir -p internal/infrastructure/{client,cluster,shard,replication,tx,storage,blockdevice,transport,performance,resilience,extension}
   ```

2. [ ] 创建第一个 domain 接口（示例：Transport）
   ```go
   // internal/domain/service/transport.go
   type Transport interface {
       Listen(ctx context.Context) error
       Close() error
       // ...
   }
   ```

3. [ ] 创建对应的 infrastructure 实现（适配器模式）
   ```go
   // internal/infrastructure/transport/libp2p_transport_adapter.go
   type Libp2pTransportAdapter struct {
       legacyTransport *transport.Libp2pTransport  // 包装现有实现
   }

   func (a *Libp2pTransportAdapter) Listen(ctx context.Context) error {
       return a.legacyTransport.Listen()
   }
   ```

**验收标准**：
- [x] DDD 目录结构创建完成
- [x] 至少 1 个 domain 接口定义
- [x] 对应的 infrastructure 适配器实现
- [x] 现有代码继续正常运行（零影响）

---

**阶段 1：基础设施层迁移（Week 1-4，4周）**

**目标**：将现有 `internal/transport/` 和 `internal/rpc/` 迁移到 DDD 架构

**迁移映射表**：

| 现有代码 | DDD 目标位置 | 迁移策略 |
|---------|-------------|----------|
| `internal/transport/libp2p_transport_adapter.go` | `infrastructure/transport/libp2p_transport_impl.go` | ✅ 直接迁移 |
| `internal/transport/discovery.go` | `infrastructure/transport/discovery_impl.go` | ✅ 直接迁移 |
| `internal/rpc/client.go` | `infrastructure/transport/requestor_impl.go` | 🔄 重构适配 |
| `internal/rpc/server.go` | `infrastructure/transport/server_impl.go` | 🔄 重构适配 |
| `internal/rpc/types.go` | `domain/model/message.go` | 🔄 抽象到 domain |

**具体步骤**：

**Week 1：Transport 接口迁移**
1. [ ] 定义 `domain/service/transport.go`（6个接口）
2. [ ] 迁移 `internal/transport/` 到 `infrastructure/transport/`
3. [ ] 创建适配器，保持向后兼容
   ```go
   // 旧代码继续工作
   legacyTransport := transport.NewLibp2pTransport()

   // 新代码使用 DDD 接口
   var transportService domain.Transport = infrastructure.NewLibp2pTransportImpl()
   ```

**Week 2：RPC 迁移为 Requestor**
1. [ ] 定义 `domain/service/transport.go` 中的 Requestor 接口
2. [ ] 重构 `internal/rpc/client.go` → `infrastructure/transport/requestor_impl.go`
3. [ ] 迁移 RPC 消息类型到 `domain/model/message.go`

**Week 3：中间件和容错机制**
1. [ ] 定义 `domain/service/middleware.go`（2个接口）
2. [ ] 实现中间件链（`infrastructure/transport/middleware_impl.go`）
3. [ ] 实现熔断器（`infrastructure/resilience/circuit_breaker_impl.go`）

**Week 4：集成测试与清理**
1. [ ] 验证所有 transport 接口工作正常
2. [ ] 删除旧代码（`internal/transport/`、`internal/rpc/`）
3. [ ] 更新所有引用

**验收标准**：
- [x] 16个基础设施层接口全部迁移
- [x] 旧代码删除，无编译错误
- [x] 所有测试通过

---

**阶段 2：存储引擎层迁移（Week 5-16，10-12周）**

**迁移映射表**：

| 现有代码 | DDD 目标位置 | 迁移策略 |
|---------|-------------|----------|
| `internal/wal/wal.go` | `infrastructure/storage/wal_impl.go` | ✅ 直接迁移 |
| `internal/wal/mvstore.go` | `infrastructure/storage/bftree_store.go` | 🔄 重构适配 Bf-Tree |
| `internal/clock/hlc.go` | `domain/model/hlc.go` | 🔄 抽象到 domain |

**关键决策**：
- **Bf-Tree 移植**：从微软官方移植，替代现有 MVStore
- **Week 8 检查点**：未达 P0 性能目标 → 启用 Badger

---

**阶段 3-5：数据平面、控制平面、API 层迁移（Week 17-26，10周）**

**迁移映射表**：

| 现有代码 | DDD 目标位置 | 说明 |
|---------|-------------|------|
| `internal/metadata/cluster/` | `infrastructure/cluster/` | 集群管理实现 |
| `internal/metadata/consistency/` | `infrastructure/replication/` | 复制一致性实现 |
| `internal/metadata/gossip/` | `infrastructure/cluster/gossip_impl.go` | Gossip 实现 |
| `internal/metadata/kvstore/` | `infrastructure/storage/` | KV 存储实现 |
| `internal/metadata/partition/` | `infrastructure/shard/partitioner_impl.go` | 分片路由实现 |
| `internal/metadata/quorum/` | `infrastructure/replication/quorum.go` | Quorum 实现 |

---

#### 2.5.3 迁移风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **破坏现有功能** | 高 | 1. 渐进式迁移（Strangler Pattern）<br/>2. 保持向后兼容<br/>3. 每个阶段充分测试 |
| **迁移周期过长** | 中 | 1. 并行迁移多个模块<br/>2. 自动化测试<br/>3. 代码生成工具（Wire） |
| **团队学习成本** | 中 | 1. DDD 培训<br/>2. 代码示例<br/>3. 结对编程 |

#### 2.5.4 迁移检查清单

**每个阶段完成后**：
- [ ] 新目录结构正确
- [ ] domain 接口定义完整
- [ ] infrastructure 实现正确
- [ ] 所有测试通过（单元测试 + 集成测试）
- [ ] 旧代码已删除
- [ ] 文档已更新

**整个迁移完成后**：
- [ ] 47个接口全部迁移
- [ ] 89个实现文件全部完成
- [ ] 性能目标达成（P0/P1/P2）
- [ ] 代码覆盖率 ≥ 80%
- [ ] 无技术债务遗留

---

## 三、风险评估

### 3.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 | 检查点 |
|------|------|------|----------|--------|
| **Bf-Tree 移植延期** | 高 | 中 | 1. **并行启动**：Bf-Tree 移植与 Phase 1 同步启动（Week 1）<br/>2. **降级方案**：准备 Badger 作为临时方案<br/>3. **明确触发条件**：Week 8 未达 P0 性能目标（50万 ops/sec）→ 启用 Badger | Week 8 检查点 |
| **libp2p 集成复杂** | 中 | 低 | 1. **单元测试**：在 Phase 1 开发过程中验证基础通信（局域网直连）⭐ v1.4<br/>2. **分阶段实现**：Week 1-2 同步 RPC → Week 3 Stream/Channel → Week 4 中间件<br/>3. **节点发现**：静态配置或 mDNS（局域网），无需 NAT 穿透 ⭐ v1.2 | Week 4 检查点 |
| **AsyncOperation[T] 实现复杂** | 高 | 中 | 1. **参考实现**：Phase 1 Week 1 完成参考实现，作为其他接口模板<br/>2. **资源管理**：FutureManager 和 BackpressureController 必须在 M1 完成<br/>3. **压力测试**：Phase 6 增加异步接口压力测试 | M1 检查点 |
| **分布式事务性能瓶颈** | 高 | 中 | 2PC 优化，减少锁持有时间 | Week 20 检查点 |
| **分片迁移数据丢失** | 高 | 低 | 双写机制确保一致性 | Week 24 检查点 |

### 3.2 项目风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|----------|
| **30-32周周期较长，需求变更** | 中 | 中 | 1. **分阶段交付**：3个阶段可独立验收，降低整体风险<br/>2. **每2周评审**：及时调整方向 |
| **开发资源不足** | 高 | 低 | 1. **明确优先级**（P0 > P1 > P2）<br/>2. **关键路径监控**：Bf-Tree 移植作为关键路径，每周进度报告 |

---

## 四、实施计划

### 4.1 里程碑时间表（30-32周）⭐ v1.5 修订

| 里程碑 | 时间 | 关键成果 | 可验证标准 | 阶段 |
|--------|------|----------|------------|------|
| **M0** | 第0周 | 团队培训完成 ⭐ | DDD + Go 泛型 + libp2p + Bf-Tree 培训通过 | 准备阶段 |
| **M1** | 第4周 | 基础设施层完成 | 16个接口单元测试通过（含 POC 验证）⭐ | 第一阶段 |
| **—** | **第12周** | **中期检查点 1** ⭐ | **Bf-Tree 移植进度评估** | — |
| **M2** | 第16周 | 存储引擎层完成 | 单节点 KV 性能达标（P0≥30万 ops/sec）⭐ | 第一阶段 |
| **—** | **第18周** | **中期检查点 2** ⭐ | **数据平面进度评估** | — |
| **M3** | 第20周 | 数据平面层完成 | 3副本写入正常 | 第二阶段 |
| **M4** | 第24周 | 控制平面层完成 | 3节点集群运行正常 | 第二阶段 |
| **M5** | 第26周 | API层完成 | 客户端路由和故障转移正常 | 第二阶段 |
| **M6** | 第32周 | 集成测试与优化完成 ⭐ | 所有性能目标达成（P0/P1/P2） | 第三阶段 |

**⭐ v1.5 修订说明**：
- 添加 M0 里程碑（Week 0 团队培训）
- 添加 Week 12 中期检查点（Bf-Tree 移植进度）
- 添加 Week 18 中期检查点（数据平面进度）
- M2 性能目标调整：50万 → 30万 ops/sec（P0）
- M6 时间恢复到第32周（Phase 6 缓冲期 6周）
- 总周期恢复到 30-32周

### 4.2 Bf-Tree 移植并行计划 ⭐ v1.5 修订

| 时间 | Bf-Tree 移植任务 | DDD Phase 任务 | 检查点 |
|------|-----------------|---------------|--------|
| **Week 0** | — | 团队培训（DDD + Go 泛型 + libp2p + Bf-Tree）⭐ | M0 里程碑 |
| **Week 1-4** | Bf-Tree 移植 Phase 1-2 | Phase 1 基础设施层（含 POC 验证）⭐ | M1 里程碑 |
| **Week 5-8** | Bf-Tree 移植 Phase 3-4 | Phase 2 存储引擎层（设计阶段） | ⭐ **Week 8 检查点**：未达 P0（30万 ops/sec）→ 启用 Badger |
| **Week 9-12** | Bf-Tree 移植 Phase 5-6 | Phase 2 存储引擎层（实现阶段） | ⭐ **Week 12 检查点**：Bf-Tree 移植进度评估 |
| **Week 13-16** | Bf-Tree 优化与集成 | Phase 2 存储引擎层（集成测试） | M2 里程碑 |

**关键决策**：
- ✅ Bf-Tree 移植与 Phase 1 基础设施层 **并行启动**（Week 1）
- ✅ Week 8 检查点：如果未达 P0 性能目标（30万 ops/sec），**自动启用 Badger 临时方案** ⭐
- ✅ Week 12 检查点：评估 Bf-Tree 移植进度，必要时调整计划 ⭐
- ✅ Badger 临时方案仅作为过渡，最终目标仍是 Bf-Tree

---

## 五、测试计划

### 5.1 测试策略

- **单元测试**：80%+ 覆盖率（所有47个接口）
- **集成测试**：3节点集群测试
- **E2E 测试**：关键用户流程
- **混沌测试**：故障注入（Phase 6）
- **性能基准测试**：分级目标（P0/P1/P2）⭐ v1.1 新增

### 5.2 性能基准测试（分级目标）⭐ v1.5 修订

| 指标 | P0（最低） | P1（推荐） | P2（理想） | 验证方法 |
|------|-----------|-----------|-----------|----------|
| **单节点写入** | ≥ 30万 ops/sec | ≥ 50万 ops/sec | ≥ 75万 ops/sec ⭐ | `go test -bench=.` |
| **单节点读取** | ≥ 80万 ops/sec | ≥ 100万 ops/sec | ≥ 150万 ops/sec | `go test -bench=.` |
| **集群吞吐（3节点）** | ≥ 100万 ops/sec | ≥ 300万 ops/sec | ≥ 500万 ops/sec | 集群压测 |
| **延迟 P99** | < 20ms | < 10ms | < 5ms | 分布式追踪 |
| **故障转移 RTO** | < 60秒 | < 30秒 | < 10秒 | 混沌测试 |

**⭐ v1.5 修订说明**：
- 性能目标从单一值调整为 **P0/P1/P2 三级目标**
- **P0 目标调整**：单机写入从 50万降低到 **30万（P0）**，基于 Badger 基线 ⭐
- **P1 目标调整**：单机写入从 75万降低到 **50万（P1）**，对齐 Bf-Tree MVP P0 目标 ⭐
- 增加分阶段验证机制：M2 验证 P0 目标，M6 验证 P1/P2 目标

### 5.3 性能验证计划 ⭐ v1.5 修订

| 里程碑 | 验证目标 | 验证标准 | 触发条件 |
|--------|----------|----------|----------|
| **M2（Week 16）** | 单机性能 P0 | 单节点写入 ≥ 30万 ops/sec ⭐ | 如果未达 P0 → 启用 Badger 临时方案 |
| **M4（Week 24）** | 集群性能 P0 | 3节点集群吞吐 ≥ 100万 ops/sec | 如果未达 P0 → 调整架构设计 |
| **M6（Week 32）** | 全面性能 P1/P2 | 所有指标达到 P1，争取 P2 | 如果仅达 P0 → 性能优化迭代 |

---

## 六、评审清单

### 6.1 Pre 文档评审要点

架构师评审时请关注：

- [x] **需求清晰度**：DDD 架构实施目标是否明确？✅ v1.1 已明确
- [x] **技术可行性**：Bf-Tree 移植、libp2p 集成是否可行？✅ v1.1 增加检查点
- [x] **风险评估**：是否识别了关键风险并有缓解措施？✅ v1.1 增加具体检查点
- [x] **实施计划**：30-32周里程碑是否合理？✅ v1.1 对齐 ADR 006
- [x] **测试覆盖**：80%+ 覆盖率目标是否可达？✅ 策略完整
- [x] **性能目标**：分级目标（P0/P1/P2）是否合理？✅ v1.1 对齐 Bf-Tree MVP

### 6.2 关键决策确认（v1.1 修订）⭐

#### 决策 1: Bf-Tree 移植 vs Badger 临时方案

**架构师意见**：**有条件批准** ✅

**执行策略**：
1. ✅ **并行启动**：Bf-Tree 移植与 Phase 1 基础设施层并行启动（Week 1）
2. ✅ **Week 8 检查点**：如果未达到 P0 性能目标（30万 ops/sec），自动启用 Badger 临时方案 ⭐
3. ✅ **Week 12 检查点**：评估 Bf-Tree 移植进度，必要时调整计划 ⭐
4. ✅ **Badger 仅作过渡**：最终目标仍是 Bf-Tree

**理由**：
- Bf-Tree 性能优势明显（比传统 BTree 快 1.7-3.3x）
- 但移植风险高，需要明确的降级触发条件
- 并行推进可以最大化时间效率
- P0 目标基于 Badger 基线（30万 ops/sec），更稳妥 ⭐

---

#### 决策 2: libp2p vs gRPC

**架构师意见**：**批准 libp2p（局域网部署）** ✅

**执行策略**：
1. ✅ **Phase 1 单元测试**：在 Transport 层单元测试中验证基础通信（局域网直连）⭐ v1.4
2. ✅ **Phase 1 集成测试**：在 Week 3-4 集成测试中验证 3 节点集群通信 ⭐ v1.4
3. ✅ **节点发现**：静态配置或 mDNS（局域网广播），无需 NAT 穿透 ⭐ v1.2
4. ✅ **接口层抽象**：保留接口层抽象，未来可扩展到公网环境

**理由**：
- libp2p 去中心化优势明显，适合 NexKV 无中心架构
- 当前局域网环境无需 NAT 穿透，降低集成复杂度 ⭐ v1.2
- POC 验证合并到 Phase 1 测试中，加快启动速度 ⭐ v1.4
- 保留未来扩展到公网的能力（如需 NAT 穿透，后续可增加）

---

#### 决策 3: 24周周期 vs 分阶段交付

**架构师意见**：**调整为分阶段交付** ✅

**执行策略**：
- **第一阶段（12-16周）**：M1-M2 完成（基础设施+存储引擎），✅ **可独立交付单机版**
- **第二阶段（10周）**：M3-M5 完成（数据平面+控制平面+API），✅ **可交付分布式版**
- **第三阶段（6周）**：M6 完成（集成测试+优化），✅ **生产就绪版**

**理由**：
- 分阶段交付可以更早获得用户反馈
- 单机版可提前验证存储引擎和异步接口
- 降低整体项目风险，即使第二阶段延期，单机版仍可用

---

### 6.3 v1.1 修订要点总结 ⭐

根据架构师评审意见，Pre 文档 v1.1 已完成以下修订：

**必须修改项**：
1. ✅ **时间线调整**：Phase 2 从 6周延长到 10-12周，总周期 24周 → 30-32周
2. ✅ **性能目标分级**：单机写入 50K → P0:50万/P1:75万/P2:100万 ops/sec
3. ✅ **Bf-Tree 移植并行化**：明确与 Phase 1 并行启动，Week 8 检查点
4. ✅ **降级触发条件**：Week 8 未达 P0（50万 ops/sec）→ 启用 Badger

**建议修改项**：
1. ✅ **分阶段交付**：调整为 3个阶段的分阶段交付计划
2. ✅ **缓冲时间**：Phase 6 从 4周增加到 6周（v1.4 调整为 5周）
3. ✅ **POC 验证**：合并到 Phase 1 单元测试和集成测试 ⭐ v1.4

---

## 七、下一步行动

**Pre 文档 v1.5 修订完成（架构师评审 v1.4 后的最终批准版本）** ⭐

### 7.1 立即行动（Week 0 + Week 1）⭐ v1.5 修订

**Week 0: 团队培训（3天）⭐ v1.5 新增**

1. ✅ **Day 1 上午：DDD 架构原则**（0.5天）
   - 5层架构设计理念
   - 依赖倒置原则（DIP）
   - 聚合根和领域事件设计

2. ✅ **Day 1 下午：Go 泛型编程**（0.5天）
   - Go 1.18+ 泛型语法
   - AsyncOperation[T] 接口设计
   - 泛型约束和类型推断

3. ✅ **Day 2-3：libp2p 基础**（1天）
   - 去中心化通信原理
   - mDNS 节点发现（局域网）
   - Stream 多路复用
   - 实践：编写 libp2p Hello World 程序

4. ✅ **Day 4-5：Bf-Tree 原理**（1天）
   - Bf-Tree 数据结构
   - WAL 写入机制
   - 性能优势分析（比传统 BTree 快 1.7-3.3x）

**验收标准**：
- [ ] 团队成员完成培训签到（100%）
- [ ] 培训资料归档（PPT + 示例代码）
- [ ] 培训后测试通过率 ≥ 80%
- [ ] M0 里程碑通过

---

**Week 1-4：Phase 1 基础设施层开发 + POC 验证（4周）⭐ v1.5 优化**

1. ✅ **Week 1-2：Transport 接口实现 + 核心 POC 验证**
   - 实现 Transport、Message、Stream、Channel 接口
   - **核心 POC 验证**：
     - mDNS 节点发现（局域网）
     - 节点间直连通信
     - RPC 调用（延迟 < 5ms）
   - 验收标准：核心功能单元测试通过

2. ✅ **Week 3-4：Requestor/Codec + 扩展 POC 验证**
   - 实现 Requestor、Codec 接口
   - **扩展 POC 验证**：
     - 3 节点集群通信
     - 性能基准测试（吞吐量 ≥ 10K ops/sec）
     - 连接建立时间（< 2秒）
   - 验收标准：M1 里程碑通过

### 7.2 第一阶段开发（Week 1-16）

1. ✅ **Phase 1 基础设施层**（Week 1-4）⭐ 含分阶段 POC 验证
   - 16个接口实现
   - AsyncOperation[T] 参考实现
   - M1 里程碑验收

2. ✅ **Bf-Tree 移植**（Week 1-12，与 Phase 1 并行）
   - MVP Phase 1-6
   - Week 8 检查点：未达 P0（30万）→ 启用 Badger ⭐
   - Week 12 检查点：进度评估 ⭐

3. ✅ **Phase 2 存储引擎层**（Week 5-16）
   - 9个接口实现
   - M2 里程碑验收（单机性能 P0: 30万 ops/sec）⭐

### 7.3 第二阶段开发（Week 17-26）

- Phase 3-5 实现（数据平面、控制平面、API）
- Week 18 检查点：数据平面进度评估 ⭐
- M3-M5 里程碑验收

### 7.4 第三阶段开发（Week 27-32）⭐

- Phase 6 集成测试与优化（6周缓冲期）
- M6 里程碑验收（生产就绪）

**预计开始时间**：Pre 文档 v1.5 评审通过后立即启动（Week 0 培训先行）

---

## 八、架构师评审历史

### 第一轮评审（v1.0）

- **评审日期**: 2026-02-18
- **评审结论**: 需要修改后批准
- **综合评分**: 7.7/10
- **关键问题**:
  1. Phase 2 时间线与 ADR 006 不一致（6周 vs 10-12周）
  2. 性能目标过于保守（50K vs 50万）
  3. 缺少 Bf-Tree 移植并行化计划
  4. 缺少降级触发条件

### 第二轮评审（v1.1）⭐

- **评审日期**: 2026-02-18
- **评审结论**: ✅ **已批准**
- **v1.1 修订要点**（全部完成）:
  1. ✅ 时间线对齐 ADR 006（30-32周）
  2. ✅ 性能目标分级（P0/P1/P2）
  3. ✅ Bf-Tree 移植并行化 + Week 8 检查点
  4. ✅ 分阶段交付计划（3个阶段）
  5. ✅ libp2p POC（Week 0）
  6. ✅ 缓冲时间（Phase 6: 4周 → 6周）

**批准条件**：已满足所有架构师要求，可以启动开发

### 第三轮评审（v1.4 → v1.5）⭐ 最终批准

- **评审日期**: 2026-02-18
- **评审结论**: ✅ **批准（有条件）** - 5个批准条件
- **综合评分**: 8.5/10
- **关键优势**:
  1. 5层架构设计优秀（9.5/10）
  2. 技术选型前瞻（Bf-Tree、libp2p、AsyncOperation[T]）
  3. 实施计划详细（29-31周）
  4. 迁移策略可行（Strangler Pattern）
  5. 文档质量高（9.5/10）

**5个批准条件（v1.5 已全部实现）**:
1. ✅ 调整 P0 性能目标（500K → 300K ops/sec）
2. ✅ 添加 Week 0 培训（3天）
3. ✅ 优化 POC 验证（分阶段：Week 1-2 核心 + Week 3-4 扩展）
4. ✅ 添加中期检查点（Week 12 + Week 18）
5. ✅ 保持 Phase 6 缓冲期 6周（总周期 30-32周）

**v1.5 最终批准版本**：
- ✅ 所有批准条件已实现
- ✅ 总周期：30-32周
- ✅ 性能目标：P0≥30万/P1≥50万/P2≥75万 ops/sec
- ✅ 可以立即启动开发（Week 0 培训先行）

- **评审日期**: 2026-02-18
- **评审结论**: ✅ **已批准**
- **v1.1 修订要点**（全部完成）:
  1. ✅ 时间线对齐 ADR 006（30-32周）
  2. ✅ 性能目标分级（P0/P1/P2）
  3. ✅ Bf-Tree 移植并行化 + Week 8 检查点
  4. ✅ 分阶段交付计划（3个阶段）
  5. ✅ libp2p POC（Week 0）
  6. ✅ 缓冲时间（Phase 6: 4周 → 6周）

**批准条件**：已满足所有架构师要求，可以启动开发

---

**文档版本**: v1.5 ⭐（架构师评审 v1.4 后的最终批准版本）
**创建日期**: 2026-02-18
**最后更新**: 2026-02-18
**批准日期**: 2026-02-18
**维护者**: NexKV 开发团队
**状态**: ✅ 已批准（架构师评审通过）

---

## 九、版本历史

### v1.5（2026-02-18）⭐ **最终批准版本**

**修订原因**：响应架构师对 v1.4 的评审意见（评分 8.5/10，有条件批准）

**架构师评审结论**：
- ✅ **批准（有条件）** - 5个批准条件
- **评分**：8.5/10
- **关键优势**：
  1. 5层架构设计优秀（9.5/10）
  2. 技术选型前瞻（Bf-Tree、libp2p、AsyncOperation[T]）
  3. 实施计划详细（29-31周）
  4. 迁移策略可行（Strangler Pattern）

**5个批准条件（全部实现）**：

1. ✅ **调整 P0 性能目标**：从 500K 调整到 300K ops/sec
   - **理由**：Bf-Tree 移植风险高，先以 Badger 性能作为基线更稳妥
   - **修订位置**：
     - 1.2 核心目标：P0≥30万/P1≥50万/P2≥75万 ops/sec
     - 5.2 性能基准测试：P0 单节点写入 ≥ 30万 ops/sec
     - 5.3 性能验证计划：M2 单机性能 P0 ≥ 30万 ops/sec
     - 4.1 里程碑时间表：M2 性能达标（P0≥30万 ops/sec）
     - 4.2 Bf-Tree 移植计划：Week 8 检查点（30万 ops/sec）
     - 6.2 决策 1：Week 8 检查点（30万 ops/sec）

2. ✅ **添加 Week 0 培训**：3天培训（DDD + Go 泛型 + libp2p + Bf-Tree）
   - **理由**：团队学习曲线陡峭（DDD + Go 泛型 + libp2p + Bf-Tree），需要前期培训
   - **修订位置**：
     - 2.3 实施计划：新增 Week 0 团队培训（3天）
     - 2.3 Phase 规划：Week 0（3天）详细培训内容
     - 4.1 里程碑时间表：新增 M0 里程碑（第0周）
     - 4.2 Bf-Tree 移植计划：Week 0 培训先行
     - 7.1 立即行动：Week 0 培训详细计划（Day 1-5）

3. ✅ **优化 POC 验证**：Week 1-2 核心 POC，Week 3-4 扩展验证
   - **理由**：当前 POC 与开发并行可能影响质量，需要分阶段验证
   - **修订位置**：
     - 2.3 POC 验证策略：分为 Week 1-2 核心 POC 和 Week 3-4 扩展 POC
     - 7.1 立即行动：Week 1-2 核心 POC + Week 3-4 扩展 POC

4. ✅ **添加中期检查点**：Week 12（Bf-Tree 进度）+ Week 18（控制平面进度）
   - **理由**：关键路径需要更多检查点，Bf-Tree 移植和控制平面是高风险模块
   - **修订位置**：
     - 4.1 里程碑时间表：新增 Week 12 检查点（Bf-Tree 移植进度）
     - 4.1 里程碑时间表：新增 Week 18 检查点（数据平面进度）
     - 4.2 Bf-Tree 移植计划：Week 12 检查点详细说明
     - 7.2 第一阶段开发：Week 12 检查点（进度评估）
     - 7.3 第二阶段开发：Week 18 检查点（进度评估）

5. ✅ **保持 Phase 6 缓冲期 6 周**：总周期 30-32 周
   - **理由**：v1.4 将 Phase 6 减少到 5 周太激进，需要恢复 6 周缓冲
   - **修订位置**：
     - 2.3 Phase 规划：Phase 6 从 5周恢复到 6周（Week 27-32）
     - 2.3 总体时间线：总周期从 29-31周恢复到 30-32周
     - 1.3 业务价值：30-32周分阶段交付
     - 4.1 里程碑时间表：M6 时间从第31周恢复到第32周

**修订内容汇总**：

| 修订项 | v1.4 | v1.5 | 变化 |
|--------|------|------|------|
| **P0 性能目标** | 50万 ops/sec | 30万 ops/sec | -40% ⭐ |
| **P1 性能目标** | 75万 ops/sec | 50万 ops/sec | -33% ⭐ |
| **P2 性能目标** | 100万 ops/sec | 75万 ops/sec | -25% ⭐ |
| **Week 0 培训** | 无 | 3天培训 | 新增 ⭐ |
| **POC 验证策略** | 合并到测试 | 分阶段（核心+扩展） | 优化 ⭐ |
| **中期检查点** | 无 | Week 12 + Week 18 | 新增 ⭐ |
| **Phase 6 缓冲** | 5周 | 6周 | +1周 ⭐ |
| **总周期** | 29-31周 | 30-32周 | +1周 ⭐ |

**关键改进**：
- ✅ 性能目标更务实（基于 Badger 基线，而非 Bf-Tree 理想值）
- ✅ 团队学习曲线得到缓解（Week 0 培训）
- ✅ POC 验证更系统（分阶段验证）
- ✅ 关键路径监控更严密（2个中期检查点）
- ✅ 项目缓冲更充足（Phase 6 恢复 6周）

---

### v1.4（2026-02-18）⭐

**修订原因**：移除独立 Phase 0，将 POC 验证合并到 Phase 1 测试中

**修订内容**：
1. ✅ **移除独立 Phase 0**
   - 删除 Phase 0 POC 计划文档
   - 移除 Week 0 独立验证阶段（-1周）

2. ✅ **POC 验证合并到 Phase 1**
   - **单元测试**：验证 libp2p 节点发现、连接建立、RPC 调用
   - **集成测试**：验证 3 节点集群通信、性能基准
   - **验收标准**：M1 里程碑包含 POC 验证

3. ✅ **时间线优化**
   - 总周期：30-32周 → 29-31周（-1周）
   - Phase 6 缓冲：6周 → 5周

**优势**：
- ✅ 减少前期规划时间，加快启动速度
- ✅ POC 验证与实际开发紧密结合，更贴近实际需求
- ✅ 通过单元测试和集成测试保证质量，无需单独阶段

### v1.3（2026-02-18）⭐

**修订原因**：补充 DDD 文件目录设计和现有代码迁移策略

**修订内容**：
1. ✅ **2.4 DDD 文件目录设计**
   - 当前目录结构（功能模块划分）
   - DDD 目标目录结构（5层架构，详细到文件级别）
   - 目录设计原则（DIP、SRP、接口分离、按领域划分）

2. ✅ **2.5 现有代码迁移与重构策略**
   - 迁移策略：渐进式重构（Strangler Pattern）
   - 迁移阶段规划（阶段 0-5，共 26 周）⭐ v1.4 调整为阶段 1-5
   - 迁移映射表（现有代码 → DDD 目标位置）
   - 迁移风险与缓解措施
   - 迁移检查清单

**关键改进**：
- 明确了从现有功能模块架构到 DDD 5层架构的迁移路径
- 详细的文件级别目录结构（89个实现文件）
- 分阶段迁移计划，保证系统始终可运行

### v1.2（2026-02-18）⭐

**修订原因**：明确部署环境为局域网，不支持 NAT DHT

**修订内容**：
1. ✅ **技术选型**：libp2p 说明调整为"局域网直连"
2. ✅ **风险评估**：libp2p 集成风险从"高"降低到"中"
3. ✅ **Week 0 POC**：移除 NAT 穿透验证，改为局域网节点发现
4. ✅ **决策 2**：libp2p 批准策略调整为局域网部署

**部署环境说明**：
- **当前环境**：局域网（LAN），节点间直连
- **节点发现**：静态配置或 mDNS（局域网广播）
- **不支持**：NAT 穿透、DHT 节点发现（公网环境）
- **未来扩展**：如需公网部署，后续可增加 NAT 穿透支持

### v1.1（2026-02-18）

**修订原因**：响应架构师评审意见（v1.0 评分 7.7/10）

**修订内容**：
1. ✅ 时间线调整（30-32周）
2. ✅ 性能目标分级（P0/P1/P2）
3. ✅ Bf-Tree 移植并行化
4. ✅ 分阶段交付计划

### v1.0（2026-02-18）

**创建日期**：初始版本，基于 Spike 研究成果
