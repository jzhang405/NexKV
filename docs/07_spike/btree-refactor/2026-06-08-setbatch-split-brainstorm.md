# Brainstorm: SetBatch PageDispatcher split 期间数据丢失 (#SetBatch-split-race)

> **日期**：2026-06-08
> **现象**：`TestSetBatch_MixedNewAndExisting` 中 mx-006 更新后读到旧值 "old-006" 而非 "val-006"。稳定复现。
> **触发条件**：Phase 3 版本内嵌使 value 25B→49B → TryInPlace 失败 → COW Update → 页面更早填满 → split。
>
> **已排除的假设**：
> - `batch.pageID` 过期 (executeBatch 全程不读取 batch.pageID)
> - mutation.newPageID=0 (inPlace, 已修复但仍失败)
> - SetWithRetry.searchPath 写到错误页 (searchPath 每次从 root 独立导航)

---

## 1. 代码发现：writeOperationWithRetry 是 writeOperation 的完整副本

`write_operation_retry.go` (179行) 是 `operations.go:writeOperation` (120-302行) 的**完整复制**，唯一差别是 `maxRetries` 参数化 vs `MaxCASRetries` 常量。

### 🔴 DRY 违规

```
writeOperation         (operations.go:120)    ← Set/Delete 路径
writeOperationWithRetry (write_operation_retry.go:18) ← SetBatch 路径

两个函数有完全相同的:
  - searchPath 遍历
  - PageInfo 读取/校验
  - Splitting 回退
  - GetLeafPage + double-check
  - Split 路径 (doSplitWithSplitting → handleRootSplit/handleLeafSplit)
  - InPlace 更新
  - COW Update + tombstoneDelta
  - CAS 发布 + 大小更新
  - retiredPages + epoch 管理
```

### 🔴 已知分歧：inPlace tombstoneDelta 处理缺失

`writeOperation` (commit 133e5ec 修复后):
```go
if leafRef.CAS(oldInfo, claimInfo) {
    b.storage.pa.OverwriteLeafValue(rawID, result.inPlaceIdx, result.inPlaceValue)
    if result.tombstoneDelta != 0 {           // ✅ 已修复
        tc := b.storage.pa.GetTombstoneCount(rawID)
        ...
        b.storage.pa.SetTombstoneCount(rawID, uint16(newTC))
    }
```

`writeOperationWithRetry` (未修复):
```go
if leafRef.CAS(oldInfo, claimInfo) {
    b.storage.pa.OverwriteLeafValue(rawID, result.inPlaceIdx, result.inPlaceValue)
    // ❌ tombstoneDelta 未处理
    finalInfo := ...
    leafRef.CAS(claimInfo, finalInfo)
    path.ReleaseAll()
    b.size.Add(result.delta)
    return nil
```

> 此分歧影响 tombstone count 准确性（G4 compaction 误判），但**不导致数据丢失**。

**建议**：合并两个函数——`writeOperation` 增加 `maxRetries int` 参数，删除 `write_operation_retry.go`。预计删除 ~170 行，杜绝未来修复遗漏。

合并细节：
```go
// operations.go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    return writeOperationImpl(b, key, mutate, MaxCASRetries)
}

// 删除 write_operation_retry.go
// 将 writeOperationWithRetry 重命名为 writeOperationImpl（内部使用）
// PageDispatcher 调用 writeOperationImpl(b, key, mutate, maxCASFastAttempts)
```
`MaxCASRetries` (operations.go) 和 `maxCASFastAttempts` (page_dispatcher.go) 当前都是 3。统一后 **保留参数化**——`writeOperationImpl(b, key, mutate, maxRetries)` 接受参数，`writeOperation` 使用 `MaxCASRetries` 作为默认值，`PageDispatcher` 使用 `maxCASFastAttempts`。保留不同常量的区分语义。

**副作用评估**：合并后 `writeOperation` 的 CAS retry 次数不变（仍用 MaxCASRetries=3）。唯一变化是 `writeOperationWithRetry` 被替换为同一个实现 → 消除 inPlace tombstoneDelta 等已知分歧。对 benchmark 无性能影响。

---

## 2. 根因理论

### Theory A：InPlace 在 split 内的 TryInPlace 歧义

现在 mx-006 固定失败，已知升级到 version 格式后 len(val)变大，TryInPlace 一定返回 false。但这个是关键：

