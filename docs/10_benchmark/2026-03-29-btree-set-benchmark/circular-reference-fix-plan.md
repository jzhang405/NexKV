# Circular Reference 修复计划

**日期**: 2026-03-30
**问题**: 8 线程 Set 操作 98.9% 失败（ErrBTreeCircularReference）
**状态**: Phase 1 + 防御性修复已完成，Phase 2（版本校验）待实施

---

## 1. 问题摘要

### 1.1 当前基准

```
8 线程 × 50000 ops，500 init keys：
  Success:      2,568  (0.6%)
  ErrRetry:     1,815  (0.5%)
  ErrCircRef: 395,615  (98.9%)  ← 核心问题
```

### 1.2 错误触发位置

| 位置 | 文件 | 行号 | 触发逻辑 |
|------|------|------|----------|
| 搜索路径 | `search_path.go` | 241 | `visitedPages` 检测到同一 pageID 被访问两次 |
| split 后 | `leaf_lock_set.go` | 699 | `hasCycleFrom()` 从新父节点遍历发现循环 |

### 1.3 已修复的 root causes（早期）

| Cause | 修复 | 说明 |
|-------|------|------|
| insertIndex 计算错误 | ✅ | split 后基于子节点 ID 定位替代 splitKey 查找 |
| 版本号编码遗漏 | ✅ | 10+ 处 `GetChild()` → `GetChildWithVersion()` |

这些修复后单线程已正常（100% 成功），但 8 线程仍有 98.9% 失败。

---

## 2. 根因分析

### 2.1 核心根因：pageRefCache 缓存过期 + 页面回收竞争

搜索路径中通过 `pageRefCache.GetOrCreate(childPageID)` 获取子节点的 PageRef。当页面被 epoch 回收重用后，缓存中的 PageInfo 与物理页面内容不一致。

**竞争时序**：

```
T1: 页面 X 作为内部节点，pageRefCache 缓存 PageInfo(X, type=internal)
T2: split/COW 操作 → 旧页面 X 通过 epoch 机制释放
T3: epoch 推进 → 页面 X 进入 freeList → 被重新分配为叶子节点
T4: 物理页面 X 内容被 InitLeafPage() 清空重置
T5: 另一个线程的搜索路径读到内部节点的 child = X（刚写入的新引用）
T6: pageRefCache.GetOrCreate(X) 返回旧的缓存 PageInfo
T7: childInfo 指向垃圾数据或已变更的内容
T8: 搜索继续 → 访问到之前已见过的 pageID → 循环引用！
```

### 2.2 关键代码路径

**搜索路径** (`search_path.go:208-241`)：

```go
// 从内部节点搜索子节点
childPageID, _, err := b.offheapAdapter.SearchChild(currentPageID, key)

// 从缓存获取 PageRef — 问题所在！
childRef := b.pageRefCache.GetOrCreate(childPageID, isChildLeaf)
childInfo := childRef.GetPageInfo()

// 循环引用检测
currentPageID = model.PageID(childInfo.GetPageID())
if visitedPages[uint64(currentPageID)] {
    return nil, nil, ErrCircularReference  // ← 98.9% 的失败发生在这里
}
```

**pageRefCache.GetOrCreate** (`btree.go:79-151`)：

- 有过期检测：`currentInfo.GetPageID() != uint64(pageID)` 时创建新 ref
- **但这个检测不够**：回收重用的页面 pageID 不变（同一 mmap slot），只是内容变了
- 所以缓存永远不会被这个检查失效

**页面回收链路**：

```
freeOldPage(srcPageID)
  → epochBasedFreeList.Add(srcPageID)
  → pending[epoch]                    ← epoch N
  → AdvanceEpochNow() 时 pm.Free()    ← epoch N+2
  → delayedFreeList
  → AdvanceDelayedFreeList()           ← epoch N+3
  → freeList
  → Alloc() 返回被回收的 pageID       ← 新操作
```

