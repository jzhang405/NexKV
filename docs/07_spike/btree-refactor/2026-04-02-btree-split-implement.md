# NexKV B+Tree Split 实现详解

> 创建时间：2026-04-04
> 状态：设计中（Phase 6.0）
> 配套：`2026-04-02-btree-refactor-interface.md` + `2026-04-02-btree-refactor-implement.md`
> **⚠️ ChildrenCache 改造影响**：本节已标注 `ChildrenCache` 方案（内嵌 separator keys）的变更点。详见 `2026-04-06-children-cache-separators.md`。

## 1. 概述

本文详细描述 NexKV B+Tree 中 **Split（分裂）** 操作的完整实现，包括：

- 底层页面 Split（Leaf Split / Node Split）
- Split 传播机制（handleLeafSplit / handleRootSplit）
- Redirect+NewRef 可见性机制（原子 CAS，无窗口期）
- 并发读者如何通过 Redirect 重新导航到正确子页面
- writeOperation 中的 CR-08（Split + Immediate Insert）集成

### 核心设计决策

| 决策 | 说明 | 参考 |
|------|------|------|
| COW 语义 | 所有变更返回新页面，原页面不变 | interface.md §6 |
| Leaf-Level Locking | 叶级自旋锁 + CAS，非 Root CAS | interface.md §5 |
| Redirect+NewRef | Tombstone+Redirect+NewRef 在同一次 CAS 中原子设置，无窗口期 | §5 |
| CR-08 | Split + Immediate Insert，强一致性返回 | implement.md §6.0.4 |
| Best-Effort 传播 | 普通更新传播不重试；Split 传播 Full Retry | implement.md §6.0.3 D1 |

---

## 2. 页面布局

NexKV 使用 4KB（4096 字节）mmap 页面，布局如下：

```
┌──────────────┬──────────────────┬──────────────┐
│ PageHeader   │ Entry 数组        │ KV 数据区     │
│ 56B          │ N × 16B           │ 变长（从末尾向前增长）│
└──────────────┴──────────────────┴──────────────┘
 共 4096 字节

PageHeader (56B):
  version(8) + prevPage(4) + nextPage(4) + extraChild(8)
  + count(2) + pageType(1) + deleted(1) + [padding 4B]
  + deleteEpoch(8) + refCount(4) + inQueue(4) + pad(3) + [padding 5B]

LeafEntry (16B): keyOff(4) + keyLen(4) + valOff(4) + valLen(4)
IndexEntry (16B): keyOff(4) + keyLen(4) + child(8)
  child = pageID(4) + version(4)
```

### 容量限制

- **MaxInternalKeys = 126**（基于 avgKey=16B：`(4096-56)/(16+16)=126`）
- **Leaf 无固定上限**：`IsFull(keyLen, valueLen)` 按空间计算，阈值 0.95
- **Node 双重判定**：`count >= 126` 兜底 + 空间计算阈值 0.90

---

## 3. 底层 Split 算法

### 3.1 Leaf Split — Copy-Up 语义

**文件**: `internal/infrastructure/storage/btree/leaf_page.go:192-235`

```go
func (h *leafPageHandle) Split() (LeafPage, LeafPage, []byte, error) {
    count := h.Count()
    if count < 2 {
        return nil, nil, nil, errpkg.BTreeLeafSplitMinKeys(count)
    }

    mid := count / 2

    // splitKey = right page's first key（copy-up：保留在 right 中，同时复制提升到 parent）
    keyOff, keyLen, _, _ := h.pa.GetLeafEntryOffset(uint32(h.id), mid)
    splitKey := h.pa.GetKey(uint32(h.id), keyOff, keyLen)
    splitKeyCopy := make([]byte, len(splitKey))
    copy(splitKeyCopy, splitKey)

    // 分配左右新页面
    leftRawID, _ := h.storage.pm.Alloc()
    rightRawID, _ := h.storage.pm.Alloc()

    srcVersion := h.pa.GetVersion(uint32(h.id))

    // Left: entries[0..mid)
    h.pa.BulkInitLeafFromSource(uint32(h.id), leftRawID, 0, mid)
    h.pa.SetVersion(leftRawID, srcVersion+1)

    // Right: entries[mid..count)
    h.pa.BulkInitLeafFromSource(uint32(h.id), rightRawID, mid, count)
    h.pa.SetVersion(rightRawID, srcVersion+1)

    left := &leafPageHandle{id: model.PageID(leftRawID), pa: h.pa, storage: h.storage}
    right := &leafPageHandle{id: model.PageID(rightRawID), pa: h.pa, storage: h.storage}
    return left, right, splitKeyCopy, nil
}
```

**Copy-Up 语义图解**：

```
Split 前 (count=10):
┌──────────────────────────────────────────┐
│ [k0, k1, k2, k3, k4, k5, k6, k7, k8, k9] │
└──────────────────────────────────────────┘
              mid=5 ↑

Split 后:
Left (5 entries):              Right (5 entries):
┌─────────────────┐           ┌─────────────────┐
│ [k0, k1, k2, k3, k4] │     │ [k5, k6, k7, k8, k9] │
└─────────────────┘           └─────────────────┘

splitKey = k5（copy-up：保留在 right 页面中，同时复制提升到父节点）
```

### 3.2 Node Split — Move-Up 语义

**文件**: `internal/infrastructure/storage/btree/node_page.go:158-205`

```go
func (h *nodePageHandle) Split() (NodePage, NodePage, []byte, error) {
    count := h.Count()
    if count < 2 {
        return nil, nil, nil, errpkg.BTreeNodeSplitMinKeys(count)
    }

    mid := count / 2

    // move-up: splitKey 从 left 和 right 中移除，提升到 parent
    splitKey := h.GetKey(mid)
    splitKeyCopy := make([]byte, len(splitKey))
    copy(splitKeyCopy, splitKey)

    leftRawID, _ := h.storage.pm.Alloc()
    rightRawID, _ := h.storage.pm.Alloc()
    srcVersion := h.pa.GetVersion(uint32(h.id))

    // Left: entries[0..mid), extraChild = child[mid]
    leftExtraChild := h.pa.GetChild(srcRawID, mid)
    h.pa.BulkInitIndexFromSource(srcRawID, leftRawID, 0, mid, leftExtraChild)
    h.pa.SetVersion(leftRawID, srcVersion+1)

    // Right: entries[mid+1..count), extraChild = original extraChild (child[count])
    rightExtraChild := h.pa.GetChild(srcRawID, count)
    h.pa.BulkInitIndexFromSource(srcRawID, rightRawID, mid+1, count, rightExtraChild)
    h.pa.SetVersion(rightRawID, srcVersion+1)

    left := &nodePageHandle{id: model.PageID(leftRawID), pa: h.pa, storage: h.storage}
    right := &nodePageHandle{id: model.PageID(rightRawID), pa: h.pa, storage: h.storage}
    return left, right, splitKeyCopy, nil
}
```

**Move-Up 语义图解**：

```
Split 前 (count=3, children=4):
┌──────────────────────────────────────────────────────┐
│ [e0(k0,c0), e1(k1,c1), e2(k2,c2)] + extraChild=c3   │
└──────────────────────────────────────────────────────┘
              mid=1 ↑

Split 后:
Left (1 entry):                    Right (1 entry):
┌──────────────────┐             ┌──────────────────┐
│ [e0(k0,c0)] + c1 │             │ [e2(k2,c2)] + c3 │
└──────────────────┘             └──────────────────┘

splitKey = k1（move-up：不保留在 left 或 right 中，提升到 parent）
注意：child[c1] 成为 left 的 extraChild
```

### Leaf Split vs Node Split 对比

| 方面 | Leaf Split | Node Split |
|------|-----------|-----------|
| splitKey 语义 | Copy-Up（保留在 right 中） | Move-Up（不保留在 left/right 中） |
| Left 范围 | `[0, mid)` | `[0, mid)` |
| Right 范围 | `[mid, count)` | `[mid+1, count)` |
| splitKey 来源 | `GetKey(mid)` = right 第一个 key | `GetKey(mid)` = 中间 key（被移除） |
| extraChild 处理 | 无（叶子无子节点） | left.extraChild = child[mid]，right.extraChild = child[count] |

---

## 4. InsertChild — 父节点更新

**文件**: `internal/infrastructure/storage/btree/node_page.go:111-152`

InsertChild 是 Split 传播的核心操作，将 split 产生的两个子页面插入父节点。

### 4.1 中间插入 (idx < count)

```go
if idx < count {
    // Step 1: SetChild(idx, right) — 将 children[idx] 替换为 right
    h.pa.SetChild(newRawID, idx, uint32(right))

    // Step 2: InsertIndexEntry(idx, splitKey, left) — shift entries 并插入新 entry
    h.pa.InsertIndexEntry(newRawID, idx, splitKey, uint32(left), &dataEnd)
}
```

**关键设计**：entries 数组中每个 IndexEntry 包含 `(keyOff, keyLen, child)` 字段。
`InsertIndexEntry` 会 shift entries 数组（`page_layout.go:370-377`），
每个 entry 的 child 字段随 shift 自动移动。因此 **不需要手动 shift children 数组**。

**图解**（2 keys，3 children，在 idx=1 插入 splitKey=kX, left=A, right=B）：

```
操作前:
  entries:  [e0(k0,c0), e1(k1,c1)]
  extraChild: c2

Step 1: SetChild(1, B)
  children[1] = B
  → entries: [e0(k0,c0), e1(k1,B)]
  extraChild: c2

Step 2: InsertIndexEntry(1, kX, A)
  → shift e1 to e2: [e0(k0,c0), ???, e1(k1,B)]
  → insert at idx=1: [e0(k0,c0), new(kX,A), e1(k1,B)]
  extraChild: c2

结果:
  entries:  [e0(k0,c0), eX(kX,A), e1(k1,B)]
  extraChild: c2
  children = [c0, A, B, c2]  ← 正确！
```

### 4.2 末尾插入 (idx == count)

```go
} else {
    // End insert: extraChild splits into left and right
    h.pa.InsertIndexEntry(newRawID, count, splitKey, uint32(left), &dataEnd)
    // count 已 +1，SetChild(new_count, right) → sets extraChild
    h.pa.SetChild(newRawID, count+1, uint32(right))
}
```

**图解**（1 key，2 children，在 idx=1 末尾插入 splitKey=kX, left=A, right=B）：

```
操作前:
  entries: [e0(k0,c0)]
  extraChild: c1

Step 1: InsertIndexEntry(1, kX, A)
  → entries: [e0(k0,c0), eX(kX,A)]
  count = 2

Step 2: SetChild(2, B) → extraChild = B
  entries: [e0(k0,c0), eX(kX,A)]
  extraChild: B

结果:
  children = [c0, A, B]  ← 正确！
```

---

## 5. Redirect+NewRef — 分裂可见性机制

**文件**: `internal/infrastructure/storage/btree/page_info.go`

### 5.1 问题

叶子 CAS 成功后、父节点更新前，存在一个窗口期：

```
Writer: leaf CAS 成功 → pInfo 已更新 → ... → 父节点 CAS 开始
Reader: 看到 leafRef 的新 pInfo → 但 leafRef 的数据可能已经 split → 只看到一半数据
```

### 5.2 解决方案

在 PageInfo 中增加 `Redirect` 和 `NewRef` 字段，**与 Tombstone 在同一次 CAS 中原子设置**。
并发 reader 在 searchPath 时检测到 Tombstone+Redirect 后，通过 NewRef 重新导航。

**与旧 SplitMarker 设计的关键区别**：SplitMarker 需要独立的 `atomic.Store` 操作，与 Tombstone CAS 是两个独立操作，存在窗口期。Redirect+NewRef 与 Tombstone 在同一个 CAS 中设置，**不存在窗口期**。

```go
type PageInfo struct {
    PageID    model.PageID
    Version   uint64
    Tombstone bool     // page has been split, no longer navigable
    Redirect  bool     // data structure changed (split), reader should re-navigate via NewRef
    NewRef    *PageRef // when Redirect=true, points to the left child of the split
}
```

**字段语义**：
- `Tombstone=true`：该页面已被分裂，不再可导航
- `Redirect=true`：数据结构已变化（分裂），reader 应通过 NewRef 重新导航
- `NewRef`：当 Redirect=true 时，指向分裂后的左子页面。Reader 应从父节点的已更新 children 缓存中重新查找正确子页面

### 5.3 原子 Redirect CAS

```go
// handleLeafSplit 中的 Redirect CAS — 单次原子操作，无窗口期
redirectInfo := &PageInfo{
    PageID:    leafInfo.PageID,
    Version:   leafInfo.Version + 1,
    Tombstone: true,
    Redirect:  true,
    NewRef:    leftRef,  // 指向分裂后的左子页面
}
leafRef.CAS(leafInfo, redirectInfo)
```

**关键优势**：
- Tombstone + Redirect + NewRef 在**同一次 CAS** 中原子生效
- 不存在 "Tombstone 已设置但 Redirect 未设置" 的窗口期
- 消除了旧 SplitMarker 设计中的 B2 Bug（顺序错误导致 reader 丢失）
- 无需独立的 SetSplitMarker / ClearSplitMarker 操作
- 无需 FollowSplit 方法（因为 NewRef 不需要按 key 选择方向）

### 5.4 Reader 如何使用 Redirect

当 searchPath 遇到 `Tombstone=true && Redirect=true` 时：

```go
childInfo := childRef.GetPageInfo()
if childInfo.Tombstone && childInfo.Redirect && childInfo.NewRef != nil {
    // 页面已分裂，需要重新导航
    // 策略：从父节点的已更新 ChildrenCache 中重新查找
    childRef.Release()
    updatedCache := currentRef.children.Load()  // ★ ChildrenCache 改造：直接读原子指针
    if updatedCache != nil {
        reIdx := updatedCache.Search(key)  // ★ 用内嵌 separators 二分搜索，不读 NodePage
        if reIdx < len(updatedCache.Children) && updatedCache.Children[reIdx] != nil {
            childRef = updatedCache.Children[reIdx]
            childRef.Retain()
        }
    }
}
```

**为什么 NewRef 指向左子页面而非按 key 选择**：
- 旧 SplitMarker 设计需要根据 key 与 splitKey 的比较来选择 Left/Right
- 新设计中，Redirect 触发后 reader 从**父节点的已更新 ChildrenCache** 中重新查找
- 父节点 CAS 成功后 ChildrenCache 已更新（包含 leftRef、rightRef 和 separator keys），reader 通过 `cache.Search(key)` 找到正确的子页面
- NewRef 作为 fallback：当父节点 ChildrenCache 尚未更新时的安全退路

