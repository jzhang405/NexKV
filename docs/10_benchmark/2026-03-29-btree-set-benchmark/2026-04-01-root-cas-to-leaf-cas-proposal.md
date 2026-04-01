# Root CAS → Leaf CAS 架构演进提案

**日期**: 2026-04-01
**问题**: 高并发下 Root CAS 导致 retry exhaustion
**目标**: 实现 Leaf CAS，使分裂成为叶子本地操作

---

## 1. 问题分析

### 1.1 当前架构（NexKV B-Link 变种）

```
┌─────────────────────────────────────────────────────┐
│                       Root                           │
│                  CAS(Root)                          │
│              ┌─────┴─────┐                          │
│          CAS │           │ CAS                     │
│          ┌───┴───┐   ┌───┴───┐                     │
│        CAS       CAS       CAS                     │
│      ┌───┐   ┌───┐   ┌───┐   ┌───┐               │
│     Leaf Leaf Leaf Leaf Leaf Leaf                  │
└─────────────────────────────────────────────────────┘

问题：Leaf 分裂时，如果父节点也需要分裂 → 级联到 Root → CAS 冲突 → retry exhaustion

注：不是每次分裂都传播到 Root，只有当路径上的父节点也需要分裂时才会级联。
```

### 1.2 当前实现与方案的对应关系

| 方面 | NexKV 当前实现 | 对应方案 |
|------|---------------|----------|
| CAS 层级 | 父节点 CAS（handleSplitOffHeapSync） | 方案 A (B-Link) |
| 分裂传播 | 递归向上直到不需要分裂的节点 | 方案 A |
| Epoch 管理 | EpochBasedFreeList | 方案 B |
| 版本检测 | GetVersionSafe, GetChildWithVersion | 方案 D |
| 锁机制 | PageLock（非公平锁） | - |

### 1.4 Lealone 架构（Leaf CAS）

```
┌─────────────────────────────────────────────────────┐
│                       Root                           │
│              (几乎固定，很少 CAS)                      │
│              ┌─────┴─────┐                          │
│          CAS │           │ CAS                      │
│          ┌───┴───┐   ┌───┴───┐                     │
│         Leaf   Leaf   Leaf   Leaf                  │
│       (本地)       (本地)                           │
│                                                         │
│  Leaf 分裂 = 本地操作，不触发 Root CAS                  │
└─────────────────────────────────────────────────────┘
```

### 1.5 关键数据对比

| 指标 | NexKV (Root CAS) | Lealone (Leaf CAS) |
|------|------------------|-------------------|
| 8 线程扩展比 | ~1x (受限于 CAS) | ~3.6x |
| Root CAS 频率 | 每次分裂都可能 | 极少 |
| 分裂传播 | 同步级联向上 | 本地完成 |
| CAS 冲突点 | 所有层级 | 只有 Leaf 本地 |

---

## 2. 核心问题

**为什么 Leaf 分裂需要向上传播更新？**

```
Leaf L 分裂成 L1 + L2:
- 父节点 P 的 child[L] → 需要变成 child[L1] 和 child[L2]
- 如果 P 也满了，P 分裂成 P1 + P2
- 祖父节点 GP 的 child[P] → 需要变成 child[P1] 和 child[P2]
- ... 可能一直传播到 Root
```

**问题本质**：索引结构更新的同步级联传播

---

## 3. 解决方案分析

### 方案 A：B-Link 树风格（Sibling Linked Leaves）

**核心思想**：叶子节点通过 sibling 指针链接，搜索可以"跳过"父节点更新

```
                    Root
                      │
            ┌─────────┴─────────┐
            │                   │
            P                   Q
          / │ \               / | \
         L1 L2 L3            R1 R2 R3
          \│/                 \|/
        [L1 ←→ L2 ←→ L3]    [R1 ←→ R2 ←→ R3]
           ↑ sibling links      ↑ sibling links
```

**搜索算法改进**：
1. 从 Root 下降到叶子
2. 如果当前节点的 child 指针指向的页面正在分裂...
3. ...通过 sibling 链找到正确的新页面

**分裂时不再更新父节点**，而是：
1. 创建 L1, L2
2. L1 ←→ L2 通过 sibling 链接
3. 更新父节点 P 的 child 指针（这是必须的）
4. **但是**：P 的更新不会传播到 Root（如果 P 不需要分裂）

**方案 A 的局限性**：
- 如果 P 也满了，P 分裂确实会传播到 Root
- 这个方案本身不能完全消除 Root CAS
- 但可以**减少**分裂传播的频率（通过 sibling 链让搜索跳过部分更新）
- 需要与方案 C (Path CoW) 或方案 E (SplittedPageInfo) 结合才能根本解决问题

