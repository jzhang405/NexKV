# `BTree.mu` 锁分析修正报告

**日期**: 2026-03-30
**关联文档**: `lock-contention-analysis.md` (第 305-343 行)
**核心结论**: **`BTree.mu` 不存在，Get/Set 路径无全局读写锁**

---

## 一、错误纠正

### 1.1 原报告的错误声明

原报告 `lock-contention-analysis.md` 第 305 行：

```
| 锁 #1 | BTree.mu | sync.RWMutex | btree.go:83 | rootRef 读写、Get/Set/Delete 全操作 | 每次 Set/Get 必经 | 🔴 极高 |
```

以及第 325-343 行声称：

> `BTree.Get()` 获取 RLock (btree.go:95)
> `BTree.Set()` 获取 RLock (btree.go:102-103)
> `BTree.Delete()` 获取 RLock (btree.go:116-117)

**以上全部错误。**

### 1.2 事实

`BTree` 结构体（`btree.go:160-204`）**没有 `mu sync.RWMutex` 字段**：

```go
// btree.go:160-204 — BTree 实际字段
type BTree struct {
    config    *model.BTreeConfig
    cowConfig *COWDeltaRefConfig
    closed    bool
    closedMu  sync.RWMutex          // ← 保护 closed 标志，不是 Get/Set 路径

    ctx        context.Context
    cancelFunc context.CancelFunc

    rootRef *RootPageRef             // ← 原子指针，无锁

    chunkMgr    *ChunkManager
    wal         wal.WAL
    offheapPM   *offheap.PageManager
    offheapAdapter *OffHeapAdapter
    pageRefCache   *PageRefCache     // ← 内部有自己的 mu（缓存锁，非 BTree 锁）

    maxLevels int
    enableWAL bool
    nextPageID atomic.Uint64
    writeMu sync.Mutex               // ← 持久化专用
    stats    *PageStats
    scheduler *concurrency.TaskScheduler
    splitMuMap sync.Map              // ← 分裂协调
    epochBasedFreeList *EpochBasedFreeList
}
```

原报告引用的 `btree.go:83` 是 `PageRefCache.mu`（`PageRefCache` 结构体的字段），不是 `BTree.mu`。

---

## 二、Get/Set 的真实锁路径

### 2.1 Get 路径 — 完全无全局锁

```
BTree.Get()                              [btree.go:524]
  └── findLeafPageRef()                  [search_path.go:277]
        └── searchPathWithRefs()         [search_path.go:163]
              ├── b.rootRef.pInfo.Load()     ← atomic load，无锁
              ├── for each level:
              │     ├── offheapAdapter.SearchChild()  ← 无锁
              │     └── pageRefCache.GetOrCreate()    ← RLock（缓存 map）
              └── Search leaf for key          ← 无锁
```

**唯一涉及的锁**：`PageRefCache.mu.RLock()` — 这是 Go map 的线程安全保护，不是 BTree 数据锁。

### 2.2 Set 路径 — Leaf-Level Locking，无全局锁

```
BTree.Set()                                          [btree.go:639]
  └── SetWithRetryAndQueue()                          [btree_ops.go:176]
        └── setWithLeafLock()                         [leaf_lock_set.go:20]
              ├── findLeafPageRef()                   ← 同 Get 路径
              │     └── pageRefCache.GetOrCreate()    ← RLock
              ├── pageLock.TryLock()                  ← atomic CAS（叶子级）
              ├── InsertToOffHeap()                   ← 无锁
              ├── leafRef.ReplacePage(old, new)       ← atomic CAS
              ├── pageLock.Unlock()
              └── [分裂路径]:
                    ├── splitMuMap                    ← 分裂协调锁
                    ├── EpochBasedFreeList.mu          ← 页面释放锁
                    └── PageRefCache.Replace()         ← Lock（缓存更新）
```

**核心机制**：
- **叶子级独占**：`PageLock.TryLock()` (atomic CAS)，不是全局锁
- **原子替换**：`PageRef.ReplacePage()` (atomic CAS)，不是 mutex
- **Root 更新**：`RootPageRef.ReplacePage()` (atomic CAS)，不是 mutex

### 2.3 Root 指针更新 — 无锁

