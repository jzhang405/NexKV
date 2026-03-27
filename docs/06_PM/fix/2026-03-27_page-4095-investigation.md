# Page 4095 僵尸引用调查报告

**日期**: 2026-03-27
**状态**: Phase 1.2 完成 - 追踪器工作正常，发现重大线索

---

## 重大发现

### 发现 1：追踪器工作正常

**测试结果** (`TestPageTrackerVerification`)：
- 直接 `Alloc()` 调用：✓ 被追踪（Total allocs: 0 → 1）
- `btree.Set()` 100 个 key：✓ 被追踪（Total allocs: 1 → 12）
- **追踪器通过验证**

**修复方法**：原来的测试只插入 10 个 key，没有触发页面分配。4KB 页面可以容纳约 40 个 entry。

### 发现 2：Page 4095 不是真实分配的页面

**失败日志数据**：
```
Page Tracking Stats:
  Total allocs: 2064
  Total frees: 123
  Active pages: 1941
  High pageID count: 0  ← 没有分配过 > 4000 的页面！

Page 4095 Report:
  Page 4095 not found in history  ← 追踪器从未捕获 Page 4095 的分配
```

### 发现 3：Page 4095 来自父节点的 IndexEntry.child 字段

**代码路径**：
1. `search_path.go:215` 调用 `b.offheapAdapter.SearchChild(currentPageID, key)`
2. `SearchChild()` → `pa.GetChild(uint32(pageID), childIdx)`
3. `GetChild()` 从 mmap 内存中读取 `IndexEntry.child` 字段
4. **这个 child 值是 4095！**

**结论**：Page 4095 是一个**僵尸引用**（stale reference），残留在父节点中。

---

## 僵尸引用产生的可能原因

### 假设 1：页面释放后未更新父节点 ⭐

**场景**：
1. 页面 A（pageID=4095）被分配
2. 父节点 P 的 IndexEntry.child = 4095
3. 页面 A 被释放（`Free(4095)`）
4. 父节点 P 的 IndexEntry.child **仍然是 4095**（未更新）
5. 后续搜索时读到 child=4095，但 4095 已被重新分配为其他页面
6. 循环引用检测触发

**验证方法**：
- 检查 `Free()` 调用是否更新父节点
- 检查分裂时是否正确更新父节点的子指针

### 假设 2：分裂时错误设置 child

**场景**：
1. 叶子节点分裂，创建新的内部节点
2. 内部节点的 `IndexEntry.child` 被错误设置为 4095（可能是未初始化的值）
3. 后续搜索时读到这个错误的 child

**验证方法**：
- 检查分裂代码中 `InsertIndexEntry()` 的调用
- 验证 child 参数是否正确传递

### 假设 3：并发竞态条件

**场景**：
1. Goroutine A 读取父节点的 child
2. Goroutine B 修改父节点（分裂或合并）
3. Goroutine A 读到陈旧的 child 值（4095）

**验证方法**：
- 使用 `go test -race` 检测数据竞争
- 检查父节点的锁保护

---

## 下一步调查

### 短期（1-2 天）

1. **检查 Free() 是否更新父节点**
   - 搜索 `Free()` 调用路径
   - 验证父节点更新逻辑

2. **检查分裂时的 child 设置**
   - 审查 `InsertIndexEntry()` 调用
   - 验证 child 参数来源

3. **运行 race detector**
   - `go test -race -run TestCollectFailureLogs`
   - 识别数据竞争

### 中期（3-5 天）

1. **增强僵尸引用检测**
   - 在 `SearchChild()` 中检测 child 是否在 freed 列表中
   - 在 `GetChild()` 中验证 child 的有效性

2. **修复僵尸引用问题**
   - 根据根因选择修复方案
   - 验证修复效果

---

## 提交记录

- `feat(offheap): 添加页面生命周期追踪器` (ded7825)
- `feat(btree): 添加失败日志收集测试` (fc7516b)
- `fix(test): 修正页面追踪验证测试` (待提交)

---

**报告生成时间**: 2026-03-27 08:56
**报告作者**: jzhang405
**下次更新**: 僵尸引用根因确认后
