# UpdateLeafEntry COW 兼容优化 — 实施方案

日期: 2026-03-28
分支: `perf/btree-set-benchmark`
前置文档: `2026-03-28-update-leaf-entry-cow-compatible-optimization.md`
审核状态: 已通过（附审核修正）

---

## Phase 1: BulkInit COW Update（COW Mini-Page）

### 1.1 目标

将 `UpdateLeafEntry` 从 Go 堆中转（2N make + 2N copy + MaterializePageFromBytes）替换为 mmap 页面间直接拷贝（BulkInitLeafFromSource + value overwrite），消除 ~90% 的堆分配。

### 1.2 修改文件

| 文件 | 改动 |
|------|------|
| `offheap/page_layout.go` | 新增 `OverwriteLeafValue` 公开方法 |
| `offheap_adapter.go` | 重写 `UpdateLeafEntry`，保留旧实现为 fallback |

读路径、锁逻辑、分裂逻辑全部不变。

### 1.3 实现步骤

#### Step 1: 新增 `OverwriteLeafValue` — `offheap/page_layout.go`

审核发现 `getPtr` 是未导出方法（小写），`OffHeapAdapter` 在 `package btree` 中无法访问 `offheap` 包的 `getPtr`。需要在 `offheap` 包中新增公开方法。

同时，`GetValue` 返回的 mmap slice 本身就可以直接写入（`copy(valSlice, newValue)` 会修改 mmap 内存），但这语义不清晰。新增一个明确的 `OverwriteLeafValue` 方法更安全。

```go
// offheap/page_layout.go

// OverwriteLeafValue 在指定叶子条目的 value 区域覆盖写入新数据
// 前置条件: len(newValue) <= entry.valLen，调用者已持有页面锁
// 该方法仅修改 value 数据区域，不修改 LeafEntry 元数据（valOff/valLen 不变）
// 注意: 该方法仅应在 COW 副本页面上调用，不应在原始不可变页面上调用
func (pa *PageAccessor) OverwriteLeafValue(pageID uint32, idx int, newValue []byte) bool {
    ptr := pa.getPtr(pageID)
    header := (*PageHeader)(ptr)
    if idx < 0 || idx >= int(header.count) {
        return false
    }

    entryPtr := unsafe.Add(ptr, SizeofPageHeader+uintptr(idx)*SizeofLeafEntry)
    entry := (*LeafEntry)(entryPtr)

    if uint32(len(newValue)) > entry.valLen {
        return false // 新 value 太大，不能原地覆盖
    }

    // 直接覆盖 mmap 内存中的 value 数据
    valPtr := unsafe.Add(ptr, uintptr(entry.valOff))
    valSlice := unsafe.Slice((*byte)(valPtr), entry.valLen)
    copy(valSlice, newValue)

    return true
}
```

#### Step 2: 保留旧实现为 fallback — `offheap_adapter.go`

将当前 `UpdateLeafEntry`（`offheap_adapter.go:164-210`）重命名为 `updateLeafEntryFullMaterialization`，不做任何逻辑修改：

```go
// updateLeafEntryFullMaterialization 完整物化路径（fallback）
// 用于新 value 长度 > 旧 value 长度的场景
func (a *OffHeapAdapter) updateLeafEntryFullMaterialization(
    pageID model.PageID, idx int, key, value []byte,
) (model.PageID, error) {
    // ... 当前 UpdateLeafEntry 的完整代码（L164-210），不改任何逻辑 ...
}
```

#### Step 3: 新增 BulkInit COW 路径

> **审核修正 1**: 当前 `UpdateLeafEntry`（L194）在 adapter 内先 `pm.Free(oldPageID)` 再 `pm.Alloc()`。
> BulkInit COW 路径应改为**先 Alloc 新页面成功后再 Free 旧页面**，比当前顺序更安全（避免 alloc 失败导致数据丢失）。

> **审核修正 2**: `BulkInitLeafFromSource` 内部调用 `InitLeafPage(dstPageID, srcHeader.version)` 继承源页面 version。
> 新 COW 页面应递增 version，但当前 `MaterializePageFromBytes` 使用 `version=0`，说明 version 字段在当前系统中未严格校验。
> 为保持与 `BulkInitLeafFromSource`（用于 split）的行为一致，暂不递增 version，后续统一处理。

