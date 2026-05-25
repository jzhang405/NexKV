# BTree 并行写入调度器设计

> 状态：草稿 → 修订 v2（评审后） | 日期：2026-05-25 | 分支：`fix/btree-par-put-stability`

## 1. 问题定义

### 现状

BTree 写操作基于乐观锁（CAS on PageInfo），高并发下多个 goroutine 争抢同一 Page 的 CAS → 重试风暴 → 大量 involuntary CS → 调度颠簸 → 吞吐暴跌且极不稳定。

```
当前模型：无差别并发
  goroutine-1 ─┐
  goroutine-2 ─┤  CAS 争抢同一 Page
  goroutine-3 ─┤  ─→ 失败 → 重试 → 更多 CS
  goroutine-4 ─┘
```

### 目标

- 同 Page 写入**串行化**，消除 CAS 竞争
- 跨 Page 写入**并发**，保持多核利用
- 事务写入**全局串行**，保证隔离性
- 消除双峰分布，CV 降到 5% 以下

## 2. 核心思想

```
                    ┌─ Page-A Queue → Worker-A (串行)
                    │
KV 写入请求 ─→ 按 Page 分片 ─┼─ Page-B Queue → Worker-B (串行)
                    │
                    ├─ Page-C Queue → Worker-C (串行)
                    │
                    └─ Txn Queue     → Txn Worker  (串行)
```

**原则**：
- **同 Page 串行**：同一 Page 的 Set 操作排入该 Page 的队列，单 goroutine 消费
- **跨 Page 并发**：不同 Page 的队列由不同 goroutine 消费，互不干扰
- **事务全局串行**：事务涉及多个 Page，走独立事务队列，整个事务序列化执行

## 3. Key → Page 映射策略

### 3.1 策略对比

| 策略 | 准确度 | 开销 | 适用场景 |
|------|:------:|:----:|---------|
| A. 完整 BTree 遍历 | 100% | O(log N) per key | 通用 |
| B. 批量排序遍历 | 100% | O(N log N) 一次 | 批量写入 |
| C. **Hash 分桶** | N/A (非映射，纯扇出) | O(1) | 通用 |
| D. Root ChildrenCache 路由 | 粗粒度 | O(log M) | M = 根节点子页数 |

### 3.2 推荐方案：Hash 分桶 + 桶内排序遍历（C + B）

**设计原则**：Phase 1 用 hash 将 key 均匀扇出到 N 个桶（O(1) per key，无锁），桶间并行处理。Phase 2 在桶内排序 key 后一次 BTree 遍历解析 PageID。

**为什么不用前缀分桶**：benchmark key 全部以 `"key-"` 开头，前缀分桶把所有 key 分入同一桶，Phase 1 退化为空操作。hash 分桶保证任意 key 分布下的均匀性。

**Phase 1 — Hash 分桶（策略 C）**：

```go
// KeyToShard 使用 FNV-1a hash 将 key 映射到固定数量的分片
// 保证均匀分布，不依赖 key 的前缀语义
const numShards = 64

func KeyToShard(key []byte) int {
    h := fnv.New32a()
    h.Write(key)
    return int(h.Sum32() % numShards)
}
```

**Phase 2 — 桶内排序 + 批量遍历（策略 B）**：

```go
// resolveShardPageIDs 对单个 shard 内的 key 排序后批量解析 PageID
// 利用 key 有序性：相邻 key 大概率同 page，减少遍历次数
func (tree *BTree) resolveShardPageIDs(ctx context.Context, keys []keyWithIndex) map[PageID][]int {
    // 1. 桶内排序（shard 内 key 数量 ≈ N/numShards，可控）
    sort.Slice(keys, func(i, j int) bool {
        return bytes.Compare(keys[i].key, keys[j].key) < 0
    })

    // 2. 顺序遍历解析 PageID
    // 相邻 key 大概率同 page，记录上次的 searchPath 加速
    result := make(map[PageID][]int)
    var lastPage PageID
    for i, k := range keys {
        var pid PageID
        if i > 0 && tree.inSamePage(lastPage, k.key) {
            pid = lastPage // 快速路径：复用上次结果
        } else {
            pid = tree.ResolvePageID(ctx, k.key)
            lastPage = pid
        }
        result[pid] = append(result[pid], k.idx)
    }
    return result
}
```

