# 【PR全流程文档】Feature - TaskScheduler 结果返回机制设计

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 0. 代码审查结果（2026-03-21 最终确认）

> **重要说明**：本文档在 2026-03-21 经过了与实际代码的对比审查，确认了以下3个核心问题需要在开发前解决。
>
> **审查方法**：
> - 读取实际代码：`internal/domain/model/task.go`
> - 读取实际代码：`internal/infrastructure/concurrency/types.go`
> - 读取实际代码：`internal/infrastructure/concurrency/shard_item.go`
> - 对比原始设计文档：`thoughts/task-scheduler-result-return-discussion.md`

#### 🔴 核心实施任务（P0 - 按文档直接实施）

**问题 1：ShardItem 接口未采用组合设计**

| 项目 | 文档设计 | 实际代码 | 状态 |
|------|----------|----------|------|
| 接口设计 | 组合 TaskRunner + TaskResult | 只有 3 个独立方法 | ❌ 不匹配 |
| TaskRunner | 应嵌入 ShardItem | 未嵌入 | ⚠️ 待实现 |
| TaskResult | 应嵌入 ShardItem | 未嵌入 | ⚠️ 待实现 |

**当前实际代码**（`internal/infrastructure/concurrency/shard_item.go:17-33`）：
```go
type ShardItem interface {
    ShardID() int     // 分片 ID（用于 CPU core 绑定）
    MaxRetries() int  // 最大重试次数
    IncAttempts() int // 增加尝试次数
    // ❌ 没有嵌入 model.TaskRunner
    // ❌ 没有嵌入 model.TaskResult
}
```

**目标设计**（接口组合）：
```go
type ShardItem interface {
    model.TaskRunner  // 嵌入：Run, Priority, SourceID
    model.TaskResult  // 嵌入：Done, Wait, Status, IsDone

    ShardID() int
    MaxRetries() int
    IncAttempts() int
}
```

**设计优势**：
- ✅ 代码行数减少 50%（~20 行 → ~10 行）
- ✅ BTreeSetItem 嵌入 BaseTask 后自动满足 ShardItem 接口
- ✅ 无需手动实现 Run(), IsDone(), Done(), Wait() 等方法
- ✅ 可维护性提升：BaseTask 变更自动传播

**实施步骤**：
1. 更新 `shard_item.go`，嵌入 TaskRunner 和 TaskResult 接口
2. 确保 BaseTask 实现了 TaskRunner 和 TaskResult（已实现 ✅）
3. 验证 BTreeSetItem 嵌入 BaseTask 后自动满足 ShardItem 接口

**实施难度**：🟢 低 - 按文档直接修改即可

---

**问题 2：BaseTask 字段完全缺失 + 状态枚举统一**

**状态**：✅ 可按文档直接实施

| 字段 | 文档要求 | 实际代码 | 状态 |
|------|----------|----------|------|
| `id string` | ✅ 需要添加 | ❌ 不存在 | ✅ 按文档添加即可 |
| `createdAt time.Time` | ✅ 需要添加 | ❌ 不存在 | ✅ 按文档添加即可 |
| `retryCount int` | ✅ 需要添加 | ❌ 不存在 | ✅ 按文档添加即可 |
| `maxRetries int` | ✅ 需要添加 | ❌ 不存在 | ✅ 按文档添加即可 |
| `opType OpType` | ❌ 需要删除 | ✅ 存在（task.go:170） | ✅ 按文档删除即可 |
| `status 类型` | ✅ 改为 TaskStatus | 当前是 OperationStatus | ✅ 需要统一 |

**重要补充：状态枚举统一**

**当前代码**：
```go
status atomic.Int32  // OperationStatus
Status() OperationStatus
```

**目标设计**：
```go
status atomic.Int32  // TaskStatus（统一枚举）
Status() TaskStatus     // 统一返回 TaskStatus
```

**影响范围**：
1. `BaseTask.status` 字段类型改为 `TaskStatus`（atomic.Int32 存储）⚠️ **重要：字段类型必须修改**
2. `BaseTask.Status()` 方法返回值改为 `TaskStatus`
3. `TaskResult.Status()` 方法返回值改为 `TaskStatus`
4. 删除或废弃 `OperationStatus` 枚举

**当前实际代码**（`internal/domain/model/task.go:168-185`）：
```go
type BaseTask[Result any] struct {
    opType   OpType         // ⚠️ 需要删除
    priority TaskPriority
    sourceID SourceID
    execute  ExecuteFunc[Result]
    done     chan struct{}
    status   atomic.Int32    // ⚠️ OperationStatus，需要改为 TaskStatus
    result   Result
    err      error
    mu       sync.RWMutex
    // ❌ 缺少：id
    // ❌ 缺少：createdAt
    // ❌ 缺少：retryCount
    // ❌ 缺少：maxRetries
}
```

**影响**：
- 无法实现 ShardItem 接口的 `MaxRetries()` 和 `IncAttempts()` 方法
- 无法支持重试机制

**实施步骤**：
1. 添加 4 个新字段：id, createdAt, retryCount, maxRetries
2. 删除 opType 字段
3. 更新构造函数签名（移除 opType 参数，添加 maxRetries 参数）
4. 实现 `MaxRetries()` 和 `IncAttempts()` 方法
5. 修改 5 处 RPCTask 调用（移除 model.OpRPC 参数）

---

**问题 3：TaskBusy 状态缺失**

**状态**：✅ 可按文档直接实施

| 状态 | 文档要求 | 实际代码 | 状态 |
|------|----------|----------|------|
| `TaskBusy` | ✅ 需要添加 | ❌ 不存在 | ✅ 按文档添加即可 |

**当前实际代码**（`internal/infrastructure/concurrency/types.go:18-31`）：
```go
const (
    TaskQueued    TaskStatus = iota  // ✅ 已存在
    TaskExecuting                     // ✅ 已存在
    TaskPassed                        // ✅ 已存在
    TaskFailed                        // ✅ 已存在
    TaskRetrying                      // ✅ 已存在
    TaskTimeout                       // ✅ 已存在
    // ❌ 缺少：TaskBusy（用于锁冲突、资源繁忙场景）
)
```

**使用场景**：TaskBusy 用于处理锁冲突、资源繁忙等可重试的瞬时错误

**实施难度**：🟢 低 - 按文档直接添加即可

**状态转换逻辑**：
```go
// 目标设计
const (
    TaskQueued    TaskStatus = iota
    TaskExecuting
    TaskPassed
    TaskFailed
    TaskTimeout
    TaskBusy      // 🆕 新增：繁忙/资源冲突（可重试）
    TaskRetrying
)
```

**实施步骤**：
1. 在 `types.go` 中添加 `TaskBusy` 状态定义
2. 更新 `TaskStatus.String()` 方法，添加 TaskBusy case 分支
3. 在 executeFunc 中实现 TaskBusy 返回逻辑（处理 ErrRetry 错误）
4. 在 runLoop 的 switch 语句中添加 TaskBusy 分支（保留重试）

---

#### 📊 问题汇总表（包含状态枚举统一）

| 问题 | 影响范围 | 修改文件 | 工作量 | 实施难度 |
|------|----------|----------|--------|----------|
| 1. ShardItem 接口未组合 | 所有 ShardItem 实现 | `shard_item.go` | 小 | 🟢 低 |
| 2. BaseTask 字段缺失 | 所有 BaseTask 使用者 | `task.go`, `rpc/task.go` 等 | 大 | 🟡 中 |
| 2a. 状态枚举统一 | 所有 TaskResult 实现 | `task.go`, `types.go` | 中 | 🟡 中 |
| 3. TaskBusy 状态缺失 | executeFunc, runLoop | `types.go`, `task_scheduler.go` | 中 | 🟢 低 |

**总结**：✅ 所有问题均可按文档直接实施，无需额外架构决策

**重要补充**：问题 2a（状态枚举统一）是问题 2 的扩展，需要将 OperationStatus 统一为 TaskStatus

---

#### 🎯 实施顺序（按文档执行）

**第一阶段：基础接口和状态**（2-3天）
1. 更新 ShardItem 接口采用组合设计
2. 添加 TaskBusy 状态

**第二阶段：BaseTask 重构**（3-5天）
3. 添加缺失字段（id, createdAt, retryCount, maxRetries）
4. 实现重试管理方法（MaxRetries, IncAttempts）
5. 更新构造函数（NewBaseTaskWithRetry）
6. 修改 RPCTask 调用（删除 opType 参数）

