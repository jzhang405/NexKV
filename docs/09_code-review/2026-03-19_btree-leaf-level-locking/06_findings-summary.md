# 问题汇总表

**审查日期**: 2026-03-19
**审查范围**: BTree Leaf-Level Locking 实现

---

## 问题分级说明

- **P0**: 严重问题，必须解决
- **P1**: 重要问题，建议解决
- **P2**: 次要问题，可延后处理

---

## 问题清单

| ID | 级别 | 类别 | 问题描述 | 文件 | 行号 | 影响 | 建议 |
|----|------|------|----------|------|------|------|------|
| 1 | P0 | 性能 | findLeafPageRef 双遍历 | search_path.go | 160-240 | 5% 性能损失 | 合并遍历逻辑 |
| 2 | P1 | 性能 | handleSplitSync 不必要拷贝 | leaf_lock_set.go | 143-146 | 2% 分裂性能损失 | 懒拷贝优化 |
| 3 | P1 | 质量 | 测试覆盖率 54.2% < 80% | - | - | 边界情况未覆盖 | 补充测试 |
| 4 | P1 | 质量 | 批量操作测试缺失 | - | - | GetBatch 未验证 | 添加测试 |
| 5 | P1 | 并发 | 页面分裂并发测试缺失 | - | - | 分裂安全性未验证 | 添加测试 |
| 6 | P1 | 质量 | 启用被禁用的测试 | search_path_test.go | 13-50 | 功能未验证 | 更新并启用 |
| 7 | P1 | 质量 | 启用被禁用的测试 | lazy_load_test.go | 11-42 | 懒加载未验证 | 实现并启用 |
| 8 | P1 | 代码 | 添加包注释 | btree.go | 1 | 文档不完整 | 补充包注释 |
| 9 | P1 | 并发 | ErrRetry 无重试上限 | leaf_lock_set.go | 46 | 可能无限重试 | 添加上限 |
| 10 | P2 | 质量 | 20 个 TODO 注释 | 多个文件 | - | 技术债务 | 逐步解决 |
| 11 | P2 | 文档 | CloneWithDelta 缺少说明 | leaf_page.go | - | 实现细节不清 | 补充注释 |
| 12 | P2 | 性能 | Delta Chain 预分配优化 | cow_delta_ref.go | 97 | append 开销 | 预分配 8 |
| 13 | P2 | 并发 | GetParentRef 类型断言 | page_ref.go | 175 | 类型断言无检查 | 添加 nil 检查 |

---

## 问题详情

### 1. P0: findLeafPageRef 双遍历

**位置**: `search_path.go:160-240`

**问题描述**:
- `searchPath()` 遍历一次路径 (line 162)
- `findLeafPageRef()` 再次遍历收集 PageRef (line 181-236)
- 重复工作浪费 CPU 时间

**代码示例**:
```go
func (b *BTree) findLeafPageRef(...) (*PageRef, []*PageInfo, error) {
    path, _ := b.searchPath(ctx, key)    // 遍历 1
    for i := 0; i < len(path)-1; i++ {   // 遍历 2
        ...
    }
}
```

**改进方案**:
```go
// 一次遍历同时收集 PageInfo 和 PageRef
func (b *BTree) searchPathWithRefs(ctx context.Context, key []byte) ([]*PageInfo, []*PageRef, error) {
    // 在遍历过程中同时收集 PageRef
    ...
}
```

**预期收益**: ~5% 写入性能提升

---

### 2. P1: handleSplitSync 不必要拷贝

**位置**: `leaf_lock_set.go:143-146`

**问题描述**:
- 每次分裂都拷贝 keys 和 values
- CAS 失败概率极低（锁保护下）
- 大部分拷贝是浪费的

**改进方案**:
```go
// 懒拷贝：仅在 CAS 失败时拷贝
var originalKeys, originalValues [][]byte
var copied bool

restoreIfNeeded := func(casErr error) error {
    if casErr == ErrRetry && !copied {
        originalKeys = make([][]byte, len(leafPage.keys))
        copy(originalKeys, leafPage.keys)
        copied = true
    }
    return casErr
}
```

**预期收益**: ~2% 分裂性能提升

---

### 3. P1: 测试覆盖率不足

**当前**: 54.2%
**目标**: 80%

**缺失功能**:
- GetBatch/SetBatch/DeleteBatch (0%)
- RangeScan (0%)
- CreateSnapshot/ReleaseSnapshot (0%)