`inSamePage` 是一个纯读优化：从 page 的 key range 元数据快速判断 key 是否在范围内。**错误判定的后果可控**：
- False positive（认为在范围内但实际已分裂）→ 下游 `tree.Set` 通过 `searchPath` 正确路由到新 Page，正确性不受影响，仅性能略降
- False negative（认为不在范围内但实际在）→ 重新 `ResolvePageID`，仅性能略降

### 3.3 分片数与桶数

```
numShards = GOMAXPROCS * 4   // 固定 64 桶（M2 8 核 → 32）
```

- 桶数固定为 CPU 核数 × 4，不随 key 数量变化
- 每个桶内 key 数量 ≈ N/numShards，桶内排序开销可控
- 桶间无锁并行，桶数 > 核数确保负载均衡

## 4. 调度器架构

### 4.1 数据结构

```go
// PageDispatcher 按 Page 分片调度写入
type PageDispatcher struct {
    tree     *btree.BTree
    pool     *WorkerPool
}

// WorkerPool 常驻 worker goroutine 池，消费 per-page 任务
type WorkerPool struct {
    taskCh    chan *pageBatch   // 所有 worker 共享的任务通道
    wg        sync.WaitGroup   // 追踪所有提交的任务
}

// pageBatch 单个 Page 的批量写入任务
type pageBatch struct {
    pageID  PageID
    tasks   []writeTask
    results []WriteResult      // 写入结果（预分配，按 task 原始索引）
}

// writeTask 单个写入任务（不含 channel，纯数据）
type writeTask struct {
    idx   int      // 在原始 keys 数组中的位置
    key   []byte
    value []byte
}

// WriteResult 单个写入的结果
type WriteResult struct {
    Index int
    Err   error
}
```

**关键变更（v1→v2）**：
- 去掉 `WriteTask.ErrCh`、`PageQueue.done` — channel 开销太大，改用预分配 `[]WriteResult` + 索引映射
- 去掉 `txnCh` — 事务在 Dispatch 内联执行，不通过通道
- `WorkerPool` 使用常驻 goroutine + 共享 `taskCh`，而非 per-task `go func()`

### 4.2 调度流程

```
┌─────────────────────────────────────────────────────┐
│                 PageDispatcher.Dispatch()            │
│                                                      │
│  1. Hash 分桶: O(N)，key 扇出到 numShards 个桶       │
│         │                                             │
│         ▼                                             │
│  2. 桶间并行: 每个桶开 goroutine                      │
│     ├─ 桶0: 排序 + resolveShardPageIDs               │
│     ├─ 桶1: 排序 + resolveShardPageIDs               │
│     └─ 桶N: 排序 + resolveShardPageIDs               │
│         │                                             │
│         ▼                                             │
│  3. 合并: 跨桶 merge → map[PageID][]writeTask        │
│         │                                             │
│         ▼                                             │
│  4. 提交: 每个 PageID → WorkerPool.Submit(pageBatch) │
│         │                                             │
│         ▼                                             │
│  5. 等待: wp.wg.Wait()                                │
│         │                                             │
│         ▼                                             │
│  6. 汇总 WriteResult[] 返回                           │
└─────────────────────────────────────────────────────┘
```

### 4.3 Worker Pool

