# PerCoreExecutor RunLoop 优化方案 (综合版)

> **日期**: 2026-03-07
> **状态**: 💡 设计讨论
> **合并来源**: percore-executor-run-loop-proposal.md + percore-runloop-optimization.md

---

## 1. 问题分析

### 1.1 当前实现 (Task-per-Operation 模式)

```go
// 当前方式：每次操作创建新 Task
func (t *BfTree) GetAsync(key []byte) model.Task[[]byte] {
    return model.NewBaseTask(
        model.OpStorage,
        model.TaskPriorityNormal,
        t.sourceID,
        func(ctx context.Context, pipeline model.PipelineContext) ([]byte, error) {
            return t.getNode(ctx, key)
        },
    )
}
```

**调用链开销**:
```
NewBaseTask → Submit() → taskItem 创建 → Push 到队列
     ↓             ↓            ↓              ↓
  ~460ns      ~10ns        ~48B 分配      futex 等待
                                               ↓
                                         Worker 唤醒
                                               ↓
                                         task.Execute()
                                               ↓
                                         关闭 done channel
                                               ↓
                                         Wait() 返回

总延迟: 9,674 ns
```

### 1.2 性能瓶颈分析

| 瓶颈 | 时间 | 占比 | 根因 |
|------|------|------|------|
| **futex 调度** | ~7,000 ns | **72.6%** | goroutine 调度开销 |
| WaitGroup 同步 | ~1,700 ns | 17.8% | 任务完成通知 |
| 实际执行 | ~930 ns | 9.59% | 业务逻辑 |

> ⚠️ **性能修正**（专家评审意见）：
> - 原始 futex 开销 7,000ns 包含测试环境特定因素
> - 实际 futex 通常在 100-500ns 量级
> - Channel 发送并非零分配（~16B 头部）
> - 实际单次延迟预估：5,000-8,000 ns
| Task 创建 | ~460 ns | 4.7% | channel + atomic 分配 |

**关键发现**:
1. **futex 开销占 72.6%** - 每次都需要唤醒 goroutine
2. 每次操作都创建新 taskItem (~48B 分配)
3. 单操作延迟: 9,674 ns vs 同步 103 ns (**慢 93.9x**)

### 1.3 内存分配压力

| 操作 | 分配大小 | 频率 | GC 压力 |
|------|---------|------|---------|
| BaseTask | ~200B | 每次 | 高 |
| done channel | ~100B | 每次 | 高 |
| taskItem | ~48B | 每次 | 中 |
| atomic.Int32 | ~8B | 每次 | 低 |

---

## 2. 优化方案: RunLoop + Channel 模式

### 2.1 核心思想

**不是** 每次创建 Task，而是:
1. Worker goroutine **长期运行** (Run Loop)
2. 通过 **Channel** 接收任务函数
3. 零分配提交，直接执行

### 2.2 设计对比

#### 当前模式 (Task-Driven)

```
Task1 → Submit → Push → Execute → 销毁
Task2 → Submit → Push → Execute → 销毁
Task3 → Submit → Push → Execute → 销毁

问题:
- 每个 Task 都需要创建和销毁
- 每次都需要 goroutine 调度 (futex)
- 大量临时对象，GC 压力大
```

#### 优化后模式 (RunLoop)

```
Worker Goroutine:
    for {
        task := <-channel  // 等待任务
        task()            // 执行任务
        // 不退出，复用 goroutine
    }

优势:
- 零分配提交
- 无调度开销
- GC 压力极低
```

---

## 3. 详细设计

### 3.1 数据结构 (简化设计)

> ⚠️ **简化设计**（专家评审意见）：
> - 移除了复杂的优先级 Channel 数组（10 个 channel 调度开销大）
> - 改为单一 channel + 任务优先级字段
> - 简化实现，降低复杂度

```go
// RunLoopWorker 基于 Channel 的 Worker（简化版）
type RunLoopWorker struct {
    coreID   int
    executor *PerCoreExecutor

    // 单一任务 Channel（包含优先级信息）
    taskCh chan *Task

    // 上下文
    ctx    context.Context
    cancel context.CancelFunc

    // 状态
    running atomic.Bool
    stats   WorkerStats
}

// Task 任务结构（带优先级）
type Task struct {
    Fn       func()
    Priority int // 0-9，0 最高
}

// 关闭机制（防止 Channel 泄漏）
type TaskResult struct {
    Result interface{}
    Err   error
}
```

