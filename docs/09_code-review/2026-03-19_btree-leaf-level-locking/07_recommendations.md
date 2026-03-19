# 改进建议

**审查日期**: 2026-03-19
**基于**: Code Review 发现的问题

---

## 短期改进 (1-2 周)

### 1. P0: 合并 findLeafPageRef 双遍历

**问题**: `search_path.go:160-240` 存在性能瓶颈

**当前实现**:
```go
func (b *BTree) findLeafPageRef(ctx context.Context, key []byte) (*PageRef, []*PageInfo, error) {
    // Step 1: 第一次遍历
    path, _ := b.searchPath(ctx, key)

    // Step 2: 第二次遍历
    currentRef := b.rootRef.PageRef
    for i := 0; i < len(path)-1; i++ {
        ...
    }
}
```

**优化方案**:
```go
// 一次遍历同时收集 PageInfo 和 PageRef
func (b *BTree) searchPathWithRefs(ctx context.Context, key []byte) ([]*PageInfo, []*PageRef, error) {
    var path []*PageInfo
    var refs []*PageRef

    // 从 Root 开始
    rootInfo := b.rootRef.pInfo.Load()
    path = append(path, rootInfo)
    refs = append(refs, b.rootRef.PageRef)

    currentInfo := rootInfo
    currentRef := b.rootRef.PageRef

    for {
        // 懒加载页面
        currentPage, _ := b.getPageOrLoad(currentInfo)

        // 判断是否为叶子节点
        if leafPage, ok := currentPage.(*LeafPage); ok && leafPage != nil {
            break
        }

        // 处理内部节点
        internalPage, _ := currentPage.(*InternalPage)
        childRef := internalPage.FindChildRef(key)
        childInfo := childRef.GetPageInfo()

        // 同时收集
        path = append(path, childInfo)
        refs = append(refs, childRef)

        // 继续向下
        currentInfo = childInfo
        currentRef = childRef
    }

    return path, refs, nil
}
```

**预期收益**: ~5% 写入性能提升

**工作量**: 2-3 小时

---

### 2. P1: 添加 ErrRetry 重试上限

**问题**: 无限重试可能导致 CPU 100%

**优化方案**:
```go
const maxRetries = 3

func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    for attempt := 0; attempt < maxRetries; attempt++ {
        err := b.setWithLeafLock(ctx, key, value)
        if err != ErrRetry {
            return err
        }
        // 让出 CPU，避免忙等待
        runtime.Gosched()
    }
    return fmt.Errorf("max retries exceeded for key=%s", string(key))
}
```

**工作量**: 30 分钟

---

### 3. P1: 补充批量操作测试

**目标**: 验证 GetBatch/SetBatch 功能

**测试模板**:
```go
func TestGetBatch(t *testing.T) {
    tree := setupTestTree(t, 1000) // 1000 个键值对

    keys := make([][]byte, 100)
    for i := 0; i < 100; i++ {
        keys[i] = []byte(fmt.Sprintf("key-%04d", i))
    }

    values, err := tree.GetBatch(ctx, keys)
    require.NoError(t, err)
    require.Len(t, values, 100)

    for i, value := range values {
        expected := fmt.Sprintf("value-%04d", i)
        require.Equal(t, expected, string(value))
    }
}

func TestSetBatch(t *testing.T) {
    tree := setupEmptyTestTree(t)

    batch := make(map[string][]byte)
    for i := 0; i < 100; i++ {
        key := fmt.Sprintf("key-%04d", i)
        value := fmt.Sprintf("value-%04d", i)
        batch[key] = []byte(value)
    }

    err := tree.SetBatch(ctx, batch)
    require.NoError(t, err)

    // 验证所有键已写入
    for key := range batch {
        val, _ := tree.Get(ctx, []byte(key))
        require.NotNil(t, val)
    }
}
```

**工作量**: 2-3 小时

---

### 4. P1: 页面分裂并发测试

**目标**: 验证 handleSplitSync 并发安全

**测试模板**:
```go
func TestHandleSplitSync_Concurrent(t *testing.T) {
    tree := setupTestTree(t)

    const goroutines = 10
    const insertsPerGoroutine = 1000

    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < insertsPerGoroutine; j++ {
                key := fmt.Sprintf("key-%05d", id*insertsPerGoroutine+j)
                value := fmt.Sprintf("value-%d", id)
                err := tree.Set(ctx, []byte(key), []byte(value))
                require.NoError(t, err)
            }
        }(i)
    }
    wg.Wait()

    // 验证树完整性
    verifyTreeIntegrity(t, tree)
}
```

**工作量**: 1-2 小时

---

### 5. P1: 启用被禁用的测试

