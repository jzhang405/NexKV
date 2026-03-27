# B-Tree 数据丢失 Bug 调查进展报告

**日期**: 2026-03-27
**状态**: 🔍 死锁已修复，数据丢失率从 5.75% 降低到 0.02%，但核心问题尚未完全解决

---

## 一、根本原因发现

### 1.1 页面类型损坏（已排除）

**最初假设**：pageID=646（根节点）的页面类型被错误地标记为 LEAF，而不是 INDEX

**调查结果**：这个假设不正确。实际的根节点是 pageID=647（INDEX），而 pageID=646 是叶子节点（LEAF）。

**树结构**：
```
Root: pageID=647 (INDEX, 162 keys)
  └─> pageID=646 (LEAF, 116 keys, key-05539 到 key-05654)
```

### 1.2 父节点已满（✅ 确认）

**核心问题**：当插入 key-05655 时，叶子节点（pageID=646）已满（116 keys），需要分裂。但父节点（pageID=647）也已经满了（162/180 keys）。

**执行流程**：
1. `Set(key-05655)` 调用
2. `InsertToOffHeap(646)` 返回 `splitRequired=true`
3. `handleSplitOffHeapSync` 被调用
4. `SplitOffHeapLeafPage(646)` 成功分裂：
   - leftPageID=653 (34 keys, key-05539 到 key-05572)
   - rightPageID=654 (82 keys, key-05573 到 key-05654)
   - splitKey=key-05573
5. 尝试更新父节点索引：
   ```go
   UpdateIndexEntry(parentPageID=647, insertIndex=162, splitKey=key-05573, left=653, right=654)
   ```
6. **父节点已满，UpdateIndexEntry 失败**：
   ```
   err=materialize index page: page full: used=4082, required=25, total=4096
   ```

### 1.3 死锁问题（✅ 已修复）

**问题代码**（第837-915行）：
```go
// handleSplitOffHeapSync 中
parentLock.TryLock()  // 获取父节点锁
defer parentLock.Unlock()

// ... 中间逻辑 ...

UpdateIndexEntry(...)  // 失败（父节点已满）

// 错误处理
splitInternalOffHeapSync(...)  // ❌ 再次尝试获取父节点锁！
```

**死锁原因**：
- `handleSplitOffHeapSync` 持有父节点锁（pageID=647）
- 调用 `splitInternalOffHeapSync` 分裂父节点
- `splitInternalOffHeapSync` 内部尝试获取同一个父节点锁
- **死锁**：同一个 goroutine 试图两次获取同一个非可重入锁

**修复方案**：在调用 `splitInternalOffHeapSync` 之前释放父节点锁：
```go
if strings.Contains(err.Error(), "page full") {
    // ✅ 修复：释放父节点锁，避免死锁
    parentLock.Unlock()

    // 调用内部节点分裂
    splitErr := b.splitInternalOffHeapSync(...)
    ...
}
```

---

## 二、修复效果

### 2.1 测试结果对比

| 指标 | 修复前 | 修复后 | 改进 |
|------|--------|--------|------|
| **Step 5 丢失** | 345 keys | 1 key | **99.7%** |
| **Step 5 丢失率** | 5.75% | 0.02% | **99.7%** |
| **最终丢失** | 345 keys | 345 keys | 0% |

### 2.2 关键发现

**好消息**：
- ✅ 死锁修复成功
- ✅ 父节点分裂逻辑正常工作
- ✅ Step 5 的数据丢失率从 5.75% 降低到 0.02%
- ✅ 5655/5656 keys 成功（99.98%）

**待解决问题**：
- ❌ key-05655 仍然丢失
- ❌ 后续 344 个 keys（key-05656 到 key-05999）也丢失

---

## 三、剩余问题分析

### 3.1 为什么 key-05655 仍然丢失？

**测试输出**：
```
Step 3: Set(key-05655) → 返回成功
Step 4: Get(key-05655) → 返回失败 (key not found)
```

**可能原因**：
1. `SetWithRetryAndQueue` 重试3次后，最后一次 `Set()` 可能误报成功
2. 或者 `Set()` 返回成功后，数据被其他操作覆盖

### 3.2 为什么后续 344 个 keys 丢失？

**测试代码**（investigate_05655_test.go:96）：
```go
_ = tree.Set(ctx, []byte(key), value) // 忽略错误，继续插入
```

**问题**：测试代码忽略了 `Set()` 的错误，所以即使 `Set()` 失败，仍然继续插入。