```go
// root_page_ref.go:55-91
func (r *RootPageRef) ReplacePage(oldRootID uint64, newInfo *PageInfo) bool {
    for {
        currentPtr := r.pInfo.Load()           // atomic load
        if r.pInfo.CompareAndSwap(currentPtr, newInfo) {  // atomic CAS
            return true
        }
        // CAS 失败重试
    }
}
```

Root 切换完全依赖 `atomic.Pointer.CompareAndSwap`，不使用任何 mutex。

---

## 三、CCOW 架构中"锁"的真实含义

### 3.1 层次化锁模型

```
┌─────────────────────────────────────────────────┐
│                BTree 数据层                       │
│                                                   │
│  Root Pointer  ← atomic CAS（无锁）               │
│  Internal Page ← atomic CAS（无锁）               │
│  Leaf Page     ← PageLock (atomic CAS) + CAS      │
│                                                   │
├─────────────────────────────────────────────────┤
│                基础设施层                          │
│                                                   │
│  PageRefCache  ← RWMutex（Go map 线程安全）       │
│  FreeList      ← Mutex（页面释放管理）             │
│  SplitCoord    ← sync.Map（分裂协调）             │
│  closedMu      ← RWMutex（生命周期管理）           │
└─────────────────────────────────────────────────┘
```

### 3.2 各"锁"的真实性质

| 锁 | 层次 | 性质 | CCOW 必需？ |
|----|------|------|-------------|
| `PageRef.pInfo` (atomic CAS) | 数据层 | CCOW 核心 — 原子版本切换 | ✅ 必需 |
| `RootPageRef.pInfo` (atomic CAS) | 数据层 | CCOW 核心 — Root 原子切换 | ✅ 必需 |
| `PageLock` (atomic CAS) | 数据层 | 乐观并发 — 叶子级独占 | ⚠️ 多线程写才需要 |
| `PageRefCache.mu` | 基础设施 | Go map 并发保护 | ✅ 必需（Go map 非并发安全） |
| `EpochBasedFreeList.mu` | 基础设施 | 延迟释放管理 | ✅ CCOW 必需 |
| `splitMuMap` | 数据层 | 分裂协调 | ⚠️ 多线程写才需要 |
| `writeMu` | 基础设施 | 持久化序列化 | ⚠️ 仅持久化模式 |

### 3.3 CCOW 核心路径确实是无锁的

真正的 CCOW 数据操作链：

```
读取 rootRef.pInfo.Load()     → atomic，无锁
遍历 InternalPage.children    → atomic pointer 追踪，无锁
读取 PageRef.pInfo.Load()     → atomic，无锁
写入 PageRef.ReplacePage()    → atomic CAS，无锁
切换 Root CompareAndSwap()    → atomic CAS，无锁
```

**所有 BTree 数据的读写都通过 atomic 操作完成，不经过任何 mutex。**

---

## 四、为什么会有"似乎有锁"的误解

### 4.1 PageRefCache.mu 是最大的混淆源

`PageRefCache.mu` 在 `btree.go:83` 定义，紧邻 `BTree` 结构体之前：

```go
// btree.go:79-84
type PageRefCache struct {       // ← 与 BTree 在同一文件
    cache map[model.PageID]*PageRef
    mu    sync.RWMutex           // ← 这个 mu 容易被误认为 BTree.mu
}

// btree.go:160-204
type BTree struct {              // ← 没有自己的 mu
    pageRefCache *PageRefCache   // ← 持有 PageRefCache 的引用
}
```

原报告将 `PageRefCache.mu` 误标为 `BTree.mu`，并错误声称 Get/Set 获取此锁。

### 4.2 PageRefCache.mu 的真实开销

`PageRefCache.mu` 只保护 `map[PageID]*PageRef` 这个 Go map 的线程安全：

```go
func (c *PageRefCache) GetOrCreate(pageID model.PageID, isLeaf bool) *PageRef {
    c.mu.RLock()             // ← 保护 map 读取
    ref, ok := c.cache[pageID]
    c.mu.RUnlock()
    // ...
}
```

**这不是保护 BTree 数据**，而是保护 Go 语言的 map 数据结构。因为 Go map 不是并发安全的，读取也需要加锁。