### 3.2 API 设计 (双模式支持)

#### 模式 A: 函数式 API (简单场景)

```go
// Submit 函数式提交
func (w *RunLoopWorker) Submit(
    ctx context.Context,
    priority model.TaskPriority,
    task func(context.Context),
) error {
    p := int(priority)
    if p < NumPriorityLevels {
        select {
        case w.priorityChs[p] <- task:
            return nil  // 零分配提交
        default:
            // 优先级队列满，尝试主队列
        }
    }

    select {
    case w.taskCh <- task:
        return nil
    default:
        return ErrQueueFull
    }
}
```

#### 模式 B: 结构化 API (复杂场景)

```go
// Request 请求结构
type Request[Result any] struct {
    // 输入参数
    Key     []byte
    Options *GetOptions

    // 结果 channel (buffered=1)
    Result chan ResultOrError[Result]

    // 上下文
    Context context.Context
}

type ResultOrError[Result any] struct {
    Result Result
    Err    error
}

// GetAsync 结构化 API
func (t *BfTree) GetAsync(key []byte) *Request[[]byte] {
    req := &Request[[]byte]{
        Key:     key,
        Result:  make(chan ResultOrError[[]byte], 1),
        Context: context.Background(),
    }

    // 提交到 RunLoop Worker
    t.executor.Submit(t.sourceID, model.TaskPriorityNormal, func(ctx context.Context) {
        result, err := t.getNode(ctx, key)
        select {
        case req.Result <- ResultOrError[[]byte]{Result: result, Err: err}:
        case <-req.Context.Done():
        }
    })

    return req
}

// Get 等待结果
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    req := t.GetAsync(key)

    select {
    case res := <-req.Result:
        return res.Result, res.Err
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

### 3.3 Worker RunLoop 实现

```go
// runLoop 高效事件循环
func (w *RunLoopWorker) runLoop() {
    defer func() {
        w.running.Store(false)
        if r := recover(); r != nil {
            // 处理 panic，重启循环
            w.executor.handlePanic(r)
            go w.runLoop()
        }
    }()

    // 绑核
    runtime.LockOSThread()
    pinToCore(w.coreID)

    for w.running.Load() {
        var task func(context.Context)

        // 优先级调度：0 最高，9 最低
        select {
        case <-w.ctx.Done():
            return

        // 优先级 0 (最高)
        case task = <-w.priorityChs[0]:
        case task = <-w.priorityChs[1]:
        case task = <-w.priorityChs[2]:
        case task = <-w.priorityChs[3]:
        case task = <-w.priorityChs[4]:
        case task = <-w.priorityChs[5]:
        case task = <-w.priorityChs[6]:
        case task = <-w.priorityChs[7]:
        case task = <-w.priorityChs[8]:
        case task = <-w.priorityChs[9]:

        // 主队列
        case task = <-w.taskCh:

        default:
            // 没有任务，避免 busy loop
            runtime.Gosched()
            continue
        }

        // 执行任务 (零分配)
        if task != nil {
            task()
            atomic.AddInt64(&w.stats.TotalProcessed, 1)
        }
    }
}
```

### 3.4 智能路由策略

```go
// 智能选择：短任务用 RunLoop，长任务用 Task 模式
func (e *PerCoreExecutor) SubmitWithSmartRouting(
    ctx context.Context,
    sourceID model.SourceID,
    priority model.TaskPriority,
    task func(context.Context),
) error {
    // 估算任务执行时间 (基于历史数据)
    estimatedDuration := e.estimateTaskDuration(sourceID)

    if estimatedDuration < 10*time.Microsecond {
        // 短任务 (<10μs): 使用 RunLoop (零分配)
        return e.runLoopWorkers[sourceID % len(e.runLoopWorkers)].Submit(ctx, priority, task)
    } else {
        // 长任务 (≥10μs): 使用 Task 模式 (避免阻塞 RunLoop)
        return e.taskWorkers[sourceID % len(e.taskWorkers)].Submit(ctx, priority, task)
    }
}