```
handleLeafSplit/handleRootSplit 内部的 mutate(target):
  target 是 leftPage 或 rightPage (刚 split 出来的)
  
  mutateUpdate(target, idx, value):
    idx = target.Search(pairs[6].Key)  ← 在 split 后的新页中找
    raw = target.GetValue(idx)          ← 读旧值
    mvccVal = ParseMVCC(raw)            ← 解码
    encoded = BuildMVCC(..., 0,0,nil)   ← 编码新值 (~49B)
    TryInPlace(idx, encoded)            ← 检查新旧长度
```

`TryInPlace` 对比的是**新页面中的旧值槽位长度**。split 后的页面是全新 COW 拷贝——每个 entry 的 `valLen` 正确。所以 TryInPlace 判定应正确。

但如果 split 页面的 `valLen` 为旧格式的 27B，而新值 49B → TryInPlace 返回 false → COW Update 路径 → 正确。

**评估**：Theory A 不会导致数据丢失。split + COW Update + CAS 逻辑已验证正确（50 key 单独 Set 全覆盖）。

### Theory B：root children cache 在 split 后的并发窗口

```
split 完成 → ReplaceRoot CAS 发布新 root
  → root.children.Store(newChildren)     ← 原子设置

并发 searchPath:
  searchPath(b.rootRef, key):
    cache := currentRef.GetChildren()
    if cache == nil → retry
    
    在 CAS 成功但 children.Store 完成之前:
      pInfo.IsLeaf=false (新的 root pInfo)
      但 children=nil (尚未由 ReplaceRoot 设置)
      → searchPath 返回 ErrRetry → writeOperation retry
```

但 `ReplaceRoot` 内是**同步的**——CAS 成功后立即 `children.Store`。没有并发窗口。searchPath 重试后必然看到正确 children。

**评估**：单 worker 串行执行，无并发。Theory B 排除。

### Theory C：leafPageHandle pool 重用导致脏读

```go
// offheap_storage.go: GetLeafPage
func (s *OffheapBTreeStorage) GetLeafPage(pageID model.PageID) (LeafPage, error) {
    ...
    if v := s.leafHandlePool.Get(); v != nil {   // ← 从 pool 取
        h := v.(*leafPageHandle)
        h.id = pageID   // ← 设置新 pageID
```

`leafPageHandle` 从 `sync.Pool` 获取，设置 `id` 为新 pageID。如果 pool 中有残留状态（比如 `pa` 指针仍然有效），新 handle 应正常工作。

但如果 `leafPageHandle` 有其他可变状态（比如缓存了 page 内部数据），pool 重用可能带来脏数据。

**验证方法**：需修改 `offheap_storage.go:GetLeafPage` ——将 `leafHandlePool.Get()` 改为总是 `return &leafPageHandle{...}`。侵入性较大，且需要重新编译。建议暂缓，在其他理论排除后再验证。（P2 优先级）

**评估**：可能性低。

### Theory D：mutateUpdate 在 split 中使用了原始页面上的 page handle

```
writeOperationWithRetry → 非 Split 路径:
  searchPath → leafRef = A (pageID=10)
  oldLeaf = GetLeafPage(10)       ← handle A (h1, id=10)
  mutate(oldLeaf)                 ← 在 A 上修改
    mutateUpdate(h1, idx, val):
      TryInPlace(h1, idx, encoded)  ← 失败 → COW
      newLeaf = h1.Update(idx, encoded)  ← 返回 h2 (id=11)
  result = {newPageID: 11}
  CAS(leafRef, oldInfo(10) → newInfo(11))  ← 页 10 → 11

  下一次 writeOperationWithRetry:
  searchPath 可能在页 11 或 split 后的页上找到 key
```

每一步都是独立的 `writeOperationWithRetry`，每次都重新 `searchPath` + `GetLeafPage`。handle 生命周期只在单次 writeOperation 内。

**评估**：无 handle 跨调用泄露。

### Theory E 🔥：SetWithRetry.err Suppression → Silent Failure

