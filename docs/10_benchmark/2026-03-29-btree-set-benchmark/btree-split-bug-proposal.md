# BTree 分裂逻辑 Bug 分析与修复提案

**日期**: 2026-04-01
**Review**: Kimi @ 2026-04-01
**状态**: 根因已确定，待修复

---

## 1. 问题概述

### 1.1 测试现象

当树变大时（init=1500+），BTree 出现以下错误，即使单线程操作也会失败：

```bash
# 单线程测试
./bin/btree_perf_scheduler -mode=scheduler -threads=1 -count=2000 -init=2000

# 结果
Success: 1018 (50.9%)
ErrRetry: 982 (49.1%)
```

**关键发现**：这不是多线程并发问题，而是 BTree 分裂逻辑本身的 bug。

### 1.2 核心错误

```
[DEBUG] MaterializeIndexPageFromBytes: fixing self-loop child at index 20, pageID=6
[DEBUG] SearchChild TOCTOU2: pageID=6, childIdx=20, currentCount=62
[DEBUG] SearchChild failed: currentPageID=6, err=btree: cas failed, retry operation
[DEBUG] findLeafPageRef failed: btree: cas failed, retry operation
```

---

## 2. 根因分析

### 2.1 架构对比：为什么 Lealone 没有这个问题

| 特性 | Lealone | NexKV (当前) | 影响 |
|------|---------|--------------|------|
| **并发控制** | MVCC + 版本链 | Lock-free + CAS | NexKV 需要处理更多 TOCTOU 竞争条件 |
| **页面释放** | 引用计数（精确跟踪） | Epoch-based（延迟释放） | NexKV 页面被提前重用，导致自环 |
| **分裂原子性** | 事务保证（ACID） | 多步骤，可能中断 | NexKV CAS 失败时部分状态残留 |
| **空值处理** | 不允许 child=0 | 自环修复强制设为 0 | NexKV 0 值传播导致 extraChild=0 |
| **验证机制** | 严格约束检查 | 防御性编程（事后检查） | NexKV 问题发现晚，累积效应明显 |

**关键差异**：
- Lealone 使用 **引用计数** 确保页面无引用时才释放，避免了页面被提前重用导致的自环问题
- Lealone 的 **事务机制** 保证分裂操作原子性，不会出现中间状态
- NexKV 的 **epoch-based 释放** 有窗口期，页面可能被提前重用

### 2.2 Bug 的传播链

```
页面 A: count=2, extraChild=0 (由于自环修复时强制设置为0)
         ↓ 分裂
页面 B: count=1, extraChild=0 (继承自页面 A)
         ↓ 分裂
页面 C: count=0, extraChild=0 (继承自页面 B)
```

### 2.3 BulkInitIndexFromSource 中的自环修复逻辑

**文件**: `offheap/page_layout.go:715-730`

```go
// 逐条从源页面读取并插入目标页面
for i := startIdx; i < endIdx; i++ {
    entry := pa.GetIndexEntry(srcPageID, i)
    key := pa.GetKey(srcPageID, entry.keyOff, entry.keyLen)
    child, _ := DecodeChildWithVersion(entry.child)
    // 安全检查：修复自环的 child
    if child == srcPageID {
        child = 0 // 用 0 替换自环的 child  ← 问题！
    }
    // ...
}

// 设置 extraChild（N+1 child）
extraChildPageID, _ := DecodeChildWithVersion(extraChild)
if extraChildPageID == srcPageID {
    extraChild = 0 // 用 0 替换自环的 extraChild  ← 问题！
}
```

**问题**：当 `child=0`（表示空引用）时，这个 0 值会被传播到新页面。

### 2.4 MaterializeIndexPageFromBytes 的处理

**文件**: `materialize.go:64-97`

```go
// 安全检查：修复自环的 children
for i, child := range children {
    if child == pageID {
        fmt.Fprintf(os.Stderr, "[DEBUG] MaterializeIndexPageFromBytes: fixing self-loop child at index %d, pageID=%d\n", i, pageID)
        children[i] = 0 // 用 0 替换自环的 child
    }
}

// ...

// 设置 extraChild
if len(children) > len(keys) {
    lastChild := children[len(keys)]
    if lastChild != 0 {
        childVersion := m.pa.GetVersionSafe(lastChild)
        header := m.pa.GetHeader(pageID)
        header.extraChild = EncodeChildWithVersion(lastChild, childVersion)
    } else {
        header := m.pa.GetHeader(pageID)
        header.extraChild = 0  // 如果 lastChild=0，extraChild 被设置为 0 ← 问题！
    }
}
```

### 2.5 为什么 child=0 会导致问题

当 `extraChild=0` 时，`SearchChild` 中的逻辑：

```go
// SearchChild 中
if childID == 0 {
    if childIdx <= currentCount {
        // childID=0 但 index 在有效范围内，说明页面被并发修改
        return 0, false, errpkg.ErrBTreeRetry
    }
    return 0, found, nil
}
```

这会导致重试循环，最终可能导致操作失败。

### 2.6 Lealone 的解决方案

**Lealone 如何避免这个问题：**

1. **引用计数精确跟踪**
   ```java
   // Lealone 的页面引用管理
   class PageReference {
       Page page;
       int refCount;  // 精确引用计数
       
       void release() {
           if (--refCount == 0) {
               // 真正无引用时才释放
               page.recycle();
           }
       }
   }
   ```

2. **事务保证原子性**
   ```java
   // Lealone 的分裂操作在事务内完成
   Transaction tx = engine.getTransaction();
   try {
       Page left = page.splitLeft();
       Page right = page.splitRight();
       parent.updateChildPointer(page, left, right);
       tx.commit();  // 原子提交
   } catch (Exception e) {
       tx.rollback();  // 回滚所有变更
   }
   ```

