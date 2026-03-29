# Phase 5: Zero-Copy 分裂优化后 pprof 分析

日期: 2026-03-28
分支: `perf/btree-set-benchmark`
提交: `c71b8ff` (零拷贝页面分裂优化)
测试命令: `./bin/btree_perf_pprof` (50000 ops, 单线程)

## 1. 性能概览

| 指标 | Phase 4 (优化前) | Phase 5 (优化后) | 变化 |
|------|-----------------|-----------------|------|
| 吞吐量 | 24,128 ops/s | 47,358 ops/s | **+96.3%** |
| 延迟 | 41.44 μs/op | 21.12 μs/op | **-49.0%** |
| GC 开销 | ~37% CPU | ~5.8% CPU | **-31.2pp** |
| mallocgc | 19.0% CPU | 3.6% CPU | **-15.4pp** |

## 2. CPU Profile Top 分析

```
Duration: 1.06s, Total samples = 1.38s (130.71%)
```

### 2.1 Flat Top 15

| flat% | 函数 | 说明 |
|-------|------|------|
| 36.96% | `runtime.futex` | 线程调度阻塞 |
| 3.62% | `PageAccessor.InitPage` | 页面初始化（分裂时分配新页面） |
| 2.17% | `runtime.stealWork` | GMP 调度偷取 |
| 1.45% | `fmt.(*pp).doPrintf` | 格式化输出 |
| 1.45% | `OffHeapAdapter.linearSearchLeaf` | 叶子页面线性搜索 |
| 1.45% | `PageAccessor.SearchKey` | 二分搜索 |
| 1.45% | `internal/sync.Mutex.Lock` | 锁竞争 |
| 1.45% | `runtime.chanrecv` | channel 接收 |
| 1.45% | `runtime.lock2` | 内部锁 |
| 1.45% | `runtime.mapaccess2_fast64` | map 查找 |

### 2.2 Cumulative Top 15

| cum% | 函数 | 说明 |
|------|------|------|
| 46.38% | `runtime.mcall` | goroutine 切换入口 |
| 40.58% | `runtime.schedule` | 调度器 |
| 39.13% | `runtime.park_m` | goroutine 挂起 |
| 37.68% | `BTree.Set` | Set 入口 |
| 36.96% | `runtime.futex` | 线程阻塞 |
| 35.51% | `SetWithRetryAndQueue` | 重试+队列调度 |
| 26.81% | `setWithLeafLock` | 叶子锁写入主路径 |
| 22.46% | `runtime.findRunnable` | 寻找可运行 G |
| 15.94% | `runtime.notewakeup` | 唤醒 M |
| 14.49% | `runtime.startm` | 启动 M |
| 10.14% | `findLeafPageRef` | 搜索叶子路径 |
| 10.14% | `searchPathWithRefs` | 搜索路径 |
| 9.42% | `handleSplitOffHeapSync` | 页面分裂 |
| 8.70% | `SetWithTask` | Task 模式 Set |
| 6.52% | `UpdateIndexEntry` | 索引更新（COW） |

## 3. 瓶颈分析

### 瓶颈 1: runtime.futex — 36.96% CPU（P0）

**现象**: futex 占用最高，是 GMP 调度器线程阻塞/唤醒的系统调用。

**调用链分析**:
```
runtime.futex (36.96%)
├── runtime.futexsleep (21.74%) → runtime.notesleep → runtime.mPark → runtime.stopm
│   M 线程无事可做，进入休眠
├── runtime.futexwakeup (15.22%) → runtime.notewakeup → runtime.startm
│   唤醒休眠的 M 线程
└── 其他 futex 操作
```

**根因**: 单线程场景下，TaskScheduler 的生产者-消费者模型引入了不必要的线程调度开销：
- `Set` → `SetWithRetryAndQueue` → `EnqueueWithShard`（入队）
- Scheduler worker goroutine → `runLoop` → `executeTask` → `SetWithTask`
- 每次操作涉及 goroutine 唤醒/休眠的 futex 系统调用

**影响**: ~37% CPU 时间用于线程调度，而非实际数据处理。

**建议**:
- **P0: 绕过 TaskScheduler 的直通模式**。单线程场景下，`Set` 应直接调用 `setWithLeafLock`，跳过队列+调度器。
- 或者：使用 `runtime.LockOSThread()` 固定 worker 到 OS 线程，减少 futex 开销。

### 瓶颈 2: TaskScheduler 调度开销 — 8.70% CPU（P1）

**调用链**:
```
SetWithRetryAndQueue (35.51%)
├── setWithLeafLock (26.81%)  ← 实际工作
├── EnqueueWithShard → SchedulerCore.wakeup (2.90%)
└── 调度器内部开销
```

**分析**: `SetWithRetryAndQueue` 的 cumulative (35.51%) 远高于 `setWithLeafLock` (26.81%)，差值 ~9% 是调度框架本身的开销。

