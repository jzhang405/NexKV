# 阶段 1.3：职责越界检查

> NexKV 模块职责边界审查

**创建时间**：2026-02-12
**检查范围**：`internal/metadata/` 核心模块

---

## 典型反模式检查

### ❌ 反模式 1：直接访问内部数据

```go
// ❌ 反例：直接访问另一个模块的内部 map
gossip.SendUpdate(metadata.hostMap["node-1"].Status)

// ✅ 正例：通过接口访问
gossip.SendUpdate(metadata.GetHostStatus("node-1"))
```

### 检查结果

| 模块 | 检查结果 | 说明 |
|--------|----------|------|
| cluster → kvstore | ✅ 通过 | 通过 api.Provider 接口访问 |
| cluster → gossip | ✅ 通过 | 通过独立接口交互 |
| cluster → quorum | ✅ 通过 | 通过独立接口交互 |
| cluster → consistency | ✅ 通过 | 通过独立接口交互 |

---

### ❌ 反模式 2：模块间直接结构体依赖

```go
// ❌ 反例：直接导入并使用另一个模块的内部结构
import "github.com/jzhang405/NexKV/internal/metadata/kvstore"
func Process() {
    store := &kvstore.MetadataKV{}  // 直接使用具体类型
}

// ✅ 正例：通过接口依赖
import "github.com/jzhang405/NexKV/internal/metadata/api"
func Process(provider api.Provider) {
    // 使用接口，不依赖具体实现
}
```

### 检查结果

| 模块 | 检查结果 | 说明 |
|--------|----------|------|
| cluster 包 | ✅ 良好 | 通过 api.Provider 接口依赖 kvstore |
| HostManager | ✅ 良好 | 通过 store.MVStore 接口访问存储 |

---

## 具体模块检查

### 1. HostManager

**文件位置**：`internal/metadata/cluster/host_manager.go`

**依赖分析**：

```go
type HostManager struct {
    metadataStore store.MVStore  // ✅ 通过接口依赖
    hosts         map[string]*Host  // ✅ 内部状态
    mu            sync.RWMutex       // ✅ 并发保护
}
```

**评估**：

| 检查项 | 结果 | 说明 |
|--------|------|------|
| 通过接口访问存储 | ✅ 是 | 使用 store.MVStore 接口 |
| 封装内部状态 | ✅ 是 | hosts map 有锁保护 |
| 暴露必要的访问方法 | ✅ 是 | AddHost/GetHost/RemoveHost |

---

### 2. TreeCoordinator

**文件位置**：`internal/metadata/cluster/tree_coordinator.go`

**依赖分析**：

```go
// setupMetadataStorage 中的依赖
mvStore, err := store.NewMemoryMVStore(...)  // ✅ 通过工厂函数
tc.initMetadataKV(mvStore)                  // ✅ 内部初始化
```

**评估**：

| 检查项 | 结果 | 说明 |
|--------|------|------|
| 通过工厂函数创建 | ✅ 是 | 使用 store.NewMemoryMVStore |
| 不直接操作内部数据 | ✅ 是 | 通过接口方法操作 |
| 封装子模块 | ⚠️ 需确认 | 管理多个子模块，需确认边界 |

---

### 3. TreeCoordinator 元数据集成

**文件位置**：`internal/metadata/cluster/tree_coordinator_metadata.go`

**依赖分析**：

```go
import (
    "github.com/jzhang405/NexKV/internal/metadata/api"     // ✅ 接口依赖
    "github.com/jzhang405/NexKV/internal/metadata/kvstore"  // ✅ 接口依赖
    "github.com/jzhang405/NexKV/internal/metadata/types"    // ✅ 类型依赖
    metadatarpc "github.com/jzhang405/NexKV/internal/rpc"   // ⚠️ RPC 依赖
    store "github.com/jzhang405/NexKV/internal/wal"          // ✅ 接口依赖
)
```

**评估**：

| 检查项 | 结果 | 说明 |
|--------|------|------|
| 接口依赖 | ✅ 是 | 使用 api.Provider, kvstore.Store |
| 类型依赖 | ✅ 合理 | types 是共享类型定义 |
| RPC 依赖 | ⚠️ 需确认 | 可能存在职责混淆 |

---

## 上帝对象"风险评估

### TreeCoordinator

**管理的子模块**：

| 子模块 | 依赖方式 | 风险评估 |
|--------|----------|----------|
| HostManager | 内部嵌套 | ✅ 可控 |
| MetadataKV | 通过接口 | ✅ 可控 |
| Gossip | 通过接口 | ✅ 可控 |
| Quorum | 通过接口 | ✅ 可控 |
| Consistency | 通过接口 | ✅ 可控 |

**评估**：TreeCoordinator 确实管理多个子模块，但：
1. ✅ 所有交互都通过接口进行
2. ✅ 没有直接操作子模块内部数据
3. ⚠️ 需要确认是否有"协调过多"的职责

---

## 观察与发现

### ✅ 设计优点

1. **接口隔离良好**：模块间通过接口交互，不直接依赖具体实现
2. **依赖注入清晰**：通过构造函数注入依赖
3. **内部状态封装**：每个模块管理自己的内部状态

### ⚠️ 潜在问题

1. **TreeCoordinator 职责较多**：
   - 管理 HostManager、MetadataKV、Gossip、Quorum、Consistency
   - 虽然通过接口交互，但协调逻辑可能过于复杂

2. **RPC 依赖方向**：
   - cluster 包依赖 internal/rpc
   - 需要确认这是否合理（通常是 RPC 依赖业务层）

3. **Raw 方法边界**：
   - `GetRaw/PutRaw` 模糊了存储层和协议层的边界
   - 建议明确这些方法的职责归属

### 📌 建议优化

| 优先级 | 建议 | 影响 |
|--------|--------|------|
| P2 | 明确 Raw 方法的归属 | 代码清晰度 |
| P2 | 评估 TreeCoordinator 职责 | 可维护性 |
| P3 | 检查 RPC 依赖方向 | 架构清晰度 |

---

## 阶段 1 完成自检

- [x] 我能画出清晰的模块依赖图（无循环依赖）
- [x] 每个模块对外暴露的接口都有文档说明
- [x] 没有发现明显的职责越界（或已记录待修复）

---

**下一步**：→ [阶段 2：并发安全审查](2026-02-12-phase2-concurrency-safety.md)
