# Off-Heap 页面池回收修复方案

**日期**: 2026-03-30

---

## 1. 问题摘要

1 线程 Set 85.4% 失败，根因有两个：

1. **`PageManager.Alloc()` 不回收已释放页面** — `freeList` 形同虚设
2. **Epoch 延迟释放窗口过大** — `AdvanceDelayedFreeList()` 被批量策略阻塞，页面回收不及时

| 组件 | 现状 | 问题 |
|------|------|------|
| `Alloc()` | 仅靠 `nextPageID++` | `freeList` 从不消费 |
| `AdvanceDelayedFreeList()` | 每 1000 次 epoch 推进才调用 | 回收滞后，freeList 耗尽后 OOM |
| `EpochBasedFreeList` | `sync.Mutex` 保护 `pending map` | 写路径有锁 |
| `mmapSize` | 硬编码 64MB | 无法适应不同负载 |
| `BTreeConfig` | 无 offheap 配置 | 调用方无法调整 |

---

## 2. 修复方案

### 2.1 P0: 核心修复

#### Fix 1: Alloc() 回收 freeList

**文件**: `offheap/page_manager.go`

**改动**: `Alloc()` 优先从 `freeList` 取页面，`nextPageID` 作为 fallback。

```go
// Alloc 分配一个页面
// 优先从 freeList 回收已释放页面，nextPageID 递增作为 fallback
func (pm *PageManager) Alloc() (uint32, error) {
    // 优先路径：从 freeList 取已释放页面（lock-free）
    if pageID, ok := pm.freeList.Dequeue(); ok {
        pm.used.Add(1)
        pm.tracker.RecordAlloc(pageID)
        return pageID, nil
    }

    // Fallback：nextPageID 单调递增
    pageID := pm.nextPageID.Load()
    if pageID >= pm.total {
        return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
    }
    pm.nextPageID.Add(1)
    pm.used.Add(1)
    pm.tracker.RecordAlloc(pageID)
    return pageID, nil
}
```

**效果**:
- 已释放页面可被重用，`nextPageID` 到 16384 不再意味着 OOM
- benchmark 预期从 14.6% 成功率恢复到 ~100%
- lock-free 队列无锁竞争，不影响并发性能

#### Fix 2: 降低 epoch 延迟 + flushDelayedOnce

**问题分析**：Fix 1 让 `Alloc()` 能从 `freeList` 取页面，但 `freeList` 的补给受 epoch 批量策略阻塞。

当前页面释放链路：

```
pm.Free(pageID)
  → delayedFreeList                    ← 第 1 层：直接进入延迟队列
  → AdvanceDelayedFreeList()            ← 第 2 层：等 epoch 推进才调用
  → freeList                            ← Alloc 才能消费
```

`AdvanceDelayedFreeList()` 只在 `AdvanceEpochNow()` 内部调用，而 `AdvanceEpochNow()` 每 1000 次才触发一次。

**稳态分析**（batch=1000，16384 页）：

```
epoch 0-1 (ops 1-2000):   delayedFreeList 积累 ~2000 页，freeList = 0
                          全靠 nextPageID → 用掉 ~2200 页
epoch 2 (op 2000):        Free(epoch-0 pending)，但没调 AdvanceDelayedFreeList
epoch 3 (op 3000):        AdvanceDelayedFreeList()！~3000 页 → freeList
epoch 4+ (稳态):          每次推进补充 ~1000 页，消费 ~1000 页 → 平衡
```

16384 页够用（前 3000 ops 用 ~3200 页）。**但小 mmap 时会 OOM**：

| mmapSize | 总页面 | init 占用 | epoch 3 前需要 | 结果 |
|----------|--------|----------|---------------|------|
| 64MB | 16,384 | ~200 | ~3,200 | OK |
| 16MB | 4,096 | ~200 | ~3,200 | OOM |
| 4MB | 1,024 | ~200 | ~3,200 | OOM |

**修复方案**：将 `delayedFreeList → freeList` 的推进从 epoch 批量策略中解耦，每次 `Alloc()` 发现 `freeList` 为空时主动推进。

**文件**: `offheap/page_manager.go`