**★ ChildrenCache 改造要点**：
- `currentRef.children` 类型从 `atomic.Pointer[[]*PageRef]` 改为 `atomic.Pointer[ChildrenCache]`
- 重新导航不再调用 `node.Search(key)`（读物理页），改用 `cache.Search(key)`（读内嵌 separators）
- 优势：separators 与 children 在同一 ChildrenCache 对象中原子更新，不存在不一致

### 5.5 PageInfo 字段在各角色的语义

| 字段 | Reader 遇到 | Writer 遇到 | propagateUpward 遇到 |
|------|-----------|-----------|---------------------|
| `Tombstone=true` | 页面已分裂，需重新导航 | 锁定后发现 Tombstone，释放锁并完整重试 | 停止传播（该路径已分裂） |
| `Redirect=true` | 数据结构已变，通过 NewRef/children 重新导航 | — | — |
| `NewRef != nil` | 指向分裂后的左子页面，作为重新导航的起点 | — | — |

### 5.6 Redirect+NewRef vs 旧 SplitMarker 设计对比

| 方面 | 旧 SplitMarker | 新 Redirect+NewRef |
|------|---------------|-------------------|
| 设置方式 | 独立 `atomic.Store` + 独立 Tombstone CAS | 单一 CAS（原子） |
| 窗口期 | 存在（SetSplitMarker 与 Tombstone CAS 之间） | **不存在**（同一次 CAS） |
| 数据结构 | `SplitMarker{Left, Right, SplitKey}` 独立 struct | PageInfo 内嵌字段 |
| Reader 导航 | `FollowSplit(key)` 按 key 选择 Left/Right | 从父节点 children 重新 Search |
| 清理操作 | 需要 `ClearSplitMarker` + Release | 无需清理（随 PageRef 生命周期管理） |
| 引用计数管理 | marker.Retain/Release，复杂 | NewRef 作为 PageInfo 字段，随 CAS 生命周期 |
| Bug B2 风险 | 存在（顺序错误） | **消除**（原子 CAS） |

---

## 6. searchPath 中的 Redirect 处理

**文件**: `internal/infrastructure/storage/btree/search.go:58-107`

> **★ ChildrenCache 改造**：`searchPath` 的核心变化是 **不再调用 `storage.GetNodePage` 做导航**，改用 `ChildrenCache.Search(key)` 二分搜索内嵌的 separator keys。仅在 cache 未命中（首次访问）时读 NodePage 懒初始化。

```go
func searchPath(storage *OffheapBTreeStorage, rootRef *RootPageRef, key []byte) (SearchPath, error) {
    var path SearchPath
    currentRef := &rootRef.PageRef
    currentRef.Retain()

    for {
        pInfo := currentRef.GetPageInfo()
        if pInfo == nil {
            path.ReleaseAll()
            return nil, errpkg.BTreeSearchPathNilPageInfo(0) // page freed
        }

        // ★ Bug B3 修复：Tombstone 检查（在 IsLeaf 判断之前！）
        if pInfo.Tombstone {
            // Redirect+NewRef: Tombstone 和 Redirect 在同一次 CAS 中设置
            // 不存在旧 SplitMarker 的窗口期问题
            if pInfo.Redirect && pInfo.NewRef != nil {
                // 从父节点的已更新 ChildrenCache 中重新导航
                currentRef.Release()
                if len(path) > 0 {
                    parentEntry := path[len(path)-1]
                    // ★ ChildrenCache 改造：直接读原子指针，不调 GetNodePage
                    updatedCache := parentEntry.Ref.children.Load()
                    if updatedCache != nil {
                        reIdx := updatedCache.Search(key)  // ★ 内嵌 separators 二分搜索
                        if reIdx < len(updatedCache.Children) && updatedCache.Children[reIdx] != nil {
                            currentRef = updatedCache.Children[reIdx]
                            currentRef.Retain()
                            continue
                        }
                    }
                }
                path.ReleaseAll()
                return nil, ErrRetry
            }
            path.ReleaseAll()
            return nil, ErrRetry
        }

        if storage.pa.IsLeaf(uint32(pInfo.PageID)) {
            path = append(path, PathEntry{Ref: currentRef, Index: -1})
            return path, nil
        }

        // ★ ChildrenCache 改造：Internal node 导航改用 cache.Search(key)
        // 旧代码：node = GetNodePage(pInfo.PageID); idx = node.Search(key); children = GetOrCreateChildren()
        // 新代码：cache = GetOrCreateChildren(); idx = cache.Search(key); childRef = cache.Children[idx]
        cache, _ := currentRef.GetOrCreateChildren(storage)  // 返回 *ChildrenCache
        if cache == nil {
            path.ReleaseAll()
            return nil, ErrRetry  // leaf 不应有 cache
        }
        idx := cache.Search(key)  // ★ 内嵌 separators 二分搜索，不读物理页
        path = append(path, PathEntry{Ref: currentRef, Index: idx})

        // ★ P1-1 修复：stale cache 越界保护
        if idx >= len(cache.Children) || cache.Children[idx] == nil {
            path.ReleaseAll()
            return nil, ErrRetry  // stale cache，重试即可
        }
        childRef := cache.Children[idx]

        // ★ Redirect 检测：检查子页面是否已分裂
        childInfo := childRef.GetPageInfo()
        if childInfo != nil && childInfo.Tombstone && childInfo.Redirect && childInfo.NewRef != nil {
            childRef.Release()
            // ★ ChildrenCache 改造：重新加载 parent cache，用内嵌 separators 重新搜索
            updatedCache := currentRef.children.Load()
            if updatedCache != nil {
                reIdx := updatedCache.Search(key)  // ★ 不再调 node.Search
                if reIdx < len(updatedCache.Children) && updatedCache.Children[reIdx] != nil {
                    childRef = updatedCache.Children[reIdx]
                    childRef.Retain()
                } else {
                    path.ReleaseAll()
                    return nil, ErrRetry
                }
            } else {
                path.ReleaseAll()
                return nil, ErrRetry
            }
        } else {
            childRef.Retain()
        }

        currentRef = childRef
    }
}
```

**关键点**：
- **Tombstone 检查必须在 IsLeaf 之前**（Bug B3）：Tombstoned 页面的 PageID 可能已变为内部节点类型，直接 IsLeaf 会判断错误
- Reader 在遍历时**无需获取锁**
- Redirect+NewRef 是原子生效的，**不存在旧 SplitMarker 的窗口期**（Tombstone 但无 marker 可跟）
- **★ ChildrenCache 改造核心**：`searchPath` 在 cache 命中时不调用 `storage.GetNodePage`，separator keys 与 children 在同一 `ChildrenCache` 对象中，**消除不一致窗口**
- 当检测到子页面 Redirect 时，从父节点的 ChildrenCache 重新查找（`cache.Search(key)` 得到正确的子页面索引）
- Retain/Release 配对：所有路径上的 Ref 都正确管理引用计数

---

## 7. ReplaceRoot — 根分裂的原子切换

**文件**: `internal/infrastructure/storage/btree/root_ref.go:29-45`

> **★ ChildrenCache 改造**：`ReplaceRoot` 参数从 `newChildren []*PageRef` 改为 `newChildren *ChildrenCache`，内部遍历 `newChildren.Children` 设置 parentRef，`children.Store(newChildren)` 参数已是指针无需取地址。

```go
func (r *RootPageRef) ReplaceRoot(oldInfo, newInfo *PageInfo, newChildren *ChildrenCache) bool {
    // ★ D14 决策：CAS 之前设置 parentRef
    // 子节点尚未对读者可见（CAS 未执行），设置 parentRef 是安全的
    if newChildren != nil {
        for _, child := range newChildren.Children {  // ★ 遍历 ChildrenCache.Children
            if child != nil {
                child.SetParentRef(&r.PageRef)
            }
        }
    }

    // CAS publish
    if !r.CAS(oldInfo, newInfo) {
        // CAS 失败：不回滚 parentRef（D14 理由见下方）
        return false
    }
    return true
}
```

**D14 设计决策 — CAS 失败不回滚 parentRef**：

1. CAS 失败 → pInfo 不变 → 读者不会看到 newChildren → parentRef 值无所谓
2. 调用方（handleRootSplit）会创建全新的 PageRef 重试
3. 回滚 `SetParentRef(nil)` 引入额外 atomic store，增加竞争面

---

## 8. 完整 Split 流程时序图

### 8.1 正常写入（无 Split）

```mermaid
sequenceDiagram
    participant Client
    participant BTree
    participant SearchPath
    participant LeafRef as PageRef(Leaf)
    participant Storage as OffheapBTreeStorage
    participant LeafPage

    Client->>BTree: Set(key, value)

    Note over BTree: 1. Search Path
    BTree->>SearchPath: searchPath(key)
    Note over SearchPath: 路径上每个 Ref.Retain()
    SearchPath-->>BTree: path

    Note over BTree: 2. Lock + Read
    BTree->>LeafRef: Lock()
    BTree->>LeafRef: GetPageInfo()
    LeafRef-->>BTree: pInfo{PageID=X, Version=3}

    BTree->>Storage: GetLeafPage(X)
    Storage-->>BTree: leaf

    Note over BTree: 3. COW Mutate
    BTree->>LeafPage: mutate(leaf) → Insert(key, value)
    Note over LeafPage,Storage: COW: Alloc new page Y<br/>copy(X→Y) + insert on Y
    LeafPage-->>BTree: newLeaf{PageID=Y}

    Note over BTree: 4. CAS
    BTree->>LeafRef: CAS(pInfo, {PageID=Y, Version=4})

    alt CAS 成功
        LeafRef-->>BTree: true
        BTree->>LeafRef: Unlock()
        Note over BTree: propagateUpward (Best-Effort)
        BTree->>BTree: path.ReleaseAll()
        BTree-->>Client: nil (Success)
    else CAS 失败
        LeafRef-->>BTree: false
        BTree->>Storage: FreePage(Y)
        BTree->>LeafRef: Unlock()
        BTree->>BTree: path.ReleaseAll()
        Note over BTree: goto Step 1 (重试)
    end
```

### 8.2 Leaf Split + Immediate Insert（CR-08）

```mermaid
sequenceDiagram
    participant Client
    participant BTree as BTree.writeOperation
    participant LeafRef as PageRef(Leaf)
    participant ParentRef as PageRef(Parent)
    participant Storage as OffheapBTreeStorage

    Client->>BTree: Set(key, value)

    Note over BTree: 1. Search Path
    BTree->>BTree: searchPath(key)
    Note over BTree: path = [root, parent, leaf]<br/>所有 Ref 已 Retain

    Note over BTree: 2. Lock + Read
    BTree->>LeafRef: Lock()
    BTree->>BTree: GetPageInfo → leaf
    BTree->>Storage: GetLeafPage(X)
    Storage-->>BTree: leaf

    Note over BTree: 3. CR-08: IsFull 检查（mutate 之前！）
    BTree->>BTree: leaf.IsFull(keyLen, valueLen)
    BTree-->>BTree: true (需要分裂)

    Note over BTree: 4. 执行 Leaf Split
    BTree->>Storage: leaf.Split()
    Note over Storage: Alloc Y1(left) + Alloc Y2(right)<br/>X 前半 → Y1, X 后半 → Y2
    Storage-->>BTree: leftPage{Y1}, rightPage{Y2}, splitKey

    Note over BTree: 5. 确定目标子页面
    BTree->>BTree: bytes.Compare(key, splitKey)
    Note over BTree: target = key < splitKey ? left : right

    Note over BTree: 6. CR-08: 在 target 上立即 mutate
    BTree->>Storage: mutate(targetPage) → COW
    Note over Storage: Alloc Z (double-COW, 优化项)
    Storage-->>BTree: mutatedPage{Z}

    Note over BTree: 7. 创建 PageRef（★ B6 修复）
    BTree->>BTree: mutatedRef = NewPageRef(mutation.newPageID)<br/>siblingRef = NewPageRef(unmutated split page)
    BTree->>BTree: mutatedRef.Retain()<br/>siblingRef.Retain()
    BTree->>Storage: FreePage(orphanPageID) // 回收 double-COW 孤儿

    Note over BTree,ParentRef: 8. Parent CAS (handleLeafSplit)
    BTree->>ParentRef: GetPageInfo → oldParentInfo
    BTree->>Storage: GetNodePage → CopyNodePage → InsertChild
    Note over Storage: ★ Bug B1 修复：InsertChild 的 left/right 参数<br/>mutated 侧用 mutation.newPageID<br/>未 mutated 侧用原始 split 页 ID
    BTree->>ParentRef: CAS(oldParentInfo, newParentInfo)

    alt Parent CAS 成功
        ParentRef-->>BTree: true

        Note over BTree: 9. Redirect+NewRef CAS（原子操作，无窗口期！）
        BTree->>LeafRef: CAS(oldInfo, {Tombstone: true, Redirect: true, NewRef: leftRef})
        Note over LeafRef: Tombstone+Redirect+NewRef 同一次 CAS<br/>Reader 检测到 Redirect 后重新导航

        Note over BTree: 9a. ★ updateChildrenCache（构建 ChildrenCache 内嵌 separators）
        Note over BTree: 从 CAS 后的 newNodePage 提取 separator keys（copy）<br/>构建 ChildrenCache{Children, Separators}<br/>children.Store(cache) 原子替换

        BTree->>LeafRef: Unlock()
        Note over BTree: size.Add(delta)
        BTree->>BTree: leftRef.Release()<br/>rightRef.Release()
        Note over BTree: Redirect+NewRef 作为 PageInfo 字段，无独立 Retain
        BTree->>BTree: path.ReleaseAll()
        BTree-->>Client: nil (强一致性成功)
    else Parent CAS 失败
        ParentRef-->>BTree: false

        Note over BTree: 10. Full Retry: 仅 Release + 孤儿回收（★ B7 修复）
        BTree->>BTree: mutatedRef.Release()<br/>siblingRef.Release()
        Note over BTree: Release→freeFunc 自动回收<br/>mutation.newPageID + sibling.PageID()
        BTree->>Storage: FreePage(newParentPage) // 无 PageRef 管理
        Note over BTree: orphanPageID 已在 Step 7 回收<br/>无需再次 FreePage
        BTree->>LeafRef: Unlock()
        BTree->>BTree: path.ReleaseAll()
        Note over BTree: goto Step 1 (完整重试)
    end
```

### 8.3 Root Split（极少数情况）