```go
// NewWorkerPool 创建 n 个常驻 worker goroutine
func NewWorkerPool(n int) *WorkerPool {
    wp := &WorkerPool{
        taskCh: make(chan *pageBatch, n*4),
    }
    for i := 0; i < n; i++ {
        go wp.runWorker()
    }
    return wp
}

// runWorker 常驻 goroutine，消费 taskCh 上的 pageBatch
func (wp *WorkerPool) runWorker() {
    for batch := range wp.taskCh {
        wp.executeBatch(batch)
    }
}

// executeBatch 串行执行同一 Page 的所有写入
// 包含 panic recovery，避免一个 write 的 panic 崩溃整个 worker
func (wp *WorkerPool) executeBatch(batch *pageBatch) {
    defer wp.wg.Done()
    for i := range batch.tasks {
        t := &batch.tasks[i]
        err := batch.execute(t) // 调用 tree.Set
        batch.results[i] = WriteResult{Index: t.idx, Err: err}
    }
}

// Submit 提交一个 pageBatch，不阻塞调用者
func (wp *WorkerPool) Submit(batch *pageBatch) {
    wp.wg.Add(1)
    wp.taskCh <- batch
}

// Wait 等待所有已提交任务完成
func (wp *WorkerPool) Wait() {
    wp.wg.Wait()
}

// Shutdown 优雅关闭
func (wp *WorkerPool) Shutdown() {
    close(wp.taskCh)
}
```

**关键设计决策**：

1. **常驻 goroutine** 而非 per-task `go func()`：避免高频 goroutine 创建/销毁开销
2. **wg.Add(1) 在 Submit 中，在 taskCh 发送之前**：消除 `dispatchNormal` 循环结束后 `Wait()` 的竞态（所有 pageBatch 提交完毕时 wg 计数已准确）
3. **无 semaphore**：常驻 worker 数量固定 = CPU 核数，自然限流
4. **panic recovery**：`executeBatch` 内 recover，panic 转为 error 写入 results

### 4.4 关键并发保证

```
Invariant 1: 同一 PageID 的所有写入在同一个 goroutine 中顺序执行
    → 保证：pageBatch 整体提交到一个 worker，worker 内顺序 for 循环

Invariant 2: 不同 PageID 的写入分布到不同 worker 并发执行
    → 保证：不同 pageBatch 进入 taskCh，由 N 个 worker 并行消费

Invariant 3: 事务写入独占执行，不与任何普通写入并发
    → 保证：Dispatch 内先 Wait() 排空所有 pageBatch，再顺序执行事务

Invariant 4: 跨 Dispatch 调用的同 Page 写入不保证在同一 goroutine
    → 说明：每次 Dispatch 独立提交 pageBatch，
      同一 Page 在不同 batch 中可能由不同 worker 处理。
      但同一 batch 内不变式 1 保证了串行。
```

## 5. 事务兼容

### 5.1 隔离模型

```
正常写入：Page 级并行
    Page-A ──→ Worker-1
    Page-B ──→ Worker-2  } 并发
    Page-C ──→ Worker-3

事务写入：全局串行
    Txn{T1, T2, T3} ──→ Dispatch goroutine（内联执行）
                        ↑
                    所有 Page Worker 必须在此之前排空（Wait）
```

### 5.2 调度策略

```go
func (pd *PageDispatcher) Dispatch(ctx context.Context, ops []WriteOp) ([]WriteResult, error) {
    // 分离事务和非事务操作
    txns, normal := partition(ops)

    // 1. 先处理所有非事务写入（Page 级并发）
    results := pd.dispatchNormal(ctx, normal)

    // 2. 等待所有 Page Worker 完成
    pd.pool.Wait()

    // 3. 顺序执行事务（在调用者 goroutine 中内联）
    for _, txn := range txns {
        if err := txn.Execute(ctx); err != nil {
            return results, err
        }
    }

    return results, nil
}
```

**事务执行期间的行为**：
- `Dispatch` 在事务执行期间占用调用者 goroutine
- 事务执行期间不会有新的普通写入被提交（调用者是串行的）
- 如果调用者需要并发调用 `Dispatch`，外部需要自己的序列化层

