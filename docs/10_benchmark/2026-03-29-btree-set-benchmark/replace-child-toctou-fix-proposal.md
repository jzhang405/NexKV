# ReplaceChild TOCTOU 修复方案

**日期**: 2026-03-31
**问题**: `FindChildIndex` → `ReplaceChild` 之间父页面可能被 COW 回收
**状态**: Proposal (Revised)
**前置依赖**: Phase 1 pageRefCache 失效已完成

---

## 1. 问题描述

### 1.1 竞争窗口

`leaf_lock_set.go:106-119` 中，非根节点 update 场景需要更新父节点的 child 指针：

```go
// T0: 获取 parentInfo（可能已过期）
parentRef := refs[len(refs)-2]
parentInfo := parentRef.GetPageInfo()              // 快照时刻的 PageInfo
parentPageID := uint32(parentInfo.GetPageID())

// T1: 在父页面中查找旧 child 的索引
childIndex := b.offheapAdapter.FindChildIndex(parentPageID, uint32(oldPageID))  // ① 读父页面

// T2-T5: 其他线程可能 COW 替换了父页面，旧 parentPageID 被 epoch 回收
//        parentPageID 指向的物理页面内容可能已经面目全非

// T6: 基于 stale 的 childIndex 操作已被回收的父页面
newParentPageID, err := b.offheapAdapter.ReplaceChild(                          // ② 写父页面
    model.PageID(parentPageID),
    childIndex,
    uint32(newPageID),
)
```

### 1.2 竞争时序

```
Thread A (setWithLeafLock)              Thread B (setWithLeafLock)
─────────────────────────               ─────────────────────────
T0: parentInfo.GetPageID() = P10
T1: FindChildIndex(P10, child=50) → 2
                                        T2: 同一 parentRef 上的 update
                                        T3: ReplaceChild(P10, idx, newChild)
                                        T4: CAS parentRef 成功
                                        T5: freeOldPage(P10) → epoch 队列
                                        T6: epoch 推进 → P10 回收到 freeList
                                        T7: P10 被 Alloc() 分配为新叶子页
T8: ReplaceChild(P10, 2, newChild)
    → 读取已被重用的 P10（内容完全不同）
    → GetCount 返回错误值
    → 可能 SIGSEGV 或数据损坏
```

### 1.3 为什么之前的修复没有暴露这个问题

- **Phase 1 之前**：搜索路径 98.9% 触发 ErrCircRef，操作在搜索阶段就失败，很少走到 ReplaceChild
- **Phase 1 之后**：ErrCircRef 降到 0%，更多操作通过了搜索路径，到达了 update 父节点阶段
- **局部重试后**：搜索路径成功率进一步提高，暴露了 ReplaceChild 的 TOCTOU

### 1.4 影响

| 表现 | 严重程度 |
|------|----------|
| SIGSEGV（访问已回收页面） | Critical |
| 数据损坏（写入错误页面位置） | Critical |
| childIndex = -1（count 不匹配） | Medium（会走错误路径） |

### 1.5 额外问题：CAS 失败时的页面泄漏

`leaf_lock_set.go:131-133` CAS 失败时，`newParentPageID` 对应的新页面不会被释放，造成内存/mmap 页面泄漏：

```go
if !parentRef.ReplacePage(parentInfo, newParentInfo) {
    // newParentPageID 已分配但未释放！
    return ErrRetry
}
```

---

## 2. 根因分析

### 2.1 核心问题：父页面缺少并发保护

叶子节点通过 `PageLock` 保护（leaf-level locking），但**父节点没有锁保护**。多个线程可以同时通过 `parentRef.GetPageInfo()` 拿到同一个 `parentPageID`，然后并发执行 `ReplaceChild`。

### 2.2 现有保护机制不足

