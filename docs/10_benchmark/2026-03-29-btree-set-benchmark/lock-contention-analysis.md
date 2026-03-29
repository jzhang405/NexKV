# BTree 8 线程 Set 锁竞争分析

**日期**: 2026-03-29
**测试条件**: GOGC=500, 8 线程, 50K ops/thread, 5K init keys
**CPU Profile**: `cpu-8t-lock-analysis.prof`
**基准数据**: 400K ops, 11913 成功 (2.98%), 14473 ops/s

---

## 1. CPU 分布概览

| 函数 | flat% | cum% | 说明 |
|------|-------|------|------|
| `runtime.futex` | 14.12% | 14.12% | 线程 park/wakeup 系统调用 |
| `runtime.procyieldAsm` | 5.65% | 5.65% | 自旋等待（lock spin） |
| `runtime.lock2` | 1.98% | 6.21% | Go runtime 内部锁获取 |
| `runtime.schedule/park_m` | - | 28.25% | 调度器开销（goroutine 挂起） |
| `cmpbody` | 4.52% | 4.52% | key 比较实际开销 |
| `pageIDToPtrUnchecked` | 3.11% | 3.11% | mmap 页面指针转换 |
| `linearSearchLeaf` | 1.69% | 9.32% | 叶子线性搜索 |
| `mallocgc` | 1.13% | 11.58% | 内存分配 |
| `errors.Wrapf` | - | 7.91% | 错误格式化开销 |

**锁相关总计**: ~26% CPU 花在 `futex` + `procyield` + `lock2` 上
**调度开销总计**: ~28% CPU 花在 `schedule` + `park_m` + `findRunnable`

---

## 2. 锁竞争热点分析

### 2.1 PageLock TryLock → ErrRetry 循环（主要瓶颈）

**位置**: `leaf_lock_set.go:36-38`, `btree_ops.go:187-212`

**机制**:
```
SetWithRetryAndQueue
  ├── attempt 1: setWithLeafLock → TryLock() 失败 → ErrRetry → runtime.Gosched()
  ├── attempt 2: setWithLeafLock → TryLock() 失败 → ErrRetry → runtime.Gosched()
  ├── attempt 3: setWithLeafLock → TryLock() 失败 → ErrRetry
  └── fallback: SetWithTask → EnqueueWithShard → Wait(ctx) → blocking
```

**问题**:
1. **TryLock 非阻塞但立即返回 ErrRetry** — 8 个线程竞争同一叶子锁时，最多 1 个成功，7 个立即失败
2. **3 次 fast retry 后进入 TaskScheduler** — TaskScheduler 是单线程消费队列，引入额外调度开销
3. **runtime.Gosched() 导致调度风暴** — 每次重试都让出 CPU，触发 `schedule` → `park_m` → `findRunnable` 链路
4. **SetWithTask 引入 channel 同步** — `item.Wait(ctx)` 内部用 `selectgo` + `sellock` 等待

**证据**:
- `runtime.schedule` cum 29.94% — 大量 goroutine 在 park/unpark 之间切换
- `runtime.findRunnable` 11.58% — 调度器忙于寻找可运行的 goroutine
- `runtime.park_m` cum 28.25% — goroutine 被频繁挂起
- `BaseTask.Wait` → `selectgo` → `sellock` — TaskScheduler 的 channel 等待
- `closechan` 出现在 `executeTask` 路径 — 任务完成时 channel 关闭

### 2.2 PageLock.cond Wait/Broadcast

**位置**: `page_lock.go:144-155`

**机制**:
```go
// wait: 阻塞等待锁释放
func (l *PageLock) wait() {
    l.mu.Lock()
    l.cond.Wait()   // 阻塞在 sync.Cond
    l.mu.Unlock()
}

// broadcast: 唤醒所有等待者
func (l *PageLock) broadcast() {
    l.mu.Lock()
    l.cond.Broadcast()  // 唤醒全部
    l.mu.Unlock()
}
```

