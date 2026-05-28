# BTree GetBatch + SetBatch 批量化读写设计

> 状态：Spike | 日期：2026-05-27 | 分支：`spike/btree-getbatch-setbatch`
> 参考：[PageDispatcher 设计文档](../../10_benchmark/2026-05-25-par-put-stability/2026-05-25-page-dispatcher-design.md)

## 1. 背景

### 1.1 现状

`service.KVStore` 接口定义了 `GetBatch`、`SetBatch`、`DeleteBatch` 三个批量操作，但 BTree 实现中三者均为 stub：

```go
// btree.go:499-508
func (b *BTree) GetBatch(_ context.Context, _ [][]byte) ([][]byte, error) {
    return nil, ErrNotImplemented
}
func (b *BTree) SetBatch(_ context.Context, _ []service.KVPair) error {
    return ErrNotImplemented
}
```

### 1.2 已有基础设施

| 组件 | 文件 | 状态 |
|------|------|:----:|
| `PageDispatcher` | `page_dispatcher.go` | ✅ 已实现 |
| `BatchWriter.WriteBatch` | `batch_writer.go` | ✅ 已实现 |
| `ResolvePageID` | `resolve_page.go` | ✅ 已实现 |
| `KeyToShard` (FNV-1a) | `page_dispatcher.go` | ✅ 已实现 |
| `resolveShardPageIDs` | `page_dispatcher.go` | ✅ 已实现 |
| `inSamePage` | `resolve_page.go` | ⚠️ stub（永远返回 false） |
| `SetWithRetry` | `set_with_retry.go` | ✅ 已实现 |
| `getRawBytes` | `btree.go:258-297` | ✅ 已实现 |

### 1.3 目标

1. **GetBatch**：读路径 lock-free，直接 `errgroup` + 并行 `Get` 即可，无需 PageDispatcher
2. **SetBatch**：对接已有 `BatchWriter.WriteBatch`，完成 `KVStore` 接口实现
3. **DeleteBatch**：暂不实现，留待 compaction 成熟后统一处理

---

## 2. GetBatch 设计

### 2.1 为什么不需要 PageDispatcher

PageDispatcher 的复杂度是为解决**写路径的 CAS 竞争**。读路径的根本区别：

| 维度 | WriteBatch | GetBatch |
|------|-----------|---------|
| 操作性质 | CAS + COW（有锁） | searchPath + leaf read（lock-free） |
| 同 Page 并发 | 多写者 CAS 冲突 → 需要串行化 | 无竞争 |
| 需要 WorkerPool？ | 是（CAS 重试 + 重新入队） | 否 |
| 需要 Hash 分桶 + resolvePageIDs？ | 是（按 Page 分组消除争抢） | 否（直接并行 Get 就行） |

**结论：读无锁 → 直接并行走，不需要任何 PageDispatcher 机制。**

尽管理论上排序 + 同 Page 复用 searchPath 可以节省遍历开销，但 `inSamePage` 当前是 stub（永远返回 false），这个优化根本不生效。花 ~200 行代码买一个没启用的优化是过度设计。

### 2.2 实现

```go
// GetBatch 批量读取。内部并行调用 Get。
// 缺失/tombstone 的 key 在结果数组中返回 nil。
// Callers MUST check results[i] == nil to distinguish missing keys.
// ctx 取消或 tree 关闭时返回 error。
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
    if err := b.checkOpen(); err != nil {
        return nil, err
    }
    if len(keys) == 0 {
        return nil, nil
    }

    results := make([][]byte, len(keys))
    g, ctx := errgroup.WithContext(ctx)
    g.SetLimit(min(runtime.GOMAXPROCS(0)*4, 64))

    for i, key := range keys {
        g.Go(func() error {
            if err := ctx.Err(); err != nil {
                return err
            }
            val, err := b.Get(ctx, key)
            if errors.Is(err, ErrKeyNotFound) {
                return nil // key 不存在 → results[i] 保持 nil
            }
            if err != nil {
                return err
            }
            results[i] = val
            return nil
        })
    }

    if err := g.Wait(); err != nil {
        return nil, err
    }
    return results, nil
}
```

