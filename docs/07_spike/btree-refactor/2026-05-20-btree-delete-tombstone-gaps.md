# BTree Delete + Tombstone 缺口分析与补全计划

> 创建日期：2026-05-20
> 前置：Phase 6.5 Lazy Merge + Tombstone Compaction（主分支 `a1b8c5d`）
> 状态：Planning
> 目标：补全 Delete → Tombstone → Lazy Merge → Compaction 全链路

---

## 一、背景

原始 spike（`2026-04-09-btree-delete-tombstone.md`）提出了三阶段 Tombstone 方案：
- Phase 1：Value 头部 1-byte Flag → Delete/Get/Set 改造
- Phase 2：MVCC 集成 → Flag 扩展为 Version + Status
- Phase 3：GC 回收 → Compaction + Split/Merge 感知

主分支已演进为完整的 MVCC Tombstone（`mvcc/codec.go`，9-byte header），Phase 6.5（Lazy Merge + Compaction）已合并（`a1b8c5d`）。但 `2026-05-19-phase4.2-remaining-tasks-spike.md` §4.2 将此任务标记为「🔄 进行中」。

本文档对主分支代码进行逐文件审查，识别剩余缺口。

---

## 二、已完成清单（无需改动）

| 组件 | 文件 | 状态 |
|------|------|------|
| MVCC Tombstone 编解码 | `mvcc/codec.go` | ✅ `FlagNormal`/`FlagTombstone` + 9-byte header |
| Delete → MVCC Tombstone | `btree/btree.go:357-404` | ✅ `BuildMVCC(FlagTombstone, ...)` + `Update(idx, tombstoneVal)` |
| Get 过滤 Tombstone | `btree/btree.go:187-237` | ✅ `ParseMVCC` → `IsTombstone()` → `ErrKeyNotFound` |
| GetRaw 不过滤 Tombstone | `btree/btree.go:275-285` | ✅ MVCC readers 可查看 beginTS |
| Set 恢复 Tombstone | `btree/btree.go:304-328` | ✅ `delta=+1`, `tombstoneDelta=-1` |
| Double Delete 防护 | `btree/btree.go:373-381` | ✅ Flag 检查 → `ErrKeyNotFound` |
| PageHeader.tombstoneCount | `offheap/page_layout.go:40` | ✅ `uint16` 字段 + Get/Set/Increment/Decrement |
| leafPageHandle tombstone API | `btree/leaf_page.go:248-255` | ✅ `IncrementTombstone()` / `DecrementTombstone()` |
| MergeLeaves/MergeNodes COW | `btree/merge.go` | ✅ 6 个方法完整实现 |
| BorrowFromLeft/Right (Leaf+Node) | `btree/merge.go` | ✅ 4 个 borrow 方法 |
| Compaction 单次循环 | `btree/compaction.go` | ✅ `Compact()` → leaf chain walk → `tryCompactLeaf` → COW replace |
| Compaction WatermarkProvider | `btree/compaction.go:17-19` | ✅ DIP 接口 |
| Sparse 检测辅助函数 | `btree/operations.go:962-974` | ✅ `isLeafSparse` / `isNodeSparse` |
| handleLeafMerge 4-phase | `btree/merge_ops.go:31-177` | ✅ NodeMerging CAS + COW + parent replace |
| mergeRoot | `btree/merge_ops.go:217-266` | ✅ 根收缩 |
| removeChildFromCache | `btree/merge_ops.go:188-215` | ✅ CAS-loop |
| Tombstone 相关测试 | `btree/btree_test.go` | ✅ 8 个 Tombstone 测试 + compaction 测试 |

**基础设施已全部就位**，剩余问题在集成层面。

---

## 三、缺口分析（7 项）

### G1: `maybeMergeAfterWrite` 显式禁用

**位置**：`btree/merge_ops.go:9-18`

```go
func (b *BTree) maybeMergeAfterWrite(path SearchPath, leafRef *PageRef, delta int64) {
    // Phase 6.5 MVP — merge is callable but not auto-triggered in hot path
    // to avoid CAS conflicts with concurrent writes during testing.
    // Enable by removing this early return after stabilization.
    _ = path
    _ = leafRef
    _ = delta
    // ... Full implementation commented out ...
}
```