### 5.3 事务内部优化（未来）

事务内部如果涉及多个 Page，且这些 Page 之间没有冲突（不同 key range），理论上可以并行——但需要事务冲突检测。**V1 不做**，保持事务全局串行确保正确性。

## 6. API 设计

### 6.1 调度器接口

```go
// BatchWriter 批量写入调度器
type BatchWriter struct {
    dispatcher *PageDispatcher
    tree       *btree.BTree
}

// WriteBatch 批量写入（非事务）
// 内部自动按 Page 分组、并发调度
// 返回聚合 error：所有 key 写入成功返回 nil，
// 任一失败返回包含所有错误的 *BatchError
func (bw *BatchWriter) WriteBatch(ctx context.Context, keys, values [][]byte) error

// WriteTxn 事务写入
func (bw *BatchWriter) WriteTxn(ctx context.Context, txn *Transaction) error

// BatchError 聚合多个写入错误
type BatchError struct {
    Errors []WriteResult // 只包含失败的
}

func (be *BatchError) Error() string {
    return fmt.Sprintf("%d write(s) failed", len(be.Errors))
}
```

**API 设计说明**：
- 返回 `error` 而非 `[]error`，符合 Go 惯例
- 正常路径（无错误）返回 `nil`，零分配
- 需要逐 key 错误时，类型断言 `*BatchError` 获取详情

### 6.2 与现有 BTree API 的关系

```
现有 API (保持兼容):
  tree.Set(ctx, key, value)        // 单 key 写入，直接调现有 CAS 路径
  tree.Get(ctx, key)               // 读取不受影响（lock-free 路径保持）

新增 API:
  batchWriter.WriteBatch(...)       // 批量写入优化路径
  batchWriter.WriteTxn(...)         // 事务写入路径
```

**不破坏现有 API**。`Set` 单次调用走现有 CAS 路径不变。`WriteBatch` 是新增的高性能批量路径。

**读路径不受影响**：`Get` 走 `searchPath` lock-free 路径，不经过调度器。读操作可以在批量写入期间并发执行，读到的是 COW 页面的一致快照。

## 7. 数据流

### 7.1 批量写入时序

```
Client
  │
  │ WriteBatch(ctx, keys=[k1..kN], values=[v1..vN])
  ▼
BatchWriter
  │
  │ 1. Hash 分桶: O(N)，key → shard index
  ▼
  │ 2. 桶间并行 (numShards 个 goroutine):
  │    ├─ 桶0: sort → resolveShardPageIDs → map[PageID][]writeTask
  │    ├─ 桶1: sort → resolveShardPageIDs → map[PageID][]writeTask
  │    └─ ...
  ▼
  │ 3. 跨桶 merge: map[PageID][]writeTask (去重合并)
  ▼
PageDispatcher
  │
  │ 4. Submit: 每个 PageID → pageBatch → taskCh
  ├─→ Worker-1: batch{pA: [k1,k5,k9]}  → tree.Set(k1), Set(k5), Set(k9)  // 串行
  ├─→ Worker-2: batch{pB: [k2,k6]}     → tree.Set(k2), Set(k6)           // 串行
  └─→ Worker-3: batch{pC: [k3,k7,k8]}  → tree.Set(k3), Set(k7), Set(k8)  // 串行
  │
  │ 5. pool.Wait() → 所有 pageBatch 完成
  ▼
  │ 6. 收集 results → 返回 error (nil 或 *BatchError)
  ▼
Client
```

### 7.2 关键保证

- **同 Page 操作顺序执行**：不会有两个 goroutine 同时 CAS 同一 Page
- **零 CAS 重试**（理想情况）：每个 Page 只有一个写者，CAS 一次成功
- **跨 Page 真并发**：不同 Page 的写入完全并行，没有共享竞争
- **同 Page 跨 batch 可并发**（两个 `WriteBatch` 调用中同一 Page 可能由不同 worker 处理，此时退化到原有 CAS 竞争——但单次 batch 内 100% 无竞争）