3. **严格约束检查**
   ```java
   // Lealone 在创建索引页面时验证
   void validateIndexPage(Page page) {
       if (page.getExtraChild() == 0 && page.getCount() > 0) {
           throw new IllegalStateException("extraChild cannot be 0");
       }
   }
   ```

**NexKV 需要学习的：**
- 引入引用计数替代纯 epoch-based 释放
- 分裂操作需要事务性保证（或更完善的状态机）
- 前置验证优于后置防御

---

## 3. 测试数据

### 3.1 树大小 vs 成功率 (单线程, GOGC=400)

| init | 成功率 |
|------|--------|
| 500 | 100.0% |
| 1000 | 100.0% |
| 1500 | 66.2% |
| 2000 | 50.9% |
| 2500 | 19.4% |
| 3000 | 0.0% |

**结论**: 树越大，成功率越低，说明分裂 bug 有累积效应。

### 3.2 多线程测试 (init=500)

| 模式 | 线程 | 成功率 |
|------|------|--------|
| Scheduler | 1 | 100.0% |
| Scheduler | 2 | 99.8% |
| Scheduler | 4 | 92.4% |
| Scheduler | 8 | 42.4% |
| Direct | 1 | 100.0% |
| Direct | 2 | 99.8% |
| Direct | 4 | 86.0% |
| Direct | 8 | 45.5% |

### 3.3 Scheduler vs Direct 吞吐量对比 (GOGC=400)

| 模式 | 线程 | 吞吐量 | 延迟 |
|------|------|--------|------|
| Scheduler | 8 | ~17-48K ops/s | ~20-60μs |
| Direct | 8 | ~10-44K ops/s | ~7-22μs |

---

## 4. 修复方案

### 4.0 关键决策确认 (2026-04-01)

经过分析讨论，确认以下三项关键决策：

| 问题 | 决策 | 理由 |
|------|------|------|
| **rollbackParent** | ❌ 不回滚父节点 | 避免级联损坏；父节点可能被其他操作引用 |
| **CAS 失败回滚** | ✅ 完整回滚 | 释放所有孤儿页面（leftPageID, rightPageID, newParentPageID） |
| **MiniTransaction** | ❌ 不实施 | 方案 A+ 已足够简单，无需引入复杂的事务机制 |

**方案 A+ 定义**：增强的错误处理 + 简化的回滚逻辑
- CAS 失败时释放所有已分配页面
- 不尝试回滚父节点（避免级联损坏风险）
- 添加 TOCTOU 防护和 Safe 版本函数

### 4.1 方案 A+：增强回滚 + TOCTOU 防护（立即可做 ⭐）

#### 4.1.1 handleSplitOffHeapSync 增强回滚逻辑

**文件**: `leaf_lock_set.go:1240-1280`

```go
// handleSplitOffHeapSync 增强回滚逻辑
func (l *LeafLockSet) handleSplitOffHeapSync(
    b *BTree,
    parentPageID uint32,
    internalPageID uint32,
    key []byte,
) (uint32, uint32, error) {
    // ... 分配页面 ...

    leftPageID, rightPageID, newParentPageID, err := b.splitInternalOffHeapSync(...)
    if err != nil {
        // ✅ 方案 A+：完整回滚 - 释放所有孤儿页面
        if leftPageID != 0 {
            b.offheapAdapter.pm.Free(leftPageID)
        }
        if rightPageID != 0 {
            b.offheapAdapter.pm.Free(rightPageID)
        }
        if newParentPageID != 0 {
            b.offheapAdapter.pm.Free(newParentPageID)
        }
        return 0, 0, err
    }

    // ... 继续正常流程 ...
}
```

#### 4.1.2 splitInternalOffHeapSync 完整清理

**文件**: `offheap_adapter.go` 或 `btree_ops.go`

```go
func (b *BTree) splitInternalOffHeapSync(...) (left, right, newParent uint32, err error) {
    // ... 分配和初始化 ...

    // ✅ 使用 defer 确保所有失败路径都清理
    needsCleanup := true
    defer func() {
        if needsCleanup && err != nil {
            // 释放所有已分配的页面
            if leftPageID != 0 {
                b.offheapAdapter.pm.Free(leftPageID)
            }
            if rightPageID != 0 {
                b.offheapAdapter.pm.Free(rightPageID)
            }
            if newParentPageID != 0 {
                b.offheapAdapter.pm.Free(newParentPageID)
            }
        }
    }()

    // ... 执行 CAS ...

    // 成功，禁用清理
    needsCleanup = false
    return leftPageID, rightPageID, newParentPageID, nil
}
```

#### 4.1.3 TOCTOU 防护（已实施）

```go
// SearchChild 中添加 TOCTOU 检查
if childID == 0 && childIdx <= currentCount {
    return 0, false, errpkg.ErrBTreeRetry
}

// 使用 Safe 版本函数
encodedChild, err := b.offheapAdapter.pa.GetChildSafe(uint32(parentPageID), i)
if err != nil {
    return nil  // TOCTOU，跳过验证
}
```

#### 4.1.4 效果分析

| 改进项 | 效果 |
|--------|------|
| 完整回滚 | 消除孤儿页面，防止内存泄漏 |
| TOCTOU 防护 | 防止验证和读取之间的竞争条件 |
| Safe 版本函数 | 避免 stale reference panic |

### 4.2 方案 B：Epoch 延迟回收（短期优化）

#### 4.2.1 问题分析

当前 `AdvanceDelayedFreeList` 的问题：
- 页面从 `delayedFreeList` 移到 `freeList` 后立即可能被重用
- epoch 推进速度可能快于所有旧引用消失的速度

#### 4.2.2 修复方案

**方案 B: 版本号 + 延迟回收**（推荐）

在现有架构基础上，最小改动实现最大效果：