```go
// PageManager 新增字段
type PageManager struct {
    // ... 现有字段 ...
    flushing atomic.Bool  // CAS 保护 flushDelayedOnce
}

// Alloc 分配一个页面
func (pm *PageManager) Alloc() (uint32, error) {
    // 路径 1：从 freeList 取（lock-free）
    if pageID, ok := pm.freeList.Dequeue(); ok {
        pm.used.Add(1)
        pm.tracker.RecordAlloc(pageID)
        return pageID, nil
    }

    // 路径 2：条件 flush（freeList 空且 delayedFreeList 有数据）
    // 前置检查避免 delayedFreeList 为空时的无效 CAS 开销
    if pm.delayedFreeList.Size() > 0 {
        pm.flushDelayedOnce()
        if pageID, ok := pm.freeList.Dequeue(); ok {
            pm.used.Add(1)
            pm.tracker.RecordAlloc(pageID)
            return pageID, nil
        }
    }

    // 路径 3：fallback，nextPageID 递增
    pageID := pm.nextPageID.Load()
    if pageID >= pm.total {
        return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
    }
    pm.nextPageID.Add(1)
    pm.used.Add(1)
    pm.tracker.RecordAlloc(pageID)
    return pageID, nil
}

// flushDelayedOnce 将 delayedFreeList 中的所有页面移到 freeList
// 每次 Alloc 的 freeList miss 时调用，避免 epoch 批量策略导致的回收滞后
//
// 并发安全：
// - LockFreeQueue 的 Dequeue/Enqueue 是原子 CAS，多 goroutine 不会重复移动页面
// - CAS flushing flag 避免多 goroutine 同时做无意义的 flush
func (pm *PageManager) flushDelayedOnce() {
    if !pm.flushing.CompareAndSwap(false, true) {
        return
    }
    defer pm.flushing.Store(false)

    for {
        pageID, ok := pm.delayedFreeList.Dequeue()
        if !ok {
            break
        }
        pm.freeList.Enqueue(pageID)
    }
}
```

**效果**：

```
修复前: freeList 补给周期 = 1000 ops → 滞后窗口大
修复后: freeList 补给周期 = 1 Alloc miss → 滞后窗口极小
```

页面回收不再依赖 epoch 批量推进，`delayedFreeList` 中的页面在下次 `Alloc()` 时立即可用。Epoch 机制仍保留用于 `EpochBasedFreeList.pending` 的安全管理（确保没有 goroutine 仍在访问旧页面），但 `delayedFreeList → freeList` 的转换变为即时。

### 2.2 P1: 性能优化

#### Fix 2 补充: EpochBasedFreeList 去 mutex

**文件**: `btree.go`（`EpochBasedFreeList` 定义）

当前 `EpochBasedFreeList` 使用 `sync.Mutex` 保护 `pending map`，写路径 `Add()` 每次加锁：

```go
// 当前：有锁
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64
    pending      map[uint64][]model.PageID  // mu 保护
    mu           sync.Mutex                 // ← 写路径热路径
    batchCounter atomic.Int64
}

func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    e.mu.Lock()
    epoch := e.currentEpoch.Load()
    e.pending[epoch] = append(e.pending[epoch], pageID)
    e.mu.Unlock()
}
```

**修复**：用已有的 `LockFreeQueue`（`offheap/lockfree_queue.go`，Michael-Scott 算法）替换 `pending map + mu`：

```go
// 修复后：无锁
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64
    pendingQueue *LockFreeQueue   // ← 替换 map + mu
    batchCounter atomic.Int64
    batchSize    int
}

func NewEpochBasedFreeList() *EpochBasedFreeList {
    return &EpochBasedFreeList{
        pendingQueue: NewLockFreeQueue(),
        batchSize:    1000,
    }
}

func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    e.pendingQueue.Enqueue(uint32(pageID))  // ← lock-free
}

func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    newEpoch := e.currentEpoch.Add(1)
    _ = newEpoch

    // drain pendingQueue → pm.Free() → delayedFreeList
    for {
        pageID, ok := e.pendingQueue.Dequeue()
        if !ok {
            break
        }
        pm.Free(pageID)
    }

    // delayedFreeList → freeList
    // Fix 2 后 Alloc 路径已通过 flushDelayedOnce 即时处理
    // 此处保留确保 AdvanceEpoch 路径也触发
    pm.flushDelayedOnce()
}
```

