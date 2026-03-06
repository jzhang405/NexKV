# PR-089 Phase 2.1 代码审查报告 - 架构设计

**审查日期**：2026-03-06
**审查范围**：Phase 2.1 Bf-Tree 核心实现
**审查专家**：存储引擎架构专家
**分支**：feature/m2-bftree-phase2.1

---

## 一、综合评分

| 维度 | 评分 | 说明 |
|------|------|------|
| **架构设计** | 9.2/10 | 分层清晰，抽象合理 |
| **接口设计** | 9.5/10 | WAL 接口设计优秀 |
| **领域建模** | 9.0/10 | 聚合根、实体、值对象定义清晰 |
| **依赖方向** | 10/10 | 严格遵循 DIP |
| **扩展性** | 8.5/10 | 预留扩展点良好 |

**总体评分**：**9.2/10** - 优秀

---

## 二、架构优势分析

### 2.1 分层架构设计 ✅

**优点**：
- 严格的 DDD 分层：Domain Layer → Infrastructure Layer
- 接口抽象位于 Domain (`internal/domain/model/task.go`)
- 实现位于 Infrastructure (`internal/infrastructure/storage/`)

**验证**：
```go
// Domain Layer - 接口定义
// internal/domain/model/task.go
type Task[Result any] interface {
    Execute(ctx context.Context, pipeline PipelineContext) (Result, error)
    Wait(ctx context.Context) (Result, error)
    IsDone() bool
}

// Infrastructure Layer - 具体实现
// internal/infrastructure/storage/bftree/async.go
func (t *BfTree) GetAsync(ctx context.Context, key []byte) model.Task[[]byte] {
    return model.NewBaseTask(...)
}
```

### 2.2 WAL 接口设计 ✅

**优点**：
- 方法完备性：Append, Sync, Recover, Truncate, Close
- 同步/异步双模式支持
- 类型安全：LSN 作为强类型，避免魔术数字

**验证**：
```go
// internal/infrastructure/storage/wal/wal.go
type WAL interface {
    // 同步模式
    Append(entry *WALEntry) (LSN, error)
    Sync() error
    Recover() ([]*WALEntry, error)
    Truncate(lsn LSN) error
    Close() error

    // 异步模式（v4 架构）
    AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]
    TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]
}
```

**评价**：
- 接口设计符合 **接口隔离原则 (ISP)**
- 方法命名清晰：Append/Sync/Recover/Truncate
- 异步模式与 v4 Task[Result] 架构完美集成

### 2.3 Bf-Tree 领域建模 ✅

**聚合根**：
- `BfTree`：聚合根，管理整个树结构

**实体**：
- `LeafNode`：叶子节点实体（有状态、有版本）
- `InnerNode`：内部节点实体（有状态、有版本）

**值对象**：
- `PageLevel`：页面级别（L1-L6/Full）
- `PageType`：页面类型（Leaf/Inner）
- `DeltaOpType`：Delta 操作类型
- `LSN`：日志序列号

**验证**：
```go
// 值对象：不可变，通过值传递
type PageLevel uint8
type PageType uint8
type LSN uint64

// 实体：可变，有唯一标识和版本
type LeafNode struct {
    pageID  uint64    // 唯一标识
    version uint64    // 版本号
    // ...
}
```

**评价**：
- 符合 DDD 战术模式
- 值对象设计合理
- 实体生命周期管理清晰

### 2.4 Mini-Page 机制设计 ✅

**设计亮点**：
- 3-level 分层存储（L1-L6/Full）
- 渐进式容量提升：64B → 4KB
- 减少空间占用，提升内存利用率

**验证**：
```go
// internal/infrastructure/storage/bftree/leaf_node.go
func maxSizeForLevel(level PageLevel) uint16 {
    switch level {
    case L1:  return 64   // 1-2 个键值对
    case L2:  return 128  // 4 个键值对
    case L3:  return 256  // 8 个键值对
    case L4:  return 512  // 16 个键值对
    case L5:  return 1024 // 32 个键值对
    case L6:  return 2048 // 64 个键值对
    default: return 4096  // 128 个键值对
    }
}
```

**评价**：
- 灵活的内存管理
- 适合不同规模的数据集
- 预留未来优化空间（自动提升级别）

### 2.5 Delta Chain 优化设计 ✅

**设计亮点**：
- 写入先记录到 Delta Chain，减少写入放大
- 定期合并到 Mini-Page（Compact）
- 细粒度并发控制

**验证**：
```go
// internal/infrastructure/storage/bftree/leaf_node.go
// 查询顺序：先查 Delta Chain（最新），再查 Mini-Page
func (n *LeafNode) Get(key []byte) ([]byte, bool) {
    // 1. 先查 Delta Chain（倒序，最新优先）
    for i := len(n.deltas) - 1; i >= 0; i-- {
        // ...
    }
    // 2. 再查 Mini-Page
    // ...
}
```

**评价**：
- 优化写入性能（减少内存复制）
- 保持读取性能（倒序查找 + O(1) map）
- 合并策略清晰（长度阈值 + 大小阈值）

---

## 三、架构问题与建议

### 3.1 P2 问题：PageTable 职责边界不够清晰

**问题描述**：
`PageTable` 当前主要负责页面元数据管理（ID 分配、引用计数），但实际页面存储在 `pageStore` 中，存在职责分散。

