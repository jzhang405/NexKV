# TaskScheduler 优先级队列 Proposal

**日期**: 2026-03-31
**分支**: `perf/task-scheduler-priority-queue`
**状态**: Proposal（V2 — 参考 executor_percore.go 优化）
**前置**: `88dc7b0` (mmap 动态计算 + allocator Free 修复)

---

## 0. 架构示意图

### 0.1 当前架构（改前）

```
                         ┌─────────────────────────────────────────────────────────┐
                         │                    TaskScheduler                         │
                         │  cores: []*SchedulerCore  (coreCount = NumCPU)          │
                         └──────────────┬──────────────────────┬───────────────────┘
                                        │                      │
                    ┌───────────────────┴──────┐   ┌───────────┴──────────────────┐
                    │    SchedulerCore 0        │   │    SchedulerCore 1           │
                    │  runLoop (goroutine)      │   │  runLoop (goroutine)         │
                    │                           │   │                              │
                    │  cachedTasks []*ShardTask │   │  cachedTasks []*ShardTask    │
                    │  ┌─────────┬───────────┐  │   │  ┌──────────┬────────────┐  │
                    │  │ idx=0   │ idx=1     │  │   │  │ idx=0    │ idx=1      │  │
                    │  │ btree-  │ btree-    │  │   │  │ btree-   │ btree-     │  │
                    │  │ set     │ split     │  │   │  │ set      │ split      │  │
                    │  │ eo=0    │ eo=1      │  │   │  │ eo=0     │ eo=1       │  │
                    │  │ pri=N   │ pri=H     │  │   │  │ pri=N    │ pri=H      │  │
                    │  └────┬────┴─────┬─────┘  │   │  └────┬─────┴──────┬─────┘  │
                    │       │          │         │   │       │            │         │
                    │  ┌────▼────┐ ┌───▼──────┐  │   │  ┌───▼─────┐ ┌───▼──────┐  │
                    │  │ MPSC    │ │ MPSC     │  │   │  │ MPSC    │ │ MPSC     │  │
                    │  │ Queue   │ │ Queue    │  │   │  │ Queue   │ │ Queue    │  │
                    │  │ [set    │ │ [split   │  │   │  │ [set    │ │ [split   │  │
                    │  │  items] │ │  items]  │  │   │  │  items] │ │  items]  │  │
                    │  └─────────┘ └──────────┘  │   │  └─────────┘ └──────────┘  │
                    │                           │   │                              │
                    │  runLoop: linear scan     │   │  runLoop: linear scan        │
                    │  for _, task := range     │   │  for _, task := range        │
                    │    cachedTasks { ... }    │   │    cachedTasks { ... }       │
                    │                           │   │                              │
                    │  ❌ priority ignored       │   │  ❌ priority ignored          │
                    │  ❌ always eo=0 first      │   │  ❌ always eo=0 first        │
                    └───────────────────────────┘   └──────────────────────────────┘

  EnqueueWithShard routing:
    shardID % coreCount → coreIndex
    tasksSnapshot[taskOrder] → ShardTask.Enqueue(item)
```

### 0.2 目标架构（改后）

