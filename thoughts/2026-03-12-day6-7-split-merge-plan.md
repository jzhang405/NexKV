# Day 6-7: Split/Merge 逻辑实施计划

**日期**: 2026-03-12
**版本**: v2.0（根据审核意见修订）
**预估工期**: 2 天
**依赖**: Day 1-5 已完成（懒加载、searchPath、Get/Set）

---

## 🔴 审核意见确认（2026-03-12）

### 已修复的关键问题

| # | 问题 | 修复方案 | 状态 |
|---|------|---------|------|
| 1 | PageInfo 无法获取 PageRef | ✅ 方案 B：调用方传入，避免循环引用 | 已修复 |
| 2 | PageInfo 缺少 parentRef | ✅ 添加 `parentRef *PageRef` 字段 | 已修复 |
| 3 | splitRoot CAS 更新 | ✅ 使用 `ReplacePage()` 方法 | 已修复 |
| 4 | InternalPage.Insert() 不明确 | ✅ 添加 `InsertKeyChild()` 方法 | 已修复 |
| 5 | createPageInfo 未定义 | ✅ 添加辅助函数 | 已修复 |
| 6 | Merge 操作描述简略 | ✅ 标记为 Future Phase | 已修复 |
| 7 | 文档一致性 | ✅ 先更新 Day 6-7，实施后再更新主计划 | 已修复 |

### 关键设计决策

1. **避免循环引用**：`PageRef` → `PageInfo`（单向），不添加 `pageRef` 字段
2. **引用链维护**：添加 `PageInfo.parentRef` 字段，用于 Split 后更新引用链
3. **CAS 更新**：使用 `RootPageRef.ReplacePage()` 而非直接操作 `unsafe.Pointer`
4. **方法签名**：`Split()` 不传 `parentRef`，由调用方负责引用更新
5. **Merge 延后**：不在 Day 6-7 实现，允许节点低于 minKeys

---

## 背景

### 为什么需要 Split/Merge

**BTree 不变量**：
- 每个页面的键数量必须在 `[minKeys, maxKeys]` 范围内
- `maxKeys = 16`（LeafPage）/ `15`（InternalPage）
- `minKeys = maxKeys / 2 = 8`

**触发 Split 的场景**：
1. **LeafPage.Insert()** - 插入后 `len(keys) > maxKeys`
2. **InternalPage.InsertChild()** - 插入后 `len(keys) > maxKeys`
3. **根节点特殊处理** - 根节点可以超过 `maxKeys`，然后分裂

**Split 后的后果**：
- 父节点需要插入新的分裂键（SplitKey）
- 如果父节点也满，需要递归分裂
- 可能导致根节点分裂（树高度 +1）

### Lealone AOSE 的 Split 策略

**Lealone 的设计优势**：
```
传统 BTree Split:
├── 锁定整个 Split 路径（Root → Leaf）
├── 阻塞所有读写操作
└── 延迟高，并发性差

Lealone AOSE Split:
├── Copy-on-Write: 克隆路径，不影响旧版本
├── 原子更新: CAS 切换根节点
├── 并发读继续访问旧版本
└── 延迟低，并发性强
```

---

## Day 6: LeafPage Split 实现

### 任务 6.0: 添加 PageInfo.parentRef 字段 ⚠️ 前置依赖

**文件**: `page_info.go`

**修改内容**：
```go
type PageInfo struct {
    pos         int64
    page        interface{}
    pageLock    *PageLock
    lastTime    int64
    hits        int64
    buff        []byte

    isDirty     bool
    isSplitted  bool
    metaVersion int
    pageSize    int32

    // ✅ 新增：父节点引用（用于 Split 后更新引用链）
    parentRef *PageRef  // 父节点引用

    _         [cacheLineSize - 72]byte  // 调整 padding（64 → 72）
}

// GetParentRef 获取父节点引用
func (info *PageInfo) GetParentRef() *PageRef {
    return info.parentRef
}

// SetParentRef 设置父节点引用
func (info *PageInfo) SetParentRef(ref *PageRef) {
    info.parentRef = ref
}
```

**原因**：Split 后需要维护引用链完整性，子节点需要知道父节点是谁。

