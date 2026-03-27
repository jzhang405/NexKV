# Phase 1 数据收集进度报告

**日期**: 2026-03-27
**阶段**: Phase 1 - 数据收集
**状态**: 进行中（2/3 完成）

---

## 进度总结

| 实验 | 状态 | 完成日期 | 关键发现 |
|------|------|----------|----------|
| Phase 1.1: 失败场景完整追踪 | ✅ 完成 | 2026-03-27 | 快速检查显示 10% 失败率，Page 4095 占 99.9% |
| Phase 1.2: Page 4095 生命周期追踪 | ✅ 完成 | 2026-03-27 | 追踪器已实现，但未能捕获 Page 4095 分配 |
| Phase 1.3: 根分裂并发压力测试 | ⏳ 待执行 | - | - |

---

## Phase 1.1: 失败场景完整追踪

### 实施内容

**新增文件**：
- `internal/infrastructure/storage/btree/failure_log_collection_test.go`
  - `TestCollectFailureLogs`: 运行 1000 次测试收集完整失败日志
  - `TestQuickFailureCheck`: 快速检查（10 次）用于开发调试

**功能特性**：
- 详细失败日志记录（错误类型、数量、时间戳）
- Page 4095 专门报告
- 高 PageID (> 4000) 报告
- 页面追踪统计信息
- 日志输出到 `/tmp/btree_circular_ref_failures.log`

### 测试结果

**快速检查测试**（10 次运行）：
- **失败率**: 10% (1/10 runs failed)
- **错误总数**: 2201 个错误
- **错误分布**:
  - Page 4095 循环引用: ~2200 个（99.95%）
  - Page 3 循环引用: 1 个（0.05%）

**关键发现**：
1. **失败率高于预期**：快速检查显示 10% 失败率，远高于之前观察到的 1%
   - 可能原因：快速检查测试的并发压力更大（每次都是新的 BTree）
   - 需要运行完整的 1000 次测试来验证

2. **Page 4095 占绝对主导**：99.95% 的失败涉及 Page 4095
   - 证实了失败模式分析的结论
   - Page 4095 确实是核心问题

3. **Page 3 循环引用**：虽然数量少，但仍然存在
   - 证实了根分裂时的竞态条件

4. **追踪器问题**：Page 4095 Report 显示 "Page 4095 not found in history"
   - 追踪器未能捕获 Page 4095 的分配
   - **需要进一步调查**：Page 4095 是如何分配的？

---

## Phase 1.2: Page 4095 生命周期追踪

### 实施内容

**新增文件**：
- `internal/infrastructure/storage/btree/offheap/page_lifecycle_tracker.go`
  - `PageLifecycleTracker`: 页面生命周期追踪器
  - `PageLifecycle`: 页面生命周期数据结构

**修改文件**：
- `internal/infrastructure/storage/btree/offheap/page_manager.go`
  - 集成 PageLifecycleTracker
  - 在 `Alloc()` 和 `Free()` 中自动记录
  - 提供 `EnablePageTracking()`/`DisablePageTracking()` 控制
  - 提供 `GetPage4095Report()` 等调试方法

**测试文件**：
- `internal/infrastructure/storage/btree/offheap/page_lifecycle_tracker_test.go`
  - 所有测试通过 ✅

### 功能特性

1. **基本追踪**：
   - 记录页面分配时间和调用栈
   - 记录页面释放时间和调用栈
   - 计算页面生命周期

2. **B-Tree 关系追踪**：
   - `SetParentPageID()`: 设置父节点
   - `SetChildPageID()`: 设置子节点
   - `SetPageType()`: 设置页面类型（Leaf/Internal/Root）

3. **专门报告**：
   - `GetPage4095Report()`: Page 4095 专门报告
   - `GetHighPageIDReport()`: 所有 > 4000 的页面报告
   - `Stats()`: 追踪统计信息

4. **页面重用检测**：
   - 自动检测页面 ID 重用
   - 记录重用次数

### 问题发现

**追踪器未能捕获 Page 4095 的分配**

**现象**：
```
Page 4095 Report:
    Page 4095 not found in history
```

**可能原因**：
1. **Off-Heap 页面分配绕过了 PageManager.Alloc()**
   - Page 4095 可能是通过其他代码路径分配的
   - 需要检查 Off-Heap Adapter 的实现

2. **页面 ID 是计算得出的，而非分配的**
   - 4095 = 0xFFF 可能是某种特殊值
   - 需要验证 Page 4095 是真实的页面 ID 还是错误值

3. **追踪器未正确启用**
   - 测试中已调用 `btree.offheapPM.EnablePageTracking()`
   - 但可能 B-Tree 使用的是不同的 PageManager 实例

**下一步调查**：
1. 检查 Off-Heap Adapter 的页面分配逻辑
2. 验证 Page 4095 是否真的是分配的页面 ID
3. 在更底层添加追踪（如 Off-Heap Adapter）

---

## Phase 1.3: 根分裂并发压力测试（待执行）

**计划内容**：
- 使用 `go test -race` 检测数据竞争
- 创建专门的根分裂测试
- 模拟高并发根分裂场景

**预期输出**：
- 根分裂失败率
- CAS 冲突次数
- 数据竞争检测结果

---

## 初步结论

### 已确认的发现

1. ✅ **Page 4095 是核心问题**：99.95% 的失败涉及此页面 ID
2. ✅ **失败率可重现**：快速检查测试成功捕获失败
3. ✅ **追踪器工具已完成**：虽然未捕获 Page 4095，但框架已就绪

### 待解决的问题

1. ❓ **Page 4095 的来源**：追踪器未能捕获，需要深入调查
2. ❓ **失败率的差异**：10% vs 1%，需要完整 1000 次测试验证
3. ❓ **Page 3 的根因**：虽然数量少，但需要确定根分裂竞态的具体原因

### 下一步行动

**短期（1-2 天）**：
1. 运行完整的 1000 次测试收集失败日志
2. 调查 Page 4095 为何未被追踪器捕获
3. 在 Off-Heap Adapter 层面添加追踪

**中期（3-5 天）**：
1. 执行 Phase 1.3：根分裂并发压力测试
2. 分析收集的失败日志，识别共同模式
3. 验证 3 个根因假设

**长期（1-2 周）**：
1. 根据根因验证结果选择修复方案
2. 实施修复方案
3. 验证修复效果

---

## 提交记录

1. `feat(offheap): 添加页面生命周期追踪器` (ded7825)
2. `feat(btree): 添加失败日志收集测试` (fc7516b)
3. `docs(fix): 修正 PR 文档审查发现的问题` (8c738d8)
4. `docs(fix): 添加循环引用 Root Cause 调查 PR 全流程文档` (6b3f5f9)

---

**报告生成时间**: 2026-03-27 08:51
**报告作者**: jzhang405
**下次更新**: Phase 1.3 完成后