**第三阶段：状态枚举统一与 TaskResult 接口扩展**（2-3天）
7. **统一状态枚举**：将 TaskResult.Status() 返回值从 OperationStatus 改为 TaskStatus
8. **扩展 TaskResult 接口**：添加 GetError() 方法
9. **在 BaseTask 中实现**：GetError() 方法，更新 Status() 返回类型
10. **更新 executeFunc**：使用 GetError() 获取错误信息

**第四阶段：集成实现**（3-5天）
11. 更新 runLoop 重试逻辑
12. 实现 BTreeSetItem
13. 集成测试

---

#### ✅ 已确定的方案（架构师已决策）

**方案确定：扩展 TaskResult 接口添加 GetError() 方法**

**当前 TaskResult 接口**（`internal/domain/model/task.go`）：
```go
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)
    Status() OperationStatus  // ⚠️ 需要改为 TaskStatus
    IsDone() bool
    // ❌ 缺少 GetError() 方法
}
```

**目标设计**（方案 A：统一状态枚举 + 扩展 GetError）：
```go
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)
    Status() TaskStatus    // ✅ 改为：返回统一的 TaskStatus（而非 OperationStatus）
    IsDone() bool
    GetError() error      // ✅ 新增：获取错误信息
}
```

**设计理由**：
- ✅ **统一状态枚举**：TaskScheduler 使用 TaskStatus，TaskResult 也返回 TaskStatus
- ✅ **语义清晰**：GetError() 直观地表达"获取错误"
- ✅ **简洁直接**：executeFunc 可以直接调用 `shardItem.GetError()`
- ✅ **符合 Go 惯例**：类似 `errors.As` 的错误获取模式
- ✅ **避免状态转换**：不需要在 OperationStatus 和 TaskStatus 之间转换

**BaseTask 需要实现**：
```go
func (t *BaseTask[Result]) GetError() error {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.err
}
```

**executeFunc 实现**：
```go
func (m *TaskScheduler) executeFunc(item any) TaskStatus {
    shardItem := item.(ShardItem)

    // 调用 Run() 执行任务
    // 说明：ShardItem 通过嵌入 model.TaskRunner 接口自动获得 Run() 方法
    //      这是 Go 接口组合（Interface Embedding）的特性，无需特殊处理
    shardItem.Run(context.Background(), nil)

    // 检查执行结果
    // 说明：IsDone() 和 GetError() 来自嵌入的 model.TaskResult 接口
    if shardItem.IsDone() {
        // ✅ 直接调用 GetError() 获取错误信息
        err := shardItem.GetError()
        return m.determineTaskStatus(err)
    }

    return TaskFailed
}
```

**接口组合说明**：
- `ShardItem` 接口嵌入了 `TaskRunner` 和 `TaskResult` 接口
- 接口嵌入会自动继承被嵌入接口的所有方法集
- 因此 `shardItem.Run()`、`shardItem.IsDone()`、`shardItem.GetError()` 都是合法调用
- 详细说明参见：`thoughts/interface-embedding-explanation.md`

**实施步骤**：
1. 在 `model/task.go` 中扩展 TaskResult 接口
2. 在 BaseTask 中实现 GetError() 方法
3. 更新 executeFunc 使用 GetError()

---

#### 🟡 其他设计问题（P2 - 后续优化）

---

**问题 5：opType 字段删除确认**

**决策**：✅ 在本 PR 中删除 opType 字段（包含 5 处 RPCTask 调用修改）

**理由**：
- ✅ opType 字段是死代码（从未被读取）
- ✅ 从旧 `AsyncOp` 模式迁移时的遗留字段
- ✅ 删除后简化 API，提高代码质量
- ✅ 与 BaseTask 重构一起完成，避免分两次修改

**影响范围**（`internal/infrastructure/rpc/task.go`）：
```go
// 需要修改的 5 处调用：
// 1. NewRPCCallTask：移除 model.OpRPC 参数
// 2. NewRPCBroadcastTask：移除 model.OpRPC 参数
// 3. NewRPCQuorumTask：移除 model.OpRPC 参数
// 4. NewRPCWriteVTask：移除 model.OpRPC 参数
// 5. NewRPCRecoveryTask：移除 model.OpRPC 参数
```

**实施步骤**：
1. 在 `model/task.go` 中删除 opType 字段和 OpType 类型定义
2. 更新 NewBaseTask 构造函数，移除 opType 参数
3. 修改 `rpc/task.go` 中 5 处 RPCTask 调用
4. 运行完整测试套件验证修改正确性

---

#### 📊 审查结论

| 类别 | 数量 | 说明 |
|------|------|------|
| ✅ 核心实施任务 | 3 | 可按文档直接实施 |
| ✅ 已确定方案 | 2 | TaskResult 扩展、opType 删除 |
| ✅ 设计正确 | - | 接口组合、BaseTask 重试管理等 |

**结论**：✅ 所有问题和方案均已明确，可按文档开始实施

---

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 新功能开发（Feature） |
| PR编号 | PR-待分配（创建GitHub PR后补充完整） |
| 分支名称 | feature/2026-03-21-task-scheduler-result-return |
| 工作主题 | TaskScheduler 结果返回机制设计与实现 |
| 负责人 | jzhang405 |
| 分支创建日期 | 2026-03-21 |
| 计划开工日期 | 待定 |
| 计划CI通过日期 | 待定 |
| 关联需求单号 | 内部优化需求 |
| 架构师评审状态 | □ 待评审 □ 评审中 □ 评审通过 □ 需优化（循环记录） |
| 预审批结果 | □ 未通过 □ 已通过（架构师签字/备注：_________ 202X-XX-XX 同意开工） |

### 2. 背景与目标（为什么干）

#### 2.1 背景

**业务场景**：
- BTree 存储引擎需要支持异步写入操作
- 高并发场景下，同步写入会成为性能瓶颈
- 需要一种机制让调用方提交任务后能够获取执行结果

**现有问题**：
- `TaskScheduler` 的 `executeFunc` 返回 `TaskStatus`，但调用方无法获取任务的执行结果
- 调用方提交任务后，无法知道任务是成功还是失败
- 缺少异步任务的结果反馈机制
- BTree.Set 仍然是同步实现，无法利用 TaskScheduler

**价值**：
- 提供统一的异步任务执行和结果获取机制
- 复用现有的 `BaseTask[Result]` 能力
- 支持 BTree 等模块的异步化改造

#### 2.2 核心目标（可量化、可验证）

1. **功能目标**：
   - 实现 `ShardItem` 与 `BaseTask` 的集成机制
   - 支持 `executeFunc` 调用 `ShardItem.Run()` 并检查 `IsDone()`
   - 扩展 `TaskStatus` 状态机，添加 `TaskBusy` 状态
   - 实现 `BTreeSetItem` 支持异步 Set 操作

2. **性能目标**：
   - 不影响现有 TaskScheduler 的调度性能
   - 异步任务提交延迟 < 1μs
   - 结果获取延迟与同步方式相当

3. **可用性目标**：
   - 保持向后兼容，不破坏现有 RPC 任务
   - 错误处理机制完善，支持重试和超时

#### 2.3 明确边界（不做什么，避免范围蔓延）

**状态枚举迁移说明**：
- ✅ **本次 PR**：将 `TaskResult.Status()` 返回值从 `OperationStatus` 改为 `TaskStatus`
- ✅ **本次 PR**：将 `BaseTask.status` 字段类型从 `OperationStatus` 改为 `TaskStatus`
- ⚠️ **OperationStatus 枚举**：暂时保留（可能被其他模块使用），后续单独清理
- ✅ **目标**：统一所有任务状态为 `TaskStatus`，简化状态管理

**本次不支持**：
- ❌ RPC 任务的 ShardTask 改造（后续独立任务）
- ❌ 其他模块的异步化改造
- ❌ TaskScheduler 调度算法优化
- ❌ 删除 OperationStatus 枚举定义（影响范围未评估）

**本次不优化**：
- ⚠️ TaskScheduler 的性能调优
- ⚠️ BaseTask 的内存占用优化
- ⚠️ executeFunc 的执行路径优化

### 3. 实现方案（怎么干，核心设计）

#### 3.1 整体流程设计

