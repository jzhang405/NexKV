# TaskScheduler pprof 性能分析

**日期**: 2026-03-29
**分支**: `perf/btree-set-benchmark`
**Benchmark**: `BenchmarkSchedLatency_FixedShard_AllCores_Parallel`（含 dispatch 完整路径）
**采样时间**: 11.76s
**CPU**: Intel Core i7-8700 @ 3.20GHz (6C/12T)

---

## pprof 火焰图 Top 函数

```
   flat   flat%    cum%        函数
    0.03s  0.09%   41.21%  BenchmarkSchedLatency_FixedShard_AllCores_Parallel.func1
    1.13s  3.39%   29.45%  EnqueueWithShard
    0.38s  1.14%   28.16%  sync.(*Pool).Get
    0.67s  2.01%   21.42%  ShardTask.Enqueue
    0.86s  2.58%   18.54%  sync.(*Pool).getSlow
    0.05s  0.15%   17.43%  sync.(*Mutex).Lock (partial-inline)
    0.76s  2.28%   16.98%  internal/sync.(*Mutex).Lock
    0.72s  2.16%   14.70%  internal/sync.(*Mutex).lockSlow
    0.01s  0.03%   17.79%  SchedulerCore.runLoop
    0.01s  0.03%   17.43%  SchedulerCore.tryProcessBatch
    0.49s  1.47%   12.69%  SchedulerCore.executeBatch
    3.89s 11.67%   11.67%  runtime.procyieldAsm
    0.23s  0.69%   11.40%  model.GetPooledTask
    1.26s  3.78%   13.02%  sync.(*poolChain).popTail
```

---

## 瓶颈排序

| # | 瓶颈 | cum% | 说明 |
|---|------|------|------|
| 1 | `sync.Pool.Get` / `getSlow` | 28% | **最大瓶颈** — 对象池竞争 |
| 2 | `sync.Mutex.Lock` + `lockSlow` | 32% | 锁竞争（Lock 17% + Slow 15%） |
| 3 | `runLoop` / `tryProcessBatch` / `executeBatch` | 48% | 调度循环开销 |
| 4 | `runtime.procyieldAsm` | 12% | CPU 空转（自旋等待） |

### 1. sync.Pool 竞争（28%）

```go
sync.(*Pool).Get      28.16%  // P-local 命中
sync.(*Pool).getSlow  18.54%  // P-local 未命中，global 加锁获取
```

`BenchmarkShardItem` 从 `sync.Pool` 获取对象，高并发下 P-local pool 竞争激烈， fallback 到 global 需要加锁。

**解决方案**：BenchmarkShardItem 使用独立的 `sync.Pool`，分离热点。

### 2. sync.Mutex 竞争（32%）

```go
sync.(*Mutex).Lock     17.13%  // 锁获取
internal/sync.lockSlow 14.70%  // 锁慢路径（自旋）
```

`ShardTask.Enqueue` 的 mutex + `tryProcessBatch` 的 mutex 在高并发下成为瓶颈。

**解决方案**：Lock-free ring buffer 替代 mutex。

### 3. runLoop 调度开销（48%）

```go
SchedulerCore.runLoop          17.79%
SchedulerCore.tryProcessBatch 17.43%
SchedulerCore.executeBatch   12.69%
```

每轮循环遍历 `cachedTasks`，调用 `tryProcessBatch`，内部多次加锁。

**解决方案**：简化为 channel-based 架构，减少批量处理框架。

### 4. CPU 空转（12%）

```go
runtime.procyieldAsm  11.67%  // 自旋等待
```

锁竞争激烈时 goroutine 在 `lockSlow` 自旋，导致 CPU 空转。

---

## 架构分析

当前架构（`SchedulerCore` on `perf/btree-set-benchmark`）：

```
EnqueueWithShard
  └─ ShardTask.Enqueue()   → sync.Mutex.Lock() + array push
  └─ SchedulerCore.wakeup() → sync.Cond.Signal()

runLoop (goroutine)
  └─ sync.Cond.Wait()       → 阻塞等待
  └─ tryProcessBatch()      → sync.Mutex.Lock() + 批量出队
  └─ executeBatch()         → handler 调用
```

**瓶颈链路**：

```
EnqueueWithShard → Mutex.Lock → Enqueue → Cond.Signal
                  ↓
        runLoop   → Cond.Wait → Mutex.Lock → Peek → Execute → Dequeue
                  ↓
             lockSlow (自旋 + 等待) → CPU 空转
```

---

## 优化方向

| 优先级 | 方案 | 预期收益 | 复杂度 |
|--------|------|----------|--------|
| P0 | 分离 BenchmarkShardItem Pool | 28% | 低 |
| P1 | 改为 channel-based SchedulerCore | 32% | 中 |
| P2 | Lock-free ring buffer | 32%+ | 高 |

### P0: sync.Pool 分离

BenchmarkShardItem 用独立的 `sync.Pool`，与通用对象池隔离，减少跨池竞争。

### P1: channel-based SchedulerCore

参考 `perf/task-scheduler-optimization` 分支的实现，用 `chan any` 替代 `sync.Cond` + `tasks[]`。

### P2: Lock-free ring buffer

用 Dmitry Vyukov 的 MPSC ring buffer 模式，彻底消除 mutex。
