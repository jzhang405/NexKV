# 【PR全流程文档】Doc - 更新 Spike DDD 文档和 M2 Storage Engine 文档

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从文档更新需求到完成归档的全流程，一个PR对应一份全流程文档。

---

## 第一部分：前置部分（开工前必完成）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 纯文档更新（Doc） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | docs/update-spike-ddd-m2-storage |
| 工作主题 | 更新 Spike DDD 文档和 M2 Storage Engine 文档以反映当前实现状态 |
| 负责人 | jzh |
| 分支创建日期 | 2026-03-05 |
| 计划完成日期 | 2026-03-05 |
| 关联需求/Issue | - |
| 更新类型 | ☑ 更新文档 |

### 2. 背景与目标（为什么更新）

#### 2.1 更新原因

- **触发因素**：代码审查发现文档与实际实现存在差异，特别是 TaskExecutor 接口和异步编程模型
- **现状问题**：
  1. DDD Interface 文档中的接口定义与实际代码不一致
  2. `AsyncTaskExecutor` 命名存在语义问题（"Async" 与 TaskExecutor 语义重复）
  3. M2 Storage Engine Roadmap 中阶段 0 状态未更新为已完成
  4. 文档间版本引用不一致
- **更新价值**：
  1. 确保文档与代码一致性，减少开发者的困惑
  2. 反映当前实现进度，便于后续规划
  3. 记录命名问题，为后续重构提供参考

#### 2.2 更新目标

1. **准确性**：更新接口定义，反映实际实现（如 `TaskExecutor.Submit` 方法签名）
2. **完整性**：添加实现状态标记，更新阶段进度
3. **可读性**：添加命名问题备注，提供改进建议

#### 2.3 明确边界

- **本次更新**：
  - DDD Interface 文档：TaskExecutor 接口定义、AsyncTaskExecutor 命名问题备注
  - M2 Roadmap 文档：阶段 0 状态更新为已完成
  - M2 Interface 文档：关联文档版本更新
- **暂不更新**：
  - 实际代码修改
  - 接口重命名（AsyncTaskExecutor → ManagedTaskExecutor）
  - 时间线的重大调整
  - 新增接口定义

### 3. 更新内容（更新什么）

#### 3.1 涉及文档列表

| 文档路径 | 更新类型 | 更新内容概述 | 优先级 |
|---------|---------|-------------|--------|
| `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` | 更新 | TaskExecutor 接口定义、AsyncTaskExecutor 命名问题备注、**V4 异步管道接口** | 高 |
| `docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md` | 更新 | 阶段 0 状态更新为已完成、添加 PR 引用 | 高 |
| `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md` | 更新 | 关联文档版本更新、**V4 存储引擎接口** | 中 |

#### 3.2 主要变更点

**新增内容**：
- AsyncTaskExecutor 命名问题备注（建议重命名为 ManagedTaskExecutor）
- 阶段 0 完成状态标记和 PR 引用
- **V4 异步管道接口规范**（见下方详细计划）

**修改内容**：
- TaskExecutor 接口定义：反映实际 `Submit(ctx, sourceID, priority, task)` 签名
- 阶段 0 状态：从"待实施"更新为"已完成"
- 关联文档版本引用

**删除内容**：
- 无

---

## 4. V4 异步管道接口规范更新计划

> **参考文档**: `docs/07_spike/2026-03-04-spike-async-pipeline-v4.md`
> **状态**: 📝 待审核

### 4.1 V4 核心接口概览

V4 设计采用**双层接口**解决泛型与统一调度的矛盾：

```go
// ═══════════════════════════════════════════════════════════════
// 第一层：TaskRunner（非泛型）—— Executor 只看到这个
// ═══════════════════════════════════════════════════════════════
type TaskRunner interface {
    Run(ctx context.Context, p *Pipeline)
    Priority() Priority
    SourceID() model.SourceID
}

// ═══════════════════════════════════════════════════════════════
// 第二层：Task[Result]（泛型）—— 用户使用，类型安全
// ═══════════════════════════════════════════════════════════════
type Task[Result any] interface {
    TaskRunner  // 嵌入第一层
    Execute(ctx context.Context, p *Pipeline) (Result, error)
}
```

### 4.2 需要添加到 DDD Interface 文档的接口

#### 4.2.1 TaskRunner 接口（新增）

**位置**: `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` → Domain Service Layer

```go
// TaskRunner 非泛型任务执行接口（Executor 视角）
// 设计意图：Executor 不需要知道 Result 类型，只需要知道"如何执行"
type TaskRunner interface {
    // Run 执行任务（由 Executor 调用）
    Run(ctx context.Context, p *Pipeline)

    // Priority 返回任务优先级（用于优先级队列调度）
    Priority() Priority

    // SourceID 返回任务来源标识（用于 CPU 亲和性绑定）
    SourceID() model.SourceID
}
```

#### 4.2.2 Task[Result] 泛型接口（新增）

**位置**: `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` → Domain Service Layer

