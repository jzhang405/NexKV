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
// btree.go — Get() 读入口
func (b *BTree) Get(_ context.Context, key []byte) ([]byte, error) {
    slot := b.epochMgr.AllocSlot()
    b.epochMgr.EnterRead(slot)              // 注册
    defer b.epochMgr.ExitRead(slot)          // 退出
    path, _ := searchPath(b.rootRef, key)   // ... 现有搜索逻辑 ...
    // GetLeafPage + Search + GetValue
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
| 写路径 | `Retire(pageID)` | per-slot Mutex + slice append | 不可省略（记录旧页供回收） |
| 写路径 | `tryReclaim()` | epoch 递增 + 快照 + FreePage | **省略——异步/定时触发** |

#### 2.8.2 EpochManager 修订设计

```go
// btree/epoch.go

const (
    epochInit       = 1    // epoch 从 1 开始，0 = reader inactive
    maxReaderSlots  = 64
)

type EpochManager struct {
    globalEpoch atomic.Uint64              // 全局 epoch，仅在 tryReclaim 内递增
    readers     [maxReaderSlots]atomic.Uint64 // per-slot reader epoch，0 = inactive
    slots       [maxReaderSlots]epochSlot     // per-slot retired 列表，各自 Mutex 保护
    freeFn      func(model.PageID)            // storage.FreePage
}

type epochSlot struct {
    mu   sync.Mutex      // 保护 list 的并发读写（Retire + tryReclaim）
    list []retiredPage
}

type retiredPage struct {
    pageID model.PageID
    epoch  uint64
}
```

**关键语义**：
- `globalEpoch` **不在写路径递增**——仅在 `tryReclaim()` 内递增
- `Retire(pageID)` 时对**单个 slot 加锁**（per-slot Mutex，粒度细，无跨 slot 竞争），用当前 `globalEpoch` 标记旧页
- `tryReclaim()` 按顺序遍历所有 slot，逐个 Lock → swap list → Unlock，收集后可回收页面
- per-slot Mutex 消除 slice append 与 swap nil 之间的 data race（原方案中无锁 append + 全局锁 swap 存在 slice header 并发读写）

**并发模型**：

```
Retire(slot, pageID):                  tryReclaim():
  slots[slot].mu.Lock()                  for each slot:
  slots[slot].list = append(...)           slots[i].mu.Lock()
  slots[slot].mu.Unlock()                  list := slots[i].list
                                           slots[i].list = nil
                                           slots[i].mu.Unlock()
                                           filter list into toFree
                                         freeFn(toFree...)  // 锁外
```

#### 2.8.3 读路径：EnterRead / ExitRead

**关键设计决策**：epoch 保护窗口必须覆盖**完整的读操作**（searchPath 遍历 + 叶子页面数据读取），而非仅 searchPath 内部。

原因：`searchPath` 返回后，调用者（`Get()`/`getRawBytes()`）继续通过 `pInfo.PageID` 访问 mmap 页面数据。若 epoch 在 searchPath 返回时退出，writer 可能在调用者读数据期间回收该页面。

```go
// EnterRead 必须在 searchPath 之前调用
func (em *EpochManager) EnterRead(slot int) {
    epoch := em.globalEpoch.Load()
    em.readers[slot].Store(epoch)
    if em.globalEpoch.Load() != epoch {
        em.readers[slot].Store(em.globalEpoch.Load())
    }
}

// ExitRead 在整个读操作完成后调用（defer）
func (em *EpochManager) ExitRead(slot int) {
    em.readers[slot].Store(0)
}
```

**Get() 集成**（epoch 覆盖 searchPath + GetLeafPage + GetValue 全程）：

```go
// btree.go — Get
func (b *BTree) Get(_ context.Context, key []byte) ([]byte, error) {
    if err := b.checkOpen(); err != nil { return nil, err }

    var slot int
    if b.epochMgr != nil {
        slot = b.epochMgr.AllocSlot()
        b.epochMgr.EnterRead(slot)
        defer b.epochMgr.ExitRead(slot)
    }

    path, err := searchPath(b.rootRef, key)
    // ... searchPath 内部无 epoch 注册 ...
    if err != nil { return nil, err }
    defer path.ReleaseAll()

    pInfo := path.Leaf().Ref.GetPageInfo()
    leaf, _ := b.storage.GetLeafPage(pInfo.PageID)
    idx, found := leaf.Search(key)
    raw := leaf.GetValue(idx)
    // ... MVCC decode ...
    return mvccVal.RealVal, nil
    // defer ExitRead(slot) 在此执行
}
```

> **注意**：`searchPath` 本身**不再**注册/退出 epoch。epoch 保护窗口由调用者（`Get`、`getRawBytes`、`writeOperation` 中的 `searchPath`）负责。

**slot 分配**：使用 atomic 轮询分配 + 栈变量保存，避免 goroutine 跨 P 迁移导致 EnterRead/ExitRead 写入不同 slot：

```go
// EpochManager
var nextSlot atomic.Uint64

func (em *EpochManager) AllocSlot() int {
    return int(nextSlot.Add(1) % maxReaderSlots)
}
```

调用者在函数入口调用 `AllocSlot()` 获取 slot，保存为栈变量，EnterRead/ExitRead/Retire 均使用该变量。goroutine 迁移不影响正确性——同一个操作的 slot 始终一致。

**安全性论证**（发生时序）：

```
Reader R (Get)                    Writer W
──────────                        ──────────
T1: slot = AllocSlot()
T2: EnterRead(slot)
    epoch = globalEpoch.Load()→E
    readers[slot]=E               T5: CAS(oldInfo→newInfo) ✓
    ★ 双检: globalEpoch==E ✓          (full barrier: LOCK CMPXCHG)
                                  T6: Retire(oldPageID)
T3: path = searchPath(...)            slots[slot].append({oldPageID, E})
    pInfo = leafRef.GetPageInfo()     (globalEpoch 仍是 E)
    → oldInfo (PageID=N)
                                  ... 异步触发 ...
T4: GetLeafPage(N)                T7: tryReclaim():
    leaf.Search(key)                  遍历 slots:
    leaf.GetValue(idx)                  slots[i].mu.Lock()
    (读旧页内容)                        list := slots[i].list
    // ... 完整读操作结束 ...            slots[i].list = nil
T4': ExitRead(slot)                    slots[i].mu.Unlock()
    readers[slot]=0                    过滤: {oldPageID, epoch=E}
                                       safeEpoch = min(活跃 readers) = E
                                       E < E → false → 不回收 ✓
```

**结论**：epoch 保护窗口覆盖完整读操作（searchPath + GetLeafPage + Search + GetValue），T4 中的页面数据访问在 epoch 保护内。`retireEpoch < safeEpoch` 严格不等保证旧页不被回收。

#### 2.8.4 写路径：Retire (per-slot Mutex，仅追加，不回收)

```go
// Retire 在 CAS 成功后调用，per-slot Mutex 保护 slice append
func (em *EpochManager) Retire(slot int, pageID model.PageID) {
    epoch := em.globalEpoch.Load()            // 快照当前 epoch
    s := &em.slots[slot]
    s.mu.Lock()
    s.list = append(s.list, retiredPage{pageID, epoch})
    s.mu.Unlock()
}
```

**writeOperation 集成**（最小化热路径改动）：

```go
// operations.go — writeOperation CAS 成功路径
var slot int
if b.epochMgr != nil {
    slot = b.epochMgr.AllocSlot()           // atomic 轮询分配
}
// ... searchPath + mutate ...
if !leafRef.CAS(oldInfo, newInfo) {
    _ = b.storage.FreePage(result.newPageID) // F-page 立即释放
    path.ReleaseAll()
    continue
}
if b.epochMgr != nil {
    b.epochMgr.Retire(slot, oldInfo.PageID)  // P-page 延迟释放
}
path.ReleaseAll()
b.size.Add(result.delta)
return nil
```

**热路径开销**：1× `atomic.Add` (AllocSlot) + 1× `globalEpoch.Load()` + 1× per-slot Mutex Lock/Unlock + 1× slice append。

per-slot Mutex 竞争中极低：`AllocSlot` 通过 atomic 轮询将 goroutine 均匀分散到 64 个 slot。不同 slot 完全无竞争。

#### 2.8.5 tryReclaim：异步触发，不在读写路径

```go
// tryReclaim 由后台 goroutine 或定时器触发，不在任何热路径中调用
func (em *EpochManager) tryReclaim() {
    // Step 1: 推进 epoch ——所有此后注册的 reader 看到新 epoch
    newEpoch := em.globalEpoch.Add(1)

    // Step 2: 快照所有 reader slot（无锁——每个 slot 是 atomic.Uint64）
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

    // Step 4: 遍历所有 slot，逐个 Lock → swap list → Unlock，收集可回收页面
    var toFree []model.PageID
    for i := range em.slots {
        s := &em.slots[i]
        s.mu.Lock()
        list := s.list
        s.list = nil
        s.mu.Unlock()

        // 批量收集：暂存不安全页，最后一次性写回（O(1) Lock 而非 O(n)）
        var unsafe []retiredPage
        for _, p := range list {
            if p.epoch < safeEpoch {
                toFree = append(toFree, p.pageID)
            } else {
                unsafe = append(unsafe, p)
            }
        }
        if len(unsafe) > 0 {
            s.mu.Lock()
            s.list = append(s.list, unsafe...)
            s.mu.Unlock()
        }
    }

    // Step 5: 锁外 FreePage（safeEpoch 已在 Step 3 确定，锁外 Free 安全）
    for _, pageID := range toFree {
        em.freeFn(pageID)
    }
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
    em.wg.Add(1)
    go func() {
        defer em.wg.Done()
        defer func() {
            if r := recover(); r != nil {
                // 记录 panic 堆栈，避免进程崩溃
                // log.Error("epoch background reclaim panic", zap.Any("panic", r), zap.Stack("stack"))
            }
        }()
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                em.tryReclaim()
            case <-ctx.Done():
                // ★ 不在此执行 tryReclaim ——最终回收由 Close() 的调用者负责
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

> **注意**：`ctx.Done()` 分支**不再执行 `tryReclaim()`**。最终回收由 `Close()` 主线程在 `wg.Wait()` 后单独执行（见 2.9.6）。避免双线程并发 FreePage。

**核心原则**：
- 写路径仅 `Retire()`（per-slot Mutex + slice append）。64 个 slot 使 Mutex 竞争近似为零
- `tryReclaim()` 的所有操作（epoch 递增 + 快照 reader + 遍历 slot + FreePage）都在热路径之外
- 无全局锁：Retire 和 tryReclaim 只在同 slot 上互斥（per-slot Mutex），不同 slot 完全并行
- SafeEpoch 在 Step 3 确定后不再变化，Step 5 锁外 FreePage 安全

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

#### 2.9.4 Split 路径：O-page 保持立即释放 + P-page 集成点

**O-page（orphan）保持立即释放**：

```go
// operations.go — handleLeafSplit
orphanPageID = leftPage.PageID() // double-COW 替换的 Split 产出页
_ = b.storage.FreePage(orphanPageID) // ★ 保持立即释放，不改为 Retire
```

**安全性论证**：`orphanPageID` 来自 `leaf.Split()` 的产出，仅在当前 goroutine 栈上存在，从未通过任何 PageRef.pInfo 发布。

**新增 P-page 集成点**（Agent 审查发现遗漏）：

```go
// handleInternalSplit — grandparentRef CAS 成功后
if grandparentRef.CAS(grandparentInfo, newGrandparentInfo) {
    if b.epochMgr != nil {
        b.epochMgr.Retire(slot, grandparentInfo.PageID) // ★ 旧祖父节点页 → P-page
    }
    // ... children cache 更新 ...
}

// handleRootInternalSplit — ReplaceRoot CAS 成功后
if b.rootRef.ReplaceRoot(rootInfo, newRootInfo, newChildren) {
    if b.epochMgr != nil {
        b.epochMgr.Retire(slot, rootInfo.PageID) // ★ 旧根节点页 → P-page
    }
    // ... children cache 更新 ...
}

// handleInternalSplit — redirectInfo CAS 后（best-effort）
redirectInfo := &PageInfo{PageID: currentInfo.PageID, NodeState: NodeRedirect, ...}
if currentRef.CAS(currentInfo, redirectInfo) {
    if b.epochMgr != nil {
        b.epochMgr.Retire(slot, currentInfo.PageID) // ★ 旧内部节点页 → P-page
    }
}
```

> **注意**：`handleLeafSplit` 中的 redirectInfo CAS（operations.go:820）同样产生 P-page。该 CAS 发生在 leafRef 上，`leafInfo.PageID` 是 split 前叶子页的物理 ID，需要 Retire。

#### 2.9.5 集成点总览

```
                     ┌──────────────────────────────────────┐
                     │         EpochManager 集成点           │
                     └──────────────────────────────────────┘
                                      │
         ┌────────────────┬───────────┼───────────┬────────────────┐
         │                │           │           │                │
    【读路径】         【写路径】   【Split】   【Compaction】     【Merge】
    Get/getRawBytes  writeOperation  handle*Split compactPage     handle*Merge
         │                │           │           │                │
    AllocSlot()         AllocSlot()  AllocSlot()  AllocSlot()     AllocSlot()
    EnterRead(slot)                  Retire:      Retire:         Retire:
    searchPath()        CAS 成功:    grandparent  oldPI.PageID    left/right
    GetLeafPage()       Retire(      ReplaceRoot  parentPI.PageID old pages
    leaf.Search()         oldPageID) redirect CAS   (best-effort)  (best-effort)
    leaf.GetValue()      (不回收)      (不回收)      (不回收)
    ExitRead(slot)
         │
    ★ epoch 覆盖完整读操作（searchPath + 页面数据访问）
         │
         └──────────────────────────────────────────────┘
                              │
                  【异步回收】tryReclaim()
                  后台 goroutine / Checkpoint 后
                  - 递增 globalEpoch
                  - 快照 reader slots → 计算 safeEpoch
                  - 遍历 slots → 回收 retired epoch < safeEpoch
                  - 锁外 FreePage
```

#### 2.9.6 EpochManager 与 BTree 生命周期

```go
// btree.go — BTree 增加字段
type BTree struct {
    // ... 现有字段 ...
    epochMgr  *EpochManager    // COW 旧页延迟回收（Phase 5.x）
    epochCtx  context.CancelFunc
}

type EpochManager struct {
    // ... 现有字段 ...
    wg sync.WaitGroup          // 等待后台 goroutine 退出
}

// NewBTree 时创建并启动后台回收
func newBTreeWithConfig(storage *OffheapBTreeStorage, cfg *btreeConfig) (*BTree, error) {
    // ... 现有初始化 ...
    em := &EpochManager{
        freeFn: func(pageID model.PageID) { _ = storage.FreePage(pageID) },
    }
    em.globalEpoch.Store(epochInit)

    ctx, cancel := context.WithCancel(context.Background())
    em.StartBackgroundReclaim(ctx)

    bt := &BTree{
        // ... 现有字段 ...
        epochMgr: em,
        epochCtx: cancel,
    }
    return bt, nil
}

// Close 保证先等待后台 goroutine 退出，再最终回收，最后关闭 storage
func (b *BTree) Close() error {
    if !b.closed.CompareAndSwap(false, true) {
        return nil
    }
    if b.epochCtx != nil {
        b.epochCtx()               // 1. 发取消信号
        b.epochMgr.wg.Wait()       // 2. 等待后台 goroutine 退出（不再执行 tryReclaim）
    }
    if b.epochMgr != nil {
        b.epochMgr.tryReclaim()    // 3. 最终回收（单线程，无并发）
    }
    return b.storage.Close()       // 4. 关闭 mmap
}
```

**Close 竞态分析**：
- Step 1 `cancel()` 通知后台 goroutine 退出
- Step 2 `wg.Wait()` 保证后台 goroutine 已完全退出，**不会再有并发的 FreePage 调用**
- Step 3 `tryReclaim()` 在单线程中执行最终回收
- Step 4 `storage.Close()` 时已无任何 FreePage 调用
- 消除双线程同时 FreePage 的 data race 和 use-after-close

---

## 三、实现计划

### Step 1: EpochManager 骨架 (~200 行)

```go
// btree/epoch.go — 新文件
// EpochManager + epochSlot + retiredPage
// AllocSlot / EnterRead(双检) / ExitRead / Retire(per-slot Mutex) / tryReclaim(批量put-back)
// StartBackgroundReclaim(WaitGroup+recover) / Close 同步
```

### Step 2: 读路径集成

```go
// btree.go — Get() / getRawBytes() 入口
// AllocSlot → EnterRead → searchPath → GetLeafPage → ... → defer ExitRead
// ★ epoch 覆盖完整读操作，非仅 searchPath
```

### Step 3: 写路径集成

```go
// operations.go — writeOperation CAS 成功路径
// AllocSlot → ... → CAS 成功 → Retire(slot, oldInfo.PageID)
// CAS 失败 → 立即 FreePage(result.newPageID) (F-page)
```

### Step 4: Split / Compaction / Merge 路径集成

```go
// operations.go — handleInternalSplit / handleRootInternalSplit / redirect CAS
// compaction.go — compactPageWithParent Phase D/E
// merge_ops.go — handleLeafMerge 左右旧页
// ★ 所有 CAS 成功点均调用 Retire
```

### Step 5: BTree 生命周期集成

```go
// btree.go — NewBTree: 创建 EpochManager + StartBackgroundReclaim
// Close: cancel → wg.Wait() → tryReclaim() → storage.Close()
```

### Step 6: 测试验证

- `TestEpochManager_DeferAndReclaim`: 基础延迟释放/回收
- `TestEpochManager_SafeEpoch`: reader 未退出 → 不回收
- `TestEpochManager_MultiEpoch`: 多 reader 不同 epoch → 仅回收 < min 的页面
- `TestEpochManager_SlotRace`: tryReclaim swap 与 Retire append 同 slot 并发
- `TestCOWWrite_NoPageLeak`: 基准测试不再 OOM
- `TestConcurrentReadWrite_EpochSafety`: epoch 竞态安全
- `TestClose_DrainRace`: BTree.Close() 与后台 goroutine 无竞态

---

## 四、风险与缓解

| 风险 | 缓解 |
|------|------|
| per-reader slot 增加 2 atomic ops + 1 atomic AllocSlot | 轮询分配 + 栈变量保存 slot；2+1 个 atomic 是 epoch 方案的最小开销 |
| ~~epoch 长时间不推进 → 延迟页堆积~~ | tryReclaim 由后台 goroutine 定时触发（100ms），不受写入频率影响 |
| ~~reader 长时间持有 → 阻塞 safeEpoch 推进~~ | **不是真实风险**：epoch 保护窗口 = searchPath + GetLeafPage + GetValue（纯内存，微秒级）。无长时间 I/O，无跨操作持有。不存在"5s timeout"机制——强制超时等价于 use-after-free |
| 长事务（批量读）+ 高写入 → 物理页池耗尽 | 100K writes/s × 1 页/写 = 400MB/s，512MB 池在 ~1.3s 耗尽。缓解：(1) `AllocPage` 饥饿检测——freeList 水位 < 20% 时触发紧急 tryReclaim；(2) 写入限流 |
| 与 refCount 机制重叠 | 已分析确认：PageRef.freeFunc 对已发布 PageRef 是死代码（Go GC 回收）；epoch 管理 P-page，cleanup 路径保持立即 FreePage（F/O-page） |
| retired 列表内存增长 | 每个 retiredPage 占 ~12 字节，10^6 个 ≈ 12MB；tryReclaim 定期清理可回收页面。per-slot Mutex 防止 swap 与 append 的 data race |
| uint64 epoch 溢出（理论） | 1GHz 递增需 ~584 年；初始实现不处理溢出，epoch=0 保留为 inactive 标记 |
| 后台 goroutine panic 致进程崩溃 | `defer recover()` + 日志记录；panic 后 goroutine 退出但不影响 epoch 推进（Checkpoint 后 `AfterCheckpoint` 仍触发 tryReclaim） |

### 4.1 设计约束

| 约束 | 说明 |
|------|------|
| **tryReclaim 不在热路径** | 触发方式为后台定时 + Checkpoint 后，读写热路径仅做 Retire (slice append) |
| **读路径 Epoch 不可省略** | EnterRead/ExitRead 是 epoch 安全性的基础，2 atomic ops 是下限 |
| **写路径仅追加** | Retire(pageID) 是 per-slot Mutex + slice append，64 slot 使竞争近似为零，不调用 tryReclaim |

---

## 五、参考

- BTree 路线图: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md`
- Tombstone 缺口分析: `docs/07_spike/btree-refactor/2026-05-20-btree-delete-tombstone-gaps.md`

---

## 六、最终评审结论

> 评审日期：2026-05-21
> 评审方式：多 Agent 并发安全审查 + 反面论证 + 边界情况审计 + 实现就绪度评估
> 审查轮次：3 轮（初版 → 修复 3 缺陷 → 第二轮审查 → 修复 8 缺陷）

### 6.1 已修复缺陷清单

| # | 来源 | 缺陷 | 严重程度 | 修复方式 |
|---|------|------|---------|---------|
| C1 | Agent-1 | epoch 保护窗口仅覆盖 searchPath，未覆盖 GetLeafPage/数据读取 | CRITICAL | EnterRead/ExitRead 移到 Get/getRawBytes 层级 |
| C2 | Agent-2 | BTree.Close() 与后台 goroutine 双线程 FreePage + use-after-close | CRITICAL | WaitGroup + ctx.Done 不执行 tryReclaim |
| C3 | Agent-3 | "reader timeout 5s 视为退出"是虚假安全承诺（tryReclaim 无 timeout 逻辑） | CRITICAL | 删除 false claim，改为准确描述 |
| H1 | Agent-1 | handleInternalSplit grandparentRef CAS 遗漏 Retire | HIGH | 新增集成点 |
| H2 | Agent-1 | handleRootInternalSplit ReplaceRoot 遗漏 Retire | HIGH | 新增集成点 |
| H3 | Agent-2 | cpuID() 无 Go 实现 + goroutine 迁移导致 slot 不一致 | HIGH | atomic 轮询 AllocSlot + 栈变量保存 |
| H4 | Agent-3 | 后台 goroutine panic 无 recover → 进程崩溃 | HIGH | defer recover + 日志 |
| H5 | Agent-3 | 长事务+高写入 → 物理页池 1.3s 耗尽（文档仅评估 retired 元数据增长） | HIGH | 风险表补充物理页耗尽分析 + 饥饿检测 |

### 6.2 当前正确性状态

所有已知竞态窗口已封死：

| 竞态窗口 | 防护机制 | 判定 |
|---------|---------|------|
| Reader Load→Store 之间 epoch 推进 | EnterRead 双检 | ✓ |
| Reader 引用旧页期间 Writer CAS + Free | `retireEpoch < safeEpoch` 严格不等 | ✓ |
| Retire append 与 tryReclaim swap 并发 | per-slot Mutex 隔离 | ✓ |
| P-page / F-page / O-page 分类回收 | 仅 CAS 发布后的旧页走 epoch 延迟 | ✓ |
| Close 与后台 goroutine 竞态 | WaitGroup + 单线程最终回收 | ✓ |
| Compaction / Split / InternalSplit CAS 回收 | 全部 CAS 成功点均有 Retire | ✓ |
| epoch 覆盖完整读操作 | Get 级 EnterRead/ExitRead | ✓ |
| goroutine 迁移导致 slot 不一致 | AllocSlot 栈变量保存 | ✓ |

### 6.3 方案选型：Epoch vs RefCount

Epoch 在 BTree COW 高频写、海量小页更迭场景下**完胜** RefCount（全局批量回收吞吐更高、热点更稳、无 per-page 自旋）。

### 6.4 64-Slot 设计

使用 `atomic` 轮询分配（非 `cpuID()`），64 slot 超冗余隔离，Mutex 竞争趋近于 0。

### 6.5 状态

✅ 方案已通过 3 轮多 Agent 审查，所有 CRITICAL/HIGH 缺陷已修复。**可进入实现阶段**。

---

**文档版本**：v4.0
**状态**：Approved
