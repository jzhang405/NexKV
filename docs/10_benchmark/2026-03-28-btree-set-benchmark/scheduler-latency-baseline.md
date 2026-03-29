# TaskScheduler 调度延迟 Baseline（PerCoreExecutor 模式）

#
# **日期**: 2026-03-29
# **分支**: `perf/btree-set-benchmark`
# **CPU**: Intel Core i7-8700 @ 3.20GHz (6C/12T)
# **Go**: 1.24
# **Executor**: PerCoreExecutor（`NewPerCoreExecutor()`）
#
# 当前 TaskScheduler.Start(executor) 委托 PerCoreExecutor 提交 runLoop，
# 调度路径: EnqueueWithShard → core.ch → runLoop(在 PerCoreExecutor worker 中) → dispatch
#
# 注意：`perf/task-scheduler-optimization` 分支已将 Start 改为直接 go+LockOSThread，
# 此 baseline 记录的是改造前的数据。
---

## 串行 Benchmark（单 goroutine 入队）

### PureEnqueue（预创建 item，排除分配）

#
# 排除 item 创建开销，仅测量 `EnqueueWithShard` 路径。
#
# | Benchmark | ns/op | B/op | allocs/op |
# |-----------|-------|------|-----------|
# | PureEnqueue_FixedShard_1Core | **138** | 44 | 0 |
# | PureEnqueue_FixedShard_AllCores | **126** | 44 | 0 |
# | PureEnqueue_LoadBalance_AllCores | **136** | 44 | 0 |
#
# 44B 来自 Go channel 内部 `hchan` 结构（运行时级别，不可消除）。
# 三种路由模式延迟接近（126-138ns），路由计算开销可忽略。

### 含 item 创建（sync.Pool 失用）
#
# | Benchmark | ns/op | B/op | allocs/op |
# |-----------|-------|------|-----------|
# | FixedShard_1Core | **255** | 70 | 0 |
# | FixedShard_AllCores | **244** | 70 | 0 |
# | LoadBalance_AllCores | **254** | 68 | 0 |
# | RoundRobin_AllCores | **306** | 67 | 0 |
#
# item 创建（pool Get + 字段设置）额外 ~110-120ns。
# **0 allocs/op**: sync.Pool 完全消除了堆分配。

## 并行 Benchmark（b.RunParallel， GOMAXPROCS=12）

### FixedShard Parallel（×3 runs）

#
# | Run | ns/op | B/op | allocs/op |
# |-----|-------|------|-----------|
# | 1 | 444 | 223 | 1 |
# | 2 | 432 | 208 | 1 |
# | 3 | **458** | 213 | 1 |
# | **avg** | **445** | **215** | **1** |
#
# 1 allocs/op: 并行竞争下 pool 偶发新分配。

### RoundRobin Parallel（×3 runs）

#
# | Run | ns/op | B/op | allocs/op |
# |-----|-------|------|-----------|
# | 1 | 122 | 66 | 0 |
# | 2 | 126 | 69 | 0 |
# | 3 | **121** | 68 | 0 |
# | **avg** | **123** | **68** | **0** |
#
# RoundRobin 并行延迟远低于 FixedShard（123 vs 445），因为 shardID 均匀分散到不同 core，
# 减少了单 channel 竞争。

### LoadBalance Parallel

#
# **未完成** — `selectLeastLoadedCore()` 的 `loadBalanceMu.RLock()` 在高并发下成为瓶颈。
# `processed.Load()` 长时间追不上 `b.N`，benchmark 超时。
# 这是已知的性能问题，直接 go+LockOSThread 方案可以消除。

---

## 开销分解

### 纯调度路径（~126ns）

| 组件 | 预估 | 说明 |
|------|------|------|
| `shardID % coreCount` | ~2ns | 路由计算 |
| `core.ch <- item` | ~60-80ns | channel 内部 mutex |
| handler 定位 + stats | ~10-20ns | 数组索引 + atomic |
| 接口断言 + 鸯彻 | ~20-30ns | ShardItem type assert |
| **合计** | **~130ns** | |

### Item 创建路径（额外 ~120ns）

| 组件 | 预估 | 说明 |
|------|------|------|
| `sync.Pool.Get` | ~20-40ns | P-local fast path |
| `BaseTask` 初始化 | ~20-30ns | done channel + 字段 |
| 字段赋值 | ~10-20ns | shardID, taskOrder |
| `Pool.Put` | ~20-30ns | 归还 |
| **合计** | **~110ns** | |

---

## 籇颈总结

| 场景 | 延迟 | 分配 | 备注 |
|------|------|------|------|
| 纯调度 | 126-138 ns | 0 | channel send 主开销 |
| 含 item 创建 | 244-306 ns | 0 | pool 消除分配 |
| 并行 FixedShard | **445 ns** | 1 | 单 channel 竞争 |
| 并行 RoundRobin | **123 ns** | 0 | 均匀分散，竞争低 |
| 并行 LoadBalance | 未完成 | - | RWMutex 瓶颈 |

### 当前瓶颈

1. **双层调度**: `EnqueueWithShard` → channel → PerCoreExecutor worker → runLoop
2. **LoadBalance RWMutex**: 高并发下 `selectLeastLoadedCore` 的读锁成为瓶颈
3. **Go Channel 内部锁**: `hchan.mutex` 占串行延迟 ~50%

### 优化方向

直接 `go + runtime.LockOSThread + pinToCore` 替代 PerCoreExecutor 委托，
消除中间层，预估延迟降至 ~80-100ns。