```mermaid
flowchart TD
    A[调用方提交 ShardItem] --> B[TaskScheduler.EnqueueWithShard]
    B --> C[根据 shardID 分发到 runLoop]
    C --> D[runLoop 队列]
    D --> E[executeFunc 执行]
    E --> F[调用 ShardItem.Run]
    F --> G[检查 IsDone]
    G --> H{任务状态}
    H -->|TaskPassed/Failed| I[Dequeue 出队]
    H -->|TaskRetrying/Timeout/Busy| J[保留队列重试]
    I --> K[调用方可通过 Wait 获取结果]
    J --> D
```

#### 3.2 关键设计点

##### 1. 接口定义与实现

**ShardItem 接口设计**（目标：接口组合）：

**当前实现**（`internal/infrastructure/concurrency/shard_item.go`）：
```go
type ShardItem interface {
    ShardID() int           // 分片 ID（用于 CPU core 绑定）
    MaxRetries() int        // 最大重试次数
    IncAttempts() int       // 增加尝试次数
}
```

**目标设计**（接口组合）：
```go
// internal/infrastructure/concurrency/shard_item.go

// ShardItem 带重试控制、按 CPU 核心分片的任务项接口
type ShardItem interface {
    // ===== TaskRunner 接口（调度能力）=====
    model.TaskRunner  // 嵌入：Run, Priority, SourceID

    // ===== TaskResult 接口（结果能力）=====
    model.TaskResult  // 嵌入：Done, Wait, Status, IsDone

    // ===== ShardItem 特有能力 =====
    ShardID() int
    MaxRetries() int
    IncAttempts() int
}
```

**对比优势**：

| 方面 | 当前实现 | 目标设计（接口组合） |
|------|----------|---------------------|
| 代码行数 | ~20 行 | ~10 行 |
| 冗余度 | 高（重复列出方法） | 低（组合复用） |
| 可维护性 | ⚠️ BaseTask 变更需同步修改 | ✅ 自动继承 |
| 语义清晰度 | ⚠️ 方法列表过长 | ✅ 分类清晰 |

**BaseTask 已有接口**（`internal/domain/model/task.go`）：
```go
// TaskRunner 接口
type TaskRunner interface {
    Run(ctx context.Context, pipeline PipelineContext) error
    Priority() TaskPriority
    SourceID() SourceID
}

// TaskResult 接口
// ⚠️ 当前实现（需要修改）
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)  // ⚠️ 当前：返回 (Result, error)
    Status() OperationStatus  // ⚠️ 当前：返回 OperationStatus
    IsDone() bool
    // ❌ 缺少 GetError() 方法
}

// ✅ 目标设计（本 PR 实现）
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)  // ✅ 保持现有签名
    Status() TaskStatus    // ✅ 统一为 TaskStatus
    IsDone() bool
    GetError() error      // ✅ 新增方法
}
```

**BaseTask 实现 ShardItem 的重试管理方法**：
```go
// internal/domain/model/task.go

func (t *BaseTask[Result]) MaxRetries() int {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.maxRetries
}

func (t *BaseTask[Result]) IncAttempts() int {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.retryCount++
    return t.retryCount
}

// 注意：ShardID() 由具体业务类型实现（如 BTreeSetItem）
// 因为 ShardID 的语义取决于业务逻辑（如 leafRef 的 PageID）
```

**优势**：
- 所有嵌入 BaseTask 的类型自动获得完整的 ShardItem 接口实现
- 避免重复实现 TaskRunner 和 TaskResult 的所有方法
- retryCount 由 BaseTask 统一管理，保证数据一致性

**TaskRunner 接口**（需要调整）：
```go
// 当前：使用 PipelineContext
type TaskRunner interface {
    Run(ctx context.Context, pipeline PipelineContext) error
    Priority() TaskPriority
    SourceID() SourceID
}

// 目标：重命名为 ExecContext
type TaskRunner interface {
    Run(ctx context.Context, execCtx ExecContext) error
    Priority() TaskPriority
    SourceID() SourceID
}
```

##### 2. TaskScheduler.EnqueueWithShard 接口

**接口签名**：
```go
// EnqueueWithShard 根据 ShardID 分发任务到对应核心
func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error
```

**参数说明**：
- `item ShardItem`：要提交的任务项，必须实现 ShardItem 接口
- `taskName string`：**目标任务名称**，用于从指定 Core 获取对应的队列（如 `"btree-set"`）

**多队列架构**（重要！）：

每个 `SchedulerCore` 内部维护多个**命名的队列**（`taskMap`），runLoop 按注册顺序处理：

```
TaskScheduler
├── Core-0 (CPU 0)
│   ├── taskMap: {
│   │     "btree-set":  ShardTask{queue: [...]},  ← 按 executionOrder 排序
│   │     "wal-append": ShardTask{queue: [...]},
│   │     "compaction": ShardTask{queue: [...]},
│   │   }
│   └── runLoop() → 循环处理所有队列（按 executionOrder）
│
├── Core-1 (CPU 1)
│   ├── taskMap: {
│   │     "btree-set":  ShardTask{queue: [...]},  ← 独立队列
│   │     "wal-append": ShardTask{queue: [...]},
│   │     "compaction": ShardTask{queue: [...]},
│   │   }
│   └── runLoop()
│
└── Core-N ...
```

**EnqueueWithShard 流程**：

```go
// 1. 根据 ShardID 确定 Core
coreIndex := item.ShardID() % m.coreCount
core := m.cores[coreIndex]

// 2. 从该 Core 的 taskMap 获取对应队列
task, err := core.GetTaskByName(taskName)  // ← taskName 在这里使用

// 3. 入队到该队列
task.Enqueue(item)

// 4. 唤醒该 Core 的 runLoop
core.wakeup()
```

**注册与使用流程**：

**步骤 1：注册任务**（初始化时）
```go
// 注册到所有 Core（每个 Core 创建独立的同名队列）
err := scheduler.RegisterTask(
    executeFunc,              // 执行函数
    "btree-set",              // ← taskName
    model.TaskPriorityNormal, // 优先级
    1,                        // executionOrder（执行顺序）
)
```

注册后，每个 Core 都有自己的 `"btree-set"` 队列：
- Core-0.taskMap["btree-set"]
- Core-1.taskMap["btree-set"]
- Core-N.taskMap["btree-set"]

**步骤 2：提交任务**（运行时）
```go
// 提交到指定 Core 的 "btree-set" 队列
err := scheduler.EnqueueWithShard(item, "btree-set")
//                                        ↑
//                                  指定队列名称
```

**为什么要用 taskName？**

1. **多队列支持**：一个 Core 可以处理多种任务类型（BTree Set、WAL Append、Compaction 等）
2. **优先级隔离**：不同类型的任务在不同的队列，互不影响
3. **执行顺序控制**：通过 `executionOrder` 控制队列的处理顺序（例如：先处理 WAL，再处理 BTree）

**示例：多任务注册**
```go
// 注册顺序决定了处理优先级
scheduler.RegisterTask(walExecuteFunc, "wal-append", HighPriority, 1)
scheduler.RegisterTask(btreeExecuteFunc, "btree-set", NormalPriority, 2)
scheduler.RegisterTask(compactExecuteFunc, "compaction", LowPriority, 3)

// 每个 Core 的 runLoop 按顺序处理：wal-append → btree-set → compaction
```

**路由逻辑**（基于 `item.ShardID()`）：
```go
shardID := item.ShardID()
var coreIndex int

if shardID == 0 {
    // 无偏好：动态选择负载最小的 Core
    coreIndex = m.selectLeastLoadedCore()
} else if shardID > 0 {
    // 固定路由：取模计算
    coreIndex = shardID % m.coreCount
} else {
    // 负数：取绝对值后取模
    coreIndex = (-shardID) % m.coreCount
}
```

**使用示例**：
```go
// 创建 BTreeSetItem
item := NewBTreeSetItem(btree, leafRef, key, value, 3)

// 提交到 TaskScheduler
err := btree.scheduler.EnqueueWithShard(item, "btree-set")
if err != nil {
    return err
}

// 等待完成
err = item.Wait(ctx)
```

**ShardID 路由规则**：

| ShardID 值 | 路由策略 | 使用场景 |
|-----------|---------|---------|
| `0` | 动态选择负载最小的 Core | 纯计算任务、无共享状态 |
| `> 0` | 固定路由到 `shardID % coreCount` | BTree leaf 操作、WAL append |
| `< 0` | 取绝对值后取模 | 备用方案 |