**Step 1: 修改 PageHeader**

```go
// page_layout.go:31
type PageHeader struct {
    version     uint64 // 已有
    prevPage    uint32 // 已有
    nextPage    uint32 // 已有
    extraChild  uint64 // 已有
    count       uint16 // 已有
    pageType    uint8  // 已有
    deleted     uint8  // 新增：标记为已删除（0=正常, 1=已删除）
    deleteEpoch uint64 // 新增：删除时的 epoch
    _pad        [7]byte // 对齐填充（使结构体大小为 48 字节）
}
// 当前大小：8+4+4+8+2+1+1+8+7 = 43 字节
// 加上尾部填充：48 字节（8 字节对齐）
```

**Step 2: 修改 Free 函数**

```go
// page_manager.go:158
func (pm *PageManager) Free(pageID uint32) error {
    if pageID >= pm.total {
        return errpkg.OffHeapInvalidPageID(int(pageID), int(pm.total))
    }
    
    header := pm.GetHeader(pageID)
    header.deleted = 1
    header.deleteEpoch = pm.currentEpoch.Load()
    header.version++  // 版本号增加，旧引用失效
    
    pm.delayedFreeList.Enqueue(pageID)
    pm.used.Add(^uint32(0))
    return nil
}
```

**Step 3: 修改 AdvanceDelayedFreeList 函数**

```go
// page_manager.go:170
func (pm *PageManager) AdvanceDelayedFreeList() int {
    moved := 0
    currentEpoch := pm.currentEpoch.Load()
    minEpochDiff := uint64(5)  // 至少 5 个 epoch 的延迟
    
    for {
        pageID, ok := pm.delayedFreeList.Dequeue()
        if !ok {
            break
        }
        
        header := pm.GetHeader(pageID)
        
        // 检查：是否过了足够的 epoch
        if currentEpoch - header.deleteEpoch < minEpochDiff {
            // 页面太新，放回队列尾部
            pm.delayedFreeList.Enqueue(pageID)
            break  // 队列已排序，越老越前面
        }
        
        pm.freeList.Enqueue(pageID)
        moved++
    }
    return moved
}
```

**Step 4: 修改 Alloc 函数（可选，进一步防护）**

```go
// page_manager.go:134
func (pm *PageManager) Alloc() (uint32, error) {
    // 路径 1：从 freeList 取已释放页面（lock-free）
    if pageID, ok := pm.freeList.Dequeue(); ok {
        // 额外检查：确保页面不是最近删除的
        header := pm.GetHeader(pageID)
        if header.deleted == 1 {
            // 页面被标记为删除，拒绝使用
            pm.freeList.Enqueue(pageID) // 放回去
            // 继续尝试分配新页面
            goto allocNew
        }
        pm.clearPage(pageID)
        pm.used.Add(1)
        pm.tracker.RecordAlloc(pageID)
        return pageID, nil
    }

allocNew:
    // 路径 2：fallback，nextPageID 递增
    pageID := pm.nextPageID.Load()
    if pageID >= pm.total {
        return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
    }
    pm.nextPageID.Add(1)
    pm.clearPage(pageID)
    pm.used.Add(1)
    pm.tracker.RecordAlloc(pageID)
    return pageID, nil
}
```

#### 4.1.3 效果分析

| 改进项 | 效果 |
|--------|------|
| `deleted` 标记 | 防止刚释放的页面被立即重用 |
| `deleteEpoch` 延迟 | 确保至少 5 个 epoch 的冷却期 |
| `version++` | 旧引用自动失效（版本不匹配） |
| Alloc 检查 | 双重保护，零容忍已删除页面 |

### 4.3 方案 C：防御性验证（补充）

#### 4.3.1 修复 BulkInitIndexFromSource

**问题**: 将自环 child 设置为 0 会导致 0 值传播

**建议修复**: 返回错误而不是静默修复

```go
// 安全检查：修复自环的 child
if child == srcPageID {
    // 记录错误但不替换为 0，让上层处理
    return 0, fmt.Errorf("self-loop child detected at index %d in page %d", i, srcPageID)
}
```

#### 4.3.2 添加防御性验证

在 `MaterializeIndexPageFromBytes` 返回前添加验证：

```go
// 在函数末尾添加
if header.count > 0 && header.extraChild == 0 {
    return 0, fmt.Errorf("invalid index page: extraChild=0, pageID=%d", pageID)
}
```

### 4.4 方案 D：单元测试

```go
// TestBulkInitIndexFromSource_SelfLoop 测试自环检测
func TestBulkInitIndexFromSource_SelfLoop(t *testing.T) {
    // 场景：源页面的 child 指向自己
    // 预期：返回错误而不是静默设置为 0
}

// TestMaterializeIndexPageFromBytes_ExtraChildZero 测试 extraChild=0 的情况
func TestMaterializeIndexPageFromBytes_ExtraChildZero(t *testing.T) {
    // 场景：children[len(keys)] = 0
    // 预期：返回错误而不是静默接受
}

// TestAdvanceDelayedFreeList_EpochDelay 测试 epoch 延迟
func TestAdvanceDelayedFreeList_EpochDelay(t *testing.T) {
    // 场景：页面刚被标记删除
    // 预期：不会被移到 freeList
}
```

### 4.5 空值处理改进（借鉴 Lealone）⭐独立问题

#### 4.5.1 问题分析

| 特性 | Lealone | NexKV 当前 |
|------|---------|-----------|
| **空值语义** | child=0 是错误 | 自环修复强制设为 0 |
| **处理方式** | 断言/异常，强制修复源头 | 防御性编程，下游掩盖 |
| **结果** | 问题在源头暴露 | 0 值传播，累积错误 |

**核心问题**：NexKV 将 child=0 视为"有效状态"并传播，导致错误累积。

