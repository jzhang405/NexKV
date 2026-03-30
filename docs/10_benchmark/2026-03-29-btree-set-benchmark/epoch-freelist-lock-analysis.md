# P3: EpochBasedFreeList 锁竞争深度分析

**日期**: 2026-03-30
**分支**: `perf/btree-set-benchmark2`
**关联**: `lock-contention-analysis.md` 锁清单 #5（争用风险 🔴 高）
**CPU Profile**: `cpu-8t-lock-analysis.prof`

---

## 1. 数据结构概览

```go
// btree.go:206-214
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64             // 当前 epoch（无锁读）
    pending      map[uint64][]model.PageID // epoch → 待释放页面列表（需 mutex 保护）
    mu           sync.Mutex                // 全局排他锁
    batchSize    int                       // 批量处理阈值（默认 1000）
    batchCounter atomic.Int64              // 无锁批量计数器
}
```

**设计意图**: 延迟 2-3 个 epoch 释放页面，避免并发场景下的 use-after-free。批量推进 epoch 减少 mutex 获取频次。

### 1.1 CPU Profile 实测数据

> 基于 `cpu-8t-lock-analysis.prof`（823ms 采样，3.54s 总 CPU）

| 指标 | 数值 | 说明 |
|------|------|------|
| `EpochBasedFreeList` 方法 | **不可观测** | Go 编译器完全内联 `Add()`/`AdvanceEpochNow()`，pprof 中无独立符号 |
| `handleSplitOffHeapSync` | **2.26%**（0.08s） | FreeList 的主要调用方（分裂路径） |
| 全局 mutex 相关 | `lock2` 6.21% + `lockSlow` 1.98% + `futex` 14.12% | **所有 mutex 的汇总**，无法单独归因到 FreeList.mu |
| PageLock 竞争 | `setWithLeafLock` 37.29% + `futex` 14.12% + `procyieldAsm` 5.65% | 远超 FreeList |
| `pm.Free()` | 非 I/O 操作 | 仅入队 delayedFreeList + atomic 计数器递减（`offheap/page_manager.go:106-114`） |

**结论**：在当前 profile 中（成功率 2.98%），FreeList.mu 竞争**不是主要瓶颈**。主要瓶颈是 PageLock TryLock 竞争。FreeList 竞争在 PageLock 优化（Phase-PageLock）实施后会凸显，因为分裂频率将大幅上升。

---

## 2. 锁获取路径全景

### 2.1 方法级锁分析

| 方法 | 锁操作 | 持锁时间 | 调用频率 |
|------|--------|----------|----------|
| `Add()` | `Lock → map append → Unlock` | 短（O(1) append） | 每次释放页面 |
| `AdvanceEpochNow()` | `Lock → epoch++ → map delete → pm.Free → Unlock` | 短（pm.Free 非 I/O） | 每 1000 次 AdvanceEpoch 触发 1 次 |
| `AdvanceEpoch()` | 无锁（atomic 计数判断）→ 条件调用 `AdvanceEpochNow` | 取决于 batch | 每次成功操作 |
| `TryAdvanceEpoch()` | 无锁（atomic add） | 无 | 每次 `AdvanceEpoch` 内部 |
| 活锁检测（`leaf_lock_set.go:286`） | `Lock → 遍历 pending 列表 → Unlock` | 中（O(n) 遍历） | 每次叶子分裂 |

### 2.2 调用点完整清单

#### `Add()` — 释放单个页面（14 处）

| # | 文件:行 | 场景 | 说明 |
|---|---------|------|------|
| 1 | `leaf_lock_set.go:424` | 父节点 CAS 失败回滚 | 释放新父页面 |
| 2 | `leaf_lock_set.go:533` | 叶子分裂成功 | 释放旧叶子页面 |
| 3 | `leaf_lock_set.go:693` | 父节点 CAS 失败回滚 | 释放新父页面 |
| 4-6 | `leaf_lock_set.go:700-702` | 循环引用检测回滚 | 释放 newParent + left + right |
| 7-9 | `leaf_lock_set.go:710-712` | 分裂完整性验证回滚 | 释放 newParent + left + right |
| 10 | `leaf_lock_set.go:826` | 祖父节点 CAS 失败回滚 | 释放新祖父页面 |
| 11 | `leaf_lock_set.go:1260` | 内部分裂 CAS 失败回滚 | 释放新父页面 |
| 12 | `leaf_lock_set.go:1276` | 内部分裂成功 | 释放旧内部页面 |
| 13 | `leaf_lock_set.go:1474` | 根分裂成功 | 释放旧根页面 |
| 14 | `btree.go:1008` | Delete 更新父页面 | 释放旧父页面 |