**BTree.Set 场景示例**：
```go
// BTreeSetItem.ShardID() 返回 leaf 的 PageID + 1
// 为什么用 PageID 而不是指针地址？
// 1. PageID 是逻辑标识符，同一 leaf 的所有操作使用相同的 PageID
// 2. 指针地址会变（PageRef 对象可能重建），导致同一 leaf 路由到不同 Core
// 3. PageID + 1 确保 shardID > 0（0 表示无固定偏好）
func (item *BTreeSetItem) ShardID() int {
    info := item.leafRef.GetPageInfo()
    if info == nil {
        return 0  // 无固定偏好
    }
    // 同一 leaf 的操作会被路由到同一个 Core
    return int(info.GetPageID()) + 1
}
```

**错误定义**：
```go
// internal/pkg/errors/errors.go
var ErrRetry = errors.New("retry: temporary error, should retry")

// 使用示例
if errors.Is(err, ErrRetry) {
    return TaskBusy
}
```

##### 3. 核心机制：executeFunc 实现

**当前实现**（不完整）：
```go
// executeFunc 只返回状态，没有调用 ShardItem.Run()
func (m *TaskScheduler) executeFunc(item any) TaskStatus {
    // 直接返回状态，没有调用 ShardItem.Run()
    return TaskPassed
}
```

**目标实现**（方案 A：使用 GetError()）：
```go
func (m *TaskScheduler) executeFunc(item any) TaskStatus {
    shardItem := item.(ShardItem)

    // ShardItem 通过接口组合包含了 TaskRunner
    // 所以可以直接调用 Run() 方法
    shardItem.Run(context.Background(), nil)

    // ShardItem 通过接口组合包含了 TaskResult
    // 所以可以直接调用 IsDone() 检查状态
    if shardItem.IsDone() {
        // ✅ 方案 A：直接调用 GetError() 获取错误信息
        err := shardItem.GetError()
        return m.determineTaskStatus(err)
    }

    return TaskFailed
}
```

**determineTaskStatus 完整实现**：
```go
// 根据错误类型确定任务状态
func (m *TaskScheduler) determineTaskStatus(err error) TaskStatus {
    if err == nil {
        return TaskPassed
    }

    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return TaskTimeout
    case errors.Is(err, ErrRetry):
        return TaskBusy
    case isTemporaryError(err):
        return TaskRetrying
    default:
        return TaskFailed
    }
}

// isTemporaryError 判断是否为临时性错误
func isTemporaryError(err error) bool {
    // 网络错误
    if isNetworkError(err) {
        return true
    }

    // IO 错误
    if isIOError(err) {
        return true
    }

    // 其他可恢复错误
    return false
}
```

**说明**：
- ✅ ShardItem 通过接口组合自动获得 `Run()` 方法（来自 TaskRunner）
- ✅ ShardItem 通过接口组合自动获得 `IsDone()` 方法（来自 TaskResult）
- ✅ ShardItem 通过接口组合自动获得 `GetError()` 方法（来自 TaskResult）
- ✅ 使用 GetError() 比状态转换更直观、更符合 Go 惯例

##### 4. 数据结构：TaskStatus 状态机

**当前状态**（两个枚举分散）：
- `OperationStatus` (internal/domain/model/task.go)
- `TaskStatus` (internal/infrastructure/concurrency/types.go)

**目标设计**（统一枚举）：
```go
// internal/infrastructure/concurrency/types.go
type TaskStatus int

const (
    TaskQueued    TaskStatus = iota  // 已入队
    TaskExecuting                     // 执行中
    TaskPassed                        // ✅ 成功完成
    TaskFailed                        // ❌ 失败（不可重试）
    TaskTimeout                       // ⏱ 超时（可重试）
    TaskBusy                          // 🔥 繁忙/资源冲突（可重试）
    TaskRetrying                      // 🔄 重试中（可重试）
)
```

**状态转换逻辑**：

| 错误类型 | 返回状态 | runLoop 处理 | 使用场景 |
|---------|---------|-------------|---------|
| nil | TaskPassed | Dequeue 出队 | 操作成功 |
| context.DeadlineExceeded | TaskTimeout | 保留重试 | 超时 |
| ErrRetry | TaskBusy | 保留重试 | 锁冲突、页分裂 |
| 临时性错误 | TaskRetrying | 保留重试 | 网络、IO |
| KeyNotFound | TaskFailed | Dequeue 出队 | 确定性错误 |

##### 5. runLoop 重试机制

```go
func (c *SchedulerCore) runLoop() {
    for c.running.Load() {
        // 检查上下文
        select {
        case <-c.ctx.Done():
            return
        default:
        }

        // 获取排序后的任务
        tasks := c.getOrderedTasks()

        // 预检查：总队列长度
        totalQueueLen := 0
        for _, task := range tasks {
            totalQueueLen += task.QueueLen()
        }

        if totalQueueLen == 0 {
            c.waitForSignal()
            continue
        }

        // 循环调度：每个 Task 最多处理一个 item
        for _, task := range tasks {
            var item any
            if !task.Peek(&item) {
                continue
            }

            // 执行任务
            status := c.executeTask(task, item)

            // 根据状态决定是否 Dequeue
            switch status {
            case TaskPassed, TaskFailed:
                var dequeued any
                task.Dequeue(&dequeued)

            case TaskTimeout, TaskBusy, TaskRetrying:
                // ✅ 检查重试次数：超过最大重试次数则出队
                shardItem := item.(ShardItem)
                if shardItem.IncAttempts() > shardItem.MaxRetries() {
                    // 超过最大重试次数，出队并标记为失败
                    var dequeued any
                    task.Dequeue(&dequeued)
                }
                // 否则保留在队列，下次循环重试
            }
        }

        // 继续下一次循环，处理其他 task
    }
}
```

#### 3.3 架构设计

**BaseTask 改造**：

```go
// 当前实现（需要修改）
type BaseTask[Result any] struct {
    opType   OpType              // ❌ 应该移除
    priority TaskPriority
    sourceID SourceID
    execute  ExecuteFunc[Result]
    done     chan struct{}
    status   atomic.Int32        // OperationStatus
    result   Result
    err      error
    mu       sync.RWMutex
    // ❌ 缺少: id, retryCount, maxRetries, createdAt
}

// 目标设计
type BaseTask[Result any] struct {
    id         string             // ✅ 任务唯一标识符
    status     atomic.Int32      // TaskStatus（统一枚举）
    result     Result
    err        error
    createdAt  time.Time          // ✅ 创建时间
    retryCount int                // ✅ 重试次数（当前值）
    maxRetries int                // ✅ 最大重试次数（用于 ShardItem.MaxRetries()）
    priority   TaskPriority
    sourceID   SourceID
    execute    ExecuteFunc[Result]
    done       chan struct{}
    mu         sync.RWMutex
}

// BaseTask 实现 ShardItem 接口的重试管理方法
func (t *BaseTask[Result]) MaxRetries() int {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.maxRetries
}

func (t *BaseTask[Result]) IncAttempts() int {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.retryCount++
    return t.retryCount
}
```

**BTreeSetItem 实现**（目标设计）：

```go
// internal/application/task/btree_set_item.go
type BTreeSetItem struct {
    *model.BaseTask[struct{}]  // 嵌入 BaseTask（无返回值）

    // 业务字段
    btree   *btree.BTree
    leafRef *btree.PageRef
    key     []byte
    value   []byte
}

func NewBTreeSetItem(
    bt *btree.BTree,
    leafRef *btree.PageRef,
    key, value []byte,
    maxRetries int,
) *BTreeSetItem {
    item := &BTreeSetItem{
        btree:   bt,
        leafRef: leafRef,
        key:     key,
        value:   value,
    }

    // 初始化 BaseTask（包含重试配置）
    item.BaseTask = model.NewBaseTaskWithRetry[struct{}](
        model.TaskPriorityNormal,
        model.SourceAny,
        maxRetries,  // 设置最大重试次数
        func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
            err := item.btree.Set(ctx, item.key, item.value)
            return struct{}{}, err
        },
    )

    return item
}

// 实现 ShardItem 接口的业务特定方法
func (item *BTreeSetItem) ShardID() int {
    // 返回 leaf 的 PageID + 1，确保同一 leaf 的操作路由到同一 Core
    info := item.leafRef.GetPageInfo()
    if info == nil {
        return 0  // 无固定偏好
    }
    return int(info.GetPageID()) + 1  // +1 避免 0（0 表示无偏好）
}

// MaxRetries() 和 IncAttempts() 由 BaseTask 提供，无需重复实现
```
```

**使用示例**：

```go
// 异步 Set 方法（目标）
func (b *BTree) SetAsync(ctx context.Context, key, value []byte) (*BTreeSetItem, error) {
    leafRef, _, err := b.findLeafPageRef(ctx, key)
    if err != nil {
        return nil, err
    }

    item := NewBTreeSetItem(b, leafRef, key, value, 3)  // maxRetries=3
    err = b.scheduler.EnqueueWithShard(item, "btree-set")
    return item, err
}

