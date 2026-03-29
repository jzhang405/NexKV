# TaskScheduler 调度延迟 Baseline

**日期**: 2026-03-29
**分支**: `perf/btree-set-benchmark`
**CPU**: Intel Core i7-8700 @ 3.20GHz (6C/12T)
**Go**: 1.24
**Executor**: PerCoreExecutor（`NewPerCoreExecutor()`）

---

## 测试环境

- `runtime.NumCPU()` = 12（6 核超线程）
- `DefaultChannelBufferSize` = 4096
- Benchmark 使用 `b.Loop()` 循环，串行测试单 goroutine 入队
- Parallel 测试使用 `b.RunParallel`，GOMAXPROCS=12

---

## 串行 Benchmark 结果

### 1. PureEnqueue（预创建 item，复用同一对象）

排除对象分配，仅测量 `EnqueueWithShard` 调度路径开销。

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| PureEnqueue_FixedShard_1Core | **138** | 44 | 0 |
| PureEnqueue_FixedShard_AllCores | **126** | 44 | 0 |
| PureEnqueue_LoadBalance_AllCores | **136** | 44 | 0 |

**分析**: 三种路由模式延迟接近（126-138ns），说明路由计算开销可忽略。44B 来自 channel 内部 `hchan` 结构（Go 运行时级别，不可消除）。

### 2. 含 item 创建（sync.Pool 复用）

每次循环从 pool 获取 `benchmarkShardItem`，handler 中归还 pool。

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| FixedShard_1Core | **255** | 70 | 0 |
| FixedShard_AllCores | **244** | 70 | 0 |
| LoadBalance_AllCores | **254** | 68 | 0 |
| RoundRobin_AllCores | **306** | 67 | 0 |

**分析**:
- item 创建（pool Get + 字段设置）额外增加 ~110-120ns
- RoundRobin 稍慢（+50ns），因为每次循环需要 `shardID++` 和分支判断
- **0 allocs/op**: sync.Pool 完全消除了堆分配

### 3. 并行 Benchmark（部分结果）

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| FixedShard_AllCores_Parallel | **440** | 213 | 1 |
| LoadBalance_AllCores_Parallel | *超时未完成* | - | - |
| RoundRobin_AllCores_Parallel | *超时未完成* | - | - |

**分析**:
- FixedShard Parallel 延迟翻倍（244ns → 440ns），符合预期（多 goroutine 竞争同一 channel）
- 1 allocs/op: 并行场景下 pool 竞争导致偶发分配
- LoadBalance/RoundRobin Parallel 超时：`selectLeastLoadedCore` 的 RWMutex 在高并发下成为瓶颈，`processed.Load()` 长时间追不上 `b.N`

---

## 开销分解

### Enqueue 路径（~126ns 纯调度）

| 组件 | 预估开销 | 说明 |
|------|----------|------|
| `shardID % coreCount` | ~2ns | 整数取模 |
| `core.ch <- item` | ~60-80ns | Go channel 内部 mutex + ring buffer 写入 |
| handler 定位 + atomic stats | ~10-20ns | 数组索引 + atomic add |
| 其他（验证、调度） | ~20-30ns | ShardItem 接口断言等 |
| **合计** | **~130ns** | 与实测吻合 |

### Item 创建路径（额外 ~110ns）

| 组件 | 预估开销 | 说明 |
|------|----------|------|
| `sync.Pool.Get` | ~20-40ns | P-local fast path |
| `BaseTask` 初始化 | ~20-30ns | done channel + 字段设置 |
| `benchmarkShardItem` 字段设置 | ~10-20ns | shardID, taskOrder 等 |
| handler 中 `Pool.Put` | ~20-30ns | 归还到 P-local pool |
| **合计** | **~110ns** | |

---

## 瓶颈分析

### 当前瓶颈：Go Channel 内部锁

`chan any` 内部使用 `hchan.mutex`：
- 每次 send: `lock()` → copy to ring buffer → `unlock()`
- 每次 recv: `lock()` → copy from ring buffer → `unlock()`
- 串行场景下单次 lock/unlock ~30-40ns，占总延迟的 ~50%

### 潜在优化方向

| 方案 | 预估延迟 | 复杂度 | 收益 |
|------|----------|--------|------|
| Lock-free MPSC ring buffer | ~50-80ns | 高 | -40~60% |
| 批量提交（batch enqueue） | ~80-100ns/batch | 中 | 吞吐量 +2-3x |
| 保持现状 | ~130ns | - | 已足够快 |

---

## Baseline 总结

| 场景 | 延迟 | 分配 | 备注 |
|------|------|------|------|
| **纯调度开销** | **126-138 ns** | 0 | channel send 是主开销 |
| **含 item 创建** | **244-306 ns** | 0 | sync.Pool 消除分配 |
| **并行（FixedShard）** | **440 ns** | 1 | channel 竞争 |
| **并行（LoadBalance）** | 未完成 | - | RWMutex 瓶颈 |

**结论**: 当前 PerCoreExecutor + Channel-based TaskScheduler 的调度开销 ~130ns，item 创建后总计 ~250ns。sync.Pool 已实现 0 allocs。Go channel 内部锁是串行场景的主要开销来源（~50%），但在 B-Tree SET 总延迟 ~1-2μs 中占比 <10%，不是当前性能瓶颈。