**影响**：Delete 后页面利用率即使降到 0%，Merge 也永远不会触发。

**根因**：Phase 6.5 合并时保守地禁用了自动触发，避免并发测试中的 CAS 冲突。

---

### G2: `writeOperation` 中 Merge 触发缺失

**位置**：`btree/operations.go:232-233`

```go
// Phase 6.5 TODO: Lazy Merge — trigger b.handleLeafMerge when
// leaf utilization drops below MergeThreshold after a Delete.
path.ReleaseAll()
b.size.Add(result.delta)
return nil
```

**缺失**：CAS 成功后、`ReleaseAll` 之前，没有调用 `b.maybeMergeAfterWrite()`。

**正确位置**（伪代码）：
```go
if !leafRef.CAS(oldInfo, newInfo) {
    // ... CAS conflict retry ...
    continue
}
// ★ 在此处注入 merge 检查
if result.delta < 0 { // Delete 操作
    b.maybeMergeAfterWrite(path, leafRef, result.delta)
}
path.ReleaseAll()
b.size.Add(result.delta)
return nil
```

---

### G3: `handleInternalMerge` 为空桩

**位置**：`btree/merge_ops.go:179-186`

```go
func (b *BTree) handleInternalMerge(path SearchPath, nodeRef *PageRef, _ *PageInfo) error {
    if len(path) < 2 {
        return nil
    }
    _ = path
    _ = nodeRef
    return nil
}
```

**影响**：Leaf merge 后父节点可能 underflow，但 cascading internal merge 未实现。这会导致内部节点持续低利用率。

**需求**：
- 找到 nodeRef 的 sibling（通过 parent 的 children）
- 检测 sibling 利用率
- MergeNodes / BorrowFromNode
- 向上传播

**对应 `handleInternalSplit`**（`operations.go:288`）已有完整实现，可作为模板。

---

### G4: `tombstoneDelta` 未应用到 Page 头

**位置**：`btree/operations.go:23` + 非 split 写入路径

```go
type leafMutation struct {
    newPageID      model.PageID
    delta          int64
    tombstoneDelta int16  // Phase 6.5: change in tombstone count
}
```

`btree.go` 正确设置了 `tombstoneDelta`：
- Delete: `tombstoneDelta: 1`
- Set(Tombstone 恢复): `tombstoneDelta: -1`
- Set(Update): `tombstoneDelta: 0`

但 `operations.go` 的**非 split 写入成功路径**（~216-236）从未将 `tombstoneDelta` 写入 `PageHeader.tombstoneCount`：

```go
// 当前：只用了 newPageID 和 delta
newInfo := &PageInfo{PageID: result.newPageID, Version: oldInfo.Version + 1, IsLeaf: true}
if !leafRef.CAS(oldInfo, newInfo) { ... }
// ★ 缺失：storage.pa.SetTombstoneCount(uint32(result.newPageID), newCount)
```

**影响**：
- `PageHeader.tombstoneCount` 始终为 0
- `Compaction` 中 `SetTombstoneCount(newRawID, 0)` 实际从 0→0（无操作）
- `isLeafSparse` 使用 `leaf.Capacity()` 而非 `(Count-TombstoneCount)/Capacity`，因此不受影响
- 但未来任何依赖 `tombstoneCount` 的逻辑（如 Split 高 Tombstone 检测）会出错

**修复**：在 CAS 成功路径中，根据 `tombstoneDelta` 更新 header：
```go
if result.tombstoneDelta > 0 {
    b.storage.pa.IncrementTombstone(uint32(result.newPageID))
} else if result.tombstoneDelta < 0 {
    b.storage.pa.DecrementTombstone(uint32(result.newPageID))
}
```

注意：Split 路径中 `handleLeafSplit` 也需要设置新页面的 `tombstoneCount`，当前在两半叶子页面均未设置。

---

### G5: Compaction 未调度

**位置**：`btree/compaction.go:29-35`