**问题**:
1. **Broadcast 而非 Signal** — 唤醒所有等待者（thundering herd），但只有 1 个能获取锁
2. **内部 `sync.Mutex` + `sync.Cond`** — 每次 wait/broadcast 都要获取 PageLock 内部的 `mu`，引入额外锁
3. **两层锁嵌套** — `PageLock.state` (atomic) + `PageLock.mu` (sync.Mutex) 并行使用

**证据**:
- `runtime.notesleep` 8.76% — `sync.Cond.Wait` 底层调用
- `runtime.notewakeup` 5.65% — `sync.Cond.Broadcast` 底层调用
- `runtime.futexsleep` 8.76% — 最终的 futex 系统调用

### 2.3 PageRefCache RWMutex

**位置**: `btree.go:79-151`

**机制**:
```go
type PageRefCache struct {
    cache map[model.PageID]*PageRef
    mu    sync.RWMutex
}
```

**调用路径**:
1. `searchPathWithRefs` → `GetOrCreate` → `RLock/RLock` (读路径)
2. `setWithLeafLock` → `Replace` → `Lock` (写路径)

**问题**:
1. **每次 searchPath 都调用 GetOrCreate** — 8 个线程同时搜索路径，频繁获取 RLock
2. **写路径获取排他锁** — `Replace` 操作阻塞所有读操作
3. **single map 全局竞争** — 所有 pageID 共享同一把锁

**证据**:
- `sync.(*RWMutex).RLock` 1.41% — 读锁获取
- `sync.(*Mutex).Lock` 2.54% — 含 PageRefCache 写锁
- `sync.(*Mutex).lockSlow` 1.98% — 锁获取慢路径（竞争失败）

### 2.4 writeMu 全局写锁

**位置**: `leaf_lock_set.go:191-192`

```go
if b.chunkMgr != nil {
    b.writeMu.Lock()         // 全局排他锁
    defer b.writeMu.Unlock()
    // ... 持久化操作 ...
}
```

**分析**:
- 仅在 `chunkMgr != nil` 时生效（纯内存模式下不触发）
- 基准测试使用 `OpenBTree("", ...)` 不涉及持久化
- **当前基准测试不是瓶颈**，但在持久化场景会成为全局序列化点

### 2.5 splitMuMap 分裂锁

**位置**: `leaf_lock_set.go:174-177`, `btree.go:200`

```go
splitMuAny, _ := b.splitMuMap.LoadOrStore(uint32(newPageID), &sync.Mutex{})
splitMu := splitMuAny.(*sync.Mutex)
splitMu.Lock()
```

**问题**:
1. **sync.Map + sync.Mutex 嵌套** — LoadOrStore 本身有原子操作开销
2. **锁粒度为 pageID** — 热点页面分裂时竞争
3. **分裂后返回 ErrRetry** — 分裂成功但整个操作重试，浪费已做的工作

---

## 3. 重试风暴分析

**核心问题**: 2.98% 成功率意味着 97% 的操作在重试

### 重试路径 CPU 开销分解

| 操作 | 单次开销 | 重试倍数 | 总开销占比 |
|------|----------|----------|-----------|
| searchPathWithRefs | ~18% CPU | ×33 | 占满 |
| linearSearchLeaf | ~9% CPU | ×33 | 占满 |
| PageLock TryLock | ~0.85% CPU | ×33 | ~28% |
| runtime.Gosched | ~28% CPU | ×33 | 占满 |
| NewPageInfo | ~1.5% CPU | ×33 | ~50% |
| errors.Wrapf | ~8% CPU | ×33 | 占满 |

**关键洞察**: 3 次 fast retry + scheduler fallback 模式下：
- 前 3 次重试（fast path）: ~3% 成功（假设均匀分布）
- 剩余 97% 进入 TaskScheduler
- TaskScheduler 是单线程消费，导致 **8 个线程串行化**
- TaskScheduler 内部又调用 `setWithLeafLock`，同样可能重试

### 锁持有时间分析