```
                         ┌─────────────────────────────────────────────────────────┐
                         │                    TaskScheduler                         │
                         │  cores: []*SchedulerCore  (coreCount = NumCPU)          │
                         └──────────────┬──────────────────────┬───────────────────┘
                                        │                      │
                    ┌───────────────────┴──────┐   ┌───────────┴──────────────────┐
                    │    SchedulerCore 0        │   │    SchedulerCore 1           │
                    │  runLoop (goroutine)      │   │  runLoop (goroutine)         │
                    │                           │   │                              │
                    │  activeBitmap: 0b0000100010│   │  activeBitmap: 0b0000100010   │
                    │                     ↑    ↑  │   │                      ↑    ↑  │
                    │                     │    │  │   │                      │    │  │
                    │                pri=1  pri=5 │   │                 pri=1  pri=5 │
                    │                (High) (Nrm) │   │                 (High)(Nrm) │
                    │                           │   │                              │
                    │  priorityBuckets[0..9]:    │   │  priorityBuckets[0..9]:      │
                    │  ┌──────────────────────┐  │   │  ┌──────────────────────┐    │
                    │  │ [0] []               │  │   │  │ [0] []               │    │
                    │  │ [1] [btree-split]  ←─╂──╂───╂──╂─[1] [btree-split]  │    │
                    │  │ [2] []               │  │   │  │ [2] []               │    │
                    │  │ [3] []               │  │   │  │ [3] []               │    │
                    │  │ [4] []                  [4] []    │    │
                    │  │ [5] [btree-set]    ←─╂──╂───╂──╂─[5] [btree-set]               │    │
                    │  │ ...                  │  │   │  │ ...                  │    │
                    │  │ [9] []               │  │   │  │ [9] []               │    │
                    │  └──────────────────────┘  │   │  └──────────────────────┘    │
                    │       │          │         │   │       │          │            │
                    │  ┌────▼────┐ ┌───▼──────┐  │   │  ┌───▼─────┐ ┌──▼───────┐   │
                    │  │ MPSC    │ │ MPSC     │  │   │  │ MPSC    │ │ MPSC     │   │
                    │  │ [split  │ │ [set     │  │   │  │ [split  │ │ [set     │   │
                    │  │  items] │ │  items]  │  │   │  │  items] │ │  items]  │   │
                    │  └─────────┘ └──────────┘  │   │  └─────────┘ └──────────┘   │
                    │                           │   │                              │
                    │  runLoop: bitmap O(1)     │   │  runLoop: bitmap O(1)        │
                    │  p = TrailingZeros16(     │   │  p = TrailingZeros16(        │
                    │    activeBitmap)           │   │    activeBitmap)             │
                    │  → p=1 (split first!)     │   │  → p=1 (split first!)       │
                    │                           │   │                              │
                    │  ✅ split always before set│   │  ✅ split always before set  │
                    │  ✅ empty buckets skipped  │   │  ✅ empty buckets skipped    │
                    └───────────────────────────┘   └──────────────────────────────┘

  EnqueueWithShard routing (unchanged):
    shardID % coreCount → coreIndex
    taskMap[taskName] → ShardTask.Enqueue(item)
```

### 0.3 Sequence Chart: Set 操作经过优先级调度

```
  Goroutine 1 (Set)          TaskScheduler           SchedulerCore 0              runLoop
  ─────────────────          ────────────           ─────────────────              ───────
       │                          │                        │                          │
       │  Set(key, value)         │                        │                          │
       │──────────────────────────│                        │                          │
       │                          │                        │                          │
       │  EnqueueWithShard()      │                        │                          │
       │  shardID=42              │                        │                          │
       │  coreIndex = 42 % 12 = 6 │                        │                          │
       │──────────────────────────│                        │                          │
       │                          │                        │                          │
       │                          │  cores[6].taskMap      │                          │
       │                          │  ["btree-set"].Enqueue │                          │
       │                          │───────────────────────>│                          │
       │                          │                        │                          │
       │                          │  cores[6].wakeup()     │                          │
       │                          │───────────────────────>│                          │
       │                          │                        │    cond.Signal()          │
       │                          │                        │──────────────────────────>│
       │                          │                        │                          │
       │                          │                        │                          │ ◄─── wakeup
       │                          │                        │                          │
       │                          │                        │    checkStarvation()      │
       │                          │                        │    (10ms interval)        │
       │                          │                        │──────────────────────────>│
       │                          │                        │                          │
       │                          │                        │    bitmap scan:           │
       │                          │                        │    activeBitmap = 0b0100010│
       │                          │                        │                          │
       │                          │                        │    ┌─── p=1 (High) ──────>│
       │                          │                        │    │ btree-split bucket   │
       │                          │                        │    │ [tryProcessBatch]    │
       │                          │                        │    │ (split items first!) │
       │                          │                        │    │                      │
       │                          │                        │    ├── p=5 (Normal) ─────>│
       │                          │                        │    │ btree-set bucket     │
       │                          │                        │    │ [tryProcessBatch]    │
       │                          │                        │    │ → process item       │
       │                          │                        │    │ → setWithLeafLock()  │
       │                          │                        │    │                      │
       │                          │                        │    └── done               │
       │                          │                        │                          │
       │  <── result via Wait() ──│                        │                          │
       │                          │                        │                          │
```