~20 行。KISS。

### 2.3 关键设计决策

#### 2.3.1 缺失 key 处理

`Get` 单 key 返回 `ErrKeyNotFound`，但 `GetBatch` 对缺失/tombstone key 返回 `nil`。单 key 失败不应中断整批。

> **API 不对称**：`Get(key)` → `ErrKeyNotFound`，`GetBatch(keys)` → `results[i] = nil`。调用方务必检查 `results[i] == nil`。

#### 2.3.2 并发度

```go
g.SetLimit(runtime.GOMAXPROCS(0) * 4)
```

读操作 CPU-bound（searchPath 二分查找），4×核数提供足够的流水线填充。

#### 2.3.3 非事务语义（latest-committed）

`GetBatch` 返回 B+Tree 最新已提交值，非快照隔离。同一 batch 内不同 key 可能来自不同时间点。需要跨 key 一致性读时使用 `BeginTx`。

#### 2.3.4 V2 优化方向

如果 profiling 显示 searchPath 是瓶颈，V2 可以加排序优化：
1. 按 key 排序 → 相邻 key 大概率同 page
2. 实现 `inSamePage` → 同 page key 直接在当前 leaf 搜索，省去 searchPath 遍历

但 V1 不做——先用简单实现，benchmark 说话。

---

## 3. SetBatch 设计

### 3.1 与 BatchWriter 的关系

`BatchWriter.WriteBatch` 已经实现了完整的批量写入逻辑：

```go
// batch_writer.go
func (bw *BatchWriter) WriteBatch(ctx context.Context, keys, values [][]byte) error
```

`SetBatch` 是 `KVStore` 接口层，需要做的是：
1. 将 `[]service.KVPair` 拆解为 `[][]byte` keys 和 `[][]byte` values
2. 委托给 `BatchWriter.WriteBatch`

### 3.2 集成方式

两种方案：

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. 每调用创建 BatchWriter** | `SetBatch` 内 `NewBatchWriter(b)` → `WriteBatch` → `Shutdown` | 简单，无状态 | 每次创建 WorkerPool（常驻 goroutine），高频调用开销大 |
| **B. BTree 持有 BatchWriter** | `b.batchWriter` 字段，懒初始化，复用 | WorkerPool 常驻，零创建开销 | BTree 增加字段 |

**决策：方案 B**。

- `BatchWriter` 内含 `WorkerPool`（8 个常驻 goroutine），创建/销毁开销不可忽略
- BTree 已有 `txMgr` 等类似字段，增加 `batchWriter` 符合现有模式
- 懒初始化：首次调用 `SetBatch` 时创建，`Close` 时 Shutdown

### 3.3 实现

```go
// SetBatch 批量写入（实现 service.KVStore 接口）。
// 内部委托给 BatchWriter.WriteBatch。
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
    if err := b.checkOpen(); err != nil {
        return err
    }
    if len(pairs) == 0 {
        return nil
    }

    keys := make([][]byte, len(pairs))
    values := make([][]byte, len(pairs))
    for i, p := range pairs {
        keys[i] = p.Key
        values[i] = p.Value
    }

    bw := b.getBatchWriter()
    // 二次检查：Close() 可能在 checkOpen 和 getBatchWriter 之间执行
    if err := b.checkOpen(); err != nil {
        return err
    }
    return bw.WriteBatch(ctx, keys, values)
}

// getBatchWriter 懒初始化并返回 BatchWriter。
func (b *BTree) getBatchWriter() *BatchWriter {
    b.batchWriterOnce.Do(func() {
        b.batchWriter = NewBatchWriter(b)
    })
    return b.batchWriter
}
```

### 3.4 BTree 结构变更