**PageLock 临界区**（`leaf_lock_set.go:36-216`）:
```
TryLock → 整个 setWithLeafLock 函数 → Unlock
```
临界区包含:
1. InsertToOffHeap（OffHeap 操作）~13% CPU
2. ReplacePage（CAS 操作）
3. handleSplitOffHeapSync（分裂操作，含父节点锁获取）
4. writeMu 持久化（如果启用）

**估计持有时间**: 单次操作 10-50μs（含分裂时更长）

---

## 4. 调度开销链

```
SetWithRetryAndQueue
  ├── setWithLeafLock (fast retry, 3次)
  │   ├── TryLock 失败 → ErrRetry
  │   ├── runtime.Gosched() → schedule → park_m → futex
  │   └── futex wakeup → findRunnable → startlockedm
  │
  └── SetWithTask (fallback)
      ├── EnqueueWithShard → SchedulerCore.runLoop
      ├── item.Wait(ctx) → selectgo → sellock → park
      └── 任务完成 → closechan → notewakeup → futexwakeup
```

**调度开销占比**:
- `runtime.schedule`: 29.94% cum
- `runtime.park_m`: 28.25% cum
- `runtime.findRunnable`: 11.58% cum
- `runtime.futex`: 14.12% flat
- **总计**: ~30-40% CPU 在调度相关操作上

---

## 5. 优化方向建议

### P0: 减少 ErrRetry 发生率

**方案 A: PageLock 增加短暂自旋**
- TryLock 失败后先自旋 10-100 次（PAUSE 指令），而非立即返回 ErrRetry
- 预期效果: 减少 50-70% 的 ErrRetry，因为锁持有时间短（10-50μs）
- 实现: 修改 `leaf_lock_set.go:36` 处的 TryLock 调用

**方案 B: 增大 fast retry 次数**
- 从 `maxFastRetries=3` 增大到 10-20
- 避免过早进入 TaskScheduler 单线程消费模式
- 预期效果: 直接提升成功率 3-5 倍

### P1: PageLock 优化

**方案 A: Signal 替代 Broadcast**
- `page_lock.go:124` — Unlock 时只唤醒一个等待者
- 避免 thundering herd 问题
- 预期效果: 减少 50% 的无效唤醒

**方案 B: 消除 PageLock 内部 mu + cond**
- 用纯 atomic CAS + runtime.Park/Ready 替代 sync.Cond
- 减少两层锁嵌套开销
- 预期效果: 减少 20-30% 锁操作开销

### P2: PageRefCache 优化

**方案 A: sync.Map 替代 RWMutex + map**
- 利用 sync.Map 的无锁读路径
- 适合读多写少场景
- 预期效果: 消除 PageRefCache 竞争

**方案 B: 分片 PageRefCache**
- 按 pageID 分片，每个分片独立锁
- 预期效果: 消除全局锁竞争

### P3: EpochBasedFreeList 锁竞争

**问题 1: 活锁检测直接拿全局 mutex**（`leaf_lock_set.go:286-295`）
```go
// handleSplitOffHeapSync 热路径上：
b.epochBasedFreeList.mu.Lock()           // ← 全局排他锁！
currentEpoch := e.currentEpoch.Load()
// ... 遍历 pending 列表做活锁检测 ...
b.epochBasedFreeList.mu.Unlock()
```
- **影响**: 8 个线程同时分裂时，全部阻塞在这个 mutex 上
- **活锁检测本身需要锁保护**: `pending` 是普通 map，无锁访问不安全

**问题 2: Add() 每次拿 mutex**（`btree.go:223-228`）
```go
func (e *EpochBasedFreeList) Add(pageID model.PageID) {
    e.mu.Lock()                          // ← 每次释放页面都拿全局锁
    epoch := e.currentEpoch.Load()
    e.pending[epoch] = append(...)
    e.mu.Unlock()
}
```
- **影响**: 分裂操作产生多个待释放页面（父/左/右），每次 `Add` 竞争同一个 mutex
- **调用频率**: 分裂路径中 `Add` 被调用 3-7 次（`leaf_lock_set.go:424,533,693-712,826,1260,1276,1474`）