**代码示例**：
```go
// internal/infrastructure/storage/bftree/bftree.go
type BfTree struct {
    pageTable *pageStore // 页面表管理器
    pageStore *pageStore // 页面存储（MVP：内存存储）
}
```

**建议**：
- 未来重构时，考虑将 `PageTable` 和 `pageStore` 合并
- 或明确职责边界：`PageTable` 负责元数据，`pageStore` 负责数据

**优先级**：P2（优化建议）

### 3.2 P2 问题：Delta Chain 合并策略可配置性不足

**问题描述**：
当前 Delta Chain 合并策略是硬编码的（长度阈值 8，大小阈值 50%），未来可能需要动态调整。

**代码示例**：
```go
// internal/infrastructure/storage/bftree/leaf_node.go
func NewLeafNode(pageID uint64, level PageLevel) *LeafNode {
    return &LeafNode{
        maxDeltaLen:  8,                                  // 硬编码
        maxDeltaSize: uint16(maxSizeForLevel(level) / 2), // 硬编码
    }
}
```

**建议**：
- 将合并策略参数化（通过 Config 传入）
- 支持自适应合并策略（根据访问模式调整）

**优先级**：P2（优化建议）

### 3.3 P1 问题：异步模式是简化实现

**问题描述**：
当前异步方法（GetAsync, SetAsync 等）实际上是同步执行的（直接调用 task.Run()），不是真正的异步。

**代码示例**：
```go
// internal/infrastructure/storage/bftree/async.go
func (t *BfTree) GetAsync(ctx context.Context, key []byte) model.Task[[]byte] {
    return model.NewBaseTask(
        model.OpStorage,
        model.TaskPriorityNormal,
        model.NewSourceShard("bftree"),
        func(ctx context.Context, pipeline model.PipelineContext) ([]byte, error) {
            return t.Get(ctx, key) // 直接调用同步方法
        },
    )
}

// 测试中需要手动调用 task.Run()
task.Run(context.Background(), nil) // MVP 简化
```

**影响**：
- 不符合真正的异步语义
- 无法利用 Pipeline 并发执行

**建议**：
- 在文档中明确标注这是 MVP 简化实现
- 未来 Phase 2.2/2.3 集成 Pipeline

**优先级**：P1（需要说明）

---

## 四、依赖方向验证 ✅

### 4.1 依赖关系图

```
┌─────────────────────────────────────────┐
│         Domain Layer                    │
│  ┌─────────────────────────────────┐   │
│  │  internal/domain/model          │   │
│  │  - task.go (Task[Result] 接口)  │   │
│  │  - source_id.go (SourceID 值对象)│   │
│  └─────────────────────────────────┘   │
└─────────────────────────────────────────┘
                    ▲
                    │ 依赖
                    │
┌─────────────────────────────────────────┐
│      Infrastructure Layer               │
│  ┌─────────────────────────────────┐   │
│  │  internal/infrastructure/storage │   │
│  │  /wal/wal.go (WAL 接口)         │   │
│  │  /bftree/async.go (异步实现)     │   │
│  └─────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

**验证**：
- ✅ Infrastructure 依赖 Domain（通过 `import "github.com/jzhang405/NexKV/internal/domain/model"`）
- ✅ Domain 不依赖 Infrastructure
- ✅ 严格遵循 **依赖倒置原则 (DIP)**

---

## 五、扩展性评估

### 5.1 良好的扩展点

1. **WAL 接口扩展**：
   - 可以轻松添加新的 WAL 实现（Memory WAL, Distributed WAL）
   - 接口方法完备，支持未来需求

2. **Mini-Page 级别扩展**：
   - 当前支持 L1-L6/Full
   - 可以轻松添加新的级别（如 L0, L7）

3. **Delta Chain 策略扩展**：
   - 当前有独立 `DeltaChain` 结构
   - 可以轻松实现不同的合并策略

### 5.2 未来扩展建议

1. **InnerNode 分裂**：
   - 当前 InnerNode 已实现基础结构
   - 预留 `putInner` 方法（`//lint:ignore U1000`）
   - 未来需要实现节点分裂逻辑

2. **并发控制升级**：
   - 当前使用 `sync.RWMutex`（MVP）
   - 未来可升级到 `BitmapLock`（细粒度锁）

3. **持久化支持**：
   - 当前 `pageStore` 是纯内存（MVP）
   - 未来需要添加磁盘持久化

---

## 六、架构总结

### 6.1 核心优势

1. **分层清晰**：DDD 分层架构，职责明确
2. **接口优秀**：WAL 接口设计符合 SOLID 原则
3. **领域建模**：聚合根、实体、值对象定义清晰
4. **依赖正确**：严格遵循 DIP
5. **扩展性好**：预留扩展点，支持未来演进

### 6.2 改进空间

1. **职责边界**：PageTable 和 pageStore 职责可以更清晰
2. **异步实现**：当前是 MVP 简化，需要文档说明
3. **配置化**：Delta Chain 合并策略可以参数化

### 6.3 最终结论

**Phase 2.1 架构设计优秀，可以继续 Phase 2.2 开发。**

**评分**：9.2/10（优秀）

---

**审查人**：存储引擎架构专家
**审查日期**：2026-03-06
