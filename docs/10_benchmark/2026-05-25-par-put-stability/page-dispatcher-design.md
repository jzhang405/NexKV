# BTree 并行写入调度器设计

> 状态：草稿 | 日期：2026-05-25 | 分支：`fix/btree-par-put-stability`

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
| C. Key 前缀分桶 | 近似 | O(1) | 顺序 key |
| D. Root ChildrenCache 路由 | 粗粒度 | O(log M) | M = 根节点子页数 |

### 3.2 推荐方案：B + C 混合

**Phase 1 — 快速分桶（策略 C）**：

利用 benchmark 场景下 key 的顺序性，使用 key 前缀直接分桶：

```go
// 取 key 的前 2 字节作为 PageHint
// 对于顺序 key "key-0000000001"，前 2 字节 = "ke"
// 不同 key 范围的 key 落入不同桶
type PageHint uint16

func KeyToPageHint(key []byte) PageHint {
    if len(key) >= 2 {
        return PageHint(binary.BigEndian.Uint16(key[:2]))
    }
    // fallback
    h := fnv.New32a()
    h.Write(key)
    return PageHint(h.Sum32() & 0xFFFF)
}
```

**Phase 2 — 批量精确分组（策略 B）**：

桶内 key 排序后，一次批量 BTree 遍历确定精确的 PageID：

```go
// BatchResolvePageIDs 对已排序的 keys 进行一次 BTree 遍历，
// 将每个 key 映射到其所在的 leaf PageID
func (tree *BTree) BatchResolvePageIDs(ctx context.Context, keys [][]byte) map[PageID][]int {
    result := make(map[PageID][]int)
    var currentPage PageID
    for i, key := range keys {
        // 利用 key 有序性：相邻 key 大概率同 page
        if i > 0 && bytes.Compare(key, keys[i-1]) > 0 {
            // 检查是否仍在同一 page 的 key range
            if !tree.InSamePage(currentPage, key) {
                currentPage = tree.ResolvePageID(ctx, key)
            }
        } else {
            currentPage = tree.ResolvePageID(ctx, key)
        }
        result[currentPage] = append(result[currentPage], i)
    }
    return result
}
```

### 3.3 分桶粒度选择

```
桶数 = min(max(4, len(keys)/100), GOMAXPROCS*4)
```

- 桶数太少 → 桶内串行度不够，浪费 CPU
- 桶数太多 → 桶管理开销超过收益
- 默认公式：每 100 个 key 一个桶，但不超过 CPU 核数 × 4

## 4. 调度器架构

### 4.1 数据结构

```go
// PageDispatcher 按 Page 分片调度写入
type PageDispatcher struct {
    mu       sync.Mutex
    queues   map[PageID]*PageQueue  // page → 专属队列
    workerCh chan func()            // 通用 worker 池
    txnCh    chan *Transaction      // 事务专用通道
}

// PageQueue 单 Page 的串行写入队列
type PageQueue struct {
    pageID PageID
    tasks  []WriteTask
    done   chan error
}

// WriteTask 单个写入任务
type WriteTask struct {
    Key   []byte
    Value []byte
    ErrCh chan error
}
```

### 4.2 调度流程

```
┌─────────────────────────────────────────────────────┐
│                 PageDispatcher.Dispatch()            │
│                                                      │
│  1. 快速分桶 (KeyToPageHint)                          │
│         │                                             │
│         ▼                                             │
│  2. 桶内精确分组 (BatchResolvePageIDs)                 │
│         │                                             │
│         ▼                                             │
│  3. 按 Page 入队 (每个 Page 一个 PageQueue)            │
│         │                                             │
│         ├─→ Page-A Queue ─→ Worker Pool              │
│         ├─→ Page-B Queue ─→ Worker Pool              │
│         └─→ Page-C Queue ─→ Worker Pool              │
│                                                      │
│  4. 等待所有 Queue 完成                                │
│         │                                             │
│         ▼                                             │
│  5. 汇总结果返回                                       │
└─────────────────────────────────────────────────────┘
```

### 4.3 Worker Pool