#### 4.5.2 短期方案：防御性验证（Phase 3.5）

```go
// ValidatePage 页面验证
func (pa *PageAccessor) ValidatePage(pageID uint32) error {
    header := pa.GetHeader(pageID)
    count := header.count

    // 验证所有 child 不为 0
    for i := 0; i < int(count); i++ {
        child, _ := DecodeChildWithVersion(pa.GetIndexEntry(pageID, i).child)
        if child == 0 {
            return fmt.Errorf("page %d has child=0 at index %d", pageID, i)
        }
    }

    // 验证 extraChild
    extraChild, _ := DecodeChildWithVersion(header.extraChild)
    if extraChild == 0 && count > 0 {
        return fmt.Errorf("page %d has extraChild=0 with count=%d", pageID, count)
    }

    return nil
}
```

**应用点**：
- 分裂前验证源页面
- 页面初始化后验证
- 定期后台验证

#### 4.5.3 长期方案：禁止 child=0（Phase 6）

```go
// InsertIndexEntry 拒绝 child=0
func (pa *PageAccessor) InsertIndexEntry(...) error {
    if child == 0 {
        return fmt.Errorf("cannot insert child=0: invalid child")
    }
    // ...
}

// MaterializeIndexPageFromBytes 返回错误而不是修复
func (m *OffHeapMaterializer) MaterializeIndexPageFromBytes(...) (uint16, error) {
    for i, child := range children {
        if child == pageID {
            return 0, fmt.Errorf("self-loop: page %d, index %d", pageID, i)
        }
    }
    // ...
}
```

**实施步骤**：
1. 添加所有 child=0 产生点的错误返回
2. 修复所有测试和调用点
3. 移除下游的 child=0 处理逻辑

#### 4.5.4 实施时间线

```
立即（今天）→ 方案 3：添加 ValidatePage 和严格检查
    ↓
短期（本周）→ 方案 1：逐步禁止 child=0，修复所有自环源头
    ↓
长期（2周后）→ 完全移除 child=0 的处理逻辑
```

**核心原则**：像 Lealone 一样，**child=0 是错误，不是有效状态**，应该在源头修复而不是掩盖。

### 4.6 验证机制改进（借鉴 Lealone）⭐

#### 4.6.1 当前问题

| Lealone | NexKV 当前 |
|---------|-----------|
| **严格约束检查**（事前） | **防御性编程**（事后） |
| 插入时检查约束 | 读取时发现问题 |
| 问题在源头暴露 | 问题传播后才暴露 |
| 失败快速 | 错误累积 |

#### 4.6.2 改进方案

**核心思路**：从"事后防御"转向"事前约束"

**短期（Phase 4）：严格约束检查**

```go
// InsertIndexEntry 严格约束检查
func (pa *PageAccessor) InsertIndexEntry(
    pageID uint32,
    index int,
    key []byte,
    child uint32,
) error {
    // 检查 child 有效性（事前检查）
    if child == 0 {
        return fmt.Errorf("constraint violation: child cannot be 0")
    }

    // 检查页面容量（事前检查）
    if pa.GetCount(pageID) >= MaxKeysPerPage {
        return fmt.Errorf("constraint violation: page full")
    }

    // 检查 key 顺序（事前检查）
    if index > 0 {
        prevKey := pa.GetKeyAt(pageID, index-1)
        if bytes.Compare(prevKey, key) >= 0 {
            return fmt.Errorf("constraint violation: key order violated")
        }
    }

    // 执行插入
    return pa.doInsert(pageID, index, key, child)
}
```

**中期（Phase 5）：不变式检查**

```go
// PageInvariant 页面不变式
type PageInvariant func(pageID uint32, pa *PageAccessor) error

var DefaultInvariants = []PageInvariant{
    // 1. key 有序
    func(pageID uint32, pa *PageAccessor) error {
        count := pa.GetCount(pageID)
        for i := 1; i < int(count); i++ {
            prevKey := pa.GetKeyAt(pageID, i-1)
            currKey := pa.GetKeyAt(pageID, i)
            if bytes.Compare(prevKey, currKey) >= 0 {
                return fmt.Errorf("invariant violated: keys not sorted at index %d", i)
            }
        }
        return nil
    },

    // 2. child 不为 0
    func(pageID uint32, pa *PageAccessor) error {
        count := pa.GetCount(pageID)
        for i := 0; i < int(count); i++ {
            child, _ := DecodeChildWithVersion(pa.GetIndexEntry(pageID, i).child)
            if child == 0 {
                return fmt.Errorf("invariant violated: child=0 at index %d", i)
            }
        }
        return nil
    },
}

// CheckInvariants 检查所有不变式
func (pa *PageAccessor) CheckInvariants(pageID uint32) error {
    for _, inv := range DefaultInvariants {
        if err := inv(pageID, pa); err != nil {
            return err
        }
    }
    return nil
}
```

**长期（Phase 6）：分层验证**

```go
// 三层验证体系

// Layer 1: 操作前验证（Pre-condition）
func (op *SplitOperation) ValidatePreCondition() error {
    if err := pa.CheckInvariants(op.sourcePageID); err != nil {
        return fmt.Errorf("pre-condition failed: %w", err)
    }
    return nil
}

// Layer 2: 操作中验证（Invariant）
func (op *SplitOperation) ValidateInvariant() error {
    if err := pa.CheckInvariants(op.leftPageID); err != nil {
        return fmt.Errorf("invariant violated in left page: %w", err)
    }
    if err := pa.CheckInvariants(op.rightPageID); err != nil {
        return fmt.Errorf("invariant violated in right page: %w", err)
    }
    return nil
}

// Layer 3: 操作后验证（Post-condition）
func (op *SplitOperation) ValidatePostCondition() error {
    // 检查最终结果
    // 1. 所有 key 都被分配
    // 2. 没有丢失 key
    // 3. 父节点正确更新
    return nil
}
```

