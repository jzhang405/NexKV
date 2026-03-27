# B-Tree 数据丢失 Bug 修复计划

**日期**: 2026-03-27
**状态**: 根本原因已找到，待实施修复

---

## 一、根本原因

**核心问题**: pageID=646（根节点）的页面类型被错误地标记为 LEAF，而不是 INDEX

**证据**:
```
[SEARCH_PATH key-05655] currentPageID=646 currentIsLeaf=true
[HANDLE_SPLIT] Page type check: leafPageID=646 isLeaf=true count=116
```

**影响**:
- `searchPath` 将 pageID=646（根节点）当作叶子节点返回
- `setWithLeafLock` 调用 `handleSplitOffHeapSync(leafRef=646, ...)`
- 但 646 实际上是 INDEX 节点，不是 LEAF 节点
- 导致分裂逻辑执行错误

---

## 二、页面类型损坏原因

**测试输出**:
```
[INIT_PAGE] pageID=646 INDEX -> LEAF (version=0)
```

**可能原因**:
1. 页面重用时，类型标记未正确更新
2. 某个代码路径错误地将 INDEX 页面初始化为 LEAF
3. COW 操作中页面类型丢失

**需要调查**:
- 哪里调用了 `InitPage(646, PageTypeLeaf, ...)`
- 页面分配和释放的流程

---

## 三、修复方案

### 方案A：修复页面类型标记（根本修复）

**目标**: 确保 pageID=646 的类型正确标记为 INDEX

**步骤**:
1. 搜索所有调用 `InitLeafPage` 或 `InitPage(..., PageTypeLeaf, ...)` 的地方
2. 检查是否有代码错误地将 INDEX 页面初始化为 LEAF
3. 修复页面初始化逻辑

**优点**: 彻底解决问题
**缺点**: 需要找到所有相关代码路径

### 方案B：在分裂处理中添加页面类型检查（临时修复）

**目标**: 在 `handleSplitOffHeapSync` 中检测页面类型并调用正确的分裂逻辑

**实现**:
```go
// 在 handleSplitOffHeapSync 开头添加
if !isLeaf {
    // 传入的是 INDEX 节点，应该调用 splitInternalOffHeapSync
    err := b.splitInternalOffHeapSync(leafRef, leafInfo, leafPageID, path)
    if err != nil {
        return nil, err
    }
    return nil, ErrRetry
}
```

**优点**: 立即修复数据丢失问题
**缺点**: 治标不治本，页面类型仍然损坏

### 方案C：在 `searchPath` 中添加页面类型验证（防御性修复）

**目标**: 防止将 INDEX 节点误认为是 LEAF 节点

**实现**:
```go
// 在 searchPathWithRefs 的叶子节点检测中添加验证
currentIsLeaf := b.offheapAdapter.IsLeaf(currentPageID)
if currentIsLeaf {
    // 防御性检查：验证页面内容是否真的是叶子节点
    count := b.offheapAdapter.pa.GetCount(uint32(currentPageID))
    if count > 0 && !b.offheapAdapter.pa.IsLeafNode(uint32(currentPageID)) {
        // 页面类型标记错误，实际是 INDEX 节点
        DebugPrintf("[SEARCH_PATH] WARNING: pageID=%d marked as LEAF but contains INDEX structure\n", currentPageID)
        currentIsLeaf = false
    }
    break
}
```

**优点**: 防御性强，能捕获所有页面类型错误
**缺点**: 需要实现 `IsLeafNode` 方法

---

## 四、建议的修复顺序

1. **立即修复（方案B）**：在 `handleSplitOffHeapSync` 中添加页面类型检查
2. **防御性修复（方案C）**：在 `searchPath` 中添加页面类型验证
3. **根本修复（方案A）**：找到并修复页面类型标记的根源

---

## 五、待调查问题

1. **谁将 pageID=646 从 INDEX 改为 LEAF？**
   - 搜索所有 `InitPage(646, ...)` 调用
   - 搜索所有 `InitLeafPage(646)` 调用
   - 检查页面重用逻辑

2. **为什么 `SplitOffHeapLeafPage` 没有被调用？**
   - 检查是否真的进入了叶子分裂逻辑（Step 1）
   - 添加更多调试日志追踪执行流程

3. **备用策略是否有问题？**
   - 检查备用策略是否正确处理父节点更新
   - 验证备用策略的数据插入逻辑

---

## 六、下一步行动

1. ✅ 已识别根本原因：页面类型损坏
2. ⏳ 待实施：方案B（添加页面类型检查）
3. ⏳ 待实施：方案C（防御性验证）
4. ⏳ 待调查：页面类型损坏的根源

---

**创建时间**: 2026-03-27
**最后更新**: 2026-03-27
**优先级**: P0（数据丢失问题）