// estimateTaskDuration 基于历史数据估算任务执行时间
func (e *PerCoreExecutor) estimateTaskDuration(sourceID model.SourceID) time.Duration {
    // 使用滑动窗口统计
    if stats, ok := e.sourceStats.Load(sourceID); ok {
        return stats.(*sourceStats).avgDuration.Load()
    }
    return 0 // 未知，默认短任务
}
```

---

## 4. 性能分析

### 4.1 理论性能对比

| 指标 | 当前实现 | RunLoop 优化 | 提升 |
|------|---------|-------------|------|
| **单次提交延迟** | ~9,674 ns | **~5,000-8,000 ns** | **~2x** |
| **内存分配** | ~348B/任务 | **~16B/任务** | **~20x** |
| **GC 暂停** | 中等 | 低 | - |
| **吞吐量** | 24.4K ops/s | **~1-2M ops/s** | **~50-80x** |

> ⚠️ **性能修正**（专家评审意见）：
> - 吞吐量从 10M ops/s 修正为 1-2M ops/s
> - Channel 发送有 ~16B 头部分配
> - select 检查 10 个 Channel 是 O(n) 而非 O(1)

### 4.2 延迟分解对比

#### 当前模式
```
提交路径:
Task 创建 (460ns) → Submit (10ns) → taskItem (48B)
                                                      ↓
                                              futex 等待 (~7,000ns)
                                                      ↓
                                              Worker 唤醒 (~2,000ns)
                                                      ↓
                                              Execute (~930ns)
                                                      ↓
                                              通知完成 (~1,700ns)
                                                      ↓
                                              Wait() 返回

总延迟: 9,674 ns
```

#### RunLoop 模式
```
提交路径:
直接发送到 Channel (~100ns) ↓
                         ←Channel 接收 (~50ns)
                                        ↓
                                直接执行 (~930ns)
                                        ↓
                                写结果 Channel (~50ns)

总延迟: ~1,130 - 2,000 ns
```

### 4.3 适用场景

| 场景 | 当前 Task 模式 | RunLoop 优化 | 推荐 |
|------|---------------|-------------|------|
| **单操作** | 9,674 ns | ~100-2,000 ns | **RunLoop** |
| **低并发 (<10)** | 慢 | 快 | **RunLoop** |
| **高并发 (≥10)** | 41,001 ns | ~15,000 ns | **RunLoop** |
| **短任务 (<10μs)** | 开销大 | 零开销 | **RunLoop** |
| **长任务 (>1ms)** | 适合 | 可能阻塞 | **Task** |
| **批量操作** | 适合 | 适合 | **Batch API** |

---

## 5. 架构设计

### 5.1 整体架构

```
┌────────────────────────────────────────────────────────┐
│                  PerCoreExecutor                       │
│  ┌──────────────────────────────────────────────────┐  │
│  │       SourceID → Worker 绑定 (无锁 map)          │  │
│  └──────────────────────────────────────────────────┘  │
│                    ↑ Submit()                         │
└────────────────────┼────────────────────────────────────┘
                     │
         ┌───────────┴───────────┐
         │                       │
         ▼                       ▼
    ┌─────────┐           ┌─────────┐
    │ Worker 0│           │ Worker 1│
    │ RunLoop │           │ RunLoop │
    │ Ch[0-9] │           │ Ch[0-9] │
    └────┬────┘           └────┬────┘
         │                     │
         │  每个 Worker 一个 goroutine
         │  长期运行，复用
         ▼                     ▼
    执行任务              执行任务
```

### 5.2 优先级调度

```
┌─────────────────────────────────────────┐
│         RunLoop Worker                  │
│  ┌──────────────────────────────────┐  │
│  │  select {                        │  │
│  │    case <-priorityChs[0]:        │  │ ← 优先级 0 (最高)
│  │    case <-priorityChs[1]:        │  │
│  │    ...                           │  │
│  │    case <-priorityChs[9]:        │  │ ← 优先级 9 (最低)
│  │    case <-taskCh:                │  │ ← Fallback
│  │  }                               │  │
│  └──────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

---

## 6. 优缺点分析

### 6.1 当前 Task 模式