**方案 A: MPSC 无锁队列替代 mutex + map**
- `Add` 用无锁 CAS 链表入队，消除 mutex
- `AdvanceEpochNow` 仍是单线程消费（已有 TaskScheduler 保证）
- 预期效果: 消除 Add 路径的 mutex 竞争

**方案 B: 活锁检测用 atomic 替代 mutex**
- 用 `atomic.Int64` 计数器记录每个页面的分裂尝试次数
- `Add` 时 atomic increment，检测时 atomic load
- 预期效果: 消除活锁检测路径的 mutex

**方案 C: 分片 EpochBasedFreeList**
- 按 pageID 分片，每个分片独立 mutex
- 预期效果: 减少 8 倍竞争概率

### P4: 减少无效工作

**方案 A: 分裂后不返回 ErrRetry**
- `handleSplitOffHeapSync` 成功后直接返回 nil，而非 ErrRetry
- 当前分裂后重新搜索路径浪费已做的工作
- 预期效果: 分裂场景成功率从 ~0% 提升到接近 100%

**方案 B: 错误格式化优化**
- `errors.Wrapf` 占 7.91% CPU（重试路径的 error 创建）
- 用预分配错误替代 fmt.Sprintf 格式化
- 预期效果: 减少 5-8% CPU

---

## 6. 完整锁清单（btree 目录，排除 _test.go）

### 6.1 结构体级锁

| # | 锁 | 类型 | 文件:行 | 保护对象 | 热路径 | 争用风险 |
|---|-----|------|---------|---------|--------|---------|
| **1** | `BTree.mu` | `sync.RWMutex` | `btree.go:83` | rootRef 读写、Get/Set/Delete 全操作 | **每次 Set/Get 必经** | 🔴 极高 |
| **2** | `BTree.writeMu` | `sync.Mutex` | `btree.go:190` | 持久化操作（chunkMgr 非空时） | Set 成功后（持久化模式） | 🟡 中（纯内存不触发） |
| **3** | `BTree.closedMu` | `sync.RWMutex` | `btree.go:164` | closed 标志 | Close() | 🟢 低 |
| **4** | `BTree.splitMuMap` | `sync.Map` → `*sync.Mutex` | `btree.go:200` | 按页面分裂协调 | 分裂时按 pageID | 🟡 中（热点页分裂） |
| **5** | `BTree.epochBasedFreeList.mu` | `sync.Mutex` | `btree.go:211` | epoch → pending map | 分裂释放页面 + 活锁检测 | 🔴 高 |
| **6** | `PageRefCache.mu` | `sync.RWMutex` | `btree.go:83` | cache map[PageID]*PageRef | **每次 searchPath 必经** | 🔴 极高 |
| **7** | `PageLock.state` | `atomic.Int64` | `page_lock.go:24` | 叶子级锁状态（CAS） | TryLock/Lock/Unlock | 🔴 极高 |
| **8** | `PageLock.mu` | `sync.Mutex` | `page_lock.go:25` | 保护 sync.Cond | Lock 阻塞路径 | 🟡 中 |
| **9** | `COWDeltaRef.mu` | `sync.RWMutex` | `cow_delta_ref.go:80` | Delta 链读写 | Delta 操作（已禁用） | 🟢 低 |
| **10** | `CCOWManager.snapshotMu` | `sync.RWMutex` | `ccow_manager.go:19` | 快照 ID | 快照操作 | 🟢 低 |
| **11** | `ChunkManager.mu` | `sync.RWMutex` | `chunk_manager.go:32` | Chunk 索引 | 持久化路径 | 🟢 低（纯内存不触发） |
| **12** | `Chunk.mu` | `sync.Mutex` | `chunk.go:29` | 文件 I/O | 持久化路径 | 🟢 低 |
| **13** | `PagePersist.mu` | `sync.Mutex` | `page_persist.go:55` | 持久化操作序列化 | 持久化路径 | 🟢 低 |
| **14** | `BTreeGC.mu` | `sync.Mutex` | `btree_gc.go:40` | GC 统计 | 后台 GC | 🟢 低 |
| **15** | `PageLifecycleTracker.mu` | `sync.RWMutex` | `offheap/page_lifecycle_tracker.go:20` | 页面生命周期历史 | 调试/测试 | 🟢 低 |