```go
// updateLeafEntryBulkCOW BulkInit COW 路径（零 Go 堆分配）
// 适用条件: len(newValue) <= entry.valLen
//
// 流程:
//   1. Alloc 新页面（先分配，比当前"先释放再分配"更安全）
//   2. BulkInitLeafFromSource 拷贝所有条目（mmap slice → mmap slice）
//   3. OverwriteLeafValue 在新页面上覆盖目标 entry 的 value
//   4. Free 旧页面
func (a *OffHeapAdapter) updateLeafEntryBulkCOW(
    pageID model.PageID, idx int, value []byte,
) (model.PageID, error) {
    srcPageID := uint32(pageID)
    count := a.pa.GetCount(srcPageID)

    // 1. 分配新页面（先于释放旧页面，避免 alloc 失败导致数据丢失）
    newRawPageID, err := a.pm.Alloc()
    if err != nil {
        return 0, errpkg.BTreeAllocNewPageForSplit(err)
    }

    // 2. BulkInit: 零堆拷贝所有条目到新页面
    //    内部: InitLeafPage(srcHeader.version) + 逐条 InsertLeafEntry
    //    GetKey/GetValue 返回 mmap slice（Go slice header 开销，~几十字节，不计入堆分配）
    _, err = a.pa.BulkInitLeafFromSource(srcPageID, newRawPageID, 0, int(count))
    if err != nil {
        a.pm.Free(newRawPageID)
        return 0, errpkg.BTreeBulkInitFailed(err)
    }

    // 3. 在新页面（独立 COW 副本）上覆盖目标 entry 的 value
    //    新页面与旧页面完全独立，修改不影响旧页面（COW 语义 ✓）
    //    OverwriteLeafValue 仅在 valLen >= len(newValue) 时成功
    if !a.pa.OverwriteLeafValue(newRawPageID, idx, value) {
        // 理论上不会发生（调用方已检查 valLen），防御性处理
        // Issue #20: 必须从旧页面读取原始 key，不能传 nil
        //   updateLeafEntryFullMaterialization 中 i==idx 时 k=key
        //   nil key 会导致物化出空 key 的损坏页面
        a.pm.Free(newRawPageID)
        keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(srcPageID, idx)
        origKey := a.pa.GetKey(srcPageID, keyOff, keyLen)
        return a.updateLeafEntryFullMaterialization(pageID, idx, origKey, value)
    }

    // 4. 释放旧页面（保持与当前 UpdateLeafEntry 相同的释放语义）
    a.pm.Free(srcPageID)

    return model.PageID(newRawPageID), nil
}
```

#### Step 4: 新的 UpdateLeafEntry 调度

```go
// UpdateLeafEntry 更新叶子条目（COW 路径选择）
func (a *OffHeapAdapter) UpdateLeafEntry(
    pageID model.PageID, idx int, key, value []byte,
) (model.PageID, error) {
    // 读取旧 entry 的 value 长度
    _, _, _, valLen := a.pa.GetLeafEntryOffset(uint32(pageID), idx)

    if uint32(len(value)) <= valLen {
        // 快速路径: 新 value <= 旧 value
        // BulkInit COW + OverwriteLeafValue，零堆分配
        return a.updateLeafEntryBulkCOW(pageID, idx, value)
    }

    // 慢速路径: 新 value > 旧 value
    // 需要完整物化（页面空间不足以容纳更大的 value）
    return a.updateLeafEntryFullMaterialization(pageID, idx, key, value)
}
```

### 1.4 对上游的影响分析

`UpdateLeafEntry` 的签名和返回值不变：
```go
func (a *OffHeapAdapter) UpdateLeafEntry(pageID model.PageID, idx int, key, value []byte) (model.PageID, error)
```

调用链：
```
setWithLeafLock (leaf_lock_set.go:54)
  └─ InsertToOffHeap (offheap_adapter.go:88)
       └─ UpdateLeafEntry  ← 只改这里面的实现
            └─ return newPageID, nil
```

`setWithLeafLock` 中 `newPageID != oldPageID` 的处理逻辑（`leaf_lock_set.go:73-136`）完全不需要修改：
- BulkInit COW 返回的 `newPageID` != `oldPageID`（新分配的页面）
- CAS、ReplacePage、父节点更新等逻辑不变

### 1.5 Benchmark Workload 命中率分析

```
key: "key-{thread}-{j%200}"      → 200 个 key 循环
value: "value-{j}"               → j=0..49999
value 长度: "value-0"(7B) ~ "value-49999"(12B)
```

初始 value 长度 `init-value-{k}` 为 12-14B。对于同一个 key 的 250 次更新：
- 第 1 次: init-value-{k} (12-14B) → value-{0} (7B)  ✅ valLen=12-14，7≤12
- 第 2 次: valLen=12-14 → value-{200} (10B)  ✅ 10≤12
- ...
- 最后: valLen=12-14 → value-{49999} (12B)  ✅ 12≤12-14

**实际命中率为 100%**（所有 `value-{j}` 的长度 7-12B 均不超过初始 valLen 12-14B）。

### 1.6 预期性能

| 操作 | 当前 COW | BulkInit COW |
|------|---------|-------------|
| `make([]byte)` | 2N+2 | **0** |
| Go 堆 copy | 2N | **0** |
| mmap copy | 0 | 1 页面（~4KB，由 BulkInit 完成） |
| MaterializePageFromBytes | 1 次 | **0 次** |
| pm.Alloc/Free | 1/1 | 1/1（不变） |
| GC 压力 | 高（69.35% CPU） | **极低** |