// 调用方使用方式1：异步提交后等待
item, err := b.SetAsync(ctx, key, value)
if err != nil {
    return err
}

// 等待完成并获取结果
result := item.Wait(ctx)

// 调用方使用方式2：检查重试次数
if item.RetryCount() > 0 {
    log.Printf("Set 操作重试了 %d 次才成功", item.RetryCount())
}
```

**关键优势**：
- BaseTask 统一管理 retryCount，BTreeSetItem 无需自己维护
- 通过 RetryCount() 方法可以查询实际重试次数
- 所有嵌入 BaseTask 的类型自动获得 ShardItem 的重试管理能力

#### 3.4 容错设计

**状态转换图**：
```mermaid
stateDiagram-v2
    [*] --> TaskQueued: 入队
    TaskQueued --> TaskExecuting: Peek成功
    TaskExecuting --> TaskPassed: 执行成功
    TaskExecuting --> TaskFailed: 执行失败
    TaskExecuting --> TaskTimeout: 超时
    TaskExecuting --> TaskBusy: 锁冲突
    TaskTimeout --> TaskRetrying: 可重试
    TaskBusy --> TaskRetrying: 可重试
    TaskRetrying --> TaskExecuting: 重试执行
    TaskPassed --> [*]: Dequeue
    TaskFailed --> [*]: Dequeue
```

**错误类型映射表**：

| 错误类型 | 返回状态 | 可重试 | runLoop 处理 | 使用场景 | 检查方式 |
|---------|---------|-------|-------------|---------|---------|
| `nil` | TaskPassed | ❌ | Dequeue 出队 | 操作成功 | `err == nil` |
| `ErrRetry` | TaskBusy | ✅ | 保留重试 | 锁冲突、页分裂 | `errors.Is(err, ErrRetry)` |
| `context.DeadlineExceeded` | TaskTimeout | ✅ | 保留重试 | 超时 | `errors.Is(err, context.DeadlineExceeded)` |
| 网络错误 | TaskRetrying | ✅ | 保留重试 | 临时故障 | `isNetworkError(err)` |
| IO 错误 | TaskRetrying | ✅ | 保留重试 | 临时故障 | `isIOError(err)` |
| `ErrKeyNotFound` | TaskFailed | ❌ | Dequeue 出队 | 确定性错误 | `errors.Is(err, ErrKeyNotFound)` |

**重试策略详细说明**：

| 错误类型 | 处理方式 | 重试策略 | 最大重试次数 |
|---------|---------|---------|------------|
| **网络错误** | 返回 TaskRetrying | 立即重试 | 3 次 |
| **IO 错误** | 返回 TaskRetrying | 指数退避（1ms → 2ms → 4ms） | 3 次 |
| **锁冲突** | 返回 TaskBusy | 延迟重试，让出 CPU | 3 次 |
| **超时** | 返回 TaskTimeout | 立即重试，记录超时日志 | 3 次 |
| **确定性错误** | 返回 TaskFailed | 不重试，记录错误日志 | 0 次 |

**重试次数检查逻辑**：
```go
// runLoop 中的处理
case TaskTimeout, TaskBusy, TaskRetrying:
    // ✅ 检查重试次数：超过最大重试次数则出队
    shardItem := item.(ShardItem)
    if shardItem.IncAttempts() > shardItem.MaxRetries() {
        // 超过最大重试次数，出队并标记为失败
        var dequeued any
        task.Dequeue(&dequeued)
    }
    // 否则保留在队列，下次循环重试
```

### 4. 风险评估与应对措施（2026-03-21 最终确认）

#### 🔴 核心风险（基于代码审查确认）

| 风险点 | 影响等级 | 状态 | 应对措施 |
|--------|---------|------|----------|
| **问题1：ShardItem 接口未组合** | 🔴 高 | ⚠️ 阻塞 | 更新 `shard_item.go`，嵌入 TaskRunner + TaskResult |
| **问题2：BaseTask 字段缺失** | 🔴 高 | ⚠️ 阻塞 | 添加 4 个字段，删除 opType，修改构造函数 |
| **问题3：TaskBusy 状态缺失** | 🟡 中 | ⚠️ 待实现 | 在 types.go 中添加状态定义并更新 String() 方法 |

#### 🟢 已确认的修改

| 修改项 | 影响等级 | 状态 | 应对措施 |
|--------|---------|------|----------|
| opType 字段删除 | 🟡 中 | ✅ 已确认 | 在本 PR 中删除，修改 5 处 RPCTask 调用 |
| TaskResult 接口扩展 | 🟢 低 | ✅ 已确认 | 采用方案 A，添加 GetError() 方法 |
| 接口兼容性 | 🟢 低 | ✅ 可控 | 采用接口组合，向后兼容 |

#### 📊 风险缓解策略

1. **分阶段实施**：按三个阶段逐步实施，避免大规模变更
2. **保持向后兼容**：创建新构造函数，保留旧版本
3. **充分测试**：每个阶段完成后运行完整测试套件
4. **代码审查**：重点审查 BaseTask 和 ShardItem 接口变更

### 5. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施（含AI辅助修改） | 优化结果 |
|----------|----------|------------------|--------------|--------------------------|----------|
| 第1轮 | 待定 | 待定 | 待评审 | 待优化 | 待定 |
| 第2轮 | 待定 | 待定 | 待评审 | 待优化 | 待定 |

### 6. 预审批确认

> **架构师签字/备注**：_________ 202X-XX-XX 该Feature方案可行，风险可控，同意启动开发，需严格按照文档落地，确保CI通过后提交Post总结。

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动开发 | 待定 | 待定 | 代码提交至分支 |
| 本地测试 | 待定 | 待定 | 测试报告/覆盖率数据 |
| Post文档编写 | 待定 | 编写后置总结文档 | 第三部分：后置部分 |
| 架构师Post批准 | 待定 | 架构师评审Post文档 | 批准签字/备注 |
| 提交GitHub | 待定 | 推送分支，创建PR | GitHub PR链接 |

### 2. CI流程记录（修复Bug直至通过）

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 待定 | 失败/成功 | 待定 | 待定 | 待定 |
| 第2轮 | 待定 | 失败/成功 | 待定 | 待定 | 待定 |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| 待定 | Squash Merge / Merge Commit | 待定 | 待定 |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：待开发完成后填写
- **与Pre文档差异**：待开发完成后填写

#### 1.2 测试策略与成果

**单元测试计划**：
```go
// internal/domain/model/task_test.go
func TestBaseTask_RetryManagement(t *testing.T) {
    // 验证 MaxRetries() 和 IncAttempts()
}

func TestBaseTask_GetError(t *testing.T) {
    // 验证 GetError() 方法返回正确错误
}

// internal/infrastructure/concurrency/shard_item_test.go
func TestShardItem_InterfaceComposition(t *testing.T) {
    // 验证接口组合：嵌入 BaseTask 自动满足 ShardItem
}

// internal/infrastructure/concurrency/task_scheduler_test.go
func TestExecuteFunc_StatusDetermination(t *testing.T) {
    // 验证 executeFunc 根据错误返回正确状态
}

func TestRunLoop_RetryLogic(t *testing.T) {
    // 验证 runLoop 重试次数检查逻辑
}
```

**集成测试计划**：
```go
// internal/application/task/btree_set_item_test.go
func TestBTreeSetItem_AsyncSet(t *testing.T) {
    // 验证 BTree 异步 Set 操作
}

func TestBTreeSetItem_ShardRouting(t *testing.T) {
    // 验证 ShardID 路由到正确 Core
}

// internal/infrastructure/concurrency/integration_test.go
func TestTaskScheduler_RetryMechanism(t *testing.T) {
    // 验证完整重试机制
}
```

**性能测试计划**：
```go
// internal/domain/model/task_bench_test.go
func BenchmarkBaseTask_MemoryOverhead(b *testing.B) {
    // 对比新旧 BaseTask 内存占用
}

