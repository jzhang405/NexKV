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

1. **GetBatch**：利用 PageDispatcher 的 Hash 分桶 + resolvePageIDs 模式，实现高性能批量读取
2. **SetBatch**：对接已有 `BatchWriter.WriteBatch`，完成 `KVStore` 接口实现
3. **DeleteBatch**：暂不实现，留待 compaction 成熟后统一处理

---

## 2. GetBatch 设计

### 2.1 与 WriteBatch 的本质差异

| 维度 | WriteBatch (已实现) | GetBatch (本设计) |
|------|-------------------|-------------------|
| 操作性质 | 写（CAS + COW） | 读（lock-free） |
| 并发竞争 | 同 Page 多写者 CAS 冲突 | 无竞争 |
| 重试需求 | CAS 失败 → 重新入队 | 无重试 |
| Worker 模型 | 常驻 WorkerPool + taskCh | 轻量 errgroup + goroutine |
| Epoch 保护 | 写路径不涉及 epoch | 每次读需 EnterRead/ExitRead |
| 调度目标 | 消除 CAS 竞争 | 最大化并行度 |

核心结论：**GetBatch 不需要 WorkerPool**。读操作无 CAS 竞争，直接 goroutine 并行即可。但**按 Page 分组的 Hash 分桶策略仍然有效**——同 Page 的 key 排序后批量读可以减少 searchPath 遍历次数。

### 2.2 核心设计：ReadBatchDispatcher

```
                         GetBatch(ctx, keys)
                              │
              ┌───────────────┴───────────────┐
              │  Phase 1: Hash 分桶 (FNV-1a)   │
              │  64 shards, O(N)               │
              └───────────────┬───────────────┘
                              │
              ┌───────────────┴───────────────┐
              │  Phase 2: 桶内排序 +           │
              │  resolvePageIDs (errgroup)     │
              │  复用 PageDispatcher 逻辑       │
              └───────────────┬───────────────┘
                              │
              ┌───────────────┴───────────────┐
              │  Phase 3: 跨桶 merge            │
              │  map[PageID][]readTask          │
              └───────────────┬───────────────┘
                              │
              ┌───────────────┴───────────────┐
              │  Phase 4: 按 Page 并行读        │
              │  errgroup + goroutine per page  │
              │  每 Page 内顺序读（searchPath   │
              │  复用 + MVCC 解码）             │
              └───────────────┬───────────────┘
                              │
              ┌───────────────┴───────────────┐
              │  Phase 5: 收集结果              │
              │  results[i] = value (按 idx)    │
              └───────────────────────────────┘
```

### 2.3 数据结构

```go
// readTask 单个读取任务。
type readTask struct {
    idx int    // 在原始 keys 数组中的位置
    key []byte
}

// pageReadBatch 单个 Page 的批量读取任务。
type pageReadBatch struct {
    pageID  model.PageID
    tasks   []readTask
    results [][]byte // results[i] 对应 tasks[i].idx（预分配，索引写入）
}
```

### 2.4 每 Page 读执行流程