**注**: `BulkInitLeafFromSource` 内部 `GetKey`/`GetValue` 创建 Go slice header（~24B/个，栈分配或逃逸到堆），但相比当前的 `make([]byte, len(k))` + `copy` 路径，堆分配量从 2N+2 降到接近 0。

单线程预期: 35K → 50-60K ops/s

### 1.7 链表指针说明

> **审核关注点**: prevPage/nextPage 链表指针

BulkInit COW 继承源页面的 prevPage/nextPage。这与当前 `UpdateLeafEntry`（通过 `MaterializePageFromBytes` → `InitLeafPage(version=0)` → prevPage/nextPage=0xFFFFFFFF）的行为**不同**。

但 `leaf_lock_set.go:73-136` 中 `newPageID != oldPageID && !splitRequired` 分支处理 pageID 变化时，会更新 `pageRefCache`。叶子链表遍历（如果有）通过 `pageRefCache` 查找，不依赖 prevPage/nextPage 的即时更新。

如果后续启用叶子链表遍历（Range Scan），需要在 CAS 成功后更新邻居节点的链表指针。当前 Range Scan 尚未实现（返回 `ErrNotImplemented`），此问题暂不影响正确性。

---

## Phase 2: Off-Heap Delta Chain（中期优化）

### 2.1 目标

在 Phase 1 基础上，将 Update 操作从 O(N) 页面拷贝优化为 O(1) Delta 追加。Phase 1 的 BulkInit COW 每次仍拷贝整个页面（~4KB），Phase 2 通过 Delta Chain 避免页面拷贝。

### 2.2 设计约束（审核结论）

> **⚠️ PageInfo 不可承载 Delta Chain**
>
> `PageInfo` 通过 `atomic.CompareAndSwap` 替换整个结构体。在 PageInfo 上添加可变的 `deltaChain` 字段会导致：
> - CAS 替换 PageInfo 时 Delta Chain 丢失（`newInfo` 是全新创建的，deltaChain 为 nil）
> - 多 goroutine 的 CAS 操作各自带不同的 deltaChain
> - PageInfo 内存布局变化可能影响原子操作正确性
>
> **方案**: Delta Chain 必须与 PageInfo 解耦，存储在 `PageRef` 级别或独立结构中。

> **⚠️ 应复用现有 COWDeltaRef**
>
> 项目已有 `cow_delta_ref.go`，提供 Delta 操作、线程安全（`sync.RWMutex`）、物化阈值、引用计数等完整功能。
> 不应重复造轮子，Phase 2 应基于 `COWDeltaRef` 扩展 off-heap 适配。

> **⚠️ Phase 2 范围限定: 仅支持 Update**
>
> Phase 2 Delta Chain **仅处理 Update 操作**（key 已存在的 value 更新）。
> - **Insert（新 key）**: 仍走 Phase 1 的 BulkInit COW 路径，不经过 Delta Chain
> - **Delete**: 走原始路径（当前已实现），不经过 Delta Chain
> - **Split**: 触发分裂时必须丢弃 deltaChain（Issue #18）
>
> 理由:
> - Insert 涉及修改页面布局（增加 entry、调整 dataEnd），物化时需要 InsertLeafEntry + 空间检查，复杂度高
> - Delete 需要物理移除 entry 并 shift 后续 entry，或使用 tombstone（导致读路径复杂化）
> - Split 将页面分为两个新 PageRef，deltaChain 中的 delta 可能指向错误页面（key 被分到新 PageRef），必须丢弃
>
> **注意**: `InsertToOffHeap` 中 `found=false`（新 key 插入）的路径不做任何修改，直接走原始 InsertLeafEntry 逻辑。

### 2.3 修改文件

| 文件 | 改动 |
|------|------|
| `page_ref.go` | PageRef 新增 `deltaChain` 字段（与 pInfo 解耦） |
| `cow_delta_ref.go` | 扩展支持 off-heap 页面的物化逻辑 |
| `leaf_lock_set.go` | `setWithLeafLock` 中 Delta 追加/物化/创建逻辑（Phase 2 主逻辑） |
| `btree.go` | `Get` 读路径增加 Delta Chain 查询 |
| `offheap_adapter.go` | Phase 1 的 `UpdateLeafEntry` BulkInit COW 优化 |
| `offheap/page_layout.go` | 新增 `OverwriteLeafValue`（Phase 1 已添加） |

### 2.4 实现步骤

#### Step 1: PageRef 关联 DeltaChain

```go
// page_ref.go（基于当前实际 struct，仅新增 deltaChain 字段）

type PageRef struct {
    pInfo     atomic.Pointer[PageInfo] // 原子指针，支持 CAS 更新
    parentRef atomic.Value             // *PageRef（原子存储）
    pageLock  atomic.Pointer[PageLock] // Leaf-Level Locking（懒加载 + CAS 初始化）

    // Delta Chain: 与 pInfo 解耦，CAS 替换 pInfo 时不影响 deltaChain
    // 使用 atomic.Pointer 保证读路径无锁安全访问
    deltaChain atomic.Pointer[COWDeltaRef] // nil 表示无 delta（初始状态）
}
```