### 6.2 锁详细分析（按争用风险排序）

---

#### 🔴 锁 #1: `BTree.mu` — 全局读写锁

```
类型: sync.RWMutex
位置: btree.go:83
保护: rootRef 的读写操作
```

**获取位置**:

| 调用点 | 操作 | 文件:行 |
|--------|------|---------|
| `BTree.Get()` | RLock | `btree.go:95` |
| `BTree.Set()` | RLock → 可能升级 Lock | `btree.go:102-103` |
| `BTree.Delete()` | RLock → 可能升级 Lock | `btree.go:116-117` |
| `BTree.Close()` | Lock | `btree.go:132` |
| 根节点分裂 | Lock | `btree.go:139-147` |

**问题**: 每次 `SetWithRetryAndQueue` 调用 `BTree.Set()` 时获取 RLock。8 线程下所有写操作竞争同一把读锁。根节点分裂时升级为写锁，**阻塞所有读写操作**。

---

#### 🔴 锁 #6: `PageRefCache.mu` — 全局缓存锁

```
类型: sync.RWMutex
位置: btree.go:81-84
保护: cache map[PageID]*PageRef
```

**获取位置**:

| 调用点 | 操作 | 文件:行 |
|--------|------|---------|
| `GetOrCreate()` | RLock(读) / Lock(写) | `btree.go:95-127` |
| `Replace()` | Lock | `btree.go:145-151` |
| `Update()` | Lock | `btree.go:131-135` |
| `Delete()` | Lock | `btree.go:138-142` |

**问题**:
- **每次 searchPathWithRefs 都调用 `GetOrCreate`** — 8 个线程同时搜索路径
- 写路径（Replace）获取排他锁时，阻塞所有读操作
- **全局 map** — 所有 pageID 共享同一把锁
- pprof 显示 `sync.(*Mutex).Lock` 2.54%，`sync.(*RWMutex).RLock` 1.41%

---

#### 🔴 锁 #7: `PageLock.state` — 叶子级原子锁

```
类型: atomic.Int64 (CAS)
位置: page_lock.go:24
保护: 叶子页面的互斥访问
```

**获取位置**:

| 调用点 | 操作 | 文件:行 |
|--------|------|---------|
| `setWithLeafLock` | TryLock | `leaf_lock_set.go:36` |
| `setWithLeafLockAndRef` | TryLock | `btree_ops.go:263` |
| `BTree.Set` (direct) | TryLock | `btree.go:828` |
| `handleSplitOffHeapSync` (parent) | TryLock | `leaf_lock_set.go:353,564,811,889,1169` |
| `BTree.Set` (parent) | TryLock | `btree.go:934` |

**问题**:
- **TryLock 失败立即返回 ErrRetry** — 无自旋等待
- 8 线程竞争同一叶子锁时，最多 1 个成功，7 个立即失败
- **级联锁获取** — 分裂时需获取 parentLock + grandParentLock
- 每个叶子页面有独立 PageLock，但热点页面（init keys 集中）竞争激烈

---

#### 🔴 锁 #5: `EpochBasedFreeList.mu` — 页面释放全局锁

```
类型: sync.Mutex
位置: btree.go:211
保护: pending map[uint64][]PageID
```

**获取位置**:

| 调用点 | 操作 | 文件:行 | 说明 |
|--------|------|---------|------|
| `Add()` | Lock | `btree.go:224` | 每次释放页面 |
| `AdvanceEpochNow()` | Lock | `btree.go:239` | epoch 推进 |
| 活锁检测 | Lock | `leaf_lock_set.go:286` | 分裂时检查 pending |

