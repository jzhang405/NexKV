# PR-089 Pre 文档与 v4 代码同步核对清单

> **核对日期**：2026-03-06
> **Pre 文档**：`docs/06_PM/feature/2026-03-01_PR-089_m2-bftree-core_Pre.md`
> **v4 代码**：Phase 0 已完成的 Task[T] 和 Pipeline 架构
> **状态**：待用户确认后修改

---

## 一、关键差异汇总

| 差异 | Pre 文档（旧） | v4 代码（新） | 位置 | 影响 |
|------|--------------|-------------|------|------|
| **类型名称** | `AsyncOperation[T]` | `Task[Result]` | 行 151-179 | 核心接口 |
| **类型别名** | `ReadOperation` 等 | 直接用 `Task[[]byte]` | 行 175-179 | 类型别名 |
| **Pipeline 接入** | ❌ 缺少 | ✅ 需要补充 | Section 3.2 | 架构说明 |
| **接口位置** | 新建 `storage.go` | 复用 `model/task.go` | 行 134-142 | 文件位置 |

---

## 二、详细修改清单

### 修改 1：类型名称变更（行 151-179）

**当前内容**（Pre 文档）：
```go
// 异步 CRUD（复用 AsyncOperation[T]）
GetAsync(ctx context.Context, key []byte) ReadOperation
SetAsync(ctx context.Context, key, value []byte) WriteOperation
DeleteAsync(ctx context.Context, key []byte) WriteOperation

// 类型别名（复用现有 AsyncOperation[T]）
type ReadOperation = AsyncOperation[[]byte]
type WriteOperation = AsyncOperation[struct{}]
type IteratorOperation = AsyncOperation[Iterator]
type BatchGetOperation = AsyncOperation[map[string][]byte]
```

**修改为**（v4 同步）：
```go
// 异步 CRUD（复用 v4 Task[Result]）
GetAsync(ctx context.Context, key []byte) model.Task[[]byte]
SetAsync(ctx context.Context, key, value []byte) model.Task[struct{}]
DeleteAsync(ctx context.Context, key []byte) model.Task[struct{}]

// 范围查询
ScanAsync(ctx context.Context, start, end []byte) model.Task[Iterator]

// 批量操作
BatchGetAsync(ctx context.Context, keys [][]byte) model.Task[map[string][]byte]
BatchSetAsync(ctx context.Context, kvs []KeyValue) model.Task[struct{}]

// 资源管理
SyncAsync(ctx context.Context) model.Task[struct{}]
```

**说明**：
- ✅ 删除类型别名定义（行 175-179）
- ✅ 直接使用 `model.Task[T]` 泛型
- ✅ 引用 `internal/domain/model/task.go`

---

### 修改 2：添加 Pipeline 接入说明（Section 3.2）

**当前状态**：Pre 文档缺少 Pipeline 说明

**需要添加**（新章节）：
```markdown
#### 3.2.5 Pipeline 集成（v4 异步管道架构）

**v4 架构说明**：

Bf-Tree 通过 **v4 异步管道架构**（Task[Result] + Pipeline）集成异步能力：

```go
// 文件：internal/domain/service/storage.go
package service

import "github.com/jzhang405/NexKV/internal/domain/model"

// KVStore 接口（同步 + 异步）
type KVStore interface {
    // 同步 CRUD
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error

    // 异步 CRUD（返回 Task[Result]）
    GetAsync(ctx context.Context, key []byte) model.Task[[]byte]
    SetAsync(ctx context.Context, key, value []byte) model.Task[struct{}]
    DeleteAsync(ctx context.Context, key []byte) model.Task[struct{}]

    // 范围查询
    Scan(ctx context.Context, start, end []byte) (Iterator, error)
    ScanAsync(ctx context.Context, start, end []byte) model.Task[Iterator]

    // 批量操作
    BatchGet(ctx context.Context, keys [][]byte) (map[string][]byte, error)
    BatchSet(ctx context.Context, kvs []KeyValue) error
    BatchGetAsync(ctx context.Context, keys [][]byte) model.Task[map[string][]byte]
    BatchSetAsync(ctx context.Context, kvs []KeyValue) model.Task[struct{}]

    // 事务支持
    NewTx() (LocalTx, error)

    // 资源管理
    Close() error
    Sync() error
    SyncAsync(ctx context.Context) model.Task[struct{}]
}
```

**BfTree 集成 Pipeline**：

```go
// 文件：internal/infrastructure/storage/bftree/bftree.go
package bftree