### 方案 B：Epoch + 延迟更新（Deferred Parent Update）

**核心思想**：分裂后不立即更新父节点，而是标记"待更新"，后续操作帮助完成

```
分裂时：
1. Leaf L 分裂成 L1, L2
2. 设置 L = "SPLITTING" 状态，指向 L1, L2
3. 记录更新任务：UpdateParent(L → L1, L2)
4. 立即返回成功

搜索时：
1. 如果遇到 SPLITTING 页面
2. 通过 L1/L2 完成查找
3. 同时应用待定的父节点更新

清理时：
1. 后台任务完成所有待定的父节点更新
2. 清除 SPLITTING 状态
```

**问题**：
- 读路径变复杂（需要检查 SPLITTING 状态）
- 需要额外的协调机制
- 仍然没有解决 Root CAS 问题

### 方案 C：Path Copy-on-Write（路径副本）

**核心思想**：分裂时复制整条路径，原子更新 Root

```
分裂前路径：
Root → P → L

分裂时（Leaf L → L1, L2）：
1. 不修改 P，而是创建 P'（P 的副本）
2. P' 的 child[L] → 变成 child[L1], child[L2] 的分隔
3. 创建新的 Root'，其 child 指向 P'
4. CAS(Root, Root')  ← 只有这里需要 CAS

其他线程继续使用旧路径（Root → P → L）
新线程使用新路径（Root' → P' → L1/L2）
```

**优点**：Root CAS 频率大幅降低
**问题**：内存开销大，需要 GC 清理旧路径

### 方案 D：Version-based Root + Local Split

**核心思想**：Root 持有版本号，Leaf 分裂是版本内操作

```
struct Root {
    version uint64
    children []PageID
}

每次分裂时：
1. Leaf 本地分裂（L → L1, L2）
2. 创建新的 Root'，version++
3. children[] 更新为 [L1, L2, ...] （对于分裂的叶子区间）
4. CAS(Root, Root')

搜索时：
1. 读取 Root.version 和 Root.children
2. 按版本号遍历
3. 如果版本过期，重新读取
```

**关键洞察**：
- **如果分裂只在叶子层完成，不影响内部节点结构，那么 Root 就不需要频繁变化**
- 问题转化为：如何让叶子分裂不影响内部节点结构？

### 方案 E：B-Link 树风格 + SplittedPageInfo（Lealone 方式）

**核心思想**：分裂时不直接更新父节点，而是用 SplittedPageInfo 追踪新页面

```
Lealone 的关键机制：

1. 当 Leaf 分裂时：
   - 创建 SplittedPageInfo，持有新页面的引用
   - 替换原 PageReference 中的 PageInfo

2. 当其他线程访问旧引用时：
   - getOrReadPage() 检测 isDataStructureChanged()
   - 自动跟随到新页面（getNewRef()）

3. 父节点不需要立即更新：
   - 旧引用自动重定向到新页面
   - 后续访问会使用新引用
```

**数据结构**（Lealone 实现）：

```java
// PageInfo.java - 分裂页面信息
public static class SplittedPageInfo extends DataStructureChangedPageInfo {
    private final PageReference pRefNew;  // 指向新页面的引用

    @Override
    public boolean isDataStructureChanged() {
        return true;  // 触发重定向
    }

    @Override
    public PageReference getNewRef() {
        return pRefNew;  // 返回新页面引用
    }
}

// PageReference.java - 获取页面时自动处理分裂
public Page getOrReadPage() {
    PageInfo pInfo = this.pInfo;
    if (pInfo.isDataStructureChanged()) { // 发生 page split 或 page remove
        return pInfo.getNewRef().getOrReadPage();  // 自动跟随新引用
    }
    ...
}
```

**与 NexKV 当前实现的对比**：

| 方面 | NexKV 当前 | Lealone (B-Link) |
|------|------------|------------------|
| 分裂传播 | 同步更新父节点 CAS | 用 SplittedPageInfo 追踪 |
| 旧引用处理 | 变成悬空指针 | 自动重定向到新页面 |
| 父节点更新 | 必须立即完成 | 懒更新 |
| CAS 冲突 | 高（同步传播）| 低（本地分裂）|

### 方案 F：两级索引结构（Two-Level Index）

**核心思想**：引入一个固定不变的顶层索引