```go
// btree.go BTree struct 新增字段：
type BTree struct {
    // ... 现有字段 ...
    batchWriter     *BatchWriter  // 批量写入器（懒初始化）
    batchWriterOnce sync.Once     // 懒初始化控制
}
```

`Close()` 方法新增：

```go
func (b *BTree) Close() error {
    if !b.closed.CompareAndSwap(false, true) {
        return nil
    }
    // 新增：关闭 BatchWriter
    if b.batchWriter != nil {
        b.batchWriter.Shutdown()
    }
    // ... 现有 cleanup ...
}
```

### 3.5 与 WriteBatch 的语义差异

`BatchWriter.WriteBatch` 已经处理：
- keys/values 长度校验
- 部分失败 → `*BatchError`
- CAS 重试 → 重新入队

`SetBatch` 作为 `KVStore` 接口适配层，不添加额外语义。

---

## 4. DeleteBatch 设计（简要）

```go
func (b *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error {
    // V1 策略：逐 key 调用 Delete
    // 不经过 PageDispatcher——单个 Delete 仅设置 tombstone，操作简单
    // 等 compaction 成熟 + 批量 tombstone 优化后再接入调度器
    for _, key := range keys {
        if err := b.Delete(ctx, key); err != nil && !errors.Is(err, ErrKeyNotFound) {
            return err
        }
    }
    return nil
}
```

**理由**：Delete 的本质是 tombstone 标记（无 COW 分裂），单 key 操作极快。引入 PageDispatcher 的 Hash 分桶 + WorkerPool 开销可能超过收益。V1 用最简单实现，V2 根据 benchmark 数据决定是否优化。

---

## 5. 实现计划

### Phase 1：GetBatch（0.5 天）

- [ ] `GetBatch` 实现：errgroup + 并行 Get（~20 行）
- [ ] 基础单元测试

### Phase 2：SetBatch 对接（0.5 天）

- [ ] BTree 新增 `batchWriter` + `batchWriterOnce` 字段
- [ ] `getBatchWriter()` 懒初始化
- [ ] `SetBatch` 实现（KVPair → keys/values → WriteBatch）
- [ ] `Close()` 中 Shutdown BatchWriter

### Phase 3：测试（1 天）

- [ ] GetBatch 单元测试
- [ ] SetBatch 单元测试
- [ ] GetBatch + SetBatch 并发安全性（`-race`）

### Phase 4：DeleteBatch（0.5 天）

- [ ] 简单串行 Delete 循环
- [ ] 基础测试

---

## 6. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|------|:---:|:---:|------|
| **SetBatch + 事务写入重叠 key → 静默丢数据** | 中 | **CRITICAL**：SetBatch 绕过 KeyLock + VersionChain 直接写 B+Tree，事务提交的 CAS 可能覆盖 SetBatch 的值且无错误返回 | V1：文档声明**禁止** SetBatch 与事务写入用于重叠 key 集合。V2：引入 OverlapDetector 或统一调度器 |
| GetBatch 中大 batch 的 searchPath 复用不生效（inSamePage stub） | 高 | 性能不如预期 | 排序后 key 的 BTree 缓存局部性仍有一定收益；V2 实现 inSamePage |
| epoch slot 在 Page 级而非 key 级可能过宽 | 低 | epoch 回收延迟 | 每 Page 读耗时 << epoch 回收间隔（500ms），影响可忽略 |
| BatchWriter 懒初始化与 Close 的竞态 | 低 | SetBatch 在 Close 后执行 → 返回 `*BatchError` 而非 `ErrTreeClosed` | `getBatchWriter()` 返回后再次检查 `b.closed` |
| GetBatch 对缺失 key 返回 nil 可能被误用 | 中 | 调用方未检查 nil → nil pointer | API 文档 + GoDoc 明确说明 nil 语义 |
| Page 过多时 errgroup goroutine 超 epoch slot 数 | 低 | epoch 回收延迟增加 | `SetLimit` 上限 64（匹配 EpochManager slot 数） |
| SetBatch 的 WorkerPool 8 个常驻 goroutine 闲置时浪费 | 低 | 内存开销（~16KB） | 可接受；未来可加 idle timeout |
| DeleteBatch 与并发 SetBatch 混用同一 Page 引入 CAS 竞争 | 中 | Delete 的 CAS 与 pageBatch 的 SetWithRetry 冲突 | V1 文档声明调用者约束；V2 统一调度 |
| SetBatch 不写 VersionChain | 中 | 如果 SetBatch 覆盖了曾由事务写入的 key，旧 VersionChain 变为悬空引用，快照读可能返回过时值 | V1 文档声明 SetBatch 用于纯非事务写入路径；事务写入过的 key 不应再用 SetBatch |

