# Epoch-based Page Reclamation Spike

> 创建日期：2026-05-21
> 前置：BTree COW 架构（Phase 5）+ Tombstone 补全
> 状态：Planning

---

## 一、问题定义

### 1.1 现状：COW 页面泄漏

BTree COW 写路径每次 Set/Delete 都分配新物理页面，旧页面从未回收：

```
Set(key, val):
  oldLeaf = GetLeafPage(leafRef.pInfo.pageID)   // pageID = N
  newLeaf = AllocLeafPage()                       // pageID = N+1 (COW copy)
  newLeaf.Update(idx, val)
  leafRef.CAS(oldInfo → newInfo)                  // 原子切换到新页
  // ★ 旧页面 N 永远不会被 FreePage → 泄漏 4KB/op
```

**后果**：
- Benchmark 写测试 OOM：`131,072 pages used / 131,072 total`
- 生产环境长时间运行后页面池耗尽
- 每个 Set 泄漏 1 页（4KB），每个 Delete 同上

### 1.2 为什么不能 CAS 后立即 FreePage

```go
if leafRef.CAS(oldInfo, newInfo) {
    b.storage.FreePage(oldInfo.PageID)  // ← 竞态！
}
```

**竞态时序**：

```
T0: reader: searchPath → GetPageInfo(leafRef) → oldInfo (pageID=N)
T1: writer: CAS(oldInfo → newInfo) ✓
T2: writer: FreePage(N)              ← N 回收到 free list
T3: writer2: AllocLeafPage() → N     ← N 被重用
T4: writer2: InitLeafPage(N) → count=0
T5: reader: GetLeafPage(N)           ← 读到 count=0 → PANIC
```

---

## 二、方案设计

### 2.1 mmap 页面池模型

NexKV 使用固定大小的 mmap 页面池（~512MB），`uint32 pageID` 直接映射到内存地址：

```
pageID = uint32 → base + pageID * 4096 → unsafe.Pointer → 直接读写
```

核心约束：
- 地址空间有界（131,072 页），必须逐页复用
- COW 旧页必须 `FreePage` → `freeList` → `Alloc` 回收
- 回收时机必须安全：不能有 reader 仍持有旧 pageID 的引用

### 2.2 适配策略：Page-level Epoch-based Reclamation

需要一个 **延迟释放机制** 来解决 1.2 节的竞态问题。

```
┌──────────────────────────────────────────────────────────────┐
│  CAS 成功后不立即 FreePage(oldPageID)                        │
│  而是加入 EpochManager.deferFreeList[globalEpoch]            │
│  仅当所有使用该 epoch 的 reader 都退出后，才真正 FreePage    │
└──────────────────────────────────────────────────────────────┘
```

### 2.3 EpochManager 设计

```go
// btree/epoch.go

type EpochManager struct {
    global    atomic.Uint64          // 全局 epoch（每写递增）
    readers   [64]atomic.Uint64      // per-CPU reader epoch 登记
    deferred  []deferredBatch        // 按 epoch 分组的待释放页
    mu        sync.Mutex             // 保护 deferred drain
    freeFunc  func(model.PageID)     // storage.FreePage
}

type deferredBatch struct {
    epoch   uint64
    pageIDs []model.PageID
}
```

### 2.4 写路径集成

```go
// operations.go — writeOperation CAS 成功路径
if leafRef.CAS(oldInfo, newInfo) {
    if b.epochMgr != nil {
        b.epochMgr.deferFree(oldInfo.PageID)
    }
    path.ReleaseAll()
    b.size.Add(result.delta)
    return nil
}
```

### 2.5 读路径集成

```go
// search.go — searchPath
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
    slot := cpuID() % 64
    em.readers[slot].Store(em.global.Load())      // 注册
    defer em.readers[slot].Store(0)                // 退出
    // ... 现有搜索逻辑 ...
}
```

### 2.6 安全回收

```go
func (em *EpochManager) tryReclaim() {
    safeEpoch := em.computeSafeEpoch()  // min(所有活跃 reader epoch)
    for i, batch := range em.deferred {
        if batch.epoch < safeEpoch {
            for _, pageID := range batch.pageIDs {
                em.freeFunc(pageID)     // ★ 安全：无 reader 引用此 epoch
            }
            em.deferred[i].pageIDs = nil
        }
    }
}
```