| 机制 | 覆盖范围 | 为什么不够 |
|------|----------|-----------|
| `parentRef.ReplacePage(CAS)` | CAS 本身是原子的 | 但 CAS 在 `ReplaceChild` **之后**，此时旧页面可能已被回收 |
| `pageRefCache` 失效 | 页面回收时清理缓存 | 但 Thread A 在 T0 就已经拿到了 `parentPageID`，后续不再查缓存 |
| Epoch 延迟释放 | 延迟 2-3 个 epoch | 高并发下 2-3 个 epoch 可能很快就被推进 |

### 2.3 版本号校验方案无效（关键发现）

**索引页面的 version 始终为 0，版本号校验无法检测页面替换。**

```
MaterializeIndexPageFromBytes (materialize.go:67)
  → InitIndexPage(pageID, 0)            // version=0，硬编码

BulkInitLeafFromSource (page_layout.go:601)
  → InitLeafPage(dstPageID, srcHeader.version)  // 叶子页继承源 version ≠ 0

offheap/page_layout.go:435 IncrementVersion — 无生产代码调用

结论：索引页 version ≡ 0
      版本号校验 → expectedVersion=0 vs actualVersion=0 → 永远通过 → 无保护
```

这意味着原 proposal 的 `ReplaceChildV2` 三次版本校验方案**完全无效**。

### 2.4 ReplaceChild 本身的缺陷

`ReplaceChild` (`offheap_adapter.go:441-498`) 直接从 `pageID` 读取物理页面内容：

```go
func (a *OffHeapAdapter) ReplaceChild(pageID model.PageID, index int, newChildID uint32) (model.PageID, error) {
    count := a.pa.GetCount(uint32(pageID))  // 读取可能已被回收的页面
    // ... 遍历 keys/children，构建新页面 ...
}
```

没有页面类型检查，没有 count 合理性检查，没有"页面是否仍然有效"的检查。

---

## 3. 修复方案

### 3.1 方案 A：双层防御（推荐，已修正）

**原理**：Layer 1 在调用侧通过 PageRef 指针比较检测并发修改；Layer 2 在 ReplaceChild 内部通过页面类型和 count 检查防御回收重用。

#### Layer 1: PageRef 快照校验（leaf_lock_set.go）

在 ReplaceChild 调用前，比较 `parentRef.GetPageInfo()` 返回的指针是否仍等于快照时刻的 `parentInfo`：

```go
// TOCTOU 防御：检查 parentRef 是否仍指向我们的快照
if parentRef.GetPageInfo() != parentInfo {
    return ErrRetry
}
```

**可靠性分析**：

- `GetPageInfo()` 底层是 `atomic.Pointer[PageInfo].Load()` (`page_ref.go:51`)
- `ReplacePage()` 底层是 `atomic.Pointer[PageInfo].CompareAndSwap()` (`page_ref.go:56-59`)
- PageInfo 对象从不被原地修改（只能通过 CAS 替换为新对象）
- 指针比较在 Go 中是确定性的（同一对象同一地址）
- 如果另一个线程已完成 CAS，`GetPageInfo()` 返回新指针 ≠ 旧 `parentInfo`

**局限性**：检查和 ReplaceChild 之间仍有极小窗口（~ns 级），但对手线程需要在此窗口内完成
ReplaceChild + CAS + epoch 推进 + 页面回收 + 重分配，实际不可能。

#### Layer 2: 页面类型 + count 防御（ReplaceChild 内部）

在 `ReplaceChild` 函数开头增加防御检查：

```go
// TOCTOU 防御 Layer 2a: 页面类型检查
// 父页面被 epoch 回收重用为叶子页时，pageType 变为 PageTypeLeaf
if a.pa.IsLeaf(pid) {
    return 0, errpkg.BTreeParentPageRecycled(uint64(pageID))
}

count := a.pa.GetCount(pid)

// TOCTOU 防御 Layer 2b: count 合理性检查
// 被回收重用的页面 count=0（InitPage 重置为 0）
// maxInternalKeys=180 (constants.go:12)
if count == 0 || count > maxInternalKeys {
    return 0, errpkg.BTreeInvalidParentState(uint64(pageID), count)
}
```

