# ADR 003: 5层 DDD 架构

**状态**: 已接受 | **日期**: 2026-02-18 | **决策者**: 架构团队

---

## 上下文（Context）

NexKV 是一个复杂的分布式 KV 系统，需要支持：

1. **分布式特性**：分片、复制、集群管理
2. **高并发**：大量并发读写请求
3. **可扩展性**：支持多种传输协议、存储引擎
4. **可测试性**：各层独立测试

**传统分层架构的问题**：
- 层次职责不清晰
- 依赖关系混乱
- 难以独立测试和演化

---

## 决策（Decision）

**采用 5层 DDD（领域驱动设计）架构**：

```
┌─────────────────────────────────────────────────┐
│  ① API层 (2个接口)                               │
│  ClientTx, KVClient                              │
│  职责：协议适配、对外接口                          │
└────────────────┬────────────────────────────────┘
                 │
┌────────────────▼────────────────────────────────┐
│  ② 控制平面层 (11个接口) 🟡 P1                   │
│  TreeCoordinator, NodeManager, TopologyManager,  │
│  HAController, MetadataStore, HeartbeatManager,  │
│  GroupManager, ShardManager, ShardRouter,        │
│  Broadcaster, SecurityLayer                      │
│  职责：分片路由、选举、分布式锁、负载均衡          │
└────────────────┬────────────────────────────────┘
                 │
┌────────────────▼────────────────────────────────┐
│  ③ 数据平面层 (6个接口) 🔴 P0                    │
│  Replicator, QuorumReplicator, ECManager,       │
│  ReplicationStrategy, TxManager, TxCoordinator  │
│  职责：复制/一致性、事务、副本管理                │
└────────────────┬────────────────────────────────┘
                 │
┌────────────────▼────────────────────────────────┐
│  ④ 存储引擎层 (9个接口) 🔴 P0                   │
│  KVStore, WAL, BTree, Iterator, LocalTx,        │
│  BlockDevice, LocalStorage, CloudStorage,       │
│  DistributedStorage                             │
│  职责：单机 KV、WAL、元数据管理                   │
└────────────────┬────────────────────────────────┘
                 │
                 └──────────────┬───────────────────┐
                                │
┌───────────────────────────────▼───────────────────┐
│  ⑤ 基础设施层 (16个接口) 🔴 P0                    │
│  Transport, Message, Stream, Channel, RPC,       │
│  Codec, Middleware, CacheLayer, CircuitBreaker,  │
│  RetryPolicy, GoroutineProvider, ...             │
│  职责：网络通信、对象存储、异步能力、扩展能力      │
└──────────────────────────────────────────────────┘
```

**核心原则**：

1. **单向依赖**：上层依赖下层，下层不依赖上层
2. **接口隔离**：每层通过接口交互
3. **职责单一**：每层专注自己的领域
4. **可替换性**：每层实现可以独立替换

---

## 理由（Rationale）

### 优势

1. **职责清晰**
   - 每层专注自己的核心职责
   - 易于理解和维护

2. **独立演化**
   - 各层可以独立开发和测试
   - 实现可以自由替换

3. **高内聚低耦合**
   - 层内接口高度相关
   - 层间依赖最小化

4. **符合 DDD 原则**
   - 领域模型清晰
   - 业务逻辑与技术实现分离

### 层次职责说明

| 层次 | 接口数 | 核心职责 | 示例 |
|------|--------|----------|------|
| **API 层** | 2 | 协议适配 | HTTP/gRPC → 内部调用 |
| **控制平面层** | 11 | 集群管理 | 分片路由、故障检测、 leader 选举 |
| **数据平面层** | 6 | 数据复制 | Raft 复制、事务协调 |
| **存储引擎层** | 9 | 数据存储 | B+tree、WAL、块存储 |
| **基础设施层** | 16 | 通用能力 | 网络通信、序列化、并发控制 |

---

## 后果（Consequences）

### 正面影响

- ✅ 架构清晰，易于理解
- ✅ 各层可独立测试
- ✅ 实现可替换（如切换 Transport）
- ✅ 支持并行开发

### 负面影响

- ⚠️ 层次较多，调用链长
- ⚠️ 需要定义更多接口
- ⚠️ 初始开发成本较高

### 性能考虑

| 关注点 | 影响 | 缓解措施 |
|--------|------|----------|
| 调用开销 | 每层增加调用开销 | 内联优化、接口合并 |
| 内存分配 | 接口包装可能增加分配 | 对象池、值类型优化 |
| 缓存局部性 | 跨层调用降低缓存命中率 | 热路径优化 |

---

## 实施细节

### 分层依赖规则