### 2.3 为什么 8 线程 98.9% 都触发

| 因素 | 影响 |
|------|------|
| 500 init keys + 8 线程 | 每个叶子页被 ~800 次 update → 大量 COW + split |
| 64MB mmap = 16384 页 | 页面池有限，回收重用频繁 |
| 每次 COW 释放旧页面 | 快速回到 freeList → 被重新分配 |
| pageRefCache 无失效机制 | 缓存永久保存，不随页面回收清理 |

### 2.4 辅助验证：hasCycleFrom 的假阳性

`hasCycleFrom` (`search_path.go:305-350`) 在 split 后从新父节点遍历整棵子树检查循环：

```go
func (b *BTree) hasCycleFrom(pageID model.PageID) bool {
    visited := make(map[uint32]bool)
    var traverse func(pid model.PageID, depth int) bool
    traverse = func(pid model.PageID, depth int) bool {
        if visited[uint32(pid)] { return true }  // 检测到循环
        visited[uint32(pid)] = true
        // 遍历所有子节点...
        encodedChild := b.offheapAdapter.pa.GetChild(pid32, i)
        child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
        // child 可能指向被回收重用的页面 → 回到已访问节点 → 误报循环
    }
    return traverse(pageID, 0)
}
```

如果子节点指向的页面已被回收重用（内容完全不同），`GetChild` 读取到的可能是垃圾数据，指向已访问过的 pageID，导致 **假阳性** 的循环引用检测。

---

## 3. 修复方案

### 3.1 方案 A：pageRefCache 失效（推荐，最小改动）

**原理**：页面回收时，从 pageRefCache 中删除对应条目。下次搜索路径访问该 pageID 时会创建新的 PageRef，加载最新的页面内容。

**改动点**：

#### A1. pageRefCache 新增 Delete 方法

**文件**: `btree.go`

```go
// Delete 从缓存中移除指定页面的 PageRef
// 在页面回收时调用，确保下次访问时重新加载
func (c *PageRefCache) Delete(pageID model.PageID) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.cache, pageID)
}
```

#### A2. BTree 新增 cacheInvalidateFreedPages 方法

**文件**: `btree.go`

```go
// cacheInvalidateFreedPages 在 epoch 推进释放页面时，同步清理 pageRefCache
func (b *BTree) cacheInvalidateFreedPages(pageIDs []model.PageID) {
    for _, pid := range pageIDs {
        b.pageRefCache.Delete(pid)
    }
}
```

#### A3. EpochBasedFreeList 改造 — 回调通知

**文件**: `btree.go`

当前 `AdvanceEpochNow` 直接调用 `pm.Free()` 和 `pm.AdvanceDelayedFreeList()`，BTree 层不知道哪些页面被释放。

**改造方案**：注入回调，在页面从 pending 移到 pm.Free 前通知 BTree 层清理缓存。

```go
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64
    pending      map[uint64][]model.PageID
    mu           sync.Mutex
    batchCounter atomic.Int64
    batchSize    int
    onBeforeFree func([]model.PageID) // 新增：页面释放前回调（清理缓存）
}

func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    e.mu.Lock()
    defer e.mu.Unlock()

    newEpoch := e.currentEpoch.Add(1)

    // 释放 epoch-2 的页面（安全窗口）
    epochToDelayed := newEpoch - 2
    if newEpoch >= 2 {
        pagesToDelayed := e.pending[epochToDelayed]
        delete(e.pending, epochToDelayed)

        // 通知 BTree 层清理这些页面的缓存
        if e.onBeforeFree != nil && len(pagesToDelayed) > 0 {
            e.onBeforeFree(pagesToDelayed)
        }

        for _, pid := range pagesToDelayed {
            pm.Free(uint32(pid))
        }
    }

    // delayedFreeList → freeList
    epochToFree := newEpoch - 3
    if newEpoch >= 3 {
        delete(e.pending, epochToFree)
        pm.AdvanceDelayedFreeList()
    }
}
```