```mermaid
sequenceDiagram
    participant Client
    participant BTree as BTree.writeOperation
    participant RootRef as RootPageRef
    participant Storage as OffheapBTreeStorage

    Client->>BTree: Set(key, value)
    BTree->>BTree: searchPath(key)
    Note over BTree: path = [root(leaf)]<br/>len(path.entries) < 2 → root split

    BTree->>RootRef: Lock()

    Note over BTree: 1. Root Leaf Split
    BTree->>Storage: rootLeaf.Split()
    Storage-->>BTree: leftPage, rightPage, splitKey

    Note over BTree: 2. CR-08: 确定目标并 mutate
    BTree->>BTree: target = left or right
    BTree->>Storage: mutate(targetPage)
    Storage-->>BTree: mutatedPage

    Note over BTree: 3. 创建新 Root Node
    BTree->>Storage: AllocNodePage()
    Storage-->>BTree: newRootPage
    BTree->>BTree: newRootPage.InsertChild(0, splitKey, leftID, rightID)

    Note over BTree: 4. 创建 PageRef
    BTree->>BTree: leftRef = NewPageRef(...)<br/>rightRef = NewPageRef(...)<br/>leftRef.Retain()<br/>rightRef.Retain()

    Note over BTree: 5. ReplaceRoot (D14: 先 SetParentRef 后 CAS)
    BTree->>RootRef: ReplaceRoot(oldInfo, newInfo, &ChildrenCache{Children: [leftRef, rightRef], Separators: [copyKey(splitKey)]})
    Note over RootRef: 先设置 children.parentRef（遍历 cache.Children）<br/>再 CAS publish

    alt ReplaceRoot 成功
        RootRef-->>BTree: true

        Note over BTree: 6. 设置 ChildrenCache（B20：先于一切可见性设置）
        BTree->>RootRef: children.Store(&ChildrenCache{Children: [leftRef, rightRef], Separators: [copyKey(splitKey)]})

        Note over BTree: ★ B11 + Redirect+NewRef: Root Split 不需要 Redirect<br/>ReplaceRoot 已原子替换 pInfo<br/>rootRef 被复用为新 internal root<br/>无需 Tombstone/Redirect 语义

        Note over BTree: 7. 更新 size + metrics
        BTree->>BTree: size.Add(delta)
        BTree->>BTree: leftRef.Release()<br/>rightRef.Release()
        BTree->>RootRef: Unlock()
        BTree-->>Client: nil (Success)
    else ReplaceRoot 失败
        RootRef-->>BTree: false
        Note over BTree: ★ B10 修复：仅 Release + 孤儿 FreePage
        BTree->>BTree: leftRef.Release()<br/>rightRef.Release()
        BTree->>Storage: FreePage(orphanPageID)<br/>FreePage(newRootID)<br/>FreePage(newRootPage.PageID())
        BTree->>RootRef: Unlock()
        Note over BTree: goto Step 1
    end
```

### 8.4 并发 Reader 通过 Redirect 重新导航

> **★ ChildrenCache 改造**：Reader 重新导航时不再调 `GetNodePage + node.Search(key)`，改用 `updatedCache.Search(key)` 在内嵌 separators 上二分搜索。

```mermaid
sequenceDiagram
    participant Reader as Reader goroutine
    participant ParentRef as PageRef(Parent)
    participant ChildRef as PageRef(old child)
    participant UpdatedCache as 更新后 ChildrenCache
    participant LeftRef as PageRef(left)
    participant RightRef as PageRef(right)

    Note over Reader,RightRef: Writer 已完成 Parent CAS + Redirect CAS<br/>ChildrenCache 已更新（含 separators）

    Reader->>ParentRef: GetOrCreateChildren(storage)[idx]
    ParentRef-->>Reader: childRef（旧子页面引用）

    Reader->>ChildRef: GetPageInfo()
    Note over ChildRef: pInfo.Tombstone=true && Redirect=true && NewRef!=nil

    Reader->>ChildRef: Release()（释放旧子页面引用）

    Reader->>ParentRef: children.Load()（重新获取 ChildrenCache）
    ParentRef-->>Reader: updatedCache（包含 Children: [leftRef, rightRef], Separators: [splitKey]）

    Reader->>ParentRef: updatedCache.Search(key) → reIdx（★ 用内嵌 separators 二分搜索，不读 NodePage）

    alt key < splitKey → reIdx 指向 left
        Reader->>LeftRef: Retain()
        Reader->>Reader: childRef = LeftRef
    else key >= splitKey → reIdx 指向 right
        Reader->>RightRef: Retain()
        Reader->>Reader: childRef = RightRef
    end

    Note over Reader: 继续正常向叶子遍历
    Reader->>Reader: 继续遍历到叶子页...
```

### 8.5 并发 Split 竞争场景（★ B22）

> **场景**：两个 Writer 同时尝试分裂同一个 Parent 的不同子页面。

```mermaid
sequenceDiagram
    participant W1 as Writer 1
    participant W2 as Writer 2
    participant Parent as PageRef(Parent)
    participant Child1 as PageRef(Child1)
    participant Child2 as PageRef(Child2)
    participant Storage as OffheapBTreeStorage

    Note over W1,Storage: 初始状态：Parent 有两个子页面 Child1 和 Child2

    W1->>Child1: Lock()
    W2->>Child2: Lock()

    Note over W1,W2: 并行执行 Split

    W1->>Storage: Child1.Split() → left1, right1, splitKey1
    W2->>Storage: Child2.Split() → left2, right2, splitKey2

    Note over W1,W2: 各自准备 Parent InsertChild

    W1->>Parent: GetPageInfo() → oldParentInfo
    W2->>Parent: GetPageInfo() → oldParentInfo（相同）

    W1->>Storage: InsertChild(idx1, splitKey1, left1ID, right1ID) → newParent1
    W2->>Storage: InsertChild(idx2, splitKey2, left2ID, right2ID) → newParent2

    Note over W1,W2: CAS 竞争

    W1->>Parent: CAS(oldParentInfo, newParentInfo1)
    Parent-->>W1: true ✅

    W2->>Parent: CAS(oldParentInfo, newParentInfo2)
    Parent-->>W2: false ❌（oldParentInfo 已过期）

    Note over W2: W2 CAS 失败 → 完整重试

    W1->>Parent: children.Store(&ChildrenCache{Children: newChildren1, Separators: newSeps1})（★ 含 separators）
    W1->>Child1: Redirect CAS(Tombstone+Redirect+NewRef)
    W1->>Child1: Unlock()
    W1->>Child1: Unlock()

    W2->>Child2: Unlock()
    W2->>Storage: 清理 left2, right2, newParent2
    Note over W2: goto Step 1 (完整重试) — 重新 searchPath，基于新 parentInfo 重新计算 idx2
```