```go
type WorkerPool struct {
    workers   int
    taskCh    chan func()
    semaphore chan struct{}  // 限制并发 worker 数
}

func NewWorkerPool(maxWorkers int) *WorkerPool {
    return &WorkerPool{
        workers:   maxWorkers,
        taskCh:    make(chan func(), maxWorkers*2),
        semaphore: make(chan struct{}, maxWorkers),
    }
}

// SubmitPage 提交一个 Page 的批量写入
// 同一 Page 的所有操作在同一个 goroutine 中顺序执行
func (wp *WorkerPool) SubmitPage(pageID PageID, tasks []WriteTask) {
    wp.semaphore <- struct{}{}
    go func() {
        defer func() { <-wp.semaphore }()
        for _, t := range tasks {
            t.Execute()  // 串行执行同一 page 的所有写入
        }
    }()
}
```

### 4.4 关键并发控制

```
Invariant 1: 同一 PageID 的所有写入在同一 goroutine 中顺序执行
    → 保证：PageQueue 由单一 goroutine 消费

Invariant 2: 不同 PageID 的写入可在不同 goroutine 中并发执行
    → 保证：不同 PageQueue 提交到不同 worker

Invariant 3: 事务写入独占执行，不与任何普通写入并发
    → 保证：事务提交到 txnCh，调度器等待所有 PageQueue 完成后才执行事务
```

## 5. 事务兼容

### 5.1 隔离模型

```
正常写入：Page 级并行
    Page-A ──→ Worker-1
    Page-B ──→ Worker-2  } 并发
    Page-C ──→ Worker-3

事务写入：全局串行
    Txn{T1, T2, T3} ──→ Txn Worker（独享 BTree）
                        ↑
                    所有 Page Worker 必须在此之前排空
```

### 5.2 调度策略

```go
func (pd *PageDispatcher) Dispatch(ops []WriteOp) error {
    // 分离事务和非事务操作
    txns, normal := partition(ops)

    // 1. 先处理所有非事务写入（Page 级并发）
    pd.dispatchNormal(normal)

    // 2. 等待所有 Page Worker 完成
    pd.waitAllPages()

    // 3. 顺序执行事务
    for _, txn := range txns {
        txn.Execute()  // 串行
    }

    return nil
}
```

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
// keys 和 values 一一对应
// 内部自动按 Page 分组、并发调度
func (bw *BatchWriter) WriteBatch(ctx context.Context, keys, values [][]byte) []error

// WriteTxn 事务写入
// 事务内所有操作串行执行
func (bw *BatchWriter) WriteTxn(ctx context.Context, txn *Transaction) error

// WriteMixed 混合写入
// 自动分离事务和非事务，先并发处理非事务，再串行处理事务
func (bw *BatchWriter) WriteMixed(ctx context.Context, ops []WriteOp) []error
```

### 6.2 与现有 BTree API 的关系

```
现有 API (保持兼容):
  tree.Set(ctx, key, value)        // 单 key 写入，直接调现有路径
  tree.Get(ctx, key)               // 读取不受影响

新增 API:
  batchWriter.WriteBatch(...)       // 批量写入优化路径
  batchWriter.WriteTxn(...)         // 事务写入路径
```

**不破坏现有 API**。`Set` 单次调用走现有 CAS 路径不变。`WriteBatch` 是新增的高性能批量路径。

## 7. 数据流

### 7.1 批量写入时序

```
Client
  │
  │ WriteBatch(keys=[k1..kN], values=[v1..vN])
  ▼
BatchWriter
  │
  │ 1. KeyToPageHint:  O(N) 快速分桶
  ▼
  │ 2. 桶内排序 + BatchResolvePageIDs: O(N log N)
  ▼
  │ 3. 按 PageID 分组: map[PageID][]WriteTask
  ▼
PageDispatcher
  │
  │ 4. SubmitPage: 每个 Page 提交到 worker pool
  ├─→ Worker-1: tasks=[k1,k5,k9]  → tree.Set(k1), tree.Set(k5), tree.Set(k9)  // 串行
  ├─→ Worker-2: tasks=[k2,k6]     → tree.Set(k2), tree.Set(k6)               // 串行
  └─→ Worker-3: tasks=[k3,k7,k8]  → tree.Set(k3), tree.Set(k7), tree.Set(k8) // 串行
  │
  │ 5. 等待所有 worker 完成
  ▼
  │ 6. 返回 []error
  ▼