```
┌─────────────────────────────────────────────────────┐
│              Level 0: Root (几乎固定)                  │
│              版本号: V1                               │
│              指向: Internal Page Array                │
└─────────────────────────────────────────────────────┘
                         │
┌─────────────────────────────────────────────────────┐
│           Level 1: Internal Page Array                │
│           每个条目指向一个子树根                         │
│           更新方式: 创建新版本，不原地修改               │
└─────────────────────────────────────────────────────┘
                         │
┌─────────────────────────────────────────────────────┐
│              Level 2+: 子树（独立管理）                 │
│              Leaf 分裂在子树内完成                      │
│              子树根变化 → 更新 Level 1 的对应条目        │
└─────────────────────────────────────────────────────┘
```

---

## 4. 可行的演进路径

### 4.1 短期：减少 Root CAS 频率

**目标**：保持 Leaf CAS 能力，减少冲突

```
1. 实现 Path Copy-on-Write（方案 C）
   - 分裂时复制路径而不是修改
   - CAS 只在 Root 层发生

2. 减少分裂传播深度
   - 分析分裂模式
   - 批量分裂，减少分裂次数
```

### 4.2 中期：实现真正的 Leaf CAS

**目标**：让分裂成为叶子本地操作

```
1. 采用 B-Link 树风格
   - Leaf 之间通过 sibling 指针链接
   - 搜索算法支持 "stale child pointer" 情况
   - 分裂后只更新直接父节点，不向上传播

2. 关键改进点：
   - handleSplitOffHeapSync 不再递归调用父节点分裂
   - 父节点更新通过单独的机制完成
   - 实现 SplitLock 或类似机制协调并发分裂
```

### 4.3 长期：两级索引结构

**目标**：完全隔离 Root 和 Leaf 操作

```
1. 引入 SubTree 概念
   - 每个 SubTree 有独立的根
   - SubTree 根变化通过 Layer 1 索引追踪

2. Root 成为版本号 + 索引指针的组合
   - Root 本身不需要分裂
   - 分裂被隔离在 SubTree 内部
```

---

## 5. 推荐方案：方案 E（Lealone B-Link + SplittedPageInfo）

### 5.1 架构设计

```
┌──────────────────────────────────────────────────────┐
│                        Root (Version V)               │
│           CAS 发生在这里，但频率大幅降低                  │
└──────────────────────────────────────────────────────┘
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
    ┌─────┴─────┐     ┌─────┴─────┐     ┌─────┴─────┐
    │Internal[0]│     │Internal[1]│     │Internal[2]│
    │ (P0)      │     │ (P1)      │     │ (P2)      │
    └─────┬─────┘     └─────┬─────┘     └─────┬─────┘
          │                 │                 │
    ┌─────┴─────┐     ┌─────┴─────┐     ┌─────┴─────┐
    │Leaf[L0]   │     │Leaf[L1]   │     │Leaf[L2]   │
    │           │     │(SPLITTING)│     │           │
    │ L0 ← L0' →│     │↻          │     │ L2 ← L2' →│
    └───────────┘     └───────────┘     └───────────┘
                        ↑
                        │
                  Sibling 链: L1 ←→ L1' ←→ ...
```

### 5.2 关键数据结构

```go
// Leaf Page 状态
type LeafState int

const (
    LeafStateNormal    LeafState = iota
    LeafStateSplitting              // 分裂中，待更新父节点
    LeafStateMerging               // 合并中
)

// Leaf Page 扩展
type OffHeapLeafPage struct {
    // 现有字段...
    State       LeafState
    SiblingPrev PageID  // 前驱叶子
    SiblingNext PageID  // 后继叶子
    SplitEpoch  uint64  // 分裂时代/epoch
}

// 分裂任务（延迟执行）
type DeferredSplitTask struct {
    ParentPageID  PageID
    ChildOld     PageID  // 旧的叶子
    ChildLeft     PageID  // 分裂后的左叶子
    ChildRight    PageID  // 分裂后的右叶子
    SplitKey      []byte  // 分裂点 key
    Epoch         uint64
}

// Epoch Manager - 管理分裂/合并任务的完成
type EpochManager struct {
    pendingTasks chan *DeferredSplitTask
    completed    atomic.Int64
}

// 获取叶子时检查状态
func (b *BTree) GetLeafWithSplitCheck(pageID PageID, key []byte) (*LeafRef, error) {
    for {
        page, err := b.offheapAdapter.GetLeafPage(pageID)
        if err != nil {
            return nil, err
        }

        if page.State == LeafStateSplitting {
            // 页面正在分裂，搜索可能走错了
            // 通过 sibling 链找到正确的新页面
            next := page.SiblingNext
            if next == InvalidPageID {
                // 分裂还未完成，使用当前页面（会 retry）
                return b.getLeafRefWithRetry(pageID, key)
            }
            pageID = next
            continue
        }

        return b.getFromLeaf(page, key)
    }
}
```

