# Phase 6.0 - Split 传播设计方案

**日期**: 2026-04-04
**状态**: 设计阶段
**目标**: 实现多级 BTree 的 split 传播机制

---

## 1. 核心问题

### 1.1 传统 Split 传播的问题

**级联更新问题**：
```
叶子 split (key-100 插入)
  → 父节点索引更新 (添加 splitKey)
    → 父节点也满了，需要 split
      → 祖父节点索引更新
        → ... 直到 root
```

**影响范围**：
- 每次叶子 split 可能触发 O(log N) 次父节点更新
- 高频写入场景下，大量 CAS 冲突
- 整个路径上的节点都需要 COW

**性能影响**：
- CPU：多次 COW + CAS 操作
- 内存：路径上每个节点都创建新页面
- 并发：路径上所有锁竞争

### 1.2 Lealone 的解决方案

参考 `thoughts/Lealone/btree/concurrent_cow_btree_2.java`：

```java
// Lealone 的优化策略
1. 延迟传播：split 后不立即更新父节点
2. 标记机制：使用 SplitMarker 标记 split 事件
3. 按需修复：读取时检测到 marker，触发父节点更新
```

**关键洞察**：
- Split 是低频操作（页面满才 split）
- 读取远多于写入
- 可以让读取操作承担部分更新成本

---

## 2. 设计方案

### 2.1 方案 A：最小化传播（推荐）

**核心思想**：Split 后只更新直接父节点，避免级联

**实现步骤**：

```
1. 叶子 split：
   - 创建 left/right 叶子
   - 生成 splitKey

2. 直接父节点更新（仅一层）：
   - COW 父节点
   - 插入 splitKey + right child
   - CAS 更新父节点的 PageRef

3. 父节点 split（如果需要）：
   - 检测父节点是否已满
   - 如果已满 → 标记为 "待 split"，但不立即执行
   - 后续操作时再处理

4. 延迟高层更新：
   - 高层节点的 split 延迟到下次访问时
   - 使用 SplitMarker 标记
```

**优点**：
- ✅ 单次 split 只影响直接父节点（1 层）
- ✅ 减少 CAS 冲突范围
- ✅ 降低内存分配

**缺点**：
- ⚠️ 读取时可能遇到 SplitMarker，需要额外处理
- ⚠️ 实现复杂度增加

**代码示例**：

```go
// operations.go

func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    for attempt := 0; attempt < MaxCASRetries; attempt++ {
        // ... 原有逻辑 ...

        // Step 5: Apply COW mutation
        result, err := mutate(oldLeaf)
        if err != nil {
            // ...
        }

        // Step 6: Check if split needed
        if result.needsSplit {
            // 执行 split
            left, right, splitKey, err := oldLeaf.Split()
            if err != nil {
                // ...
            }

            // Step 7: 只更新直接父节点（一层）
            parentUpdated := false
            if parentPath := path.ParentPath(); len(parentPath) > 0 {
                directParent := parentPath[len(parentPath)-1]
                parentUpdated = updateDirectParent(b, directParent, splitKey, left.PageID(), right.PageID())
            }

            // Step 8: 如果父节点也满了，标记但不立即处理
            if !parentUpdated {
                // 标记父节点需要 split（延迟处理）
                markParentNeedsSplit(directParent)
            }

            // 更新叶子节点的 PageRef
            newInfo := &PageInfo{
                PageID:  left.PageID(),  // 使用 left 作为新页面
                Version: oldInfo.Version + 1,
            }

            if !leafRef.CAS(oldInfo, newInfo) {
                // CAS 冲突，清理
                _ = b.storage.FreePage(left.PageID())
                _ = b.storage.FreePage(right.PageID())
                continue
            }

            // 更新 size
            b.size.Add(result.delta)
            path.ReleaseAll()
            return nil
        }

        // ... 原有的非 split 逻辑 ...
    }
}

// 只更新直接父节点
func updateDirectParent(b *BTree, parentEntry PathEntry, splitKey []byte, leftID, rightID model.PageID) bool {
    parentRef := parentEntry.Ref

    oldInfo := parentRef.GetPageInfo()
    if oldInfo == nil {
        return false
    }

    oldParent, err := b.storage.GetNodePage(oldInfo.PageID)
    if err != nil {
        return false
    }

    // COW: 插入新 entry
    idx := parentEntry.Index
    newParent, err := oldParent.InsertEntry(idx, splitKey, leftID, rightID)
    if err != nil {
        return false
    }

    // 检查父节点是否已满
    if newParent.IsFull() {
        // 标记但不立即 split
        return false
    }

    // CAS 更新父节点
    newInfo := &PageInfo{
        PageID:  newParent.PageID(),
        Version: oldInfo.Version + 1,
    }

    if !parentRef.CAS(oldInfo, newInfo) {
        _ = b.storage.FreePage(newParent.PageID())
        return false
    }

    return true
}
```

### 2.2 方案 B：批量传播（延后到 Phase 7+）