### 2.7 与现有 ChunkCompactor 的关系

Phase 4.4 实现的 `ChunkCompactor` 处理 **AO 文件级别** 的空间回收（重写低填充 .ao 文件）。EpochManager 处理 **mmap 页面池** 的 COW 旧页回收。两者互补：

| | ChunkCompactor | EpochManager |
|---|---|---|
| 层级 | .ao 文件 (Chunk) | mmap 页面池 (Page) |
| 触发 | Checkpoint 后异步 | 后台定时 / 容量阈值（不在读写热路径） |
| 回收目标 | removedPages → 低填充 Chunk | COW 旧页 → FreePage |
| 安全保证 | compacting 标记 + 写屏障 | epoch-based 延迟释放 |

### 2.8 Epoch 时序精确定义

#### 2.8.1 核心设计决策：tryReclaim 不在热路径

`tryReclaim()` 涉及 `globalEpoch` 递增、快照所有 reader slot、遍历 retired 列表、调用 `FreePage`——这些操作**不应当在读写热路径中执行**。

热路径仅保留最小化的不可省略操作：

| 路径 | 操作 | 开销 | 是否可省略 |
|------|------|------|-----------|
| 读路径 | `EnterRead(slot)` | 1× atomic Load + Store | 不可省略（安全性基础） |
| 读路径 | `ExitRead(slot)` | 1× atomic Store | 不可省略 |
| 写路径 | `Retire(pageID)` | per-slot slice append (无锁) | 不可省略（记录旧页供回收） |
| 写路径 | `tryReclaim()` | epoch 递增 + 快照 + FreePage | **省略——异步/定时触发** |

#### 2.8.2 EpochManager 修订设计

```go
// btree/epoch.go

const (
    epochInit       = 1    // epoch 从 1 开始，0 = reader inactive
    maxReaderSlots  = 64
)

type EpochManager struct {
    globalEpoch atomic.Uint64              // 全局 epoch，仅在 tryReclaim 锁内递增
    readers     [maxReaderSlots]atomic.Uint64 // per-slot reader epoch，0 = inactive
    retired     [maxReaderSlots][]retiredPage  // per-slot 无锁追加
    reclaimMu   sync.Mutex                 // 仅保护 tryReclaim
    freeFn      func(model.PageID)         // storage.FreePage
}

type retiredPage struct {
    pageID model.PageID
    epoch  uint64
}
```

**关键语义**：
- `globalEpoch` **不在写路径递增**——仅在 `tryReclaim()` 的 `reclaimMu` 临界区内递增
- `Retire(pageID)` 时用**当前 `globalEpoch` 的快照**标记旧页（不递增）
- `tryReclaim()` 先递增 `globalEpoch`，再快照全部 reader slot，计算 safeEpoch

#### 2.8.3 读路径：EnterRead / ExitRead

```go
// EnterRead 必须在任何 pInfo.Load() 之前调用（search.go:searchPath 开头）
func (em *EpochManager) EnterRead(slot int) {
    epoch := em.globalEpoch.Load()   // LoadAcquire 语义
    em.readers[slot].Store(epoch)    // StoreRelease 语义
}

// ExitRead 在 searchPath 完全退出时调用（defer）
func (em *EpochManager) ExitRead(slot int) {
    em.readers[slot].Store(0)
}
```

**searchPath 集成**：

```go
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
    slot := cpuID() % maxReaderSlots
    em.EnterRead(slot)          // ★ 必须在 pInfo.Load() 之前
    defer em.ExitRead(slot)     // ★ 必须在函数完全退出后

    // ... 现有搜索逻辑不变 ...
}
```

**安全性论证**（发生时序）：