### 瓶颈 3: 搜索路径 — 10.14% CPU（P2）

**调用链**:
```
findLeafPageRef / searchPathWithRefs (10.14%)
├── SearchChild (3.47%)  ← 索引节点二分搜索
├── linearSearchLeaf (4.35%)  ← 叶子节点线性搜索
└── PageRefCache.GetOrCreate (4.35%)  ← 缓存查找
```

**分析**:
- `linearSearchLeaf` 使用线性遍历而非二分搜索，O(n) vs O(log n)
- `PageRefCache.GetOrCreate` 涉及 `mapaccess2_fast64` (1.45%)，hash 查找开销
- 多层树每层都需要搜索，路径越长开销越大

### 瓶颈 4: fmt.Sprintf + errors.Wrapf — 5.80% + 4.35%（P2）

**分析**:
- `fmt.Sprintf` (5.80%) 用于 key 生成和错误格式化
- `errors.Wrapf` (4.35%) 仍在热路径上产生 `fmt.Sprintf` 开销

**建议**: 热路径上移除所有 `Wrapf`，直接返回 sentinel error。

### 瓶颈 5: InitPage — 3.62% CPU（P3）

**分析**: 分裂时分配新页面需要 `InitPage` 初始化 header。每次分裂 2-3 次 InitPage 调用。

**建议**: 可通过批量初始化（`memset` 整页 + 设置 header）优化。

### 瓶颈 6: GC — 5.07% CPU（已大幅改善）

**对比**:

| GC 函数 | Phase 4 | Phase 5 | 变化 |
|---------|---------|---------|------|
| `gcBgMarkWorker` | 18.69% | 5.80% | **-12.9pp** |
| `gcDrain` | 17.70% | 5.07% | **-12.6pp** |
| `mallocgc` | 19.02% | 3.62% | **-15.4pp** |
| `mallocgcTiny` | 5.25% | 0% | **-5.25pp** |

GC 已从主要瓶颈降为次要开销。零拷贝分裂优化效果显著。

## 4. CPU 分布饼图

```
┌─────────────────────────────────────────────────┐
│              CPU 分布 (Phase 5)                  │
├─────────────────────────────────────────────────┤
│                                                  │
│  ██████████████████████████  37%  futex 调度     │
│  █████████████████           16%  实际数据处理   │
│  █████████                   10%  搜索路径       │
│  ██████                       6%  分裂处理       │
│  █████                        5%  GC + malloc    │
│  ████                         4%  fmt/errors     │
│  █████                        5%  索引更新(COW)  │
│  ██████                       6%  TaskScheduler  │
│  █████                        5%  其他           │
│  ██                           2%  InitPage       │
│                                                  │
└─────────────────────────────────────────────────┘
```

## 5. 优化路线图（Phase 6 建议）

### P0: 绕过 TaskScheduler 直通模式（预期 +30-40% 吞吐）

**问题**: 37% CPU 用于 futex 调度，单线程下完全没有必要。

**方案**: 在 `BTree.Set` 中直接调用 `setWithLeafLock`，跳过队列：
```go
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    // 单线程/低并发模式：直接执行
    return b.setWithLeafLock(ctx, key, value)
}
```

**预期**: 消除 futex 开销，吞吐量提升至 ~65-70K ops/s。

### P1: linearSearchLeaf → 二分搜索（预期 +5-10%）

**当前**: `linearSearchLeaf` O(n) 遍历
**优化**: 使用 `SearchKey` 二分搜索 O(log n)

### P2: 热路径 Wrapf 清理（预期 +3-5%）

移除 `handleSplitOffHeapSync`、`UpdateIndexEntry` 等热路径上的 `Wrapf` 调用。

### P3: InitPage 批量优化（预期 +1-2%）

使用 `memset` 清零整页 + 设置 header，替代逐字段初始化。

## 6. 与 Phase 4 对比总结

```
Phase 4 瓶颈排序:
1. GC (mallocgc + gcDrain)          ~37%  ████████████████████████████████████
2. SplitOffHeapLeafPage             ~26%  ██████████████████████████
3. futex 调度                       ~22%  ██████████████████████
4. 搜索路径                         ~10%  ██████████

Phase 5 瓶颈排序:
1. futex 调度                       ~37%  ████████████████████████████████████
2. 搜索路径                         ~10%  ██████████
3. TaskScheduler                    ~9%   █████████
4. fmt/errors                       ~10%  ██████████
5. 分裂处理                         ~9%   █████████
6. GC                               ~5%   █████
```

**结论**: 零拷贝分裂成功消除了 GC 瓶颈和分裂函数开销。当前主要瓶颈已转移至 **TaskScheduler 调度框架的 futex 开销**（37% CPU），这是架构层面的开销，非算法层面。下一步优化方向是绕过调度器直通模式。