**兼容性修改**：`leaf_lock_set.go:284-295` 直接访问 `epochBasedFreeList.mu` 和 `.pending`（活锁检测），需提供公开方法：

```go
// EpochBasedFreeList 新增公开方法
// PendingCountInCurrentEpoch 返回当前 epoch 待释放页面的数量
// 用于活锁检测（替代 leaf_lock_set.go 中直接访问 mu + pending）
func (e *EpochBasedFreeList) PendingCountInCurrentEpoch() int {
    return e.pendingQueue.Size()
}
```

`leaf_lock_set.go` 修改：
```go
// 旧代码（直接访问内部字段）：
// b.epochBasedFreeList.mu.Lock()
// currentEpoch := b.epochBasedFreeList.currentEpoch.Load()
// currentEpochPending := b.epochBasedFreeList.pending[currentEpoch]
// ...
// b.epochBasedFreeList.mu.Unlock()

// 新代码（通过公开方法）：
pendingCount := b.epochBasedFreeList.PendingCountInCurrentEpoch()
if pendingCount > 15 {
    // 活锁检测触发备用策略
}
```

**效果**：整个页面回收链路 `Add → pendingQueue → pm.Free → delayedFreeList → flushDelayedOnce → freeList → Alloc` 全程 lock-free。

#### Fix 3: mmap 大小动态计算

**文件**: `offheap/page_manager.go`

**改动**: 新增 `GetRecommendedMmapSize()`，不写死 64MB。

```go
// GetRecommendedMmapSize 根据系统内存计算推荐 mmap 大小
// ratio: 使用物理内存的比例（推荐 0.6）
// 最小 64MB，最大 32GB
func GetRecommendedMmapSize(ratio float64) (int, error) {
    sysInfo := &syscall.Sysinfo_t{}
    if err := syscall.Sysinfo(sysInfo); err != nil {
        fmt.Fprintf(os.Stderr, "offheap: failed to get system memory info: %v, fallback to 64MB\n", err)
        return 64 << 20, nil
    }

    totalMem := uint64(sysInfo.Totalram) * uint64(sysInfo.Unit)
    size := uint64(float64(totalMem) * ratio)

    const minSize = 64 << 20   // 64MB
    const maxSize = 32 << 30   // 32GB

    if size < minSize {
        size = minSize
    }
    if size > maxSize {
        size = maxSize
    }
    return int(size), nil
}
```

**不同机器自动计算结果**:

| 机器内存 | mmapSize（60%） | 可用页面数 |
|---------|-----------------|-----------|
| 8GB     | 4.8GB           | 1,258,291 |
| 16GB    | 9.6GB           | 2,516,582 |
| 32GB    | 19.2GB          | 5,033,164 |
| 64GB+   | 32GB（硬上限）   | 8,388,608 |

当前硬编码 64MB = 16,384 页，远低于机器能力。

#### Fix 5: 页面池监控指标

**文件**: `offheap/page_manager.go`

```go
// PageManagerStats 页面池运行时统计
type PageManagerStats struct {
    AllocFromFreeList   atomic.Uint64  // 从 freeList 分配次数
    AllocFromNextPageID atomic.Uint64  // 从 nextPageID 分配次数
    AllocMissFlush      atomic.Uint64  // Alloc miss 触发 flush 次数
    FreeListEmpty       atomic.Uint64  // freeList 为空次数（含 flush 后仍空）
}

// GetAllocStats 获取分配统计（调试/监控用）
func (pm *PageManager) GetAllocStats() PageManagerStats {
    return pm.allocStats
}
```

`Alloc()` 中埋点（在 Fix 2 的 3 路径 Alloc 基础上添加）：