**问题**:
- **热路径全局 mutex** — 分裂时多次调用 `Add()`（3-7 次），每次拿同一把锁
- **活锁检测直接拿锁** — `handleSplitOffHeapSync` 开头就锁住整个 pending map
- **普通 map** — 没有分片或无锁替代
- 调用频率：`leaf_lock_set.go` 中 14 处 `Add()` 调用

---

#### 🔴 锁 #8: `PageLock.mu` — 条件变量内部锁

```
类型: sync.Mutex
位置: page_lock.go:25
保护: sync.Cond (wait/broadcast)
```

**获取位置**:

| 调用点 | 操作 | 文件:行 |
|--------|------|---------|
| `wait()` | Lock → cond.Wait | `page_lock.go:145-147` |
| `broadcast()` | Lock → cond.Broadcast | `page_lock.go:152-154` |

**问题**:
- **Broadcast 唤醒全部** — 只有 1 个能获取锁，其余重新阻塞
- 两层锁嵌套: PageLock.state(atomic) + PageLock.mu(sync.Mutex)
- pprof: `runtime.notesleep` 8.76% + `runtime.notewakeup` 5.65%

---

#### 🟡 锁 #4: `splitMuMap` — 分裂协调锁

```
类型: sync.Map → *sync.Mutex (按 pageID)
位置: btree.go:200
保护: 防止同一页面并发分裂
```

**获取位置**:

| 调用点 | 操作 | 文件:行 |
|--------|------|---------|
| `setWithLeafLock` | LoadOrStore + Lock | `leaf_lock_set.go:174-177` |

**问题**:
- 热点页面分裂时多个线程竞争同一个 pageID 的 mutex
- 分裂完成后返回 ErrRetry（即使分裂成功），浪费已做的工作
- sync.Map.LoadOrStore 有额外原子操作开销

---

#### 🟡 锁 #2: `BTree.writeMu` — 持久化全局写锁

```
类型: sync.Mutex
位置: btree.go:190
保护: 持久化操作序列化
```

**获取位置**:

| 调用点 | 文件:行 |
|--------|---------|
| `setWithLeafLock` (持久化) | `leaf_lock_set.go:191-192` |
| `setWithLeafLockAndRef` (持久化) | `btree_ops.go:330-331` |

**分析**: 仅当 `chunkMgr != nil` 时触发。基准测试用 `OpenBTree("", ...)` 纯内存模式，**当前不影响**。但持久化模式下会成为全局序列化瓶颈。

---

#### 🟢 低争用锁

| 锁 | 类型 | 文件:行 | 说明 |
|----|------|---------|------|
| `BTree.closedMu` | RWMutex | `btree.go:164` | Close() 时才竞争 |
| `COWDeltaRef.mu` | RWMutex | `cow_delta_ref.go:80` | Delta Chain 已禁用 |
| `CCOWManager.snapshotMu` | RWMutex | `ccow_manager.go:19` | 快照操作低频 |
| `CCOWManager.dirtyPages` | sync.Map | `ccow_manager.go:22` | 已用 sync.Map 优化 |
| `PageStats.readCounts` | sync.Map | `page_stats.go:21` | 已用 sync.Map 优化 |
| `PageStats.lastAccess` | sync.Map | `page_stats.go:22` | 已用 sync.Map 优化 |
| `ChunkManager.mu` | RWMutex | `chunk_manager.go:32` | 纯内存不触发 |
| `Chunk.mu` | Mutex | `chunk.go:29` | 纯内存不触发 |
| `PagePersist.mu` | Mutex | `page_persist.go:55` | 纯内存不触发 |
| `BTreeGC.mu` | Mutex | `btree_gc.go:40` | 后台线程 |
| `PageLifecycleTracker.mu` | RWMutex | `offheap/page_lifecycle_tracker.go:20` | 调试用 |

---

### 6.3 锁获取频率估算（8 线程, 50K ops/thread）

单次 `Set` 成功操作的锁获取链路：

