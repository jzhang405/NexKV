# BTree Lazy Merge G1+G2+G3 补全预研

> 创建日期：2026-05-22
> 前置：Epoch-based Page Reclamation (`28ec388`)、Phase 6.5 Lazy Merge (`a1b8c5d`)
> 状态：Ready for Implementation
> 目标：补全 Delete → Lazy Merge → 树收缩的最后三个缺口

---

## 一、背景

`2026-05-20-btree-delete-tombstone-gaps.md` 识别了 7 个缺口（G1-G7）。Epoch 提交 `28ec388` 已修复 G4/G5/G6/G7：

| 缺口 | 描述 | 状态 |
|------|------|------|
| G4 | tombstoneDelta 应用到 header | ✅ `28ec388` |
| G5 | Compaction 调度 | ✅ `28ec388` |
| G6 | Split 路径 Tombstone 传播 | ✅ `28ec388` |
| G7 | Merge 路径 Tombstone 合并 | ✅ `28ec388` |
| **G1** | **maybeMergeAfterWrite 禁用** | **❌ 未完成** |
| **G2** | **writeOperation 未调用 merge** | **❌ 未完成** |
| **G3** | **handleInternalMerge 空桩** | **❌ 未完成** |

G1 被禁用的原因是 `handleLeafMerge` 调用直接 `FreePage`，与 `searchPath` 引用者竞争。现在 Epoch 已在位，解除 G1 的前提条件已满足。

---

## 二、代码审计

### 2.1 G1: maybeMergeAfterWrite 空函数体

**位置**：`btree/merge_ops.go:25`

```go
func (b *BTree) maybeMergeAfterWrite(_ []byte, _ int64) {}
```

注释（lines 13-24）明确了禁用原因：`handleLeafMerge` 直接调用 `FreePage`。现在 Epoch 已实现，两个修复缺一不可：

1. **G1a**：`handleLeafMerge` 中 `FreePage` → `epochMgr.Retire()`
2. **G1b**：实现 `maybeMergeAfterWrite` 函数体

### 2.2 G2: writeOperation 未调用 merge

**位置**：`btree/operations.go:249-262`（CAS 成功路径）

`path.ReleaseAll()` 之前没有 merge 检查。调用点必须在 release 之前，因为 `handleLeafMerge` 需要通过 `path[len(path)-2]` 访问父节点。

### 2.3 G3: handleInternalMerge 空桩

**位置**：`btree/merge_ops.go:175-182`

```go
func (b *BTree) handleInternalMerge(path SearchPath, nodeRef *PageRef, _ *PageInfo) error {
    if len(path) < 2 { return nil }
    _ = path
    _ = nodeRef
    return nil
}
```

模板：`handleInternalSplit` (`operations.go:298-485`) 和 `handleLeafMerge` (`merge_ops.go:27-173`) 共享相同的 4-Phase CAS 协议。

---

## 三、设计决策

### D1: G1a — handleLeafMerge 使用 Epoch Retire

**方案**：`FreePage` → `epochMgr.Retire()`，对齐 `handleInternalSplit` 和 `compactPageWithParent` 中已有的模式。

```go
if b.epochMgr != nil {
    slot := b.epochMgr.AllocSlot()
    b.epochMgr.Retire(slot, piA.PageID)
    b.epochMgr.Retire(slot, piB.PageID)
} else {
    _ = b.storage.FreePage(piA.PageID)
    _ = b.storage.FreePage(piB.PageID)
}
```

向后兼容：测试用 `newTestBTree(t)` 创建（无 `WithEpoch()`），走 fallback 直接 FreePage，行为不变。

### D2: G1b — maybeMergeAfterWrite 签名与实现

**签名变更**：`func (b *BTree) maybeMergeAfterWrite(_ []byte, _ int64) {}` → `func (b *BTree) maybeMergeAfterWrite(path SearchPath, key []byte, delta int64)`

**实现**：
1. `delta >= 0` → return（仅 Delete 触发）
2. 从 path 获取 leafRef/leafPI，检查 busy/redirect
3. `isLeafSparse(leaf, MergeThreshold)` 快速守卫
4. 调用 `handleLeafMerge(path, leafRef, leafPI)`
5. 所有错误静默忽略（merge 是优化非正确性要求）