### 0.4 Sequence Chart: Split 优先于 Set

```
  Timeline:
  ──────────────────────────────────────────────────────────────────────────>

  SchedulerCore 0 runLoop:

    t0: bitmap = c.activeBitmap   ← 局部变量拷贝，c.activeBitmap 不被修改
        bitmap = 0b0100010
        TrailingZeros16 → p=1 (btree-split bucket)

        ┌─────────────────────────┐
        │ btree-split: dequeue &  │  ← split task 入队后立即被处理
        │ execute split           │     不受 set 队列长度影响
        └────────────┬────────────┘
                     │
    t1: bitmap &^= (1<<1)         ← 清除局部变量 bit 1
        bitmap = 0b0100000
        TrailingZeros16 → p=5 (btree-set bucket)

        ┌─────────────────────────┐
        │ btree-set: tryBatch(16) │  ← 批量处理 set items
        │ → process 16 set ops   │
        └────────────┬────────────┘
                     │
    t2: back to top, scan bitmap again
        split 有新 item? → 立即处理
        split 无 item?  → 继续 set
```

---

## 1. 问题

### 1.1 现状：注册时有优先级，执行时无优先级

```
ShardTask 按 executionOrder 排序（btree-set=0, btree-split=1）
runLoop 遍历 cachedTasks（按 executionOrder 顺序）

但 runLoop 的遍历顺序由 executionOrder 决定，不由 priority 决定：
  - btree-set：executionOrder=0 → 最先被遍历 → 优先处理
  - btree-split：executionOrder=1 → 第二个被遍历 → 必须等 set 处理完

两个 ShardTask 有独立的 MPSC 队列，不存在共享队列的问题。
真正的问题是：runLoop 总是先处理 set（eo=0），再处理 split（eo=1），
即使 split 的 Priority=High 也不会提前执行。
```

### 1.2 实际影响

当前注册了两个 task 类型：

| Task | ExecutionOrder | Priority | 队列 |
|------|---------------|----------|------|
| btree-set | 0 (最先执行) | Normal | 每个 core 独立 MPSC 队列 |
| btree-split | 1 | High | 每个 core 独立 MPSC 队列 |

**`executionOrder` 只控制 task 类型之间的执行顺序**，不控制同类型 items 之间的优先级。

split 任务虽然 Priority=High，但如果前面有大量 set 任务排队，split 必须等 set 全部执行完才轮到（因为 set 的 executionOrder=0 比 split=1 更先被遍历）。

### 1.3 实验数据佐证

实验（Set 全走 TaskScheduler + 禁用重试）：

| 配置 | Success 率 | ops/sec |
|------|-----------|---------|
| 500 init, 1t × 100K | 100% | 43,607 |
| 500K init, 2t × 10K | 93.1% | 69,040 |
| 500K init, 4t × 10K (3.2GB) | 99.7% | 20,669 |
| 500K init, 8t × 10K (3.2GB) | 99.9% | 21,255 |

8 线程不扩展（21K ≈ 4 线程 20K），瓶颈从 PageLock 竞争转移到 TaskScheduler 单 shard 串行执行。

---

## 2. 目标

**每个 SchedulerCore 维护 bitmap 优化的多级优先级桶，高优先级任务 O(1) 优先执行。**

参考 `executor_percore.go` 中已验证的 `taskQueue` 实现：
- bitmap + `bits.TrailingZeros16()` 实现 O(1) 最高优先级查找
- 饥饿防护：超时低优先级任务自动提升
- `sync.RWMutex` 保护并发访问