**search_path_test.go**:
```go
// 移除 t.Skip
func TestBTree_searchPath_Basic(t *testing.T) {
    // t.Skip("Skipping test - searchPath implementation pending")
    ...
}
```

**lazy_load_test.go**:
- 需要先实现 ChunkManager 集成
- 然后移除 `t.Skip`

**工作量**: 3-4 小时

---

## 中期优化 (1-2 月)

### 1. P2: 解决 TODO 注释

**高优先级 TODO**:
1. `btree_gc.go:168` - 实现自底向上写入逻辑
2. `internal_page.go:580` - 更新子节点的父引用
3. `btree.go:1524` - 实现真正的物化逻辑

**工作量**: 1-2 周

---

### 2. P2: Delta Chain 预分配优化

**当前**: `deltas: make([]Delta, 0)`
**优化**: `deltas: make([]Delta, 0, 8)`

**评估**:
- ✅ 减少 append 扩容开销
- ⚠️ 可能浪费内存（大部分场景 0-2 deltas）

**建议**: 先进行性能测试，再决定是否优化

**工作量**: 1 小时 + 性能测试

---

### 3. P2: 添加包注释

**位置**: `btree.go:1`

**内容**: (见问题汇总表 #8)

**工作量**: 30 分钟

---

## 长期规划 (3-6 月)

### 1. 异步分裂 (原计划 Phase 3)

**当前实现**: 同步分裂
**优化方向**: 异步分裂

**收益评估**:
- ✅ 分裂不阻塞写入路径
- ⚠️ 复杂度显著增加
- ⚠️ 需要 WAL 持久化支持

**建议**: 先进行性能测试，评估同步分裂是否成为瓶颈

**工作量**: 2-3 周

---

### 2. 持久化模式支持

**当前**: 纯内存模式已优化
**待完成**: 持久化模式优化

**关键问题**:
1. 持久化路径的并发安全
2. WAL 集成
3. 崩溃恢复

**工作量**: 3-4 周

---

### 3. 监控和可观测性

**建议添加的指标**:
1. CAS 失败率
2. 锁竞争统计
3. 分裂频率
4. 内存分配热点

**工作量**: 1-2 周

---

## 实施路线图

### Week 1: P0/P1 短期改进

| 任务 | 优先级 | 工作量 | 负责人 |
|------|--------|--------|--------|
| findLeafPageRef 双遍历优化 | P0 | 2-3h | - |
| ErrRetry 重试上限 | P1 | 0.5h | - |
| 批量操作测试 | P1 | 2-3h | - |

### Week 2: P1 并发测试

| 任务 | 优先级 | 工作量 | 负责人 |
|------|--------|--------|--------|
| 页面分裂并发测试 | P1 | 1-2h | - |
| 启用被禁用的测试 | P1 | 3-4h | - |
| 持久化模式并发测试 | P1 | 2h | - |

### Week 3-4: P2 中期优化

| 任务 | 优先级 | 工作量 | 负责人 |
|------|--------|--------|--------|
| 解决 TODO 注释 | P2 | 1-2w | - |
| Delta Chain 预分配评估 | P2 | 1h | - |
| 添加包注释 | P2 | 0.5h | - |

### Month 2-3: 长期规划

| 任务 | 优先级 | 工作量 | 负责人 |
|------|--------|--------|--------|
| 异步分裂评估 | - | 1w | - |
| 持久化模式支持 | - | 3-4w | - |
| 监控和可观测性 | - | 1-2w | - |

---

## 成功标准

### 短期 (2 周)

- [ ] findLeafPageRef 双遍历已优化
- [ ] ErrRetry 有重试上限
- [ ] 批量操作测试已添加
- [ ] 页面分裂并发测试已添加
- [ ] 测试覆盖率提升到 70%+

### 中期 (2 月)

- [ ] 所有 P1 TODO 已解决
- [ ] 测试覆盖率提升到 80%+
- [ ] 持久化模式并发测试通过

### 长期 (6 月)

- [ ] 异步分裂已评估（决定是否实施）
- [ ] 持久化模式完全支持
- [ ] 监控和可观测性已添加

---

## 风险和缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 性能优化引入 bug | 中 | 充分的单元测试 + race detector |
| 测试补充工作量超预期 | 中 | 分阶段实施，优先 P0/P1 |
| 异步分裂复杂度过高 | 高 | 先评估同步分裂性能，再决定 |

---

## 总结

**短期优先级**:
1. P0: findLeafPageRef 双遍历优化（性能关键）
2. P1: ErrRetry 重试上限（健壮性）
3. P1: 补充测试（质量保证）

**中期目标**: 解决技术债务，提升测试覆盖率到 80%

**长期规划**: 评估异步分裂和持久化模式支持