func BenchmarkExecuteFunc_Latency(b *testing.B) {
    // 测量 executeFunc 执行延迟
}

// internal/infrastructure/concurrency/task_scheduler_bench_test.go
func BenchmarkTaskScheduler_Throughput(b *testing.B) {
    // 测量吞吐量变化
}
```

- **性能数据**：待测试完成后填写
- **测试成果**：待测试完成后填写

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | 待定 | GitHub PR链接 |
| 文档更新 | 待定 | 文档路径 |

### 2. 未完成项与ToDo清单（有哪些没干，后续规划）

#### 2.1 本次PR未完成项
- **未支持**：待定
- **遗留问题**：待定

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| 高 | 待定 | 待定 | PR-XXX | 待定 |
| 中 | 待定 | 待定 | PR-XXX | 待定 |
| 低 | 待定 | 待定 | PR-XXX | 待定 |

### 3. 下一步工作建议（建议干啥）

1. **优先推进**：待定
2. **监控要点**：待定
3. **运维补充**：待定
4. **后续规划**：待定
5. **反馈收集**：待定

---

## 附录：关键代码示例

### A.1 TaskStatus 状态机（目标设计）

```go
// internal/infrastructure/concurrency/types.go
type TaskStatus int

const (
    TaskQueued    TaskStatus = iota  // 已入队
    TaskExecuting                     // 执行中
    TaskPassed                        // ✅ 成功完成
    TaskFailed                        // ❌ 失败（不可重试）
    TaskTimeout                       // ⏱ 超时（可重试）
    TaskBusy                          // 🔥 繁忙/资源冲突（可重试）
    TaskRetrying                      // 🔄 重试中（可重试）
)

func (s TaskStatus) String() string {
    switch s {
    case TaskQueued:
        return "Queued"
    case TaskExecuting:
        return "Executing"
    case TaskPassed:
        return "Passed"
    case TaskFailed:
        return "Failed"
    case TaskTimeout:
        return "Timeout"
    case TaskBusy:
        return "Busy"
    case TaskRetrying:
        return "Retrying"
    default:
        return "Unknown"
    }
}
```

### A.2 executeFunc 完整实现（目标设计-方案 A）

```go
// internal/infrastructure/concurrency/task_scheduler.go

func (m *TaskScheduler) executeFunc(item any) TaskStatus {
    shardItem, ok := item.(ShardItem)
    if !ok {
        return TaskFailed
    }

    // 执行任务（会阻塞直到任务完成）
    // 说明：
    //   1. executeFunc 在 runLoop 的循环中调用，会阻塞等待 shardItem.Run() 完成
    //   2. ShardItem 通过嵌入 model.TaskRunner 接口自动获得 Run() 方法
    //   3. 这是 Go 接口组合（Interface Embedding）的特性，直接调用即可
    shardItem.Run(context.Background(), nil)

    // 检查执行结果
    // 说明：IsDone() 和 GetError() 来自嵌入的 model.TaskResult 接口
    if shardItem.IsDone() {
        // ✅ 方案 A：使用 GetError() 直接获取错误信息
        err := shardItem.GetError()

        // 立即返回状态，由 runLoop 层决定是否重试
        return m.determineTaskStatus(err)
    }

    // 任务未完成（异常情况）
    return TaskFailed
}

// determineTaskStatus 根据错误类型确定任务状态
func (m *TaskScheduler) determineTaskStatus(err error) TaskStatus {
    if err == nil {
        return TaskPassed
    }

    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return TaskTimeout

    case errors.Is(err, ErrRetry):
        return TaskBusy

    case isTemporaryError(err):
        return TaskRetrying

    default:
        return TaskFailed
    }
}

// isTemporaryError 判断是否为临时性错误
func isTemporaryError(err error) bool {
    // 网络错误
    if isNetworkError(err) {
        return true
    }

    // IO 错误
    if isIOError(err) {
        return true
    }

    // 其他可恢复错误
    return false
}
```

### A.3 runLoop 重试逻辑

```go
// internal/infrastructure/concurrency/task_scheduler.go

func (c *SchedulerCore) runLoop() {
    c.wg.Add(1)
    defer c.wg.Done()

    for c.running.Load() {
        // 检查上下文
        select {
        case <-c.ctx.Done():
            return
        default:
        }

        // 获取排序后的任务
        tasks := c.getOrderedTasks()

        // 预检查：总队列长度
        totalQueueLen := 0
        for _, task := range tasks {
            totalQueueLen += task.QueueLen()
        }

        if totalQueueLen == 0 {
            c.waitForSignal()
            continue
        }

        // 循环调度：每个 Task 最多处理一个 item
        for _, task := range tasks {
            var item any
            if !task.Peek(&item) {
                continue
            }

            // 执行任务
            status := c.executeTask(task, item)

            // 根据状态决定是否 Dequeue
            switch status {
            case TaskPassed, TaskFailed:
                var dequeued any
                task.Dequeue(&dequeued)

            case TaskTimeout, TaskBusy, TaskRetrying:
                // ✅ 检查重试次数：超过最大重试次数则出队
                shardItem := item.(ShardItem)
                if shardItem.IncAttempts() > shardItem.MaxRetries() {
                    // 超过最大重试次数，出队并标记为失败
                    var dequeued any
                    task.Dequeue(&dequeued)
                }
                // 否则保留在队列，下次循环重试
            }
        }

        // 继续下一次循环，处理其他 task
    }
}
```

### A.4 BaseTask 字段调整与 ShardItem 接口实现

**当前实现**（需要删除的字段）：
```go
type BaseTask[Result any] struct {
    opType   OpType  // ❌ 需要删除
    // ...
}
```

**目标实现**：
```go
type BaseTask[Result any] struct {
    id         string
    status     atomic.Int32     // ✅ TaskStatus（统一枚举，替代 OperationStatus）
    result     Result
    err        error
    createdAt  time.Time       // ✅ 新增
    retryCount int             // ✅ 新增
    maxRetries int             // ✅ 新增（用于 ShardItem.MaxRetries()）
    priority   TaskPriority
    sourceID   SourceID
    execute    ExecuteFunc[Result]
    done       chan struct{}
    mu         sync.RWMutex
}
```

**BaseTask 实现接口方法**：
```go
// ========== ShardItem 重试管理方法 ==========

// MaxRetries 返回最大重试次数
func (t *BaseTask[Result]) MaxRetries() int {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.maxRetries
}

// IncAttempts 增加重试次数并返回当前值
func (t *BaseTask[Result]) IncAttempts() int {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.retryCount++
    return t.retryCount
}

// ========== TaskResult 结果查询方法（方案 A） ==========

// Status 返回任务状态（✅ 返回统一的 TaskStatus）
func (t *BaseTask[Result]) Status() TaskStatus {
    return TaskStatus(t.status.Load())
}

// GetError 返回任务执行错误（方案 A：新增方法）
func (t *BaseTask[Result]) GetError() error {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.err
}

// Done 返回完成通知 channel
func (t *BaseTask[Result]) Done() <-chan struct{} {
    return t.done
}

// IsDone 检查任务是否完成
func (t *BaseTask[Result]) IsDone() bool {
    return t.status.Load() != int32(TaskQueued) &&
           t.status.Load() != int32(TaskExecuting)
}

// Wait 等待任务完成
func (t *BaseTask[Result]) Wait(ctx context.Context) error {
    select {
    case <-t.done:
        return t.GetError()
    case <-ctx.Done():
        return ctx.Err()
    }
}

// 注意：ShardID() 由具体业务类型实现
// 因为 ShardID 的语义取决于业务逻辑（如 leafRef 的 PageID）
```

**构造函数调整**：

**命名策略**：采用方案 B - 创建新构造函数（向后兼容）

```go
// 当前构造函数（保留，向后兼容）
func NewBaseTask[Result any](
    priority TaskPriority,
    sourceID SourceID,
    execute ExecuteFunc[Result],
) *BaseTask[Result]