#### `AdvanceEpoch()` — 推进 epoch（5 处）

| # | 文件:行 | 场景 | 说明 |
|---|---------|------|------|
| 1 | `btree_ops.go:197` | Set 成功（fast retry 路径） | 批量推进 |
| 2 | `btree.go:579` | Get 成功（Delta Chain 命中） | 批量推进 |
| 3 | `btree.go:600` | Get 成功（OffHeap 命中） | 批量推进 |
| 4 | `btree.go:669` | Set 成功（setDirect 路径） | 批量推进 |
| 5 | `btree.go:1017` | Delete 成功 | 批量推进 |

#### 直接访问 mutex（1 处）

| # | 文件:行 | 场景 | 说明 |
|---|---------|------|------|
| 1 | `leaf_lock_set.go:286-295` | 叶子分裂活锁检测 | 遍历 pending 列表统计同一页面出现次数 |

---

## 3. 调用频次估算

### 3.1 场景分析

基于基准测试条件（8 线程, 50K ops/thread, 5K init keys, 400K total ops）：

| 操作路径 | 调用 `Add()` 次数 | 调用 `AdvanceEpoch()` 次数 | mutex 获取次数 |
|----------|-------------------|---------------------------|----------------|
| **正常 Set（无分裂）** | 0 | 1 | 0 或 1（1/1000 概率触发 AdvanceEpochNow） |
| **叶子分裂 + 父更新成功** | 1（旧叶子） | 1 | 1 + 0/1 = **1-2** |
| **叶子分裂 + 父 CAS 失败** | 2（旧叶子 + 新父） | 0（返回 ErrRetry） | **2** |
| **叶子分裂 + 循环引用回滚** | 4（旧叶子 + 新父 + 左 + 右） | 0 | **4** |
| **叶子分裂 + 完整性回滚** | 4（同上） | 0 | **4** |
| **根分裂** | 1（旧根） | 1 | 1 + 0/1 = **1-2** |
| **内部分裂成功** | 1（旧内部页面） | 1 | **1-2** |
| **活锁检测** | 0（单独） | 0 | **1**（额外，与上面叠加） |

### 3.2 总频次估算

**假设**：
- 400K ops，2.98% 成功率（11,913 次成功）
- 叶子分裂率约 2-5%（BTree 页面满时触发）
- 活锁检测仅在有 pending 页面时触发

| 操作类型 | 估算次数 | Add() 调用 | mutex 获取 |
|----------|----------|------------|------------|
| 成功的 Set（无分裂） | ~11,000 | 0 | ~11（11,000/1000 batch） |
| 叶子分裂成功 | ~500 | 500 | ~500 |
| 分裂失败/回滚 | ~1,000 | ~2,000-4,000 | ~2,000-4,000 |
| 活锁检测 | ~500 | 0 | ~500 |
| AdvanceEpoch batch 触发 | ~11 | 0 | ~11 |
| **合计** | — | **~2,500-5,000** | **~3,000-5,000** |

**关键结论**：分裂失败路径贡献了约 60-80% 的 mutex 竞争。高频重试导致分裂场景被反复进入。

---

## 4. 竞争热点分析

### 4.1 热点 1：`Add()` 全局 mutex（btree.go:223-228）

```go
func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    e.mu.Lock()                          // ← 每次释放页面都拿全局锁
    epoch := e.currentEpoch.Load()
    e.pending[epoch] = append(...)       // map 写操作
    e.mu.Unlock()
}
```