## 8. 关键设计决策

### 8.1 Hash 分桶

| 选择 | 优点 | 缺点 |
|------|------|------|
| 前缀分桶（v1） | 实现简单 | **不可用**：顺序 key 同前缀，退化为单桶 |
| **Hash 分桶（v2）** | 均匀分布，通用性强 | 打散同 page 的 key 到不同桶 |
| 去掉 Phase 1，直接全局排序 | 分组最精确 | O(N log N) 单线程，延迟高 |

**选择：Hash 分桶**。同 page 的 key 被打散到不同桶的代价可接受——跨桶 merge 阶段会重新聚合。桶间并行排序 + 遍历的收益远超跨桶 merge 的开销。

### 8.2 Worker 数量

```go
workers = min(runtime.GOMAXPROCS(0), numShards/2)
```

- M2 8 核 → 8 workers
- Worker 数不超过 Page 分组数的一半（Page 太少时不需要全部 worker）
- 常驻 goroutine 模型 — 无需动态扩缩

### 8.3 事务隔离级别

V1 实现 **串行快照隔离（Serializable Snapshot Isolation）**：
- 事务开始时所有普通写入已排空（Wait 保证）
- 事务期间无新写入（Dispatch 同步执行）
- 事务提交后下一个 Dispatch 的写入开始

### 8.4 panic recovery

Worker goroutine 内必须 recover：

```go
func (wp *WorkerPool) executeBatch(batch *pageBatch) {
    defer wp.wg.Done()
    defer func() {
        if r := recover(); r != nil {
            // 将 panic 转为所有未完成 task 的 error
            for i := range batch.tasks {
                if batch.results[i].Err == nil {
                    batch.results[i] = WriteResult{
                        Index: batch.tasks[i].idx,
                        Err:   fmt.Errorf("panic: %v", r),
                    }
                }
            }
        }
    }()
    for i := range batch.tasks {
        t := &batch.tasks[i]
        batch.results[i] = WriteResult{
            Index: t.idx,
            Err:   batch.tree.Set(ctx, t.key, t.value),
        }
    }
}
```

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|------|:---:|:---:|------|
| Hash 分桶负载不均 | 低 | 部分桶 key 多，并行度降低 | FNV-1a 分布均匀；桶数 >> 核数 |
| COW 分裂导致 PageID 变化 | 中 | 同 page 的两部分 key 被分到不同 pageBatch | worker 内 tree.Set 的 searchPath 实时路由，正确性不受影响 |
| 内部节点 CAS 竞争 | 中 | 多个 page 同时 split 争抢 parent CAS | parent split 频率 << leaf write，影响可控；V2 暂接受 |
| Page 过少时并发度不足 | 高 | 同一 page 的所有 key 一个 worker 处理，退化为单线程 | 无可避免——page 少意味着 key 范围小，串行是正确选择 |
| 桶间 merge 开销 | 低 | map 合并有内存分配 | key 总数确定时预分配 merge map |
| 事务执行时间长阻塞后续写入 | 中 | 后续 WriteBatch 被阻塞 | V1 接受；V2 考虑仅 drain 事务涉及的 page |
| Worker panic 导致 goroutine 退出 | 低 | 丢失 worker | panic recovery + 自动重启 worker |

### 9.1 COW 分裂处理（详细）

COW 写操作可能触发 Page 分裂：

```
场景：page A 分配了 10 个 key，worker 串行执行
  1. worker 执行 key1, key2, key3 → 成功
  2. key4 写入最后一个 slot → 触发 split
     → page A 分裂为 A-left + A-right
     → tree.Set 内部 searchPath 重新路由到正确的新 page
  3. key5 在 A-left，key6 在 A-right
     → tree.Set 各自通过 searchPath 正确定位

结论：同 worker 内串行执行，split 由 tree.Set 内部处理
      worker 无需感知分裂，正确性由 BTree 的 CAS retry 保障
```