**关键点**：
- **Parent-Level 串行化**：Parent CAS 确保同一时刻只有一个 Split 成功
- **失败方清理**：W2 CAS 失败后，释放所有新分配的页面（left2, right2, newParent2）
- **无死锁**：不同 Writer 锁定不同的 Child，Parent CAS 决定胜负
- **★ 重试必须重新 searchPath**：W2 重试时必须重新执行 `searchPath` 获取最新的 parentInfo，基于新的 pInfo 重新 `Search(key)` 计算正确的 idx。**不能直接使用 CAS 失败前缓存的旧 idx**，因为 parent 的 children 数组结构已改变（插入了新的 left1Ref/right1Ref）
```

**关键点**：
- **Parent-Level 串行化**：Parent CAS 确保同一时刻只有一个 Split 成功
- **失败方清理**：W2 CAS 失败后，释放所有新分配的页面（left2, right2, newParent2）
- **无死锁**：不同 Writer 锁定不同的 Child，Parent CAS 决定胜负
- **★ ChildrenCache 原子更新**：children.Store 替换为 ChildrenCache（含 separators + children），读者通过 cache.Search(key) 导航，不依赖物理 NodePage
- **★ 重试必须重新 searchPath**：W2 重试时必须重新执行 `searchPath` 获取最新的 parentInfo，基于新的 pInfo 重新 `cache.Search(key)` 计算正确的 idx。**不能直接使用 CAS 失败前缓存的旧 idx**，因为 parent 的 ChildrenCache 已被替换（插入了新的 left1Ref/right1Ref 和更新的 separators）

**文件**: `internal/infrastructure/storage/btree/operations.go`

### 9.1 当前实现（Phase 5）

```go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    for attempt := 0; attempt < MaxCASRetries; attempt++ {
        path, _ := searchPath(b.storage, b.rootRef, key)
        leafRef := path.Leaf().Ref
        leafRef.Lock()

        pInfo := leafRef.GetPageInfo()
        oldLeaf, _ := b.storage.GetLeafPage(pInfo.PageID)

        result, err := mutate(oldLeaf)  // 直接 mutate，无 IsFull 检查
        if err != nil { ... }

        newInfo := &PageInfo{PageID: result.newPageID, Version: pInfo.Version + 1}
        if leafRef.CAS(pInfo, newInfo) {
            leafRef.Unlock()
            propagateUpward(b, path.ParentPath(), result.newPageID, ...)  // Best-Effort
            b.size.Add(result.delta)
            path.ReleaseAll()
            return nil
        }
        // CAS 失败 → cleanup + retry
    }
    return ErrCASConflict
}
```

### 9.2 CR-08 目标状态（Phase 6.0）

```go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    for attempt := 0; attempt < MaxCASRetries; attempt++ {
        // ★ Review 修复 B8：必须检查 searchPath 错误
        // searchPath 可能返回 ErrRetry（Tombstone 窗口期）或其他错误
        path, err := searchPath(b.storage, b.rootRef, key)
        if err != nil {
            if errors.Is(err, ErrRetry) {
                continue  // Tombstone 窗口，立即重试
            }
            return errpkg.BTreeWriteOpSearch(err)
        }

        leafRef := path.Leaf().Ref
        leafRef.Lock()

        pInfo := leafRef.GetPageInfo()
        if pInfo == nil || pInfo.Tombstone {
            // 页面已释放或已分裂，重试
            leafRef.Unlock()
            path.ReleaseAll()
            continue
        }

        leaf, _ := b.storage.GetLeafPage(pInfo.PageID)

        // ★ CR-08: IsFull 检查在 mutate 之前
        if leaf.IsFull(len(key), len(value)) {
            // ★ Root Split 检测：path 长度 < 2 表示 root 是 leaf
            if len(path) < 2 {
                splitErr := b.handleRootSplit(leafRef, pInfo, path, key, mutate)
                leafRef.Unlock()
                path.ReleaseAll()
                if splitErr == nil {
                    return nil
                }
                if errors.Is(splitErr, ErrCASConflict) {
                    continue
                }
                return splitErr
            }
            splitErr := b.handleLeafSplit(leafRef, pInfo, path, key, mutate)
            leafRef.Unlock()
            path.ReleaseAll()

            if splitErr == nil {
                return nil  // ✅ 强一致性：Split + Insert 一次完成
            }
            if errors.Is(splitErr, ErrCASConflict) {
                continue  // Parent CAS 失败，完整重试
            }
            return splitErr  // 其他错误（ErrDuplicateKey 等）
        }

        // 正常路径：mutate → CAS
        result, err := mutate(leaf)
        if err != nil { ... }

        newInfo := &PageInfo{PageID: result.newPageID, Version: pInfo.Version + 1}
        if leafRef.CAS(pInfo, newInfo) {
            leafRef.Unlock()
            propagateUpward(b, path.ParentPath(), result.newPageID, ...)
            b.size.Add(result.delta)
            path.ReleaseAll()
            return nil
        }
        // CAS 失败 → cleanup + retry
    }
    return ErrCASConflict
}
```

### 9.3 handleLeafSplit 伪代码（含 Bug 修复）

```go
func (b *BTree) handleLeafSplit(leafRef *PageRef, leafInfo *PageInfo,
    path SearchPath, key []byte, mutate mutateFunc) error {

    // Step 1: Split leaf → left + right + splitKey
    leaf, _ := b.storage.GetLeafPage(leafInfo.PageID)
    leftPage, rightPage, splitKey, _ := leaf.Split()

    // Step 2: CR-08 — determine target child and immediate insert
    var target LeafPage
    var sibling LeafPage
    if bytes.Compare(key, splitKey) < 0 {
        target, sibling = leftPage, rightPage
    } else {
        target, sibling = rightPage, leftPage
    }

    // Step 3: Mutate target (double-COW)
    mutation, err := mutate(target)
    if err != nil {
        // 清理 split 页面
        b.storage.FreePage(leftPage.PageID())
        b.storage.FreePage(rightPage.PageID())
        return err
    }

    // ★ Review 修复 B6：PageRef 必须绑定 double-COW 后的实际 PageID
    // mutated 侧：mutation.newPageID（COW 后的页面）
    // 未 mutated 侧：原始 split 页面 ID
    // 同时，未被 PageRef 引用的孤儿 split 页面需要显式回收
    var leftRef, rightRef *PageRef
    var orphanPageID model.PageID  // 被 double-COW 替换的原始 split 页面
    if bytes.Compare(key, splitKey) < 0 {
        // target = left → left 被 mutate
        leftRef = NewPageRef(mutation.newPageID, ...)
        rightRef = NewPageRef(rightPage.PageID(), ...)
        orphanPageID = leftPage.PageID()   // left 被 double-COW 替换，成为孤儿
    } else {
        // target = right → right 被 mutate
        leftRef = NewPageRef(leftPage.PageID(), ...)
        rightRef = NewPageRef(mutation.newPageID, ...)
        orphanPageID = rightPage.PageID()  // right 被 double-COW 替换，成为孤儿
    }
    leftRef.Retain()   // refCount: 0 → 1
    rightRef.Retain()  // refCount: 0 → 1

    // 回收孤儿页面（double-COW 产生的、不被任何 PageRef 引用的原始 split 页面）
    b.storage.FreePage(orphanPageID)

    // Step 4: Parent InsertChild (COW)
    parentEntry := path[len(path)-2]  // leaf 的父节点
    parentRef := parentEntry.Ref
    parentInfo := parentRef.GetPageInfo()
    if parentInfo == nil {
        // 父页面已释放，完整重试
        leftRef.Release(); rightRef.Release()  // refCount → 0 → freeFunc 回收
        return ErrCASConflict
    }

    oldParent, _ := b.storage.GetNodePage(parentInfo.PageID)

    // Step 5: InsertChild — ★ P2-1 修复：直接使用 Step 4 已确定的局部变量
    // 不通过 GetPageInfo() 读取（虽然无竞争时等价，但局部变量更明确安全）
    var leftChildID, rightChildID model.PageID
    if bytes.Compare(key, splitKey) < 0 {
        leftChildID = mutation.newPageID   // target=left, left 被 double-COW
        rightChildID = rightPage.PageID()  // right 未 mutate
    } else {
        leftChildID = leftPage.PageID()    // left 未 mutate
        rightChildID = mutation.newPageID  // target=right, right 被 double-COW
    }
    newParent, _ := oldParent.InsertChild(
        parentEntry.Index, splitKey,
        leftChildID,   // Step 4 已确定
        rightChildID,  // Step 4 已确定
    )

    newParentInfo := &PageInfo{PageID: newParent.PageID(), Version: parentInfo.Version + 1}

    // Step 6: Parent CAS
    if !parentRef.CAS(parentInfo, newParentInfo) {
        // CAS 失败 → 完整清理
        // ★ Review 修复 B7：仅 Release，不做显式 FreePage（避免 double-free）
        // Release() 在 refCount=0 时自动调用 freeFunc → FreePage
        leftRef.Release()   // refCount 1→0 → freeFunc(leftRef.pageID)
        rightRef.Release()  // refCount 1→0 → freeFunc(rightRef.pageID)
        b.storage.FreePage(newParent.PageID())  // newParent 无 PageRef，需显式回收
        return ErrCASConflict
    }

    // Step 7: ★ Redirect+NewRef — 单次原子 CAS（替代旧 SetSplitMarker + Tombstone CAS 两步操作）
    // Tombstone + Redirect + NewRef 在同一次 CAS 中原子生效，无窗口期
    // 消除旧 SplitMarker 设计的 Bug B2（顺序错误导致 reader 丢失）
    redirectInfo := &PageInfo{
        PageID:    leafInfo.PageID,
        Version:   leafInfo.Version + 1,
        Tombstone: true,
        Redirect:  true,
        NewRef:    leftRef,  // 指向分裂后的左子页面
    }
    // 此 CAS 在 leafRef 持锁期间执行，且 pInfo 未被修改过 → 必然成功
    leafRef.CAS(leafInfo, redirectInfo)

    // Step 9: ★ B18 修复 + ★ B19 修复 — 直接设置 parent 的 ChildrenCache（含 separators），复用旧 children 中的 PageRef
    //
    // ★ ChildrenCache 改造：构建 ChildrenCache{Children, Separators}，而非 []*PageRef
    //   Separators 从 CAS 后的 newNodePage（= newParent）提取 separator keys（必须 copy，GetKey 返回 mmap slice）
    //   同时按 B19 修复：复用旧 ChildrenCache.Children 中的 PageRef 对象
    //
    // B18 修复：GetOrCreateChildren 创建全新的 PageRef 对象（refCount=0），
    //   与 leftRef/rightRef 是独立对象。InvalidateChildren 后 Release leftRef/rightRef
    //   → refCount→0 → freeFunc → 物理页面被回收，但 parent 的 child pointer 仍指向该 PageID → UAF。
    //
    // B19 修复：不在 children.Store 前创建新的 PageRef 对象，
    //   而是从旧 ChildrenCache.Children 数组中直接复用现有 PageRef 对象。
    //   避免同一 pageID 上创建两个独立 PageRef → 旧 cache 被替换后 refCount 混淆 → UAF。
    //
    // ★ Redirect+NewRef 优势：无需 ClearSplitMarker。NewRef 作为 PageInfo 字段随 CAS 生命周期管理，
    //   不存在独立的 Retain/Release 循环，简化引用计数管理。

    // --- 构建新的 Children 数组（B19 复用模式不变）---
    oldCache := parentRef.children.Load()  // ★ B19 修复：获取旧 cache 以便复用其 Children
    var oldChildren []*PageRef
    if oldCache != nil {
        oldChildren = oldCache.Children
    }
    newChildCount := oldParent.Count() + 1   // InsertChild 后 count+1
    newChildren := make([]*PageRef, newChildCount)
    for i := 0; i < newChildCount; i++ {
        if i == parentEntry.Index {
            newChildren[i] = leftRef   // ★ 直接放入，无需额外 Retain
        } else if i == parentEntry.Index+1 {
            newChildren[i] = rightRef  // ★ 同上
        } else {
            srcIdx := i
            if i > parentEntry.Index+1 {
                srcIdx = i - 1
            }
            if oldChildren != nil && srcIdx < len(oldChildren) && oldChildren[srcIdx] != nil {
                newChildren[i] = oldChildren[srcIdx]  // ★ B19: 复用同一 PageRef 对象
            } else {
                // 防御：旧 children 为 nil 或越界时，从 newParent 页面数据构建（理论上不会发生）
                childID := newParent.GetChildID(i)
                ref := NewPageRef(childID, 0, parentRef, parentRef.freeFunc)
                ref.Retain()
                newChildren[i] = ref
            }
        }
    }

    // --- ★ ChildrenCache 改造：从 CAS 后的 newParent 提取 separator keys（copy）---
    newSeparators := make([][]byte, newChildCount-1)  // len(Separators) == len(Children) - 1
    for i := 0; i < newChildCount-1; i++ {
        keyOff, keyLen := newParent.GetKeyOffset(i)  // 从 COW 后的 newParent 提取
        rawKey := newParent.pa.GetKey(uint32(newParent.PageID()), keyOff, keyLen)
        newSeparators[i] = copyKey(rawKey)  // ★ 必须拷贝（GetKey 返回 mmap slice）
    }

    newCache := &ChildrenCache{
        Children:   newChildren,
        Separators: newSeparators,
    }
    parentRef.children.Store(newCache)  // ★ 原子替换（类型从 []*PageRef 变为 *ChildrenCache）

    // ★ Redirect+NewRef：无需 ClearSplitMarker
    // 旧设计中 ClearSplitMarker 释放 SplitMarker 的 Retain（refCount -1），
    // 新设计中 NewRef 作为 PageInfo 字段，不涉及独立的 Retain/Release 循环。
    // leftRef 被 newChildren 持有，refCount=1，安全。

    // Step 10: Update metrics + size
    b.size.Add(mutation.delta)
    // ★ 引用计数追踪（Redirect+NewRef 设计）：
    //   leftRef:  Retain(Step4)=1 → 被 newChildren[i] 持有 → refCount=1，安全
    //   rightRef: Retain(Step4)=1 → 被 newChildren[i+1] 持有 → refCount=1，安全
    //   注意：不再 Release leftRef/rightRef！它们的生命周期由 parent.children 管理。
    //   回收时机：parentRef 的 children 被 InvalidateChildren 或重建时，
    //   对旧 children 中的每个 childRef 调用 Release（包括 leftRef/rightRef）。
    //   但只有当 parent 本身被 split 或 tree 关闭时才会发生。

    return nil
}
```

**关键修正点**：
1. **Review B6**（§9.3 Step 3-4）：PageRef 绑定 double-COW 后的实际 PageID（`mutation.newPageID`），而非原始 split 页面。确保 Redirect NewRef 和父节点 InsertChild 指向同一物理页面。
2. **Bug B1**（§9.3 Step 5）：InsertChild 直接从 PageRef 读取 PageID，保证与 Redirect NewRef 一致。
3. **Review B7**（§9.3 Step 6）：CAS 失败路径移除显式 `FreePage(leftPage.PageID())` / `FreePage(rightPage.PageID())`，仅依赖 `Release()` → `freeFunc` 自动回收，避免 double-free。
4. **★ Redirect+NewRef**（§9.3 Step 7）：`Tombstone + Redirect + NewRef` 在**同一次 CAS** 中原子设置，彻底消除了旧 SplitMarker 设计中 `SetSplitMarker` 和 `Tombstone CAS` 之间的窗口期（Bug B2）。无需独立的 SetSplitMarker / ClearSplitMarker 操作。
5. **孤儿页面回收**（§9.3 Step 3 后）：double-COW 替换的原始 split 页面显式 `FreePage(orphanPageID)`。
6. **★ Review B18 + ChildrenCache**（§9.3 Step 9）：直接构建 `ChildrenCache{Children, Separators}` 并 `children.Store`，而非 `InvalidateChildren + GetOrCreateChildren` 重建。`GetOrCreateChildren` 创建独立 PageRef 对象（refCount=0），与 leftRef/rightRef 的引用计数互不关联。修复后 leftRef/rightRef 被 cache.Children 持有（refCount≥1），Separators 从 CAS 后的 newParent 提取（copy）。不再在 handleLeafSplit 中释放 leftRef/rightRef，生命周期由 parent.children（`*ChildrenCache`）管理。

### 9.4 handleRootSplit 伪代码（Root Leaf 分裂）

当树只有一个叶子根节点（`len(path) < 2`）且该叶子满时，需要创建新的根节点。

```go
func (b *BTree) handleRootSplit(leafRef *PageRef, rootInfo *PageInfo,
    path SearchPath, key []byte, mutate mutateFunc) error {

    // Step 1: Split root leaf → left + right + splitKey
    rootLeaf, _ := b.storage.GetLeafPage(rootInfo.PageID)
    leftPage, rightPage, splitKey, _ := rootLeaf.Split()

    // Step 2: CR-08 — determine target and immediate insert
    var target LeafPage
    if bytes.Compare(key, splitKey) < 0 {
        target = leftPage
    } else {
        target = rightPage
    }

    // Step 3: Mutate target (double-COW)
    mutation, err := mutate(target)
    if err != nil {
        b.storage.FreePage(leftPage.PageID())
        b.storage.FreePage(rightPage.PageID())
        return err
    }

    // ★ Review 修复 B10：与 §9.3 同理，追踪 double-COW 产生的孤儿页面
    var orphanPageID model.PageID
    if bytes.Compare(key, splitKey) < 0 {
        orphanPageID = leftPage.PageID()   // left 被 double-COW 替换
    } else {
        orphanPageID = rightPage.PageID()  // right 被 double-COW 替换
    }

    // Step 4: Determine final left/right child IDs
    var leftChildID, rightChildID model.PageID
    if bytes.Compare(key, splitKey) < 0 {
        leftChildID = mutation.newPageID   // left mutated
        rightChildID = rightPage.PageID()  // right unchanged
    } else {
        leftChildID = leftPage.PageID()    // left unchanged
        rightChildID = mutation.newPageID  // right mutated
    }

    // Step 5: Create new root node page
    newRootID, _ := b.storage.AllocNodePage()
    newRootPage, _ := b.storage.GetNodePage(newRootID)

    // Insert left/right as children of new root
    // Index 0: splitKey separates leftChildID (< splitKey) and rightChildID (>= splitKey)
    newRootPage, _ = newRootPage.InsertChild(0, splitKey, leftChildID, rightChildID)

    // Step 6: Create PageRefs with parentRef = rootRef
    leftRef := NewPageRef(leftChildID, 0, &b.rootRef.PageRef, freePageFunc(b))
    rightRef := NewPageRef(rightChildID, 0, &b.rootRef.PageRef, freePageFunc(b))
    leftRef.Retain()
    rightRef.Retain()

    // Step 7: ReplaceRoot (D14: SetParentRef before CAS)
    newRootInfo := &PageInfo{
        PageID:  newRootPage.PageID(),
        Version: rootInfo.Version + 1,
    }
    // ★ ChildrenCache 改造：ReplaceRoot 参数从 []*PageRef 改为 *ChildrenCache
    newChildren := &ChildrenCache{
        Children:   []*PageRef{leftRef, rightRef},
        Separators: [][]byte{copyKey(splitKey)},  // ★ 1 个 separator（2 个 children - 1）
    }

    if !b.rootRef.ReplaceRoot(rootInfo, newRootInfo, newChildren) {
        // CAS 失败 → cleanup
        // ★ Review 修复 B10：与 §9.3 B7 同理，仅 Release + 孤儿页面 FreePage
        leftRef.Release()   // refCount→0 → freeFunc(leftChildID)
        rightRef.Release()  // refCount→0 → freeFunc(rightChildID)
        // 显式回收孤儿页面（不被任何 PageRef 管理）
        b.storage.FreePage(orphanPageID)          // double-COW 替换的原始 split 页面
        b.storage.FreePage(newRootID)             // InsertChild COW 替换的空白 NodePage
        b.storage.FreePage(newRootPage.PageID())  // InsertChild COW 产出页
        return ErrCASConflict
    }

    // Step 8: ★ B20 修复 — 先设置 children
    //
    // 安全性论证（B20 修复的核心）：
    // GetOrCreateChildren 只有在 children == nil 时才触发懒初始化。
    // 在 ReplaceRoot CAS 成功前，children 一直是 nil（旧 leaf root 无 children）。
    // ReplaceRoot CAS 成功后，children 立刻被 Store 为非 nil（同一 goroutine，无调度点）。
    // 因此，不存在 "pInfo 已指向新 internal root 但 children 仍为 nil" 的可观测窗口。
    //
    // ★ Redirect+NewRef 优势：Root Split 不需要 SetSplitMarker/ClearSplitMarker。
    //   ReplaceRoot CAS 原子替换 pInfo（指向新 internal root），并发 reader 看到的
    //   rootRef.pInfo 已经是新的 internal node。无需在 rootRef 上设置 Redirect。
    //   这与 handleLeafSplit 不同：handleLeafSplit 中旧 leafRef 需要设置 Redirect
    //   让 reader 知道它已分裂；但 handleRootSplit 中 rootRef 被复用为新的 internal root，
    //   不需要 Tombstone/Redirect 语义。
    //
    // ★ ChildrenCache 改造：Store 的是 *ChildrenCache，而非 []*PageRef
    //   newChildren 已在 Step 7 构建（含 Separators）
    b.rootRef.children.Store(newChildren)  // ★ 参数已是指针，Store(newChildren) 而非 Store(&newChildren)

    // Step 9: Update metrics + size
    b.size.Add(mutation.delta)
    if b.metrics != nil {
        b.metrics.IncrementSplit()
        b.metrics.IncrementTreeHeight() // Tree height increased
    }

    // Step 10: Cleanup orphan pages
    // ★ 引用计数追踪（Redirect+NewRef + ChildrenCache 设计）：
    //   leftRef:  Retain(Step6)=1 → 被 newChildren.Children[0] 持有 → refCount=1，安全
    //   rightRef: Retain(Step6)=1 → 被 newChildren.Children[1] 持有 → refCount=1，安全
    //   注意：不再 Release leftRef/rightRef！生命周期由 rootRef.children（*ChildrenCache）管理。
    //   无需 ClearSplitMarker（无 SplitMarker）。
    _ = b.storage.FreePage(orphanPageID)    // double-COW 替换的原始 split 页面
    _ = b.storage.FreePage(newRootID)       // InsertChild COW 替换的空白 NodePage

    return nil
}
```

**与 handleLeafSplit 的关键区别**：
1. **无 Parent CAS**：通过 `ReplaceRoot` 原子替换根节点
2. **树高度增加**：新根是内部节点，原根叶子分裂为两个子节点
3. **无需 Redirect/Tombstone**：rootRef 被 ReplaceRoot CAS 原子替换为新 internal root，不需要像 handleLeafSplit 那样在旧 leafRef 上设置 Redirect（B11 修复）
4. **D14 决策**：`ReplaceRoot` 内部先 `SetParentRef`（遍历 `newChildren.Children`）再 CAS，消除 parentRef==nil 窗口
5. **★ B18 + B20 修复**：children.Store 紧接在 ReplaceRoot CAS 之后（消除懒初始化 UAF）；leftRef/rightRef 生命周期由 rootRef.children（`*ChildrenCache`）管理，不释放
6. **★ ChildrenCache 改造**：`ReplaceRoot` 参数从 `[]*PageRef` 改为 `*ChildrenCache`；`children.Store` 存储含 separators 的 ChildrenCache，searchPath 用 `cache.Search(key)` 导航

---

## 10. 内部节点分裂 — Cascading Split（设计，未实现）

> **状态**：设计完成，代码尚未实现。当前实现仅处理 leaf split → parent InsertChild，
> 当 parent 内部节点也满时无法继续，限制了树高不超过 2 层（约 4760 keys）。

### 10.1 问题

当前 handleLeafSplit 流程：leaf split → parent InsertChild → parent CAS。

**当 parent 内部节点在 InsertChild 后也满**（`newParent.IsFull()`），无法继续增长。

**容量限制**：
- 2 层树：126（内部节点容量）× ~38 keys/leaf ≈ 4760 keys
- 超过此数量后，InsertChild 使 parent 满，后续操作无法处理

### 10.2 解决方案 — 递归向上分裂

**核心算法**：parent CAS 成功后，检查 `newParent.IsFull()`。如果满，分裂 parent 并向上传播。

```
handleLeafSplit (现有):
  1. leaf.Split() → leftPage, rightPage, splitKey
  2. parent.InsertChild(idx, splitKey, leftID, rightID)
  3. parent CAS
  4. if newParent.IsFull(keyLen, valueLen) → handleInternalSplit(parentRef, parentInfo, ...)