**问题**：
- 单次分裂中 `Add()` 被连续调用 3-7 次，每次都拿同一把全局 mutex
- Go 的 `sync.Mutex` 在竞争时会触发 `runtime.procyield` + `runtime.futex`
- 8 线程同时分裂时，所有线程串行化在这把锁上

**竞争场景**：
```
Thread-1: Add(pageA) → Lock ✓ → Unlock
Thread-2: Add(pageB) → Lock ✗ (blocked) → ...
Thread-3: Add(pageC) → Lock ✗ (blocked) → ...
...
Thread-8: Add(pageH) → Lock ✗ (blocked) → ...
```

### 4.2 热点 2：活锁检测直接访问 mutex（leaf_lock_set.go:286-295）

```go
// handleSplitOffHeapSync 中，每次叶子分裂前执行：
b.epochBasedFreeList.mu.Lock()
currentEpoch := b.epochBasedFreeList.currentEpoch.Load()
currentEpochPending := b.epochBasedFreeList.pending[currentEpoch]
pendingCount := 0
for _, pid := range currentEpochPending {
    if pid == leafPageID {
        pendingCount++
    }
}
b.epochBasedFreeList.mu.Unlock()
```

**问题**：
1. **与 Add() 竞争同一把锁** — 分裂路径中先做活锁检测（拿锁），后续多次 Add（再拿锁）
2. **O(n) 遍历** — 遍历整个 pending 列表查找特定页面 ID，持锁时间长
3. **绕过封装** — 直接访问内部 mutex 和 pending map，破坏抽象

### 4.3 热点 3：`AdvanceEpochNow()` 持锁时间

```go
func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    e.mu.Lock()
    defer e.mu.Unlock()
    newEpoch := e.currentEpoch.Add(1)
    // 遍历 epochToDelayed 的所有页面调用 pm.Free
    for _, pid := range pagesToDelayed {
        pm.Free(uint32(pid))  // 非 I/O，仅入队 delayedFreeList + atomic 递减
    }
    // ...
}
```

**核实结果**：`pm.Free()` 不是 I/O 操作（`offheap/page_manager.go:106-114`），仅执行 `delayedFreeList.Enqueue` + `used.Add(^uint32(0))`。持锁时间主要取决于 pending 列表长度，不涉及 mmap。**此热点严重程度低于预期**，但仍存在批量释放时的锁持有时间问题。

---

## 5. 三层竞争叠加

```
时间线（单次叶子分裂）:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
T1: 活锁检测 → mu.Lock → 遍历 pending → mu.Unlock
T2: Add(旧叶子) → mu.Lock → map append → mu.Unlock
T3: Add(新父) → mu.Lock → map append → mu.Unlock    ← 如果 CAS 失败
T4: Add(left) → mu.Lock → map append → mu.Unlock     ← 如果回滚
T5: Add(right) → mu.Lock → map append → mu.Unlock    ← 如果回滚
T6: AdvanceEpoch → mu.Lock → batch Free → mu.Unlock  ← 如果 batch 触发
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

8 线程并发时：8 × 6 = 48 次 mutex 竞争（单轮分裂）
```

---

## 6. 优化方案

### 6.1 方案 A：MPSC 无锁队列

**核心思路**：`Add()` 改用无锁 CAS 链表，消除热路径上的 mutex。

**复杂度**：中 | **预期提升**：消除分裂路径 80-90% mutex 竞争 | **工作量**：1-2 天

### 6.2 方案 B：活锁检测嵌入 PageRef

**核心思路**：在 `PageRef` 结构中嵌入 `splitAttemptCount atomic.Int64`，完全无锁。

```go
// page_ref.go — 在 PageRef 结构中添加字段
type PageRef struct {
    pInfo     atomic.Pointer[PageInfo]
    parentRef atomic.Value
    pageLock  atomic.Pointer[PageLock]
    deltaChain atomic.Pointer[COWDeltaRef]

    splitAttemptCount atomic.Int64  // 新增：活锁检测计数器（随页面生命周期管理）
}

// 活锁检测（无锁，不访问 FreeList 内部）
func (r *PageRef) RecordSplitAttempt() bool {
    return r.splitAttemptCount.Add(1) > 15
}

func (r *PageRef) ResetSplitAttempt() {
    r.splitAttemptCount.Store(0)
}
```