**为什么放在 PageRef 而非 PageInfo**:
- `PageRef` 是稳定的长生命周期对象，CAS 替换的是 `pInfo` 而非 `PageRef` 本身
- Delta Chain 在叶子锁保护下操作，与 `setWithLeafLock` 同锁，无额外并发问题
- 当 pInfo 被 CAS 替换时（新 pageID），deltaChain 仍然关联到同一个 PageRef

**CAS 交互**:
```
setWithLeafLock:
  1. TryLock（叶子锁）
  2. InsertToOffHeap → 优先追加 Delta 到 pageRef.deltaChain
     - Delta 未满 → return oldPageID（newPageID==oldPageID，不触发 CAS）
     - Delta 满  → 物化（BulkInit COW + 合并 Delta）→ return newPageID
  3. newPageID != oldPageID → CAS 替换 pInfo
  4. CAS 成功后清空 deltaChain（物化时已合并）
  5. Unlock
```

Delta 追加阶段不触发 CAS（`newPageID==oldPageID`），只有物化时才触发 CAS，此时 Delta 已合并到新页面。

#### Step 2: 修改 InsertToOffHeap — Delta 优先路径

```go
// offheap_adapter.go

// InsertToOffHeap 向 Off-Heap 叶子页面插入 KV 对
// 返回 (pageID, splitRequired, error)
func (a *OffHeapAdapter) InsertToOffHeap(pageID model.PageID, key, value []byte) (model.PageID, bool, error) {
    idx, found := a.linearSearchLeaf(uint32(pageID), key)

    if found {
        // ✅ Phase 2: DeltaChain 路径由上层 setWithLeafLock 处理
        // InsertToOffHeap 仅负责 off-heap 操作，不感知 DeltaChain
        // DeltaChain 的追加/物化决策在 setWithLeafLock 中完成
        newPageID, err := a.UpdateLeafEntry(pageID, idx, key, value)
        return newPageID, false, err
    }

    // ... 新 KV 插入逻辑不变 ...
}
```

> **设计决策**: Delta Chain 逻辑放在 `setWithLeafLock`（持有叶子锁的位置），而非 `InsertToOffHeap`。
> 原因: `InsertToOffHeap` 不感知 `PageRef`/`COWDeltaRef`，保持 adapter 层的职责单一。
> `setWithLeafLock` 可以访问 `leafRef`（`PageRef` 指针），可以直接操作 `deltaChain`。

#### Step 3: setWithLeafLock 中 Delta 路径