```
SchedulerCore {
    priorityBuckets [NumPriorityLevels][]*ShardTask  // 优先级桶
    bitmap          uint16                           // 位图标记非空桶
}

runLoop {
    // O(1) 找最高优先级非空桶，而非循环遍历
    p := bits.TrailingZeros16(bitmap)
    for _, task := range priorityBuckets[p] {
        tryProcessBatch(task) || processSingle(task)
    }
}
```

---

## 3. 方案设计

### 3.0 参考实现：executor_percore.go 的 taskQueue

**文件**: `internal/infrastructure/concurrency/executor_percore.go`

已实现的高性能多级优先级队列，核心优化模式：

```go
// executor_percore.go:142-149
type taskQueue struct {
    queues            [NumPriorityLevels][]taskItem  // 10 级优先级队列
    bitmap            uint16                         // 位图：位 0-9 表示对应优先级队列是否有任务
    starvationCheck   int64                          // 上次饥饿检查时间（纳秒）
    starvationTimeout time.Duration                  // 饥饿防护超时时间
    checkInterval     int64                          // 饥饿检查间隔（默认 10ms）
    mu                sync.RWMutex                   // 读写锁
}
```

**关键优化**:

| 操作 | 复杂度 | 实现 |
|------|--------|------|
| Push | O(1) | `append` + `bitmap |= (1 << p)` (L187-203) |
| Pop | O(1) | `bits.TrailingZeros16(bitmap)` 找最高优先级 (L207-250) |
| 饥饿防护 | 均摊 O(1) | `promoteStarvedTasks()` 每 10ms 检查 (L255-292) |

**适配差异**: `taskQueue` 管理的是 `taskItem`（直接函数指针），而 `SchedulerCore` 管理的是 `ShardTask`（每个 task 类型有独立 MPSC 队列）。方案 A 在 ShardTask 级别分桶。

### 3.1 SchedulerCore 改为 bitmap 优化的优先级桶

**当前结构**（`task_scheduler.go:200-225`）：

```go
type SchedulerCore struct {
    tasks         []*ShardTask       // 按 executionOrder 排序的平面数组
    tasksSnapshot atomic.Value       // []*ShardTask
    cachedTasks   []*ShardTask       // runLoop 缓存
}
```

**目标结构**（参考 executor_percore.go:139-149 的 taskQueue 模式，**不保留旧字段**）：

```go
const NumPriorityLevels = 10  // TaskPriorityCritical(0) ~ TaskPriorityIdle(9)

type SchedulerCore struct {
    coreID int

    // 优先级桶：替代原来的 tasks []*ShardTask 平面数组
    // 参考 executor_percore.go taskQueue.queues
    priorityBuckets [NumPriorityLevels][]*ShardTask  // 按优先级分桶，桶内按 executionOrder 排序
    activeBitmap    uint16                          // 位图：标记哪些优先级桶有注册的 ShardTask

    // task lookup（简化：不再需要 tasksSnapshot，直接用 map）
    taskMap map[string]*ShardTask  // name → ShardTask（RegisterTask 时填充，只读）

    // runLoop 缓存（避免每轮重建）
    cachedBuckets [NumPriorityLevels][]*ShardTask

    // 饥饿防护（参考 executor_percore.go:146-147）
    starvationCheck   int64          // 上次饥饿检查时间（纳秒）
    starvationTimeout time.Duration  // 默认 100ms（split 任务不至于等太久）
    checkInterval     int64          // 检查间隔，默认 10ms

    // 调度控制
    mu              sync.Mutex
    running         atomic.Bool
    wg              sync.WaitGroup
    ctx             context.Context
    cancel          context.CancelFunc
    stats           CoreStats
    cond            *sync.Cond
    totalQueueItems atomic.Int64
    batchBuffer     []any
}
```

**删除的旧字段**：
- `tasks []*ShardTask` → `priorityBuckets`
- `tasksSnapshot atomic.Value` → 不需要，runLoop 直接读 `cachedBuckets`
- `taskMap atomic.Value` → 普通 `map[string]*ShardTask`（RegisterTask 仅在启动时调用，无并发问题）