#### 4.6.3 实施时间线

```
Phase 4: 严格约束检查（2-3 天）
    ↓ 添加 InsertIndexEntry 事前检查
Phase 5: 不变式检查（3-5 天）
    ↓ 实现 CheckInvariants
Phase 6: 分层验证（1 周）
    ↓ 实现 Pre/Invariant/Post 三层验证
```

#### 4.6.4 核心改进

| 当前（防御性） | 改进后（约束性） |
|---------------|-----------------|
| 读取时发现问题 | 写入前检查约束 |
| 错误传播后处理 | 源头阻止错误 |
| 事后修复 | 事前预防 |

---

## 5. 风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 修复引入新 bug | 中 | 高 | 充分单元测试 |
| 性能回归 | 低 | 中 | 性能基准测试 |
| 自环检测报错导致分裂失败 | 高 | 中 | 改进错误处理，允许跳过自环条目 |
| 孤儿页面累积（CAS 失败时） | 低→无 | 高 | 方案 A+ 完整回滚已解决 |

---

## 6. 与 Lealone 架构对比

### 6.1 核心差异对比表

| 特性 | Lealone | NexKV (当前) | 影响分析 |
|------|---------|--------------|----------|
| **并发控制** | MVCC + 版本链 | Lock-free + CAS | NexKV 需要处理更多 TOCTOU 竞争条件，代码复杂度更高 |
| **页面释放** | 引用计数（精确跟踪） | Epoch-based（延迟释放） | NexKV 页面可能被提前重用，导致自环和僵尸引用 |
| **分裂原子性** | 事务保证（ACID） | 多步骤，可能中断 | NexKV CAS 失败时部分状态残留，需要手动回滚 |
| **空值处理** | 不允许 child=0 | 自环修复强制设为 0 | NexKV 0 值传播导致 extraChild=0，引发连锁错误 |
| **验证机制** | 严格约束检查（前置） | 防御性编程（事后） | NexKV 问题发现晚，累积效应明显，树越大错误越多 |
| **错误处理** | 事务回滚 | 重试机制 | NexKV 重试成本高，成功率随树大小下降 |

### 6.2 Lealone 的核心优势

1. **引用计数精确跟踪**
   - 每个页面有精确的引用计数
   - 无引用时才真正释放，避免提前重用
   - 从根本上避免自环问题

2. **事务保证原子性**
   - 分裂操作在事务内完成
   - 要么全部成功，要么全部回滚
   - 不会出现中间状态

3. **严格的前置验证**
   - 创建页面前验证所有参数
   - extraChild=0 在创建时就被拒绝
   - 问题早发现，不传播

### 6.3 NexKV 的改进方向

基于 Lealone 的经验，NexKV 需要：

1. **短期**：添加前置验证，拒绝 extraChild=0
2. **中期**：改进分裂状态机，确保操作完整性
3. **长期**：引入引用计数或改进 epoch-based 释放机制

### 6.4 Lealone 详细架构分析 ⭐

#### 6.4.1 页面类型严格分离

Lealone 有明确的页面类型系统：

```java
// Lealone 页面类型
enum PageType {
    INTERNAL_PAGE,  // 内部节点：存储 key + child 指针
    LEAF_PAGE,      // 叶子节点：存储 key + value
}

// InternalPage 严格约束
// - 只存储 key 和 child 指针
// - extraChild 指向第 N+1 个子节点
// - key[i] 的左子是 child[i]，右子是 child[i+1]
// - 所有 child 必须有效（不能为 0）

// LeafPage 严格约束
// - 只存储 key 和 value
// - 不存储 child
// - 相邻叶子节点通过双向链表连接
```

**NexKV 问题**：当前文档没有明确强调页面类型分离的重要性。

#### 6.4.2 MVCC 版本链设计

Lealone 的 MVCC 是核心架构：

```java
// Lealone 版本链
class VersionChain {
    BasePage base;           // 最老版本的基础页面
    Delta latestDelta;       // 最新增量
    long version;            // 当前版本号
}

// Delta 增量结构
class Delta {
    long txnId;              // 事务 ID
    long version;             // 版本号
    Map<String, byte[]> changes;  // key -> value 变更
    Delta prevDelta;          // 前一个增量
    long commitTime;          // 提交时间
    AtomicInteger refCount;   // GC 引用计数
}
```

**关键设计**：
1. **读不阻塞写**：快照读 + 版本链
2. **写不阻塞读**：增量写入，不修改 base page
3. **版本回收**：基于 refCount 的 GC

#### 6.4.3 Root CAS 优化

Lealone 的 Root CAS 概率极低：

| 操作类型 | Root CAS 概率 | 说明 |
|---------|--------------|------|
| 正常插入 | 0% | 只在叶子节点操作 |
| Leaf 分裂 | ~0.625% | 100万次操作约 6250 次 |
| Root 分裂 | ~0.001% | 100万次操作约 12 次 |

**原因**：
1. **单写线程模式**：避免并发写入冲突
2. **Leaf-level Locking**：只在叶子节点加锁
3. **层级分离**：分裂传播到 Root 的概率很低

#### 6.4.4 Delta Chain 优化

Lealone 使用 Delta Chain 减少内存分配：

```java
// 读操作：沿着版本链回溯
V read(K key, long readVersion) {
    for (Delta d = latestDelta; d != null; d = d.prevDelta) {
        if (d.version <= readVersion) {
            if (d.changes.containsKey(key)) {
                return d.changes.get(key);  // 找到可见版本
            }
        }
    }
    return basePage.read(key);  // 读取 base page
}

// 写操作：创建新 delta
void write(K key, V value, Transaction tx) {
    Delta delta = new Delta();
    delta.version = tx.commitVersion;
    delta.txnId = tx.id;
    delta.changes.put(key, value);
    delta.prevDelta = latestDelta;

    // CAS 安装新 delta
    if (!latestDelta.compareAndSet(oldDelta, delta)) {
        // 冲突，重试
    }
}
```