```go
// page_dispatcher.go: executeBatch
err := batch.tree.SetWithRetry(batch.ctx, t.key, t.value, maxCASFastAttempts)
// maxCASFastAttempts = 3

if err != nil && isCASRetryExhausted(err) && batch.retries < maxCASRequeue {
    // requeue
}

// ↓ 如果 err != nil 但不是 CASRetryExhausted（比如 ErrDuplicateKey, ErrBTreeValidationError...）
// ↓ 错误被静默吞掉（不 requeue，不记录 results）
batch.results[i] = WriteResult{Index: t.idx, Err: err}
```

```go
// set_with_retry.go
func (b *BTree) SetWithRetry(ctx context.Context, key, value []byte, maxRetries int) error {
    err := writeOperationWithRetry(b, key, func(leaf LeafPage) (*leafMutation, error) {
        idx, found := leaf.Search(key)
        if found {
            return b.mutateUpdate(leaf, idx, value)
        }
        return b.mutateInsert(leaf, key, value)
    }, maxRetries)
    return err
}
```

`SetWithRetry` returns `writeOperationWithRetry`'s error. If `writeOperationWithRetry` exhausts retries, it returns `ErrCASRetryExhausted` → requeue path takes it. But what if the mutate function itself returns an error? That error is passed through to `SetWithRetry` → `executeBatch` → recorded as a WriteResult error.

In the test before the fix, `SetBatch` returned `BatchError("2 write(s) failed")`. After the fix (child≠0), the test no longer returns BatchError — size=80, but mx-006 has stale "old-006". The 2 failures were NOT mx-006 — they were keys hitting the child≠0 error in split propagation. mx-006's write **was not among the reported failures** — WriteResult for mx-006 has `Err: nil`, meaning either:
1. mx-006's write succeeded on a page that was later recycled without proper CAS publication
2. `SetBatch` succeeded (`WriteResult.Err == nil`) but the written data didn't persist in the BTree
3. A subsequent write overwrote mx-006's slot during split page redistribution

**验证方法**：在修复 child≠0 后重新跑测试，检查 BatchError 是否消失（2 failures→0），同时 mx-006 是否仍然 stale。

### Theory F 🔥🔥：split 后 children cache 的 left child 是 mutation 页，但 double-COW orphan 被提前 free

```
key 0..5 共 6 次 SetWithRetry:
  每次 COW Update → 新 pageID → CAS root 发布
  root page 当前是 P_M_minus_1（包含 key 0..M-1 的最新值 + key M..49 的旧值）

key M: COW Update → IsFull() → Split!
  handleRootSplit:
    rootLeaf = GetLeafPage(P_M_minus_1)     ← 包含全部 50 个 key 的当前状态
    left, right, splitKey = rootLeaf.Split() ← 正确分割
    
    mutate(target) → double-COW             ← target = left 或 right
    orphanPageID = 被替换的原始子页           ← Free 回收
    
    leftChildID = mutation.newPageID 或 leftPage.PageID()
    rightChildID = rightPage.PageID() 或 mutation.newPageID()
    
    newRoot = InsertChild(splitKey, leftChildID, rightChildID)
    ReplaceRoot(splittingInfo → internalRootInfo, newChildren)
    
    ↓ 此时 left/right children 指向正确的页 ✅

key M+1..79: searchPath 从 internal root 导航到正确叶子页 → 写入正确 ✅
```

**但是**——`handleRootSplit` 中 `mutateUpdate` 在 split 后的新页上执行。如果 `mutateUpdate` 走 `TryInPlace`、不走 COW、`mutation.newPageID` 没有设置（inPlace 下 newPageID 为空）——这已经在上次 commit 中修复（!mutation.inPlace 守卫）。

**还有一个关键点**：`handleRootSplit` 的 ReplaceRoot CAS 使用的是 `splittingInfo`（由 `writeOperation` 的 Step 6 在 CAS ① 中设置）。但如果 `doSplitWithSplitting` 的 defer 函数中检测到 `leafRef.GetPageInfo() == splittingInfo`，它会 rollback splittingInfo → oldInfo。这个 defer 和 ReplaceRoot 的时序是否安全？

```go
func doSplitWithSplitting(...) error {
    defer func() {
        if leafRef.GetPageInfo() == splittingInfo {
            leafRef.CAS(splittingInfo, oldInfo) // Rollback
        }
    }()
    // ...
    return b.handleRootSplit(...)  // 内部 ReplaceRoot 替换 splittingInfo → internalRoot
}
```

