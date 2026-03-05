# NexKV 组件接口清单与优先级规划

> **文档类型**: Code Review / Architecture Review
> **创建日期**: 2026-03-05
> **版本**: v1.0
> **状态**: 📝 评审中

## 📋 评审目标

1. **组件完整性检查**: 识别 DDD 和 Storage Engine 文档中缺失的组件
2. **接口优先级分类**: 按 MUST / SHOULD / NICE-TO-HAVE / OPTIONAL 四级分类
3. **架构一致性**: 确保 V4 异步管道架构与 DDD 分层架构一致

---

## 一、组件完整性分析

### 1.1 现有组件清单

#### 基础设施层 (Infrastructure Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **Transport** | Transport, Message, RPCMessage, Stream, PubSub | DDD Interface | ✅ 完整 |
| **Codec** | Codec, StreamCodec | DDD Interface | ✅ 完整 |
| **Security** | SecurityLayer, TLSManager, NoiseManager, SecureTransport | DDD Interface | ✅ 完整 |
| **Middleware** | Middleware, MiddlewareChain, MiddlewareTransport | DDD Interface | ✅ 完整 |
| **TaskExecutor** | TaskExecutor, PerCoreExecutor | DDD Interface | ✅ 完整 |
| **GoroutineProvider** | GoroutineProvider (已弃用) | DDD Interface | ⚠️ 已弃用 |

#### 存储引擎层 (Storage Engine Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **KVStore** | KVStore, ReadOnlyStore, BatchStore, AsyncStore | M2 Interface | ✅ 完整 |
| **BTree** | BTree, BTreeConfig, Page | DDD/M2 Interface | ✅ 完整 |
| **WAL** | WAL, WALEntry | DDD/M2 Interface | ✅ 完整 |
| **Iterator** | Iterator | DDD/M2 Interface | ✅ 完整 |
| **LocalTx** | LocalTx | DDD/M2 Interface | ✅ 完整 |
| **Pipeline** | Pipeline, PipelineWorker | DDD Interface | ✅ 完整 |
| **Task** | TaskRunner, Task[Result], BaseTask | DDD Interface | ✅ 完整 |
| **CompositeTask** | CompositeWriteTask | DDD/M2 Interface | ✅ 完整 |
| **具体Task** | BTreeReadTask, BTreeWriteTask, BTreeDeleteTask, WALAppendTask | DDD/M2 Interface | ✅ 完整 |

#### 块设备层 (Block Device Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **BlockDevice** | BlockDevice | DDD/M2 Interface | ✅ 完整 |
| **LocalStorage** | LocalStorage, LocalStorageConfig | DDD/M2 Interface | ✅ 完整 |
| **CloudStorage** | CloudStorage, VersionedCloudStorage | DDD/M2 Interface | ✅ 完整 |
| **DistributedStorage** | DistributedStorage | DDD/M2 Interface | ✅ 完整 |

#### 数据平面层 (Data Plane Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **Replication** | Replicator, QuorumReplicator, ECManager, ReplicationStrategy | DDD Interface | ✅ 完整 |
| **Transaction** | TxManager, TxCoordinator | DDD Interface | ✅ 完整 |
| **AsyncOperation** | AsyncOperation[T], AsyncOp[T] | DDD Interface | ✅ 完整 |

#### 控制平面层 (Control Plane Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **Cluster** | Cluster, TreeTopology, ParentHA, Group, Membership, GossipController, FailureDetector | DDD Interface | ✅ 完整 |
| **Shard** | ShardManager, ShardRouter | DDD Interface | ✅ 完整 |
| **Partition** | Partitioner | DDD Interface | ✅ 完整 |
| **Election** | Election | DDD Interface | ✅ 完整 |
| **Balance** | LoadBalancer | DDD Interface | ✅ 完整 |
| **Broadcast** | Broadcaster | DDD Interface | ✅ 完整 |

#### API层 (API Layer)

| 组件 | 接口/结构 | 位置 | 状态 |
|------|----------|------|------|
| **Client** | KVClient, TxClient | DDD Interface | ✅ 完整 |
| **Future** | ReadFuture, WriteFuture, BatchFuture, etc. | DDD Interface | ✅ 完整 |

### 1.2 缺失组件识别

#### 🔴 MUST - 核心缺失