**优势**：
1. **减少内存分配**：只分配 delta，不克隆整个页面
2. **写放大优化**：多个小修改合并到一次 delta
3. **GC 友好**：旧版本可以被批量回收

#### 6.4.5 严格平衡保证

Lealone BTree 维护严格平衡：

```java
// BTree 平衡约束
class BTreeConstraints {
    static final int MIN_KEYS = 32;   // 最小 key 数
    static final int MAX_KEYS = 64;   // 最大 key 数

    // 分裂触发条件
    boolean shouldSplit(Page page) {
        return page.getKeyCount() >= MAX_KEYS;
    }

    // 合并触发条件
    boolean shouldMerge(Page page) {
        return page.getKeyCount() < MIN_KEYS;
    }

    // 分裂时保持平衡
    Page[] split(Page page) {
        int mid = page.getKeyCount() / 2;
        Page left = page.leftHalf(mid);
        Page right = page.rightHalf(mid);
        // left 和 right 都恰好是 mid 个 key
        return new Page[]{left, right};
    }
}
```

**关键保证**：
1. **所有叶子节点在同一层**
2. **节点至少半满**（除 Root）
3. **分裂/合并保持平衡**

---

### 6.5 NexKV vs Lealone 性能差距分析 ⭐

#### 6.5.1 性能数据对比

| 指标 | NexKV | Lealone | 差距 |
|------|-------|---------|------|
| 单线程 | 574K ops/sec | 1.01M ops/sec | 1.76x |
| 8 线程 | 548K ops/sec | 3.68M ops/sec | **6.71x** |
| 扩展比 | 0.95x | 3.6x | 3.79x |
| 扩展效率 | 11.9% | 45% | - |

**关键发现**：NexKV 的 8 线程性能反而低于单线程，说明并发控制存在严重问题。

#### 6.5.2 差距根因分析

| 根因 | NexKV 问题 | Lealone 优势 |
|------|-----------|-------------|
| **并发模式** | 全局锁竞争 | 单写线程 + Leaf Locking |
| **CAS 频率** | Root CAS 高频 | Leaf CAS 为主 |
| **内存分配** | 每操作克隆页面 | Delta Chain 优化 |
| **版本管理** | 无 MVCC | 版本链 + 快照读 |

#### 6.5.3 Root CAS vs Leaf CAS

```
NexKV 当前（Root CAS）：
┌─────────────────────────────────────────┐
│                 Root                      │
│            CAS(Root)                      │
│         /      |      \                   │
│      CAS    CAS     CAS                  │ ← 每次分裂都触Root CAS
│     /  \    / \    /  \                  │
│   ...  ... ... ...  ...                  │
└─────────────────────────────────────────┘

Lealone（Leaf CAS + 单写线程）：
┌─────────────────────────────────────────┐
│                 Root                      │
│              (固定)                       │
│         /      |      \                   │
│      CAS    (无)   CAS                   │ ← 仅 Root 分裂时 CAS
│     /  \          /  \                  │
│   CAS   ...     ...   CAS               │ ← Leaf 分裂是本地操作
│  (本地)                  (本地)          │
└─────────────────────────────────────────┘
```

#### 6.5.4 性能优化路线图

> **注意**：以下 Phase 编号与 Section 7 不同，这是性能优化阶段的编号体系。

```
当前 NexKV
    │
    ├── Phase 1-3: Bug 修复（成功率 50% → 95%+）
    │
    ▼
Phase 7: Leaf-level Locking（目标：扩展比 1x → 3x）
    │  - 8 线程：548K → 1.5M ops/sec（2.7x）
    │
    ▼
Phase 8: Delta Chain 优化（目标：内存分配 -50%）
    │  - 每操作分配：大幅减少
    │
    ▼
Phase 9: 单写线程模式（目标：扩展比 3x → 3.6x）
        - 8 线程：1.5M → 3.68M ops/sec（匹配 Lealone）
```

---

## 7. 实施计划

> **Phase 编号说明**：本 section 使用 Bug 修复 + 架构优化的编号体系（Phase 1-5）。Section 6.5.4 性能路线图使用独立的优化阶段编号（Phase 7-9）。两者编号不同但对应关系如下：
> - Bug 修复：Phase 1-5（Section 7）
> - 性能优化：Phase 7-9（对应 Section 6.5.4），其中 Leaf-level Locking 是 Phase 7

### Phase 1: Epoch 延迟回收 (1天) ⭐推荐优先实施
- [ ] 修改 PageHeader 添加 `deleted` 和 `deleteEpoch` 字段
- [ ] 修改 `Free` 函数设置 `deleted` 标记和 `deleteEpoch`
- [ ] 修改 `AdvanceDelayedFreeList` 实现 epoch 延迟逻辑
- [ ] （可选）修改 `Alloc` 添加 `deleted` 检查
- [ ] 添加单元测试验证 epoch 延迟行为
- [ ] 运行基准测试验证效果

### Phase 2: 分裂回滚修复 - 方案 A+ (1-2天)
- [ ] 实现 handleSplitOffHeapSync 增强回滚逻辑
- [ ] 实现 splitInternalOffHeapSync 完整清理（defer 模式）
- [ ] 验证 TOCTOU 防护已正确实施
- [ ] 验证 Safe 版本函数使用正确
- [ ] 添加单元测试验证回滚行为
- [ ] 运行基准测试验证效果