**检测能力分析**：

| 回收重用场景 | Layer 1 | Layer 2a (IsLeaf) | Layer 2b (count) |
|-------------|---------|-------------------|------------------|
| 父页面被回收重用为**叶子页** | CAS 已更新 → 检测 ✅ | pageType=Leaf → 检测 ✅ | count=0 → 检测 ✅ |
| 父页面被回收重用为**索引页** | CAS 已更新 → 检测 ✅ | pageType=Index → 不检测 ❌ | count=0 → 检测 ✅ |
| 父页面仍在 epoch 队列（未回收） | CAS 已更新 → 检测 ✅ | 内容仍有效 → 不需要检测 ✅ | N/A |

**Layer 2b 捕获索引页重用场景**：新分配的索引页 `InitIndexPage` 会将 count 设为 0，
所以 `count == 0` 检查能捕获"父页面被回收重用为另一个索引页"的情况。

#### 额外修复: CAS 失败时释放新页面

```go
if !parentRef.ReplacePage(parentInfo, newParentInfo) {
    // CAS 失败，释放新分配的父页面（走 epoch 机制避免 use-after-free）
    b.offheapAdapter.freeOldPage(uint32(newParentPageID))
    return ErrRetry
}
```

`freeOldPage` (offheap_adapter.go:42) 是小写方法，但同属 `package btree`，可直接访问。

#### 改动量

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `pkg/errors/errors.go` | +2 错误变量, +2 错误函数 (~8 行) |
| 2 | `offheap_adapter.go:442` | ReplaceChild 开头 +IsLeaf +count 检查 (~10 行) |
| 3 | `leaf_lock_set.go:113` | +parentRef 快照校验 (~5 行) |
| 4 | `leaf_lock_set.go:132` | CAS 失败 +freeOldPage (~2 行) |

**总改动量**: ~25 行代码
**风险**: 低。仅增加防御性校验和资源释放，不改变正常路径。

### 3.2 方案 B：Optimistic Lock Coupling（中等改动，备选）

**原理**：在操作父节点时，给父页面的 PageRef 加一个乐观锁（版本号）。其他线程修改父节点时递增版本号，当前线程在操作前检测版本变化。

**改动点**：

1. `PageRef` 增加一个 `version` 字段（atomic.Uint64）
2. `ReplaceChild` 前后校验 parentRef 的 version
3. CAS 成功后递增 version

**改动量**：~60 行代码
**风险**：中。需要确保所有修改父节点的路径都递增 version。

### 3.3 方案 C：父节点 PageLock（大改动，长期方向）

**原理**：为内部节点也加 PageLock，类似叶子节点的锁机制。

**改动量**：200+ 行代码
**风险**：高。改变并发模型，可能引入死锁。
**建议**：当前不考虑，作为长期优化方向。

---

## 4. 推荐方案：方案 A（双层防御）

### 4.1 选择理由

1. **最小改动**：~25 行代码，不改变并发模型
2. **版本号校验无效后的最优替代**：不依赖版本号，使用指针比较 + 页面元数据检查
3. **兼容性**：不改变 ReplaceChild 签名，不破坏其他调用者
4. **可测试**：IsLeaf/count 检查触发明确错误，易于写测试

### 4.2 实施步骤

| 步骤 | 改动 | 文件 |
|------|------|------|
| 1 | 新增 `ErrBTreeParentPageRecycled`、`ErrBTreeInvalidParentState` 错误 | `pkg/errors/errors.go` |
| 2 | `ReplaceChild` 开头增加 IsLeaf + count 防御检查 | `offheap_adapter.go` |
| 3 | `leaf_lock_set.go` 添加 `parentRef.GetPageInfo() != parentInfo` 校验 | `leaf_lock_set.go` |
| 4 | CAS 失败时 `freeOldPage` 释放新页面 | `leaf_lock_set.go` |