Client
```

### 7.2 关键保证

- **同 Page 操作顺序执行**：不会有两个 goroutine 同时 CAS 同一 Page
- **零 CAS 重试**（理想情况）：每个 Page 只有一个写者，CAS 一次成功
- **跨 Page 真并发**：不同 Page 的写入完全并行，没有共享竞争

## 8. 关键设计决策

### 8.1 PageHint 粒度

| 选择 | 优点 | 缺点 |
|------|------|------|
| 粗粒度（如 key 前 1 字节，256 桶） | 管理开销小 | 桶内 key 多，串行度不足 |
| 细粒度（如精确 PageID，数万桶） | 最大并发度 | 桶过多，管理开销大 |
| **自适应**（前 2 字节 + BatchResolve refine） | 平衡 | 实现稍复杂 |

**选择：自适应**。快速分桶用前 2 字节（65536 个逻辑桶），然后通过 BatchResolvePageIDs 细化到实际 PageID。对于 100 万 key 的 benchmark，实际会落到几百到几千个物理 Page。

### 8.2 Worker 数量

```go
workers = min(len(pageGroups), runtime.GOMAXPROCS(0))
```

worker 数不超过 Page 分组数（没那么多 Page 就不需要那么多 worker），也不超过 CPU 核数。

### 8.3 事务隔离级别

V1 实现 **串行快照隔离（Serializable Snapshot Isolation）**：
- 事务开始时获取 BTree 的独占写权限
- 事务期间所有非事务写入被阻塞
- 事务提交后释放独占权限

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|------|:---:|:---:|------|
| Key→Page 映射错误 | 低 | 同一 key 被分到错误 page | BatchResolvePageIDs 精确映射 |
| 事务阻塞正常写入 | 中 | 延迟增加 | 事务队列 + 超时机制 |
| COW 分裂导致 PageID 变化 | 高 | 预分组失效 | 写入路径内重试 + Page 重映射 |
| 工作窃取不均衡 | 中 | 部分 worker 空闲 | 按 key 数量而非 Page 数量分配 |
| 内存开销（队列） | 低 | 大 batch 内存高 | 流式处理 + 背压 |

### 9.1 关键风险：COW 分裂

COW 写操作可能触发 Page 分裂，导致：
1. 分裂后 key 被移动到新 Page
2. 预计算的 PageID 失效

**缓解**：
```
写入路径内保持现有 CAS 重试机制
  → 当 CAS 失败 (ErrRetry) 时：
    1. 重新遍历找到新 Page
    2. 继续在当前 worker 中重试（不跨 worker）
    3. 同 Page 串行保证不破
```

分裂本身在同一个 worker 内处理，不影响其他 Page 的并发 worker。

## 10. 实现计划

### Phase 1：核心调度器（1-2 天）

- [ ] `PageDispatcher` + `PageQueue` 数据结构
- [ ] `WorkerPool` 实现
- [ ] `KeyToPageHint` 快速分桶
- [ ] `BatchResolvePageIDs` 精确分组

### Phase 2：集成 BTree（1 天）

- [ ] `BatchWriter` API 实现
- [ ] 与现有 `tree.Set` 的兼容适配
- [ ] COW 分裂后的 Page 重映射

### Phase 3：事务支持（1 天）

- [ ] 事务队列 + 独占执行
- [ ] 事务/非事务混合调度
- [ ] 隔离性测试

### Phase 4：验证（1 天）

- [ ] 8T/16T/32T 稳定性回归测试（CV < 5% 目标）
- [ ] CPU profile 验证锁竞争消除
- [ ] 性能对比：调度器开销 vs 原有直接并发

## 11. 预期效果

| 指标 | 当前 | 目标 |
|------|------|:---:|
| 8T CV | 16.9% | < 5% |
| 16T CV | 40.8% | < 5% |
| 32T CV | 100.9% | < 10% |
| 16T 极差比 | 2.93x | < 1.2x |
| 慢组 QPS 地板 | ~270K | 接近均值 |

预期新增的调度开销（分桶+分组）应控制在总延迟的 5% 以内，远小于当前 CAS 重试的损失。