```go
// ✅ 正确：上层依赖下层
type Client struct {
    shardManager ShardManager  // 控制平面层
    replicator   Replicator    // 数据平面层
}

// ❌ 错误：下层依赖上层
type Transport struct {
    client Client  // 不允许！
}
```

### 接口定义示例

```go
// pkg/domain/service/client.go - API 层
package service

type KVClient interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
}

// pkg/domain/service/sharding.go - 控制平面层
package service

type ShardManager interface {
    GetShard(key []byte) (ShardID, error)
    ListShards() []ShardInfo
}

// pkg/domain/service/replication.go - 数据平面层
package service

type Replicator interface {
    Replicate(ctx context.Context, shard ShardID, data []byte) error
}

// pkg/domain/service/storage.go - 存储引擎层
package service

type KVStore interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
}

// pkg/domain/service/transport.go - 基础设施层
package service

type Transport interface {
    Connect(ctx context.Context, addr string) (PeerID, error)
    Send(ctx context.Context, peer PeerID, msg Message) error
}
```

### 包组织结构

```
internal/
├── domain/
│   └── service/          # 所有接口定义
│       ├── client.go      # API 层
│       ├── cluster.go     # 控制平面层
│       ├── replication.go # 数据平面层
│       ├── storage.go     # 存储引擎层
│       └── transport.go   # 基础设施层
│
├── application/           # 应用层（编排）
│   └── coordinator.go
│
└── infrastructure/        # 基础设施实现
    ├── transport/
    ├── storage/
    └── concurrent/
```

---

## 开发顺序

基于依赖关系，推荐开发顺序：

### 第一阶段：基础设施层（4周）

- Transport 实现（libp2p）
- Message 序列化（MessagePack）
- RPC 框架

### 第二阶段：存储引擎层（6周）

- Metadata KV（sync.Map）
- BfTree 实现
- WAL 实现

### 第三阶段：控制平面 + 数据平面层（8周）

- Cluster 管理
- Replication 实现
- 分片路由

### 第四阶段：API 层（2周）

- HTTP/gRPC 适配
- 客户端 SDK

---

## 接口优先级

| 优先级 | 层次 | 说明 |
|--------|------|------|
| 🔴 P0 | Transport, Storage, Replication, Client | 核心功能，必须实现 |
| 🟡 P1 | Cluster, Sharding, Transaction, BlockDevice | 重要功能，强烈建议 |
| 🟢 P2 | Middleware, SecurityLayer, Codec, Stream | 增强功能，可选 |

---

## 测试策略

### 单元测试

每层独立测试，使用 Mock 隔离依赖：

```go
// 测试 Client 层，Mock 下层
func TestKVClient_Get(t *testing.T) {
    mockShardManager := mocks.NewMockShardManager(ctrl)
    mockReplicator := mocks.NewMockReplicator(ctrl)

    client := NewClient(mockShardManager, mockReplicator)
    // ...
}
```

### 集成测试

测试跨层交互：

```go
// 集成测试：Client → Sharding → Replication → Storage
func TestIntegration_WritePath(t *testing.T) {
    // 启动完整的层次栈
    transport := NewLibp2pTransport()
    storage := NewBTreeKV()
    replicator := NewReplicator(transport, storage)
    sharding := NewShardManager(replicator)
    client := NewClient(sharding)

    // 端到端测试
    err := client.Set(ctx, key, value)
    assert.NoError(t, err)
}
```

---

## 替代方案

### 方案 A：3层架构

```
API → Business → Data
```

- ❌ 层次太少，职责不清
- ❌ 控制平面和数据平面混杂

### 方案 B：微服务架构

```
每个能力独立服务
```

- ❌ 增加运维复杂度
- ❌ 通信开销大
- ✅ 可考虑未来拆分

### 方案 C：单体无分层

```
所有代码在一起
```

- ❌ 难以维护
- ❌ 难以测试
- ❌ 无法扩展

---

## 演进路径

### 当前阶段（M2）

- 5层架构完整定义
- 核心接口实现

### 未来演进

1. **性能优化**
   - 热路径层间内联
   - 减少接口包装

2. **服务拆分**
   - 控制平面独立服务
   - 存储引擎独立服务

3. **插件化**
   - 每层支持动态加载
   - 第三方扩展

---

## 参考资料

- `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` - 47个接口详细定义
- `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md` - 实现方案
- [Domain-Driven Design](https://domainlanguage.com/ddd/)
- [Clean Architecture](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)

---

**相关 ADR**:
- [ADR 001: 双存储引擎策略](./001-dual-storage-engine.md)
- [ADR 002: 异步流水线架构](./002-async-pipeline.md)