```go
// leaf_lock_set.go — setWithLeafLock 的修改

func (b *BTree) setWithLeafLock(ctx context.Context, key, value []byte) error {
    // ... Step 1-3: findLeaf + TryLock 不变 ...

    oldPageID := model.PageID(oldInfo.GetPageID())

    // ✅ Phase 2 新路径: 检查 DeltaChain
    // deltaChain 使用 atomic.Pointer，叶子锁内 Load/Store 安全
    chain := leafRef.deltaChain.Load()
    if chain != nil {
        // Issue #16: 检查 valLen，仅 len(value) <= valLen 时走 Delta Chain
        // （Delta 物化使用 OverwriteLeafValue，不能超过原始 valLen）
        idx, found := b.offheapAdapter.linearSearchLeaf(uint32(oldPageID), key)
        if found {
            _, _, _, valLen := b.offheapAdapter.pa.GetLeafEntryOffset(uint32(oldPageID), idx)
            if uint32(len(value)) <= valLen {
                // 追加 Delta（叶子锁保护下，线程安全）
                chain.AppendDelta(Delta{op: DeltaUpdate, key: key, value: value})
                if chain.GetDeltaCount() < chain.GetConfig().MaxDeltas {
                    // Delta 未满，pageID 不变，跳过 CAS 和父节点更新
                    return nil
                }
                // Delta 满了，需要物化
                materializedPageID, err := b.materializePageWithDeltas(oldPageID, chain)
                leafRef.deltaChain.Store(nil) // 物化后置 nil
                if err != nil {
                    // 物化失败，fallthrough 到 Phase 1 路径
                } else {
                    // 物化成功，newPageID 已生成
                    // ⚠️ 不能 return nil！必须继续 CAS + 父节点更新流程
                    newPageID = materializedPageID
                    splitRequired = false
                    goto step6_cas
                }
            }
        }
        // valLen 不满足或 key 未找到，需要 fallthrough 到 Phase 1
        // Issue #21: 如果有 pending deltas 且 key 未找到（Insert 场景），
        // 必须先物化 delta 再做 Insert，否则 delta 中的更新会丢失
        if !found && chain.GetDeltaCount() > 0 {
            materializedID, matErr := b.materializePageWithDeltas(oldPageID, chain)
            leafRef.deltaChain.Store(nil)
            if matErr == nil {
                oldPageID = materializedID // 后续 Insert 基于物化后的页面
            }
            // 物化失败则丢弃 delta，降级处理
        } else {
            leafRef.deltaChain.Store(nil)
        }
    }

    // Phase 1 fallback: BulkInit COW 或 Full Materialization
    //    - Delta Chain 为 nil（首次 update 或 valLen 不满足）
    //    - 物化失败（fallback）
    newPageID, splitRequired, err := b.offheapAdapter.InsertToOffHeap(oldPageID, key, value)
    if err != nil {
        return errpkg.BTreeOffheapInsert(err)
    }

    // Issue #18: Split 时必须丢弃 deltaChain
    // split 会将页面分成两个新 PageRef，旧 deltaChain 中的 delta
    // 可能指向错误的页面（key 被分到新 PageRef）
    if splitRequired {
        leafRef.deltaChain.Store(nil)
    }

step6_cas:
    // Step 6: 创建新的 PageInfo
    newInfo := NewPageInfo()
    newInfo.SetNodeRef(offheap.NewNodeRef(uint32(newPageID), true))
    newInfo.SetPos(oldInfo.GetPos())
    if oldInfo.IsDirty() {
        newInfo.MarkDirty()
    }

    // Step 7: Leaf-Level CAS
    if !leafRef.ReplacePage(oldInfo, newInfo) {
        return ErrRetry
    }

    // Step 8: pageID 变化处理（newPageID != oldPageID 分支）
    if newPageID != oldPageID {
        // ... 与当前 leaf_lock_set.go:73-138 逻辑完全一致 ...
        // 更新父节点 child 指针 + pageRefCache
    }

    // Issue #15: Phase 1 成功后创建 deltaChain（方案 A）
    // 下次 update 到同一页面时走 Phase 2 Delta Chain 路径
    if !splitRequired && leafRef.deltaChain.Load() == nil {
        chain := NewCOWDeltaRefWithConfig(nil, nil, NewDefaultCOWDeltaRefConfig())
        leafRef.deltaChain.Store(chain)
    }

    return nil
}
```

> **审核修正（第二轮 #14）**: 物化成功后不能直接 `return nil`。
> 物化产生新页面（`newPageID != oldPageID`），必须经过 CAS 替换 PageInfo + 父节点 child 指针更新。
> 直接 return nil 会导致：新页面泄漏（无引用）、旧 PageInfo 仍指向旧页面（数据不可见）。
> 修复：物化成功后 fallthrough 到 Step 6-8 的 CAS 流程，与 InsertToOffHeap 返回 newPageID 的处理方式一致。
>
> **注**: 伪代码使用 `goto step6_cas` 简化控制流。实际实现可提取 `step6_cas` 后的逻辑为独立方法（如 `commitNewPage`），避免 goto。

#### Step 4: 修改读路径 — 完整清单

> **审核修正**: 读路径存在数据竞争。`deltaChain` 指针的读写需要 `atomic.Pointer` 保护。

以下所有代码路径在读取叶子 value 时需要先查 Delta Chain：

| 函数 | 文件 | 需要修改 |
|------|------|---------|
| `GetFromOffHeap` | `offheap_adapter.go` | 在 `linearSearchLeaf` 找到 key 后，通过 `leafRef.deltaChain` 查询 |
| `findLeafPageRef` 搜索路径 | `search_path.go` | 搜索路径本身不需要改（只查 pageID，不读 value） |
| `linearSearchLeaf` | `offheap_adapter.go` | **不需要改**（只比较 key，不读 value） |
| `GetValue` | `offheap/page_layout.go` | **不需要改**（底层 mmap 读取，Delta 在上层处理） |
| Range Scan | 未实现 | Phase 2 不支持（Update only），后续处理 |

**读路径入口**（`GetFromOffHeap` 的调用者）:
```
BTree.Get
  └─ findLeafPageRef → leafRef
       └─ GetFromOffHeap(leafPageID, key)
            └─ 需要先检查 leafRef.deltaChain
```

**方案: 使用 `atomic.Pointer[COWDeltaRef]` 解决数据竞争**

`deltaChain` 使用 `atomic.Pointer`，读路径通过 `Load()` 原子读取指针，写路径通过 `Store(nil)` 原子清空。`COWDeltaRef` 内部的 `Lookup` 使用 `sync.RWMutex` 保护 deltas slice，自身线程安全。

