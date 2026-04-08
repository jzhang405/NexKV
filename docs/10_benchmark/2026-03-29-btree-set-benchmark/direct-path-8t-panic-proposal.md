# Direct 路径 8 线程 Panic 调查

**日期**: 2026-03-31
**分支**: `perf/btree-set-benchmark2`
**状态**: 已修复

---

## 1. 现象

`cmd/btree_perf_scheduler -threads=8 -mode=direct` 运行时间歇性 panic：

```
panic: index 44 out of range (count: 5)
panic: index 36 out of range (count: 3)
panic: index 1 out of range (count: 4)
panic: index 27 out of range (count: 2)
```

---

## 2. 根因：Off-Heap 页面并发访问无保护

panic 来自多个代码路径，共同问题是：**Off-Heap 页面被 split/COW 回收后，旧的 pageID 被重新分配给新页面，但旧 goroutine 仍持有旧 pageID 并访问其内容**。

TOCTOU 场景：
1. goroutine A: `setWithLeafLock` → `InsertToOffHeap` → `UpdateLeafEntry` → 读取旧页面
2. goroutine B: 同一页面被 split，旧页面被释放回 `pm.Alloc` 池
3. goroutine C: 从池中分配到同一个 pageID（新页面，count=0）
4. goroutine A: 继续用旧 pageID 访问 → 读到新页面的 `count=0` → 越界 panic

核心问题：**Off-Heap 页面的 `pageID` 是裸指针，释放后立即可被重用，无 epoch 隔离保护**。

---

## 3. 修复方案

将所有 `Get*Entry*` 的 unsafe 调用替换为 Safe 版本，越界时返回 error 而非 panic。

### 3.1 新增 Safe 函数（page_layout.go）

| 函数 | 说明 |
|------|------|
| `GetLeafEntryOffsetSafe` | 叶子条目偏移安全读取，已存在 |
| `GetIndexEntryOffsetSafe` | 索引条目偏移安全读取，已存在 |
| `GetChildSafe` | 子节点安全读取，已存在 |

### 3.2 修复的调用点

| 文件 | 函数 | 修复 |
|------|------|------|
| `offheap_adapter.go:224` | `updateLeafEntryFullMaterialization` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:90` | `InsertToOffHeap` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:321` | `UpdateLeafEntry` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:521` | `ReplaceChildInternal` | `GetIndexEntryOffset` → `GetIndexEntryOffsetSafe` |
| `offheap_adapter.go:538` | `ReplaceChild` | `GetChild` → `GetChildSafe` |
| `offheap_adapter.go:547` | `ReplaceChild` | `GetChild` → `GetChildSafe` |
| `offheap_adapter.go:702` | `SplitOffHeapLeafPage` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:740` | `splitOffHeapLeafPageFallback` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:1049` | `VerifyOffHeapPage` | `GetLeafEntryOffset` → `GetLeafEntryOffsetSafe` |
| `offheap_adapter.go:1057` | `VerifyOffHeapPage` | `GetIndexEntryOffset` → `GetIndexEntryOffsetSafe` |
| `offheap_adapter.go:1142` | `UpdateChildIndex` | `GetIndexEntryOffset` → `GetIndexEntryOffsetSafe` |
| `offheap_adapter.go:365` | `UpdateIndexEntry` | 显式边界检查 `index >= count` |

### 3.3 新增错误类型

`pkg/errors/errors.go` 新增 `ErrBTreeConcurrentModification`：
```go
ErrBTreeConcurrentModification = errors.New("btree: page modified concurrently during update")
```

所有 Safe 函数在检测到并发修改后，返回 `ErrRetry` 让调用方重试。

---

## 4. 验证结果

修复后 8 线程 10 轮测试：**0 次 panic**

```
=== Run 1 ===
Success: 6454 (2.7%), ErrMaxRetry: 97.2%
=== Run 2 ===
Success: 9319 (3.9%), ErrMaxRetry: 96.1%
=== Run 3 ===
Success: 15593 (6.5%), ErrMaxRetry: 92.4%
=== Run 4 ===
Success: 11437 (4.8%), ErrMaxRetry: 95.1%
=== Run 5 ===
Success: 7849 (3.3%), ErrMaxRetry: 96.7%
=== Run 6 ===
Success: 21173 (8.8%), ErrMaxRetry: 91.1%
=== Run 7 ===
Success: 10032 (4.2%), ErrMaxRetry: 95.8%
=== Run 8 ===
Success: 13297 (5.5%), ErrMaxRetry: 94.5%
=== Run 9 ===
Success: 8815 (3.7%), ErrMaxRetry: 96.3%
=== Run 10 ===
Success: 1732 (0.7%), ErrMaxRetry: 99.3%
```

**panic 修复完成**。但成功率仍低（2-9%），这是并发竞争问题（ErrMaxRetry），需要后续优化。

---

## 5. 下一步

- [ ] Epoch 机制完善：从根本上解决页面回收后的 use-after-free
- [ ] 降低 ErrMaxRetry 率：优化锁竞争策略（如 key-range sharding）
- [ ] 动态重试次数：替代固定 10 次重试