| 缺失组件 | 说明 | 建议位置 | 优先级 |
|---------|------|---------|--------|
| **HLCClock** | 混合逻辑时钟接口 | `internal/domain/service/clock.go` | P0 |
| **SnapshotManager** | 快照管理接口 | `internal/domain/service/snapshot.go` | P0 |
| **MetricsCollector** | 指标收集接口 | `internal/domain/service/metrics.go` | P0 |
| **HealthChecker** | 健康检查接口 | `internal/domain/service/health.go` | P0 |

#### 🟡 SHOULD - 强烈建议

| 缺失组件 | 说明 | 建议位置 | 优先级 |
|---------|------|---------|--------|
| **CacheManager** | 缓存管理接口 | `internal/domain/service/cache.go` | P1 |
| **CompactionManager** | 压缩管理接口 | `internal/domain/service/compaction.go` | P1 |
| **BackupManager** | 备份管理接口 | `internal/domain/service/backup.go` | P1 |
| **SchemaManager** | Schema 管理接口 | `internal/domain/service/schema.go` | P1 |

#### 🟢 NICE-TO-HAVE - 锦上添花

| 缺失组件 | 说明 | 建议位置 | 优先级 |
|---------|------|---------|--------|
| **QueryOptimizer** | 查询优化器 | `internal/domain/service/query.go` | P2 |
| **IndexManager** | 索引管理器 | `internal/domain/service/index.go` | P2 |
| **AuditLogger** | 审计日志 | `internal/domain/service/audit.go` | P2 |
| **RateLimiter** | 速率限制器 | `internal/domain/service/ratelimit.go` | P2 |

#### ⚪ OPTIONAL - 可选扩展

| 缺失组件 | 说明 | 建议位置 | 优先级 |
|---------|------|---------|--------|
| **PluginManager** | 插件管理器 | `internal/domain/service/plugin.go` | P3 |
| **TracingCollector** | 链路追踪收集器 | `internal/domain/service/tracing.go` | P3 |
| **AlertManager** | 告警管理器 | `internal/domain/service/alert.go` | P3 |

---

## 二、接口优先级分类

### 2.1 基础设施层 (Infrastructure Layer)

#### MUST - 核心必需

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `Transport` | 网络传输核心接口 | `internal/domain/service/transport.go` |
| `Message` | 消息接口 | `internal/domain/service/transport.go` |
| `Stream` | 流接口 | `internal/domain/service/transport.go` |
| `Codec` | 编解码器接口 | `internal/domain/service/transport.go` |
| `TaskExecutor` | 任务执行器接口 | `internal/domain/service/task.go` |
| `SecurityLayer` | 安全层接口 | `internal/domain/service/security.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `Middleware` | 中间件接口 | `internal/domain/service/middleware.go` |
| `MiddlewareChain` | 中间件链 | `internal/domain/service/middleware.go` |
| `PubSub` | 发布订阅 | `internal/domain/service/transport.go` |

#### NICE-TO-HAVE - 锦上添花

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `RateLimitMiddleware` | 限流中间件 | `internal/domain/service/middleware.go` |
| `CircuitBreakerMiddleware` | 熔断中间件 | `internal/domain/service/middleware.go` |

#### OPTIONAL - 可选扩展

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `LoggingMiddleware` | 日志中间件 | `internal/domain/service/middleware.go` |
| `MetricsMiddleware` | 指标中间件 | `internal/domain/service/middleware.go` |

### 2.2 存储引擎层 (Storage Engine Layer)

#### MUST - 核心必需

| 接口/结构 | 说明 | 文件位置 |
|----------|------|---------|
| `KVStore` | KV 存储接口 | `internal/domain/service/storage.go` |
| `BTree` | B+树接口 | `internal/domain/service/storage.go` |
| `WAL` | WAL 接口 | `internal/domain/service/storage.go` |
| `Iterator` | 迭代器接口 | `internal/domain/service/storage.go` |
| `LocalTx` | 本地事务接口 | `internal/domain/service/storage.go` |
| `TaskRunner` | 任务执行器接口 | `internal/domain/service/task.go` |
| `Task[Result]` | 泛型任务接口 | `internal/domain/service/task.go` |
| `BaseTask[Result]` | 任务基类 | `internal/domain/model/base_task.go` |
| `Pipeline` | 流水线结构 | `internal/infrastructure/storage/pipeline.go` |
| `CompositeWriteTask` | 组合写入任务 | `internal/infrastructure/storage/composite_task.go` |
| `BTreeReadTask` | BTree 读取任务 | `internal/infrastructure/storage/btree_tasks.go` |
| `BTreeWriteTask` | BTree 写入任务 | `internal/infrastructure/storage/btree_tasks.go` |
| `BTreeDeleteTask` | BTree 删除任务 | `internal/infrastructure/storage/btree_tasks.go` |
| `WALAppendTask` | WAL 追加任务 | `internal/infrastructure/storage/wal_tasks.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `ReadOnlyStore` | 只读存储接口 | `internal/domain/service/storage.go` |
| `BatchStore` | 批量存储接口 | `internal/domain/service/storage.go` |
| `AsyncStore` | 异步存储接口 | `internal/domain/service/storage.go` |
| `HLCClock` | 混合逻辑时钟 | `internal/domain/service/clock.go` |
| `SnapshotManager` | 快照管理器 | `internal/domain/service/snapshot.go` |