```go
// executePageRead 在单个 Page 内顺序执行所有读操作。
// 同 Page 内 key 已排序，相邻 key 大概率在同一 leaf page，
// 可通过 inSamePage 快速判断减少 searchPath 遍历。
//
// 错误处理：
//   - 缺失 key / tombstone / MVCC 解码失败 → results[i] = nil（静默，不中断整批）
//   - searchPath ErrRetry（瞬态） → 重试一次，仍失败则 results[i] = nil
//   - ctx.Err() / ErrTreeClosed（非瞬态） → 返回 error（中断整批）
func (b *BTree) executePageRead(ctx context.Context, batch *pageReadBatch) error {
    // Epoch 保护：整批读共享一个 epoch slot
    var epochSlot int
    if b.epochMgr != nil {
        epochSlot = b.epochMgr.AllocSlot()
        b.epochMgr.EnterRead(epochSlot)
        defer b.epochMgr.ExitRead(epochSlot)
    }

    var lastPath SearchPath
    var lastLeaf LeafPage
    var lastPageID model.PageID

    // releaseLast 释放缓存的上一个 path 和 leaf handle（幂等，panic 安全）
    releaseLast := func() {
        if lastLeaf != nil {
            lastLeaf.Release()
            lastLeaf = nil
        }
        if lastPath != nil {
            lastPath.ReleaseAll()
            lastPath = nil
        }
    }
    defer releaseLast() // panic 安全：确保 epoch 退出前释放资源

    for i, t := range batch.tasks {
        // 检查 context（每 key 检查，不阻塞取消）
        if err := ctx.Err(); err != nil {
            releaseLast()
            return err
        }

        // 快速路径：与前一个 key 同 page，直接在当前 leaf 中搜索
        if lastLeaf != nil && b.inSamePage(lastPageID, t.key) {
            idx, found := lastLeaf.Search(t.key)
            if found {
                raw := lastLeaf.GetValue(idx)
                mvccVal, err := mvcc.ParseMVCC(raw)
                if err == nil && !mvccVal.IsTombstone() {
                    batch.results[i] = mvccVal.RealVal
                }
                // 未找到或 tombstone：results[i] 保持 nil
                continue
            }
            // false positive from inSamePage：释放旧缓存，走慢速路径
            releaseLast()
        }

        // 慢速路径：searchPath → getLeaf → search → parse MVCC
        path, err := searchPath(b.rootRef, t.key)
        if err != nil {
            if errors.Is(err, ErrRetry) {
                // 瞬态错误（mid-split）：重试一次
                path, err = searchPath(b.rootRef, t.key)
            }
            if err != nil {
                continue // 仍然失败 → 该 key 结果为 nil
            }
        }

        leafEntry := path.Leaf()
        pInfo := leafEntry.Ref.GetPageInfo()
        if pInfo == nil {
            path.ReleaseAll()
            continue
        }

        leaf, err := b.storage.GetLeafPage(pInfo.PageID)
        if err != nil {
            path.ReleaseAll()
            continue
        }

        idx, found := leaf.Search(t.key)
        if found {
            raw := leaf.GetValue(idx)
            mvccVal, err := mvcc.ParseMVCC(raw)
            if err == nil && !mvccVal.IsTombstone() {
                batch.results[i] = mvccVal.RealVal
            }
        }

        // 释放旧 path/leaf，缓存当前的供下一个 key 复用
        releaseLast()
        lastPath = path
        lastLeaf = leaf
        lastPageID = pInfo.PageID

        // 注意：如果 key 未找到，leaf 已释放但 lastPath 仍被缓存。
        // 下一个 key 的 inSamePage(lastPageID) 会判定是否在范围内。
        if !found {
            // 未找到 key 时释放 leaf handle（数据已无用），但保留 path 引用用于 inSamePage
            if lastLeaf != nil {
                lastLeaf.Release()
                lastLeaf = nil
            }
        }
    }

    releaseLast()
    return nil
}
```

### 2.5 关键设计决策

#### 2.5.1 inSamePage 优化

当前 `inSamePage` 是 stub（永远返回 false）。在 GetBatch 场景中，同 Page 内的 key 序列化执行，searchPath 复用收益显著。

**决策**：GetBatch V1 不依赖 `inSamePage`。排序后的 key 通过 searchPath 顺序遍历，利用 BTree 缓存局部性已能获得大部分收益。`inSamePage` 实现作为 V2 优化项。

#### 2.5.2 缺失 key 处理

`Get` 单 key 返回 `ErrKeyNotFound`。但批量场景下，一个 key 缺失不应该让整个 batch 失败。

**决策**：`GetBatch` 对缺失/tombstone 的 key 在结果数组中返回 `nil`，不返回 error。调用方通过 `results[i] == nil` 判断 key 不存在。