**与原有模型的区别**：
- 原模型：N 个 goroutine 同时 CAS 同一 page，split 时 parent CAS 竞争 × N
- 调度器模型：1 个 goroutine 操作同一 page，split 时 parent CAS 仅 1 个写者
- Parent CAS 竞争仅发生在**不同 page 同时 split** 时（都 hit 同一 parent），概率远低于原模型

### 9.2 内部节点 CAS 竞争（未来关注）

当多个 leaf page 同时 split 且共享同一 parent 时，parent CAS 仍有竞争。但由于：
- Leaf write 频率 >> split 频率（每页 100+ entries 才 split 一次）
- 调度器保证了 leaf 级无竞争，split 触发率更低
- Parent split 只在 leaf split 时触发，频率更低

**V1 接受此风险**，通过 benchmark 验证实际影响。若成为瓶颈，V2 可对内部节点也引入 page 级调度。

## 10. 实现计划

### Phase 1：核心调度器（1-2 天）

- [ ] `WorkerPool`：常驻 goroutine + taskCh + WaitGroup + panic recovery
- [ ] `KeyToShard` hash 分桶
- [ ] `resolveShardPageIDs`：桶内排序 + 批量遍历
- [ ] 跨桶 merge → `map[PageID][]writeTask`

### Phase 2：集成 BTree（1 天）

- [ ] `BatchWriter` API 实现
- [ ] `Dispatch` 主流程：hash → resolve → submit → wait → collect
- [ ] COW 分裂路径验证

### Phase 3：事务支持（1 天）

- [ ] `partition` 分离事务/非事务
- [ ] `Wait + 内联执行` 事务隔离
- [ ] 隔离性测试

### Phase 4：验证（1 天）

- [ ] 8T/16T/32T 稳定性回归测试（CV < 5% 目标）
- [ ] CPU profile 验证 involuntary CS 消除
- [ ] 性能对比：调度器开销 vs 原有直接并发
- [ ] 压测 100 轮确认无双峰

## 11. 预期效果

| 指标 | 当前 | 目标 |
|------|------|:---:|
| 8T CV | 16.9% | < 5% |
| 16T CV | 40.8% | < 5% |
| 32T CV | 100.9% | < 10% |
| 16T 极差比 | 2.93x | < 1.2x |
| 慢组 QPS 地板 | ~270K | 接近均值 |
| Involuntary CS | 37K (慢轮) | < 5K |

## 附录 A：v1 → v2 变更摘要

| 变更 | v1 问题 | v2 修复 |
|------|---------|---------|
| Key 分桶 | 前缀分桶 — 顺序 key 退化为单桶 | Hash 分桶 (FNV-1a) — 均匀分布 |
| InSamePage | 乐观快照可能 false positive | 保留但降级为纯读优化，错误后果可控 |
| WorkerPool | `semaphore <-` 在 `go func()` 之前阻塞提交；per-task goroutine | 常驻 goroutine + taskCh；semaphore 移除 |
| 错误传播 | `ErrCh` channel 从未被读取 | 预分配 `[]WriteResult` + 索引映射 |
| 事务执行 | `txnCh` 定义但未使用 | `Dispatch` 内联执行，删除通道 |
| WaitGroup | `dispatchNormal` 后 Wait 存在竞态 | `wg.Add(1)` 在 Submit 中、taskCh 发送之前 |
| API 返回值 | `[]error` 非 Go 惯例 | `error` + `*BatchError` 类型断言 |
| goroutine 生命周期 | 无 context、shutdown、panic recovery | `Shutdown()` + panic recovery + 常驻 goroutine |
| COW 分裂 | 描述不完整 | 详细分析：worker 内 searchPath 实时路由，正确性有保障 |