handleInternalSplit (新增):
  1. parent.Split() → parentLeft, parentRight, parentSplitKey  (move-up 语义)
  2. grandparentRef := parentRef.GetParentRef()
  3. if grandparentRef == nil → handleRootSplit(parentRef, ...)  // root 是 internal node
  4. else: grandparent.InsertChild(idx, parentSplitKey, parentLeftID, parentRightID)
  5. grandparent CAS
  6. if newGrandparent.IsFull() → 递归继续向上
  7. 循环直到 root 或非满 ancestor
```

### 10.3 Node Split 语义 — Move-Up

内部节点分裂使用 **Move-Up** 语义（区别于 Leaf Split 的 Copy-Up）：

```
Split 前 (count=3, children=4):
┌──────────────────────────────────────────────────────┐
│ [e0(k0,c0), e1(k1,c1), e2(k2,c2)] + extraChild=c3   │
└──────────────────────────────────────────────────────┘
              mid=1 ↑

Split 后:
Left (1 entry):                    Right (1 entry):
┌──────────────────┐             ┌──────────────────┐
│ [e0(k0,c0)] + c1 │             │ [e2(k2,c2)] + c3 │
└──────────────────┘             └──────────────────┘

splitKey = k1（move-up：不保留在 left 或 right 中，提升到 grandparent）
```

**与 Leaf Split 的关键区别**：
- splitKey **不保留**在 left 或 right 中（move-up）
- Left 的 extraChild = child[mid]
- Right 的 extraChild = child[count]（原始 extraChild）

### 10.4 容量计算

实现 cascading split 后的树容量：

| 层级 | 计算 | 容量 |
|------|------|------|
| 2 层 | 126 × ~38 keys/leaf | ~4.7K keys |
| 3 层 | 126^2 × ~38 keys/leaf | ~600K keys |
| 4 层 | 126^3 × ~38 keys/leaf | ~76M keys |
| 5 层 | 126^4 × ~38 keys/leaf | ~9.5B keys |

实际使用中 3-4 层即可覆盖绝大多数场景。

### 10.5 并发设计

**每层 CAS 独立**，与 leaf split 的并发模型一致：

```
handleInternalSplit (iterative, per-level CAS):
  1. currentRef := parentRef
     currentInfo := parentInfo

  loop:
    2. currentNode, _ := storage.GetNodePage(currentInfo.PageID)
       currentLeft, currentRight, splitKey := currentNode.Split()

    3. grandparentRef := currentRef.GetParentRef()
       if grandparentRef == nil:
         // Root split (root is internal node, not leaf)
         // ★ 须在 step 3a 之前检查 nil，避免用 nil 创建 PageRef
         handleRootInternalSplit(currentRef, currentInfo, currentLeft, currentRight, splitKey)
         // handleRootInternalSplit 内部创建 leftRef/rightRef
         return

       // ★ P4 fix: 获取 currentRef 在 grandparent children 中的 index
       idx := findChildIndex(grandparentRef, currentRef)

    3a. // ★ P2 fix: 创建 PageRef + Retain（生命周期由 parent children 管理）
       //   grandparentRef 已在 step 3 获取，确保 non-nil
       currentLeftRef := NewPageRef(currentLeft.PageID(), 0, grandparentRef, freeFunc)
       currentRightRef := NewPageRef(currentRight.PageID(), 0, grandparentRef, freeFunc)
       currentLeftRef.Retain()   // refCount: 0 → 1
       currentRightRef.Retain()  // refCount: 0 → 1

    4. grandparentInfo := grandparentRef.GetPageInfo()
       if grandparentInfo == nil || grandparentInfo.Tombstone:
         // grandparent 已变化，完整重试
         currentLeftRef.Release()
         currentRightRef.Release()
         cleanup(currentLeft, currentRight)
         return ErrCASConflict

    5. newGrandparent := oldGrandparent.InsertChild(idx, splitKey, currentLeft.PageID(), currentRight.PageID())
       if !grandparentRef.CAS(grandparentInfo, newGrandparentInfo):
         // CAS 失败，完整重试
         currentLeftRef.Release()
         currentRightRef.Release()
         cleanup(currentLeft, currentRight, newGrandparent)
         return ErrCASConflict

    6. // CAS 成功：设置 Redirect on old internal node
       // ★ CAS-R1: Redirect CAS 可被并发 propagateUpward 抢先修改 currentRef.pInfo
       //   如果失败：parent 已更新指向 left/right，新 reader 不会到达此节点（通过 updated children）
       //   旧 reader 使用 COW 旧数据（安全）。Redirect 是优化（加速重新导航），非正确性要求。
       //   如果成功：reader 检测到 Tombstone+Redirect，立即从 parent children 重新导航（更快）。
       redirectInfo := &PageInfo{
         PageID:    currentInfo.PageID,
         Version:   currentInfo.Version + 1,
         Tombstone: true,
         Redirect:  true,
         NewRef:    currentLeftRef,
       }
       if !currentRef.CAS(currentInfo, redirectInfo):
         // Redirect CAS 失败 — 被 propagateUpward 抢先
         // 安全性：parent CAS 已成功，新 reader 走 updated children path
         // 旧 reader 使用 COW 旧数据（pageID 未变，内容 COW 安全）
         // 无需重试，继续执行

    7. // ★ RC-I1: 所有权转移 — currentLeftRef/currentRightRef 的生命周期现由
       //   grandparentRef.children（*ChildrenCache）管理。后续 loop iteration 或失败 cleanup
       //   不需要 Release 这些 refs。它们将在 parent children 被替换或 tree Close 时释放。
       //   ★ ChildrenCache 改造：更新 grandparent children 缓存（B18/B19 修复模式 + separators）
       //   重用旧 ChildrenCache.Children 中的 PageRef，插入 currentLeftRef/currentRightRef
       //   同时从 CAS 后的 newGrandparent 提取 separator keys（copy）构建新 ChildrenCache

    8. // ★ P3 fix: 检查 newGrandparent 是否也满（在 CAS 成功后、继续向上之前）
       if !newGrandparent.IsFull(keyLen, valueLen):
         return nil  // 完成

    9. // 继续向上
       currentRef = grandparentRef
       currentInfo = newGrandparentInfo
       goto loop
```

**关键并发保证**：
- **每层独立 CAS**：不持有跨层锁，避免死锁（只持 leafRef.Lock，parent 操作无锁 CAS）
- **Parent-Level 串行化**：同一 parent 的并发 split 通过 CAS 竞争决定胜负
- **失败方完整重试**：CAS 失败的 Writer 释放所有新页面，重新 searchPath
- **Redirect 原子生效**：内部节点 split 后的 Redirect+NewRef 与 leaf split 一致
- **CAS-R1（Redirect CAS 安全性）**：Redirect CAS 可能因并发 propagateUpward 而失败，但安全性不依赖 Redirect — parent CAS 已确保新 reader 使用 updated children path
- **RC-I1（所有权转移）**：Split 产出的 PageRef 在存入 parent.children 后，生命周期由 children cache 接管

### 10.6 Root Internal Split

当分裂传播到根节点（根是 internal node，不是 leaf）时：

```
handleRootInternalSplit:
  1. rootInternal.Split() → left, right, splitKey (move-up)
  2. Create new root node (更高一层)
  3. newRoot.InsertChild(0, splitKey, left, right)
  4. ReplaceRoot CAS（原子替换 pInfo）★ 参数为 *ChildrenCache{Children:[leftRef,rightRef], Separators:[copyKey(splitKey)]}
  5. children.Store 已在 ReplaceRoot 后执行（★ 与 handleRootSplit 一致）
  6. 树高度 +1
```

与 handleRootSplit（Section 9.4）的区别：
- Split 的是 internal node，不是 leaf
- 使用 move-up 语义（splitKey 不保留在 left/right 中）
- 其他流程相同（ReplaceRoot CAS + children.Store）

### 10.7 Redirect for Internal Nodes

内部节点 split 后的 Redirect 与 leaf 完全一致：

```go
// CAS 原子设置 Tombstone + Redirect + NewRef
redirectInfo := &PageInfo{
    PageID:    currentInfo.PageID,
    Version:   currentInfo.Version + 1,
    Tombstone: true,
    Redirect:  true,
    NewRef:    leftRef,  // 指向分裂后的左半部分
}
currentRef.CAS(currentInfo, redirectInfo)
```

**searchPath 中的处理**：与 leaf Redirect 相同 — 检测 Tombstone+Redirect 后从父节点 children 重新导航。

### 10.8 propagateUpward 变更说明

> **★ propagateUpward 已禁用**：`propagateUpward` 在 ChildrenCache 改造后确认禁用。原因：propagateUpward 改 parent pInfo 后，ChildrenCache 中的 separators 与新物理页不一致，会引入新的导航错误。如果未来恢复，**必须同时更新 parent 的 ChildrenCache（含 separator keys）**，否则引入新的不一致。详见 `2026-04-06-children-cache-separators.md` Step 4。

**现状**: `propagateUpward` (operations.go:168) 已被注释禁用（best-effort 模式，遇到 Tombstone 就停止传播）。

**变更**: 实现 cascading split 后，`propagateUpward` 将被 `handleInternalSplit` 替代：

> **★ propagateUpward 已禁用**（ChildrenCache 改造后）：`propagateUpward` 在当前代码中已被注释禁用。原因：它改 parent pInfo 后不更新 ChildrenCache 中的 separators，导致 readers 用 stale separators 导航。如果未来恢复 propagateUpward，**必须同时更新 parent 的 ChildrenCache（含 separator keys）**。详见 `2026-04-06-children-cache-separators.md` Step 4。

- **非满路径**: `writeOperation` 的 leaf CAS 成功后，如果 parent 不满，~~调用 `propagateUpward`~~（当前已禁用）
- **满路径**: 如果 `InsertChild` 后发现 parent 满，改用 `handleInternalSplit`（新路径）
- **触发点**: `writeOperation` 的 `propagateUpward` 调用处需增加 IsFull 检查：
  ```go
  // 替换 operations.go:142-144
  // ★ 注意：当前 propagateUpward 已禁用，以下代码为未来恢复时的目标状态
  if parentPath := path.ParentPath(); len(parentPath) > 0 {
      if newParent.IsFull(0, 0) {
          // Parent full → cascading split
          b.handleInternalSplit(parentRef, newParentInfo, ...)
      } else {
          // ★ 恢复 propagateUpward 时必须同时更新 parent 的 ChildrenCache（含 separators）
          propagateUpward(b, parentPath, result.newPageID, parentPath[len(parentPath)-1].Index)
      }
  }
  ```

### 10.9 Cleanup 策略 — 跨层 Orphan Page 追踪

Cascading split 在多层失败时需要清理所有已分配的 pages。建议使用 `allocatedPages` 追踪 + defer cleanup：

```go
func (b *BTree) handleInternalSplit(currentRef *PageRef, currentInfo *PageInfo, ...) error {
    var allocatedPages []model.PageID
    var retainedRefs []*PageRef

    defer func() {
        if err != nil {
            // 释放所有已分配的 raw pages
            for _, id := range allocatedPages {
                _ = b.storage.FreePage(id)
            }
            // 释放所有 Retained PageRefs
            for _, ref := range retainedRefs {
                ref.Release()
            }
        }
    }()

    // Split 产出 pages
    currentLeft, currentRight, splitKey := currentNode.Split()
    allocatedPages = append(allocatedPages, currentLeft.PageID(), currentRight.PageID())

    // PageRef 创建
    currentLeftRef := NewPageRef(currentLeft.PageID(), 0, grandparentRef, freeFunc)
    currentRightRef := NewPageRef(currentRight.PageID(), 0, grandparentRef, freeFunc)
    currentLeftRef.Retain()
    currentRightRef.Retain()
    retainedRefs = append(retainedRefs, currentLeftRef, currentRightRef)

    // InsertChild COW 产出
    newGrandparent := oldGrandparent.InsertChild(idx, splitKey, ...)
    allocatedPages = append(allocatedPages, newGrandparent.PageID())

    // CAS 成功后从追踪列表移除（所有权转移给 parent.children）
    if grandparentRef.CAS(grandparentInfo, newGrandparentInfo) {
        allocatedPages = removePage(allocatedPages, currentLeft.PageID()) // 不再是 orphan
        // ...
    }
}
```

**关键原则**：
- 每个 `Alloc()` / `Split()` / `InsertChild()` 产出都加入 `allocatedPages`
- CAS 成功后从追踪列表移除（所有权转移）
- 失败时 defer 清理所有剩余 entries
- 与 `handleLeafSplit` (`operations.go:242-244`, `311-317`) 的 cleanup 模式一致

### 10.10 findChildIndex 实现

Cascading loop 需要找到 currentRef 在 grandparent children 中的 index。两种实现：

**方案 A — 从 searchPath 路径信息获取（推荐，O(1)）**：
```go
// handleInternalSplit 从 handleLeafSplit 接收 path 参数
// path[i].Index 记录了每层的 child index
func (b *BTree) handleInternalSplit(path SearchPath, startLevel int, ...) error {
    for level := startLevel; level >= 0; level-- {
        idx := path[level].Index  // 直接从 path 获取
        // ...
    }
}
```
优势：O(1) 查找，无额外开销。

**方案 B — 从 ChildrenCache 线性搜索（fallback）**：
```go
func findChildIndex(parent *PageRef, child *PageRef) (int, error) {
    cache := parent.children.Load()  // ★ *ChildrenCache
    if cache != nil {
        for i, c := range cache.Children {  // ★ 遍历 ChildrenCache.Children
            if c == child {
                return i, nil
            }
        }
    }
    return -1, ErrCASConflict  // 不应发生，触发完整重试
}
```
优势：不依赖 path 参数，适用于 path 不可用的场景。

**推荐**：方案 A。Cascading split 从 handleLeafSplit 触发，path 参数始终可用。

### 10.11 实现优先级

内部节点分裂是**中期优化**（P1），原因：
1. 当前 2 层树支持 ~10000 keys（key 较短时），覆盖单机测试场景
2. 超过此数量前，需要先实现其他优化（WAL 持久化、事务等）
3. Cascading split 的设计已验证（与 leaf split 模式一致），实现时风险低
4. 核心 API（`Node.Split()`、`InsertChild()`、`IsFull()`）已实现，无需新增

### 10.12 并发安全性审核总结

以下场景经代码级验证确认安全：

| 场景 | 安全性 | 机制 |
|------|--------|------|
| 两 writer split 同 parent 不同 children | ✅ | Parent CAS 串行化 |
| Cascading split vs 并发 reader | ✅ | Parent CAS → updated children → reader 走新路径 |
| Cascading split vs 不同 subtree leaf split | ✅ | 不同路径 propagateUpward 互不干扰 |
| 两 cascading split 同时到达 root | ✅ | ReplaceRoot CAS 串行化 |
| SchedulerLock + cascading | ✅ | 只持 leafRef.Lock，parent 操作无锁 CAS |
| IsFull TOCTOU | ✅ | 最坏多一次 unnecessary split |
| Children cache consistency | ✅ | B18/B19 原子替换模式 |
| NewRef dangling | ✅ | 同一 PageRef 在 children cache 被 Retain |
| Redirect CAS 失败 (CAS-R1) | ✅ | 安全性不依赖 Redirect，优化性质 |

---

## 11. 传播模式对比

### 11.1 Split 传播 — Full Retry

**触发条件**：`leaf.IsFull(keyLen, valueLen) == true`

```
handleLeafSplit:
  1. Split leaf → leftPage + rightPage + splitKey
  2. Determine target child (bytes.Compare)
  3. mutate(targetPage) → mutatedPage (double-COW)
  4. Create PageRefs with Retain
  5. Parent InsertChild (COW)
  6. Parent CAS

  CAS 失败 → 释放所有新页面 → 返回 ErrCASConflict → 上层完整重试
  CAS 成功 → Redirect CAS（原子 Tombstone+Redirect+NewRef） → 更新 metrics → 返回 nil