> **⚠️ API 不对称性**：`Get(key)` 对缺失 key 返回 `ErrKeyNotFound`，但 `GetBatch(keys)` 返回 `nil`。这是因为批量 API 中，单 key 失败不应中断整批。调用方务必检查 `results[i] == nil`，而非依赖 error 判断 key 存在性。

但以下**非瞬态错误**仍需返回（中断整批）：
- `ctx.Err()` — context 取消
- `ErrTreeClosed` — 树已关闭

#### 2.5.3 并行度控制

读操作无锁，理论上可以无限并行。但过多 goroutine 导致调度开销；且 EpochManager 仅有 64 个 slot，并发数不应超过 slot 数。

**决策**：
```go
maxConcurrency = min(runtime.GOMAXPROCS(0)*4, len(pageGroups), 64)
```

- 上限 64（匹配 EpochManager slot 数，防止极端情况下 epoch 回收延迟）
- Page 数量较少时，limit = pageGroups 数量

#### 2.5.4 Epoch 保护

写路径通过 `writeOperationWithRetry` 的 epoch slot 保护。读路径需要类似保护。

**决策**：每个 `executePageRead` goroutine 一个 epoch slot（而非每 key）。整批 Page 读在同一个 EnterRead/ExitRead 窗口内完成，减少 epoch 分配开销。64 个 slot 足够覆盖最大并发 64 个 reader。

#### 2.5.5 与 WriteBatch 共享基础设施

Phase 1-3（Hash 分桶 → 排序 → resolvePageIDs → merge）与 WriteBatch 完全一致。

**决策**：V1 不抽取公共函数。`GetBatch` 使用 `keyWithIndex`（已在 `page_dispatcher.go` 中定义），`WriteBatch` 使用 `writeTask`。两者类型不同，强行统一会增加泛型/接口复杂度。等两者稳定后再评估抽取收益。

#### 2.5.6 非事务读语义（latest-committed）

`GetBatch` 与 `Get` 一致，返回 B+Tree 上的**最新已提交值**，而非快照隔离读（snapshot-isolated）。这意味着：
- 同一 batch 内不同 key 可能来自不同时间点（无一致性保证）
- 与 `SnapshotTx.Get` 的快照语义不同

**需要跨 key 一致性读的场景**：使用 `BeginTx(ctx, WithReadOnly())` 获取事务，在事务内逐 key 调用 `tx.Get`。

> **文档要求**：`GetBatch` 的 GoDoc 必须显式说明此语义差异。

### 2.6 API 设计

```go
// GetBatch 批量读取。
// 返回的 values 与 keys 一一对应：values[i] 是 keys[i] 的值。
// key 不存在或已 tombstone 时 values[i] == nil。
// context 取消或 tree 关闭时返回 error。
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
    if err := b.checkOpen(); err != nil {
        return nil, err
    }
    if len(keys) == 0 {
        return nil, nil
    }

    // Phase 1: Hash 分桶
    shards := make([][]keyWithIndex, numShards)
    for i, key := range keys {
        s := KeyToShard(key)
        shards[s] = append(shards[s], keyWithIndex{key: key, idx: i})
    }

    // Phase 2: 桶内排序 + resolvePageIDs（内联实现，见附录）
    pageGroups, err := b.resolveKeysToPages(ctx, shards)
    if err != nil {
        return nil, err
    }

    // Phase 3: 按 Page 并行读
    results := make([][]byte, len(keys))
    batches := make([]*pageReadBatch, 0, len(pageGroups))
    for pid, tasks := range pageGroups {
        batch := &pageReadBatch{
            pageID:  pid,
            tasks:   make([]readTask, len(tasks)),
            results: results, // 共享 results slice（各 goroutine 写不相交的 idx）
        }
        for i, t := range tasks {
            batch.tasks[i] = readTask{idx: t.idx, key: t.key}
        }
        batches = append(batches, batch)
    }

    g, ctx := errgroup.WithContext(ctx)
    g.SetLimit(min(runtime.GOMAXPROCS(0)*4, len(batches), 64))
    for _, batch := range batches {
        g.Go(func() error {
            return b.executePageRead(ctx, batch)
        })
    }
    if err := g.Wait(); err != nil {
        // ctx 取消或 tree closed → 返回错误，调用方不应依赖 results
        return nil, err
    }

    return results, nil
}
```