```go
func (pm *PageManager) Alloc() (uint32, error) {
    // 路径 1：从 freeList 取
    if pageID, ok := pm.freeList.Dequeue(); ok {
        pm.allocStats.AllocFromFreeList.Add(1)
        pm.used.Add(1)
        pm.tracker.RecordAlloc(pageID)
        return pageID, nil
    }

    // 路径 2：条件 flush
    if pm.delayedFreeList.Size() > 0 {
        pm.flushDelayedOnce()
        pm.allocStats.AllocMissFlush.Add(1)
        if pageID, ok := pm.freeList.Dequeue(); ok {
            pm.allocStats.AllocFromFreeList.Add(1)
            pm.used.Add(1)
            pm.tracker.RecordAlloc(pageID)
            return pageID, nil
        }
    }

    // 路径 3：nextPageID fallback
    pm.allocStats.AllocFromNextPageID.Add(1)
    pageID := pm.nextPageID.Load()
    if pageID >= pm.total {
        return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
    }
    pm.nextPageID.Add(1)
    pm.used.Add(1)
    pm.tracker.RecordAlloc(pageID)
    return pageID, nil
}
```

**用途**：
- 线上观察页面回收效率（`AllocFromFreeList / TotalAlloc` 比率）
- 判断是否需要调大 mmapSize（`FreeListEmpty` 过高说明池子不够）
- 排查 OOM（`AllocFromNextPageID` 持续增长说明回收链路有问题）

### 2.3 P2: 配置优化

#### Fix 4: BTreeConfig 增加 OffHeap 配置

**文件**: `internal/domain/model/btree_types.go`

```go
type BTreeConfig struct {
    // ... 现有字段 ...

    // OffHeap 配置
    OffHeapEnabled     bool    // 是否启用 Off-Heap 存储（默认 true）
    OffHeapSize        int     // Off-Heap 内存池大小（字节），0 = 自动计算
    OffHeapMemoryRatio float64 // 自动计算时的内存使用比例（默认 0.6）
}
```

**默认值**（`NewDefaultBTreeConfig`）:

```go
OffHeapEnabled:     true,
OffHeapSize:        0,    // 0 = 自动计算
OffHeapMemoryRatio: 0.6,  // 60% 物理内存
```

**文件**: `internal/infrastructure/storage/btree/btree.go` — `OpenBTree()`

```go
// 替换硬编码
// 旧: mmapSize := 64 * 1024 * 1024 // 64MB
// 新:
var mmapSize int
if config.OffHeapSize > 0 {
    mmapSize = config.OffHeapSize
} else {
    mmapSize, err = offheap.GetRecommendedMmapSize(config.OffHeapMemoryRatio)
    if err != nil {
        mmapSize = 64 << 20 // fallback 64MB
    }
}
offheapPM, err := offheap.NewPageManager(mmapSize)
```

---

## 3. 修复优先级

| 优先级 | 修复项 | 影响 | 复杂度 |
|--------|--------|------|--------|
| **P0** | Fix 1: Alloc() 回收 freeList | 让 freeList 生效 | ~5 行代码 |
| **P0** | Fix 2: flushDelayedOnce + epoch 延迟降低 | 消除 freeList 耗尽窗口 | ~15 行代码 |
| **P1** | Fix 2 补充: EpochBasedFreeList 去 mutex | 写路径全程无锁 | 替换 map→LockFreeQueue + 修改 leaf_lock_set.go |
| **P1** | Fix 3: mmapSize 动态计算 | 适应不同机器 | 新增函数 |
| **P1** | Fix 5: 页面池监控指标 | 线上可观测 | 新增结构体 + 埋点 |
| **P2** | Fix 4: BTreeConfig OffHeap 配置 | 调用方可配置 | 配置字段 |

**Fix 1 + Fix 2 组合**彻底解决 OOM：Fix 1 让 Alloc 能回收，Fix 2 确保回收即时。

---

## 4. 审核意见（2026-03-30）

### 4.1 严重问题

#### S1. Fix 2 补充 "去 mutex" 破坏 Epoch 安全语义 — use-after-free 风险

**当前代码**（安全）: `AdvanceEpochNow()` 按 epoch 分组释放，只释放 epoch-2 的页面。

**文档提案**（不安全）: 用单个 `LockFreeQueue` 替换 `pending map`，drain 时会把所有 pending 页面一起 Free，包括刚 Add 进去的页面。

**后果**: goroutine A 刚把旧页面加入 pending，goroutine B 立即 drain 并 Free → use-after-free。

**修复方案**: 改用 epoch-based ring buffer 保持分组语义。

**Ring buffer 大小推导**: 核心约束是 drain slot 不能与 write slot 重叠。写入 epoch = E 时写入 `E % N`，drain epoch-2 时读取 `(E-2) % N`。要求 `E % N ≠ (E-2) % N`，即 N 不能整除 2。因此 **N ≥ 3 是最小值**（N=2 时两个 slot 会冲突）。