#### NICE-TO-HAVE - 锦上添花

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `CacheManager` | 缓存管理器 | `internal/domain/service/cache.go` |
| `CompactionManager` | 压缩管理器 | `internal/domain/service/compaction.go` |
| `BackupManager` | 备份管理器 | `internal/domain/service/backup.go` |

#### OPTIONAL - 可选扩展

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `IndexManager` | 索引管理器 | `internal/domain/service/index.go` |
| `QueryOptimizer` | 查询优化器 | `internal/domain/service/query.go` |

### 2.3 块设备层 (Block Device Layer)

#### MUST - 核心必需

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `BlockDevice` | 块设备接口 | `internal/domain/service/blockdevice.go` |
| `LocalStorage` | 本地存储接口 | `internal/domain/service/blockdevice.go` |
| `DeviceStats` | 设备统计 | `internal/domain/service/blockdevice.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `CloudStorage` | 云存储接口 | `internal/domain/service/blockdevice.go` |
| `DistributedStorage` | 分布式存储接口 | `internal/domain/service/blockdevice.go` |

#### NICE-TO-HAVE - 锦上添花

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `VersionedCloudStorage` | 版本化云存储 | `internal/domain/service/blockdevice.go` |

### 2.4 数据平面层 (Data Plane Layer)

#### MUST - 核心必需

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `Replicator` | 复制器接口 | `internal/domain/service/replication.go` |
| `QuorumReplicator` | Quorum 复制器 | `internal/domain/service/replication.go` |
| `TxManager` | 事务管理器 | `internal/domain/service/tx.go` |
| `TxCoordinator` | 事务协调器 | `internal/domain/service/tx.go` |
| `AsyncOperation[T]` | 异步操作接口 | `internal/domain/async/operation.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `ECManager` | 纠删码管理器 | `internal/domain/service/replication.go` |
| `ReplicationStrategy` | 复制策略 | `internal/domain/service/replication.go` |

### 2.5 控制平面层 (Control Plane Layer)