`ReplaceRoot` 成功后 `leafRef.GetPageInfo() != splittingInfo` → defer 跳过 rollback ✅。

**评估**：Theory F 完整路径正确，单 worker 序列化执行不存在竞态。根因不在 writeOperation 主路径。

### Theory G：`SetWithRetry` 的 mutate 闭包在 split 路径中的 TryInPlace 交互

`handleRootSplit/handleLeafSplit` 内 `mutate(target)` 在 split 后的新页上调用。`mutateUpdate` 使用 `leaf.GetValue(idx)` 读取新页面的旧值槽位长度 → TryInPlace 判定正确。闭包对两种页（原始页 / split 后新页）透明工作。

**评估**：路径正确，排除。

### Theory H：tombstoneDelta 缺失 → G4 压缩误判 → 存活的 key 被压缩

`writeOperationWithRetry` 的 inPlace 路径缺少 tombstoneDelta 处理。可能导致 tombstone count 偏大 → Merge 误判存活的 key 为 tombstone → 压缩造成数据丢失。但当前 benchmark 场景（纯 update+insert，无 Tombstone）下 Merge 不会触发。纯写场景不适用。

**评估**：排除。但确认了 `writeOperationWithRetry` inPlace tombstoneDelta 缺失是个真实 bug——虽不导致此场景的数据丢失。

### Theory I：`executeBatch` wg.Add/wg.Done 计数不匹配

每次 requeue: wg.Add(1) + goroutine Submit。Submit 成功 → 新 executeBatch 入口 defer Done(-1)。Submit 失败 → goroutine 内立即 Done(-1)。数学验证：初始 count=1，N 次 requeue 后 count 归零。正确。

**评估**：wg 计数正确，排除。

### 总结：全部 9 个理论 (A-I) 被排除或验证不相关

### Theory J 🔥🔥🔥：WriteResult.Err==nil 但数据未持久化 — 核心矛盾

这是本 bug 最深层的问题——代码报告成功，但读回旧值：

```
mx-006 的 WriteResult.Err == nil → SetWithRetry 返回 nil

SetWithRetry 返回 nil 的三条路径:
  1. mutateUpdate 成功 → COW + CAS 成功 → return nil
  2. doSplitWithSplitting → handleRootSplit → return nil  
  3. result.inPlace → CAS claim → overwrite → return nil

所有三条路径都表示"BTree 页面的 CAS 发布成功"。
但 BTree.Get("mx-006") 返回 "old-006"。
```

可能的解释：
- **CAS 成功但发布到了非活跃的父级/叶子链节点**：CAS 是对 `leafRef` 做的，如果 leafRef 指向的页面在 `searchPath` 和 `mutate` 之间变成了 Redirect（不可能——单 worker 串行），CAS 虽然成功但页面已不再被树引用
- **两次写同 key 且第二次覆盖了第一次**：batch 中 mx-006 出现两次？但 80 个 key 各唯一
- **Split 的页面重分配中，mx-006 的 slot 被更晚的 key 覆盖**：handleRootSplit 中 left/right 分割时存在 off-by-one？

**❌ 静态分析无法解释这一矛盾。需要运行时调试。**

下一步需要通过实际调试来确定根因，而非静态代码分析。

---

## 3. 调试方案

### 3.1 启用 GlobalTracer，Hook 关键路径

```go
type captureTracer struct {
    mu   sync.Mutex
    logs []string
}

func (t *captureTracer) LogOp(op string, args ...any) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.logs = append(t.logs, fmt.Sprintf("%s %v", op, args))
}

func TestDebugSetBatchWithTrace(t *testing.T) {
    ct := &captureTracer{logs: make([]string, 0, 10000)}
    oldTracer := btree.GlobalTracer
    btree.GlobalTracer = ct
    defer func() { btree.GlobalTracer = oldTracer }()

    // 执行前 20 个 key 的 SetBatch
    // 每 key 打印: writeOperation 进入, searchPath 结果, mutate 完成, CAS 结果
    
    // 搜索 mx-006 的痕迹:
    for _, log := range ct.logs {
        if strings.Contains(log, "006") {
            t.Log(log)
        }
    }
}
```

### 3.2 分步执行 + 逐 key 验证 — 用 `tree.Set` (非 SetWithRetry)