```go
// Task[Result] 泛型任务接口（用户视角）
// 设计意图：编译时类型安全，不同任务返回不同类型
type Task[Result any] interface {
    TaskRunner  // 嵌入 TaskRunner（类型擦除点）

    // Execute 执行任务并返回类型化结果
    Execute(ctx context.Context, p *Pipeline) (Result, error)
}
```

#### 4.2.3 BaseTask[Result] 泛型基类（新增）

**位置**: `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` → Domain Model Layer

```go
// BaseTask[Result] 泛型任务基类
// 提供 TaskRunner 接口的默认实现
type BaseTask[Result any] struct {
    // 调度属性
    opType   OpType
    priority Priority
    sourceID model.SourceID

    // 结果存储（单 done channel + 直接存储）
    done   chan struct{}  // 完成信号
    result Result         // 直接存储结果（值类型）
    err    error
}

// NewBaseTask 创建任务基类
func NewBaseTask[Result any](opType OpType, priority Priority, sourceID model.SourceID) BaseTask[Result]

// Run 实现 TaskRunner 接口（Executor 调用）
func (b *BaseTask[Result]) Run(ctx context.Context, p *Pipeline)

// Wait 等待完成并返回结果（类型安全恢复）
func (b *BaseTask[Result]) Wait() (Result, error)

// Done 返回完成 channel（用于 select）
func (b *BaseTask[Result]) Done() <-chan struct{}
```

#### 4.2.4 AsyncOp[Result] 异步操作句柄（新增）

**位置**: `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` → Domain Service Layer

```go
// AsyncOp[Result] 异步操作句柄（包装 Task[Result]）
// 设计意图：异步 API 返回此句柄，用户可等待或检查完成状态
type AsyncOp[Result any] struct {
    task Task[Result]  // 持有 Task 引用
}

// Wait 等待完成并返回结果
func (op *AsyncOp[Result]) Wait() (Result, error)

// Done 返回完成 channel（用于 select）
func (op *AsyncOp[Result]) Done() <-chan struct{}

// IsComplete 非阻塞检查是否完成
func (op *AsyncOp[Result]) IsComplete() bool
```

### 4.3 需要添加到 M2 Storage Engine Interface 文档的接口

#### 4.3.1 具体 Task 实现（新增）

**位置**: `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md` → Storage Engine Layer

| Task 类型 | Result 类型 | 优先级 | 说明 |
|----------|------------|-------|------|
| `BTreeReadTask` | `[]byte` | High | BTree 读取任务 |
| `BTreeWriteTask` | `struct{}` | High | BTree 写入任务 |
| `BTreeDeleteTask` | `struct{}` | High | BTree 删除任务 |
| `BTreeRangeTask` | `[]KVPair` | Normal | BTree 范围查询任务 |
| `WALAppendTask` | `struct{}` | **Critical** | WAL 追加任务 |
| `CompositeWriteTask` | `struct{}` | High | 组合写入（WAL + BTree） |

#### 4.3.2 CompositeWriteTask 组合任务（重点）

**设计意图**: Set 操作需要先 WAL 后 BTree，用组合任务保证顺序和 CPU 亲和性

```go
// CompositeWriteTask 组合写入任务（WAL + BTree）
// 关键特性：整个 Set 操作在同一个 Worker 中完成，保持 CPU 亲和性
type CompositeWriteTask struct {
    BaseTask[struct{}]
    key, value []byte
    ts         *HLC
}

func (t *CompositeWriteTask) Execute(ctx context.Context, p *Pipeline) (struct{}, error) {
    // 1. 写 WAL（同步执行，在当前 Worker 中）
    walTask := NewWALAppendTask(...)
    if err := walTask.Execute(ctx, p); err != nil {
        return struct{}{}, fmt.Errorf("wal: %w", err)
    }

    // 2. 更新 BTree（同步执行，在当前 Worker 中）
    btreeTask := NewBTreeWriteTask(...)
    if err := btreeTask.Execute(ctx, p); err != nil {
        return struct{}{}, fmt.Errorf("btree: %w", err)
    }

    return struct{}{}, nil
}
```

#### 4.3.3 Pipeline.Submit 方法（更新）

```go
// Pipeline.Submit 提交任务到 Executor
func (p *Pipeline) Submit(task TaskRunner) error {
    return p.executor.Submit(
        p.ctx,
        task.SourceID(),
        model.TaskPriority(task.Priority()),
        func(ctx context.Context) {
            task.Run(ctx, p)  // 调用 TaskRunner.Run
        },
    )
}
```

### 4.4 更新 DDD Interface 文档的具体位置

| 章节 | 更新内容 | 行号范围（估计） |
|------|---------|----------------|
| Domain Service Layer | 添加 TaskRunner 接口 | ~200-220 |
| Domain Service Layer | 添加 Task[Result] 泛型接口 | ~220-240 |
| Domain Service Layer | 添加 AsyncOp[Result] 接口 | ~240-260 |
| Domain Model Layer | 添加 BaseTask[Result] 基类 | ~150-180 |
| 实现状态追踪 | 更新 Task 相关接口状态 | 末尾新增章节 |