### 4.3 开销评估

| 操作 | PageRefCache.mu 获取次数 | 说明 |
|------|------------------------|------|
| Get (单次) | ~3 次 RLock | 树深度层级的缓存查找 |
| Set (单次成功) | ~3 次 RLock + 1 次 Lock | 查找 + Replace |
| Set (重试) | ~3 次 RLock/次 | 每次重试重复查找 |

CPU Profile 中 `sync.(*RWMutex).RLock` 占 1.41% — 这是**基础设施锁**的开销，不是 CCOW 架构的锁。

---

## 五、修正后的锁竞争分析

### 5.1 修正前 vs 修正后

| 维度 | 原报告（错误） | 修正后 |
|------|--------------|--------|
| **Get 有全局锁？** | 是（BTree.mu RLock） | **否**，完全无全局锁 |
| **Set 有全局锁？** | 是（BTree.mu RLock） | **否**，使用 Leaf-Level atomic CAS |
| **Root 更新有锁？** | 是（BTree.mu Lock） | **否**，使用 atomic CAS |
| **锁竞争根源** | BTree.mu 全局读写锁 | PageLock TryLock 竞争 + 调度风暴 |

### 5.2 真实的锁竞争排名

| 排名 | 锁 | 竞争级别 | CPU 占比 |
|------|-----|---------|---------|
| 1 | `PageLock` TryLock → ErrRetry → Gosched | 🔴 极高 | ~28%（调度开销） |
| 2 | `EpochBasedFreeList.mu` | 🔴 高 | 分裂路径频繁获取 |
| 3 | `PageRefCache.mu` RLock | 🟡 中 | 1.41% |
| 4 | `PageLock.mu + cond` | 🟡 中 | notesleep 8.76% |
| 5 | `splitMuMap` | 🟡 中 | 分裂场景 |

### 5.3 核心瓶颈修正

**原报告的核心瓶颈**："BTree.mu 全局读写锁"

**修正后的核心瓶颈**：`PageLock.TryLock` 失败 → `ErrRetry` → `runtime.Gosched()` → 调度风暴

这是**两个完全不同的问题**：
- 原报告认为问题在于"全局锁序列化了所有操作"
- 实际问题在于"乐观锁失败导致调度风暴"

---

## 六、设计正确性确认

### 6.1 CCOW 架构实现是否正确？

**是的，CCOW 核心路径正确实现了无锁设计：**

1. **读路径完全无锁** ✅
   - `rootRef.pInfo.Load()` → atomic
   - `PageRef.pInfo.Load()` → atomic
   - 不修改任何共享状态

2. **写路径使用原子操作** ✅
   - `PageLock.TryLock()` → atomic CAS
   - `PageRef.ReplacePage()` → atomic CAS
   - `RootPageRef.ReplacePage()` → atomic CAS

3. **旧版本延迟释放** ✅
   - `EpochBasedFreeList` 管理
   - `scheduleDelayedRelease` 100ms 延迟

### 6.2 问题不在 CCOW 设计，而在并发策略

| 设计点 | 正确性 | 性能 |
|--------|--------|------|
| CCOW 数据路径 | ✅ 正确 | ✅ 无锁 |
| Leaf-Level Locking | ✅ 正确 | ⚠️ TryLock 竞争 |
| 重试策略 | ⚠️ 可工作 | ❌ Gosched 风暴 |
| TaskScheduler fallback | ⚠️ 可工作 | ❌ 单线程串行化 |

---

## 七、结论

| 问题 | 答案 |
|------|------|
| `BTree.mu` 存在吗？ | **不存在**，`BTree` 结构体没有 `mu` 字段 |
| Get 有全局锁吗？ | **没有**，完全依赖 atomic 操作 |
| Set 有全局锁吗？ | **没有**，使用 Leaf-Level atomic CAS |
| 原报告为什么错？ | 将 `PageRefCache.mu`（btree.go:83）误认为 `BTree.mu` |
| CCOW 设计正确吗？ | **正确**，数据层完全无锁 |
| 性能瓶颈是什么？ | PageLock TryLock 竞争 → ErrRetry → Gosched 调度风暴 |
| 优化方向？ | 增加自旋、或改为单写线程队列 |
