# PRE: Compaction CRITICAL Bug Fix — 使用真实 PageRef + COW 父节点

## 背景

Code Review 发现 `compaction.go:compactPage` 存在严重数据损坏漏洞：
1. 在叶子链遍历中创建的临时 `PageRef` 上执行 CAS，对真实树上 `PageRef` 无效
2. 显式释放旧页面导致 double-free
3. 父节点 child pointer 未更新

## Bug 详情

### Bug 1 — 错误的 PageRef

`compactCycle` 通过叶子链表（`nextPage` 指针）遍历，每次迭代 `NewPageRef(nextID, 0, ...)`。这些是临时对象，与父节点 `ChildrenCache` 无关。`compactPage` CAS 只更新临时对象的 `pInfo`，真实树上 `PageRef` 保留已释放 page ID 的旧 `pInfo`。

### Bug 2 — 显式 double-free

`_ = b.storage.pm.Free(uint32(oldPI.PageID))` 在真实 `PageRef` 还持有该页时释放。当 `refCount` 降到 0 时，`freeFunc(r.pageID)` 再次 free 同一页面。

### 正确模式（参考 `writeOperation` / `handleLeafSplit` / `handleLeafMerge`）

- 通过 `searchPath` 获取真实树上 `PageRef`（来自父节点 `ChildrenCache`）
- CAS 真实 `leafRef.pInfo` 原子更新
- COW 父节点 `ReplaceChild` → CAS `parentRef` → 更新 ChildrenCache
- 不显式 free 旧页，让 `PageRef.Release` 生命周期处理

## 设计方案

### 修改范围

| 文件 | 改动 | 说明 |
|------|------|------|
| `btree/page_info.go` | +1 行 | 添加 `NodeCompacting = 5` |
| `btree/operations.go` | +4 行 | `writeOperation` 跳过 `NodeCompacting` 状态 |
| `btree/compaction.go` | 重写 `compactPage` + `compactCycle` | 核心修复 |
| `btree/compaction_test.go` | ~200 行（新文件） | 测试 |

### 压缩流程（5 阶段）

**Phase A — CAS 标记 Compacting**：
```go
compactingInfo := &PageInfo{PageID: oldPI.PageID, Version: oldPI.Version+1, IsLeaf: true, NodeState: NodeCompacting}
leafRef.CAS(oldPI, compactingInfo)
```
defer 回滚：失败时 CAS 回退到 oldPI

**Phase B — 收集保留条目**：遍历 leaf entries，跳过 commitTS < watermark 的 tombstone。无条目被移除时直接回滚。

**Phase C — 分配 COW 页面**：`pm.Alloc()` → `InitLeafPage` → `InsertLeafEntry` × N。复制 `prevPage`/`nextPage` 链指针。

**Phase D — COW 父节点**（depth >= 2）：`parent.ReplaceChild(leafIdx, newRawID)` → `parentRef.CAS(parentPI, newParPI)`。parent CAS 失败时 leaf CAS 仍然有效（运行时正确性不依赖 parent node）。

**Phase E — CAS leafRef 到最终状态**：
```go
finalPI := &PageInfo{PageID: model.PageID(newRawID), Version: compactingInfo.Version+1, IsLeaf: true, NodeState: NodeNormal}
leafRef.CAS(compactingInfo, finalPI)
```

### 并发安全

- Write vs Compaction：`NodeCompacting` 被 `writeOperation` 跳过，write 重试后操作压缩后页面
- Split vs Compaction：`NodeCompacting` 被 `IsBusy()` 拦截
- Merge vs Compaction：同上
- Compaction vs Compaction：Phase A CAS 只有一个成功

### 不显式释放旧页面

`leafRef.pageID` 在创建时绑定到旧 pageID（不可变）。CAS 仅更新 `pInfo.PageID`。当 `refCount` 降到 0，`freeFunc(oldPI.PageID)` 自动释放。这是 `writeOperation` 的相同模式。

### 已知限制

叶子链邻居的 `prevPage`/`nextPage` 仍指向旧 pageID。这是已有问题（当前代码同样存在），作为后续优化跟踪。实际影响有限：
- 叶子链遍历仅在 compaction 中使用（后台，非热路径）
- compaction 跳过 `GetLeafPage` 失败的页面
- 旧页在 PageRef refCount→0 后释放，之后叶子链重建

## 测试计划

- `TestCompactSingleLeaf`：插入 30 条，tombstone 20 条（commitTS < watermark），压缩后剩余 10 条
- `TestCompactPreserveAboveWatermark`：tombstone 10 条（commitTS > watermark），全部保留
- `TestCompactMultiLeaf`：跨 3 叶子的树，压缩中间叶子
- `TestCompactRootLeaf`：单叶子树（根即叶子）
- `TestCompactConcurrentWrite`：并发写 vs 压缩
- `TestCompactAllLeaves`：多轮压缩遍历所有叶子

## 风险评估

- **低风险**：代码仅影响 `compactPage` + `compactCycle`，不改变其他路径
- `Compact()` 当前无外部调用者（存储层未集成到应用入口），线上无影响
- 所有 CAS 失败路径有回滚，不泄漏页面
- 修改模式与已稳定的 `writeOperation` / `handleLeafSplit` / `handleLeafMerge` 一致

## 参考资料

- `operations.go:writeOperation` — COW leaf 替换的正确模式
- `operations.go:handleLeafSplit` — parent update + ChildrenCache + Redirect
- `merge_ops.go:handleLeafMerge` — parent RemoveChild + ReplaceChild + CAS
- `search.go:searchPath` — ChildrenCache 导航机制
- `page_ref.go` — PageRef CAS + 生命周期