### D3: G3 — handleInternalMerge 仅 Merge，不 Borrow

**证明**：`MergeThreshold=0.5`，`MaxInternalKeys=126`。稀疏意味着 `ChildCount() < 63`，即 `Count() < 62`。两个稀疏内部节点合并后 `Count ≤ 61+61+1(separator) = 123 ≤ 126`，始终适合单页。Borrow 在数学上不需要。

**算法**（对齐 `handleLeafMerge` 的 Phase 1-4 + underflow）：

```
1. Guard: len(path) < 2 → nil；父节点 busy → nil
2. 找兄弟: prefer 左兄弟，fallback 右兄弟
   检查 !IsLeaf && !IsBusy() && isNodeSparse(sibNode, MergeThreshold)
3. Phase 1: 按 PageID 升序 CAS NodeMerging（防死锁）
4. Phase 2: MergeNodes(selfNode, sibNode, separator)
5. Phase 3: COW 父节点 — RemoveChild(removeIdx) + ReplaceChild → parentRef.CAS
6. 更新 children cache: mergeChildRefsInCache(parentRef, removeIdx, merged.PageID())
7. Phase 4: 标记旧页 NodeRedirect，epochMgr.Retire
8. Underflow: 父节点稀疏 → 递归 handleInternalMerge(path[:len(path)-1], parentRef, npi)
9. mergeRoot()
```

**separator 获取**：
- 左兄弟：`separator = parent.GetKey(sibIdx)`（sibIdx = leafIdx - 1）
- 右兄弟：`separator = parent.GetKey(nodeIdx)`（nodeIdx = leafIdx）

---

## 四、实现步骤

### Step 1: G1a — handleLeafMerge FreePage → Retire
- 文件：`btree/merge_ops.go:161-162`
- 改动：~6 行

### Step 2: G1b — 实现 maybeMergeAfterWrite
- 文件：`btree/merge_ops.go:25`
- 改动：~20 行

### Step 3: G2 — writeOperation 注入调用点
- 文件：`btree/operations.go:253`（`path.ReleaseAll()` 之前）
- 改动：~2 行

### Step 4: G3 — 实现 handleInternalMerge
- 文件：`btree/merge_ops.go:175-182`
- 改动：~120 行

---

## 五、验证标准

```bash
go build ./internal/infrastructure/storage/btree/...
go test -race ./internal/infrastructure/storage/btree/...
go test -cover ./internal/infrastructure/storage/btree/...
```

- 所有现有测试通过（epochMgr=nil → fallback FreePage 路径）
- 无 data race
- 覆盖率 >= 80%

---

## 六、风险与缓解

| 风险 | 缓解 |
|------|------|
| handleLeafMerge 被并发 goroutine 重复调用 | Phase 1 CAS NodeMerging 失败 → 安全 return |
| handleInternalMerge 无限递归 | `len(path) < 2` 守卫，每层递归 path 减 1 |
| mergeChildRefsInCache 在 nil children cache 上调用 | 函数内部 `curCache == nil` 早期返回 |
| 旧页双重释放（epoch + 直接 FreePage） | `if b.epochMgr != nil` 精确互斥 |
| MergeNodes 合并后数据丢失 | 保留左右节点的所有子节点，B+Tree 结构正确 |

---

## 七、Out of Scope

| 事项 | 原因 |
|------|------|
| BorrowFromNode 路径 | 数学上不需要（见 D3） |
| Compaction 与 Merge 的交错触发 | Compaction 使用 NodeCompacting 状态隔离 |
| maybeMergeAfterWrite 的 Set/Update 触发 | 非必要——仅 Delete 产生稀疏度变化 |

---

## 八、参考

- Tombstone 缺口分析：`2026-05-20-btree-delete-tombstone-gaps.md`
- Epoch 预研：`2026-05-21-epoch-page-reclamation-spike.md`
- Phase 6.5 预研：`2026-05-11-phase6.5-merge-compaction-spike.md`
- BTree 路线图：`2026-04-02-btree-refactor-roadmap.md`

---

**文档版本**：v1.0
**状态**：Ready for Implementation