### 5.3 分裂流程改变

**当前流程（同步级联）**：
```
handleSplitOffHeapSync(leaf):
    1. splitLeaf → left, right
    2. updateParent(left, right)
    3. if parent needs split:
         handleSplitOffSync(parent)  ← 递归！
    4. return retry
```

**新流程（本地分裂 + 延迟更新）**：
```
handleSplitOffHeapSync(leaf):
    1. splitLeaf → left, right
    2. linkSibling(left, right)
    3. setState(leaf, Splitting)
    4. enqueueDeferredTask(parent, leaf → left, right)
    5. return success  ← 不再向上传播！

后台 EpochManager:
    for task := range pendingTasks:
        applyParentUpdate(task)
        if task.parent.needsSplit:
            enqueueDeferredTask(grandparent, task.parent → ...)
```

### 5.4 搜索流程改变

**新搜索算法**：
```
Search(key):
    node = Root
    while !node.IsLeaf:
        child = node.chooseChild(key)
        if child.State == Splitting:
            // Child 正在分裂，可能走错了
            // 尝试通过 sibling 链找到正确分支
            child = resolveSplitChild(child, key)
        node = child
    return node.search(key)
```

---

## 6. 当前代码瓶颈分析

### 6.0 NexKV 特有瓶颈

| NexKV 特有瓶颈 | 当前实现 | 影响 |
|---------------|----------|------|
| `handleSplitOffHeapSync` 递归 | 会递归向上到 Root | 高并发下 CAS 竞争严重 |
| OffHeap 页面 Alloc/Free 与版本管理耦合 | EpochBasedFreeList 管理延迟释放 | 活锁检测 (pendingCount > 15) |
| 分裂与 Epoch 交互 | Epoch 推进可能与分裂并发 | TOCTOU 竞态条件 |
| PageRefCache 与 PageID 重用 | GetOrCreate 检测 pageID 变化 | 假阳性循环引用检测 |

### 6.1 handleSplitOffHeapSync 的问题

```go
// 当前 handleSplitOffHeapSync 的瓶颈点
func (b *BTree) handleSplitOffHeapSync(...) {
    // 问题 1：递归调用可能导致深递归
    // 每次递归都需要重新获取锁和 CAS
    if parentNeedsSplit {
        handleSplitOffHeapSync(parent)  // ← 递归！
    }

    // 问题 2：CAS 失败后的重试在高温 workload 下会耗尽
    // ErrMaxRetry 表明 CAS 冲突频繁
}
```

### 6.2 分裂传播的瓶颈

```
分裂传播路径：
Leaf L → Parent P → Grandparent GP → ... → Root

每个层级都需要：
1. CAS 获取锁
2. 创建新页面
3. 更新父节点
4. CAS 释放锁

如果任何一步 CAS 失败，整个操作需要重试。
```

---

## 7. 性能目标

### 7.1 量化指标

| 指标 | 当前 (Root CAS) | 目标 (Leaf CAS) |
|------|----------------|-----------------|
| 8 线程扩展比 | ~1x | ~3x |
| ErrRetry 率 | >50% (高并发) | <5% |
| 50k ops 延迟 P99 | TBD | <100ms |
| CAS 冲突频率 | 高 | 低 |
| Root CAS 频率 | 每次分裂都可能 | 极少（仅 Root 分裂时）|

### 7.2 验收标准

**单线程验收（已完成）**：
- [x] 50k ops 压力测试通过率 100%
- [x] 无 child zero detected 错误
- [x] 无 stale child reference 错误（单线程）

**多线程验收（待完成）**：
- [ ] 8 线程扩展比达到 2.5x 以上
- [ ] ErrRetry 率在高并发下 < 10%
- [ ] 50k ops 压力测试通过率 > 95%（多线程）

---

## 8. 实施计划

> **注意**：以下时间估算基于保守估计，实际实现可能需要更长。

### Phase 1：验证方案可行性（1 周）

**目标**：验证 SplittedPageInfo 思路在 NexKV 可行

**验证标准**：ErrRetry 率从 98% 下降到 < 50%

#### Phase 1.1：添加 SplitInfo 数据结构（2 天）

**文件**：`internal/infrastructure/storage/btree/offheap/split_info.go`（新建）