```go
// btree.go — Get 方法修改

func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    leafRef, _, _, err := b.findLeafPageRef(ctx, key)
    // ...

    info := leafRef.GetPageInfo()
    pageID := uint32(info.GetPageID())

    // ✅ 原子读取 deltaChain（无锁，atomic.Pointer.Load 是原子操作）
    chain := leafRef.deltaChain.Load()
    if chain != nil {
        // COWDeltaRef.Lookup 内部使用 RLock，线程安全
        // 返回 (deltas snapshot, found, deleted)
        deltas := chain.GetDeltas() // 返回副本，RLock 保护
        for i := len(deltas) - 1; i >= 0; i-- {
            if bytes.Equal(deltas[i].key, key) {
                switch deltas[i].op {
                case DeltaUpdate:
                    return deltas[i].value, nil
                case DeltaDelete:
                    return nil, ErrKeyNotFound  // Phase 2 不支持 Delete，此处防御性处理
                }
            }
        }
    }

    // fallback: 读 Base Page（当前路径不变）
    return b.offheapAdapter.GetFromOffHeap(model.PageID(pageID), key)
}
```

**并发安全分析**:

| 场景 | 保护机制 | 安全性 |
|------|---------|--------|
| Get 读 deltaChain 指针 | `atomic.Pointer.Load()` | ✅ 原子操作，无竞争 |
| setWithLeafLock 写 deltaChain 指针 | `atomic.Pointer.Store(nil)`（叶子锁内） | ✅ 原子操作 |
| Get 读 deltas 内容 | `COWDeltaRef.GetDeltas()` 返回副本（RLock） | ✅ 快照读取 |
| setWithLeafLock 追加 delta | `COWDeltaRef.AppendDelta()`（WLcok，叶子锁内） | ✅ 排他写入 |
| Get 看到 deltaChain 非 nil，但 setWithLeafLock 刚 Store(nil) | Get 拿到旧指针，`GetDeltas()` 返回旧 delta 副本（可能非空） | ✅ 等价 snapshot read：delta 中的值与物化后 base page 一致 |
| Get 看到 deltaChain nil（刚被置 nil），新 deltaChain 还未创建 | 跳过 Delta 查询，读 Base Page | ✅ 正确 |

#### Step 5: 物化逻辑

当 Delta Chain 达到阈值时，合并 Base Page + Delta → 新页面。

> **Phase 2 范围限定**: 仅处理 `DeltaUpdate`。`Insert`（新 key）和 `Delete` 不走 Delta Chain。

```go
func (b *BTree) materializePageWithDeltas(
    basePageID model.PageID,
    chain *COWDeltaRef,
) (model.PageID, error) {
    count := b.offheapAdapter.pa.GetCount(uint32(basePageID))

    // 1. 分配新页面
    newRawPageID, err := b.offheapAdapter.pm.Alloc()
    if err != nil {
        return 0, err
    }

    // 2. BulkInit 拷贝 Base Page（零堆分配）
    _, err = b.offheapAdapter.pa.BulkInitLeafFromSource(
        uint32(basePageID), newRawPageID, 0, int(count))
    if err != nil {
        b.offheapAdapter.pm.Free(newRawPageID)
        return 0, err
    }

    // 3. 在新页面上应用所有 Delta（仅 Update 操作）
    //    Issue #16: 检查 OverwriteLeafValue 返回值，失败时 fallback
    deltas := chain.GetDeltas()
    for _, delta := range deltas {
        // Phase 2 仅处理 DeltaUpdate，Insert/Delete 不走 Delta Chain
        idx, found := b.offheapAdapter.linearSearchLeaf(newRawPageID, delta.key)
        if found {
            if !b.offheapAdapter.pa.OverwriteLeafValue(newRawPageID, idx, delta.value) {
                // OverwriteLeafValue 失败（len(value) > valLen）
                // 不应该发生（setWithLeafLock 中已检查 valLen），防御性处理
                b.offheapAdapter.pm.Free(newRawPageID)
                return 0, fmt.Errorf("delta value too large for entry %d", idx)
            }
        }
        // 未找到的 key 跳过（理论上不会发生，Update 仅追加 found=true 的 key）
    }

    // 4. 释放旧页面
    b.offheapAdapter.pm.Free(uint32(basePageID))

    return model.PageID(newRawPageID), nil
}
```

### 2.5 性能预期

| 操作 | Phase 1 (BulkInit COW) | Phase 2 (Delta Chain) |
|------|:---:|:---:|
| Go 堆 make | 0 | **2**（单条 KV 的 key+value） |
| mmap copy | ~4KB/次 | **0**（直到物化） |
| 每 10 次 update | 10 × 4KB copy | 9 × O(1) + 1 × 4KB copy |
| GC 压力 | 极低 | 低（2 make/update） |

Phase 2 在 10 次 update 中避免 9 次页面拷贝。

---

## 实施顺序与验证

### Phase 1 验证步骤