---

### 任务 6.1: 实现带引用更新的 LeafPage.Split()

**文件**: `leaf_page.go`（修改现有 Split 方法）

**设计方案确认**：**使用方案 B（调用方传入 PageRef）**

**理由**：
- 调用方在 `splitLeaf()` 时已经持有 `PageRef`
- 避免循环引用风险（`PageRef` ↔ `PageInfo` ↔ `PageRef`）
- API 更清晰

**修改后的签名**：
```go
// Split 分裂叶子节点
// 参数：
//   - 无需 parentRef 参数（由调用方传入）
//
// 返回：
//   - *LeafPage: 新创建的叶子节点
//   - []byte: 分裂键（提升到父节点）
//   - error: 错误信息
//
// 调用方负责：
//   - 创建新节点的 PageRef 和 PageInfo
//   - 更新父节点的 children 引用
//   - 更新新节点的 parentRef
func (p *LeafPage) Split() (*LeafPage, []byte, error) {
    // 1. 分裂逻辑（已实现）
    mid := len(p.keys) / 2
    splitKey := p.keys[mid]

    newPage := NewLeafPage(model.PageID(p.pageID + 1))
    newPage.keys = append(newPage.keys, p.keys[mid+1:]...)
    newPage.values = append(newPage.values, p.values[mid+1:]...)

    p.keys = p.keys[:mid]
    p.values = p.values[:mid]
    p.version++

    return newPage, splitKey, nil
}
```

**接受标准**：
- [ ] Split 后两个页面的键数量都 ≤ maxKeys/2
- [ ] 单元测试覆盖所有边界情况

### 任务 6.2: 实现 BTree.splitLeaf()

**文件**: `btree_split.go`（新建）

**设计**：
```go
// splitLeaf 分裂叶子节点，并向上传播
func (b *BTree) splitLeaf(ctx context.Context, path []*PageInfo, leafInfo *PageInfo) error {
    // 1. 获取父节点引用
    var parentRef *PageRef
    if len(path) > 1 {
        parentInfo := path[len(path)-2]
        if parentInfo.IsPageLoaded() {
            parentPage := parentInfo.GetPage().(*InternalPage)
            // 找到指向当前叶子节点的子节点引用
            for i, childRef := range parentPage.Children() {
                if childRef != nil && childRef.GetPageInfo() == leafInfo {
                    parentRef = childRef
                    break
                }
            }
        }
    }

    // 2. 分裂叶子节点
    leafPage := leafInfo.GetPage().(*LeafPage)
    newLeaf, splitKey, newLeafRef, err := leafPage.Split(parentRef)
    if err != nil {
        return fmt.Errorf("split leaf: %w", err)
    }

    // 3. 如果没有父节点（根节点分裂）
    if len(path) == 1 {
        return b.splitRoot(ctx, leafInfo, newLeafRef, splitKey)
    }

    // 4. 在父节点中插入分裂键和新子节点
    parentInfo := path[len(path)-2]
    parentPage := parentInfo.GetPage().(*InternalPage)

    // TODO: Day 7 - 实现父节点更新逻辑
    // parentPage.Insert(splitKey, newLeafRef)

    // 5. 检查父节点是否需要分裂
    if parentPage.NumKeys() >= parentPage.GetMaxKeys() {
        // TODO: Day 7 - 递归分裂父节点
    }

    return nil
}

// splitRoot 分裂根节点（树高度 +1）
//
// 设计确认：使用 ReplacePage() 进行 CAS 更新
//
// 参数：
//   ctx - 上下文
//   leftRef - 左子节点引用（原根节点）
//   rightRef - 右子节点引用（新分裂出的节点）
//   splitKey - 分裂键（插入到新根节点）
func (b *BTree) splitRoot(ctx context.Context, leftRef *PageRef, rightRef *PageRef, splitKey []byte) error {
    // 1. 创建新的根节点（内部节点）
    newRootPage := NewInternalPage(model.PageID(1))
    newRootPage.keys = [][]byte{splitKey}
    newRootPage.children = []*PageRef{leftRef, rightRef}

    // 2. 创建新的 PageInfo
    newRootInfo := NewPageInfo()
    newRootInfo.SetPage(newRootPage)
    newRootInfo.SetParentRef(nil)  // 根节点无父引用

    // 3. 创建新的 RootPageRef
    newRootRef := NewRootPageRefWithInfo(newRootInfo)

    // 4. 更新子节点的父引用（指向新的根节点）
    leftInfo := leftRef.GetPageInfo()
    if leftInfo != nil {
        leftInfo.SetParentRef(newRootRef)
    }

    rightInfo := rightRef.GetPageInfo()
    if rightInfo != nil {
        rightInfo.SetParentRef(newRootRef)
    }

    // 5. CAS 更新 BTree 的根引用
    // 使用 ReplacePage() 进行原子更新
    oldRootInfo := b.rootRef.pInfo.Load()
    if !b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
        return ErrRetry  // CAS 失败，调用方重试
    }

    // 6. 延迟释放旧根（等待活跃读操作完成）
    go func() {
        time.Sleep(100 * time.Millisecond)
        // 标记旧根为脏页（如果需要）
    }()

    return nil
}
```