**优势**（vs sync.Map / 分片 mutex）：
- **完全无锁**：`atomic.Int64` 操作，~2ns，无 mutex 竞争
- **自动生命周期管理**：随 PageRef 创建/销毁，无需手动清理无用条目
- **零额外内存**：8 字段嵌入已有结构体，无独立 map 开销

**调用修改**（仅 1 处）：
```go
// leaf_lock_set.go:284-298 — 替换活锁检测
// Before:
b.epochBasedFreeList.mu.Lock()
// ... O(n) 遍历 pending map ...
b.epochBasedFreeList.mu.Unlock()

// After:
if leafRef.RecordSplitAttempt() {
    // 同一个页面被多次尝试分裂，使用备用策略
}
```

**复杂度**：低 | **预期提升**：消除活锁检测路径 mutex，减少 ~10-15% 总竞争 | **工作量**：0.5 天

### 6.3 方案 C：分片 FreeList

**核心思路**：按 pageID 分片到多个独立 FreeList 实例。

**复杂度**：低 | **预期提升**：减少 70-80% 竞争（但不消除 mutex 本身） | **工作量**：0.5 天

### 6.4 方案 D：Add 批量接口

**核心思路**：添加 `AddBatch()` 方法，一次 mutex 获取添加多个页面。

```go
func (e *EpochBasedFreeList) AddBatch(pageIDs []model.PageID) {
    e.mu.Lock()
    epoch := e.currentEpoch.Load()
    e.pending[epoch] = append(e.pending[epoch], pageIDs...)
    e.mu.Unlock()
}
```

**调用侧重构**：
```go
// Before: 4 次 mutex
b.epochBasedFreeList.Add(newParentPageID)
b.epochBasedFreeList.Add(leftPageID)
b.epochBasedFreeList.Add(rightPageID)

// After: 1 次 mutex
b.epochBasedFreeList.AddBatch([]model.PageID{newParentPageID, leftPageID, rightPageID})
```

**复杂度**：极低 | **预期提升**：分裂回滚路径减少 60-75% mutex 竞争 | **工作量**：0.5 天

### 6.5 方案 E：`AdvanceEpochNow` 缩小临界区

**核心思路**：锁内仅操作 map，`pm.Free` 移到锁外。

> **核实结论**：`pm.Free()` 不是 I/O 操作（仅 `delayedFreeList.Enqueue` + atomic 递减），锁外执行收益有限。但批量释放数百页面时，缩小临界区仍有价值。

```go
func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    var pagesToDelayed []model.PageID
    var pagesToFree []model.PageID

    e.mu.Lock()
    newEpoch := e.currentEpoch.Add(1)

    if newEpoch >= 2 {
        pagesToDelayed = e.pending[newEpoch-2]
        delete(e.pending, newEpoch-2)
    }
    if newEpoch >= 3 {
        delete(e.pending, newEpoch-3)
    }
    e.mu.Unlock()  // ← 提前释放

    // 以下操作在锁外
    for _, pid := range pagesToDelayed {
        pm.Free(uint32(pid))
    }
    if len(pagesToFree) > 0 || newEpoch >= 3 {
        pm.AdvanceDelayedFreeList()
    }
}
```

**复杂度**：极低 | **预期提升**：减少 AdvanceEpochNow 持锁时间 | **工作量**：0.5 天

---

## 7. 推荐方案组合

| 优先级 | 方案 | 复杂度 | 预期效果 | 工作量 |
|--------|------|--------|----------|--------|
| **Phase-FreeList-1** | B: PageRef 嵌入活锁检测 | 低 | 消除活锁检测 mutex + 修复代码坏味道 | 0.5 天 |
| **Phase-FreeList-1** | D: AddBatch | 极低 | 分裂路径减少 60-75% mutex | 0.5 天 |
| **Phase-FreeList-1** | E: 缩小临界区 | 极低 | 减少 AdvanceEpochNow 持锁时间 | 0.5 天 |
| **Phase-FreeList-2** | A: MPSC 无锁队列 | 中 | 消除 Add 路径全部 mutex | 1-2 天 |

