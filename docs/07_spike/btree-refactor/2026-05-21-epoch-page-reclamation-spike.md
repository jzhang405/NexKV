# Epoch-based Page Reclamation Spike

> 创建日期：2026-05-21
> 前置：BTree COW 架构（Phase 5）+ Tombstone 补全
> 参考：H2 MVStore / Lealone ChunkManager
> 状态：Planning

---

## 一、问题定义

### 1.1 现状：COW 页面泄漏

BTree 的 COW 写路径每次 Set/Delete 都分配新物理页面：

```
Set(key, val):
  oldLeaf = GetLeafPage(leafRef.pInfo.pageID)   // pageID = N
  newLeaf = AllocLeafPage()                       // pageID = N+1 (COW copy)
  newLeaf.Update(idx, val)
  leafRef.CAS(oldInfo → newInfo)                  // atomically switch to page N+1
  // ★ 旧页面 N 永远不会被回收
```

**后果**：
- Benchmark 写测试 OOM（131,072 页 / 512MB 池耗尽）
- 生产环境长时间运行后所有可写页面耗尽
- 每个 Set 操作泄漏一页（4KB），删除操作泄漏更严重（COW + 旧页）

### 1.2 为什么不能直接 FreePage

```go
// 看似正确的做法（实际不安全）：
if leafRef.CAS(oldInfo, newInfo) {
    b.storage.FreePage(oldInfo.PageID)  // ← 竞态！
}
```

**竞态窗口**：

```
Writer Goroutine              Reader Goroutine
─────────────────────         ─────────────────────
CAS(oldInfo → newInfo) ✓
                              path = searchPath(root, key)
                              path[0].Ref → root (retain)
                              path[1].Ref → parent (retain)
                              path[2].Ref → leaf (retain) ← 持有 leafRef
FreePage(oldInfo.PageID)      leaf = GetLeafPage(path[2].Ref.pInfo.PageID)
                              // ★ PageInfo 已改为 newInfo，但如果 reader 在 CAS 之前
                              //    读取了 oldInfo，就会访问已释放的 oldInfo.PageID
```

**Reader 时序**：

```
T0: reader 调用 leafRef.GetPageInfo() → oldInfo (pageID=N)
T1: writer CAS(oldInfo → newInfo)
T2: writer FreePage(N)              ← N 回到 free list
T3: AllocLeafPage() → pageID=N      ← N 被另一个 writer 分配
T4: InitLeafPage(N) → count=0
T5: reader 调用 GetLeafPage(N)      ← 读到 count=0 的页面 → PANIC
```

---

## 二、Lealone/H2 MVStore 方案

### 2.1 核心思想：Version-based Delayed Reclamation

```
┌──────────────────────────────────────────────────────────────┐
│  Page 不直接被 Free，而是加入 DelayedFreeList[version]        │
│  当且仅当所有使用 version 的 reader 都退出后，才真正 Free    │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 H2 MVStore 关键数据结构

```java
// FileStore.java — H2 MVStore
class FileStore {
    // 全局已删除页面队列，按 version 排序
    PriorityBlockingQueue<RemovedPageInfo> removedPages;

    // 处理已删除页面：version < 指定值时，回收对应 chunk 空间
    void acceptChunkOccupancyChanges(long time, long version) {
        while (true) {
            RemovedPageInfo rpi = removedPages.peek();
            if (rpi == null || rpi.version >= version) break;
            rpi = removedPages.poll();

            Chunk chunk = chunks.get(getPageChunkId(rpi.pos));
            chunk.accountForRemovedPage(
                rpi.pageNo, rpi.pageLength, rpi.isPinned, time
            );
            if (chunk.getLivePageCount() == 0) {
                freeChunks.add(chunk);  // 整块 free
            }
        }
    }
}

// MVStore.java
class MVStore {
    // 每次写操作递增 store version
    // 旧版本页面关联到此 version，等 version 过期后回收
    volatile long currentVersion;

    void accountForRemovedPage(long pos, long version, boolean pinned, int pageNo) {
        fileStore.accountForRemovedPage(pos, version, pinned, pageNo);
    }
}
```

### 2.3 关键流程

```
Write Path:
  1. COW alloc newPage
  2. CAS leafRef → newPage
  3. accountForRemovedPage(oldPos, currentVersion, pinned, pageNo)
     → 加入 removedPages[version] 队列
     → oldPage 不是立即 free，而是"标记为待回收"