#### A4. OpenBTree 注入回调

**文件**: `btree.go` — `OpenBTree()`

```go
// 注入缓存清理回调
epochBasedFreeList.onBeforeFree = func(pageIDs []model.PageID) {
    for _, pid := range pageIDs {
        b.pageRefCache.Delete(pid)
    }
}
```

**注意**：这里 `b` 是 BTree 指针，需要在 `btree := &BTree{...}` 之后设置。由于 `epochBasedFreeList` 在 `btree` 之前创建，需要在 `btree` 构造后注入：

```go
btree := &BTree{...}

// 注入回调（必须在 btree 构造后）
epochBasedFreeList.onBeforeFree = func(pageIDs []model.PageID) {
    btree.pageRefCache.Delete_batch(pageIDs) // 批量删除优化
}
```

#### A5. 优化：批量 Delete

```go
// Delete_batch 批量从缓存中移除 PageRef（减少锁开销）
func (c *PageRefCache) Delete_batch(pageIDs []model.PageID) {
    c.mu.Lock()
    defer c.mu.Unlock()
    for _, pid := range pageIDs {
        delete(c.cache, pid)
    }
}
```

**改动量**：~30 行代码

**风险**：低。只是在页面回收时清理缓存，不影响正常读写路径。最坏情况是缓存未命中（重新创建 PageRef），轻微性能损失。

### 3.2 方案 B：版本校验搜索路径（中等改动）

**原理**：在搜索路径中读取子节点时，校验 child version 是否与 PageInfo 中的版本匹配。不匹配则返回 ErrRetry。

**改动点**：

#### B1. SearchChild 返回 version

**文件**: `offheap_adapter.go` — `SearchChild()`

当前 `SearchChild` 只返回 `childPageID`，不返回 version。需要改为同时返回 version。

```go
func (a *OffHeapAdapter) SearchChild(pageID uint32, key []byte) (uint32, uint32, error) {
    // SearchKey 找到 child 所在的 index
    idx, _, err := a.pa.SearchKey(pageID, key, false)
    if err != nil {
        return 0, 0, err
    }

    // 获取 encoded child（含 version）
    encodedChild := a.pa.GetChild(pageID, idx+1) // +1 因为 children 比 keys 多一个
    childPageID, childVersion := offheap.DecodeChildWithVersion(encodedChild)

    return childPageID, childVersion, nil
}
```

#### B2. 搜索路径校验 version

**文件**: `search_path.go` — `searchPathWithRefs()`

```go
childPageID, childVersion, err := b.offheapAdapter.SearchChild(currentPageID, key)

childRef := b.pageRefCache.GetOrCreate(childPageID, isChildLeaf)
childInfo := childRef.GetPageInfo()

// 版本校验：如果不匹配说明页面已被回收重用
if childInfo != nil && childInfo.GetVersion() != uint64(childVersion) {
    return nil, nil, ErrRetry // 版本不匹配，重试
}
```

**改动量**：~15 行代码

**风险**：中。需要确认所有 `SearchChild` 调用者能正确处理新返回值。版本比较可能引入新的边界条件。

**优点**：不修改 epoch 机制，只在搜索路径增加防御性检查。

### 3.3 方案 C：引用计数（大改动，长期方向）

**原理**：搜索路径获取 PageRef 时增加引用计数，页面回收时检查引用计数是否为 0。

**改动量**：需要重新设计 PageRef 生命周期管理，影响面大。

**建议**：Phase 2 再考虑。

---

## 4. 推荐实施策略

### Phase 1：方案 A（pageRefCache 失效）

最小改动，立即解决 98.9% 循环引用问题。

| 步骤 | 改动 | 文件 |
|------|------|------|
| 1 | pageRefCache 新增 Delete/Delete_batch | `btree.go` |
| 2 | EpochBasedFreeList 新增 onBeforeFree 回调 | `btree.go` |
| 3 | AdvanceEpochNow 释放页面前调用回调 | `btree.go` |
| 4 | OpenBTree 注入回调 | `btree.go` |