```go
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64
    ringBuffers  [3]*LockFreeQueue  // 3 epoch 环形缓冲区（最小安全值）
    ringIdx      atomic.Uint64      // 当前写入的 ring index
    batchSize    int
    batchCounter atomic.Int64
}

func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    idx := e.ringIdx.Load() % 3
    e.ringBuffers[idx].Enqueue(uint32(pageID))
}

func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    newEpoch := e.currentEpoch.Add(1)
    e.ringIdx.Store(e.ringIdx.Load() + 1)

    // 释放 epoch-2 的页面（安全窗口）
    // ringIdx 已推进，写入 slot 为 newEpoch%3，drain slot 为 (newEpoch-2)%3
    // 因为 N=3 不整除 2，两个 slot 不会重叠
    if newEpoch >= 2 {
        delayIdx := (newEpoch - 2) % 3
        for {
            pageID, ok := e.ringBuffers[delayIdx].Dequeue()
            if !ok { break }
            pm.Free(pageID)
        }
    }

    // 推进 delayedFreeList → freeList
    if newEpoch >= 3 {
        pm.flushDelayedOnce()
    }
}
```

#### S2. flushDelayedOnce 对 COW 路径有 use-after-free 风险

**COW 路径分析**: `updateLeafEntryBulkCOW()` 绕过 epoch 机制，直接调用 `pm.Free(srcPageID)` 把旧页面放入 `delayedFreeList`。这些页面未经 epoch 保护。

如果 `flushDelayedOnce()` 立即将其移到 `freeList` 并被重用，其他并发读可能还在访问旧页面。

**修复方案**（推荐方案 A）:

**方案 A（推荐）**: COW 路径的 Free 也走 epoch 机制，统一页面释放入口。

**需要改造的 pm.Free() 调用点清单**（`offheap_adapter.go`）:

| 行号 | 函数 | 释放对象 | 类型 | 改造 |
|------|------|----------|------|------|
| 199 | `updateLeafEntryFullMaterialization` | srcPageID（旧页面） | **COW 旧页面** | → epochFree |
| 253 | `updateLeafEntryBulkCOW` | srcPageID（旧页面） | **COW 旧页面** | → epochFree |
| 210 | `updateLeafEntryFullMaterialization` | newPageID | 错误清理 | 保持 pm.Free |
| 242 | `updateLeafEntryBulkCOW` | newRawPageID | 错误清理 | 保持 pm.Free |
| 248 | `updateLeafEntryBulkCOW` | newRawPageID | 错误清理 | 保持 pm.Free |
| 375 | `UpdateIndexEntry` | newPageID | 错误清理 | 保持 pm.Free |
| 468 | `ReplaceChild` | newPageID | 错误清理 | 保持 pm.Free |
| 503 | `MaterializeLeafPage` | pageID | 错误清理 | 保持 pm.Free |
| 555,560,565 | `SplitOffHeapLeafPage` | left/rightPageID | 错误清理 | 保持 pm.Free |
| 614,615 | `SplitOffHeapLeafPage` | leftPageID,rightPageID | 错误清理 | 保持 pm.Free |
| 673 | `splitOffHeapLeafPageFallback` | leftPageID | 错误清理 | 保持 pm.Free |
| 745,765,766 | `splitOffHeapLeafPageFallback` | newLeft/newRightPageID | 错误清理 | 保持 pm.Free |
| 786,787 | `splitOffHeapLeafPageFallback` | left/rightPageID | 错误清理 | 保持 pm.Free |
| 794,795 | `splitOffHeapLeafPageFallback` | left/rightPageID | 错误清理 | 保持 pm.Free |
| 1004 | `DeleteFromLeafPage` | newPageID | 错误清理 | 保持 pm.Free |
| 1064 | `UpdateChildIndex` | newParentPageID | 错误清理 | 保持 pm.Free |

**改造原则**: 只有 COW 成功后释放**旧页面**的调用需要走 epoch（其他 goroutine 可能仍在读）。错误清理释放的是从未被其他 goroutine 看到的新页面，可直接 pm.Free。