```go
// SplitInfo 追踪分裂后的新页面引用
// 替代直接修改 PageInfo，实现旧引用的自动重定向
type SplitInfo struct {
    OriginalPageID PageID      // 原始页面 ID
    NewPageRef    *PageRef    // 分裂后的新页面引用
    SplitKey      []byte      // 分裂点 key
    Timestamp     uint64      // 分裂时间戳
}

// PageInfo 扩展字段
type PageInfo struct {
    // ... 现有字段 ...
    splitInfo unsafe.Pointer // *SplitInfo（原子操作）
    splitEpoch uint64        // 分裂时代，用于检测并发冲突
}

// 原子操作
func (pi *PageInfo) GetSplitInfo() *SplitInfo
func (pi *PageInfo) SetSplitInfo(si *SplitInfo)
func (pi *PageInfo) GetSplitEpoch() uint64
func (pi *PageInfo) SetSplitEpoch(epoch uint64)

// IsRedirecting 判断是否需要重定向
func (pi *PageInfo) IsRedirecting() bool {
    return pi.GetSplitInfo() != nil
}

// GetNewRef 获取分裂后的新引用
func (pi *PageInfo) GetNewRef() *PageRef {
    si := pi.GetSplitInfo()
    if si == nil {
        return nil
    }
    return si.NewPageRef
}

// IsSplitEpochChanged 检测分裂时代是否变化（用于 TOCTOU 检测）
func (pi *PageInfo) IsSplitEpochChanged(initialEpoch uint64) bool {
    return atomic.LoadUint64(&pi.splitEpoch) != initialEpoch
}
```

#### Phase 1.2：修改 handleSplitOffHeapSync 返回 SplitInfo（2 天）

**文件**：`internal/infrastructure/storage/btree/leaf_lock_set.go`

**修改点 1**：`handleSplitOffHeapSync` 返回 `(leftRef, rightRef, splitInfo, error)`

```go
// 修改前
func (b *BTree) handleSplitOffHeapSync(leafRef *PageRef, leafInfo *PageInfo, ...) (*PageRef, error) {
    // ... 分裂逻辑 ...
    return leftRef, nil
}

// 修改后
func (b *BTree) handleSplitOffHeapSync(leafRef *PageRef, leafInfo *PageInfo, ...) (*PageRef, *PageRef, *SplitInfo, error) {
    // ... 分裂逻辑 ...
    // 创建 SplitInfo
    splitInfo := &SplitInfo{
        OriginalPageID: leafPageID,
        NewPageRef:     leftRef,
        SplitKey:       splitKey,
        Timestamp:      atomic.LoadUint64(&b.epochBasedFreeList.currentEpoch),
    }
    return leftRef, rightRef, splitInfo, nil
}
```

**修改点 2**：将 SplitInfo 设置到原始 leafRef

```go
// 在 handleSplitOffHeapSync 返回前
leafInfo.SetSplitInfo(splitInfo)
return leftRef, rightRef, splitInfo, nil
```

#### Phase 1.3：修改 SearchChild 支持重定向（2 天）

**文件**：`internal/infrastructure/storage/btree/offheap_adapter.go`

```go
// 修改 GetChildWithVersion 支持重定向
func (pa *PageAccessor) GetChildWithVersion(pageID uint32, index int) (childPageID uint32, expectedVersion uint64) {
    // ... 现有逻辑 ...

    // 新增：检查是否需要重定向
    // 如果当前页面的 PageInfo 有 SplitInfo，需要跟随到新页面
    // 注意：这个逻辑在 PageInfo 层实现，不在 PageAccessor 层
    // PageAccessor 只负责读写原始页面数据
}

// 实际的重定向逻辑应该在 BTree 层或 PageRef 层实现
// 参考 Lealone 的 getOrReadPage 模式
```

**修改点**：`searchPathWithRefs` 中的 SearchChild 调用

```go
// search_path.go:209
// 在调用 SearchChild 后，添加重定向检查

childPageID, _, err := b.offheapAdapter.SearchChild(currentPageID, key)
if err != nil {
    return nil, nil, ErrRetry
}

// 新增：检查子节点是否需要重定向
childRef := b.pageRefCache.GetOrCreate(childPageID, isChildLeaf)
childInfo := childRef.GetPageInfo()
if childInfo.IsRedirecting() {
    // 需要重定向到新页面
    newRef := childInfo.GetNewRef()
    if newRef != nil {
        childInfo = newRef.GetPageInfo()
        childPageID = model.PageID(childInfo.GetPageID())
    }
}
```

#### Phase 1.4：运行压力测试验证（1 天）