```
Reader R                          Writer W
──────────                        ──────────
T1: EnterRead(slot)              T4: CAS(oldInfo→newInfo) ✓
    globalEpoch.Load()→E             (full barrier: LOCK CMPXCHG)
    readers[slot]=E
                                  T5: Retire(oldPageID)
T2: pInfo = leafRef.GetPageInfo()     retired[slot].append({oldPageID, E})
    → oldInfo (PageID=N)              (globalEpoch 仍是 E)
    → 或 newInfo (PageID=N+1)
                                  ... 异步触发 ...
T3: GetLeafPage(N)                T6: tryReclaim():
    (读旧页内容)                       Lock
                                      globalEpoch.Add(1) → E+1
T3': ExitRead(slot)                   快照 readers → {slot: E}
    readers[slot]=0                   safeEpoch = E
                                      retiredPage{oldPageID, epoch=E}
                                      E < E → false → 不回收 ✓
                                      Unlock
```

**结论**：reader 注册的 epoch E ≤ writer retire 的 epoch（writer 在 CAS 后读取 globalEpoch，而 reader 在 CAS 前注册）。`retireEpoch < safeEpoch` 严格不等保证旧页不被回收。

#### 2.8.4 写路径：Retire (仅追加，不回收)

```go
// Retire 在 CAS 成功后调用，仅追加记录（无锁、O(1)），不触发回收
func (em *EpochManager) Retire(slot int, pageID model.PageID) {
    epoch := em.globalEpoch.Load()
    em.retired[slot] = append(em.retired[slot], retiredPage{pageID, epoch})
}
```

**writeOperation 集成**（最小化热路径改动）：

```go
// operations.go — CAS 成功块
if leafRef.CAS(oldInfo, newInfo) {
    if b.epochMgr != nil {
        slot := cpuID() % maxReaderSlots
        b.epochMgr.Retire(slot, oldInfo.PageID) // 仅追加，不触发 tryReclaim
    }
    path.ReleaseAll()
    b.size.Add(result.delta)
    return nil
}
```

**热路径开销**：1× `globalEpoch.Load()` + 1× slice append（通常无 allocation）。

#### 2.8.5 tryReclaim：异步触发，不在读写路径

```go
// tryReclaim 由后台 goroutine 或定时器触发，不在任何热路径中调用
func (em *EpochManager) tryReclaim() {
    em.reclaimMu.Lock()
    defer em.reclaimMu.Unlock()

    // Step 1: 推进 epoch ——所有此后注册的 reader 看到新 epoch
    newEpoch := em.globalEpoch.Add(1)

    // Step 2: 快照所有 reader slot
    minActive := uint64(math.MaxUint64)
    for i := range em.readers {
        if e := em.readers[i].Load(); e != 0 && e < minActive {
            minActive = e
        }
    }

    // Step 3: 计算 safeEpoch
    var safeEpoch uint64
    if minActive == math.MaxUint64 {
        safeEpoch = newEpoch // 无活跃 reader，全部安全
    } else {
        safeEpoch = minActive
    }

    // Step 4: 收集可回收页面（锁内 swap retired 列表）
    var toFree []model.PageID
    for i := range em.retired {
        list := em.retired[i]
        em.retired[i] = nil
        for _, p := range list {
            if p.epoch < safeEpoch {
                toFree = append(toFree, p.pageID)
            } else {
                em.retired[i] = append(em.retired[i], p) // 未安全，放回
            }
        }
    }
    // Step 5: 锁外 FreePage（不阻塞其他操作）
    em.reclaimMu.Unlock()
    for _, pageID := range toFree {
        em.freeFn(pageID)
    }
    em.reclaimMu.Lock()
}
```

**触发策略**（完全不在读写热路径）：

| 触发方式 | 描述 |
|---------|------|
| 后台 goroutine 定时 | 每 100ms tick，确保 epoch 持续推进 |
| Checkpoint 后 | 持久化完成后回收，此时无 pending 写操作 |
| retired 容量阈值 | retired 积累 > 阈值时发信号触发 |

```go
// 后台回收 goroutine（在 BTree 初始化时启动）
func (em *EpochManager) StartBackgroundReclaim(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                em.tryReclaim()
            case <-ctx.Done():
                em.tryReclaim() // 停止前最终回收
                return
            }
        }
    }()
}

// Checkpoint 后显式触发
func (b *BTree) AfterCheckpoint() {
    if b.epochMgr != nil {
        b.epochMgr.tryReclaim()
    }
}
```

**核心原则**：写路径仅 `Retire()`（slice append，无锁，O(1)）。`tryReclaim()` 的所有操作（epoch 递增 + 快照 reader + FreePage）都在热路径之外。