> **并发安全说明**: Go map 的并发只读（多 goroutine 同时读取、无写入）是安全的。`taskMap` 在 `Start()` 之前由 RegisterTask 填充（单线程），`Start()` 之后不再写入。`EnqueueWithShard` 的并发调用只读取 `taskMap`，因此无需锁保护。代码中应添加注释标注此约束：`// taskMap: 只读（Start 后不再写入），并发只读安全，无需锁`。

### 3.2 RegisterTask 改为写入优先级桶 + bitmap 更新

**当前逻辑**（`insertTaskOrdered`，L275-282）：

```go
func (c *SchedulerCore) insertTaskOrdered(task *ShardTask) {
    // 按 executionOrder 插入到 c.tasks 平面数组
    pos := sort.Search(len(c.tasks), func(i int) bool {
        return c.tasks[i].ExecutionOrder() >= task.ExecutionOrder()
    })
    c.tasks = append(c.tasks, nil)
    copy(c.tasks[pos+1:], c.tasks[pos:])
    c.tasks[pos] = task
}
```

**改为**（参考 executor_percore.go:187-203 的 Push 模式）：

```go
func (c *SchedulerCore) insertTaskOrdered(task *ShardTask) {
    p := int(task.Priority())
    p = max(0, min(p, NumPriorityLevels-1))

    bucket := c.priorityBuckets[p]

    // 桶内仍按 executionOrder 排序（保持同优先级内的确定性顺序）
    insertPos := sort.Search(len(bucket), func(i int) bool {
        return bucket[i].ExecutionOrder() >= task.ExecutionOrder()
    })

    bucket = append(bucket, nil)
    copy(bucket[insertPos+1:], bucket[insertPos:])
    bucket[insertPos] = task
    c.priorityBuckets[p] = bucket

    // bitmap 置位：标记该优先级桶有 ShardTask
    // 注意：与 executor_percore.go Push 的条件置位（if wasEmpty）不同，
    // 此处无条件置位，因为 activeBitmap 标记的是"桶中有注册的 ShardTask"而非"队列中有待处理 item"
    c.activeBitmap |= (1 << p)
}
```

### 3.3 runLoop 改为 bitmap O(1) 优先级遍历

**当前逻辑**（L347）：

```go
for _, task := range c.cachedTasks {
    // FIFO 处理所有 task 类型
}
```

**改为**（参考 executor_percore.go:207-250 的 Pop 模式）：

```go
// runLoop 核心调度循环
func (c *SchedulerCore) runLoop() {
    c.cachedBuckets = c.getOrderedBuckets()

    for {
        // ... context 检查、totalQueueItems 预检查不变 ...

        // 饥饿防护检查（参考 executor_percore.go:212-222）
        c.checkStarvation()

        // 使用 bitmap O(1) 查找有任务的非空桶（参考 executor_percore.go:227-238）
        // 遍历所有有注册 ShardTask 的优先级桶
        bitmap := c.activeBitmap
        for bitmap != 0 {
            // O(1) 找最高优先级非空桶
            p := bits.TrailingZeros16(bitmap)
            if p >= NumPriorityLevels {
                break
            }

            // 处理该优先级桶内所有 ShardTask
            for _, task := range c.cachedBuckets[p] {
                if c.tryProcessBatch(task) {
                    continue
                }
                // ... 单个处理逻辑不变（Peek → Execute → Dequeue/Requeue）...
            }

            // 清除已处理的位，继续下一个优先级
            bitmap &^= (1 << p)
        }
    }
}
```

**核心优化**: `bits.TrailingZeros16(bitmap)` 单条 CPU 指令定位最高优先级桶，保证 **PriorityHigh(1) 的 btree-split 始终在 PriorityNormal(5) 的 btree-set 之前被遍历**。这从语义上解决了 "注册时有优先级、执行时无优先级" 的核心问题。