### 6.1 关键约束

#### 6.1.1 SetBatch 与事务写入的隔离（CRITICAL）

SetBatch 绕过 KeyLock + VersionChain 直接写 B+Tree：

```
SetBatch 路径:   Hash分桶 → resolvePageIDs → SetWithRetry(CAS 3次)
事务提交路径:    KeyLock → VersionChain.Prepend → btreeStorageAdapter.Set(CAS)
```

**危险场景**：
1. Txn 获取 KeyLock(k)
2. SetBatch CAS-writes to B+Tree(k)（无 KeyLock）
3. Txn CAS-writes to B+Tree(k)（under KeyLock）
4. **如果 Txn 的 CAS 胜出**：SetBatch 的值被覆盖且无 VersionChain 记录 → 静默丢失

> **V1 强制约束**：SetBatch 与事务写入**禁止**用于重叠 key 集合。违反此约束会导致静默数据丢失。

#### 6.1.2 SetBatch 与 tree.Set 的 Page 级 CAS 竞争

与 `BatchWriter.WriteBatch` 相同的约束：

> **调用者约束**：`SetBatch` 和 `tree.Set` 混用同一 Page 会引入 CAS 竞争。V1 期望调用者按路径隔离：批量写入走 `SetBatch`，单 key 写入走 `tree.Set`。

`GetBatch` 无此约束——读操作 lock-free，可与写操作任意混用。

#### 6.1.3 SetBatch 不写 VersionChain

SetBatch 使用 `SetWithRetry`（writeOperationWithRetry），直接写 MVCC 编码值到 B+Tree，不创建 VersionChain 条目。如果 key 之前被事务写入过（有 VersionChain），快照读在 timestamp 介于旧 chain 和新 SetBatch beginTS 之间时，会错误地从 VersionChain 返回旧值而非 SetBatch 写入的新值。

> **V1 约束**：事务写入过的 key 不应再通过 SetBatch 写入。SetBatch 适用于纯非事务批量写入路径。

---

## 7. 测试策略

### 7.1 单元测试场景

**GetBatch：**

| 场景 | 输入 | 预期 |
|------|------|------|
| 空 batch | `keys=[]` | `results=[], err=nil` |
| 单 key 存在 | `keys=[k1]` | `results=[v1]` |
| 单 key 不存在 | `keys=[k_missing]` | `results=[nil]` |
| 全部存在 | `keys=[k1,k2,k3]` | `results=[v1,v2,v3]` |
| 部分缺失 | `keys=[k1,k_missing,k3]` | `results=[v1,nil,v3]` |
| tombstone key | 先 Set 后 Delete 的 key | `results[i]=nil` |
| 跨 Page | 足够多的 key 使 BTree 多 page | 所有存在的 key 返回正确值 |
| context cancel | `ctx, cancel := context.WithCancel(...); cancel()` | 返回 `ctx.Err()` |
| tree closed | `tree.Close()` 后调用 | 返回 `ErrTreeClosed` |

**SetBatch：**

| 场景 | 输入 | 预期 |
|------|------|------|
| 空 batch | `pairs=[]` | `err=nil` |
| 单 pair | `pairs=[{k1,v1}]` | 写入成功，`Get(k1)=v1` |
| 大批量 | 100K pairs | 全部写入成功 |
| 部分失败 | 混入无效操作 | 返回 `*BatchError` |
| tree closed | `tree.Close()` 后调用 | 返回 `ErrTreeClosed` |