### 4.5 更新 M2 Storage Engine Interface 文档的具体位置

| 章节 | 更新内容 | 行号范围（估计） |
|------|---------|----------------|
| Storage Engine Layer | 添加具体 Task 实现表格 | ~300-330 |
| Storage Engine Layer | 添加 CompositeWriteTask 设计 | ~330-370 |
| Pipeline Layer | 更新 Pipeline.Submit 方法签名 | ~280-300 |
| 关联文档 | 添加 V4 设计文档引用 | 文档开头 |

### 4.6 V4 接口与现有接口的关系

| 现有接口 | V4 接口 | 关系 |
|---------|--------|------|
| `TaskExecutor.Submit(ctx, sourceID, priority, task)` | `Pipeline.Submit(task TaskRunner)` | V4 的 Pipeline 包装了 TaskExecutor |
| `AsyncOp[T]`（已实现） | `AsyncOp[Result]`（V4） | 兼容，V4 更强调包装 Task |
| `GoroutineProvider`（已弃用） | `TaskRunner` | V4 正式定义非泛型执行接口 |

### 4.7 更新优先级

| 优先级 | 更新内容 | 预估时间 |
|--------|---------|---------|
| P0 | 添加 TaskRunner/Task[Result] 接口定义 | 20 分钟 |
| P0 | 添加 BaseTask[Result] 基类定义 | 15 分钟 |
| P1 | 添加具体 Task 实现说明 | 15 分钟 |
| P1 | 添加 CompositeWriteTask 设计 | 10 分钟 |
| P2 | 添加 AsyncOp[Result] 说明 | 10 分钟 |

---

### 5. 一致性验证（如何保证一致）

#### 5.1 与代码一致性

- **代码变更检查**：无需代码变更，纯文档更新
- **接口定义验证**：对照 `internal/domain/service/task.go` 中的 TaskExecutor 接口
- **实现状态验证**：确认 PR-088、PR-087、PR-073 已合并

#### 5.2 跨文档一致性

- **术语一致性**：统一使用 TaskExecutor 而非 GoroutineProvider
- **引用检查**：确保三份文档的版本引用一致
- **版本同步**：统一使用 v3.0 版本号

### 6. 评审要点

| 评审项 | 检查内容 | 评审人 | 评审结果 |
|--------|---------|--------|---------|
| 内容准确性 | 接口定义与代码一致 | - | □ 通过 □ 需修改 |
| 术语一致性 | TaskExecutor/AsyncTaskExecutor 术语统一 | - | □ 通过 □ 需修改 |
| 格式规范 | Markdown 格式正确 | - | □ 通过 □ 需修改 |
| 链接有效性 | 文档间引用链接有效 | - | □ 通过 □ 需修改 |

### 6. 评审记录

| 评审轮次 | 评审日期 | 评审人 | 核心意见 | 修改措施 | 完成状态 |
|----------|----------|--------|---------|---------|---------|
| 第1轮 | - | - | - | - | - |

---

## 第二部分：流程节点记录

### 1. 更新过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| Pre文档编写 | 2026-03-05 | 编写前置规划文档 | 本文档 |
| 文档更新 | 2026-03-05 | 更新三份 spike 文档 | ✅ 完成 |
| Post文档编写 | - | 编写后置总结文档 | - |
| 提交GitHub | - | 创建PR | - |

### 2. CI流程记录

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | - | - | - | - | - |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| - | - | - | - |

---

## 第三部分：后置部分（PR合并后编写）

### 1. 更新成果总结

#### 1.1 完成情况
- **已更新文档**：[待填写]
- **变更统计**：[待填写]
- **与Pre文档差异**：[待填写]

#### 1.2 质量验证
- **链接检查**：[待填写]
- **术语检查**：[待填写]

#### 1.3 交付物清单

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 更新文档 | DDD Interface | docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md |
| 更新文档 | M2 Roadmap | docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md |
| 更新文档 | M2 Interface | docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md |

### 2. 后续优化建议

#### 2.1 未完成项
- **接口重命名**：AsyncTaskExecutor → ManagedTaskExecutor（建议在后续重构中处理）

#### 2.2 ToDo清单

| 优先级 | 任务内容 | 预估时间 | 关联PR | 备注 |
|--------|----------|---------|--------|------|
| 中 | AsyncTaskExecutor 重命名 | 2小时 | - | 需要代码修改 |

### 3. 文档维护建议

1. **持续更新**：每次 PR 合并后检查是否需要更新相关文档
2. **反馈收集**：通过 Code Review 收集文档改进建议
3. **版本管理**：保持三份文档版本号同步

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.0 |
| 归档日期 | - |
| 归档路径 | `docs/06_PM/doc/2026-03-05_PR-update-spike-ddd-m2-storage_全流程.md` |
| 后续维护人 | jzh |