**bitmap 运行时语义**: `activeBitmap` 是**静态注册标记**（非动态队列非空标记）：
- 在 RegisterTask 时置位，标记哪些桶有注册的 ShardTask
- 运行时永不修改 — 值始终为 `0b0000100010`（bit 1 + bit 5）
- runLoop 中 `bitmap` 是局部变量拷贝，`bitmap &^= (1<<p)` 清除的是局部变量，不影响 `c.activeBitmap`

**与 executor_percore.go 的关键差异**: `executor_percore.go` 的 bitmap 是动态的（Push 置位、Pop 清除），能真正跳过空队列。本方案中 `activeBitmap` 是静态的，**不能跳过队列为空的桶**。但这是合理的权衡：
1. 当前仅 2 个 ShardTask，动态清除 bitmap 的开销（每轮检查+清位）> 遍历 2 个桶的开销
2. runLoop 内部通过 `tryProcessBatch` 返回值判断桶是否为空，空桶只消耗一次函数调用
3. 如果后续 ShardTask 数量增长到 10+，可切换为动态 bitmap 模式（改动量小，只需在 Enqueue 时置位、处理完清位）

### 3.4 BTree 使用场景

当前注册：
- `btree-set`：PriorityNormal (5)，ExecutionOrder=0
- `btree-split`：PriorityHigh（应改为更合理值），ExecutionOrder=1

当前 `PriorityHigh(1)` 已足够 — bitmap `0b0000100010` 的 `TrailingZeros16` 返回 p=1（split），p=5（set），split 始终优先于 set。无需改为 `PriorityCritical(0)`。如果后续引入第三种 task 类型（如 btree-merge），再考虑调整优先级梯度。

### 3.5 饥饿防护（参考 executor_percore.go:255-292）

**问题**: 高负载时低优先级任务（如 btree-set）可能永远得不到执行机会。

**解决方案**: 参考 `promoteStarvedTasks()`，定期检查并提升超时的低优先级 ShardTask。

```go
// checkStarvation 定期检查并提升超时的低优先级 ShardTask
// 参考 executor_percore.go:255-292 的 promoteStarvedTasks
func (c *SchedulerCore) checkStarvation() {
    if c.starvationTimeout <= 0 {
        return
    }

    now := time.Now().UnixNano()
    lastCheck := atomic.LoadInt64(&c.starvationCheck)

    // 每 10ms 检查一次（避免频繁扫描的开销）
    if now-lastCheck <= c.checkInterval {
        return
    }

    if !atomic.CompareAndSwapInt64(&c.starvationCheck, lastCheck, now) {
        return  // 另一个 goroutine 已在检查
    }

    // 从低优先级到高优先级遍历（跳过最高优先级 0）
    for p := NumPriorityLevels - 1; p >= 1; p-- {
        for _, task := range c.cachedBuckets[p] {
            submitTime := task.LastSubmitTime()
            if submitTime > 0 && now-submitTime > int64(c.starvationTimeout) {
                // 临时提升该 ShardTask 的执行优先级
                // 方法：在本轮 runLoop 中将 task 移到最高优先级桶前面
                // 注意：不修改 task.priority（保持原始优先级用于后续轮次）
                task.SetPriorityBoost(true)
            }
        }
    }
}
```

**ShardTask 新增字段/方法**（`shard_task.go`）：

```go
// ShardTask 新增字段
type ShardTask struct {
    // ... 现有字段 ...

    // 饥饿防护
    lastSubmitTime  atomic.Int64  // 最近一次 Enqueue 的时间（UnixNano）
    priorityBoost   atomic.Bool   // 饥饿防护：临时提升标志
}

// LastSubmitTime 返回最近一次 Enqueue 的纳秒时间戳
func (t *ShardTask) LastSubmitTime() int64 {
    return t.lastSubmitTime.Load()
}

// SetLastSubmitTime 记录 Enqueue 时间（在 ShardTask.Enqueue 中调用）
func (t *ShardTask) SetLastSubmitTime() {
    t.lastSubmitTime.Store(time.Now().UnixNano())
}

// SetPriorityBoost 设置/清除临时优先级提升
func (t *ShardTask) SetPriorityBoost(boost bool) {
    t.priorityBoost.Store(boost)
}

// HasPriorityBoost 检查是否有临时优先级提升
func (t *ShardTask) HasPriorityBoost() bool {
    return t.priorityBoost.Load()
}
```