### 4.3 与局部重试的依赖关系

```
ReplaceChild TOCTOU 修复（方案 A 双层防御）
        ↓ 完成后
搜索路径局部重试可安全启用
```

修复 ReplaceChild TOCTOU 后，局部重试才能安全地让更多操作通过搜索路径，因为到达 update 父节点阶段时不会 SIGSEGV。

### 4.4 范围外发现

| 发现 | 位置 | 优先级 |
|------|------|--------|
| `UpdateChildIndex` 也有相同 TOCTOU | `btree.go:1007` (deleteOffHeapWithMVCC) | P1 |
| `UpdateChildIndex` 缺少 IsLeaf/count 防御 | `offheap_adapter.go:1041` | P1 |
| delete 路径 CAS 失败也泄漏页面 | `btree.go:1021` | P1 |

---

## 5. 验证方案

### 5.1 正确性验证

```bash
# 运行完整测试套件
go test -v -race ./internal/infrastructure/storage/btree/...

# 重点测试：
# - TestSetWithLeafLock_Concurrent
# - TestSetWithLeafLock_ExtremeConcurrency
# - TestDebug6000KeysNoLoss
```

### 5.2 压力测试

```bash
# 8 线程压力测试
go run ./cmd/btree_perf_pprof -threads=8 -count=50000 -init=500

# 预期：
# - 无 SIGSEGV
# - Success 率应不低于修复前
# - ErrBTreeParentPageRecycled 和 ErrBTreeInvalidParentState 在高并发下按预期触发
```

### 5.3 新增单元测试

```go
func TestReplaceChild_RecycledLeafPage(t *testing.T) {
    // 模拟父页面被回收重用为叶子页
    // 1. 创建索引页面
    // 2. 回收并重用为叶子页
    // 3. ReplaceChild 应返回 ErrBTreeParentPageRecycled
}

func TestReplaceChild_InvalidCount(t *testing.T) {
    // 模拟父页面被回收重用后 count=0
    // 1. 创建索引页面
    // 2. 回收重用（count=0）
    // 3. ReplaceChild 应返回 ErrBTreeInvalidParentState
}

func TestSetWithLeafLock_ParentRefCASDetection(t *testing.T) {
    // 模拟另一个线程先完成 CAS
    // 1. 两个 goroutine 同时操作同一 parentRef
    // 2. 先完成的线程 CAS 成功
    // 3. 后到的线程应检测到 parentInfo 快照失效 → ErrRetry
}
```

---

## 6. 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 指针比较在极端情况下不敏感 | 极低 | Critical | Layer 2 IsLeaf + count 作为额外防御 |
| 页面被回收重用为索引页（IsLeaf=false） | 极低 | Medium | count==0 检查覆盖此场景 |
| CAS 失败路径 freeOldPage 走 epoch 延迟 | 低 | 轻微 | 延迟释放是设计预期，不是问题 |
| 性能影响（额外一次 atomic Load） | 低 | 轻微 | atomic.Load 是 ~ns 级操作 |
| 新页面泄漏（CAS 失败路径） | 当前已存在 | Medium | 修复方案中包含 freeOldPage |

---

## 7. 后续工作

| 优先级 | 任务 | 依赖 |
|--------|------|------|
| P0 | 修复 ReplaceChild TOCTOU（本方案） | 无 |
| P0 | 修复 CAS 失败时的页面泄漏（本方案一并修复） | 无 |
| P1 | 重新启用搜索路径局部重试 | P0 完成 |
| P1 | `UpdateChildIndex` 添加相同 TOCTOU 防御 | P0 完成后参照 |
| P1 | `deleteOffHeapWithMVCC` CAS 失败页面释放 | P0 完成后参照 |
| P2 | ReplaceChild CAS 失败后清理 pageRefCache | P0 完成 |
| P3 | 父节点 PageLock（方案 C） | 长期方向 |