Read Path:
  1. 获取当前 store version → readerVersion
  2. searchPath → retain page refs
  3. 完成读操作
  4. 释放 readerVersion（递减引用计数）

GC Path (acceptChunkOccupancyChanges):
  1. 计算 safe version = min(所有活跃 reader 的 version)
  2. 处理 removedPages 中 version < safe version 的条目
  3. 递减对应 chunk 的 livePageCount
  4. livePageCount == 0 的 chunk → free
```

### 2.4 Chunk 级别 vs Page 级别

H2 MVStore 在 **Chunk 级别** 做空间回收，而非逐页：

```
Chunk (典型 64MB):
  ┌─────────────────────────────────┐
  │ Header (chunkID, version, ...)  │
  │ Page 0 (leaf)                   │
  │ Page 1 (internal)               │
  │ Page 2 (leaf)                   │
  │ ...                             │
  │ Page N-1                        │
  └─────────────────────────────────┘

- livePageCount: 当前 chunk 中仍被引用的页面数
- 写操作：新版本页面写入新 chunk，旧 chunk 的 livePageCount--
- 当 livePageCount == 0：整个 chunk 可被回收/重用
```

NexKV 当前是 **mmap offheap 页面池**，不做 chunk 管理。这意味着需要 **Page 级别的 epoch-based reclamation**。

---

## 三、NexKV 适配方案

### 3.1 核心设计

```
┌────────────────────────────────────────────────────────────────┐
│                        EpochManager                            │
│                                                                │
│  globalEpoch    atomic.Uint64      // 全局写 epoch（单调递增） │
│  activeReaders  [N]atomic.Uint64   // per-core reader epochs  │
│  pendingFrees   sync.Map[epoch→[]PageID]  // 待回收页面        │
└────────────────────────────────────────────────────────────────┘

Writer:                            Reader:
  epoch = globalEpoch.Add(1)         myEpoch = globalEpoch.Load()
  ... COW ...                        activeReaders[cpu].Store(myEpoch)
  CAS ...                            ... searchPath + read ...
  pendingFrees.Store(epoch, oldID)   activeReaders[cpu].Store(0)
  tryReclaim()
```

### 3.2 数据结构

```go
// internal/infrastructure/storage/btree/epoch.go

type EpochManager struct {
    globalEpoch    atomic.Uint64
    activeReaders  []atomic.Uint64    // per-goroutine or per-core
    pendingFrees   []pendingFreeBatch // ring buffer by epoch
    freeFunc       func(model.PageID) // storage.FreePage
    mu             sync.Mutex         // protects pendingFrees drain
}

type pendingFreeBatch struct {
    epoch  uint64
    pages  []model.PageID
}

const maxPendingBatches = 64  // max concurrent epochs tracked
```

### 3.3 写路径改造

```go
// operations.go — writeOperation 成功路径
if leafRef.CAS(oldInfo, newInfo) {
    // Phase 6.5: 将旧页面加入延迟回收队列
    epoch := b.epochMgr.EnterWrite()
    b.epochMgr.DeferFree(epoch, oldInfo.PageID)
    b.epochMgr.ExitWrite(epoch)

    path.ReleaseAll()
    b.size.Add(result.delta)
    return nil
}
```

### 3.4 读路径改造

```go
// search.go — searchPath
func searchPath(rootRef *RootPageRef, key []byte, epochMgr *EpochManager) (SearchPath, error) {
    epoch := epochMgr.EnterRead()      // 注册 reader epoch
    defer epochMgr.ExitRead(epoch)     // 退出时释放

    // ... 现有 searchPath 逻辑 ...
}
```

### 3.5 回收触发

```go
// epoch.go
func (em *EpochManager) tryReclaim() {
    safeEpoch := em.computeSafeEpoch()   // min(所有活跃 reader 的 epoch)

    em.mu.Lock()
    defer em.mu.Unlock()

    for _, batch := range em.pendingFrees {
        if batch.epoch < safeEpoch {
            for _, pageID := range batch.pages {
                em.freeFunc(pageID)      // ★ 安全释放
            }
            batch.pages = nil            // 清除
        }
    }
}