```

**为什么 Split 必须 Full Retry？**
- Split 创建了新页面（left, right, mutated）
- 如果 Parent CAS 失败，这些页面变成**孤儿页面**（无法被访问）
- 必须清理并重试整个操作，否则内存泄漏

### 11.2 普通更新传播 — Best-Effort（★ 当前已禁用）

> **★ propagateUpward 已禁用**：ChildrenCache 改造后，propagateUpward 改 parent pInfo 会导致 ChildrenCache 中的 separators 与新物理页不一致。如果未来恢复，必须同时更新 parent 的 ChildrenCache（含 separator keys）。

**触发条件**：Leaf CAS 成功后（当前代码中此路径已被注释禁用）

```
propagateUpward:
  1. Walk from leaf's parent to root
  2. ★ Bug B4 修复：检查 pInfo.Tombstone
     → Tombstone=true → 停止传播（该路径已分裂）
  3. COW copy parent → ReplaceChild
  4. Parent CAS

  CAS 失败 → FreePage(newNode) → 停止传播 → 返回 nil
  CAS 成功 → 继续向上一层
```

**Bug B4 修复**：propagateUpward 中必须检查 Tombstone。如果某个父节点已被分裂（Tombstone=true），继续传播会导致：
- 从 Tombstoned 节点读取的 PageID 可能已变为错误类型
- ReplaceChild 操作在无效的 NodePage 上执行 → panic 或数据损坏

**为什么普通更新可以 Best-Effort？**
- Leaf-Level CAS 已经成功，数据已持久化
- Parent 更新是**优化**（让下次访问更快），不影响正确性
- 即使失败，下次操作会重新 searchPath，自然找到新位置

### 11.3 性能对比

| 模式 | CAS 失败影响 | 重试范围 | 性能 |
|------|-------------|---------|------|
| Full Retry (Split) | 整个操作 | searchPath + Lock + Split + Parent CAS | 重（~0.625% 概率） |
| Best-Effort (更新) | 仅当前层级 | 停止传播 | 轻（95%+ 的操作） |

---

## 12. 引用计数管理

### 12.1 Split 场景的引用计数

> **★ Review 修正**：PageRef 绑定 double-COW 后的实际 PageID（B6 修复）。
> **★ B18 修正**：直接设置 children 缓存，leftRef/rightRef 生命周期由 parent.children 管理。
> **★ B19 修正**：复用旧 children 中的 PageRef，而非创建新对象。避免同一 pageID 上两个独立 PageRef 导致的 refCount 混淆。
> **★ Redirect+NewRef**：消除 SplitMarker 的独立 Retain/Release 循环，NewRef 作为 PageInfo 字段随 CAS 生命周期管理，简化引用计数。

```
handleLeafSplit 创建（假设 target=left，即 left 被 mutate）：
  leftRef  = NewPageRef(mutation.newPageID, ...) → refCount=0  // ★ B6: 绑定 COW 后的页面
  rightRef = NewPageRef(rightPage.PageID(), ...) → refCount=0  // 未 mutate，绑定原始 split 页面
  FreePage(leftPage.PageID())                    // 回收孤儿页面（被 double-COW 替换）
  leftRef.Retain()  → refCount=1  ← C1 修复：防止过早释放
  rightRef.Retain() → refCount=1

Parent CAS 失败:
  leftRef.Release()   → refCount=0 → freeFunc(mutation.newPageID)   // ★ B7: 仅 Release，无显式 FreePage
  rightRef.Release()  → refCount=0 → freeFunc(rightPage.PageID())
  FreePage(newParent.PageID())  // newParent 无 PageRef，需显式回收
  // 无 double-free ✅

Parent CAS 成功（★ B18+B19+Redirect+NewRef 修正后）:
  Redirect CAS（原子 Tombstone+Redirect+NewRef）
    → leftRef 作为 NewRef 存入 PageInfo，无独立 Retain

  ★ B19: 从旧 ChildrenCache.Children 复用 PageRef，构建新的 ChildrenCache
  oldCache := parentRef.children.Load()
  newChildren.Children[i] = leftRef           // leftRef 被 newChildren.Children 持有
  newChildren.Children[i+1] = rightRef       // rightRef 被 newChildren.Children 持有
  newChildren.Children[其他位置] = oldCache.Children[相应位置]  // ★ B19: 复用现有 PageRef，不创建新对象
  newChildren.Separators = [从 newNodePage 提取并 copy]  // ★ ChildrenCache 改造：含 separators
  parentRef.children.Store(newCache)           // ★ 存储 *ChildrenCache
    → leftRef 被 newChildren 持有，refCount 不变
    → rightRef 同理

  // ★ Redirect+NewRef 优势：无需 ClearSplitMarker
  //   无独立的 Retain/Release 循环，refCount 管理简化

  // ★ 注意：不再 Release leftRef/rightRef！生命周期由 parent.children 管理。
  //   leftRef:  Retain(创建)=1，被 newChildren 持有 → refCount=1
  //   rightRef: Retain(创建)=1，被 newChildren 持有 → refCount=1

  // ★ B19 关键：其他 child 的 refCount 如何变化？
  //   例如：位置 j（j < i 或 j > i+1）的 childRef[j]
  //   复用前：oldChildren[j] 的 refCount = 1（仅被 oldChildren 持有）
  //   复用后：newChildren[j'] = oldChildren[j]（同一 PageRef 对象）
  //           newChildren 被 parent.children.Store 持有 → refCount 不变
  //           oldChildren 被 Store 替换后不再可达，但 refCount 不变（无额外 Release）
  //   结果：childRef[j] 的 refCount 不变，无泄漏，无 UAF ✅

后续:
  searchPath 遍历到 leftRef（通过 cache.Children[i]）→ Retain → refCount=2
  离开路径 → Release → refCount=1
  ...

最终（parent 被 split 或 tree 关闭时）:
  parentRef.children（*ChildrenCache）被重建或替换
    → 对旧 cache.Children 中的每个 childRef 调用 Release
    → leftRef.Release()  → refCount=0 → freeFunc(mutation.newPageID)    // 释放 COW 后的页面 ✅
    → rightRef.Release() → refCount=0 → freeFunc(rightPage.PageID())    // 释放原始 split 页面 ✅
```

### 12.2 关键不变量

1. **PageRef.pageID 不可变**（C1 修复）：Release 使用绑定的 pageID，不读 pInfo
2. **Redirect+NewRef 原子性**：Tombstone+Redirect+NewRef 在同一次 CAS 中设置，不存在窗口期
3. **searchPath Retain/ReleaseAll 配对**：所有路径上的 Ref 都 Retain，使用完 ReleaseAll
4. **ReplaceRoot 先 SetParentRef 后 CAS**（D14 决策）：消除并发读者 parentRef==nil 窗口
5. **★ Review B6**：PageRef 绑定 double-COW 后的实际 PageID，而非原始 split 页面 ID
6. **★ Review B7**：被 PageRef 管理的页面仅通过 Release() 回收，不做显式 FreePage（避免 double-free）
7. **★ Review B18**：Split 后 leftRef/rightRef 的生命周期由 parent.children（`*ChildrenCache`）管理，不在 handleLeafSplit/handleRootSplit 中释放
8. **★ Review B19**：构建 newChildren 时，复用旧 ChildrenCache.Children 中的 PageRef 对象，而非创建新对象。避免对同一 pageID 创建两个独立 PageRef 导致 refCount 混淆和 UAF
9. **★ Review B20**：handleRootSplit 中 children.Store 必须紧接在 ReplaceRoot CAS 之后执行，消除懒初始化窗口期
10. **★ ChildrenCache 原子性**：children 和 separators 在同一 `*ChildrenCache` 对象中原子更新（`children.Store`），读者不会看到 children 与 separators 不一致的状态

---

## 13. 页面分配与回收

### 13.1 Split 的页面分配

| 场景 | 分配页面数 | 说明 |
|------|----------|------|
| Leaf Split | 2 | left + right |
| Leaf Split + mutate (double-COW) | 3 | left + right + mutated target |
| Node Split | 2 | left + right |
| Root Split | 3 | left + right + new root node |
| Parent InsertChild | 1 | COW copy of parent |

### 13.2 失败时的页面回收

> **★ Review 修正 B7**：避免 double-free。PageRef 的 Release() 在 refCount→0 时自动调用 freeFunc(FreePage)。不应再对同一 PageID 显式调用 FreePage。

```
Parent CAS 失败:
  1. leftRef.Release()                   // refCount → 0 → freeFunc(leftRef.pageID)
  2. rightRef.Release()                  // refCount → 0 → freeFunc(rightRef.pageID)
  3. FreePage(newParent.PageID())        // newParent 无 PageRef，需显式回收
  // ✅ 无 double-free：每个 PageID 恰好释放一次

mutate 失败（Step 3）:
  1. FreePage(leftPage.PageID())         // split 页面，无 PageRef
  2. FreePage(rightPage.PageID())        // split 页面，无 PageRef

parentInfo == nil（Step 4）:
  1. leftRef.Release()                   // refCount → 0 → freeFunc
  2. rightRef.Release()                  // refCount → 0 → freeFunc
  // ✅ 无显式 FreePage，仅依赖 Release