### 7.2 性能基准

| Benchmark | 对比基线 | 目标 |
|-----------|---------|:---:|
| `GetBatch_1K` vs `Get×1K` | 串行 Get 循环 | >4x throughput |
| `SetBatch_10K` vs `Set×10K` | 串行 Set 循环 | >5x throughput（已有 PageDispatcher） |

### 7.3 竞态检测

```bash
go test -v -race -run "TestGetBatch|TestSetBatch" ./internal/infrastructure/storage/btree/
```

---

## 8. SetBatch 并行化瓶颈分析（2026-05-28 补充）

### 8.1 Benchmark 结果

GetBatch + SetBatch 实现完成后的 benchmark 数据（500K ops, 8 核）：

| 场景 | batch size | 线程 | QPS |
|------|-----------|------|-----|
| batch-get-256 | 256 | 1 | **1.50M** |
| batch-get-1024 | 1024 | 1 | **1.87M** |
| par-batch-set-256-8 | 256 | 8 | **1.30M** |
| par-batch-get-256-8 | 256 | 8 | **4.27M** |
| par-batch-set-1024-8 | 1024 | 8 | **1.70M** |
| par-batch-get-1024-8 | 1024 | 8 | **4.51M** |

**核心问题**：SetBatch 8 线程仅 1.30M QPS，GetBatch 8 线程达 4.27M QPS。SetBatch 并行扩展效率极低（1→8 线程仅 1.2x）。

### 8.2 根因分析

瓶颈分两层：

#### 第一层：bench 外层 mutex（直接原因）

```go
// cmd/tools/btree_bench/main.go:349-368
var setMu sync.Mutex  // 所有 goroutine 共享
setMu.Lock()
_ = tree.SetBatch(ctx, pairs)
setMu.Unlock()
```

8 个 goroutine 中只有 1 个能拿到锁，其余全部阻塞。**直接退化为串行。**

注释原因：`SetBatch uses a shared BatchWriter with non-concurrent-safe WorkerPool`。

#### 第二层：SetBatch 内部同步阻塞（根本原因）

调用链：

```
SetBatch → BatchWriter.WriteBatch → PageDispatcher.Dispatch → WorkerPool.Submit + Wait()
```

关键代码路径：

1. **BTree 持有唯一 `batchWriter` 实例**（懒初始化，全局共享）

   ```go
   // btree.go:45
   batchWriter *BatchWriter

   // btree.go:589-593
   func (b *BTree) getBatchWriter() *BatchWriter {
       b.batchWriterOnce.Do(func() {
           b.batchWriter = NewBatchWriter(b)
       })
       return b.batchWriter
   }
   ```

2. **`BatchWriter` 内含唯一 `PageDispatcher`**，后者内含唯一 `WorkerPool`

   ```go
   // batch_writer.go:10-18
   type BatchWriter struct {
       dispatcher *PageDispatcher
   }
   func NewBatchWriter(tree *BTree) *BatchWriter {
       return &BatchWriter{
           dispatcher: NewPageDispatcher(tree),
       }
   }
   ```

3. **`PageDispatcher.Dispatch` 内部调用 `pd.pool.Wait()`（同步阻塞）**

   ```go
   // page_dispatcher.go:254-255
   // Phase 5: 等待完成
   pd.pool.Wait()
   ```

   这意味着每次 `Dispatch` 调用是**同步阻塞**的：提交所有 page batch 到 WorkerPool，然后 Wait() 等全部 worker 完成。两个并发 SetBatch 会共享同一个 WorkerPool 的 `sync.WaitGroup`，导致 Wait() 语义混乱。

**结论**：即使去掉 bench 的外层 mutex，并发 SetBatch 也会因为共享 WorkerPool + Dispatch 内部 Wait() 而无法真正并行。