```
Set → SetWithRetryAndQueue
  ├── BTree.mu.RLock()                          × 1 (per attempt)
  ├── searchPathWithRefs
  │   └── PageRefCache.GetOrCreate → mu.RLock   × ~3 (树深度)
  ├── PageLock.TryLock() (atomic CAS)            × 1
  ├── InsertToOffHeap (无锁)
  ├── PageRef.ReplacePage (atomic CAS)           × 1
  ├── [分裂路径]
  │   ├── splitMuMap.LoadOrStore                 × 1
  │   ├── splitMu.Lock()                         × 1
  │   ├── parentLock.TryLock()                   × 1
  │   ├── grandParentLock.TryLock()              × 0-1
  │   ├── EpochBasedFreeList.Add() → mu.Lock     × 3-7
  │   ├── EpochBasedFreeList 活锁检测 → mu.Lock  × 1
  │   └── PageRefCache.Replace → mu.Lock         × 1-3
  ├── PageRefCache.Replace → mu.Lock             × 1
  ├── [持久化路径]
  │   └── writeMu.Lock()                         × 0-1 (纯内存跳过)
  └── BTree.mu.RUnlock()                         × 1
```

**重试场景** (97% 操作): 每次重试重复上述锁获取链路，但 TryLock 失败后提前返回：

```
Set → SetWithRetryAndQueue
  ├── BTree.mu.RLock()                           × 1
  ├── PageRefCache.mu.RLock × 3
  ├── PageLock.TryLock() → FAIL                   × 1
  ├── BTree.mu.RUnlock()                          × 1
  └── runtime.Gosched() → schedule → park         × 1
```

**总锁获取次数** (8 线程, 400K 总操作):
- 成功操作 (~12K): ~12 次锁操作/次 = ~144K 次
- 失败重试 (~388K, 平均 3 次重试): ~6 次锁操作/次 × 3 = ~7M 次
- **重试路径锁操作占总量的 ~98%**

---

## 7. 锁竞争火焰图解读

### 热点调用链 (cum% 排序)

```
main.main.func1                                    54.80%
  BTree.Set                                        54.80%
    BTree.SetWithRetryAndQueue                     53.11%
      BTree.setWithLeafLock                        37.29%
        BTree.findLeafPageRef                      18.36%
          BTree.searchPathWithRefs                 18.08%
            OffHeapAdapter.SearchChild             ~5%
        OffHeapAdapter.InsertToOffHeap             13.28%
          OffHeapAdapter.linearSearchLeaf           9.32%
        errors.Wrapf                                7.91%
      runtime.schedule (重试开销)                   29.94%
        runtime.park_m                             28.25%
          runtime.futex                            14.12%
      BTree.SetWithTask (fallback)                 16.38%
        SchedulerCore.runLoop                       9.89%
          executeTask/executeBatch                  6.78%
        BaseTask.Wait (channel 等待)                1.98%
```

### 锁竞争专用链

```
runtime.lock2                            6.21% (实际锁获取)
  ├── runtime.goschedImpl                 6.21% (Gosched 导致)
  ├── runtime.findRunnable               11.58% (调度器搜索)
  └── runtime.closechan                   (channel 关闭)

runtime.notesleep                         8.76% (sync.Cond.Wait)
  └── PageLock.wait                       (阻塞等待)

runtime.procyieldAsm                      5.65% (自旋等待)
  └── runtime.lock2                       (Go 内部锁自旋)
```

---

## 8. 总结

**核心瓶颈**: 不是某个特定锁，而是 **ErrRetry → 重试 → 调度** 的恶性循环:

1. **8 线程竞争少量叶子锁** → 7/8 操作 TryLock 失败
2. **ErrRetry 触发 runtime.Gosched** → goroutine 被挂起
3. **3 次重试后进入 TaskScheduler** → 单线程串行化
4. **TaskScheduler 内部又可能重试** → 雪上加霜
5. **97% 操作在重试** → CPU 浪费在调度而非实际工作上

**最有效的单一优化**: 在 TryLock 失败后增加短暂自旋（方案 P0-A），预期直接将成功率从 3% 提升到 30-50%。