```go
func TestStepByStepBatch(t *testing.T) {
    // Preload 50 keys
    // 逐 key 调用 tree.Set(...) — 走普通 writeOperation 路径
    // 每次 Set 后立即 Get 验证
    // 记录 split 发生的位置
    // 找出数据丢失的精确时机
}
```

> **与 §3.4 的区别**：§3.2 用 `tree.Set`（`writeOperation`，MaxCASRetries=3），§3.4 用 `tree.SetWithRetry`（`writeOperationWithRetry`，参数化 maxRetries）。两者走不同的 writeOperation 实现。如果 §3.2 正确但 §3.4 失败 → Bug 在 `writeOperationWithRetry` 的复制分歧。如果两者都正确 → Bug 在 PageDispatcher/executeBatch 层。

### 3.3 最小 batch size 二分法 — 精确确定触发条件

```go
func TestBinarySearchBatchSize(t *testing.T) {
    // 二分查找最小触发 batch size:
    //  batch=50（纯更新，0 插入）→ 是否失败？
    //  batch=51（50 更新 + 1 插入）→ 是否失败？
    //  batch=60（50 更新 + 10 插入）→ 是否失败？
    //  batch=70（50 更新 + 20 插入）→ 是否失败？
    //
    // batch=50 场景：如果 50 次 COW Update（每次 49B）累积到页面满（~80 entries）→ 不触发 split
    // batch=51 场景：1 次 INSERT 增加 1 个 entry → 可能刚好越过分页阈值
    // 这样可以区分：是否需要 INSERT 触发 split？还是单纯 COW Update 累积就能触发？
}
```

### 3.4 绕过 PageDispatcher — 最快验证是否为 PD 专有问题

```go
func TestWithoutPageDispatcher(t *testing.T) {
    tree, _ := newTestBTree(t)
    
    // Preload 50 keys (使用 tree.Set)
    // 模拟 SetBatch 但不经过 PageDispatcher:
    //   对每个 key 调用 tree.SetWithRetry(ctx, key, val, 3)
    //   50 次 update + 30 次 insert
    //
    // tree.SetWithRetry 直接调用 writeOperationWithRetry (maxRetries=3)
    // 跳过 BatchWriter → PageDispatcher → WorkerPool → executeBatch 全链路
    //
    // 如果这也能复现数据丢失 → Bug 在 writeOperationWithRetry
    // 如果不能复现 → Bug 在 PageDispatcher/executeBatch
}
```

> **WriteResult Index→Key 映射**：`WriteResult.Index` 是 `pairs` 数组的下标。mx-006 对应 `pairs[6]` → `WriteResult.Index=6`。通过检查 `batch.results[6].Err` 确认 mx-006 的写入结果。

> **GlobalTracer 可用性**：`GlobalTracer` 是导出变量 (`var GlobalTracer Tracer = &nilTracer{}`)，在测试中可以直接替换为 `captureTracer`。现有 trace 点覆盖 `writeOperation`（Line 306+）、`handleLeafSplit`（Line 794+）、`updateChildrenCache`（Line 741+）。需要确认 `writeOperationWithRetry` 中有对应的 trace 点——当前 write_operation_retry.go 中只有 Line 176 `writeOpWithRetry.EXHAUSTED`，缺常规的 split/mutate 路径 trace。合并两个函数后自动补全。


### 4.1 🔴 合并 writeOperation / writeOperationWithRetry

```go
// operations.go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    return writeOperationWithRetry(b, key, mutate, MaxCASRetries)
}

// 删除 write_operation_retry.go (~170行)
// 将 writeOperationWithRetry 移入 operations.go，writeOperation 作为薄包装
```

**收益**：
- 消除未来 fix 遗漏风险（当前已知分歧：inPlace tombstoneDelta + 后续修复）
- 减少代码重复 ~170 行
- make lint 减少一个函数 check
- `writeOperationWithRetry` 缺常规 trace 点（只有 EXHAUSTED），合并后复用 `writeOperation` 的全部 trace 覆盖

### 4.2 🟡 增强 error reporting

`executeBatch` 中的每个 WriteResult.Err 在 `SetBatch` 调用处只显示 "N write(s) failed"。应展开第一个错误的详细信息。

### 4.3 🟡 考虑简化 SetBatch 实现

当前 `SetBatch` → `BatchWriter` → `PageDispatcher` → `WorkerPool` → `SetWithRetry` 的层级很深。对于单层 BTree（所有 key 命中同一页），这种设计过于复杂。