#### 对比 GetBatch

```go
// GetBatch - 每次 errgroup 独立
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(min(runtime.GOMAXPROCS(0)*4, 64))
```

GetBatch 用 errgroup 每次调用创建新 group，无共享状态，**真正并行**。

### 8.3 架构对比

```
当前架构（串行）：
BTree
└── 1 个 BatchWriter（全局共享）
    └── 1 个 PageDispatcher
        └── 1 个 WorkerPool（N 个 worker goroutine）
            ├── Dispatch() → Submit all batches → Wait() ← 同步阻塞点
            └── 所有并发 SetBatch 调用排队等待

GetBatch 架构（真并行）：
BTree
└── 每次 GetBatch 调用 → 独立 errgroup → 独立 goroutine 集合
    └── 无共享状态 → 无阻塞 → 真并行
```

| 维度 | GetBatch | SetBatch |
|------|----------|----------|
| 并行模型 | errgroup，每次独立 | 共享 BatchWriter + WorkerPool |
| 并发调用 | 多个 GetBatch 真并行 | 多个 SetBatch 被 Wait() 串行化 |
| bench 外锁 | 无 | mutex（防御性） |
| 实际效果 | 8线程 4.5M QPS | 8线程 1.3M QPS（≈1线程） |

### 8.4 修复方案

**核心思路**：去掉全局共享 BatchWriter，让每次 SetBatch 调用创建独立的 BatchWriter（含独立的 PageDispatcher + WorkerPool）。

#### 方案 A：每次 SetBatch 创建独立 BatchWriter（KISS 最优）

```go
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
    if err := b.checkOpen(); err != nil {
        return err
    }
    if len(pairs) == 0 {
        return nil
    }

    keys := make([][]byte, len(pairs))
    values := make([][]byte, len(pairs))
    for i, p := range pairs {
        keys[i] = p.Key
        values[i] = p.Value
    }

    // 关键：每次创建独立 BatchWriter，无共享状态
    bw := NewBatchWriter(b)
    defer bw.Shutdown()
    return bw.WriteBatch(ctx, keys, values)
}
```

**变更量**：
- `btree.go`：删除 `batchWriter`、`batchWriterOnce` 字段，删除 `getBatchWriter()` 方法，简化 `Close()`
- `btree_bench/main.go`：删除外层 `setMu` mutex

**优点**：
- 无锁、无共享状态、无复杂调度
- 完全对齐 GetBatch 的独立上下文模式
- 改动最小（删除代码 > 新增代码）

**缺点**：
- 每次 SetBatch 创建 WorkerPool（N 个 goroutine），单次调用有 goroutine 创建开销
- 高频小 batch 场景（batch=1~10）可能不如方案 B

#### 方案 B：共享 WorkerPool + 异步 Dispatch（更复杂但更优）

将 `Dispatch` 改为异步提交（不内部 Wait），调用方在需要时统一 Wait：

```go
// DispatchAsync 提交任务但不等待完成，返回 WaitFunc
func (pd *PageDispatcher) DispatchAsync(ctx context.Context, keys, values [][]byte) (func() ([]WriteResult, error), error)

// SetBatch 内部
future, err := pd.DispatchAsync(ctx, keys, values)
// ... 其他逻辑
results, err := future() // 此时才 Wait
```

**优点**：WorkerPool 常驻复用，无创建开销

**缺点**：
- 改动大，需要重构 PageDispatcher.Dispatch（影响面广）
- 需要处理并发 DispatchAsync 的 WaitGroup 语义（当前 wg 是全局的）
- 过度设计——当前没有证据表明 WorkerPool 创建是瓶颈

### 8.5 推荐方案：方案 A

**理由**：