### Phase 3: 防御性验证 (1天)
- [ ] 修复 BulkInitIndexFromSource 自环检测逻辑
- [ ] 添加 extraChild=0 验证
- [ ] 添加单元测试

### Phase 4: 稳定性改进 (2-3天)
- [ ] 实现分裂状态机
- [ ] 改进错误处理和日志
- [ ] 添加更多边界测试

### Phase 3.5: 空值处理改进 (1-2天) ⭐独立问题
- [ ] 实施方案 3：添加 ValidatePage 验证函数
- [ ] 在分裂前验证源页面
- [ ] 在页面初始化后验证
- [ ] 添加单元测试验证空值检测
- [ ] （可选）实施方案 1：逐步禁止 child=0

### Phase 5: 架构优化 - 完整 MVCC (8周)
- [ ] 调研 Lealone MVCC 架构
- [ ] 设计 NexKV MVCC 方案
- [ ] 实现 VersionManager
- [ ] 实现事务机制
- [ ] 性能调优

### Phase 6: 验证机制改进 (2-3天)
- [ ] 实施方案 4.6：严格约束检查（InsertIndexEntry 事前检查）
- [ ] 实施方案 4.6：不变式检查（CheckInvariants）
- [ ] 实施方案 4.6：分层验证（Pre/Invariant/Post）
- [ ] 添加单元测试验证

### Phase 8: Delta Chain 优化 (1-2周) ⭐性能关键
- [ ] 调研 Lealone Delta Chain 设计
- [ ] 设计 NexKV Delta Chain 方案
- [ ] 实现 Delta 写入路径
- [ ] 实现 Delta 合并和 GC
- [ ] 性能测试对比

### Phase 9: 单写线程模式 (2-3周) ⭐性能关键
- [ ] 调研 Lealone 单写线程模式
- [ ] 设计 NexKV 单写线程方案
- [ ] 实现写线程池
- [ ] 实现任务分配机制
- [ ] 性能测试对比

### Phase 7: Leaf-level Locking 优化 (2-3周) ⭐性能关键（已废弃，内容合并到 Phase 6-9）

> **已废弃**：Leaf-level Locking 优化已拆分为 Phase 6-9，更清晰。

#### 7.1 目标

将 NexKV 从当前的 **Root CAS** 模式改为 **Leaf-level Locking** 模式，大幅提升并发性能。

#### 7.2 当前问题

NexKV 当前使用 Root CAS，每次分裂都需要在 Root 节点进行 CAS：

```go
// 当前 NexKV：每次分裂都可能在 Root 进行 CAS
func (b *BTree) Insert(key, value []byte) error {
    // 1. 从 Root 开始 search
    // 2. 找到目标 Leaf
    // 3. 获取 Leaf 锁
    // 4. 如果 Leaf 需要分裂：
    //    - 分裂向上传播
    //    - 可能在 Root 进行 CAS ← 问题！
    // 5. 释放锁
}
```

**问题**：
- 所有并发操作竞争同一个 Root 锁
- 分裂传播路径长，失败概率高
- 8 线程反而比单线程慢（扩展比 0.95x）

#### 7.3 Lealone 的 Leaf-level Locking

```java
// Lealone：只在 Leaf 节点加锁
class BTree {
    void insert(Transaction tx, byte[] key, byte[] value) {
        // 1. 从 Root 开始 search（不上锁）
        Page leaf = findLeaf(key);

        // 2. 只在目标 Leaf 获取锁
        leaf.lock();

        try {
            // 3. 在 Leaf 内操作
            if (leaf.isFull()) {
                leaf.split();  // 分裂只在 Leaf 层进行
            }
            leaf.insert(key, value);
        } finally {
            leaf.unlock();
        }
    }
}
```

**优势**：
- 99.4% 操作只在 Leaf 层
- 只有 0.6% 分裂需要向上传播
- 几乎无锁竞争

#### 7.4 实现方案

**方案 A：保守方案（推荐）**

```go
// 1. 分离 Leaf Lock 和 Internal Lock
type LeafLockSet struct {
    leafMu   sync.RWMutex  // Leaf 锁
    internal sync.RWMutex  // Internal 锁（仅分裂时）
}

// 2. 只在 Leaf 层获取锁
func (b *BTree) Insert(key, value []byte) error {
    // 搜索路径（不上锁）
    path := b.searchPath(key)

    // 只锁定目标 Leaf
    leafPage := path[len(path)-1]
    leafLockSet.lock(leafPage.pageID)

    defer leafLockSet.unlock(leafPage.pageID)

    // Leaf 内操作
    if leafPage.isFull() {
        // 分裂只在 Leaf 层进行
        return b.splitLeaf(leafPage, path)
    }

    return b.insertIntoLeaf(leafPage, key, value)
}

// 3. 分裂传播优化
func (b *BTree) splitLeaf(leafPage *Page, path []*PageInfo) error {
    // 分配新的 Leaf
    newLeaf := b.allocLeaf()

    // 分裂 Leaf 内容
    mid := leafPage.getKeyCount() / 2
    b.moveKeys(leafPage, mid, newLeaf)

    // 只在父节点更新（不上全局锁）
    parent := path[len(path)-2]
    parent.lock()  // 短暂锁住父节点

    defer parent.unlock()

    // 更新父节点
    return b.updateParent(parent, leafPage, newLeaf)
}
```

**方案 B：完整实现**

参考 Lealone 的设计，实现完整的 Leaf-level Locking：
- 分离 Leaf 和 Internal 操作
- 移除 Root 全局锁
- 实现路径压缩（Path Compaction）
- 实现写线程池

#### 7.5 预期效果

| 指标 | 当前 | Phase 7 目标 | 提升 |
|------|------|-------------|------|
| 8 线程吞吐 | 548K ops/sec | 1.5M ops/sec | **2.7x** |
| 扩展比 | 0.95x | 3x | **3.2x** |
| 扩展效率 | 11.9% | 37.5% | **3.2x** |