**执行策略**：
- Phase-FreeList-1 的三个方案**同批次实施**（1-1.5 天），改动量小、风险低
- 实施后基准测试评估，再决定是否需要 Phase-FreeList-2

---

## 8. 方案详细设计

### 8.1 方案 B：PageRef 嵌入活锁检测（Phase-FreeList-1）

**改动文件**：`page_ref.go` + `leaf_lock_set.go`

```go
// === page_ref.go ===
type PageRef struct {
    pInfo             atomic.Pointer[PageInfo]
    parentRef         atomic.Value
    pageLock          atomic.Pointer[PageLock]
    deltaChain        atomic.Pointer[COWDeltaRef]
    splitAttemptCount atomic.Int64  // 新增
}

func (r *PageRef) RecordSplitAttempt() bool {
    return r.splitAttemptCount.Add(1) > 15
}

func (r *PageRef) ResetSplitAttempt() {
    r.splitAttemptCount.Store(0)
}
```

```go
// === leaf_lock_set.go:284-298 ===
// Before:
b.epochBasedFreeList.mu.Lock()
currentEpoch := b.epochBasedFreeList.currentEpoch.Load()
currentEpochPending := b.epochBasedFreeList.pending[currentEpoch]
pendingCount := 0
for _, pid := range currentEpochPending {
    if pid == leafPageID {
        pendingCount++
    }
}
b.epochBasedFreeList.mu.Unlock()
if pendingCount > 15 { ... }

// After:
if leafRef.RecordSplitAttempt() {
    // 活锁检测：同一页面被多次尝试分裂，使用备用策略
}
```

### 8.2 方案 D：AddBatch 批量接口（Phase-FreeList-1）

**改动文件**：`btree.go` + `leaf_lock_set.go`

```go
// === btree.go: 新增方法 ===
func (e *EpochBasedFreeList) AddBatch(pageIDs []model.PageID) {
    if len(pageIDs) == 0 {
        return
    }
    e.mu.Lock()
    epoch := e.currentEpoch.Load()
    e.pending[epoch] = append(e.pending[epoch], pageIDs...)
    e.mu.Unlock()
}
```

**14 处调用点重构**（模式统一，改动可控）：

| 调用模式 | 原代码 | 重构后 |
|----------|--------|--------|
| 单次 Add（7 处） | `Add(pageA)` | 保持不变（单次调用无优化空间） |
| 连续 Add — 回滚（4 处） | `Add(A); Add(B); Add(C)` | `AddBatch([]PageID{A, B, C})` |
| 连续 Add — 分裂（3 处） | `Add(旧叶子); Add(新父)` | `AddBatch([]PageID{旧叶子, 新父})` |

### 8.3 方案 A：MPSC 无锁队列（Phase-FreeList-2）

#### 8.3.1 数据结构

```go
// freeNode 是 MPSC 栈的节点
type freeNode struct {
    pageID model.PageID
    epoch  uint64
    next   atomic.Pointer[freeNode]
}

// EpochBasedFreeList V2：MPSC + 批量消费
type EpochBasedFreeList struct {
    currentEpoch atomic.Uint64

    // MPSC Treiber 栈（无锁 push，单线程 pop）
    stackHead   atomic.Pointer[freeNode]

    // 仅 AdvanceEpochNow 使用（低频 mutex）
    pending     map[uint64][]model.PageID
    mu          sync.Mutex

    batchSize    int
    batchCounter atomic.Int64
}
```

#### 8.3.2 核心方法

