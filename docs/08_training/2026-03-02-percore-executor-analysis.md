# PerCoreExecutor 架构分析

## 概述

PerCoreExecutor 是一个高性能的 Per-Core 任务执行器，核心设计目标：

1. **CPU 亲和性**：每个 Worker 绑定到特定 CPU 核心
2. **SourceID 绑定**：相同 SourceID 的任务总是路由到同一 Worker
3. **优先级队列**：10 级优先级，O(1) Push/Pop
4. **饥饿防护**：低优先级任务超时自动提升

## 架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                      PerCoreExecutor                            │
├─────────────────────────────────────────────────────────────────┤
│  config: PerCoreConfig                                          │
│  state: RUNNING | CLOSING | CLOSED                              │
│  sourceBindings: sync.Map  ──────────────────────────────────┐  │
│  bindingMu: sync.Mutex (保护首次绑定)                          │  │
│  submitCount: atomic.Int64                                   │  │
│  cleanupTicker: *time.Ticker                                 │  │
├──────────────────────────────────────────────────────────────┤  │
│                         workers[]                            │  │
│  ┌─────────────────────────────────────────────────────────┐ │  │
│  │ Worker 0 (Core 0)                                       │ │  │
│  │ ├── queue: *taskQueue                                   │ │  │
│  │ │   ├── queues[10][]taskItem (P0-P9)                    │ │  │
│  │ │   ├── bitmap: uint16 (快速查找最高优先级)                │ │  │
│  │ │   └── mu: sync.RWMutex                                │ │  │
│  │ ├── cond: *sync.Cond                                    │ │  │
│  │ └── ctx/cancel                                          │ │  │
│  ├─────────────────────────────────────────────────────────┤ │  │
│  │ Worker 1 (Core 1) ...                                   │ │  │
│  ├─────────────────────────────────────────────────────────┤ │  │
│  │ Worker N-1 (Core N-1) ...                               │ │  │
│  └─────────────────────────────────────────────────────────┘ │  │
│                                                              │  │
│  sourceIDBinding (存储在 sourceBindings)  ◄───────────────────┘
│  ├── workerID: int64
│  └── lastUsedTime: int64
└─────────────────────────────────────────────────────────────────┘
```

## 核心数据结构

### 1. taskQueue - 多级优先级队列

```go
type taskQueue struct {
    queues            [10][]taskItem  // 10 个优先级队列
    bitmap            uint16          // 位图：快速查找最高优先级
    starvationCheck   int64           // 上次饥饿检查时间
    starvationTimeout time.Duration   // 饥饿防护超时
    checkInterval     int64           // 检查间隔 (10ms)
    mu                sync.RWMutex    // 保护队列访问
}
```

**关键操作**：

| 操作 | 时间复杂度 | 说明 |
|------|------------|------|
| Push | O(1) | 追加到对应优先级队列，更新 bitmap |
| Pop | O(1) | 使用 `bits.TrailingZeros16` 找最高优先级 |
| Len | O(10) | 遍历 10 个队列求和 |

### 2. sourceIDBinding - SourceID 绑定

```go
type sourceIDBinding struct {
    workerID     int64  // 绑定的 Worker ID
    lastUsedTime int64  // 最后使用时间（用于超时清理）
}
```

## 任务提交流程

### SubmitWithSource（智能调度）

```
┌─────────────────────────────────────────────────────────────┐
│                    SubmitWithSource                         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ 规则 B：快速路径（无锁）                                │    │
│  │                                                      │    │
│  │ sourceBindings.Load(sourceKey)                       │    │
│  │         │                                            │    │
│  │         ├── 找到 → 更新 lastUsedTime → submitToWorker │    │
│  │         │                                            │    │
│  │         └── 未找到 ↓                                  │    │
│  └─────────────────────────────────────────────────────┘    │
│                         │                                   │
│                         ↓                                   │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ 规则 A：慢速路径（持锁）                                │    │
│  │                                                      │    │
│  │ bindingMu.Lock()                                     │    │
│  │         │                                            │    │
│  │         ├── 双重检查 Load(sourceKey)                  │    │
│  │         │         │                                  │    │
│  │         │         └── 找到 → 解锁 → submitToWorker    │    │
│  │         │                                            │    │
│  │         ├── selectIdleWorker() → 选择未绑定 Worker     │    │
│  │         │                                            │    │
│  │         ├── Store(sourceKey, binding)                │    │
│  │         │                                            │    │
│  │         └── bindingMu.Unlock() → submitToWorker      │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### SubmitWithPriority（简单轮询）

```go
workerID := int(atomic.AddInt64(&e.stats.TotalSubmitted, 1)-1) % len(e.workers)
```