`Compact(wp WatermarkProvider)` 存在且可用，但从未被调用：
- 未在 `CheckpointManager.FuzzyCheckpoint()` 中触发
- 未在后台 goroutine 中周期运行
- 未注册到统一任务调度器

**对比 Phase 4.4**：`ChunkCompactor` 已在 Checkpoint 后异步触发（`2026-05-20-phase4.4-gc-integration-spike.md`）。

**修复**：在 `CheckpointManager.FuzzyCheckpoint()` 结尾增加：
```go
// Phase 6.5: 触发 BTree Tombstone compaction
if m.compactWp != nil {
    go func() { _ = m.btree.Compact(m.compactWp) }()
}
```

---

### G6: Split 路径中的 Tombstone 传播

**位置**：`btree/operations.go` 中 `handleLeafSplit` / `handleRootSplit`

当叶子页面分裂时，Tombstone 条目分配到 left 或 right 子页面。但分裂后：
1. 新 left/right 页面的 `tombstoneCount` 未从旧页面继承/重新计算
2. 由 split 发起的 `leftMutation`/`rightMutation` 不包含 `tombstoneDelta` 信息

当前 `BulkInitLeafFromSource` 拷贝所有条目（含 Tombstone），但 `tombstoneCount` 字段未设置。

**影响**：分裂后的页面 `tombstoneCount` 为 0，导致 compaction 可能无法识别高 Tombstone 比例的页面。

---

### G7: Merge 中 Tombstone 计数合并

**位置**：`btree/merge.go:17-49` `MergeLeaves`

```go
func (s *OffheapBTreeStorage) MergeLeaves(left, right LeafPage) (LeafPage, error) {
    // ... collectKVRange + InsertLeafEntry ...
    // ★ 未设置 new page 的 tombstoneCount
}
```

`MergeLeaves` 使用 `collectKVRange` 复制**所有**条目（含 Tombstone），但未统计和设置 `tombstoneCount`。

同样，`BorrowFromLeftLeaf`/`BorrowFromRightLeaf` 也缺失 Tombstone 计数更新。

---

## 四、缺口汇总

| ID | 缺口 | 严重度 | 阻塞什么 |
|----|------|--------|---------|
| G1 | `maybeMergeAfterWrite` 禁用 | CRITICAL | Delete 后 Merge 永不会触发 |
| G2 | `writeOperation` 未调用 merge | CRITICAL | Merge 触发链断裂 |
| G3 | `handleInternalMerge` 空桩 | HIGH | 内部节点持续 underflow |
| G4 | `tombstoneDelta` 未应用到 header | HIGH | PageHeader.tombstoneCount 始终为 0 |
| G5 | Compaction 未调度 | MEDIUM | Tombstone 永不被物理回收 |
| G6 | Split 路径 Tombstone 传播 | MEDIUM | 分裂后 tombstoneCount 丢失 |
| G7 | Merge 路径 Tombstone 合并 | LOW | 合并后 tombstoneCount 丢失 |

---

## 五、实施计划

### Step 1: 启用 Lazy Merge 触发（G1 + G2）

**范围**：`btree/merge_ops.go` + `btree/operations.go`

1. 在 `writeOperation` 成功路径中注入 merge 检查
2. 启用 `maybeMergeAfterWrite` 函数体
3. 仅对 Delete 操作（`result.delta < 0`）触发

**验证**：
- `TestBTreeDelete_TriggersMerge`：删除足够多的 key 后，merge 被触发
- `TestBTreeConcurrentDelete_MergeNoConflict`：并发 Delete + Merge 不产生 CAS 死锁

### Step 2: 实现 handleInternalMerge（G3）

**范围**：`btree/merge_ops.go`

对齐 `handleInternalSplit` 的模式：
1. 找到 sibling 内部节点（通过 parentRef.children）
2. MergeNodes / BorrowFromNode
3. 向上传播 underflow

**验证**：
- `TestInternalMerge_Cascade`：多级内部节点合并
- `TestInternalMerge_Underflow`：内部节点 underflow 检测

### Step 3: 修复 tombstoneDelta 应用（G4）

**范围**：`btree/operations.go`