#### MUST - 核心必需

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `Cluster` | 集群接口 | `internal/domain/service/cluster.go` |
| `TreeTopology` | 树形拓扑 | `internal/domain/service/cluster.go` |
| `ShardManager` | 分片管理器 | `internal/domain/service/shard.go` |
| `ShardRouter` | 分片路由器 | `internal/domain/service/shard.go` |
| `FailureDetector` | 故障检测器 | `internal/domain/service/cluster.go` |
| `GossipController` | Gossip 控制器 | `internal/domain/service/cluster.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `Partitioner` | 分区器 | `internal/domain/service/partition.go` |
| `Election` | 选举接口 | `internal/domain/service/election.go` |
| `LoadBalancer` | 负载均衡器 | `internal/domain/service/balance.go` |
| `Broadcaster` | 广播器 | `internal/domain/service/broadcast.go` |
| `Membership` | 成员管理 | `internal/domain/service/cluster.go` |

#### NICE-TO-HAVE - 锦上添花

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `ParentHA` | 主备高可用 | `internal/domain/service/cluster.go` |
| `Group` | 分组管理 | `internal/domain/service/cluster.go` |

### 2.6 API层 (API Layer)

#### MUST - 核心必需

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `KVClient` | KV 客户端 | `internal/domain/service/client.go` |
| `TxClient` | 事务客户端 | `internal/domain/service/client.go` |

#### SHOULD - 强烈建议

| 接口 | 说明 | 文件位置 |
|------|------|---------|
| `ReadFuture` | 读取 Future | `internal/domain/model/futures.go` |
| `WriteFuture` | 写入 Future | `internal/domain/model/futures.go` |
| `BatchFuture` | 批量 Future | `internal/domain/model/futures.go` |

---

## 三、架构一致性检查

### 3.1 V4 异步管道与 DDD 分层一致性

| 检查项 | 状态 | 说明 |
|--------|------|------|
| **TaskRunner 在 Domain 层** | ✅ | `internal/domain/service/task.go` |
| **BaseTask 在 Model 层** | ✅ | `internal/domain/model/base_task.go` |
| **Pipeline 在 Infrastructure 层** | ✅ | `internal/infrastructure/storage/pipeline.go` |
| **具体 Task 在 Infrastructure 层** | ✅ | `internal/infrastructure/storage/*_tasks.go` |
| **依赖方向正确** | ✅ | Domain → 不依赖 Infrastructure |
| **依赖注入支持** | ✅ | Pipeline 通过构造函数注入 Executor |

### 3.2 接口命名一致性

| 检查项 | 状态 | 说明 |
|--------|------|------|
| **Manager 后缀统一** | ✅ | NodeManager, ShardManager, etc. |
| **Provider 已清理** | ✅ | 已统一改为 Manager |
| **Task 命名规范** | ✅ | XXXTask 表示具体任务 |
| **Async 命名规范** | ✅ | AsyncOperation / AsyncOp |

### 3.3 异步模式一致性

| 检查项 | 状态 | 说明 |
|--------|------|------|
| **V4 Task/Executor 为主** | ✅ | 新代码使用 V4 |
| **AsyncOperation 保留兼容** | ✅ | 旧代码逐步迁移 |
| **Future 类型统一** | ✅ | 使用泛型 Future |
| **Callback 支持** | ✅ | OnComplete/OffComplete |

---

## 四、缺失接口详细设计

### 4.1 HLCClock 接口 (MUST)

```go
// HLCClock 混合逻辑时钟接口
type HLCClock interface {
    // Now 获取当前时间
    Now() *HLC
    
    // Update 更新时钟（接收远程时间）
    Update(remote *HLC) *HLC
    
    // Compare 比较两个时间戳
    Compare(a, b *HLC) int
    
    // LessThan 判断 a < b
    LessThan(a, b *HLC) bool
    
    // Marshal 序列化
    Marshal(h *HLC) ([]byte, error)
    
    // Unmarshal 反序列化
    Unmarshal(data []byte) (*HLC, error)
}

// HLC 混合逻辑时钟结构
type HLC struct {
    PT int64   // 物理时间（毫秒）
    C  uint16  // 逻辑计数
}
```

### 4.2 SnapshotManager 接口 (MUST)

```go
// SnapshotManager 快照管理器接口
type SnapshotManager interface {
    // CreateSnapshot 创建快照
    CreateSnapshot(ctx context.Context) (SnapshotID, error)
    
    // RestoreSnapshot 恢复快照
    RestoreSnapshot(ctx context.Context, id SnapshotID) error
    
    // DeleteSnapshot 删除快照
    DeleteSnapshot(ctx context.Context, id SnapshotID) error
    
    // ListSnapshots 列出所有快照
    ListSnapshots(ctx context.Context) ([]SnapshotInfo, error)
    
    // GetSnapshotInfo 获取快照信息
    GetSnapshotInfo(ctx context.Context, id SnapshotID) (SnapshotInfo, error)
}

// SnapshotInfo 快照信息
type SnapshotInfo struct {
    ID        SnapshotID
    Timestamp time.Time
    Size      int64
    Metadata  map[string]string
}
```

### 4.3 MetricsCollector 接口 (MUST)

```go
// MetricsCollector 指标收集器接口
type MetricsCollector interface {
    // Counter 计数器
    Counter(name string, value int64, tags map[string]string)
    
    // Gauge 仪表盘
    Gauge(name string, value float64, tags map[string]string)
    
    // Histogram 直方图
    Histogram(name string, value float64, tags map[string]string)
    
    // Timer 计时器
    Timer(name string, duration time.Duration, tags map[string]string)
    
    // Flush 刷盘
    Flush(ctx context.Context) error
}
```

### 4.4 HealthChecker 接口 (MUST)

```go
// HealthChecker 健康检查接口
type HealthChecker interface {
    // Check 执行健康检查
    Check(ctx context.Context) HealthStatus
    
    // RegisterCheck 注册检查项
    RegisterCheck(name string, check HealthCheckFunc)
    
    // UnregisterCheck 注销检查项
    UnregisterCheck(name string)
}

// HealthStatus 健康状态
type HealthStatus struct {
    Status    HealthState
    Checks    map[string]HealthCheckResult
    Timestamp time.Time
}

type HealthState int
const (
    HealthUnknown HealthState = iota
    HealthHealthy
    HealthDegraded
    HealthUnhealthy
)
```

---

## 五、实施建议

### 5.1 优先级实施顺序

```
Phase 1 (MUST - P0):
├── HLCClock
├── SnapshotManager
├── MetricsCollector
└── HealthChecker

Phase 2 (SHOULD - P1):
├── CacheManager
├── CompactionManager
├── BackupManager
└── SchemaManager

Phase 3 (NICE-TO-HAVE - P2):
├── QueryOptimizer
├── IndexManager
├── AuditLogger
└── RateLimiter

Phase 4 (OPTIONAL - P3):
├── PluginManager
├── TracingCollector
└── AlertManager
```

### 5.2 接口设计原则

1. **接口优先**: 所有核心组件都定义接口，避免直接依赖具体实现
2. **类型安全**: 优先使用泛型而非 any，提供编译时类型检查
3. **生命周期完整**: 所有资源持有者必须提供 Close/CloseWithTimeout
4. **状态可见**: 异步操作应提供状态查询能力
5. **取消支持**: 长时间运行的操作应支持取消
6. **依赖倒置**: 依赖关系向内指向 Domain 层

### 5.3 文件组织建议

```
internal/domain/service/
├── storage.go          # KVStore, BTree, WAL, Iterator, LocalTx
├── blockdevice.go      # BlockDevice, LocalStorage, CloudStorage
├── task.go             # TaskRunner, Task[Result]
├── replication.go      # Replicator, QuorumReplicator
├── tx.go               # TxManager, TxCoordinator
├── cluster.go          # Cluster, TreeTopology, FailureDetector
├── shard.go            # ShardManager, ShardRouter
├── client.go           # KVClient, TxClient
├── transport.go        # Transport, Message, Stream
├── security.go         # SecurityLayer, TLSManager
├── middleware.go       # Middleware, MiddlewareChain
├── clock.go            # HLCClock (新增)
├── snapshot.go         # SnapshotManager (新增)
├── metrics.go          # MetricsCollector (新增)
└── health.go           # HealthChecker (新增)

internal/domain/model/
├── base_task.go        # BaseTask[Result]
├── futures.go          # AsyncOperation, Future types
├── hlc.go              # HLC struct (新增)
├── snapshot.go         # SnapshotInfo (新增)
├── metrics.go          # Metric types (新增)
└── health.go           # Health types (新增)
```

---

## 六、总结

### 6.1 关键发现

1. **现有组件完整性**: 80% 完整，核心组件都已定义
2. **V4 架构一致性**: 良好，Task/Executor 架构正确分层
3. **缺失组件**: 主要是可观测性相关（Metrics, Health, Snapshot）
4. **接口命名**: 统一规范，Manager 后缀一致

### 6.2 下一步行动

| 优先级 | 行动项 | 负责模块 | 预估时间 |
|--------|-------|---------|---------|
| P0 | 添加 HLCClock 接口 | Clock | 2h |
| P0 | 添加 SnapshotManager 接口 | Storage | 4h |
| P0 | 添加 MetricsCollector 接口 | Observability | 4h |
| P0 | 添加 HealthChecker 接口 | Observability | 2h |
| P1 | 添加 CacheManager 接口 | Storage | 4h |
| P1 | 添加 CompactionManager 接口 | Storage | 4h |

---

**文档版本**: v1.0
**评审人员**: DDD 专家, Architect 专家
**评审日期**: 2026-03-05