### 2.7 错误处理策略

| 场景 | 行为 | 原因 |
|------|------|------|
| key 不存在 | `results[i] = nil` | 批量语义：单 key 失败不中断整批 |
| key 已 tombstone | `results[i] = nil` | tombstone = 已删除 |
| searchPath ErrRetry | 重试一次 → 仍失败则 `results[i] = nil` | 瞬态错误（mid-split），重试大概率成功 |
| MVCC 解码失败 | `results[i] = nil` | 数据损坏或格式不匹配，不中断整批 |
| GetLeafPage 失败 | `results[i] = nil` | 瞬态 page 类型变更，不中断整批 |
| ctx 取消 | 返回 `ctx.Err()`，**中断整批** | 调用方主动取消，不应返回部分结果 |
| tree 已关闭 | 返回 `ErrTreeClosed`，**中断整批** | 全局状态变更，后续读不安全 |

**设计原则**：单 key 失败不影响其他 key。只有全局状态变更（ctx cancel / tree close）才中断整批。

**⚠️ 注意事项**：
- `Get(key)` 返回 `ErrKeyNotFound`，但 `GetBatch(keys)` 返回 `nil`。这是 API 层面的不对称，调用方务必检查 `results[i] == nil`。
- 静默跳过的错误（ErrRetry 重试后仍失败、MVCC 解码失败、GetLeafPage 失败）通过 `GlobalTracer.LogOp` 记录，便于排查。
- `ErrTreeClosed` 的检测依赖 `GetBatch` 入口的 `checkOpen()` + EpochManager 的 page 回收保护。`searchPath` 本身不返回 `ErrTreeClosed`——它返回 `ErrRetry` 或 `ErrBTreeSearchError`。若 epoch 未启用且 tree 在 batch 执行中被关闭，极端情况下已关闭 tree 的读错误会被静默为 per-key nil。V1 接受此风险（epoch 在生产环境中默认启用）。

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

### Phase 1：GetBatch 核心（1-2 天）

- [ ] `readTask` / `pageReadBatch` 数据结构
- [ ] `executePageRead`：单 Page 顺序读 + epoch 保护 + searchPath 复用
- [ ] `groupKeysByPage`：从 PageDispatcher 抽取公共 Phase 1-3 逻辑
- [ ] `GetBatch` 主流程：hash → resolve → errgroup → collect

### Phase 2：SetBatch 对接（0.5 天）

- [ ] BTree 新增 `batchWriter` + `batchWriterOnce` 字段
- [ ] `getBatchWriter()` 懒初始化
- [ ] `SetBatch` 实现（KVPair → keys/values → WriteBatch）
- [ ] `Close()` 中 Shutdown BatchWriter

### Phase 3：测试（1 天）

- [ ] GetBatch 单元测试：
  - 空 keys
  - 单 key
  - 全部存在
  - 部分缺失
  - tombstone key
  - 跨 Page 分布
  - context cancel
- [ ] SetBatch 单元测试：
  - 空 pairs
  - 单 pair
  - 大批量（1K/10K/100K）
  - 部分失败 → BatchError
- [ ] GetBatch + SetBatch 并发安全性（`-race`）
- [ ] Benchmark：单 key Get vs GetBatch(1K keys)

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
| `GetBatch_1K` vs `Get×1K` | 串行 Get 循环 | >2x throughput |
| `GetBatch_10K` vs `Get×10K` | 串行 Get 循环 | >3x throughput |
| `SetBatch_10K` vs `Set×10K` | 串行 Set 循环 | >5x throughput (已有 PageDispatcher) |