**核心思想**：累积多个 split，交给 Task Scheduler 批量处理

**实现步骤**：

```
1. 维护全局 SplitQueue（无锁队列）
2. 写入操作检测到 split，加入队列
3. Task Scheduler 定期批量处理 split
4. 批量更新减少 CAS 冲突
```

**优点**：
- ✅ 批量处理减少 CAS 冲突
- ✅ 写入操作不被阻塞
- ✅ 与 Task Scheduler 集成（已有基础设施）

**缺点**：
- ❌ 需要后台任务机制
- ❌ 实现复杂度中等
- ❌ 读取可能看到短暂不一致状态

**延后原因**：
- ⏸️ **优先级较低**：方案 A 已能满足 Phase 6 需求
- ⏸️ **依赖基础设施**：需要先完成 Task Scheduler 集成
- ⏸️ **复杂度权衡**：收益不足以抵消本阶段的实现成本

**触发条件**（Phase 7+ 考虑）：
- 如果写入吞吐量成为瓶颈（> 1M writes/sec）
- 如果 CAS 冲突率 > 50%
- 如果需要更高并发扩展性（64+ 核心场景）

### ~~2.3 方案 C：路径压缩~~（已废弃）

> **废弃原因**：
> - 4KB 页面大小是固定的（`PageSize = 4096`）
> - 改变页面大小会影响整个系统架构
> - 压缩存储无法根本解决级联更新问题
> - 收益不明显，成本高

---

## 3. 推荐方案：方案 A（最小化传播）

## 3. 推荐方案：方案 A（最小化传播）

### 3.1 核心设计

**SplitMarker 机制**：

```go
// page_ref.go

type SplitMarker struct {
    SplitKey   []byte
    LeftChild  model.PageID
    RightChild model.PageID
    Timestamp  time.Time
}

type PageRef struct {
    // ... 原有字段 ...
    splitMarker atomic.Pointer[SplitMarker]
}

// 标记 split 事件
func (r *PageRef) MarkSplit(splitKey []byte, left, right model.PageID) {
    marker := &SplitMarker{
        SplitKey:   splitKey,
        LeftChild:  left,
        RightChild: right,
        Timestamp:  time.Now(),
    }
    r.splitMarker.Store(marker)
}

// 检查是否有 split 标记
func (r *PageRef) GetSplitMarker() *SplitMarker {
    return r.splitMarker.Load()
}
```

**读取时的 Split 处理**：

```go
// search.go

func searchPath(storage *OffheapBTreeStorage, rootRef *RootPageRef, key []byte) (*SearchPath, error) {
    path := &SearchPath{}
    currentRef := rootRef.PageRef

    for {
        // 检查 split marker
        if marker := currentRef.GetSplitMarker(); marker != nil {
            // 触发父节点更新
            if err := resolveSplitMarker(storage, currentRef, marker); err != nil {
                // 更新失败，继续使用旧数据
            }
        }

        pInfo := currentRef.GetPageInfo()
        if pInfo == nil {
            return nil, ErrPageFreed
        }

        // ... 原有的搜索逻辑 ...
    }
}

// 处理 split marker
func resolveSplitMarker(storage *OffheapBTreeStorage, ref *PageRef, marker *SplitMarker) error {
    // 懒更新：读取时修复父节点
    // 如果成功，清除 marker
    defer ref.splitMarker.Store(nil)

    // 更新父节点的索引
    // ... 实现细节 ...
}
```

### 3.2 数据结构扩展

```go
// page_handle.go

type LeafPage interface {
    // ... 原有方法 ...

    // 新增：检查是否需要 split
    NeedsSplit() bool

    // 新增：获取 split 相关信息
    GetSplitInfo() (splitKey []byte, leftID, rightID model.PageID, err error)
}

type NodePage interface {
    // ... 原有方法 ...

    // 新增：检查是否需要 split
    NeedsSplit() bool
}
```

### 3.3 并发安全性

**问题**：Split 期间的并发访问

**解决方案**：
1. **乐观锁**：使用 CAS 确保原子性
2. **版本号**：PageInfo.Version 检测并发修改
3. **重试机制**：检测到 marker 变化时重新搜索

```go
// 读取操作的重试逻辑
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    for retries := 0; retries < MaxSplitRetries; retries++ {
        path, err := searchPath(b.storage, b.rootRef, key)
        if err != nil {
            return nil, err
        }

        // 检查路径上是否有 split marker
        hasMarker := false
        for _, entry := path.entries {
            if entry.Ref.GetSplitMarker() != nil {
                hasMarker = true
                break
            }
        }

        if hasMarker {
            // 路径上有 marker，释放并重试
            path.ReleaseAll()
            time.Sleep(time.Microsecond * time.Duration(retries))
            continue
        }

        // 正常读取逻辑
        // ...
    }
}
```

---

## 4. 实现计划

### 4.1 Phase 6.0 任务分解