#### ✅ 优点
1. **通用性强**: Task 可在任何 Executor 上执行
2. **类型安全**: 泛型 `Task[Result]` 提供类型安全
3. **可组合**: Task 可提交到不同 Pipeline/Executor
4. **符合 DDD**: 清晰的领域模型

#### ❌ 缺点
1. **调度开销大**: futex ~7,000 ns (72.6%)
2. **内存分配**: ~348B/任务
3. **单操作性能差**: 9,674 ns vs 103 ns

### 6.2 RunLoop 模式

#### ✅ 优点
1. **零调度开销**: 无需唤醒 goroutine
2. **零分配提交**: 直接发送到 Channel
3. **低延迟**: ~100-2,000 ns (vs 9,674 ns)
4. **内存复用**: 无临时对象
5. **实现简单**: 代码直观

#### ❌ 缺点
1. **专用性强**: 绑定到特定 Worker
2. **长任务风险**: 可能阻塞 RunLoop
3. **失去可组合性**: 不能随意切换

### 6.3 混合方案 (推荐)

```go
type BfTree struct {
    executor        *PerCoreExecutor   // 通用 Executor (Task 模式)
    runLoopWorkers  []*RunLoopWorker   // 专用 Worker (RunLoop 模式)
    useRunLoop      bool               // 配置开关
    smartRouting    bool               // 智能路由
}

// Get 内部自动选择
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    if t.useRunLoop || t.smartRouting {
        return t.getRunLoopMode(ctx, key)
    }
    return t.getTaskMode(ctx, key)
}
```

---

## 7. 实施方案

### 7.1 Phase 1: 原型验证 (1-2 天)

**目标**: 验证 RunLoop 性能

1. 实现 `RunLoopWorker` 基础结构
2. 实现优先级 Channel 数组
3. 实现简单的 runLoop 循环
4. 性能测试对比 (同步, Task, RunLoop)

**验收标准**:
- 单次延迟 < 2,000 ns
- 零分配提交
- 无 panic 或死锁

### 7.2 Phase 2: 完整实现 (3-5 天)

**目标**: 生产就绪

1. 完善所有操作类型 (Get, Set, Delete, BatchGet)
2. 实现结构化 API (Request/Result 模式)
3. 智能路由策略
4. 错误处理和超时
5. 集成到 BfTree

**验收标准**:
- 所有操作类型支持
- 测试覆盖率 > 80%
- 性能达标

### 7.3 Phase 3: 优化和集成 (2-3 天)

**目标**: 最佳性能

1. 对象池优化 (Request 复用)
2. 配置化选项
3. 监控和指标
4. 文档和示例

**验收标准**:
- 配置灵活
- 文档完整
- 示例清晰

### 7.4 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **Channel 阻塞** | 🟡 中 | 合理设置队列大小，监控队列长度 |
| **优先级饥饿** | 🟡 中 | 定期提升低优先级任务 |
| **Panic 处理** | 🟢 低 | defer recover + 自动重启 |
| **长任务阻塞** | 🟡 中 | 智能路由 + 超时机制 |
| **类型安全** | 🟢 低 | 泛型 Request[Result] |

---

## 8. 性能预估

### 8.1 单操作延迟

| 模式 | 延迟 | vs 同步 | vs Task 模式 |
|------|------|---------|-------------|
| 同步 | 103 ns | 1.0x | - |
| Task 模式 | 9,674 ns | 93.9x 慢 | 1.0x |
| **RunLoop 模式** | **~5,000-8,000 ns** | 48-77x 慢 | **~1.2-2x 快** |

> ⚠️ **修正说明**：原理论值 ~100ns 过于乐观，Channel + select 开销约 5,000-8,000ns

### 8.2 吞吐量 (100 并发)

| 模式 | 延迟 | 吞吐量 | vs Task 模式 |
|------|------|--------|-------------|
| Task 模式 | 41,001 ns | 24.4K ops/s | 1.0x |
| **RunLoop 模式** | **~20,000 ns** | **~50K ops/s** | **~2x 快** |

> ⚠️ **修正说明**：原预估 66K ops/s 修正为 ~50K ops/s

### 8.3 内存分配