// ✅ 新构造函数：支持重试管理
func NewBaseTaskWithRetry[Result any](
    priority TaskPriority,
    sourceID SourceID,
    maxRetries int,  // ✅ 新增：最大重试次数
    execute ExecuteFunc[Result],
) *BaseTask[Result]
```

**使用说明**：
- **新代码**（ShardItem 场景）：使用 `NewBaseTaskWithRetry`
- **现有代码**（RPCTask 等）：继续使用 `NewBaseTask`（向后兼容）
- **废弃计划**：下一轮 PR 将 `NewBaseTask` 标记为 `@Deprecated`

**opType 删除计划**（已确认在本 PR 中执行）：

**理由**：
- ✅ opType 字段是死代码（从未被读取）
- ✅ 从旧 `AsyncOp` 模式迁移时的遗留字段
- ✅ 删除后简化 API，提高代码质量

**需要修改的文件**：

1. **`internal/domain/model/task.go`**：
   - 删除 `opType OpType` 字段定义
   - 删除 `OpType` 类型定义
   - 更新 `NewBaseTask` 构造函数，移除 opType 参数

2. **`internal/infrastructure/rpc/task.go`**（5 处调用修改）：
```go
// 修改前
task.BaseTask = model.NewBaseTask[service.ResponseMsg](
    model.OpRPC,              // ❌ 需要删除
    model.TaskPriorityNormal,
    sourceID,
    executeFunc,
)

// 修改后
task.BaseTask = model.NewBaseTaskWithRetry[service.ResponseMsg](
    model.TaskPriorityNormal,
    sourceID,
    maxRetries,
    executeFunc,
)
```

3. **测试验证**：
   - 运行 RPC 相关测试用例
   - 确保所有 RPCTask 功能正常

---

## 附录 B：代码审查详细报告（2026-03-21 最终确认）

### B.1 审查方法

本次审查基于以下文件的**实际代码**：
- `internal/domain/model/task.go` (BaseTask 实现)
- `internal/infrastructure/concurrency/types.go` (TaskStatus 枚举)
- `internal/infrastructure/concurrency/shard_item.go` (ShardItem 接口)

### B.2 三大核心问题（确认存在）

#### 问题 1：ShardItem 接口未采用组合设计

**文档目标设计**（接口组合）：
```go
type ShardItem interface {
    model.TaskRunner  // 嵌入：Run, Priority, SourceID
    model.TaskResult  // 嵌入：Done, Wait, Status, IsDone, GetError

    ShardID() int
    MaxRetries() int
    IncAttempts() int
}
```

**当前实际实现**（`internal/infrastructure/concurrency/shard_item.go`）：
```go
type ShardItem interface {
    ShardID() int
    MaxRetries() int
    IncAttempts() int
    // ❌ 没有嵌入 TaskRunner 和 TaskResult
}
```

**解决方案**：
1. 更新 `shard_item.go`，嵌入 TaskRunner 和 TaskResult 接口
2. 确保 BaseTask 实现了所有 TaskResult 方法（包括新增的 GetError()）

**影响分析**：
- ✅ 如果采用接口组合，BTreeSetItem 嵌入 BaseTask 后自动满足 ShardItem 接口
- ✅ 无需手动实现 Run(), IsDone() 等方法
- ❌ 当前实现需要手动声明所有方法，代码冗余度高

**解决方案**：采用接口组合设计

```go
// 更新 shard_item.go
type ShardItem interface {
    model.TaskRunner  // 嵌入：Run, Priority, SourceID
    model.TaskResult  // 嵌入：Done, Wait, Status, IsDone

    ShardID() int
    MaxRetries() int
    IncAttempts() int
}

// BTreeSetItem 实现
type BTreeSetItem struct {
    *model.BaseTask[struct{}]  // 自动获得 TaskRunner、TaskResult 和重试管理
    btree   *btree.BTree
    leafRef *btree.PageRef
    key     []byte
    value   []byte
}

// 只需实现业务特定方法
func (item *BTreeSetItem) ShardID() int {
    info := item.leafRef.GetPageInfo()
    if info == nil {
        return 0
    }
    return int(info.GetPageID()) + 1
}

// MaxRetries() 和 IncAttempts() 由 BaseTask 提供，无需重复实现