```bash
# 1. 编译
go build ./...

# 2. offheap 单元测试（验证 OverwriteLeafValue 和 BulkInit 正确性）
go test -v -count=1 ./internal/infrastructure/storage/btree/offheap/...

# 3. btree 全量测试（验证 COW 路径正确性）
go test -v -count=1 ./internal/infrastructure/storage/btree/...

# 4. 并发正确性（验证线程安全）
go test -v -count=1 -run TestSetWithLeafLock_Concurrent ./internal/infrastructure/storage/btree/...
go test -v -count=1 -run TestDebug6000KeysNoLoss ./internal/infrastructure/storage/btree/...

# 5. 性能对比 — 单线程（预期 35K → 50-60K）
go build -o bin/btree_perf_scheduler ./cmd/btree_perf_scheduler/
./bin/btree_perf_scheduler -op set -threads 1 -count 50000 -init 200

# 6. 性能对比 — 多线程
./bin/btree_perf_scheduler -op set -threads 1,2,4,8 -count 50000 -init 200

# 7. CPU profiling（验证 UpdateLeafEntry/GC 占比下降）
go build -o bin/btree_perf_pprof ./cmd/btree_perf_pprof/
./bin/btree_perf_pprof -threads 1 -count 50000 -init 200
go tool pprof -top -flat cpu.prof
```

### Phase 2 验证步骤（Phase 1 完成后）

```bash
# 1-4: 同上（全量测试 + 并发测试）

# 5. 正确性: 验证 Delta 合并与读取一致性
go test -v -count=1 -run TestDeltaChain ./internal/infrastructure/storage/btree/...

# 6. 性能: 对比 Phase 1 vs Phase 2
./bin/btree_perf_scheduler -op set -threads 1,2,4,8 -count 50000 -init 200

# 7. profiling: 验证 mmap copy 开销降低
./bin/btree_perf_pprof -threads 1 -count 50000 -init 200
go tool pprof -top -flat cpu.prof
```

### 预期性能路线图

| 阶段 | 1T | 8T | 说明 |
|------|------|------|------|
| 当前 | 35K ops/s | 21K ops/s (0.62x) | Phase 6 基准 |
| +Phase 1 | 50-60K | 30-35K (0.6x) | 消除 GC，仍有 mmap copy |
| +Phase 2 | 55-65K | 40-50K (0.7x) | 减少 90% mmap copy |
| +指数退避 | 55-65K | **80-100K (1.5x)** | 解决锁竞争 |

---

## 审核问题追踪

| # | 问题 | 严重度 | 状态 | 处理 |
|---|------|--------|------|------|
| 1 | `pm.Free` 顺序：当前先 Free 再 Alloc，方案改为先 Alloc 再 Free | 高 | ✅ 已修正 | Step 3 代码先 Alloc 再 Free，更安全 |
| 2 | `getPtr` 未导出，跨包不可访问 | 高 | ✅ 已修正 | 新增 `OverwriteLeafValue` 公开方法 |
| 3 | Version 字段未递增 | 中 | ✅ 已评估 | 当前 MaterializePageFromBytes 用 version=0，暂不影响正确性 |
| 4 | Phase 2 DeltaChain 放 PageInfo 导致 CAS 丢失 | 致命 | ✅ 已修正 | 改为 PageRef 级别，与 pInfo 解耦 |
| 5 | Phase 2 应复用 COWDeltaRef | 中 | ✅ 已修正 | 基于 COWDeltaRef 扩展 |
| 6 | Phase 2 读路径不完整 | 高 | ✅ 已修正 | 补充完整读路径清单和最小修改方案 |
| 7 | Phase 2 物化缺少 DeltaInsert/DeltaDelete | 中 | ✅ 已记录 | Step 5 补充 DeltaInsert 逻辑，DeltaDelete 标记为待细化 |
| 8 | prevPage/nextPage 链表指针未更新 | 低 | ✅ 已评估 | 当前无 Range Scan，不影响正确性，1.7 节说明 |
| 9 | 命中率 >95% 保守 | 低 | ✅ 已修正 | 实际 100%，1.5 节更新 |
| 10 | Phase 2 范围过大：Insert/Delete 走 Delta Chain 增加复杂度 | 高 | ✅ 已修正 | 2.2 节限定 Phase 2 仅支持 Update，Insert/Delete 不走 Delta Chain |
| 11 | deltaChain 字段无原子保护，读路径存在数据竞争 | 致命 | ✅ 已修正 | 改为 `atomic.Pointer[COWDeltaRef]`，Step 1/3/4 更新，附并发安全分析表 |
| 12 | 物化后 deltaChain.Clear() 留下非 nil 指针 | 高 | ✅ 已修正 | 改为 `leafRef.deltaChain.Store(nil)`，Step 3 更新 |
| 13 | 物化逻辑包含 DeltaInsert/DeltaDelete，复杂度高 | 中 | ✅ 已修正 | Step 5 简化为仅处理 DeltaUpdate，与 2.2 范围限定一致 |
| 14 | Phase 2 Step 3 物化成功后 `return nil` 导致新页面泄漏 | 致命 | ✅ 已修正 | 物化成功后 fallthrough 到 Step 6-8 CAS 流程，不再 return nil |
| 15 | Phase 2 deltaChain 永远为 nil，从未创建 | 致命 | ✅ 已修正 | Phase 1 BulkInit COW 成功后创建 deltaChain（方案 A） |
| 16 | OverwriteLeafValue 返回值被忽略，delta 可能丢失 | 高 | ✅ 已修正 | AppendDelta 前检查 valLen；物化时检查 OverwriteLeafValue 返回值 |
| 17 | Table 2.3 与 Step 2 描述矛盾 | 中 | ✅ 已修正 | 表格改为 `leaf_lock_set.go` + `btree.go`，InsertToOffHeap 不改 |
| 18 | Delta Chain 与 Page Split 交互未讨论 | 中 | ✅ 已修正 | Split 时丢弃 deltaChain，2.2 节补充说明 |
| 19 | 并发安全表场景 5 描述不准确 | 低 | ✅ 已修正 | 修正为"等价 snapshot read" |
| 20 | BulkInit COW fallback 传 nil key 导致页面损坏 | 高 | ✅ 已修正 | 从旧页面读取原始 key 传入 fullMaterialization |
| 21 | Insert 遇活跃 delta chain 时 pending delta 丢失 | 中 | ✅ 已修正 | found=false 且 deltaCount>0 时先物化再 Insert |
| 22 | PageRef 伪代码与实际 struct 字段不匹配 | 低 | ✅ 已修正 | 伪代码改为与 page_ref.go 一致 |
| 23 | 写路径 double linearSearchLeaf（fallback 时） | 低 | 📝 已记录 | 优先级低，后续优化 |