import (
    "context"
    "github.com/jzhang405/NexKV/internal/domain/model"
    "github.com/jzhang405/NexKV/internal/domain/service"
)

// BfTree 实现 KVStore 接口
type BfTree struct {
    pipeline *service.Pipeline  // ✅ v4 Pipeline 引用
    config   *Config
    // ... 其他字段
}

// SetAsync 异步设置（v4 模式）
func (t *BfTree) SetAsync(ctx context.Context, key, value []byte) model.Task[struct{}] {
    // 创建 Set 任务
    task := NewBTreeSetTask(t, key, value)

    // 提交到 Pipeline（异步执行）
    err := t.pipeline.Submit(task)
    if err != nil {
        // 返回已失败的 Task
        return model.NewFailedTask[struct{}](err)
    }

    return task
}

// BTreeSetTask BTree 写入任务
type BTreeSetTask struct {
    model.BaseTask[struct{}]
    tree  *BfTree
    key   []byte
    value []byte
}

// NewBTreeSetTask 创建 BTreeSetTask
func NewBTreeSetTask(tree *BfTree, key, value []byte) *BTreeSetTask {
    return &BTreeSetTask{
        BaseTask: *model.NewBaseTask(
            model.OpStorage,
            model.TaskPriorityNormal,
            model.NewSourceStorage("bftree"),
            func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
                // 实际的 BTree 写入逻辑
                err := tree.set(ctx, key, value)
                return struct{}{}, err
            },
        ),
        tree:  tree,
        key:   key,
        value: value,
    }
}

// Execute 实现 Task[Result] 接口
func (t *BTreeSetTask) Execute(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
    return t.BaseTask.Execute(ctx, pipeline)
}
```

**CompositeWriteTask（WAL + BTree 组合）**：

```go
// CompositeWriteTask 组合写入任务（WAL + BTree）
// ✅ 关键：确保先写 WAL，再写 BTree
type CompositeWriteTask struct {
    model.BaseTask[struct{}]
    wal    WAL
    btree  *BfTree
    key    []byte
    value  []byte
}

// NewCompositeWriteTask 创建组合写入任务
func NewCompositeWriteTask(wal WAL, btree *BfTree, key, value []byte) *CompositeWriteTask {
    return &CompositeWriteTask{
        BaseTask: *model.NewBaseTask(
            model.OpStorage,
            model.TaskPriorityNormal,
            model.NewSourceStorage("composite-write"),
            func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
                // 1. 先写 WAL
                lsn, err := wal.Append(&WALEntry{
                    Type:  WALTypeInsert,
                    Key:   string(key),
                    Value: value,
                })
                if err != nil {
                    return struct{}{}, err
                }

                // 等待 WAL 持久化
                if err := wal.Sync(); err != nil {
                    return struct{}{}, err
                }

                // 2. 再写 BTree（内存）
                err = btree.set(ctx, key, value)
                return struct{}{}, err
            },
        ),
        wal:   wal,
        btree: btree,
        key:   key,
        value: value,
    }
}

// Execute 实现 Task[Result] 接口
func (t *CompositeWriteTask) Execute(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
    return t.BaseTask.Execute(ctx, pipeline)
}
```

**使用示例**：

```go
// 用户代码（API 层）
func (s *StorageService) Set(ctx context.Context, key, value []byte) error {
    // 方式 1：使用 BfTree 异步接口
    task := s.bftree.SetAsync(ctx, key, value)

    // 等待完成
    _, err := task.Wait(ctx)
    return err

    // 方式 2：使用组合任务（原子性更好）
    task := NewCompositeWriteTask(s.wal, s.bftree, key, value)
    s.pipeline.Submit(task)
    _, err := task.Wait(ctx)
    return err
}
```

**关键设计点**：
1. ✅ **复用 v4 架构**：使用 `Task[Result]` 和 `Pipeline`
2. ✅ **组合任务**：`CompositeWriteTask` 保证 WAL + BTree 原子性
3. ✅ **异步执行**：通过 `Pipeline.Submit()` 异步执行
4. ✅ **类型安全**：泛型 `Task[Result]` 提供类型安全
```

---

### 修改 3：接口定义位置（行 134-142）

**当前内容**（Pre 文档）：
```markdown
**文件位置**（Week 4.4 创建）：
- **领域层接口**：`internal/domain/service/storage.go`
- **基础设施层实现**：`internal/infrastructure/storage/bftree/bftree.go`
```