### 2.9 与现有 PageRef/Compaction 的交互

#### 2.9.1 页面可见性分类与回收策略

按页面是否对 reader 可见，分为三类：

| 类别 | 场景 | 示例 | 回收方式 |
|------|------|------|---------|
| **P-page**（已发布） | CAS 替换的旧 PageInfo.PageID | `writeOperation` CAS 成功后的 `oldInfo.PageID` | `Retire()` → epoch 延迟 |
| **F-page**（未发布） | CAS 失败时需清理的新页面 | `writeOperation` CAS 失败后的 `result.newPageID` | 立即 `FreePage` |
| **O-page**（孤儿页） | Split 中 double-COW 替换页 | `handleLeafSplit` 中的 `orphanPageID` | 立即 `FreePage` |

**F-page 和 O-page 永远不需要 epoch 延迟释放**——它们从未通过 CAS 发布到 PageRef.pInfo 中，无 reader 可引用。

#### 2.9.2 PageRef.Release / freeFunc 分析

**关键发现**：已发布到 children cache 中的 PageRef 的 `refCount` 基础值（创建时 Retain=1）在树正常运行期间**永远不会被 Release**。原因是：

1. searchPath 遍历：Retain(+1) → 使用 → ReleaseAll(-1)，回到基础值 1
2. Children cache 更新：旧 cache 的 PageRef 由 Go GC 回收，**Go GC 不调用 refCount 逻辑，也不调用 freeFunc**
3. 仅有未发布的临时 PageRef（CAS 失败 cleanup 路径）会 Release 到 0

**结论**：`freeFunc` 在当前代码中对已发布 PageRef 实际上是死代码。物理页回收完全依赖 EpochManager 在 CAS 点的 `Retire()` 调用。不对 PageRef.Release 做修改，保留其 cleanup 语义。

#### 2.9.3 Compaction 路径的 epoch 集成

`compactPageWithParent()` (compaction.go:152-248) 有两个需要集成的点：

**Phase E (leafRef CAS)**：

```go
// compaction.go — Phase E 成功
if leafRef.CAS(compactingInfo, finalPI) {
    if b.epochMgr != nil {
        b.epochMgr.Retire(slot, oldPI.PageID) // 旧叶子页 → P-page
    }
    cleanup = false
}
```

**Phase D (parentRef CAS, best-effort)**：

```go
// compaction.go — Phase D 成功
if parentRef.CAS(parentPI, newParPI) {
    if b.epochMgr != nil {
        b.epochMgr.Retire(slot, parentPI.PageID) // 旧父节点页 → P-page
    }
}
```

Compaction **不需要 EnterRead/ExitRead**——compaction 本身是 writer，通过 `NodeCompacting` 状态阻止并发写操作。

#### 2.9.4 Split 路径的 O-page 保持立即释放

```go
// operations.go — handleLeafSplit
orphanPageID = leftPage.PageID() // double-COW 替换的 Split 产出页
_ = b.storage.FreePage(orphanPageID) // ★ 保持立即释放，不改为 Retire
```

**安全性论证**：`orphanPageID` 来自 `leaf.Split()` 的产出，仅在当前 goroutine 栈上存在，**从未通过任何 PageRef.pInfo 发布**。无 reader 可引用它 → 立即 FreePage 安全。

#### 2.9.5 集成点总览

```
                    ┌──────────────────────────────────────┐
                    │         EpochManager 集成点           │
                    └──────────────────────────────────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        │                            │                            │
   【读路径】                    【写路径】                   【Compaction】
   searchPath                   writeOperation               compactPageWithParent
        │                            │                            │
   EnterRead(slot)              CAS 成功:                    Phase E CAS 成功:
   ... pInfo.Load() ...         Retire(oldPageID)            Retire(oldPI.PageID)
   ExitRead(slot)               (不触发 tryReclaim)          (不触发 tryReclaim)
                                                                  │
                                          ┌───────────────────────┘
                                          │
                              【异步回收】tryReclaim()
                              后台 goroutine / Checkpoint 后
                              - 递增 globalEpoch
                              - 快照 reader slots
                              - 计算 safeEpoch
                              - 回收 retired epoch < safeEpoch
```

