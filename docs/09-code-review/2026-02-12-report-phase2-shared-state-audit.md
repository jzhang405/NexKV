# 阶段 2.1：共享状态审查

> NexKV 共享状态与并发保护机制分析

**创建时间**：2026-02-12
**分析方法**：静态代码审查

---

## 共享状态清单

### 1. 全局变量（包级）

| 文件 | 变量名 | 类型 | 用途 | 保护方式 | 风险评估 |
|------|--------|------|------|----------|----------|
| `internal/transport/message.go:271` | `payloadTypeFactories` | map | 无需保护（只读） | ✅ 安全 |
| `internal/config/logging/logger.go:11` | config 变量 | - | 只读初始化 | ✅ 安全 |
| `internal/rpc/metrics.go:16` | metrics 变量 | - | 待确认 | ⚠️ 需确认 |
| `internal/rpc/types.go:118` | 类型定义变量 | - | 只读 | ✅ 安全 |
| `internal/rpc/quorum.go:263` | `globalClusterService` | ClusterService | **⚠️ 无保护** | ❌ 高风险 |
| `internal/rpc/client.go:19` | `globalRequestID` | uint64 | **⚠️ 无保护** | ❌ 高风险 |
| `internal/metadata/types/errors.go:2109` | 错误类型变量 | - | 只读 | ✅ 安全 |

### 2. 全局变量风险评估

#### 🔴 高风险：`globalClusterService`（quorum.go:263）

```go
var globalClusterService ClusterService
```

**问题**：
- 多个 goroutine 可能同时读写此变量
- 没有锁保护
- 没有原子操作

**修复建议**：
```go
var (
    globalClusterServiceMu sync.RWMutex
    globalClusterService    ClusterService
)

func GetClusterService() ClusterService {
    globalClusterServiceMu.RLock()
    defer globalClusterServiceMu.RUnlock()
    return globalClusterService
}

func SetClusterService(s ClusterService) {
    globalClusterServiceMu.Lock()
    defer globalClusterServiceMu.Unlock()
    globalClusterService = s
}
```

#### 🔴 高风险：`globalRequestID`（client.go:19）

```go
var globalRequestID uint64
```

**问题**：
- 并发递增可能导致 ID 冲突
- 没有使用 atomic 操作

**修复建议**：
```go
var globalRequestID uint64

func nextRequestID() uint64 {
    return atomic.AddUint64(&globalRequestID, 1)
}
```

---

### 3. 结构体字段并发保护

#### ✅ TreeCoordinator（tree_coordinator.go:264）

| 字段名 | 保护方式 | 评估 |
|--------|----------|------|
| `allNodes` | `nodesMu sync.RWMutex` | ✅ 正确 |
| `metadataKV` | `metadataMu sync.RWMutex` | ✅ 正确 |
| `metadataAPI` | `metadataMu sync.RWMutex` | ✅ 正确 |
| `mvStore` | 间接保护 | ✅ 合理 |
| `state` | `atomic.Int32` | ✅ 正确 |
| `heartbeatSeq` | `atomic.Uint64` | ✅ 正确 |
| `started` | `atomic.Bool` | ✅ 正确 |
| `stopped` | `atomic.Bool` | ✅ 正确 |

**评估**：TreeCoordinator 的并发保护机制完善 ✅

---

#### ✅ HostManager（host_manager.go:23）

| 字段名 | 保护方式 | 评估 |
|--------|----------|------|
| `metadataStore` | MVStore 内部保护 | ✅ 间接保护 |
| `hosts` | `mu sync.RWMutex` | ✅ 正确 |

**评估**：HostManager 的并发保护正确 ✅

---

#### ✅ 其他模块

| 模块 | 保护方式 | 评估 |
|--------|----------|------|
| `internal/transport/key_manager` | `sync.Mutex` | ✅ 正确 |
| `internal/transport/key_mapper` | `sync.RWMutex` | ✅ 正确 |
| `internal/transport/p2p_service` | `sync.RWMutex` | ✅ 正确 |
| `internal/wal/mem_store` | `sync.Mutex` | ✅ 正确 |

---

## 观察与发现

### ✅ 设计优点

1. **defer 模式使用正确**：
   - 所有 `Lock()` 都有对应的 `defer Unlock()`
   - 避免了 panic 导致的死锁风险

2. **结构体字段保护完善**：
   - TreeCoordinator、HostManager 等核心结构体都有适当的锁保护
   - 状态字段使用 atomic 操作

3. **读写锁使用合理**：
   - 读多写少场景使用 `sync.RWMutex`
   - 写场景使用 `sync.Mutex`

### ⚠️ 发现的问题

| 优先级 | 问题 | 修复建议 |
|--------|------|----------|
| **P0** | `globalClusterService` 无并发保护 | 添加 `sync.RWMutex` 保护 |
| **P0** | `globalRequestID` 无原子操作 | 使用 `atomic.AddUint64()` |

### 📌 需要进一步确认

1. `internal/rpc/metrics.go` 中的指标变量并发保护情况
2. Gossip 模块内部状态的并发保护
3. Quorum 模块的并发安全性

---

## 下一步

→ [阶段 2.2：锁使用模式审查](phase2_lock_usage_audit.md)