```

**原则**：被 PageRef 管理的页面仅通过 Release() 回收；未被 PageRef 管理的页面（newParent、orphans）显式 FreePage。

---

## 14. 实现状态

> **★ P2-2 修正**：区分"底层组件已实现"和"Split 逻辑已设计"。Phase 6 的 Split 逻辑（Tombstone、handleLeafSplit、handleRootSplit）当前仅设计完成，代码待实现。

| 组件 | 文件 | 设计 | 代码 | 备注 |
|------|------|:----:|:----:|------|
| Leaf Split | `leaf_page.go:192-235` | ✅ | ✅ | 底层页面操作 |
| Node Split | `node_page.go:158-205` | ✅ | ✅ | 底层页面操作 |
| InsertChild (中间/末尾) | `node_page.go:111-152` | ✅ | ✅ | 底层页面操作 |
| **★ ChildrenCache 结构体** | **新建** `children_cache.go` | ✅ | ❌ | `ChildrenCache{Children, Separators, Search(key)}` + `copyKey()`；详见 `2026-04-06-children-cache-separators.md` Step 1 |
| Redirect+NewRef (PageInfo 字段) | `page_info.go` §5 | ✅ | ❌ | 需添加 Redirect bool + NewRef *PageRef 字段 |
| PageInfo.Tombstone 字段 | `page_info.go` | ✅ | ❌ | 需添加 Tombstone bool 字段 |
| **★ PageRef.children 类型** | `page_ref.go` §6 | ✅ | ❌ | `atomic.Pointer[[]*PageRef]` → `atomic.Pointer[ChildrenCache]`；`GetOrCreateChildren` 返回 `*ChildrenCache`，懒初始化时提取 separator keys（copy） |
| searchPath Tombstone+Redirect 检查 | `search.go` §6 | ✅ | ❌ | ★ Bug B3 + ChildrenCache：cache 命中时不读 NodePage，用 `cache.Search(key)` 导航 |
| searchPath children 越界保护 | `search.go` §6 | ✅ | ❌ | ★ P1-1：idx >= len(cache.Children) → ErrRetry |
| **★ ReplaceRoot 签名变更** | `root_ref.go:29-45` | ✅ | ❌ | `newChildren []*PageRef` → `newChildren *ChildrenCache`；SetParentRef 遍历 `newChildren.Children` |
| writeOperation IsFull 检查 | `operations.go` §9.2 | ✅ | ❌ | 需在 mutate 前添加 IsFull 分支 |
| handleLeafSplit (CR-08) | `operations.go` §9.3 | ✅ | ❌ | ★ Bug B1/B6/B7/B18/B19 修复 + Redirect CAS + **构建 ChildrenCache（含 separators）** |
| handleRootSplit (CR-08) | `operations.go` §9.4 | ✅ | ❌ | ★ Bug B10/B11/B18/B20 + **ReplaceRoot 传 `&ChildrenCache{Children, Separators}`** + `children.Store(cache)` |
| propagateUpward | `operations.go` §10.2 | ✅ | ❌ | ★ **已禁用**：ChildrenCache 改造后 propagateUpward 改 parent pInfo 导致 separators 不一致。未来恢复需同时更新 ChildrenCache |
| Cascading Split (内部节点分裂) | §10 | ✅ | ❌ | 设计完成，代码待实现（P1 优先级）；**distributeChildrenAfterSplit 需分配 separators** |

---

## 15. 测试覆盖

### 已实现的测试

| 测试文件 | 测试名 | 覆盖场景 |
|---------|--------|---------|
| `leaf_page_test.go` | TestLeafSplit | 基本叶子分裂 |
| `leaf_page_test.go` | TestLeafSplitKeyBoundary | splitKey 边界验证 |
| `leaf_page_test.go` | TestLeafSplitEvenOdd | 奇偶 count 分裂 |
| `node_page_test.go` | TestNodeSplit | 基本节点分裂 |
| `node_page_test.go` | TestNodeSplitChildren | 分裂后子节点正确性 |
| `node_page_test.go` | TestNodeInsertChildMiddle | 中间插入子页面 |
| `node_page_test.go` | TestNodeInsertChildPreservesOtherKeys | 插入后其他 key 保持 |
| `page_ref_test.go` | TestPageRefRedirect | Redirect CAS 设置/读取 |
| `page_ref_test.go` | TestPageRefRedirectNoRedirect | 无 Redirect 时正常处理 |
| `page_ref_test.go` | TestPageRefRedirectNewRef | NewRef 指向正确子页面 |
| `page_ref_test.go` | TestHandleLeafSplit_CASFailure_Cleanup | CAS 失败清理 |
| `page_ref_test.go` | TestHandleRootSplit_ReplaceRoot | 根分裂 ReplaceRoot |

### Phase 6.0 需要的测试

| 测试名 | 目的 |
|--------|------|
| TestSplitPropagation | 叶子分裂传播到父节点 |
| TestRedirectCASOnLeafSplit | Redirect CAS 原子性验证（Tombstone+Redirect+NewRef 同时生效） |
| TestRedirectReaderReNavigates | 并发读者通过 Redirect 重新导航到正确子页面 |
| TestRootSplit | 根分裂后树高度 +1 |
| TestMultiLevelSplit | 3+ 层级联分裂（§10 设计验证） |
| TestConcurrentSplit | 并发写入触发分裂，无 data race |
| TestSplitNoOrphanPages | 分裂后无孤立页面 |
| TestSplitDuringConcurrentRead | 分裂中并发 reader 看到一致数据 |
| TestFullLifecycle | 大量 Set → 验证 → 大量 Delete → 无泄漏 |

---

## 16. 参考资料

- **Interface 设计**: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-interface.md`
- **Implementation 指南**: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-implement.md`
- **Lealone 参考**: CCOW (Concurrent Compare-And-Swap + Copy-on-Write) 架构
- **设计决策**: interface.md §13 (D1-D15), implement.md §6.0.3 (C1-C6, D1)

---

**文档创建**: 2026-04-04
**最后更新**: 2026-04-06
**状态**: v3.0 — ★ ChildrenCache 改造标注：`children` 字段从 `[]*PageRef` 改为 `*ChildrenCache{Children, Separators}`，searchPath 用 `cache.Search(key)` 导航，ReplaceRoot 签名变更，propagateUpward 禁用。详见 `2026-04-06-children-cache-separators.md`

---

## 附录 A: 调试发现的 Bug 勘误（2026-04-04）

> 以下 Bug 在 Phase 6.0 实现的首次集成测试中发现。原文档 v1.0 中的设计未考虑这些问题。

### Bug B1: InsertChild 子页面 ID 错误（SIGSEGV / 数据丢失）

**严重性**: P0（SIGSEGV + 数据丢失）
**发现场景**: `TestSplitFillsPage` 单线程测试

**现象**：
- `searchPath` 访问子页面时 SIGSEGV（访问已释放页面）
- `TestSplitLargeDataset` 丢失 145 个 keys（child[2] 只有 1 个 entry）

**根因**：
`handleLeafSplit` 调用 `InsertChild` 时，直接使用 `leftPage.PageID()` 和 `rightPage.PageID()` 作为子页面 ID。但 CR-08 的 double-COW 流程中，被 mutate 的那一侧已经被 COW 替换为新页面（`mutation.newPageID`），原始 split 页面变成了孤儿页面（可能被回收）。

```
错误代码：
  InsertChild(idx, splitKey, leftPage.PageID(), rightPage.PageID())
                                       ↑ 指向可能已释放的页面！

正确代码：
  if target == left:
    InsertChild(idx, splitKey, mutation.newPageID, rightPage.PageID())
                                  ↑ COW 后的页面    ↑ 未 mutate，安全
  if target == right:
    InsertChild(idx, splitKey, leftPage.PageID(), mutation.newPageID)
                                  ↑ 未 mutate，安全  ↑ COW 后的页面
```

**修复位置**: §9.3 handleLeafSplit Step 5

### Bug B2: SetSplitMarker 与 Tombstone 顺序错误（并发 Reader 丢失）
> **★ 已在 Redirect+NewRef 重构中消除**：新设计中 Tombstone+Redirect+NewRef 在同一次 CAS 中原子设置，不存在顺序问题。

**严重性**: P1（并发场景下 reader 可能看不到数据）
**发现场景**: 并发 `TestSplitLargeDataset`，"page is not a leaf page" 错误

**现象**：
- 并发 reader 在 Tombstone 窗口期无法导航到正确的子页面
- `searchPath` 遍历到 Tombstoned 的 Ref → 尝试 IsLeaf(pInfo.PageID) → PageID 已被复用为 NodePage → 类型判断错误

**根因**：
原设计先设置 Tombstone（CAS pInfo），再设置 SplitMarker。窗口期内 reader 看到 Tombstone=true 但 splitMarker=nil，无法 FollowSplit，也无法读取旧数据。

**修复**: §5.6.1 Tombstone 严格顺序约束 + §9.3 Step 7-8（旧设计）→ §5 Redirect+NewRef 原子 CAS（新设计，已消除此问题）

### Bug B3: searchPath 缺少 Tombstone 检查（类型判断错误）

**严重性**: P1（并发场景下 panic）
**发现场景**: 与 Bug B2 同时发现

**现象**：
- `searchPath` 遍历到 Tombstoned PageRef → 直接调用 `IsLeaf(pInfo.PageID)`
- Tombstoned 页面的 PageID 可能已被复用为不同类型 → IsLeaf 返回错误值
- 后续 GetLeafPage/GetNodePage 类型断言失败

**根因**：
searchPath 在 GetPageInfo() 后直接检查 IsLeaf，跳过了 Tombstone 检查。Tombstoned 页面应该先 FollowSplit，而非直接使用其 PageID。

**修复**: §6 searchPath Tombstone 检查（IsLeaf 之前）

### Bug B4: propagateUpward 缺少 Tombstone 检查（传播到已分裂页面）

**严重性**: P2（Best-Effort 传播场景）
**发现场景**: 并发 split 时 propagateUpward 继续传播到已 Tombstoned 的父节点

**现象**：
- propagateUpward 遍历父节点链，遇到已 Tombstoned 的节点
- 继续对该节点做 GetNodePage + ReplaceChild → 操作在无效页面上执行

**根因**：
propagateUpward 没有检查 pInfo.Tombstone。Best-Effort 传播应该在遇到 Tombstone 时停止。

**修复**: §10.2 propagateUpward Tombstone 检查

### Bug B5: GetOrCreateChildren 返回 Stale 子引用（窗口期）

**严重性**: P2（并发 reader 短暂不一致）
**发现场景**: 并发 reader 在 parent CAS 后、InvalidateChildren 前获取 children

**现象**：
- Parent CAS 成功后，children 缓存仍指向旧的子页面引用
- 并发 reader 获取 stale children → 遍历到错误的子页面

**根因**：
`GetOrCreateChildren` 使用 CAS 保护的懒初始化缓存。Parent InsertChild CAS 成功后，旧的 children 缓存仍有效，直到 `InvalidateChildren()` 被调用。窗口期：

```
Parent CAS 成功 ──── InvalidateChildren() ────
                  ↑ 窗口期：并发 reader 获取 stale children ↑