```bash
# 运行 btree_perf_pprof
./bin/btree_perf_pprof -threads 8 -count 50000 -init 5000

# 验证标准
# - ErrRetry 率 < 50% → 方案有效，继续 Phase 2
# - ErrRetry 率无变化 → 重新评估方案
```

#### Phase 1 验证结果（2026-04-01）

**实现内容**：
1. ✅ 添加 `SplitInfo` 数据结构（`internal/infrastructure/storage/btree/split_info.go`）
2. ✅ 修改 `handleSplitOffHeapSync` 在分裂后存储 SplitInfo
3. ✅ 修改 `SearchChild` 支持基于 SplitInfo 的重定向
4. ✅ 添加 `IsPageDeleted` 方法（`page_layout.go`）
5. ✅ `SearchChild` 中添加 deleted 标志检查
6. ✅ `ReplaceChild` 中添加 child=0 检查
7. ✅ `UpdateIndexEntry` 中添加 extraChild=0 检查

**基准测试结果**（1 线程，50k ops）：
| 指标 | 实现后 |
|------|--------|
| Success | 100.0% (50k/50k) |
| ErrRetry | 0 |
| ErrOther | 0 |

**问题分析**：
1. **child zero detected** 根因：并发时父页面中的 child 字段变成 0
2. **解决方案**：在读取路径（ReplaceChild、UpdateIndexEntry）中检查 child=0，返回 ErrRetry
3. **deleted 标志**：利用已有的 deleted 标志，在 SearchChild 中优先检查页面是否正在被回收

**关键修改**：
- `page_layout.go`: 添加 `IsPageDeleted` 方法
- `offheap_adapter.go`: SearchChild 中先检查 deleted 标志
- `offheap_adapter.go`: ReplaceChild 中检查 child=0
- `offheap_adapter.go`: UpdateIndexEntry 中检查 extraChild=0

**单线程验收标准**：
- [x] 50k ops 压力测试通过率 100%
- [ ] 8 线程扩展比达到 2.5x 以上
- [ ] ErrRetry 率在高并发下 < 10%

---

### Phase 2：解决多线程并发问题（1 周）

**目标**：解决 2 线程以上并发时的 child=0 和 stale reference 问题

**问题分析**：
```
1. 单线程 50k ops：100% 成功
2. 2 线程 50k ops：~1% 成功，58% ErrOther (child zero detected)
3. 根因：
   - 并发分裂时，父页面的 child 字段被并发修改
   - UpdateIndexEntry/ReplaceChild 读取的 child 可能已被另一线程修改为 0
   - 页面回收后 version=1 但旧引用期望更高版本
```

#### Phase 2.1：解决 child=0 问题（3 天）

**问题**：`UpdateIndexEntry` 和 `ReplaceChild` 从父页面读取 child 时，读到 0 值

**方案**：在读取 child 时检测父页面是否正在被修改

```go
// 修改 GetChildSafe，增加 deleted 检查
func (pa *PageAccessor) GetChildSafe(pageID uint32, index int) (uint64, error) {
    // 1. 检查页面是否被删除
    if pa.IsPageDeleted(pageID) {
        return 0, errpkg.ErrBTreeRetry
    }
    // 2. 读取 child
    ...
}
```

**实现**：
1. [ ] `GetChildSafe` 增加 deleted 检查
2. [ ] `GetIndexEntryOffsetSafe` 增加 deleted 检查
3. [ ] `ReplaceChild` 失败时返回 ErrRetry 而非报错

#### Phase 2.2：优化 Epoch 延迟机制（2 天）

**问题**：当前 5 epoch 延迟可能不足

**方案**：动态调整 epoch 延迟或增加版本检测

```go
// 修改 Free 时增加 version++
func (pm *PageManager) Free(pageID uint32) error {
    ptr := pm.PageIDToPtr(pageID)
    header := (*PageHeader)(ptr)

    // 原子性递增版本
    atomic.AddUint64(&header.version, 1)

    header.deleted = 1
    header.deleteEpoch = pm.currentEpoch.Load()

    pm.delayedFreeList.Enqueue(pageID)
    return nil
}

// Alloc 时不清除 version，让 epoch + version 双重保护
```

**注意**：之前 version++ 尝试导致不稳定，需要：
1. 使用原子操作 `atomic.AddUint64`
2. 确保读取 version 时也是原子的
3. 验证稳定性

**实现**：
1. [ ] `Free` 时使用 `atomic.AddUint64` 增加 version
2. [ ] `Alloc` 时不清除 version（移除 `version = 1`）
3. [ ] 压力测试验证稳定性

#### Phase 2.3：验证多线程改进（2 天）