### 7.3 竞态检测

```bash
go test -v -race -run "TestGetBatch|TestSetBatch" ./internal/infrastructure/storage/btree/
```

---

## 8. 附录：Phase 2 实现细节

### 8.1 resolveKeysToPages（GetBatch 专用）

GetBatch 的 Phase 2 无法直接复用 `PageDispatcher.resolveShardPageIDs`（返回 `writeTask` 包含 value），因此需要自己的 resolve 逻辑。为保持实现内聚，直接在 BTree 上定义：

```go
// resolveKeysToPages 将 shard 分桶后的 keys 映射到 PageID。
// 内部并行：每个非空 shard 一个 goroutine，桶内排序后 resolve。
func (b *BTree) resolveKeysToPages(ctx context.Context, shards [][]keyWithIndex) (
    map[model.PageID][]keyWithIndex, error) {

    shardResults := make([]map[model.PageID][]keyWithIndex, numShards)
    shardErr := make([]error, numShards)
    var wg sync.WaitGroup

    for s := range numShards {
        if len(shards[s]) == 0 {
            continue
        }
        wg.Add(1)
        go func(shardIdx int) {
            defer wg.Done()
            shardResults[shardIdx], shardErr[shardIdx] = b.resolveShardKeys(ctx, shards[shardIdx])
        }(s)
    }
    wg.Wait()

    for _, err := range shardErr {
        if err != nil {
            return nil, err
        }
    }

    // 跨桶 merge
    result := make(map[model.PageID][]keyWithIndex)
    for _, sr := range shardResults {
        for pid, keys := range sr {
            result[pid] = append(result[pid], keys...)
        }
    }
    return result, nil
}

// resolveShardKeys 桶内排序 + 顺序 resolve PageID。
func (b *BTree) resolveShardKeys(ctx context.Context, keys []keyWithIndex) (
    map[model.PageID][]keyWithIndex, error) {

    sort.Slice(keys, func(i, j int) bool {
        return string(keys[i].key) < string(keys[j].key)
    })

    result := make(map[model.PageID][]keyWithIndex)
    var lastPage model.PageID
    for _, k := range keys {
        var pid model.PageID
        if lastPage != 0 && b.inSamePage(lastPage, k.key) {
            pid = lastPage
        } else {
            var err error
            pid, err = b.ResolvePageID(ctx, k.key)
            if err != nil {
                return nil, err
            }
            lastPage = pid
        }
        result[pid] = append(result[pid], k)
    }
    return result, nil
}
```

与 `PageDispatcher.resolveShardPageIDs` 的核心区别：
- 返回 `map[PageID][]keyWithIndex`（纯 key），而非 `map[PageID][]writeTask`（key + value）
- 复用相同的算法骨架（排序 → 遍历 → inSamePage → ResolvePageID）

**决策**：V1 不抽取公共函数。两段代码 ~40 行，类型不同（`keyWithIndex` vs `writeTask`），强行统一需要泛型/接口。等两者稳定后再评估抽取收益。`keyWithIndex` 已在 `page_dispatcher.go` 中定义，`GetBatch` 直接复用。

---

## 9. 参考

- [PageDispatcher 设计文档](../../10_benchmark/2026-05-25-par-put-stability/2026-05-25-page-dispatcher-design.md) — Hash 分桶、WorkerPool、CAS 重试策略
- [BTree 存储引擎设计](../../02_design/03_存储引擎设计.md)
- [Lealone AOSE 异步 Page 调度](https://github.com/codefollower/My-Blog/issues/22) — Page→线程绑定思想来源
- `internal/infrastructure/storage/btree/batch_writer.go` — 已实现的 BatchWriter
- `internal/infrastructure/storage/btree/page_dispatcher.go` — 已实现的 PageDispatcher