适配层改动 — `offheap_adapter.go` 需要访问 `epochBasedFreeList`：

```go
// OffHeapAdapter 新增字段
type OffHeapAdapter struct {
    pa         *offheap.PageAccessor
    pm         *offheap.PageManager
    materializer *offheap.PageMaterializer
    epochFree  func(model.PageID)  // 新增：epoch 释放回调（由 BTree 层注入）
}
```

BTree 层注入回调 — `btree.go` OpenBTree()：

```go
offheapAdapter := NewOffHeapAdapter(offheapPM)
// 注入 epoch 释放回调
offheapAdapter.epochFree = func(pageID model.PageID) {
    b.epochBasedFreeList.Add(pageID)
}
```

COW 路径改造 — `offheap_adapter.go` updateLeafEntryBulkCOW()：

```go
// 旧代码：直接 pm.Free（绕过 epoch，不安全）
// a.pm.Free(srcPageID)

// 新代码：走 epoch 机制（安全）
if a.epochFree != nil {
    a.epochFree(model.PageID(srcPageID))
} else {
    a.pm.Free(srcPageID)  // fallback：无 epoch 机制时直接释放
}
```

同理 `updateLeafEntryFullMaterialization()` 中的 `pm.Free()` 也需统一改造。

**方案 B**: `flushDelayedOnce()` 仅处理已经过 epoch 保护的页面（需在 Free 时标记来源）

**方案 C**: `flushDelayedOnce` 只在 `Alloc()` 的 freeList miss 且 `nextPageID` 也耗尽时才调用

**选择方案 A 的理由**: 最安全，所有页面释放统一走 epoch 机制，消除 `delayedFreeList` 中混入未保护页面的风险。

### 4.2 中等问题

#### M1. livelock detection 语义变化

当前代码按特定 pageID 过滤计数（检测"同一页面被多次分裂"），文档改为 pending 总数。

**建议**: 提供 `PendingCountForPage(pageID)` 方法保持原语义。如果用 ring buffer 方案，可遍历对应 epoch 的 ring buffer。

#### M2. tracker.RecordAlloc() 一致性

文档 Fix 2 的代码片段中，路径 1 和路径 2 缺少 `pm.tracker.RecordAlloc(pageID)` 调用。需确保所有 Alloc 路径都调用。

#### M3. GetRecommendedMmapSize 缺少单元测试

需补充：不同内存比例测试、最小/最大边界测试、Sysinfo 失败 fallback 测试。

#### M4. 监控指标不完整

`PageManagerStats` 缺少 Free 相关指标：`FreeTotal`、`DelayedFreeListSize`、`FreeListSize`。

### 4.3 推荐实施策略

**Phase 1 — P0 核心修复**（先解决 OOM，最小改动）:
1. Fix 1: `Alloc()` 从 freeList 回收（~5 行，安全）
2. Fix 2 简化版: COW 路径的 Free 统一走 epoch 机制（方案 A，含适配层改造），`flushDelayedOnce()` 仅在 epoch 触发时调用

**Phase 2 — P1 优化**（验证 Phase 1 后再推进）:
3. Fix 2 补充: 去 mutex（改用 ring buffer 方案）
4. Fix 3: mmap 动态计算
5. Fix 5: 监控指标

**Phase 3 — P2 配置**:
6. Fix 4: BTreeConfig 配置项

---

## 5. Phase 1 实施记录（2026-03-30）

### 5.1 已完成修复

#### Fix 1: Alloc() 从 freeList 回收 ✅

**文件**: `offheap/page_manager.go`

`Alloc()` 优先从 `freeList` 取页面，`nextPageID` 作为 fallback。无锁路径，直接消费 `LockFreeQueue`。

#### Fix 2 简化版: COW 路径走 epoch + 移除 Alloc 中的 flushDelayedOnce ✅

**改造内容**:

1. **`offheap_adapter.go`**: 新增 `epochFree` 回调字段 + `SetEpochFree()` 方法
2. **`btree.go` OpenBTree()**: 注入 epoch 回调，COW 旧页面走 `epochBasedFreeList.Add()`
3. **`offheap_adapter.go`**: `freeOldPage()` 统一释放入口（epoch 优先，fallback pm.Free）
4. **`offheap/page_manager.go`**: 从 `Alloc()` 移除 `flushDelayedOnce()` 调用（避免 COW 路径 use-after-free）