**修改为**：
```markdown
**文件位置**（Week 4.4 创建）：
- **领域层接口**：`internal/domain/service/storage.go`（KVStore、Iterator、LocalTx 接口）
- **基础设施层实现**：`internal/infrastructure/storage/bftree/bftree.go`（BfTree 实现）
- **复用 v4 组件**：
  - `internal/domain/model/task.go`（Task[Result]、BaseTask[Result]）
  - `internal/domain/service/pipeline.go`（Pipeline）

**说明**：
- ✅ 不创建新的 `AsyncOperation[T]`，直接复用 v4 的 `Task[Result]`
- ✅ 接口定义在 `storage.go`，但 Task 模型复用 `model/task.go`
```

---

### 修改 4：删除 AsyncOperation 引用（多处）

**需要删除/替换的行**：
- 行 42：`2. 异步操作接口（AsyncOperation[T]），提升吞吐量` → `2. 异步操作接口（Task[Result]），提升吞吐量`
- 行 106：`C[AsyncOperation T]` → `C[Task[Result]]`
- 行 151：`// 异步 CRUD（复用 AsyncOperation[T]）` → `// 异步 CRUD（复用 v4 Task[Result]）`
- 行 1599：``- `internal/domain/service/rpc_async.go`（AsyncOperation[T]）` → 删除此行
- 行 1600：``- `internal/infrastructure/rpc/async_impl.go`（AsyncOperation[T] 实现）` → 删除此行

---

### 修改 5：添加 v4 架构引用（Section 1）

**需要添加**（背景部分）：
```markdown
## 与 v4 架构的关系

**Phase 0 已完成**：Task[T] + Pipeline 架构

M2 Phase 2.1 Bf-Tree 实现直接复用 Phase 0 成果：
- ✅ **Task[Result]**：泛型任务模型（`internal/domain/model/task.go`）
- ✅ **Pipeline**：异步执行上下文（`internal/domain/service/pipeline.go`）
- ✅ **BaseTask[Result]**：任务基类（通用实现）
- ✅ **PerCoreExecutor**：Per-Core 无锁执行器

**集成方式**：
- BfTree 任务实现 `Task[Result]` 接口
- 通过 `Pipeline.Submit()` 异步执行
- WAL + BTree 使用 `CompositeWriteTask` 保证原子性

**参考文档**：
- `docs/07_spike/2026-03-04-spike-async-pipeline-v4.md`
```

---

## 三、修改汇总表

| 序号 | 修改类型 | 行号 | 当前内容 | 修改内容 |
|------|---------|------|---------|---------|
| 1 | 类型名称 | 151-179 | `AsyncOperation[T]` | `model.Task[Result]` |
| 2 | 删除类型别名 | 175-179 | `ReadOperation` 等 | 删除，直接用泛型 |
| 3 | 添加 Pipeline 说明 | Section 3.2 | ❌ 缺少 | ✅ 新增 Section 3.2.5 |
| 4 | 接口位置说明 | 134-142 | 新建 `storage.go` | 复用 `model/task.go` |
| 5 | 删除引用 | 42, 106, 151 | `AsyncOperation[T]` | `Task[Result]` |
| 6 | 删除参考 | 1599-1600 | `rpc_async.go` 等 | 删除 |

---

## 四、确认清单

请逐一确认以下修改：

- [ ] **修改 1**：类型名称 `AsyncOperation[T]` → `Task[Result]`
- [ ] **修改 2**：删除类型别名（`ReadOperation` 等）
- [ ] **修改 3**：添加 Section 3.2.5 Pipeline 集成说明
- [ ] **修改 4**：更新接口位置说明（复用 `model/task.go`）
- [ ] **修改 5**：删除所有 `AsyncOperation[T]` 引用
- [ ] **修改 6**：删除已废弃文件引用（`rpc_async.go`、`async_impl.go`）

---

## 五、修改优先级

| 优先级 | 修改 | 原因 |
|--------|------|------|
| **P0** | 修改 1-2 | 核心接口，必须同步 |
| **P0** | 修改 3 | 架构说明，用户理解必需 |
| **P1** | 修改 4-5 | 文档一致性 |
| **P1** | 修改 6 | 清理废弃引用 |

---

**核对完成时间**：2026-03-06
**下一步**：等待用户确认后执行修改