func (em *EpochManager) computeSafeEpoch() uint64 {
    minEpoch := uint64(math.MaxUint64)
    for _, r := range em.activeReaders {
        if e := r.Load(); e > 0 && e < minEpoch {
            minEpoch = e
        }
    }
    if minEpoch == math.MaxUint64 {
        return em.globalEpoch.Load()  // 无活跃 reader
    }
    return minEpoch
}
```

### 3.6 简化版：Lealone 风格的 RefCount + Version

Lealone 实际上用的是更简单的方案：**页面级引用计数 + 版本号延迟释放**。

```go
// page_info.go — 扩展现有 PageInfo
type PageInfo struct {
    PageID    model.PageID
    Version   uint64
    IsLeaf    bool
    NodeState NodeState
    ChunkPos  model.ChunkPosition
    Redirect  bool
    NewRef    *PageRef
    // ★ Phase 6.5 新增
    RefCount  int32  // 活跃引用计数（读者 + 缓存引用）
}
```

**关键区别**：NexKV 已有 `PageRef.refCount`（通过 `Retain()/Release()` 维护），但 `freeFunc` 仅在 `refCount==0` 时触发。当前的问题是：**CAS 成功后，旧 pageID 的 PageRef 仍被 cache path 持有，refCount 永远不到 0**。

### 3.7 最简方案：Epoch-based Free List（对齐 NexKV 当前架构）

```
核心思路：
  CAS 成功后不 FreePage(oldPageID)，而是将 oldPageID + epoch 加入 deferFreeList。
  TryReclaim 检查：epoch 是否 < safeEpoch（所有活跃 reader 看到的版本号之后）。
  如果是 → FreePage(oldPageID)；否则延迟到下次 reclaim。
```

**Simplification over H2 MVStore**：
- 不需要 Chunk 概念（NexKV 是 mmap 页面池）
- 不需要单独的 FileStore（页面管理在 PageManager 中）
- 只需要一个延迟释放队列 + epoch 追踪

---

## 四、实现计划

### Step 1: EpochManager

```go
// btree/epoch.go — 新文件 (~120 行)

type EpochManager struct {
    global    atomic.Uint64
    readers   [64]atomic.Uint64  // per-cpu reader epoch (64 = max CPUs)
    deferred  []deferredFree
    mu        sync.Mutex
    freeFunc  func(model.PageID)
}

type deferredFree struct {
    epoch   uint64
    pageIDs []model.PageID
}
```

### Step 2: 集成 writeOperation

```go
// operations.go — CAS 成功路径改造
if leafRef.CAS(oldInfo, newInfo) {
    em.deferFree(oldInfo.PageID)
    em.tryReclaim()
    // ... 现有逻辑 ...
}
```

### Step 3: 集成 searchPath

```go
// search.go — 注册 reader epoch
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
    slot := cpuID() % 64
    em.readers[slot].Store(em.global.Load())
    defer em.readers[slot].Store(0)
    // ... 现有逻辑 ...
}
```

### Step 4: 测试验证

- `TestEpochManager_DeferAndReclaim`: 延迟释放 + 回收
- `TestEpochManager_SafeEpoch`: reader 未退出时不回收
- `TestCOWWrite_NoPageLeak`: 写 benchmark 无 OOM
- `TestConcurrentReadWrite_NoDataCorruption`: epoch 竞态安全

---

## 五、风险与缓解

| 风险 | 缓解 |
|------|------|
| per-reader epoch 有 CPU 开销 | 用 per-CPU slot 避免 cache line 竞争；读路径增加 ~2 atomic ops |
| deferFreeList 无限增长 | 每次 write 后 tryReclaim；定期强制 GC |
| reader 长时间不退出 → 阻塞回收 | 设置 reader timeout（如 5s），超时视为退出 |
| 与现有 PageRef.refCount 冲突 | PageRef.Release 中的 freeFunc 仅处理"引用计为 0"的场景；epoch-based free 是正交的延迟释放 |
| 与 merge/compaction 路径的 FreePage 交互 | merge 中的 FreePage 保持不变（页面不再被引用时立即释放）；epoch free 只用于写路径的 COW 旧页 |

---

## 六、参考

- H2 MVStore FileStore.java: `acceptChunkOccupancyChanges`, `removedPages` queue
- H2 MVStore MVStore.java: `accountForRemovedPage`, `currentVersion`
- Lealone BTree 深度分析: `docs/07_spike/btree-refactor/2026-04-01-lealone-btree-deep-dive.md`
- NexKV btree2 roadmap: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md`
- BTree tombstone 缺口分析: `docs/07_spike/btree-refactor/2026-05-20-btree-delete-tombstone-gaps.md`

---

**文档版本**：v1.0
**状态**：Planning