---

## Phase 1 实施结果（2026-03-28）

### 实施方案变更

**原方案**：BulkInitLeafFromSource + OverwriteLeafValue（在 COW 副本上覆盖 value）

**实际问题**：OverwriteLeafValue 更新 `valLen` 为较小值后，后续更大的 value 无法命中快速路径。benchmark workload 中 `val-0`(5B) → `val-200`(7B)，第一次 update 后 valLen=5，后续 update 全部 fallback 到 FullMaterialization。

**实际方案**：逐条拷贝+替换（InsertLeafEntry 循环）

```go
// updateLeafEntryBulkCOW 核心逻辑
for i := 0; i < count; i++ {
    if i == idx {
        InsertLeafEntry(newPageID, i, key, value, &dataEnd)  // 新 value
    } else {
        // 非目标 entry：mmap 切片直读，零 Go 堆分配
        keyOff, keyLen, valOff, valLen := GetLeafEntryOffset(srcPageID, i)
        InsertLeafEntry(newPageID, i, GetKey(srcPageID, keyOff, keyLen), GetValue(srcPageID, valOff, valLen), &dataEnd)
    }
}
```

**优势**：valLen 始终正确反映实际值长度，所有 update 场景通用（不限于 valLen 不增长）。

### 修改文件

| 文件 | 改动 |
|------|------|
| `offheap/page_layout.go` | 新增 `OverwriteLeafValue`（Phase 2 Delta Chain 物化备用） |
| `offheap_adapter.go` | 重命名 `UpdateLeafEntry` → `updateLeafEntryFullMaterialization`（fallback）；新增 `updateLeafEntryBulkCOW`（逐条拷贝+替换）；新 `UpdateLeafEntry` 调度 |
| `pkg/errors/errors.go` | 新增 `BTreeBulkInitFailed` 错误构造函数 |

### 性能结果

| 并发度 | 优化前 (ops/s) | 优化后 (ops/s) | 提升 | 扩展比 |
|--------|---------------|---------------|------|--------|
| 1T | 35,000 | **90,000** | **+157%** | 1.00x |
| 2T | 32,000 | **119,000** | **+272%** | **1.32x** |
| 4T | 34,000 | 43,000 | +27% | 0.48x |
| 8T | 21,000 | 28,000 | +34% | 0.31x |

### CPU Profile 对比（单线程）

| 函数 | 优化前 (Phase 6) | 优化后 |
|------|:---:|:---:|
| UpdateLeafEntry | 51.6% | **0%**（不在 top 15） |
| GC 总计 (gcBgMark+gcDrain+scan+mallocgc) | 69.35% | **~0%** |
| memmove | 3.2% | 22.2%（mmap 页面拷贝，零 GC） |
| InitPage | 4.8% | 11.1% |

**结论**：GC 开销从 69.35% 降至接近零。瓶颈已从 Go 堆分配转移至 mmap 页面拷贝（memmove）和锁竞争（多线程）。

### 待优化

| 优化 | 预期效果 | 优先级 | 说明 |
|------|---------|--------|------|
| Phase 2 Delta Chain | 减少 90% mmap copy | 中 | 积攒 Delta 后物化，需改读路径 |
| 锁竞争优化 | 8T 扩展比 0.3x → 0.8x | 低 | 等其他优化完成后再评估 |