// Run(), IsDone(), Done(), Wait() 等方法由 BaseTask 自动提供
```

---

#### 问题 2：BaseTask 字段缺失详情

**文档要求的字段**（附录 A.4）：
```go
type BaseTask[Result any] struct {
    id         string             // ❌ 不存在
    status     atomic.Int32       // ✅ 存在（类型是 OperationStatus）
    result     Result             // ✅ 存在
    err        error              // ✅ 存在
    createdAt  time.Time          // ❌ 不存在
    retryCount int                // ❌ 不存在
    maxRetries int                // ❌ 不存在
    priority   TaskPriority       // ✅ 存在
    sourceID   SourceID           // ✅ 存在
    execute    ExecuteFunc[Result] // ✅ 存在
    done       chan struct{}      // ✅ 存在
    mu         sync.RWMutex       // ✅ 存在
    opType     OpType             // ⚠️ 存在但文档要求删除
}
```

**需要添加的字段**：
1. `id string` - 任务唯一标识符
2. `createdAt time.Time` - 创建时间
3. `retryCount int` - 当前重试次数
4. `maxRetries int` - 最大重试次数

**需要删除的字段**：
1. `opType OpType` - 操作类型（影响 5 处 RPCTask 调用）

---

#### 问题 3：TaskStatus 状态机缺失 TaskBusy

**文档要求的状态机**（第 3.2 节）：
```go
const (
    TaskQueued    TaskStatus = iota
    TaskExecuting
    TaskPassed
    TaskFailed
    TaskTimeout
    TaskBusy      // ❌ 当前代码中不存在
    TaskRetrying
)
```

**当前代码的状态**（`internal/infrastructure/concurrency/types.go`）：
```go
const (
    TaskQueued    TaskStatus = iota
    TaskExecuting
    TaskPassed
    TaskFailed
    TaskTimeout
    TaskRetrying
    // 缺少 TaskBusy
)
```

**使用场景**：TaskBusy 用于处理锁冲突、资源繁忙等可重试的瞬时错误。

---

#### ✅ 方案确定：TaskResult 接口扩展（方案 A）

**架构师决策**：采用方案 A，扩展 TaskResult 接口添加 GetError() 方法

**当前 TaskResult 接口**（`internal/domain/model/task.go`）：
```go
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)
    Status() OperationStatus  // ⚠️ 需要改为 TaskStatus
    IsDone() bool
    // ❌ 缺少 GetError() 方法
}
```

**目标设计**（方案 A：统一状态枚举 + 扩展 GetError）：
```go
type TaskResult interface {
    Done() <-chan struct{}
    Wait(ctx context.Context) (Result, error)
    Status() TaskStatus    // ✅ 改为：返回统一的 TaskStatus（而非 OperationStatus）
    IsDone() bool
    GetError() error      // ✅ 新增：获取错误信息
}
```

**设计理由**：
- ✅ **统一状态枚举**：TaskScheduler 使用 TaskStatus，TaskResult 也返回 TaskStatus
- ✅ **语义清晰**：GetError() 直观地表达"获取错误"
- ✅ **简洁直接**：executeFunc 可以直接调用 `shardItem.GetError()`
- ✅ **符合 Go 惯例**：类似 `errors.As` 的错误获取模式
- ✅ **避免状态转换**：不需要在 OperationStatus 和 TaskStatus 之间转换

**BaseTask 需要实现**：
```go
// GetError 返回任务执行错误
func (t *BaseTask[Result]) GetError() error {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.err
}
```

**实施步骤**：
1. 在 `model/task.go` 中扩展 TaskResult 接口定义
2. 在 BaseTask 中实现 GetError() 方法
3. 更新 executeFunc 使用 GetError() 获取错误信息

---

#### 问题 4：接口组合的优势（已确认）

**接口组合设计的优势**：

| 方面 | 手动实现所有方法 | 接口组合 |
|------|------------------|----------|
| 代码行数 | ~20 行 | ~10 行 |
| 冗余度 | 高 | 低 |
| 可维护性 | BaseTask 变更需同步修改 | 自动继承 |
| BTreeSetItem 实现 | 需手动实现 8+ 个方法 | 只需 3 个业务方法 |

**结论**：采用接口组合是正确的架构决策

---

### B.3 需要修改的文件清单

| 文件 | 修改类型 | 详情 |
|------|----------|------|
| `internal/domain/model/task.go` | 接口扩展 | TaskResult 添加 `GetError()` 方法（方案 A） |
| `internal/domain/model/task.go` | 字段修改 | 添加 4 个字段（id, createdAt, retryCount, maxRetries） |
| `internal/domain/model/task.go` | 字段删除 | 删除 opType 字段 |
| `internal/domain/model/task.go` | 方法添加 | 实现 `MaxRetries()`, `IncAttempts()`, `GetError()` |
| `internal/domain/model/task.go` | 构造函数修改 | 创建 `NewBaseTaskWithRetry`（移除 opType 参数） |
| `internal/infrastructure/concurrency/types.go` | 状态添加 | 添加 `TaskBusy` 状态，更新 String() 方法 |
| `internal/infrastructure/concurrency/shard_item.go` | 接口组合 | 嵌入 TaskRunner + TaskResult 接口 |
| `internal/infrastructure/concurrency/task_scheduler.go` | 逻辑重写 | 重写 `executeFunc`，使用 GetError() |
| `internal/infrastructure/concurrency/task_scheduler.go` | 重试逻辑 | 更新 runLoop switch 语句 |
| `internal/infrastructure/rpc/task.go` | 调用修改 | 移除 5 处 `model.OpRPC` 参数 |
| `internal/application/task/btree_set_item.go` | 新建文件 | 创建 BTreeSetItem 实现 |

---

### B.4 影响范围评估

| 模块 | 影响程度 | 说明 |
|------|----------|------|
| RPC 任务 | 🟡 中 | 5 处调用需要修改（删除 opType 参数） |
| 存储任务 | 🟢 低 | 新建 BTreeSetItem，不影响现有代码 |
| Raft 任务 | 🟢 低 | 可能需要适配新构造函数 |
| 压缩任务 | 🟢 低 | 可能需要适配新构造函数 |
| TaskScheduler | 🔴 高 | executeFunc 需要完全重写 |

---

### B.5 建议的实施步骤

1. **第一阶段：接口设计确认**（1-2 天）
   - [ ] 架构师评审 ShardItem 接口扩展方案
   - [ ] 确定 executeFunc 的最终实现方式
   - [ ] 确认 BaseTask 的接口职责

2. **第二阶段：基础实现**（3-5 天）
   - [ ] BaseTask 字段添加
   - [ ] BaseTask 方法实现
   - [ ] TaskStatus 状态机更新
   - [ ] 单元测试编写

3. **第三阶段：集成实现**（3-5 天）
   - [ ] executeFunc 重写
   - [ ] BTreeSetItem 实现
   - [ ] RPCTask 迁移
   - [ ] 集成测试编写

4. **第四阶段：验证与优化**（2-3 天）
   - [ ] 性能测试
   - [ ] 压力测试
   - [ ] 文档更新

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.9 |
| 最后更新日期 | 2026-03-21 |
| 更新记录 | V1.0 → V1.1 → V1.2 → V1.3 → V1.4 → V1.5 → V1.6 → V1.7 → V1.8 → V1.9（接口问题修复） |
| 归档路径 | `docs/06_PM/feature/2026-03-21_PR-task-scheduler-result-return_全流程.md` |
| 后续维护人 | jzhang405 |

---

**文档更新历史**：
- **V1.0** (2026-03-21 上午)：初始版本，基于 `thoughts/task-scheduler-result-return-discussion.md` 创建
- **V1.1** (2026-03-21 下午)：更正 ShardItem 接口理解，添加代码审查结果
- **V1.2** (2026-03-21 晚)：确定采用方案 A（TaskResult 扩展 GetError()）
- **V1.3** (2026-03-21 晚)：确认在本 PR 中删除 opType 字段
- **V1.4** (2026-03-21 晚)：
  - ✅ 明确所有核心问题均可按文档直接实施
  - 将"严重问题"改为"核心实施任务"
  - 将"阻塞开发"改为"可按文档执行"
  - 删除"暂停开发"建议，确认可直接开始实施
  - ✅ **新增**：状态枚举统一（OperationStatus → TaskStatus）
    - TaskResult.Status() 返回值改为 TaskStatus
    - BaseTask.status 字段类型改为 TaskStatus
    - 问题汇总表添加"问题 2a：状态枚举统一"
    - 附录 B 中的 TaskResult 接口定义同步更新
- **V1.5** (2026-03-21 晚)：
  - ✅ **优化**：移除 runLoop 中不必要的 `runtime.Gosched()` 调用
    - runLoop 是持续循环的调度器，无需手动让出 CPU
    - 如果队列中有其他 task，循环会自然继续处理
    - 如果队列空，`waitForSignal()` 会处理等待
- **V1.6** (2026-03-21 晚)：
  - ✅ **新增**：添加 `TaskScheduler.EnqueueWithShard` 接口详细说明
    - 多队列架构：每个 Core 内部有多个命名的队列
    - taskName 参数用于从指定 Core 获取对应队列
    - 完整的注册与使用流程说明
  - ✅ **修正**：BTreeSetItem.ShardID() 实现使用 PageID + 1
    - 删除错误的重试字段（BaseTask 已包含）
    - 删除使用指针地址的错误实现
    - 说明为什么用 PageID 而不是指针地址
- **V1.7** (2026-03-21 晚)：P0/P1 问题修复
  - ✅ **P0-1**：统一 TaskResult 接口定义标注（当前 vs 目标）
  - ✅ **P0-2**：补充 runLoop 重试次数检查逻辑
    - 添加 `IncAttempts() > MaxRetries()` 检查
    - 超过最大重试次数自动出队
  - ✅ **P0-3**：修正 executeFunc 阻塞语义说明
    - 删除"不应该阻塞"的错误注释
    - 明确说明会阻塞等待任务完成
  - ✅ **P0-4**：添加 ErrRetry 错误定义
    - 明确错误包路径
    - 添加使用示例
  - ✅ **P1-1**：添加完整的测试策略
    - 单元测试：BaseTask、ShardItem、executeFunc
    - 集成测试：BTreeSetItem、TaskScheduler
    - 性能测试：内存开销、延迟、吞吐量
  - ✅ **P1-4**：添加状态枚举迁移说明
    - 明确 OperationStatus 暂时保留
    - 说明本次 PR 的迁移范围
  - ✅ **新增**：添加状态转换图（mermaid）
  - ✅ **新增**：添加详细的错误类型映射表
  - ✅ **新增**：添加重试次数检查逻辑说明
  - ✅ **新增**：添加 `TaskScheduler.EnqueueWithShard` 接口详细说明
    - 多队列架构：每个 Core 内部有多个命名的队列
    - taskName 参数用于从指定 Core 获取对应队列
    - 完整的注册与使用流程说明
  - ✅ **修正**：BTreeSetItem.ShardID() 实现使用 PageID + 1
    - 删除错误的重试字段（BaseTask 已包含）
    - 删除使用指针地址的错误实现
    - 说明为什么用 PageID 而不是指针地址
- **V1.8** (2026-03-21 晚)：第二轮复审问题修复
  - ✅ **Minor-1**：添加 `TaskStatus.String()` 方法更新提醒
    - 在"问题 3：TaskBusy 状态缺失"的实施步骤中添加提醒
    - 确保开发者添加 TaskBusy case 分支
  - ✅ **Minor-2**：强调 `BaseTask.status` 字段类型修改的重要性
    - 在"问题 2a：状态枚举统一"的影响范围中加粗标注
    - 明确说明字段类型必须从 OperationStatus 改为 TaskStatus
  - ✅ **Minor-3**：统一构造函数命名策略
    - 采用方案 B：创建新构造函数 `NewBaseTaskWithRetry`
    - 保留旧构造函数向后兼容
    - 明确使用场景和废弃计划
  - ✅ **新增**：第二轮 Agent 复审报告
    - 文档质量评分：9.5/10
    - 确认文档已达到生产就绪状态
- **V1.9** (2026-03-21 晚)：接口问题修复
  - ✅ **问题 2**：修正 TaskResult.Wait() 签名
    - 从 `Wait(ctx) error` 改为 `Wait(ctx) (Result, error)`
    - 与实际 BaseTask 实现保持一致
  - ✅ **问题 3**：添加接口组合说明
    - 在 executeFunc 实现中添加接口组合机制说明
    - 说明 ShardItem 通过嵌入 TaskRunner/TaskResult 获得 Run() 等方法
    - 补充 `thoughts/interface-embedding-explanation.md` 详细说明文档
  - ✅ **问题 4**：确认无 RegisterTask/RegisterShardTask 混淆
    - 文档中统一使用 `RegisterTask` 方法名