1. **KISS**：删除代码比新增代码更好
2. **对齐现有模式**：GetBatch 已经证明了「每次独立上下文」的可行性
3. **改动量极小**：~20 行变更，不触及 PageDispatcher / WorkerPool 内部逻辑
4. **WorkerPool 创建开销可接受**：
   - WorkerPool 创建 = `min(GOMAXPROCS, numShards/2)` = 4~8 个 goroutine
   - goroutine 初始栈仅 2KB（Go 1.21+），创建成本 ~100ns
   - 对比 Dispatch 本身的 SetWithRetry CAS 开销，goroutine 创建可忽略
5. **benchmark 数据预期**：SetBatch 8线程 QPS 从 1.3M → 4.0~4.5M（与 GetBatch 持平）

### 8.6 实施步骤

#### Step 1：重构 SetBatch（btree.go）

```diff
 type BTree struct {
-    batchWriter     *BatchWriter  // batch write dispatcher (lazy init)
-    batchWriterOnce sync.Once
     // ... 其他字段不变
 }

 func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
     if err := b.checkOpen(); err != nil {
         return err
     }
     if len(pairs) == 0 {
         return nil
     }

     keys := make([][]byte, len(pairs))
     values := make([][]byte, len(pairs))
     for i, p := range pairs {
         keys[i] = p.Key
         values[i] = p.Value
     }

-    bw := b.getBatchWriter()
-    if err := b.checkOpen(); err != nil {
-        return err
-    }
-    return bw.WriteBatch(ctx, keys, values)
+    bw := NewBatchWriter(b)
+    defer bw.Shutdown()
+    return bw.WriteBatch(ctx, keys, values)
 }

-// getBatchWriter 懒初始化并返回 BatchWriter。
-func (b *BTree) getBatchWriter() *BatchWriter {
-    b.batchWriterOnce.Do(func() {
-        b.batchWriter = NewBatchWriter(b)
-    })
-    return b.batchWriter
-}
```

#### Step 2：简化 Close()（btree.go）

```diff
 func (b *BTree) Close() error {
     if !b.closed.CompareAndSwap(false, true) {
         return nil
     }
-    b.batchWriterOnce.Do(func() {})
-    if b.batchWriter != nil {
-        b.batchWriter.Shutdown()
-        b.batchWriter.Wait()
-    }
     // ... 其他 cleanup 不变
 }
```

#### Step 3：去掉 bench 外层 mutex（cmd/tools/btree_bench/main.go）

```diff
 func batchLoop(n, threads int, tree *btree.BTree, ops *atomic.Int64, getOnly bool, batchSize, maxKey int) {
     ctx := context.Background()
     numBatches := (n + batchSize - 1) / batchSize
-    var setMu sync.Mutex

     execBatch := func(b int) {
         // ...
         } else {
             pairs := make([]service.KVPair, end-start)
             for i := range pairs {
                 pairs[i] = service.KVPair{Key: keyOf(start + i), Value: valOf(start + i)}
             }
-            setMu.Lock()
             _ = tree.SetBatch(ctx, pairs)
-            setMu.Unlock()
         }
         // ...
     }
```

#### Step 4：运行 benchmark 验证

```bash
go run ./cmd/tools/btree_bench -only par-batch -n 500000 -warmup 50000
```

预期：par-batch-set-256-8 QPS 从 ~1.3M 提升到 ~4.0M+。

#### Step 5：运行测试确保无回归

```bash
go test -v -race -run "TestGetBatch|TestSetBatch" ./internal/infrastructure/storage/btree/
```

---

## 9. 参考

- [PageDispatcher 设计文档](../../10_benchmark/2026-05-25-par-put-stability/2026-05-25-page-dispatcher-design.md) — Hash 分桶、WorkerPool、CAS 重试策略
- [BTree 存储引擎设计](../../02_design/03_存储引擎设计.md)
- [Lealone AOSE 异步 Page 调度](https://github.com/codefollower/My-Blog/issues/22) — Page→线程绑定思想来源
- `internal/infrastructure/storage/btree/batch_writer.go` — 已实现的 BatchWriter
- `internal/infrastructure/storage/btree/page_dispatcher.go` — 已实现的 PageDispatcher