**验证标准**：
```
2 线程 50k ops:
- Success > 80%
- ErrRetry < 15%
- ErrOther < 5%

8 线程 50k ops:
- Success > 60%
- ErrRetry < 30%
- ErrOther < 10%
- 扩展比 > 1.5x
```

---

### Phase 3：Leaf CAS 优化（1-2 周）

**目标**：参考 Lealone B-Link，实现真正的 Leaf CAS

#### Phase 3.1：实现 Sibling 链（3-4 天）

**数据结构**：
```go
type PageHeader struct {
    // 现有字段...
    siblingPrev uint32  // 前驱页面（0 表示无）
    siblingNext uint32  // 后继页面（0 表示无）
}
```

**分裂流程改进**：
```
Leaf 分裂 L → L1, L2:
1. 分配 L1, L2
2. L1.siblingNext = L2
3. L2.siblingPrev = L1
4. 设置 L 的状态为 SPLITTING，指向 L2
5. 更新父节点（当前流程）
6. 不再需要分裂传播到 Root
```

**搜索改进**：
```
SearchChild:
1. 获取 child 后，检查 child 是否 SPLITTING
2. 如果是，跟随 sibling 到正确的新页面
3. 不需要重试 ErrRetry
```

#### Phase 3.2：减少 Root CAS（3-4 天）

**方案**：实现 Path CoW（Copy-on-Write）

```
分裂时：
1. 不修改原父页面，创建 P'（副本）
2. P' 的 child 指向新的 L1, L2
3. 只在 P' 层做 CAS，不向上传播

好处：
- 只有直接的父节点需要更新
- 避免级联到 Root
```

#### Phase 3.3：延迟父节点更新（3-4 天）

**方案**：参考 Lealone 的懒更新机制

```
分裂时：
1. Leaf 分裂完成
2. 记录 "待更新父节点" 任务
3. 立即返回成功
4. 后台任务完成父节点更新

搜索时：
1. 如果遇到待更新的父节点
2. 先完成更新，再继续搜索
```

---

### Phase 4：测试和调优（1 周）

```
1. 单元测试（2 天）
2. 并发压力测试（2 天）
3. 性能基准测试（1 天）
4. 与 Lealone 对比分析（1 天）
```

---

### Phase 5：性能优化（持续）

**目标**：达到 Lealone 性能水平

```
| 指标 | 当前 | Phase 2 目标 | Phase 3 目标 |
|------|------|-------------|--------------|
| 1 线程 | 200K ops/s | 200K ops/s | 220K ops/s |
| 2 线程 | ~1% | >80% | >90% |
| 8 线程 | ~0% | >30% | >70% |
| 扩展比 | ~1x | >1.5x | >2.5x |
```

---

## 9. 风险和注意事项

### 9.1 数据一致性风险

**问题**：如果 DeferredTask 执行前系统崩溃？

**缓解方案**：
1. **WAL 记录**：每次分裂操作先写入 WAL，包含：
   - 分裂前的 parent page 状态
   - 分裂后的 left/right page 信息
   - 待执行的 parent update 任务

2. **恢复流程**：
   ```
   启动时：
   1. 读取 WAL
   2. 识别未完成的分裂操作
   3. 完成 parent update（如果 left/right page 仍有效）
   4. 或者回滚到分裂前状态（如果 page 已回收）
   ```

3. **混合策略**（推荐）：
   - 小型分裂：同步完成，不走延迟更新
   - 大型分裂（可能触发级联）：走延迟更新，但先记录 WAL

### 9.2 内存压力

**问题**：DeferredTask 队列可能积累大量任务

**缓解**：
- 监控队列深度，超过阈值时触发限流
- 队列满时回退到同步执行模式
- 后台 GC 清理已完成的 task 引用

### 9.3 搜索性能

**问题**：resolveSplitChild 可能增加搜索延迟

**缓解**：
- 大部分情况下不需要 resolve（只有在分裂过程中）
- 优化 sibling 链的遍历（最多 2-3 跳）
- 可以缓存最近解析的 child 映射

### 9.4 并发分裂竞争

**问题**：多个线程同时分裂同一个页面

**缓解**：
- 使用 SplitLock 保护分裂过程
- CAS 失败后重试，而不是死等
- 参考 Lealone 的 PageLock 机制

### 9.5 回滚策略

**目的**：确保 Phase 1-2 效果不佳时可以安全回滚

#### Feature Flag 切换

```go
// btree_config.go
type BTreeConfig struct {
    // ...
    EnableLeafCAS bool // 默认 false
}

// 使用
if b.config.EnableLeafCAS {
    // 新逻辑：SplittedPageInfo + 自动重定向
} else {
    // 旧逻辑：同步分裂传播
}
```