**runLoop 集成 priorityBoost**：

```go
// 在 runLoop 的 bitmap 遍历之前，处理被提升的 ShardTask
func (c *SchedulerCore) runLoop() {
    // ...
    for {
        c.checkStarvation()

        // Phase 1: 处理被提升的低优先级 ShardTask（插入到最高优先级桶前面）
        for p := NumPriorityLevels - 1; p >= 1; p-- {
            for _, task := range c.cachedBuckets[p] {
                if task.HasPriorityBoost() {
                    c.tryProcessBatch(task)
                    task.SetPriorityBoost(false)  // 一次性提升，用完恢复
                }
            }
        }

        // Phase 2: 正常 bitmap 优先级遍历（不变）
        bitmap := c.activeBitmap
        for bitmap != 0 {
            p := bits.TrailingZeros16(bitmap)
            if p >= NumPriorityLevels { break }
            for _, task := range c.cachedBuckets[p] {
                c.tryProcessBatch(task)
            }
            bitmap &^= (1 << p)
        }
    }
}
```

**说明**: `priorityBoost` 是一次性提升 — checkStarvation 设置后，runLoop 在 Phase 1 处理并清除。下一轮如果仍超时，checkStarvation 会再次设置。这避免了移动桶的复杂性。

---

## 4. 改动清单

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `task_scheduler.go` SchedulerCore 结构体 | 删除 `tasks []*ShardTask`；新增 `priorityBuckets [10][]*ShardTask` + `activeBitmap uint16`；删除 `tasksSnapshot`/`taskMap` atomic.Value |
| 2 | `task_scheduler.go` `insertTaskOrdered` | 写入对应优先级桶 + bitmap 置位 |
| 3 | `task_scheduler.go` 删除 `updateTaskSnapshots` | 不再需要 COW 快照 |
| 4 | `task_scheduler.go` `runLoop` | bitmap O(1) 优先级遍历替代线性遍历 |
| 5 | `task_scheduler.go` `NewSchedulerCore` | 初始化 10 个桶 + bitmap + 饥饿防护参数 |
| 6 | `task_scheduler.go` 删除 `getOrderedTasks` | 替换为 `getOrderedBuckets` |
| 7 | `task_scheduler.go` 新增 `checkStarvation` | 饥饿防护（参考 executor_percore.go:255-292） |
| 8 | `task_scheduler.go` 新增 `getOrderedBuckets` | 返回所有桶的缓存副本 |
| 9 | `task_scheduler.go` `GetTaskByName` | 改为直接查 `taskMap`（普通 map，不再是 atomic.Value） |
| 10 | `task_scheduler.go` `EnqueueWithShard` | 从 `tasksSnapshot[taskOrder]` 改为 `taskMap[taskName]` 查找 ShardTask |
| 11 | `task_scheduler.go` `calculateCoreQueueLen` | 从 `getOrderedTasks()` 改为遍历 `priorityBuckets`（`HealthCheck` 和 `GetStats` 通过此方法间接受影响，无需单独修改） |
| 12 | `task_scheduler.go` `ShardTask.Enqueue` | 入队成功后调用 `t.SetLastSubmitTime()`，为饥饿防护提供时间戳（L71-81） |
| 13 | `shard_task.go` 新增 `lastSubmitTime` / `priorityBoost` 字段 + 方法 | 饥饿防护辅助（`LastSubmitTime()`, `SetPriorityBoost()`, `HasPriorityBoost()`） |
| 14 | `shard_item.go` `ShardItem` 接口 | `TaskOrder()` 在路由中不再使用（改为 `taskMap[taskName]`），但 `BTreeSetItem`/`ParentSplitItem` 仍保留该字段（用于调试/日志）。建议保留接口方法，标记 deprecated |
| 15 | `task_scheduler_test.go` 测试文件 | 更新 `getOrderedTasks` 相关测试（`TestSchedulerCore_getOrderedTasks_*`）为 `getOrderedBuckets`；新增优先级调度正确性测试（验证 split 优先于 set） |