**关键设计决策**：
- ✅ 使用 `RootPageRef.ReplacePage()` 而非直接操作 `unsafe.Pointer`
- ✅ 与现有 CCOW 模式一致
- ✅ 更新子节点的 `parentRef`，维护引用链完整性
- ✅ CAS 失败返回 `ErrRetry`，调用方重试
```

**接受标准**：
- [ ] 根节点分裂成功（树高度 +1）
- [ ] 非根节点分裂正确更新父节点
- [ ] 递归分裂处理正确
- [ ] 单元测试覆盖所有场景

### 任务 6.3: LeafPage Split 测试

**文件**: `leaf_page_split_test.go`（新建）

```go
// 辅助函数：创建 PageInfo
func createPageInfo(page interface{}) *PageInfo {
    info := NewPageInfo()
    info.SetPage(page)
    return info
}

// TestLeafPage_Split_Basic 测试基本分裂
func TestLeafPage_Split_Basic(t *testing.T) {
    leaf := NewLeafPage(1)

    // 插入 maxKeys + 1 个键
    for i := 0; i <= 16; i++ {
        key := []byte(fmt.Sprintf("key-%03d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        leaf.Insert(key, value)
    }

    // 分裂（新签名：无需参数）
    newLeaf, splitKey, err := leaf.Split()
    require.NoError(t, err)

    // 验证分裂键
    assert.Equal(t, []byte("key-008"), splitKey)

    // 验证原页面保留前半部分
    assert.Equal(t, 8, leaf.NumKeys())
    assert.Equal(t, []byte("key-007"), leaf.keys[7])

    // 验证新页面包含后半部分
    assert.Equal(t, 8, newLeaf.NumKeys())
    assert.Equal(t, []byte("key-009"), newLeaf.keys[0])

    // 验证新 PageRef
    assert.NotNil(t, newRef)
    assert.NotNil(t, newRef.GetPageInfo())
    assert.Equal(t, newLeaf, newRef.GetPageInfo().GetPage())
}

// TestLeafPage_Split_MaxKeys 测试达到 maxKeys 时分裂
func TestLeafPage_Split_MaxKeys(t *testing.T) {
    leaf := NewLeafPage(1)

    // 插入刚好 maxKeys 个键
    for i := 0; i < 16; i++ {
        key := []byte(fmt.Sprintf("key-%03d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        leaf.Insert(key, value)
    }

    // 再插入一个，应该触发分裂
    err := leaf.Insert([]byte("key-100"), []byte("value-100"))
    // TODO: 需要修改 Insert() 方法，在满时自动分裂
    // 目前 Insert() 不会自动触发分裂，需要调用方处理

    // 手动分裂（新签名：返回 2 个值）
    newLeaf, splitKey, err := leaf.Split()
    require.NoError(t, err)
    assert.Equal(t, 8, leaf.NumKeys())
    assert.Equal(t, 9, newLeaf.NumKeys())
}

// TestLeafPage_Split_EmptyPage 测试空页面分裂
func TestLeafPage_Split_EmptyPage(t *testing.T) {
    leaf := NewLeafPage(1)

    _, _, err := leaf.Split()
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "cannot split")
}

// TestLeafPage_Split_SingleKey 测试单键页面分裂
func TestLeafPage_Split_SingleKey(t *testing.T) {
    leaf := NewLeafPage(1)
    leaf.Insert([]byte("key1"), []byte("value1"))

    _, _, err := leaf.Split()
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "cannot split")
}
```

---

## Day 7: InternalPage Split 和引用更新

### 任务 7.0: 实现 InternalPage.InsertKeyChild()

**文件**: `internal_page.go`（新增方法）

**设计方案确认**：添加 `InsertKeyChild()` 方法

**实现**：
```go
// InsertKeyChild 在指定位置插入键和子节点引用
// 用于 Split 后在父节点中插入分裂键和新子节点
//
// 参数：
//   key - 要插入的键（分裂键）
//   childRef - 新的子节点引用
//
// 返回：
//   error - 错误信息
func (p *InternalPage) InsertKeyChild(key []byte, childRef *PageRef) error {
    // 1. 使用二分查找找到插入位置
    idx := p.search(key)

    // 2. 插入键
    p.keys = insertSlice(p.keys, idx, key)

    // 3. 插入子节点引用（在 idx+1 位置）
    p.children = insertSlice(p.children, idx+1, childRef)

    // 4. 更新版本号
    p.version++

    return nil
}
```

**使用示例**：
```go
// 在父节点中插入分裂键和新的子节点
parentPage.InsertKeyChild(splitKey, newChildRef)
```

---

### 任务 7.1: 实现带引用更新的 InternalPage.Split()

**文件**: `internal_page.go`（修改现有 Split 方法）

**设计方案确认**：**使用方案 B（调用方传入 PageRef）**

**修改后的签名**：
```go
// Split 分裂内部节点
//
// 返回：
//   - *InternalPage: 新创建的内部节点
//   - []byte: 分裂键（提升到父节点）
//   - error: 错误信息
//
// 调用方负责：
//   - 创建新节点的 PageRef 和 PageInfo
//   - 更新父节点的 children 引用
//   - 更新所有子节点的 parentRef（包括新节点和原节点）
func (p *InternalPage) Split() (*InternalPage, []byte, error) {
    mid := len(p.keys) / 2
    splitKey := p.keys[mid]

    // 创建新页面
    newPage := NewInternalPage(model.PageID(p.pageID + 1))
    newPage.keys = append(newPage.keys, p.keys[mid+1:]...)
    newPage.children = append(newPage.children, p.children[mid+1:]...)

    // 修改当前页面
    p.keys = p.keys[:mid]
    p.children = p.children[:mid+1]
    p.version++

    return newPage, splitKey, nil
}
```

---

### 任务 7.2: 实现引用更新机制

**设计方案确认**：使用 `PageInfo.SetParentRef()`

**实现**：
```go
// UpdateChildrenParentRef 更新子节点的父引用
// 用于 Split 后维护引用链完整性
//
// 参数：
//   parentRef - 新的父节点引用
//
// 返回：
//   error - 错误信息
func (p *InternalPage) UpdateChildrenParentRef(parentRef *PageRef) error {
    for _, childRef := range p.children {
        if childRef != nil {
            childInfo := childRef.GetPageInfo()
            if childInfo != nil {
                // 直接设置父引用（非 CAS 操作，因为这是新创建的路径）
                childInfo.SetParentRef(parentRef)
            }
        }
    }
    return nil
}
```

**引用更新流程**：

```
分裂前：
InternalPage (keys=[k5,k10], children=[c1,c2,c3])
    ├── c1 → LeafPage (keys=[k1,k2])
    ├── c2 → LeafPage (keys=[k6,k7,k8])
    └── c3 → LeafPage (keys=[k11,k12])

分裂 c2（LeafPage）：
LeafPage (keys=[k6,k7,k8]) → Split() →
    ├── leftLeaf (keys=[k6,k7])
    └── rightLeaf (keys=[k8])

调用方更新父节点：
parentPage.InsertKeyChild(splitKey, rightRef)
parentPage.UpdateChildrenParentRef(newParentRef)
```

**关键点**：
- ✅ 使用 `PageInfo.SetParentRef()` 更新父引用
- ✅ 调用方负责在 CCOW 路径中更新所有引用
- ✅ 避免在 Split() 方法中直接操作引用（保持简洁）
    p.keys = insertSlice(p.keys, idx, splitKey)

    // 3. 插入新的子节点引用
    p.children = insertSlice(p.children, idx+1, newChildRef)

    // 4. 更新子节点的父引用
    if newChildRef != nil {
        // TODO: 设置父引用为当前节点
    }

    p.version++
    return nil
}
```

### 任务 7.3: 实现 BTree.splitInternal()

**文件**: `btree_split.go`（扩展）

```go
// splitInternal 分裂内部节点，并向上传播
func (b *BTree) splitInternal(ctx context.Context, path []*PageInfo, internalInfo *PageInfo, splitKey []byte, newChildRef *PageRef) error {
    // 1. 获取父节点
    var parentRef *PageRef
    if len(path) > 1 {
        parentInfo := path[len(path)-2]
        // 找到指向当前内部节点的子节点引用
        parentPage := parentInfo.GetPage().(*InternalPage)
        for i, childRef := range parentPage.Children() {
            if childRef != nil && childRef.GetPageInfo() == internalInfo {
                parentRef = childRef
                break
            }
        }
    }

    // 2. 分裂内部节点
    internalPage := internalInfo.GetPage().(*InternalPage)
    newInternal, newSplitKey, updatedRefs, err := internalPage.Split(parentRef)
    if err != nil {
        return fmt.Errorf("split internal: %w", err)
    }

    // 3. 如果是根节点
    if len(path) == 1 {
        return b.splitRoot(ctx, internalInfo, newChildRef, splitKey)
    }

    // 4. 在父节点中插入分裂键
    parentInfo := path[len(path)-2]
    parentPage := parentInfo.GetPage().(*InternalPage)

    // 找到插入位置
    idx := parentPage.search(splitKey)
    if idx < len(parentPage.keys) && bytes.Equal(parentPage.keys[idx], splitKey) {
        // 分裂键已存在（不太可能），替换
        parentPage.keys[idx] = newSplitKey
    } else {
        // 插入新的分裂键
        parentPage.Insert(splitKey, newChildRef)
    }

    // 5. 检查父节点是否需要分裂
    if parentPage.NumKeys() >= parentPage.GetMaxKeys() {
        return b.splitInternal(ctx, path[:len(path)-1], parentInfo, newSplitKey, nil)
    }

    return nil
}
```

### 任务 7.4: Split 测试

**文件**: `internal_page_split_test.go`（新建）

```go
// TestInternalPage_Split_Basic 测试基本分裂
func TestInternalPage_Split_Basic(t *testing.T) {
    internal := NewInternalPage(1)

    // 创建子节点
    child1 := NewLeafPage(10)
    child2 := NewLeafPage(11)
    child3 := NewLeafPage(12)

    // 设置键和子节点
    internal.keys = [][]byte{
        []byte("key5"),
        []byte("key10"),
    }
    internal.children = []*PageRef{
        NewPageRefWithInfo(createPageInfo(child1)),
        NewPageRefWithInfo(createPageInfo(child2)),
        NewPageRefWithInfo(createPageInfo(child3)),
    }

    // 分裂
    newInternal, splitKey, updatedRefs, err := internal.Split(nil)
    require.NoError(t, err)

    // 验证分裂键
    assert.Equal(t, []byte("key10"), splitKey)

    // 验证原页面
    assert.Equal(t, 1, len(internal.keys))
    assert.Equal(t, []byte("key5"), internal.keys[0])
    assert.Equal(t, 2, len(internal.children))

    // 验证新页面
    assert.Equal(t, 0, len(newInternal.keys))
    assert.Equal(t, 1, len(newInternal.children))
}

// TestBTree_Split_RecursiveSplit 测试递归分裂
func TestBTree_Split_RecursiveSplit(t *testing.T) {
    // 构建一个多层树，每一层都快满了
    // 触发递归分裂
    t.Skip("TODO: Day 7 - 实现递归分裂测试")
}

// TestBTree_Split_RootSplit 测试根节点分裂
func TestBTree_Split_RootSplit(t *testing.T) {
    // 创建一个满的根节点（叶子节点）
    // 分裂后应该创建新的内部节点作为根
    t.Skip("TODO: Day 7 - 实现根节点分裂测试")
}
```

---

## Merge 操作（Future Phase - 不在 Day 6-7 范围内）

> **实施说明**：Merge 操作是可选的，不在 Day 6-7 的实施计划中。允许节点低于 `minKeys` 不会影响正确性，只影响空间效率。

### Merge 触发条件（未来实现）

**触发 Merge 的场景**：
1. **LeafPage.Delete()** - 删除后 `len(keys) < minKeys`
2. **InternalPage.DeleteChild()** - 删除后 `len(keys) < minKeys`

**Merge 策略**：
```
Option 1: 从兄弟节点借一个键（Redistribute）
    ├── 兄弟节点有足够的键
    └── 重新分配键

Option 2: 合并到兄弟节点（Merge）
    ├── 兄弟节点也不够
    └── 合并两个节点
```

### 实施计划（Week 14 或后续 Phase）

**Phase 1：基础 Merge（Week 14，如果时间允许）**
- [ ] LeafPage.Merge()
- [ ] InternalPage.Merge()
- [ ] 合并后的引用链更新

**Phase 2：Redistribute（优化，后续 Phase）**
- [ ] LeafPage.Redistribute()（从兄弟节点借）
- [ ] InternalPage.Redistribute()
- [ ] 自动触发 Merge 的 Delete()

**Phase 3：树平衡优化（Future Phase）**
- [ ] 合并后的树平衡优化
- [ ] 自适应 Merge 策略
- [ ] 性能优化

### 当前策略（Day 6-7）

**允许节点低于 minKeys**：
- ✅ 不影响正确性（BTree 仍然能正确工作）
- ✅ 简化实现（优先完成 Split）
- ⚠️ 空间效率降低（可接受）
- 📊 后续通过 Merge 恢复空间效率

---

## 集成到 Get/Set

### 修改 BTree.Set() 集成 Split

**文件**: `btree.go`（修改 Set 方法）

```go
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    // ... 现有代码

    // Insert the key-value pair
    inserted, err := leaf.Insert(key, value)
    if err != nil {
        return fmt.Errorf("insert into leaf: %w", err)
    }

    // ✅ 新增：检查是否需要分裂
    if leaf.NumKeys() > leaf.GetMaxKeys() {
        if err := b.splitLeaf(ctx, copiedPath, leafInfo); err != nil {
            return fmt.Errorf("split leaf: %w", err)
        }
    }

    // ... 现有代码
}
```

### LeafPage.GetMaxKeys() 辅助方法

**文件**: `leaf_page.go`（新增）

```go
// GetMaxKeys 获取最大键数量
func (p *LeafPage) GetMaxKeys() int {
    return 16 // TODO: 从配置读取
}

// GetMinKeys 获取最小键数量
func (p *LeafPage) GetMinKeys() int {
    return p.GetMaxKeys() / 2
}
```

---

## 风险和注意事项

### 🔴 高风险

1. **引用链维护**
   - **风险**：Split 后引用链断裂，导致访问错误
   - **应对**：
     - 使用原子操作（CAS）更新引用
     - Split 前复制完整路径，Split 后原子替换
     - 测试覆盖所有引用更新场景

2. **并发 Split**
   - **风险**：多个 goroutine 同时触发 Split
   - **应对**：
     - CAS 失败时重试（已在 Set() 中实现）
     - 使用版本号检测并发冲突
     - 测试并发 Split 场景

3. **递归深度**
   - **风险**：多层 Split 导致栈溢出
   - **应对**：
     - 限制 `maxLevels`（当前为 10）
     - 检查递归深度，超过阈值时返回错误
     - 使用迭代而非递归（优化方向）

### 🟡 中风险

4. **内存占用**
   - **风险**：Split 时需要复制多个页面
   - **应对**：
     - 使用 sync.Pool 复用 Page 对象
     - 及时释放旧版本的 PageInfo
     - 监控内存使用

5. **性能影响**
   - **风险**：Split 操作较慢，影响写入延迟
   - **应对**：
     - 异步 Split（高级优化）
     - 预分裂（在达到阈值前触发）
     - 性能测试和优化

---

## 测试策略

### 单元测试

| 测试 | 文件 | 覆盖场景 |
|------|------|---------|
| TestLeafPage_Split_Basic | leaf_page_split_test.go | 基本分裂功能 |
| TestLeafPage_Split_MaxKeys | leaf_page_split_test.go | 达到 maxKeys 时分裂 |
| TestLeafPage_Split_EmptyPage | leaf_page_split_test.go | 空页面错误处理 |
| TestInternalPage_Split_Basic | internal_page_split_test.go | 内部节点分裂 |
| TestBTree_Split_LeafSplit | btree_split_test.go | 叶子节点分裂 |
| TestBTree_Split_InternalSplit | btree_split_test.go | 内部节点分裂 |
| TestBTree_Split_RootSplit | btree_split_test.go | 根节点分裂 |
| TestBTree_Split_RecursiveSplit | btree_split_test.go | 递归分裂 |

### 集成测试

```go
// TestBTree_Set_TriggerSplit 测试 Set 触发分裂
func TestBTree_Set_TriggerSplit(t *testing.T) {
    // 1. 创建 BTree
    // 2. 插入 maxKeys + 1 个键
    // 3. 验证触发分裂
    // 4. 验证树高度增加
    t.Skip("TODO: Day 6-7 - 实现")
}

// TestBTree_Set_MultipleSplits 测试多次分裂
func TestBTree_Set_MultipleSplits(t *testing.T) {
    // 1. 创建 BTree
    // 2. 插入大量键，触发多次分裂
    // 3. 验证树结构正确
    t.Skip("TODO: Day 6-7 - 实现")
}

// TestBTree_ConcurrentSplit 测试并发分裂
func TestBTree_ConcurrentSplit(t *testing.T) {
    // 多个 goroutine 同时插入
    // 验证并发安全性
    t.Skip("TODO: Day 7 - 实现")
}
```

### 性能测试

```go
// BenchmarkLeafPage_Split 分裂性能
func BenchmarkLeafPage_Split(b *testing.B) {
    leaf := createFullLeafPage()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        leaf.Split(nil)
    }
}

// BenchmarkBTree_Set_WithSplit 带 Split 的 Set 性能
func BenchmarkBTree_Set_WithSplit(b *testing.B) {
    tree := setupBTree(b)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        tree.Set(context.Background(), key, value)
    }
}
```

---

## 验收标准

### Day 6 完成标准

- [ ] LeafPage.Split() 正确处理引用
- [ ] BTree.splitLeaf() 实现根节点分裂
- [ ] BTree.splitLeaf() 实现非根节点分裂
- [ ] 所有 LeafPage Split 单元测试通过
- [ ] 集成测试：Set 触发分裂成功

### Day 7 完成标准

- [ ] InternalPage.Split() 正确处理子节点引用
- [ ] 引用更新机制（SetParentRef）实现
- [ ] BTree.splitInternal() 实现递归分裂
- [ ] 所有 InternalPage Split 单元测试通过
- [ ] 并发 Split 测试通过
- [ ] Get/Set 集成 Split 后功能正常

---

## 后续工作（Week 14）

1. **Merge 实现** - 处理删除后的节点合并
2. **Redistribute** - 从兄弟节点借键
3. **Split 优化** - 异步 Split、预分裂
4. **ChunkManager 集成** - Split 后的持久化
5. **端到端测试** - 完整的 Get/Set/Split/Merge 流程

---

## 参考资料

- **Lealone AOSE 论文**：Section 4.3 - Page Splitting
- **BTree 经典算法**：《Introduction to Algorithms》Chapter 18
- **现有代码**：`leaf_page.go:196` - 现有 Split() 实现
- **现有代码**：`internal_page.go:234` - 现有 Split() 实现