```go
// Add — 无锁 MPSC push
func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    epoch := e.currentEpoch.Load()
    node := &freeNode{pageID: pageID, epoch: epoch}
    for {
        old := e.stackHead.Load()
        node.next.Store(old)
        if e.stackHead.CompareAndSwap(old, node) {
            return
        }
    }
}

// AddBatch — 无锁批量 push
func (e *EpochBasedFreeList) AddBatch(pageIDs []model.PageID) {
    epoch := e.currentEpoch.Load()
    var first, last *freeNode
    for _, pid := range pageIDs {
        node := &freeNode{pageID: pid, epoch: epoch}
        if first == nil {
            first = node
        } else {
            last.next.Store(node)
        }
        last = node
    }
    if first == nil {
        return
    }
    for {
        old := e.stackHead.Load()
        last.next.Store(old)
        if e.stackHead.CompareAndSwap(old, first) {
            return
        }
    }
}

// drainStack — 将 MPSC 栈内容转移到 pending map（在 mutex 保护下）
func (e *EpochBasedFreeList) drainStack() {
    head := e.stackHead.Swap(nil)
    for node := head; node != nil; {
        e.pending[node.epoch] = append(e.pending[node.epoch], node.pageID)
        next := node.next.Load()
        node.next.Store(nil)  // 防止 ABA：清空 next 指针
        node = next
    }
}

// AdvanceEpochNow — 推进 epoch
func (e *EpochBasedFreeList) AdvanceEpochNow(pm *offheap.PageManager) {
    e.mu.Lock()
    defer e.mu.Unlock()

    e.drainStack()

    newEpoch := e.currentEpoch.Add(1)
    epochToDelayed := newEpoch - 2
    if newEpoch >= 2 {
        pagesToDelayed := e.pending[epochToDelayed]
        delete(e.pending, epochToDelayed)
        for _, pid := range pagesToDelayed {
            pm.Free(uint32(pid))
        }
    }
    epochToFree := newEpoch - 3
    if newEpoch >= 3 {
        delete(e.pending, epochToFree)
        pm.AdvanceDelayedFreeList()
    }
}
```

#### 8.3.3 ABA 防护说明

**风险**：Treiber 栈的 ABA 问题 — 节点从栈弹出后被回收到 `sync.Pool`，立即被新 `Add()` 重用，此时 CAS 可能错误成功。

**防护措施**：
1. **不使用 `sync.Pool`**：`freeNode` 使用一次性分配（`&freeNode{}`），不回池。`drainStack` 消费后由 GC 回收。代价是每次 `Add` 分配 ~32 字节（2 个 uint64 + 1 个指针），但 `Add` 仅在分裂路径调用，频率可控
2. **清空 next 指针**：`drainStack` 中 `node.next.Store(nil)`，即使节点内存被复用也不会形成环
3. **Go 的 atomic 保证顺序一致性**：`atomic.Pointer[freeNode]` 的 `Load`/`Store`/`CompareAndSwap` 提供完整的内存屏障，无需额外 barrier

**Go 的 CAS 安全性**：Go 的 `atomic.Pointer[T].CompareAndSwap` 是指针宽度的原子操作，在 64 位平台上天然避免 torn read，ABA 风险仅来自逻辑层面（节点复用），不来自硬件层面。

---

## 9. 预期性能收益

| 指标 | 当前 | Phase-1 (B+D+E) | Phase-1+2 (+MPSC) |
|------|------|------------------|-------------------|
| `Add()` mutex 竞争 | 2,500-5,000 次 | 800-1,500 次 | **0 次** |
| 活锁检测 mutex | ~500 次 | **0 次** | **0 次** |
| `AdvanceEpochNow` mutex | ~11 次 | ~11 次 | ~11 次 |
| 总 mutex 获取 | 3,000-5,500 | 800-1,500 | **~11** |
| 预期吞吐提升 | — | +5-8% | +10-15% |

> 注：在当前 profile（成功率 2.98%）下，FreeList mutex 竞争不是主要瓶颈。Phase-PageLock（spin wait 优化）实施后成功率提升到 30-50%，分裂频率上升，FreeList 竞争才会凸显为瓶颈。

---

## 10. 依赖关系

```
BTree 优化依赖图：

Phase-PageLock（PageLock spin wait）← 已识别为最高优先级
  └── 解决后成功率从 2.98% 提升到 30-50%
      └── 分裂频率上升 → FreeList 竞争加剧
          └── Phase-FreeList-1（AddBatch + PageRef 活锁 + 缩小临界区）
              └── 基准测试评估
                  └── 如仍不足 → Phase-FreeList-2（MPSC 无锁队列）
```

**建议**：在 Phase-PageLock 实施并验证后，再推进 Phase-FreeList-1。