**改进方案**:
1. 补充批量操作测试
2. 补充范围扫描测试
3. 补充快照功能测试

---

### 4. P1: 批量操作测试缺失

**影响**: GetBatch/SetBatch 功能未验证

**改进方案**:
```go
func TestGetBatch(t *testing.T) {
    tree := setupTestTree(t)
    keys := [][]byte{[]byte("key1"), []byte("key2")}

    values, err := tree.GetBatch(ctx, keys)
    require.NoError(t, err)
    require.Len(t, values, 2)
}
```

---

### 5. P1: 页面分裂并发测试缺失

**影响**: handleSplitSync 并发安全性未验证

**改进方案**:
```go
func TestHandleSplitSync_Concurrent(t *testing.T) {
    const goroutines = 10
    var wg sync.WaitGroup

    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            // 触发分裂
            for j := 0; j < 100; j++ {
                key := fmt.Sprintf("key-%05d", id*100+j)
                tree.Set(ctx, []byte(key), []byte("value"))
            }
        }(i)
    }
    wg.Wait()

    // 验证树完整性
    verifyTreeIntegrity(t, tree)
}
```

---

### 6-7. P1: 被禁用的测试

**search_path_test.go** (4 个测试)
**lazy_load_test.go** (4 个测试)

**影响**: 功能未验证

**改进方案**:
1. 更新 searchPath 测试（实现已完成）
2. 实现 ChunkManager 集成测试
3. 移除 `t.Skip()` 调用

---

### 8. P1: 添加包注释

**影响**: 文档不完整

**改进方案**:
```go
// Package btree provides an in-memory B-Tree implementation with
// Leaf-Level Locking optimization for high-concurrency scenarios.
//
// Key features:
//   - Leaf-Level Locking: 99.37% of writes only need Leaf CAS
//   - Copy-on-Write with Delta Chain optimization
//   - Lazy-loaded PageLock for memory efficiency
//
// Basic usage:
//   tree, err := btree.OpenBTree("", &model.BTreeConfig{})
//   tree.Set(ctx, []byte("key"), []byte("value"))
//   value, _ := tree.Get(ctx, []byte("key"))
package btree
```

---

### 9. P1: ErrRetry 无重试上限

**位置**: `leaf_lock_set.go:46`

**问题**: 无限重试可能导致 CPU 100%

**改进方案**:
```go
const maxRetries = 3

func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    for attempt := 0; attempt < maxRetries; attempt++ {
        err := b.setWithLeafLock(ctx, key, value)
        if err != ErrRetry {
            return err
        }
        runtime.Gosched()
    }
    return fmt.Errorf("max retries exceeded for key=%s", string(key))
}
```

---

### 10. P2: 20 个 TODO 注释

**影响**: 技术债务

**改进方案**: 逐步解决，优先级排序

---

### 11. P2: CloneWithDelta 缺少说明

**位置**: `leaf_page.go`

**问题**: 实现细节不清楚

**改进方案**: 补充注释说明 Delta Chain 工作原理

---

### 12. P2: Delta Chain 预分配优化

**位置**: `cow_delta_ref.go:97`

**当前**: `deltas: make([]Delta, 0)`
**优化**: `deltas: make([]Delta, 0, 8)`

**权衡**: 需要评估内存浪费 vs 扩容开销

---

### 13. P2: GetParentRef 类型断言

**位置**: `page_ref.go:175`

**当前**: `return r.parentRef.Load().(*PageRef)`

**改进**:
```go
func (r *PageRef) GetParentRef() *PageRef {
    val := r.parentRef.Load()
    if val == nil {
        return nil
    }
    return val.(*PageRef)
}
```

---

## 问题统计

| 级别 | 数量 | 必须解决 |
|------|------|----------|
| P0 | 1 | 是 |
| P1 | 8 | 建议 |
| P2 | 5 | 可选 |

---

## 优先级建议

### 合并前必须解决 (P0)

1. **findLeafPageRef 双遍历优化** - 性能关键
   - 影响: 5% 性能提升
   - 工作量: 中等

### 强烈建议解决 (P1)

1. **测试覆盖率提升** - 质量保证
2. **批量操作测试** - 功能验证
3. **页面分裂并发测试** - 并发安全
4. **ErrRetry 重试上限** - 健壮性

### 可延后解决 (P2)

1. **TODO 注释** - 技术债务
2. **文档完善** - 可读性
3. **Delta Chain 预分配** - 微优化