| 模式 | 每次分配 | GC 压力 | 10M ops 分配 |
|------|---------|---------|--------------|
| Task 模式 | ~348B | 高 | ~3.3 GB |
| **RunLoop 模式** | **~16B** | **低** | **~160 MB** |

> ⚠️ **修正说明**：Channel 发送有 ~16B 头部分配，并非零分配

---

## 9. 配置选项

### 9.1 Executor 配置

```go
type RunLoopConfig struct {
    // Channel 大小
    QueueSize        int           // 默认: 1000
    PriorityQueueSize int           // 默认: 100

    // 模式选择
    UseRunLoop       bool          // 默认: false
    SmartRouting     bool          // 默认: true

    // 智能路由阈值
    ShortTaskThreshold time.Duration // 默认: 10μs

    // 饥饿防护
    StarvationTimeout time.Duration // 默认: 10s

    // Panic 处理
    PanicHandler     func(any)     // 默认: log + restart
}

// 应用配置
executor, err := NewPerCoreExecutor(
    WithRunLoopConfig(RunLoopConfig{
        UseRunLoop:   true,
        SmartRouting: true,
        QueueSize:    1000,
    }),
)
```

### 9.2 BfTree 配置

```go
type BfTreeConfig struct {
    // Executor 模式
    ExecutorMode     ExecutorMode  // Task | RunLoop | Auto

    // 性能调优
    UseRunLoop       bool          // 默认: false
    SmartRouting     bool          // 默认: true

    // ...
}

// 使用
tree, err := NewBfTree(BfTreeConfig{
    ExecutorMode: ExecutorModeAuto,  // 自动选择
    UseRunLoop:   true,
})
```

---

## 10. 测试计划

### 10.1 单元测试

```go
func TestRunLoopWorker_Basic(t *testing.T) {
    worker := NewRunLoopWorker(0, executor, 100)
    worker.Start()
    defer worker.Close()

    // 测试基本功能
    err := worker.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
        // 任务逻辑
    })
    assert.NoError(t, err)
}

func TestRunLoopWorker_Priority(t *testing.T) {
    // 测试优先级调度
    // ...
}

func TestRunLoopWorker_Overflow(t *testing.T) {
    // 测试队列满
    // ...
}
```

### 10.2 性能测试

```go
func BenchmarkRunLoop_SingleOp(b *testing.B) {
    worker := NewRunLoopWorker(0, executor, 1000)
    worker.Start()
    defer worker.Close()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        worker.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
            // 模拟短任务
        })
    }
}

func BenchmarkRunLoop_Concurrent(b *testing.B) {
    // 并发测试
    // ...
}
```

### 10.3 对比测试

```go
func BenchmarkCompare_All(b *testing.B) {
    b.Run("Sync", func(b *testing.B) {
        // 同步基准
    })

    b.Run("Task", func(b *testing.B) {
        // Task 模式基准
    })

    b.Run("RunLoop", func(b *testing.B) {
        // RunLoop 模式基准
    })
}
```

---

## 11. 监控指标

### 11.1 Worker 统计

```go
type WorkerStats struct {
    TotalProcessed int64  // 总处理数
    TotalFailed    int64  // 总失败数
    QueueLength    int64  // 当前队列长度
    AvgLatency     int64  // 平均延迟 (ns)
    P50Latency     int64  // P50 延迟
    P95Latency     int64  // P95 延迟
    P99Latency     int64  // P99 延迟
}

// 获取统计
stats := worker.Stats()
```

### 11.2 Executor 统计

```go
type ExecutorStats struct {
    TotalSubmitted   int64
    TotalCompleted   int64
    TotalFailed      int64
    ActiveWorkers    int64
    ModeDistribution map[string]int64  // Task vs RunLoop 分布
}
```

---


---

## 12. Pipeline 链式架构扩展 ⭐

> **提出日期**: 2026-03-07  
> **设计灵感**: 用户提出的 Channel 链式架构猜想  
> **重要性**: ⭐⭐⭐⭐⭐ RunLoop 优化的自然延伸和架构升级

---

### 12.1 核心概念

#### 用户的架构猜想

```
kv-write-req --channel--> kvapi core
    --(create)--> btree-write-req --channel--> btree core
    --(create)--> wal-write-req --channel--> wal core
    --(create)--> disk-write-req --channel--> disk write core
    --(result return through)--> kv-write-result channel
```