## 锁层次结构

```
┌─────────────────────────────────────────────────────────────┐
│                       锁层次                                 │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. bindingMu (sync.Mutex)                                  │
│     └── 保护 selectIdleWorker + Store 的原子性                │
│         └── 只在首次绑定时获取                                 │
│                                                             │
│  2. worker.cond.L (sync.Mutex)                              │
│     └── 保护 worker 的等待/通知机制                            │
│         └── submitToWorker 和 worker.run 都会获取             │
│                                                             │
│  3. queue.mu (sync.RWMutex)                                 │
│     └── 保护队列操作                                          │
│         ├── Push: 写锁                                       │
│         ├── Pop: 写锁（含饥饿检查）                            │
│         └── Len: 读锁                                        │
│                                                             │
│  注意：queue.mu 和 cond.L 是独立的，不会形成死锁                 │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## 饥饿防护机制

```
┌─────────────────────────────────────────────────────────────┐
│                    饥饿防护流程                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Pop() 时检查：                                              │
│                                                             │
│  if now - lastCheck > 10ms {                                │
│      if CAS(&starvationCheck, lastCheck, now) {             │
│          promoteStarvedTasks(now)                           │
│      }                                                      │
│  }                                                          │
│                                                             │
│  promoteStarvedTasks:                                       │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ for p := 1; p < 10; p++ {           // 跳过 P0       │    │
│  │     for each item in queues[p] {                     │    │
│  │         if now - item.submitTime > timeout {         │    │
│  │             move item to queues[0]  // 提升到最高      │    │
│  │         }                                            │    │
│  │     }                                                │    │
│  │ }                                                    │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## 绑定清理机制（混合策略）

```
┌─────────────────────────────────────────────────────────────┐
│                    混合清理策略                              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. 次数触发（提交时）                                         │
│     └── 每 1000 次提交触发一次清理                             │
│         if count % 1000 == 0 {                              │
│             go cleanExpiredBindings()                       │
│         }                                                   │
│                                                             │
│  2. 时间触发（后台定时器）                                      │
│     └── 每 30 秒触发一次清理                                   │
│         ticker := time.NewTicker(30 * time.Second)          │
│                                                             │
│  清理逻辑：                                                  │
│  sourceBindings.Range(func(key, value) {                    │
│      if now - binding.lastUsedTime > 30s {                  │
│          sourceBindings.Delete(key)                         │
│      }                                                      │
│  })                                                         │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## 已修复的问题

### P0-02: 严格独占绑定

**问题**：多个 SourceID 可能绑定到同一 Worker

**修复**：使用 `bindingMu` 保护 `selectIdleWorker + Store` 的原子性

```go
e.bindingMu.Lock()
// 双重检查
if bindingValue, ok := e.sourceBindings.Load(sourceKey); ok {
    e.bindingMu.Unlock()
    // 使用已存在的绑定
}
// 选择未绑定的 Worker（此时持有锁，保证独占）
workerID, _ := e.selectIdleWorker()
e.sourceBindings.Store(sourceKey, newBinding)
e.bindingMu.Unlock()
```

### P0-04: LoadOrStore 返回值

**问题**：并发场景下使用错误的 workerID

**修复**：使用返回的 actual binding

```go
actual, loaded := e.sourceBindings.LoadOrStore(sourceKey, newBinding)
binding := actual.(*sourceIDBinding)
workerID = int(binding.workerID)  // 使用实际存储的
```

### P1-01: Len/LenUnsafe 一致性

**问题**：持锁时使用 Len() 导致额外的 RLock

**修复**：持锁时使用 LenUnsafe()

```go
worker.cond.L.Lock()
if worker.queue.LenUnsafe() >= e.config.QueueSize {  // 无锁版本
    // ...
}
```

## 性能特点

| 方面 | 特点 |
|------|------|
| 任务提交 | O(1)，无锁快速路径（已绑定） |
| 任务取出 | O(1)，bitmap 优化 |
| 内存占用 | 每个 Worker 独立队列，预分配容量 |
| 锁竞争 | 每个 Worker 独立锁，无跨 Worker 竞争 |
| CPU 亲和性 | Worker 绑定核心，缓存友好 |

## 使用建议

1. **高并发场景**：使用 `SubmitWithSource` 保证 CPU 亲和性
2. **简单场景**：使用 `Submit` 或 `SubmitWithPriority`
3. **配置调优**：
   - `QueueSize`：根据任务处理时间调整
   - `BindingTimeout`：根据业务特点调整
   - `StarvationTimeout`：根据优先级需求调整