#### 回滚条件

| 条件 | 说明 |
|------|------|
| ErrRetry 无明显下降 | Phase 1 验证后 ErrRetry 率仍 > 70% |
| 发现新的 critical bug | 影响数据一致性或正确性 |
| 性能提升 < 20% | 与旧逻辑相比无明显优势 |

#### 回滚步骤

```
1. 设置 EnableLeafCAS = false
2. 运行单元测试验证旧逻辑正常
3. 运行压力测试验证无 regression
4. 保留新逻辑代码（不删除），标记为 experimental
5. 分析失败原因，重新评估方案
```

---

## 10. 替代方案对比

| 方案 | 复杂度 | 效果 | 风险 |
|------|--------|------|------|
| A. B-Link 风格 | 中 | 好 | 中（搜索算法变复杂）|
| B. Epoch 延迟更新 | 中 | 中 | 高（一致性难保证）|
| C. Path CoW | 低 | 中 | 低（内存开销）|
| D. Version-based Root | 中 | 中 | 中（仍需要同步）|
| **E. Lealone B-Link** | **中** | **最好** | **低** |
| F. 两级索引 | 高 | 最好 | 高（重构大）|

**推荐**：方案 E（Lealone B-Link + SplittedPageInfo）

---

## 11. 关键实现细节（来自 Lealone 源码）

### 11.1 SplittedPageInfo 机制

```java
// Lealone PageInfo.java
public static class SplittedPageInfo extends DataStructureChangedPageInfo {
    private final PageReference pRefNew;  // 新页面引用

    public SplittedPageInfo(PageReference pRefNew, PageInfo pInfoOld, PageLock pageLock) {
        this.pRefNew = pRefNew;
        setPageLock(pageLock);
        this.page = pInfoOld.page;
        this.pos = pInfoOld.pos;
    }

    @Override
    public boolean isDataStructureChanged() { return true; }

    @Override
    public PageReference getNewRef() { return pRefNew; }
}
```

### 11.2 自动重定向的 getOrReadPage

```java
// Lealone PageReference.java
public Page getOrReadPage() {
    PageInfo pInfo = this.pInfo;
    if (pInfo.isDataStructureChanged()) { // 发生 split 或 remove
        return pInfo.getNewRef().getOrReadPage();  // 自动跟随！
    }
    // 正常读取...
}
```

### 11.3 关键洞察

1. **分裂是本地操作**：Leaf 分裂后，用 `SplittedPageInfo` 替换原 `PageInfo`，不需要立即更新父节点

2. **旧引用自动重定向**：任何尝试访问旧引用的线程都会通过 `isDataStructureChanged()` 检测到变化，并自动跟随到新页面

3. **读路径保持简单**：读取时调用 `getOrReadPage()`，自动处理所有分裂情况

4. **写路径局部化**：只有直接参与分裂的页面需要 CAS，其他页面不受影响

---

## 12. 结论

通过分析 Lealone 源码，发现其实现 Leaf CAS 的关键机制：

### Lealone 的核心方案

1. **SplittedPageInfo 追踪机制**
   - 分裂时，旧的 PageReference 不直接修改
   - 而是用 `SplittedPageInfo` 替换，它持有新页面的引用
   - `isDataStructureChanged()` 返回 true 表示需要重定向

2. **自动重定向**
   - `getOrReadPage()` 自动检测 `isDataStructureChanged()`
   - 如果为 true，自动调用 `getNewRef().getOrReadPage()`
   - 读取路径完全不需要知道分裂的存在

3. **不需要同步更新父节点**
   - 因为旧引用会自动重定向到新页面
   - 父节点可以在后续访问时懒更新

### 推荐实施方案

**Phase 1**：实现 SplittedPageInfo 机制
```
1. 添加 PageState 和 SplitInfo 结构
2. 实现 isDataStructureChanged() 检测
3. 实现 getNewRef() 返回新页面引用
4. 修改 GetLeafPage 逻辑支持自动重定向
```

**Phase 2**：修改分裂流程
```
1. handleSplitOffHeapSync 不再向上传播更新
2. 创建 SplitInfo 替换原 PageReference
3. 验证自动重定向正确工作
```

**Phase 3**：性能调优
```
1. 减少不必要的重定向
2. 优化内存管理
3. 性能基准测试对比
```

这是一个中等规模的架构重构，但风险可控，因为：
- 现有读路径自动兼容
- 不需要单写线程
- Lealone 已验证此方案可行