#### 2.9.6 EpochManager 与 BTree 生命周期

```go
// btree.go — BTree 增加字段
type BTree struct {
    // ... 现有字段 ...
    epochMgr  *EpochManager    // COW 旧页延迟回收（Phase 5.x）
    epochCtx  context.CancelFunc
}

// NewBTree 时创建并启动后台回收
func newBTreeWithConfig(storage *OffheapBTreeStorage, cfg *btreeConfig) (*BTree, error) {
    // ... 现有初始化 ...
    em := &EpochManager{
        freeFn: func(pageID model.PageID) { _ = storage.FreePage(pageID) },
    }
    em.globalEpoch.Store(epochInit) // epoch 从 1 开始

    ctx, cancel := context.WithCancel(context.Background())
    em.StartBackgroundReclaim(ctx, 100*time.Millisecond)

    bt := &BTree{
        // ... 现有字段 ...
        epochMgr: em,
        epochCtx: cancel,
    }
    return bt, nil
}

// Close 时停止后台回收并最终回收一次
func (b *BTree) Close() error {
    if !b.closed.CompareAndSwap(false, true) {
        return nil
    }
    if b.epochCtx != nil {
        b.epochCtx()
        b.epochMgr.tryReclaim() // 最终回收
    }
    return b.storage.Close()
}
```

**注意**：初始实现中 EpochManager 为可选（`epochMgr` 可为 nil），通过 `NewBTree` 的 Option 控制是否启用，确保向后兼容测试。

---

## 三、实现计划

### Step 1: EpochManager 骨架

```go
// btree/epoch.go — 新文件 (~120 行)
// EpochManager + deferredBatch + EnterRead/ExitRead + deferFree + tryReclaim
```

### Step 2: searchPath 集成 reader epoch

```go
// search.go — register reader slot before traversal, unregister on return
```

### Step 3: writeOperation 集成 deferFree

```go
// operations.go — CAS 成功后 deferFree(oldPageID) + tryReclaim()
```

### Step 4: 测试验证

- `TestEpochManager_DeferAndReclaim`: 基础延迟释放/回收
- `TestEpochManager_SafeEpoch`: reader 未退出 → 不回收
- `TestCOWWrite_NoPageLeak`: 基准测试不再 OOM
- `TestConcurrentReadWrite_EpochSafety`: epoch 竞态安全

---

## 四、风险与缓解

| 风险 | 缓解 |
|------|------|
| per-reader slot 增加 2 atomic ops | per-CPU slot 避免 cache line 竞争；2 个 atomic 是 epoch 方案的最小开销 |
| ~~epoch 长时间不推进 → 延迟页堆积~~ | tryReclaim 由后台 goroutine 定时触发（100ms），不受写入频率影响 |
| reader 长时间持有 → 阻塞 safeEpoch 推进 | reader timeout（如 5s），超时视为退出；或监控 max reader hold time |
| 与 refCount 机制重叠 | 已分析确认：PageRef.freeFunc 对已发布 PageRef 是死代码（Go GC 回收）；epoch 管理 P-page，cleanup 路径保持立即 FreePage（F/O-page） |
| retired 列表内存增长 | 每个 retiredPage 占 ~12 字节，10^6 个 ≈ 12MB；tryReclaim 定期清理可回收页面 |
| uint64 epoch 溢出（理论） | 1GHz 递增需 ~584 年；初始实现不处理溢出，epoch=0 保留为 inactive 标记 |

### 4.1 设计约束

| 约束 | 说明 |
|------|------|
| **tryReclaim 不在热路径** | 触发方式为后台定时 + Checkpoint 后，读写热路径仅做 Retire (slice append) |
| **读路径 Epoch 不可省略** | EnterRead/ExitRead 是 epoch 安全性的基础，2 atomic ops 是下限 |
| **写路径仅追加** | Retire(pageID) 是 per-slot slice append（无锁），不调用 tryReclaim |

---

## 五、参考

- BTree 路线图: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md`
- Tombstone 缺口分析: `docs/07_spike/btree-refactor/2026-05-20-btree-delete-tombstone-gaps.md`

---

**文档版本**：v2.0
**状态**：Planning
