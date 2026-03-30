# NexKV CCOW 架构锁设计分析报告

**日期**: 2026-03-29  
**分析主题**: 为什么 CCOW 架构的 BTree 仍需要锁？锁在保护什么？  
**基准**: Lealone BTree 无锁设计对比

---

## 一、核心问题

### 1.1 理论预期 vs 实际实现

| 架构 | 理论预期 | NexKV 实际 | Lealone 实际 |
|------|----------|------------|--------------|
| **Get (读)** | 完全无锁 | 无锁 ✅ | 完全无锁 ✅ |
| **Set (写)** | 无锁/乐观锁 | 多锁竞争 ❌ | 单写线程队列 ✅ |

### 1.2 关键疑问

> "既然是 CCOW 架构，为什么 Set 操作还需要 PageLock？为什么不是像 Lealone 那样完全无锁？"

**答案**: NexKV 的 CCOW 实现是**"有锁 CCOW"**，而 Lealone 是**"无锁 CCOW"**。两者的根本差异在于**写并发模型**。

---

## 二、NexKV 的锁全景分析

### 2.1 BTree 结构体中的锁

```go
type BTree struct {
    // 1. 关闭状态锁
    closedMu  sync.RWMutex  // 保护 closed 标志
    
    // 2. 持久化全局锁
    writeMu sync.Mutex      // 全局写锁（持久化操作）
    
    // 3. 页面分裂锁（细粒度）
    splitMuMap sync.Map     // map[uint32]*sync.Mutex
    
    // 4. PageRef 缓存锁
    pageRefCache   *PageRefCache  // 内部有 mu sync.RWMutex
    
    // 5. Epoch 延迟释放锁
    epochBasedFreeList *EpochBasedFreeList  // 内部有 mu sync.Mutex
}
```

### 2.2 各锁的作用与必要性分析

#### 2.2.1 `closedMu` - 必要 ✅

**作用**: 保护 `closed` 标志，防止关闭后仍有操作执行。

```go
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    if b.closed {  // 需要锁保护读取
        return ErrClosed
    }
    // ...
}
```

**必要性**: 必须保留，用于优雅关闭。

---

#### 2.2.2 `writeMu` - 条件必要 ⚠️

**作用**: 保护持久化操作的原子性。

```go
// leaf_lock_set.go:191-192
if b.chunkMgr != nil {
    b.writeMu.Lock()         // 全局排他锁
    defer b.writeMu.Unlock()
    // ... 持久化操作 ...
}
```

**分析**:
- 纯内存模式（`chunkMgr == nil`）: **不需要此锁**
- 持久化模式: 需要保证写入原子性

**优化建议**: 可用 WAL + 原子提交替代全局锁。

---

#### 2.2.3 `splitMuMap` - 设计妥协 ⚠️

**作用**: 防止同一页面被多个 goroutine 同时分裂。

```go
// leaf_lock_set.go:174-177
splitMuAny, _ := b.splitMuMap.LoadOrStore(uint32(newPageID), &sync.Mutex{})
splitMu := splitMuAny.(*sync.Mutex)
splitMu.Lock()
```

**问题**:
- 这是**细粒度锁**，但仍有竞争
- Lealone 通过**单写线程**完全避免此问题

---

#### 2.2.4 `PageRefCache.mu` - 性能瓶颈 ❌

**作用**: 保护 PageID → PageRef 的映射缓存。

```go
// btree.go:83
type PageRefCache struct {
    cache map[model.PageID]*PageRef
    mu    sync.RWMutex  // 每次搜索路径都获取
}
```

**问题**:
- 每次 `searchPathWithRefs` 都调用 `GetOrCreate`
- 8 线程并发时，频繁获取 RLock
- CPU Profile 显示 `sync.(*RWMutex).RLock` 占 1.41%

**Lealone 方案**: 使用 `sync.Map` 或原子操作替代。

---

#### 2.2.5 `EpochBasedFreeList.mu` - 可优化 ⚠️

**作用**: 保护延迟释放页面列表。

```go
type EpochBasedFreeList struct {
    pending map[uint64][]model.PageID
    mu      sync.Mutex  // 保护 pending
}
```

**分析**:
- 这是 CCOW 必需的（延迟释放旧版本页面）
- 但可用**批量处理 + 原子操作**减少锁竞争

---

### 2.3 最关键的锁：`PageLock`（页面级锁）

```go
// page_lock.go
type PageLock struct {
    state atomic.Uint32  // 0=unlocked, 1=locked
    mu    sync.Mutex     // 保护 cond
    cond  *sync.Cond     // 等待队列
}
```

**为什么需要 PageLock？**

NexKV 的写流程：
```
SetWithRetryAndQueue
  ├── setWithLeafLock
  │   ├── findLeafPageRef      (无锁搜索路径)
│   ├── leafRef.pageLock.TryLock()  ← 获取页面锁
│   ├── CCOW: copyPath         (复制路径)
│   ├── InsertToOffHeap        (修改新路径)
│   ├── ReplacePage            (CAS 替换)
│   └── Unlock()               ← 释放页面锁
  └── 失败 → ErrRetry → runtime.Gosched()
```

**根本原因**: 
1. **多线程并发写**: 8 个线程同时竞争同一叶子
2. **TryLock 失败立即返回**: 不阻塞，直接 ErrRetry
3. **CCOW 路径复制需要独占**: 复制期间不能有其他修改