**Task 6.1**: SplitMarker 基础设施（0.5 天）
- [ ] 添加 `SplitMarker` 结构体
- [ ] 扩展 `PageRef` 添加 marker 字段
- [ ] 实现 `MarkSplit()` / `GetSplitMarker()`

**Task 6.2**: Leaf Split 检测（0.5 天）
- [ ] 添加 `LeafPage.NeedsSplit()` 方法
- [ ] 修改 `writeOperation` 检测 split 条件
- [ ] 测试：`TestLeafSplitDetection`

**Task 6.3**: 直接父节点更新（1 天）
- [ ] 实现 `updateDirectParent()` 函数
- [ ] 处理父节点 split 标记
- [ ] 测试：`TestDirectParentUpdate`

**Task 6.4**: 延迟高层更新（1 天）
- [ ] 实现 `resolveSplitMarker()` 函数
- [ ] 修改 `searchPath` 检查 marker
- [ ] 测试：`TestLazySplitResolution`

**Task 6.5**: Root Split（0.5 天）
- [ ] 实现 `splitRoot()` 方法
- [ ] 创建新的 root 节点
- [ ] 测试：`TestRootSplit`

**Task 6.6**: 集成测试（0.5 天）
- [ ] `TestMultiLevelRandomOperations`
- [ ] `TestConcurrentSplitMerge`
- [ ] 压力测试：10000+ keys

### 4.2 验证标准

```bash
# 功能测试
go test -run TestLeafSplit ./...
go test -run TestDirectParentUpdate ./...
go test -run TestRootSplit ./...

# 并发测试
go test -run TestMultiLevelRandomOperations -race ./...
go test -run TestConcurrentSplitMerge -race ./...

# 压力测试
go test -run TestLargeDataset ./...

# 性能基准
go test -bench=BenchmarkBTreeSet -benchtime=3s ./...
```

---

## 5. 性能预期

### 5.1 Split 频率分析

**假设**：
- 页面容量：126 keys
- 数据集：1M keys
- Split 次数：~8000 次

**当前设计**：
- 每次 split 影响层数：O(log N) ≈ 20 层
- 总更新次数：8000 × 20 = 160,000 次

**优化后**：
- 每次 split 影响层数：1 层（直接父节点）
- 立即更新：8000 × 1 = 8,000 次
- 延迟更新：8000 × (log N - 1) ≈ 152,000 次（由读取操作承担）

**收益**：
- 写入操作减少 95% 的 CAS 冲突
- 写入延迟从 O(log N) → O(1)

### 5.2 内存使用

**当前**：
- 每次 split：O(log N) 个新页面
- 总页面数：~160,000（split 期间）

**优化后**：
- 每次 split：1-2 个新页面（叶子 + 可能的父节点）
- 总页面数：~16,000（减少 90%）

---

## 6. 风险与缓解

### 6.1 SplitMarker 泄漏

**风险**：Marker 未被清理

**缓解**：
- 添加超时机制（5 秒后强制清理）
- 后台线程定期扫描 marker
- 读取操作失败时清理 marker

### 6.2 读取性能退化

**风险**：读取遇到 marker 需要额外处理

**缓解**：
- Marker 是低频事件（只在 split 后短暂存在）
- 限制 marker 处理的重试次数
- 监控 marker 遇到率（目标 < 1%）

### 6.3 并发正确性

**风险**：Split 期间的并发访问

**缓解**：
- 全面的并发测试（`-race`）
- 压力测试（1000 并发写入）
- 不变式检查（`AssertInvariants`）

---

## 7. 决策点

### 7.1 立即决策

- ✅ **采用方案 A**：最小化传播
- ✅ **实现 SplitMarker**：延迟高层更新
- ✅ **限制传播层数**：只更新直接父节点

### 7.2 延后决策

- ⏸️ **方案 B（批量传播）**：Phase 7+ 考虑，由 Task Scheduler 处理
  - **触发条件**：写入吞吐量 > 1M/sec 或 CAS 冲突率 > 50%
  - **依赖**：Task Scheduler 基础设施（已有）
  - **收益**：进一步减少 CAS 冲突，提升高并发性能
- ⏸️ **后台清理线程**：如果 marker 泄漏严重再添加

---

## 8. 参考

### 8.1 Lealone 实现

- 文件：`thoughts/Lealone/btree/concurrent_cow_btree_2.java`
- 关键类：`SplitMarker`, `BTreeMap`, `PageReference`
- 策略：延迟传播 + 按需修复

### 8.2 理论基础

- **B-Link Tree**: 允许节点有右兄弟指针，延迟父节点更新
- **Lazy Maintenance**: 将维护成本分摊到读取操作
- **Optimistic Concurrency**: 使用版本号检测并发修改

---

## 9. 下一步

1. **审查设计**：团队评审此方案
2. **创建分支**：`feat/btree-split-propagation`
3. **开始实现**：Task 6.1（SplitMarker 基础设施）

**预计工期**：4-5 天
**风险等级**：中（并发复杂性）
**收益评估**：高（解锁写入性能 + 减少内存使用）