在非 split 写入成功路径 + split 路径中：
1. 根据 `tombstoneDelta` 调用 `IncrementTombstone`/`DecrementTombstone`
2. 所有 COW 页面初始化时 `tombstoneCount = 0`（已有 `InitLeafPage` 清零）

**验证**：
- `TestTombstoneCount_AfterDelete`：Delete 后 `GetTombstoneCount() == 1`
- `TestTombstoneCount_AfterRecovery`：Tombstone 恢复后 `GetTombstoneCount() == 0`

### Step 4: 修复 Split/Merge Tombstone 传播（G6 + G7）

**范围**：`btree/merge.go` + `btree/operations.go`

1. `MergeLeaves`：统计输入页面的 `tombstoneCount` 并设置到合并页
2. `handleLeafSplit`：分配合并前统计 Tombstone，分配后按 left/right 分别设置
3. `BorrowFromLeftLeaf`/`BorrowFromRightLeaf`：根据借出的条目更新 `tombstoneCount`

**验证**：
- `TestMergeLeaves_TombstoneCountSum`：合并后 `tombstoneCount` = left + right
- `TestSplit_TombstoneDistribution`：分裂后 left/right 的 `tombstoneCount` 之和等于原页面

### Step 5: 调度 Compaction（G5）

**范围**：`btree/compaction.go` + 调度集成点

1. 在 `CheckpointManager.FuzzyCheckpoint()` 结尾异步触发
2. 参考 Phase 4.4 `ChunkCompactor` 的触发模式：
```go
if m.btreeCompactor != nil {
    go func() { _ = m.btree.Compact(m.watermarkProvider) }()
}
```

**验证**：
- End-to-end: Set → Delete → Checkpoint → 页面 Tombstone 被物理回收
- `TestCompact_ReclaimsTombstoneSpace`（已存在，需要 Watermark 正确设置）

---

## 六、不在此范围的项

| 项 | 原因 |
|----|------|
| RangeScan Tombstone 过滤 | `RangeScan` 返回 `ErrNotImplemented`，先实现再过滤 |
| MVCC Phase 2 补全（事务恢复） | 独立任务，见 `2026-04-10-mvcc-phase2-plan.md` |
| ChunkCompactor 联动 | Phase 4.4 已完成（`397d4d7`） |
| 新 Split/Merge 决策算法 | 当前算法功能正确，调优可后置 |

---

## 七、风险与缓解

| 风险 | 等级 | 缓解 |
|------|------|------|
| Merge 自动触发导致并发 CAS 冲突增加 | 中 | 仅 Delete 操作触发；Merge CAS 失败不阻塞 Delete 返回 |
| handleInternalMerge 级联合并导致长 CAS 链 | 中 | 与 `handleInternalSplit` 相同的退避策略 |
| tombstoneDelta 应用延迟导致 header 不一致 | 低 | 仅在 COW 页面创建时设置，原子 CAS 发布 |
| Compaction 与 Checkpoint 交错触发 | 低 | Compaction 和 Checkpoint 操作不同页面集 |

---

## 八、建议执行顺序

```
Step 1 (G1+G2): 启用 maybeMergeAfterWrite  —— 最小改动，解锁全链路
  ↓
Step 3 (G4):   修复 tombstoneDelta 应用     —— 数据正确性基础
  ↓
Step 4 (G6+G7): 修复 Split/Merge 传播       —— 补全元数据
  ↓
Step 2 (G3):   实现 handleInternalMerge     —— 多级树收缩
  ↓
Step 5 (G5):   调度 Compaction              —— 物理空间回收
```

---

## 九、参考

- 原始设计：`2026-04-09-btree-delete-tombstone.md`
- Phase 6.5 Spike：`2026-05-11-phase6.5-merge-compaction-spike.md`
- 剩余任务清单：`2026-05-19-phase4.2-remaining-tasks-spike.md`
- Phase 4.4 GC 集成：`2026-05-20-phase4.4-gc-integration-spike.md`
- 路线图：`2026-04-02-btree-refactor-roadmap.md`

---

**文档版本**：v1.0
**状态**：Planning