---

## 三、与 Lealone 的核心差异

### 3.1 并发模型对比

| 维度 | NexKV | Lealone |
|------|-------|---------|
| **写并发** | 多线程竞争 | 单写线程队列 |
| **锁策略** | 细粒度 PageLock | 无锁（串行化） |
| **重试机制** | TryLock + ErrRetry + Gosched | 无重试 |
| **调度开销** | 28% CPU | 接近 0% |

### 3.2 架构流程对比

#### NexKV: 多线程竞争模型

```
8 线程并发 Set:
┌─────────┐ ┌─────────┐ ┌─────────┐
│ 线程 1  │ │ 线程 2  │ │ 线程 3  │
└────┬────┘ └────┬────┘ └────┬────┘
     │           │           │
     └───────────┼───────────┘
                 ↓
        ┌─────────────────┐
        │  TryLock Leaf   │ ← 7/8 失败
        │  失败→ErrRetry  │
        └────────┬────────┘
                 ↓
        ┌─────────────────┐
        │ runtime.Gosched │ ← 28% CPU
        └────────┬────────┘
                 ↓
        ┌─────────────────┐
        │ 3次后→Scheduler │ ← 单线程串行化
        └─────────────────┘
```

#### Lealone: 单写线程模型

```
多线程并发 Set:
┌─────────┐ ┌─────────┐ ┌─────────┐
│ 线程 1  │ │ 线程 2  │ │ 线程 3  │
└────┬────┘ └────┬────┘ └────┬────┘
     │           │           │
     └───────────┼───────────┘
                 ↓
        ┌─────────────────┐
        │   写请求队列     │ ← 无竞争入队
        └────────┬────────┘
                 ↓
        ┌─────────────────┐
        │   单写线程       │ ← 串行执行
        │  (无锁，无重试)  │
        └────────┬────────┘
                 ↓
        ┌─────────────────┐
        │   CCOW 路径复制  │ ← 独占执行
        └─────────────────┘
```

### 3.3 关键差异总结

| 问题 | NexKV | Lealone |
|------|-------|---------|
| **为什么需要 PageLock？** | 多线程竞争同一页面 | 单线程串行，无需锁 |
| **为什么成功率低？** | 8 线程竞争，7/8 失败 | 100% 成功（无竞争） |
| **为什么调度开销高？** | ErrRetry → Gosched | 无重试，无调度 |
| **为什么复杂？** | 锁 + 重试 + 队列混合 | 简单队列模型 |

---

## 四、锁的必要性结论

### 4.1 各锁的必要性评级

| 锁 | 必要性 | 说明 |
|----|--------|------|
| `closedMu` | ✅ 必须 | 生命周期管理 |
| `writeMu` | ⚠️ 条件 | 仅持久化模式需要 |
| `splitMuMap` | ⚠️ 可优化 | 单写线程可消除 |
| `PageRefCache.mu` | ❌ 可优化 | 可用 sync.Map |
| `EpochBasedFreeList.mu` | ⚠️ 可优化 | 批量处理减少竞争 |
| `PageLock` | ❌ 可避免 | 单写线程模型可消除 |

### 4.2 核心结论

> **NexKV 的锁不是 CCOW 架构必需的，而是"多线程并发写"设计选择的产物。**

**如果改为 Lealone 式的单写线程模型：**
1. ✅ 消除 `PageLock` 竞争
2. ✅ 消除 `splitMuMap`  
3. ✅ 消除 28% 调度开销
4. ✅ 成功率从 3% → 100%
5. ⚠️ 需要重构 TaskScheduler 为写队列

---

## 五、优化建议

### 5.1 短期优化（保持当前架构）

1. **PageLock 增加自旋**
   ```go
   func (l *PageLock) TryLockWithSpin() bool {
       for i := 0; i < 100; i++ {
           if l.TryLock() { return true }
           runtime.Procyield()
       }
       return false
   }
   ```

2. **增大重试次数**
   ```go
   const maxFastRetries = 20  // 从 3 增大
   ```

3. **PageRefCache 改用 sync.Map**
   ```go
   type PageRefCache struct {
       cache sync.Map  // 替代 map+RWMutex
   }
   ```

### 5.2 长期优化（参考 Lealone）

1. **单写线程队列**
   ```go
   type BTree struct {
       writeQueue chan WriteOperation
       // 移除 PageLock
   }
   ```

2. **完全无锁读**
   - 当前已实现 ✅

3. **无锁写（乐观并发）**
   - 参考 Lealone 的 CAS 根指针切换

---

## 六、总结

| 问题 | 答案 |
|------|------|
| CCOW 为什么需要锁？ | NexKV 的 CCOW 是"有锁 CCOW"，锁用于保护多线程并发写 |
| 锁在保护什么？ | 1) 页面修改独占权 2) 元数据一致性 3) 生命周期管理 |
| 可以消除吗？ | 可以，改为 Lealone 式单写线程模型 |
| 推荐方案？ | 短期：自旋 + 增大重试；长期：单写线程队列 |

**最终结论**: NexKV 的锁竞争问题根源不是 CCOW 架构本身，而是**"多线程并发写"**的设计选择。参考 Lealone 的单写线程模型可以彻底消除锁竞争。