---

## 5. 破坏性变更

| 删除 | 原因 |
|------|------|
| `tasks []*ShardTask` | 平面数组被 `priorityBuckets` 替代 |
| `cachedTasks []*ShardTask` | runLoop 缓存数组，被 `cachedBuckets [10][]*ShardTask` 替代 |
| `tasksSnapshot atomic.Value` | COW 快照机制复杂且不必要，runLoop 直接读 `cachedBuckets` |
| `taskMap atomic.Value` | RegisterTask 仅启动时调用，改为普通 `map[string]*ShardTask` |
| `getOrderedTasks()` | 替换为 `getOrderedBuckets()`，返回桶结构 |
| `updateTaskSnapshots()` | 不再需要 |

**保留不变**：
- `RegisterTask` 接口签名（已接收 `priority` 参数）
- `EnqueueWithShard` 路由逻辑
- `ShardItem` 接口
- `executeFunc` 签名

---

## 6. 验证

```bash
# 编译
go build ./...

# 单元测试（TaskScheduler）
go test -v -count=1 ./internal/infrastructure/concurrency/...

# 正确性测试
go test -count=1 -timeout 120s -run "TestSetWithLeafLock_Concurrent|TestDebug6000KeysNoLoss" \
    ./internal/infrastructure/storage/btree/...

# 性能测试
go run ./cmd/btree_perf_pprof -threads=8 -count=100000 -init=500
```

---

## 7. 风险

| 风险 | 缓解 |
|------|------|
| 饥饿：低优先级任务永远得不到执行 | 参考 executor_percore.go 的 promoteStarvedTasks 机制，超时自动提升（初始版本可简化） |
| bitmap 竞争：不存在（`activeBitmap` 是静态注册标记） | RegisterTask 在 `Start()` 前单线程写入，`Start()` 后永不修改，runLoop 只读，无竞争 |
| 遍历开销：`activeBitmap` 是静态 bitmap，空桶也会被遍历 | 当前仅 2 个 ShardTask（2 个桶），遍历开销可忽略（2 次 `tryProcessBatch` 调用）。后续 ShardTask 数量增长到 10+ 时可切换为动态 bitmap 模式（Enqueue 置位、处理完清位） |
| 饥饿检查开销：定期扫描所有 ShardTask | 每 10ms 检查一次，且只有活跃 ShardTask 才参与（参考 executor_percore.go:256-279） |
| ShardTask 数量少（当前仅 2 个）：bitmap 优化收益有限 | 优化是通用的，后续新增 task 类型时收益递增；且 bitmap 查找比 `if len == 0` 更简洁 |
| taskMap 不再线程安全 | RegisterTask 在 Start() 之前完成（Start 后只读），无需锁保护 |

---

## 8. 参考

- `executor_percore.go:142-149` — `taskQueue` 结构体：`[NumPriorityLevels][]taskItem` + `bitmap uint16`
- `executor_percore.go:186-203` — `Push()`: O(1) append + 条件 bitmap 置位
- `executor_percore.go:207-250` — `Pop()`: O(1) `bits.TrailingZeros16` + 动态 bitmap 清除
- `executor_percore.go:255-292` — `promoteStarvedTasks()`: 超时低优先级提升
- `executor_percore.go:114-128` — `coreWorker`: 每个 worker 独立 taskQueue
- `task_scheduler.go:200-224` — 当前 `SchedulerCore` 结构（待改）
- `task_scheduler.go:322-390` — 当前 `runLoop`（待改）
- `task_scheduler.go:656-699` — 当前 `EnqueueWithShard`（需改为 taskMap 查找）
- `task_scheduler.go:801-809` — 当前 `calculateCoreQueueLen`（需改为遍历 priorityBuckets）