```

**缓解措施**：
1. InvalidateChildren 必须紧接在 Parent CAS 成功后调用（最小化窗口）
2. Stale children 的 Redirect 仍能帮助 reader 导航到正确页面
3. 最坏情况：reader 需要重试一次 searchPath

---

### Review 修正（2026-04-04 第二轮）

> 以下问题在文档 v1.1 的 code review 中发现，已在 v1.2 中修正。

### Bug B6: PageRef 绑定错误 PageID（P0）

**严重性**: P0（数据不可见 + 孤儿页面泄漏）
**发现**: Code Review Agent #2 + #3 同时发现

**现象**：
- 并发 reader 通过 Redirect 重新导航到达 leftRef（pageID=leftPage.PageID()）
- 但父节点 InsertChild 的 left child 指向 mutation.newPageID（double-COW 后的页面）
- Reader 看到**不含 CR-08 立即插入数据**的旧页面
- 且 leftPage.PageID() 作为孤儿页面永远不被回收（无人引用但也不被释放）

**根因**：
§9.3 Step 4 创建 `leftRef = NewPageRef(leftPage.PageID(), ...)`，但 CR-08 的 double-COW 已将 target 侧替换为 `mutation.newPageID`。Redirect NewRef 和父节点指向不同的物理页面。

**修复**（已在 §9.3 Step 3-4 修正）：
- mutated 侧的 PageRef 用 `mutation.newPageID` 创建
- 未 mutated 侧用原始 split 页面 ID
- 被替换的原始 split 页面显式 `FreePage(orphanPageID)` 回收

### Bug B7: CAS 失败路径 Double-Free（P0）

**严重性**: P0（页面分配器数据损坏）
**发现**: Code Review Agent #3

**现象**：
- CAS 失败路径先调用 `leftRef.Release()` → refCount→0 → `freeFunc` → `FreePage(leftPage.PageID())`
- 然后又显式调用 `b.storage.FreePage(leftPage.PageID())`
- 同一 PageID 被释放两次 → 分配器将该 PageID 分配给其他数据后又被错误回收

**根因**：
`freeFunc` = `storage.FreePage`（在 NewBTree 中绑定的）。Release() 在 refCount==0 时自动调用 freeFunc。显式 FreePage 与 freeFunc 对同一 PageID 构成 double-free。

**修复**（已在 §9.3 Step 6 修正）：
- CAS 失败路径仅调用 `Release()`，不做显式 `FreePage` 对应 PageRef 管理的页面
- 仅对无 PageRef 的页面（如 newParent）显式 FreePage

### Bug B8: writeOperation 忽略 searchPath 错误（P0）

**严重性**: P0（ErrRetry 丢失 → nil pointer panic）
**发现**: Code Review Agent #2

**现象**：
- `path, _ := searchPath(...)` 忽略了 ErrRetry 错误
- searchPath 在 Tombstone 窗口期返回 `nil, ErrRetry`
- 后续 `path.Leaf()` 对 nil path 访问 → panic

**修复**（已在 §9.2 修正）：
```go
path, err := searchPath(b.storage, b.rootRef, key)
if err != nil {
    if errors.Is(err, ErrRetry) {
        continue  // Tombstone 窗口，立即重试
    }
    return errpkg.BTreeWriteOpSearch(err)
}
```

### Bug B9: Root Split 缺少 Tombstone CAS（P2）
> **★ 已在 Redirect+NewRef 重构中消除**：Root Split 不需要 Redirect（B11 修复），ReplaceRoot CAS 原子替换 pInfo。

### Review 修正（2026-04-04 第三轮）

> 以下问题在文档 v1.2 的第二轮 code review 中发现，已在 v1.3 中修正。

### Bug B10: §9.4 handleRootSplit CAS 失败路径 double-free 复现（P0）

**严重性**: P0（页面分配器数据损坏）
**发现**: Code Review Agent #1

**现象**：
- §9.4 Step 7 CAS 失败路径先 `leftRef.Release()` → freeFunc(leftChildID) + `rightRef.Release()` → freeFunc(rightChildID)
- 然后又显式调用 `FreePage(mutation.newPageID)` + `FreePage(leftPage.PageID())` + `FreePage(rightPage.PageID())`
- 当 target=left 时，leftChildID=mutation.newPageID → 同一 PageID 被 freeFunc 和显式 FreePage 各释放一次 → double-free

**根因**：
§9.3 已通过 B7 修复解决 double-free（"被 PageRef 管理的页面仅通过 Release() 回收"），但 §9.4 未同步修复，仍保留旧的 "Release + 显式 FreePage" 模式。

**修复**（已在 §9.4 Step 7 修正）：
- 添加 orphanPageID 追踪（与 §9.3 Step 3-4 同理）
- CAS 失败路径：仅 Release + 显式 FreePage 不被 PageRef 管理的孤儿页面（orphanPageID, newRootID, newRootPage.PageID()）

### Bug B11: §9.4 Tombstone CAS 死代码（P1）

**严重性**: P1（死代码 + 变量未定义）
**发现**: Code Review Agent #2

**现象**：
1. `rootRef` 变量未在函数参数中定义（参数是 `leafRef *PageRef`）
2. ReplaceRoot 已将 rootRef.pInfo CAS 为 newRootInfo，后续 `CAS(rootInfo, tombstonedInfo)` 使用旧 rootInfo → 100% 失败
3. Root split 语义上不需要 Tombstone：ReplaceRoot 已原子替换 rootRef 的 pInfo，rootRef 被复用为新 internal root

**修复**（已在 §9.4 Step 8-9 修正）：
- 移除 Tombstone CAS 死代码
- 添加注释说明 root split 不需要 Tombstone 的理由

### Bug B12: §9.3 parentInfo==nil 路径引用未创建变量（P2）

**严重性**: P2（文档伪代码错误）
**发现**: Code Review Agent #1

**现象**：
§9.3 Step 4 parentInfo==nil 路径中 `b.storage.FreePage(newParent.PageID())` 引用了在 Step 5 才创建的 `newParent`。

**修复**（已在 §9.3 Step 4 修正）：
- 删除无效的 FreePage 调用，仅保留 Release

### Bug B13: §8.2/§8.3 时序图与 §9 伪代码不一致（P2）

**严重性**: P2（文档不一致）
**发现**: Code Review Agent #3

**现象**：
1. §8.2 Step 7 未反映 B6 修复（缺少孤儿页面回收）
2. §8.2 CAS 失败路径展示旧的 double-free 模式（Release + 显式 FreePage）
3. §8.3 Root Split 缺少 Tombstone 决策说明

**修复**（已在 §8.2/§8.3 修正）：
- §8.2 Step 7：标注 double-COW PageRef + 孤儿回收
- §8.2 CAS 失败路径：改为仅 Release + FreePage(newParent)
- §8.3：标注 Root Split 不需要 Tombstone 的理由

### Bug B14: FollowSplit 与 ClearSplitMarker Use-After-Free（P0）
> **★ 已在 Redirect+NewRef 重构中消除**：新设计无 FollowSplit/ClearSplitMarker，Redirect 后从 children 重新导航。

**严重性**: P0（数据损坏 / Use-After-Free）
**发现**: Concurrency Review Agent（第三轮）

**现象**：
- FollowSplit 返回 `marker.Left` 指针但不 Retain
- searchPath 在获取指针后才调用 Retain
- ClearSplitMarker 可能在此窗口期 Release 到 refCount=0 → freeFunc → 页面回收
- 后续 Retain → 对已回收页面操作 → 数据损坏

**修复**（已在 §5.5 修正）：
- FollowSplit 内部 Retain 后再返回
- searchPath 相应调整：FollowSplit 返回后直接使用（已 Retain），不再额外 Retain

### Bug B15: ClearSplitMarker 缺少调用时机 → 内存泄漏（P0）
> **★ 已在 Redirect+NewRef 重构中消除**：新设计无 SplitMarker，NewRef 作为 PageInfo 字段随 CAS 生命周期管理。

**严重性**: P0（leftRef/rightRef 永不回收 → 内存泄漏）
**发现**: Page Lifecycle Review Agent（第三轮）

**现象**：
- §5.4 定义了 ClearSplitMarker 但全文无调用点
- SplitMarker 的 Retain 永远不被 Release
- leftRef/rightRef refCount 永远 ≥ 1 → 页面永不回收

**修复**（已在 §5.4.1 和 §9.3 Step 9 修正）：
- §5.4.1：明确调用时机 = InvalidateChildren() 内部
- §9.3 Step 9：在 InvalidateChildren 后调用 leafRef.ClearSplitMarker()
- 安全性：P0-1 修复后 FollowSplit 内部 Retain，ClearSplitMarker 不会影响正在途中的 reader

### Bug B16: GetOrCreateChildren stale 缓存 idx 越界（P1）

**严重性**: P1（并发场景下 panic）
**发现**: Concurrency Review Agent（第三轮）

**现象**：
- Parent CAS 成功后 pInfo 指向新 parent（更多 children）
- 但 children 缓存仍为旧数组（长度不足）
- searchPath 基于新 pInfo 的 Search 结果 idx 可能超出旧 children 长度
- `children[idx]` → index out of range panic

**修复**（已在 §6 searchPath 修正）：
- 添加 `if idx >= len(children) || children[idx] == nil` 越界检查
- 越界时返回 ErrRetry，由上层重试 searchPath

### Bug B17: §5.6.1 未引用 Go atomic 语义保证（P1）

**严重性**: P1（设计推理不完整）
**发现**: Atomic Ordering Review Agent（第三轮）

**现象**：
- §5.6.1 声称"严格顺序约束"但未解释为何跨 atomic 变量操作能保证顺序
- 可能误导实现者认为需要额外内存屏障

**修复**（已在 §5.6.1 修正）：
- 添加 Go 1.19+ sync/atomic sequential consistency 保证说明
- 明确不需要额外内存屏障

### Review 修正（2026-04-04 第五轮）

> 以下问题在文档 v1.5 的第四轮 code review 中发现，已在 v1.6 中修正。

### Bug B18: handleLeafSplit/handleRootSplit children 缓存重建导致 refCount→0 → 页面回收（P0）
> **★ Redirect+NewRef 简化**：新设计无 ClearSplitMarker，但仍需直接构建 children 数组（B18 修复保留）。
> refCount 管理更简单：leftRef/rightRef 仅被 children 持有（refCount=1），无 SplitMarker 的额外 Retain/Release。

**严重性**: P0（UAF — 页面被错误回收后仍被引用）
**发现**: Concurrency + Reference Counting Review Agent（第四轮）

**现象**：
- §9.3 Step 9 使用 `InvalidateChildren()` + `GetOrCreateChildren()` 重建 children 缓存
- `GetOrCreateChildren()` 为每个 child 创建**全新的 PageRef 对象**（refCount=0），与 leftRef/rightRef 是独立对象
- Step 10 调用 `leftRef.Release()` / `rightRef.Release()` → refCount 1→0 → `freeFunc` 回收页面
- 但 parent 的 children 缓存中的**新 PageRef 对象**指向同一 PageID → UAF（访问已回收页面）

**根因**：
PageRef 是 per-object 引用计数，不是 per-PageID 引用计数。两个独立的 PageRef 对象即使 pageID 相同，refCount 也各自独立。`GetOrCreateChildren` 创建的新对象不了解 leftRef/rightRef 的存在，二者 refCount 互不影响。

**修复**（已在 §9.3 Step 9-10 和 §9.4 Step 9-11 修正）：
- 方案 A：直接构建 children 数组，将 leftRef/rightRef 放入 children[i] 和 children[i+1]
- leftRef/rightRef 被 children 持有 → refCount 保持 ≥ 1 → 页面不被回收
- ClearSplitMarker 在 children.Store 之后调用，此时 refCount 从 2 降到 1（仍 ≥ 1）
- 不再在 handleLeafSplit/handleRootSplit 中 Release leftRef/rightRef
- 生命周期由 parent.children 管理：parent 被 split 或 tree 关闭时 InvalidateChildren 释放旧 children

### Bug B19: §9.3 Step 9 创建新的 PageRef 对象导致 refCount 混淆 → UAF（P0）

**严重性**: P0（UAF — 对同一 pageID 创建两个独立 PageRef，替换时 pageID 被提前回收）
**发现**: Reference Counting Review Agent（第四轮）

**现象**：
- §9.3 Step 9（应用 B18 修复后）为非 left/right 的其他 child 创建**全新的 PageRef 对象**：
  ```go
  ref := NewPageRef(childID, 0, parentRef, parentRef.freeFunc)
  ref.Retain()
  newChildren[i] = ref
  ```
- 同一 pageID 上存在两个独立 PageRef 对象：旧 children 中的 `oldChildren[srcIdx]` 和新 children 中的 `newChildren[i]`
- 当 `children.Store(newChildren)` 替换旧数组后，旧 children 的 `oldChildren[srcIdx]` 不再被任何可达路径引用
- 如果 `oldChildren[srcIdx].refCount == 1`（仅被旧 children 持有），它变成孤儿 → 但物理 pageID 仍被 `newChildren[i]` 引用
- allocator 可能立刻回收该 pageID 并分配给其他请求 → `newChildren[i]` 访问已回收页面 → UAF

**根因**：
为未 split 的 child 创建新的 PageRef 对象，与旧 children 中的 PageRef 重复引用同一 pageID。旧 children 被替换时，旧的 PageRef 如果 refCount=1 会被 Release → freeFunc → pageID 可能被 allocator 立即回收。

**修复**（已在 §9.3 Step 9 修正）：
- **B19 修复**：从旧 children 数组中**直接复用**现有 PageRef 对象，不创建新对象
- `oldChildren := parentRef.children.Load()` → 直接引用 `oldChildren[srcIdx]` 作为 `newChildren[i]`
- 同一 PageRef 对象不会被重复创建，refCount 自然正确，无 pageID 被提前回收的问题

### Bug B20: §9.4 handleRootSplit children.Store 时机 → 懒初始化 UAF（P0）
> **★ Redirect+NewRef 简化**：新设计中 children.Store 紧接 ReplaceRoot CAS，无 SetSplitMarker/ClearSplitMarker 干扰。

**严重性**: P0（UAF — ReplaceRoot CAS 成功后、children.Store 前，reader 通过懒初始化创建独立 PageRef → Release → UAF）
**发现**: Concurrent Reader Visibility Review Agent（第四轮）

**现象**：
- §9.4 handleRootSplit 的执行顺序（修复 B18 后）：
  1. `ReplaceRoot` CAS 成功 → `rootRef.pInfo` 指向新的 internal root
  2. `SetSplitMarker(leftRef, rightRef, splitKey)` ← **在此窗口期**
  3. `rootRef.children.Store([leftRef, rightRef])`
- 在步骤 2-3 之间，并发 reader 调用 `GetOrCreateChildren`：
  - `children` 为 nil → 触发懒初始化
  - 从 `rootRef.pInfo` 的 page data（新的 internal root）读取 leftChildID/rightChildID
  - 为每个 childID 创建**独立的 PageRef 对象**（refCount=0 → Retain → refCount=1）
  - reader 通过这些独立 PageRef 读取 leaf 数据
- 步骤 3 `children.Store` 覆盖了懒初始化的结果
- reader 最终 Release 这些独立 PageRef → refCount → 0 → `freeFunc(pageID)` → **物理页面被回收**
- 但 `handleRootSplit` 的 `leftRef/rightRef` 仍引用同一 pageID → 后续访问 → UAF

**根因**：
`children.Store` 在 `SetSplitMarker` 之后执行，导致 ReplaceRoot CAS 成功后、children.Store 前存在一个窗口期。在此窗口期内，`GetOrCreateChildren` 的懒初始化会为同一 pageID 创建独立的 PageRef 对象，与 `leftRef/rightRef` 竞争。当 reader 释放独立 PageRef 时，物理页面被回收，但 `leftRef/rightRef` 仍引用该 pageID。

**修复**（已在 §9.4 Step 8-9 修正）：
- **B20 修复**：将 `children.Store` 移到 `SetSplitMarker` **之前**
- 调整后的顺序：
  1. `ReplaceRoot` CAS 成功
  2. `rootRef.children.Store([leftRef, rightRef])` ← 先设置 children
  3. `SetSplitMarker(leftRef, rightRef, splitKey)` ← 后设置 marker
  4. `ClearSplitMarker`
- 现在 `GetOrCreateChildren` 在 CAS 后看到非 nil children，直接返回，不会触发懒初始化，消除了窗口期

### Review 修正（2026-04-04 第六轮）

> 以下问题在文档 v1.6 的第五轮 code review 中发现，已在 v1.7 中修正。

### Bug B21: §11.1 缺少"其他 child"的 refCount 追踪说明（P1）

**严重性**: P1（文档不完整，可能导致实现者误解）
**发现**: Reference Counting Review Agent（第六轮）

**现象**：
- §11.1 详细追踪了 leftRef/rightRef 的 refCount 变化
- 但未说明"其他 child"（未 split 的原有子页面）的 refCount 如何变化
- 实现者可能误以为需要为这些 child 创建新的 PageRef 对象

**根因**：
B19 修复引入的"复用旧 children 中的 PageRef"机制需要明确说明其 refCount 影响。

**修复**（已在 §11.1 修正）：
- 添加注释说明其他 child 的 refCount 追踪：
  ```
  // ★ B19 关键：其他 child 的 refCount 如何变化？
  //   例如：位置 j（j < i 或 j > i+1）的 childRef[j]
  //   复用前：oldChildren[j] 的 refCount = 1（仅被 oldChildren 持有）
  //   复用后：newChildren[j'] = oldChildren[j]（同一 PageRef 对象）
  //           newChildren 被 parent.children.Store 持有 → refCount 不变
  //           oldChildren 被 Store 替换后不再可达，但 refCount 不变（无额外 Release）
  //   结果：childRef[j] 的 refCount 不变，无泄漏，无 UAF ✅
  ```

### Bug B22: 并发 Split 竞争场景未在文档中说明（P1）

**严重性**: P1（缺少关键并发场景文档）
**发现**: Reference Counting + Concurrency Review Agent（第六轮）

**现象**：
- 文档缺少两个 Writer 同时分裂同一 Parent 的不同子页面的场景说明
- 该场景涉及 Parent CAS 竞争、失败方清理、无死锁保证等关键设计点

**修复**（已在 §8.5 添加）：
- 添加并发 Split 竞争时序图（§8.5）
- 展示 Writer 1 和 Writer 2 同时分裂 Child1 和 Child2 的完整流程
- 明确 Parent CAS 的串行化作用、失败方清理责任、无死锁保证

### Bug B23: §8.5 W2 重试时 idx 计算逻辑不明确（P1）

**严重性**: P1（文档不完整，可能导致实现错误）
**发现**: Concurrent Reader Visibility Review Agent（第六轮）

**现象**：
- §8.5 时序图中 W2 CAS 失败后标注 "goto Step 1 (完整重试)"
- 但未明确说明 W2 重试时是否需要重新 searchPath 以获取最新的 parentInfo 和重新计算 idx
- 如果 W2 直接使用 CAS 失败前缓存的旧 idx，会导致插入位置错误（覆盖已更新的 children）

**根因**：
W2 在 CAS 失败前的 parentInfo 已过期，children 数组结构也已改变（插入了 W1 的 left1Ref/right1Ref）。直接使用旧 idx 会导致错误。

**修复**（已在 §8.5 关键点中修正）：
- 明确说明 "W2 重试时必须重新执行 searchPath 获取最新的 parentInfo，基于新的 pInfo 重新 Search(key) 计算正确的 idx"
- 不能直接使用 CAS 失败前缓存的旧 idx