#### 7.6 实施步骤

```
Week 1: 基础架构
- [ ] 分离 LeafLockSet 和 InternalLockSet
- [ ] 实现 leaf-only lock 模式
- [ ] 添加单元测试验证

Week 2: 分裂优化
- [ ] 实现 leaf-level split
- [ ] 优化父节点更新（减少锁持有时间）
- [ ] 集成测试

Week 3: 性能调优
- [ ] 基准测试对比
- [ ] 瓶颈分析
- [ ] 针对性优化
```

---

## 8. 验证计划

### 测试用例

```bash
# 单线程大树的正确性测试
./bin/btree_perf_scheduler -mode=scheduler -threads=1 -count=5000 -init=5000

# 多线程稳定性测试
./bin/btree_perf_scheduler -mode=scheduler -threads=8 -count=5000 -init=5000

# 对比测试
./bin/btree_perf_scheduler -mode=direct -threads=8 -count=5000 -init=5000
```

**预期结果**: 成功率 > 95%

---

## 9. 相关文件

### 9.1 方案 B 涉及的文件

| 文件 | 关键函数/结构 | 修改内容 |
|------|----------------|----------|
| `offheap/page_layout.go:31` | PageHeader | 添加 `deleted`, `deleteEpoch` 字段 |
| `offheap/page_manager.go:134` | Alloc | 添加 `deleted` 检查 |
| `offheap/page_manager.go:158` | Free | 设置 `deleted` 标记和 `deleteEpoch` |
| `offheap/page_manager.go:170` | AdvanceDelayedFreeList | 实现 epoch 延迟逻辑 |

### 9.2 防御性验证涉及的文件

| 文件 | 关键函数 | 问题 |
|------|----------|------|
| `offheap/page_layout.go:715-730` | BulkInitIndexFromSource | 返回错误而不是设为 0 |
| `materialize.go:64-97` | MaterializeIndexPageFromBytes | extraChild=0 验证 |
| `offheap/page_layout.go:283-323` | InsertIndexEntry | 添加验证 |
| `leaf_lock_set.go:304` | handleSplitOffHeapSync | 分裂状态机（需验证行号） |
| `leaf_lock_set.go:1108` | splitInternalOffHeapSync | 完整回滚逻辑（需验证行号） |

---

## 10. 结论

BTree 分裂逻辑的 bug 主要体现在：

1. **自环 child 强制设为 0**: `BulkInitIndexFromSource` 将自环 child 替换为 0，导致 0 值传播
2. **extraChild=0 验证缺失**: 没有对 extraChild=0 进行检测和报警
3. **分裂状态机缺失**: 没有事务性保证

修复后预期成功率从 ~50% 提升到 95%+。

---

## 11. 关键决策确认记录

### 决策 1：父节点回滚策略

**问题**：CAS 失败时是否回滚父节点？

**决策**：❌ 不回滚父节点

**理由**：
- 父节点可能被其他并发操作引用
- 回滚父节点可能引入新的竞争条件
- 即使回滚也可能因为其他线程的修改而失败
- 不回滚的风险可控（孤儿页面，但不影响数据正确性）

### 决策 2：CAS 失败回滚范围

**问题**：CAS 失败时释放哪些页面？

**决策**：✅ 完整回滚（释放所有孤儿页面）

**理由**：
- 必须释放 leftPageID 和 rightPageID（分裂产生的新页面）
- 必须释放 newParentPageID（已安装到版本链的父节点）
- 释放所有孤儿页面，防止内存泄漏
- 使用 defer 模式确保所有失败路径都正确清理

### 决策 3：MiniTransaction 必要性

**问题**：是否需要引入 MiniTransaction 机制？

**决策**：❌ 不实施 MiniTransaction

**理由**：
- 方案 A+（增强回滚 + TOCTOU 防护）已足够简单有效
- MiniTransaction 引入复杂性（需要修改函数签名、引入新类型）
- 当前问题的根因是 epoch 延迟和回滚不完整，不是缺乏事务机制
- 未来如果需要更复杂的事务支持，可以在 MVCC 阶段统一考虑

---

## Appendix A: Kimi Review 要点

- 根因分析已确认：`BulkInitIndexFromSource` 是主要问题点
- 建议添加单元测试验证修复
- 建议添加风险评估
- 建议补充与 Lealone 对比

## Appendix B: 方案 B 审核 (2026-04-01) ✅ 已确认

### 核心问题分析

**问**：引用计数在 NexKV 可行吗？

**答**：**不可行**。NexKV 使用 Off-Heap mmap，child 引用是编码的 uint64，不是 Go 指针。Go 无法自动追踪这些引用。

### 方案 B 审核结果

| 项目 | 评估 |
|------|------|
| **PageHeader 修改** | 可行。当前 `_pad [5]byte` 可复用，但需要改结构 |
| **Alloc 检查时机** | 建议在 `AdvanceDelayedFreeList` 中延迟，不是在 `Alloc` 中 |
| **版本号增加** | 必要。当前 `Free` 后没有增加 `version` |
| **epoch 延迟** | 必要。防止页面过早被重用 |

### 方案 B 关键改进

1. **deleted 标记**：防止刚释放的页面被立即重用
2. **deleteEpoch 延迟**：确保至少 5 个 epoch 的冷却期
3. **version++**：旧引用自动失效
4. **Alloc 双重检查**：零容忍已删除页面

### 方案 B 与方案 A+ 的关系

- **方案 A+（Phase 2）**：处理 CAS 失败时的孤儿页面问题
- **方案 B（Phase 1）**：处理页面过早被重用导致的 stale reference 问题
- **两者互补**：Phase 1 + Phase 2 完整解决 split 相关 bug