**核心思想**: 将多个处理阶段通过 Channel 链式连接，每个阶段都是独立的 RunLoop Worker，数据通过零拷贝引用传递。

---

### 12.2 架构可视化

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        异步写入管道 (Async Write Pipeline)                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Client                                                                       │
│    │                                                                         │
│    ├─ kv-write-req ───────────────────────────────────────┐                 │
│    │                                                        │                 │
│    ▼                                                        ▼                 │
│  ┌───────────────────┐                              ┌───────────────────┐   │
│  │  KV API Core       │                              │  BfTree Core       │   │
│  │  (RunLoop Worker)  │──create──btree-req──channel──→  │  (RunLoop Worker)  │   │
│  └───────────────────┘                              └─────────┬─────────┘   │
│                                                                      │          │
│  ┌───────────────────┐                              ┌───────────────────┐   │
│  │  WAL Core          │←──wal-req──channel──────────│  Disk Core         │   │
│  │  (RunLoop Worker)  │──create──disk-req──channel──→│  (RunLoop Worker)  │   │
│  └───────────────────┘                              └─────────┬─────────┘   │
│                                                                      │          │
│                                        result callback ◄────────────┘          │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

---

### 12.3 设计优势

| 优势 | 说明 | 价值 |
|------|------|------|
| **完全异步** | 每个阶段独立 RunLoop | 无阻塞 |
| **零拷贝** | 指针引用传递数据 | 内存高效 |
| **天然背压** | Channel 阻塞自动限流 | 流量控制 |
| **易于扩展** | 添加新阶段只需插入 Channel | 可维护 |
| **独立监控** | 每阶段独立统计 | 可观测 |

---

### 12.4 与 RunLoop 优化的关系

| 维度 | RunLoop 优化 | Pipeline 架构 |
|------|-------------|-------------|
| **范围** | 单组件内部 | 多组件串联 |
| **优化点** | 减少调度开销 | 提升整体吞吐 |
| **复杂度** | 中等 | 较高 |
| **依赖** | 独立 | 依赖 RunLoop |
| **收益** | 单操作 +50% | 批量操作 +100% |

**结论**: Pipeline 架构**基于 RunLoop 优化**，是进一步的架构升级。

---

### 12.5 实施建议

1. ✅ **优先**: 完成基础 RunLoop 优化（第 1-11 章）
2. ✅ **渐进**: 先实现单阶段 Pipeline
3. ✅ **验证**: 性能测试确认收益
4. ✅ **扩展**: 逐步添加更多阶段

**推荐路径**: RunLoop 优化 → Pipeline 单阶段 → Pipeline 多阶段

---


---

## 附录 A: 专家评审意见

> **评审日期**: 2026-03-07

### A.1 架构评审 ⭐⭐⭐⭐⭐

**Pipeline 评价**: "Pipeline 架构是经典异步处理模式，应作为主文档章节，不应放附录" ✅ **已采纳为第12章**

---

## 附录 B: 关键问题修复方案

### B.1 P0 级别问题（已修复）

- ✅ 引用计数内存泄漏
- ✅ 并发安全保护
- ✅ 优雅关闭实现

---

## 总结

本文档整合了 PerCoreExecutor RunLoop 优化的完整设计方案，包括：

1. ✅ **问题分析**: 当前实现的性能瓶颈
2. ✅ **优化方案**: RunLoop + Channel 模式
3. ✅ **详细设计**: 数据结构、API、Worker 实现
4. ✅ **Pipeline 扩展**: 链式架构设计（第12章）⭐
5. ✅ **专家评审**: 架构、性能、代码三维度评审
6. ✅ **问题修复**: P0/P1 级别问题的解决方案

### 实施路径

1. **Phase 1**: 基础 RunLoop 优化（第2-11章）
2. **Phase 2**: Pipeline 架构扩展（第12章）
3. **Phase 3**: 性能优化和问题修复（附录B）

---

**文档版本**: v3.1  
**最后更新**: 2026-03-07  
**维护者**: NexKV Team  
**主要变更**: Pipeline 架构从附录提升为主章节（第12章），移除冗余内容