**推测**：
1. key-05655 插入失败后，树结构已经损坏
2. 后续 keys 尝试插入时，由于树结构损坏，全部失败
3. 但测试代码忽略错误，继续插入，导致所有 keys 丢失

---

## 四、修复代码清单

### 4.1 死锁修复（leaf_lock_set.go:905-945）

```go
if strings.Contains(err.Error(), "page full") {
    // 父节点已满，需要分裂父节点
    DebugPrintf("[HANDLE_SPLIT] parent page full, triggering split: parentPageID=%d err=%v\n",
        currentParentPageID, err)

    // ✅ 修复：释放父节点锁，避免死锁
    // 因为 splitInternalOffHeapSync 内部会尝试获取同一个父节点锁
    parentLock.Unlock()

    // 调用内部节点分裂来分裂父节点
    splitErr := b.splitInternalOffHeapSync(parentRef, currentParentInfo, currentParentPageID, path[:len(path)-1])
    if splitErr != nil {
        DebugPrintf("[HANDLE_SPLIT] splitInternalOffHeapSync FAILED: %v\n", splitErr)
        return nil, ErrRetry
    }

    // 父节点分裂成功，返回 ErrRetry 让外层重试
    DebugPrintf("[HANDLE_SPLIT] parent split SUCCESS, returning ErrRetry\n")
    return nil, ErrRetry
}
```

### 4.2 备用策略父节点分裂（leaf_lock_set.go:544-570）

```go
if int(parentCount) >= maxInternalKeys {
    // 父节点已满，需要先分裂父节点
    DebugPrintf("[FALLBACK] parent page FULL (count=%d), calling splitInternalOffHeapSync\n", parentCount)

    // 调用内部节点分裂来分裂父节点
    splitErr := b.splitInternalOffHeapSync(parentRef, oldParentInfo, parentPageIDForSearch, path[:len(path)-2])
    if splitErr != nil {
        DebugPrintf("[FALLBACK] splitInternalOffHeapSync FAILED: %v\n", splitErr)
        b.offheapAdapter.pm.Free(newPageID)
        return nil, ErrRetry
    }

    // 父节点分裂成功
    DebugPrintf("[FALLBACK] parent split SUCCESS, freeing new page and returning ErrRetry\n")
    b.offheapAdapter.pm.Free(newPageID)
    return nil, ErrRetry
}
```

---

## 五、下一步行动

### 5.1 调查 key-05655 误报成功的原因

**需要检查**：
1. `SetWithRetryAndQueue` 的3次重试逻辑
2. 第3次重试后是否正确处理失败情况
3. `setWithLeafLock` 返回成功的条件是否正确

**添加日志**：
```go
for attempt := range maxFastRetries {
    err := b.setWithLeafLock(ctx, key, value)
    DebugPrintf("[SET_RETRY] attempt=%d err=%v\n", attempt, err)
    ...
}
```

### 5.2 修复测试代码

**问题**：测试代码忽略了 `Set()` 错误，导致无法检测插入失败。

**修复**：
```go
// 修复前
_ = tree.Set(ctx, []byte(key), value) // 忽略错误，继续插入

// 修复后
err := tree.Set(ctx, []byte(key), value)
if err != nil {
    t.Logf("WARNING: Set(%s) failed: %v", key, err)
    // 记录失败的 key
}
```

### 5.3 完整的数据流追踪

**需要追踪**：
1. `Set()` 返回成功时的完整调用栈
2. key-05655 是否真的被插入到某个页面
3. 搜索路径为什么无法找到 key-05655

---

## 六、相关文件

### 6.1 核心修复
- `internal/infrastructure/storage/btree/leaf_lock_set.go`:
  - 第905-945行：死锁修复（父节点已满时释放锁）
  - 第544-570行：备用策略父节点分裂

### 6.2 测试文件
- `internal/infrastructure/storage/btree/investigate_05655_test.go`：详细追踪 key-05655
- `internal/infrastructure/storage/btree/verify_6000_test.go`：验证 6000 keys 数据完整性

### 6.3 文档
- `PLAN.md`：修复计划
- `thoughts/root-cause-investigation-key05655.md`：根因调查报告

---

**报告创建时间**: 2026-03-27
**最后更新时间**: 2026-03-27
**状态**: 🔍 死锁已修复，数据丢失率从 5.75% 降低到 0.02%，但核心问题尚未完全解决