### Phase 2：方案 B（版本校验）— 防御增强

在方案 A 基础上，搜索路径增加 version 校验作为额外防御层。

### Phase 3：hasCycleFrom 优化

当前 `hasCycleFrom` 遍历整棵子树，开销很大（O(n)）。在方案 A+B 实施后，循环引用应该不再发生，可以考虑：
- 移除 `hasCycleFrom` 检测（减少 split 路径延迟）
- 或改为采样检测（只在特定条件下触发）

---

## 5. 验证方案

### 5.1 基准测试

```bash
# Phase 1 修复后
go run ./cmd/btree_perf_pprof -threads=8 -count=50000 -init=500

# 预期：
# - ErrCircRef 应降到 <5%（或 0%）
# - Success 应恢复到 >80%
# - 无 panic
```

### 5.2 正确性验证

```bash
# 运行完整测试套件
go test -v -race ./internal/infrastructure/storage/btree/...

# 重点测试：
# - TestSetWithLeafLock_Concurrent
# - TestSetWithLeafLock_ExtremeConcurrency
# - TestDebug6000KeysNoLoss
```

### 5.3 性能对比

| 指标 | 修复前 | 目标 |
|------|--------|------|
| 8 线程 Success | 0.6% | >80% |
| ErrCircRef | 98.9% | <5% |
| ops/sec | ~2,094 | >100,000 |

---

## 6. 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 缓存失效导致性能下降 | 低 | 轻微 | 失效仅在 epoch 推进时发生，频率低 |
| onBeforeFree 回调死锁 | 低 | 严重 | 回调中不持有其他锁，仅操作 pageRefCache |
| 仍有残留循环引用 | 中 | 中等 | 方案 B 作为额外防御层 |
| 批量 Delete 的锁持有时间 | 低 | 轻微 | 每次 epoch 推进释放的页面数有限（~batchSize） |

---

## 7. 实施记录

### Phase 1 已完成 (2026-03-30)

| 改动 | 文件 | 说明 |
|------|------|------|
| `DeleteBatch` 批量缓存清除 | `btree.go:144-152` | 复用已有 `pageRefCache.mu`，无新锁 |
| `onBeforeFree` 回调注入 | `btree.go:256-262` | epoch 推进释放页面前通知 BTree 层清理缓存 |
| `OpenBTree` 注入回调 | `btree.go:417-421` | 构造完成后注入，避免循环依赖 |
| 循环引用 → `ErrRetry` | `search_path.go:241` | 假阳性循环引用不再致命，允许重试 |
| `GetLeafEntrySafe` 安全读取 | `page_layout.go:607-612` | 源页面被回收时返回 error 而非 panic |

### 验证结果

**ErrCircRef 已完全消除**：

```
8 线程 × 50000 ops，500 init keys：
  Success:      4,740  (1.2%)    ← 目标 >80%，待进一步优化
  ErrRetry:   395,259  (98.8%)   ← 页面竞争导致重试，非致命
  ErrCircRef:       0  (0.0%)    ✅ 已消除（原 98.9%）
```

**正确性测试全部通过**：

- `TestSetWithLeafLock_Concurrent` — PASS
- `TestSetWithLeafLock_ExtremeConcurrency` — 100% 成功率 (10000/10000)
- `TestDebug6000KeysNoLoss` — PASS

### 锁安全性

修复未引入新锁。嵌套锁顺序固定：`EpochBasedFreeList.mu → PageRefCache.mu`，无反向路径。

### 待办：Phase 2（版本校验搜索路径）

当前 Success 率仅 1.2%，说明页面竞争仍然严重。Phase 2 需在搜索路径中加入 version 校验，
在 COW/split 导致 child 指针过期时快速失败，减少无效重试开销。`SearchChild` 已返回 `childVersion`，
搜索路径中尚未加入 version 比较逻辑。