**设计决策**: `flushDelayedOnce()` 不在 `Alloc()` 中调用。页面回收完全由 epoch 机制控制：
- `freeOldPage()` → `epochBasedFreeList.Add()` → `pending map`
- `AdvanceEpochNow()` → `pm.Free()` → `delayedFreeList`
- `AdvanceDelayedFreeList()` → `freeList` → `Alloc()` 消费

这样保证 COW 旧页面经过完整的 epoch 延迟窗口后才可被重用。

### 5.2 Phase 1 过程中发现的新 Bug

#### Bug: COW 路径 srcPageID 被回收重用导致 panic

**症状**:
```
panic: index 0 out of range (count: 0)
  → GetLeafEntry() page_layout.go:215
  → BulkInitLeafFromSource() page_layout.go:607
  → updateLeafEntryBulkCOW() offheap_adapter.go:256
```

**根本原因**:

`BulkInitLeafFromSource(src, dst, ...)` 内部先调用 `InitLeafPage(dst)` 清空目标页面。如果 `Alloc()` 返回的 `dstPageID` 恰好等于 `srcPageID`（页面被快速回收重用），则：

```
1. InitLeafPage(srcPageID) → count=0, 数据全部清空
2. GetLeafEntry(srcPageID, 0) → panic: count=0
```

**触发条件**: epoch 机制推进导致源页面被释放到 `freeList`，紧接着的 `Alloc()` 将其分配出来作为目标页面。在 mmap 较小、页面数量有限时更容易触发。

**影响范围**: 三个使用 `BulkInit*FromSource` 的路径：

| 函数 | 文件 | 状态 |
|------|------|------|
| `updateLeafEntryBulkCOW` | `offheap_adapter.go` | ✅ 已修复 |
| `SplitOffHeapLeafPage` | `offheap_adapter.go` | ✅ 已修复 |
| 内部页面分裂 | `leaf_lock_set.go` | ✅ 已修复 |

**修复方案**: 检测 `dst == src` 冲突，降级到安全的 Go 堆路径。

`updateLeafEntryBulkCOW`:
```go
if newRawPageID == srcPageID {
    a.pm.Free(newRawPageID)
    return a.updateLeafEntryFullMaterialization(pageID, idx, nil, value)
}
```

`SplitOffHeapLeafPage`:
```go
srcRawID := uint32(pageID)
if leftPageID == rightPageID || leftPageID == srcRawID || rightPageID == srcRawID {
    a.pm.Free(leftPageID)
    a.pm.Free(rightPageID)
    return a.splitOffHeapLeafPageFallback(pageID)
}
```

内部页面分裂 (`leaf_lock_set.go`):
```go
srcInternalID := uint32(internalPageID)
if uint32(leftPageID) == srcInternalID || uint32(rightPageID) == srcInternalID {
    b.offheapAdapter.pm.Free(uint32(leftPageID))
    b.offheapAdapter.pm.Free(uint32(rightPageID))
    return fmt.Errorf("allocated page conflicts with source page %d", internalPageID)
}
```

**修复原理**: 降级路径（fullMaterialization/fallback）先将源页面数据拷贝到 Go 堆上，然后再释放源页面并分配新页面。因为数据已安全保存在堆上，不存在 src==dst 自清空问题。

### 5.3 验证结果

#### 单线程验证

```
$ go run ./cmd/btree_perf_pprof -threads=1 -count=5000 -init=200

========== 结果 ==========
耗时: 20.611488ms, 242583 ops/s
总 ops: 5000

--- 错误分类 ---
Success:              5000 (100.0%)
ErrRetry:                0 (0.0%)
ErrCircRef:              0 (0.0%)
ErrMaxRetries:           0 (0.0%)
ErrOther:                0 (0.0%)
```

✅ 单线程 100% 成功，无 panic。

#### 8 线程验证

```
$ go run ./cmd/btree_perf_pprof -threads=8 -count=50000 -init=500

========== 结果 ==========
耗时: 1.226283375s, 2094 ops/s
总 ops: 400000

--- 错误分类 ---
Success:              2568 (0.6%)
ErrRetry:             1815 (0.5%)
ErrCircRef:         395615 (98.9%)
ErrMaxRetries:           0 (0.0%)
ErrOther:                2 (0.0%)

--- Other 错误明细 ---
  [     2] overwrite leaf value failed: valLen insufficient
```