**备选方案**：类似事务路径的 `btreeStorageAdapter.SetBatch`——分段 commit + `writeOperation` 重试循环内批量处理。实现方案详见主文档 [[2026-06-08-txn-benchmark-spike#改动-②b-计划btreesetbatch-cow-批量化]]。每段 ~100 keys（页面容量上限），一段一次 COW+一次 CAS。对单层 BTree 同时解决了 PageDispatcher 的 split 期间数据丢失问题（不再需要 PD 的 pageID 分组）。可选替代修复——如果当前 PD split bug 难以定位，直接用分段 COW 实现替换 `SetBatch`。

---

## 5. 下一步

| 优先级 | 行动 | 工作量 | 备注 |
|:--:|------|:--:|------|
| P0 | §3.4 绕过PD — SetWithRetry逐key调用验证 | ~15min | ✅ Bug确认在PD层 |
| P0 | §3.2 逐keySet验证 — writeOperation路径 | ~15min | ✅ writeOperation正确 |
| P0 | §3.3 二分法(batch=50→75→79) | ~15min | ✅ 阈值79, INSERT必需 |
| P0 | GlobalTracer 追踪 + Theory J 验证 | ~60min | ✅ err==nil但数据不持久 |
| P1 | BTree.SetBatch替换为SetWithRetry | ~15min | ✅ batch-set +190% |
| ~~P1~~ | ~~合并 writeOperation + writeOperationWithRetry~~ | — | ⏸️ (SetBatch不再需要) |
| P2 | Theory C pool 验证 | ~20min | ⏸️ |
| P2 | 增强 error reporting | ~10min | ⏸️ |
| P2 | §6 分段COW替代PD | ~1h | ⏸️ (SetWithRetry已够用) |
| ✅ | 移除 TestSetBatch_MixedNewAndExisting skip | ~1min | ✅ 完成 |
| ✅ | **根因修复** | — | ✅ SetWithRetry替代PD |

---

## 6. 🔥 修复方案：分段 COW 替代 PageDispatcher 实现 SetBatch（推荐）

> **选择原因**：PD split bug 根因定位困难（Theory J: 写入报告成功但不持久化），且 PD 设计对单层 BTree 过于复杂。使用已验证正确的分段 COW 方案完全规避此 bug。

### 思路

用 `btreeStorageAdapter.SetBatch` 的同款模式替换 `BTree.SetBatch`：`writeOperation` 循环内分段 Insert/Update同一份 COW 拷贝。

```
当前 (有bug):
  BTree.SetBatch → BatchWriter → PageDispatcher → resolveShardPageIDs
    → WorkerPool.Submit → executeBatch → 逐 key SetWithRetry

修复后:
  BTree.SetBatch → sort keys → writeOperation 循环:
    每段 ~90 keys (接近 4KB 页面容量)
      mutate(leaf):
        for each pair in segment:
          if found → leaf.Update(idx, encoded)
          else     → leaf.Insert(key, encoded)
        return leafMutation{newPageID: current.PageID()}
      → 一次 CAS 发布
    下一段继续（writeOperation 的 CAS retry 循环自动处理 split）
```

### 关键设计

1. **页面容量预估**：~90 entries/page (4096B ÷ 49B MVCC value)。`IsFull` 预检避免 Insert 失败回滚
2. **MVCC 编码在 mutate 内**：`BuildMVCC(FlagNormal, ts, value, prevFlag, prevTS, prevVal)` — prev 值从 BTree 旧值提取
3. **tryInPlace 优化**：分段内每个 Update 先检查 TryInPlace — 如果原位覆盖能成功，跳过后面的 COW+Insert
4. **与 `btreeStorageAdapter.SetBatch` 的差异**：适配器处理的是已编码值；BTree.SetBatch 需要在 mutate 闭包内做编码

### 实现伪代码

```go
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
    if len(pairs) == 0 { return nil }

    // Step 1: Sort keys (required for COW page sequential inserts)
    sort.Slice(pairs, func(i, j int) bool {
        return bytes.Compare(pairs[i].Key, pairs[j].Key) < 0
    })

    // Step 2: Segmented COW — writeOperation's built-in CAS retry loop
    // handles split. Each segment fills ~90 entries before CAS publish.
    offset := 0
    for offset < len(pairs) {
        segStart := offset
        err := writeOperation(b, pairs[segStart].Key, func(leaf LeafPage) (*leafMutation, error) {
            current := leaf
            var totalDelta int64
            consumed := 0

            for i := segStart; i < len(pairs); i++ {
                p := pairs[i]

                // Pre-check: if page is full for this key, stop the segment
                // MVCC header(12) + prevVal(0-50) + beginTS(8) + realVal = ~70B
                estValLen := len(p.Value) + mvcc.MVCCHeaderSize + 8
                if current.IsFull(len(p.Key), estValLen) {
                    break
                }

                idx, found := current.Search(p.Key)
                if found {
                    raw := current.GetValue(idx)
                    mvccVal, _ := mvcc.ParseMVCC(raw)
                    encoded, _ := mvcc.BuildMVCC(
                        mvcc.FlagNormal, b.tsGen.NextTS(), p.Value,
                        mvccVal.Flag, mvccVal.BeginTS, mvccVal.RealVal,
                    )
                    // TryInPlace first (same as BTree.Set)
                    if lh, ok := current.(*leafPageHandle); ok && lh.TryInPlace(idx, encoded) {
                        if mvccVal.IsTombstone() { totalDelta++ }
                        consumed++
                        continue
                    }
                    newLeaf, err := current.Update(idx, encoded)
                    if err != nil { return nil, err }
                    if mvccVal.IsTombstone() { totalDelta++ }
                    current = newLeaf
                } else {
                    encoded, _ := mvcc.BuildMVCC(
                        mvcc.FlagNormal, b.tsGen.NextTS(), p.Value,
                        0, 0, nil,
                    )
                    newLeaf, err := current.Insert(p.Key, encoded)
                    if err != nil { return nil, err }
                    current = newLeaf
                    totalDelta++
                }
                consumed++
            }

            offset += consumed
            return &leafMutation{
                newPageID: current.PageID(),
                delta:     totalDelta,
            }, nil
        })

        if err != nil { return err }
        if offset == segStart {
            // Safety: force-advance if no keys consumed (page already full)
            offset++
        }
    }

    return nil
}
```

### 与 `BTree.Set` 的一致性

| 维度 | `BTree.Set` (单个) | `BTree.SetBatch` (分段 COW) |
|------|------|------|
| MVCC 编码 | `BuildMVCC(flag, ts, val, 0, 0, nil)` | `BuildMVCC(flag, ts, val, prevFlag, prevTS, prevVal)` |
| InPlace 优化 | TryInPlace → 跳过 COW | 同左 |
| Tombstone recovery | delta+1, tombstoneDelta−1 | 同左 |
| CAS 发布 | writeOperation 内 | 同左（分段，每段一次 CAS） |
| Split 处理 | writeOperation → doSplitWithSplitting | **完全相同** — writeOperation 的 built-in split 逻辑不变 |

### 收益

| 项目 | 效果 |
|------|------|
| **Bug 修复** | ✅ 绕过 PageDispatcher split 数据丢失 |
| **性能** | batch-set-1024 预期 +60-100% (1024次COW → ~10次) |
| **代码** | 可删除 `batch_writer.go` + `page_dispatcher.go` + `write_operation_retry.go` (~500行) |
| **复杂度** | 调用链从 5 层 → 2 层 (SetBatch → writeOperation) |

### 实现工作量

~80 行（`BTree.SetBatch` 重写 + 排序 + IsFull 预检 + MVCC 编码 + tombstoneDelta 聚合）

### 风险

- **key 排序开销**：79 个 key 排序 ~79×log₂(79)≈500 次比较，< 10µs，可忽略
- **Split 页面切换**：`leaf.Insert` 满页返回 error → IsFull 预检防止。即使满页，writeOperation 的 CAS retry 循环自动处理
- **TryInPlace 和 COW 混用**：同一分段内 inPlace keys 跳过 COW → 不影响 totalDelta 累计 → 正确
- **`seq-put` 路径不受影响**：`BTree.Set` 不走 SetBatch，性能不变

### rollback 回退方案

如果分段 COW 方案有意外问题，可回退到当前 PD 实现并继续 debug PD split bug。分段 COW 替换只涉及 `btree.go:SetBatch` 函数，回退简单。