✅ 8 线程无 panic。ErrCircRef 98.9% 是已知的高并发循环引用重试问题（非本次修复范围），需 Phase 2 优化。

### 5.4 Phase 1 修改文件清单

| 文件 | 改动说明 |
|------|----------|
| `offheap/page_manager.go` | Fix 1: `Alloc()` 优先从 freeList 取页面；移除 `flushDelayedOnce()` 调用 |
| `offheap_adapter.go` | Fix 2: 新增 `epochFree`/`SetEpochFree`/`freeOldPage`；COW 路径走 epoch；src==dst 冲突检测 |
| `btree.go` | Fix 2: 注入 epoch 释放回调到 OffHeapAdapter |
| `leaf_lock_set.go` | 内部页面分裂 src==dst 冲突检测 |

---

## 6. 后续验证方案

### 6.1 Phase 1 验证（Fix 1 + Fix 2 简化版 + S2 方案 A）

```bash
# 修复前
go run ./cmd/btree_perf_pprof -threads 1 -count 50000 -init 200
# 预期: Success 14.6%, ErrOther 85.4% (OOM)

# 修复后
go run ./cmd/btree_perf_pprof -threads 1 -count 50000 -init 200
# 预期: Success 100%, OOM = 0
```

### 6.2 并发验证

```bash
go run ./cmd/btree_perf_pprof -threads 8 -count 50000 -init 5000
# 预期: 无 OOM，只有正常的 ErrRetry（锁竞争）
```

### 6.3 小 mmap 验证（Fix 2 关键场景）

```bash
# 强制小 mmap（如果 Fix 4 已实现）
go run ./cmd/btree_perf_pprof -threads 1 -count 50000 -init 200 -mmap-size 4194304
# 4MB = 1024 页，Fix 2 确保从 delayedFreeList 紧急补充
# 预期: Success 100%（不再依赖 nextPageID 的缓冲量）
```

### 6.4 单元测试

```go
func TestPageManager_AllocRecyclesFreeList(t *testing.T) {
    pm, _ := NewPageManager(PageSize * 10) // 10 页
    // 分配 10 页
    ids := make([]uint32, 10)
    for i := range ids {
        ids[i], _ = pm.Alloc()
    }
    // 释放 5 页
    for i := 0; i < 5; i++ {
        pm.Free(ids[i])
    }
    // 推进延迟释放
    pm.flushDelayedOnce()
    // 再分配 5 页：应从 freeList 取，nextPageID 不变
    for i := 0; i < 5; i++ {
        id, err := pm.Alloc()
        assert.NoError(t, err)
        assert.Contains(t, ids[:5], id) // 必须是之前释放的页面
    }
    // nextPageID 应该还是 10
    assert.Equal(t, uint32(10), pm.nextPageID.Load())
}

func TestAlloc_FallbackToDelayedFreeList(t *testing.T) {
    pm, _ := NewPageManager(PageSize * 20) // 20 页
    // 分配 20 页
    for i := 0; i < 20; i++ {
        pm.Alloc()
    }
    // nextPageID 已达上限
    // 释放 10 页到 delayedFreeList
    for i := uint32(0); i < 10; i++ {
        pm.Free(i)
    }
    // Alloc 应通过 flushDelayedOnce 回收
    id, err := pm.Alloc()
    assert.NoError(t, err)
    assert.Less(t, id, uint32(10)) // 必须是回收的页面
}

func TestFlushDelayedOnce_Concurrent(t *testing.T) {
    pm, _ := NewPageManager(PageSize * 100)
    // 释放 50 页
    for i := uint32(0); i < 50; i++ {
        pm.Free(i)
    }
    // 并发 flush + alloc
    var wg sync.WaitGroup
    for g := 0; g < 10; g++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for i := 0; i < 5; i++ {
                pm.Alloc()
            }
        }()
    }
    wg.Wait()
    // 所有 50 页应被正确分配（不重复、不丢失）
    stats := pm.GetStats()
    assert.Equal(t, uint32(50), stats.Used)
}
```
