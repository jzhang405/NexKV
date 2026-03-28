# UpdateLeafEntry COW 兼容优化方案评估

日期: 2026-03-28
分支: `perf/btree-set-benchmark`
背景: Phase 6 pprof 显示 UpdateLeafEntry COW 占 51.6% CPU，GC 总开销 69.35%

## 1. 问题陈述

### 当前 COW 路径开销

`UpdateLeafEntry`（`offheap_adapter.go:164-210`）每次 value 更新：

```
1. 收集全部 KV → Go 堆    2N make([]byte) + 2N copy
2. pm.Free(oldPageID)       释放旧页面
3. pm.Alloc()               分配新页面
4. MaterializePageFromBytes 重新物化所有条目
```

**Benchmark workload**: 200 个 key 反复更新 250 次（`j%initCount`），value 长度 7-12B。
**命中路径**: `InsertToOffHeap` → `linearSearchLeaf` → `found=true` → `UpdateLeafEntry`（COW 全量复制）。

### In-Place Update 被否决

In-place 直接覆盖 mmap 中的 value 字节虽然性能最优（零分配），但**破坏 COW 语义**：
- COW 的核心保证：页面一旦创建，内容不可变
- 快照/并发读依赖页面不可变性
- In-place 覆写导致旧快照读者看到中间状态

**需要 COW 兼容的方案。**

## 2. 方案评估

### 方案 A: Delta Chain 内嵌 4K 页面（COW + Delta Overlay）

来源: `thoughts/2026-03-28-off-heep-delta-chain.md`

**核心思路**: 在页面空闲区存储 Delta 记录，读操作先查 Delta 再查 Base Entry。

```
┌──────────────────────────────────────────────┐
│ PageHeader (32B)                              │
│   count: uint16, deltaCount: uint8           │
│   version: uint64, dataEnd: uint16           │
│   deltaEnd: uint16                           │
├──────────────────────────────────────────────┤
│ LeafEntry[0..n-1] (16B × n)  ← Base Entries │
├──────────────────────────────────────────────┤
│                                              │
│ KV Data (从 PageSize 向低地址增长)            │
│                                              │
├──────────────────────────────────────────────┤
│ Delta Region (从 dataEnd 向低地址增长)        │
│ Delta[0]: entryIdx=2, valOff, valLen         │
│ Delta[1]: entryIdx=5, valOff, valLen         │
└──────────────────────────────────────────────┘
```

**写操作**:
1. 不修改 Base Entry
2. 在 Delta Region 追加一条 Delta 记录（entryIdx + newVal 的 offset/len）
3. 新 value 写入 KV Data 空闲区
4. 递增 `deltaCount` + `version`

**读操作**:
1. 扫描 Delta Region（从最新到最旧）
2. 找到匹配 entryIdx 的 Delta → 返回 Delta 中的 value
3. 未找到 → 读 Base Entry 的 value

**Compaction**: deltaCount 超过阈值时，分配新页面，将 Base + Delta 合并物化到新页面。

#### 方案 A 评估

| 维度 | 评价 |
|------|------|
| COW 兼容性 | **不兼容**。修改了同一个页面的内容（追加了 Delta），违反页面不可变语义 |
| 快照安全 | **不安全**。旧快照读者可能看到新追加的 Delta（同一 mmap 页面） |
| 实现复杂度 | **高**。需要修改 PageHeader 结构、所有读路径、Compaction 逻辑 |
| 读放大 | **有**。每次读取额外扫描 Delta Region |
| 空间限制 | **受限于页面空闲区**。4KB 页面空闲空间通常 <1KB，Delta 容量有限 |

**结论**: ❌ 不可行。Delta 内嵌页面的本质是在原页面上追加修改，**仍然违反 COW 不可变语义**。对于 off-heap mmap 页面，这和 in-place update 是同一类问题。

---

### 方案 B: COW Mini-Page（轻量级页面复制）

**核心思路**: 复制页面但使用 BulkInit 零堆分配，而非当前的 Go 堆中转。

当前路径（2N 堆分配）:
```
旧页面 → make([]byte) × 2N → Go 堆 → MaterializePageFromBytes → 新页面
```

优化路径（0 堆分配）:
```
旧页面 → BulkInitLeafFromSource → 新页面（mmap 直接拷贝）
```

**实现**:

```go
func (a *OffHeapAdapter) UpdateLeafEntryBulk(pageID model.PageID, idx int, key, value []byte) (model.PageID, error) {
    count := a.pa.GetCount(uint32(pageID))

    // 1. 分配新页面
    newPageID, err := a.pm.Alloc()
    if err != nil {
        return 0, err
    }

    // 2. BulkInit: 零堆拷贝所有条目到新页面
    _, err = a.pa.BulkInitLeafFromSource(uint32(pageID), newPageID, 0, int(count))
    if err != nil {
        a.pm.Free(newPageID)
        return 0, err
    }

    // 3. 在新页面上更新目标 entry 的 value
    //    新页面是独立的 COW 副本，修改不影响旧页面
    entry := a.pa.GetLeafEntry(newPageID, idx)
    if uint32(len(value)) <= entry.valLen {
        // 新 value <= 旧 value: 直接覆盖新页面上的 value
        ptr := a.pa.GetPtr(newPageID)
        valPtr := unsafe.Add(ptr, entry.valOff)
        valSlice := unsafe.Slice((*byte)(valPtr), entry.valLen)
        copy(valSlice, value)
    } else {
        // 新 value > 旧 value: 需要重新物化（或删除旧 entry + 重新插入）
        // fallback 到当前路径
        a.pm.Free(newPageID)
        return a.UpdateLeafEntry(pageID, idx, key, value)
    }

    // 4. 释放旧页面（延迟释放更安全）
    a.pm.Free(uint32(pageID))

    return model.PageID(newPageID), nil
}
```

#### 方案 B 评估

| 维度 | 评价 |
|------|------|
| COW 兼容性 | **完全兼容**。新页面是独立副本，旧页面不可变 |
| 快照安全 | **安全**。旧快照读者继续引用旧页面，新读者看到新页面 |
| 实现复杂度 | **低**。复用已有 BulkInitLeafFromSource，仅新增 UpdateLeafEntryBulk |
| 读放大 | **无**。新页面与普通页面布局完全一致 |
| 性能预期 | **中等**。消除了 2N 堆分配，但仍有页面 Alloc/Free 开销 |

**关键限制**: BulkInit 拷贝所有条目，包括不需要修改的条目。这仍然是 O(N) 的数据拷贝，只是从 Go 堆拷贝变成了 mmap 拷贝。对于 200 条目/页面的场景，需要拷贝 ~4KB 数据（整个页面）。

**注**: 项目已实现 `BulkInitLeafFromSource`（`page_layout.go`），可直接复用。

---

### 方案 C: COW + Delta Page（独立 Delta 页面，Lealone 风格）

来源: `thoughts/2026-03-28-ccow-delta-chain-analysis.md`，参考 Lealone 架构

**核心思路**: 在 Base Page 之外维护独立的 Delta Chain（Go 堆或独立 off-heap 页面），COW 只复制 Base Page + Delta Chain 的引用。

```
写操作:
1. 在 Delta Chain 追加一条 Delta（内存中）
2. 不复制 Base Page
3. 读取时合并 Base Page + Delta Chain

Delta Chain 满时:
1. 分配新页面
2. 合并 Base + Delta → 物化到新页面
3. CAS 替换页面引用
```

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│ Base Page    │     │ Delta[0]     │     │ Delta[1]     │
│ (off-heap)   │────→│ key,val      │────→│ key,val      │
│ 不可变        │     │ (Go 堆/独立) │     │ (Go 堆/独立) │
└──────────────┘     └──────────────┘     └──────────────┘
      ↑                                          │
      │           读取时合并 ←────────────────────┘
```

**与 NexKV 现有架构的关系**:

NexKV 已有 `COWDeltaRef`（`cow_delta_ref.go`）：
```go
type COWDeltaRef struct {
    sharedKeys   [][]byte     // 共享的 Base Keys
    sharedValues [][]byte     // 共享的 Base Values
    deltas       []Delta      // 增量操作链
    refCount     atomic.Int32 // 引用计数
    version      atomic.Uint64
}
```

但 `COWDeltaRef` 工作在 Go 堆的 `keys/values` 切片上，与 off-heap 页面是**两套独立体系**。当前 off-heap 路径（`InsertToOffHeap`）不使用 `COWDeltaRef`。

**要让 Delta Chain 与 off-heap COW 协同**:

```go
// 新结构: OffHeapDeltaChain
type OffHeapDeltaChain struct {
    basePageID uint32        // 指向 off-heap Base Page（不可变）
    deltas     []Delta       // 增量操作（Go 堆分配，但只分配单条 KV）
    mu         sync.RWMutex  // 保护 delta chain
    version    atomic.Uint64
    threshold  int           // 物化阈值（默认 10）
}

type Delta struct {
    Op    DeltaOp    // Insert/Update/Delete
    Key   []byte     // 单条 KV 的 key
    Value []byte     // 单条 KV 的 value
}
```

**写操作（Update 路径）**:
```
setWithLeafLock
  └─ InsertToOffHeap
       └─ linearSearchLeaf → found=true
            └─ 不再调用 UpdateLeafEntry（COW 全量复制）
            └─ 改为: AppendDelta(leafRef, key, value)  ← 仅追加一条 Delta
                 └─ 成功 → return pageID, false, nil   ← newPageID==oldPageID
                 └─ Delta 满了 → MaterializeWithDeltas → COW 复制一次
```

**读操作（Get 路径）**:
```
Get
  └─ findLeafPageRef
       └─ 从 leafRef 获取 DeltaChain
            ├─ 有 Delta → 查 Delta Chain（从新到旧），然后 fallback 到 Base Page
            └─ 无 Delta → 直接读 Base Page（当前路径，零开销）
```

#### 方案 C 评估

| 维度 | 评价 |
|------|------|
| COW 兼容性 | **完全兼容**。Base Page 不可变，Delta 是独立结构 |
| 快照安全 | **需要设计**。Delta Chain 需要与 PageInfo 生命周期绑定 |
| 实现复杂度 | **高**。需要修改读路径、写路径、PageInfo 结构、物化逻辑 |
| 写性能 | **最优**。Update = 追加一条 Delta（O(1)），只有 2 次 make（key+value） |
| 读性能 | **有退化**。每次 Get 需扫描 Delta Chain |
| 内存开销 | **可控**。Delta 阈值 10 条，物化后释放 |

**关键问题**:

1. **Delta Chain 存储在哪里**:
   - 方案 C1: Go 堆（`[]Delta`）— 简单但引入 GC 压力
   - 方案 C2: 独立 off-heap 小页面 — 避免堆分配但实现复杂
   - 方案 C3: 混合 — Delta 元数据在堆，value 数据在 off-heap

2. **并发安全**:
   - Delta Chain 追加需要锁（与叶子锁复用）
   - Delta Chain 读取需要与 CAS 协调（ReplacePage 时 Delta Chain 如何迁移）

3. **读路径修改范围大**:
   - `GetValue` 需要感知 Delta Chain
   - `linearSearchLeaf` 需要合并 Delta 中的 Insert/Delete
   - 所有遍历页面的代码路径都需要适配

---

### 方案 D: COW Mini-Page + Value-Only Copy（方案 B 的改进版）

**核心思路**: 结合方案 B 的 BulkInit，但优化为**只复制 value 区域中被修改的部分**。

当前 BulkInit 复制整个页面（~4KB）。但 Update 只修改一个 entry 的 value（7-12B），理论上只需要复制被修改 value 所在的尾部数据区域。

**页面布局分析**:

```
┌────────────────────────────────────────────────┐
│ Header(32B) │ Entry[0]..Entry[N-1] │ KV Data  │
│             │ (从前往后增长)         │(从后往前) │
└────────────────────────────────────────────────┘
```

Value 更新时，新 value 可以放到 KV Data 区域的空闲空间（向前增长），然后修改 Entry[idx].valOff/valLen 指向新位置。

**但**: Entry 数组也在页面中，修改 `valOff/valLen` 就是修改页面内容，违反 COW。

**所以**: 仍然需要 COW 复制。但可以优化为**部分复制**:
- 复制 Header + Entry 数组（32 + 16×N 字节，通常 <400B）
- 复制被修改 value 的新值（7-12B）
- **不复制**其他 value 和 key（通过 offset 引用旧页面的数据）

**问题**: 新旧页面共享 KV Data 区域（mmap 同一区域），释放旧页面后数据可能被覆盖。

**结论**: 不可行。off-heap 页面通过 pageManager 管理，释放后可能被重用，共享数据区域不安全。

---

## 3. 方案对比

| 维度 | A: Delta 内嵌 | B: COW Mini-Page | C: COW + Delta Chain | D: Value-Only Copy |
|------|:---:|:---:|:---:|:---:|
| COW 兼容 | ❌ | ✅ | ✅ | ❌ |
| 实现复杂度 | 高 | **低** | 高 | 中 |
| 写性能 | O(1) | O(N) 拷贝 | **O(1)** | — |
| 读性能退化 | 有 | **无** | 有 | — |
| GC 压力 | 零 | **零** | 中(2 make/update) | — |
| 修改范围 | 大 | **小** | 大 | — |
| 可行性 | ❌ | ✅ | ✅ | ❌ |

## 4. 推荐方案: B（COW Mini-Page）作为 Phase 1, C（Delta Chain）作为 Phase 2

### 4.1 Phase 1: BulkInit-Based COW Update（方案 B）

**为什么选方案 B 作为 Phase 1**:
- **COW 完全兼容**: 新页面是独立副本，不违反任何不可变语义
- **零 GC 压力**: mmap 页面间直接拷贝，不经过 Go 堆
- **实现简单**: 复用已有 `BulkInitLeafFromSource`
- **读路径零改动**: 新页面布局与普通页面完全一致
- **修改文件少**: 仅 `offheap_adapter.go`（修改 `UpdateLeafEntry`）

**实现细节**:

```go
// offheap_adapter.go

// UpdateLeafEntry 更新叶子条目（BulkInit COW 路径）
func (a *OffHeapAdapter) UpdateLeafEntry(pageID model.PageID, idx int, key, value []byte) (model.PageID, error) {
    count := a.pa.GetCount(uint32(pageID))
    entry := a.pa.GetLeafEntry(uint32(pageID), idx)

    // 快速路径: 新 value <= 旧 value, 可以在 COW 副本上直接覆盖
    if uint32(len(value)) <= entry.valLen {
        return a.updateLeafEntryBulkInPlace(pageID, idx, value)
    }

    // 慢速路径: 新 value > 旧 value, 需要完整物化
    return a.updateLeafEntryFullMaterialization(pageID, idx, key, value)
}

// updateLeafEntryBulkInPlace BulkInit 复制 + 原地覆盖 value
func (a *OffHeapAdapter) updateLeafEntryBulkInPlace(pageID model.PageID, idx int, value []byte) (model.PageID, error) {
    count := a.pa.GetCount(uint32(pageID))

    // 1. 分配新页面
    newPageID, err := a.pm.Alloc()
    if err != nil {
        return 0, errpkg.BTreeAllocNewPageForSplit(err)
    }

    // 2. BulkInit: 零堆拷贝所有条目到新页面
    _, err = a.pa.BulkInitLeafFromSource(uint32(pageID), newPageID, 0, int(count))
    if err != nil {
        a.pm.Free(newPageID)
        return 0, errpkg.BTreeBulkInitFailed(err)
    }

    // 3. 在新页面上覆盖目标 entry 的 value
    //    新页面是独立 COW 副本，修改不影响旧页面 ✓
    newEntry := a.pa.GetLeafEntry(newPageID, idx)
    ptr := a.pa.GetPtr(newPageID)
    valPtr := unsafe.Add(ptr, newEntry.valOff)
    copy(unsafe.Slice((*byte)(valPtr), newEntry.valLen), value)

    // 4. 释放旧页面（COW 正常流程）
    a.pm.Free(uint32(pageID))

    return model.PageID(newPageID), nil
}

// updateLeafEntryFullMaterialization 保留原有完整物化路径作为 fallback
func (a *OffHeapAdapter) updateLeafEntryFullMaterialization(pageID model.PageID, idx int, key, value []byte) (model.PageID, error) {
    // ... 当前 UpdateLeafEntry 的完整实现 ...
}
```

**性能分析**:

| 操作 | 当前 COW | BulkInit COW | 改善 |
|------|---------|-------------|------|
| Go 堆 make | 2N+2 | **0** | 消除 |
| Go 堆 copy | 2N | **0** | 消除 |
| mmap copy | 0 | 1 页面 (~4KB) | 新增，但零 GC |
| pm.Free/Alloc | 1/1 | 1/1 | 不变 |
| MaterializePageFromBytes | 1 | **0** | 消除 |

**预期效果**: UpdateLeafEntry CPU 从 51.6% 降到 ~15-20%（mmap copy + pm 开销），GC 压力从 69.35% 大幅降低。

### 4.2 Phase 2: Off-Heap Delta Chain（方案 C，中期）

Phase 1 解决了 GC 压力问题，但每次 Update 仍有 ~4KB 的页面拷贝。Phase 2 通过 Delta Chain 将 Update 优化为 O(1)。

**设计要点**:
- Delta Chain 存储在 `PageInfo` 中（与页面引用绑定）
- Delta 元数据用 Go 堆（单条 KV 的 make），value 数据可选 off-heap
- 物化阈值 10 条（与现有 `COWDeltaRef` 一致）
- 物化时使用 BulkInit（Phase 1 的基础设施）

**实施时机**: Phase 1 完成并验证后，根据 pprof 数据决定是否需要 Phase 2。如果 Phase 1 已将 GC 降到 <20%，Phase 2 的优先级可以降低。

## 5. 风险分析

### 5.1 BulkInit COW 的正确性

**风险**: BulkInit 拷贝后 entry offset 是否正确？

**验证**: `BulkInitLeafFromSource` 已在 zero-copy split 中使用并通过测试。它拷贝完整的 Header + Entry 数组 + KV Data 区，entry 的 offset 指向新页面中的正确位置（offsetDelta=0）。

**已确认**（plan 中三次审核）: KV 数据位置在拷贝后不变，entry offset 无需调整。

### 5.2 Value Overwrite 的一致性

**风险**: 在新页面上覆盖 value 时，如果新 value < 旧 value，剩余字节是旧数据。

**影响**: 无。`LeafEntry.valLen` 不变，读操作只读 `valLen` 字节。剩余字节的旧数据不会被访问。

### 5.3 pm.Free 的时机

**风险**: 释放旧页面后，并发读者可能还在访问。

**缓解**: 当前 COW 流程已有 `epochBasedFreeList` 延迟释放机制。BulkInit COW 使用相同的延迟释放。

## 6. 实施计划

### Phase 1: BulkInit COW Update

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `offheap_adapter.go` | 将现有 `UpdateLeafEntry` 重命名为 `updateLeafEntryFullMaterialization` |
| 2 | `offheap_adapter.go` | 新增 `updateLeafEntryBulkInPlace`（BulkInit + value overwrite） |
| 3 | `offheap_adapter.go` | 新的 `UpdateLeafEntry` 调度：value≤oldVal → BulkInPlace, value>oldVal → FullMaterialization |
| 4 | 测试 | 添加 BulkInit COW update 单元测试 |
| 5 | 测试 | 运行全量 btree 测试 |

### Phase 2: Off-Heap Delta Chain（可选）

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | 新文件 `offheap_delta.go` | `OffHeapDeltaChain` 结构体 |
| 2 | `page_ref.go` | PageInfo 关联 DeltaChain |
| 3 | `offheap_adapter.go` | `InsertToOffHeap` 优先追加 Delta |
| 4 | 读路径 | `GetValue`/`linearSearchLeaf` 合并 Delta |
| 5 | `offheap_adapter.go` | 物化逻辑（Delta 满 → BulkInit + 合并） |

## 7. 验证方案

```bash
# 1. 编译
go build ./...

# 2. offheap 单元测试
go test -v -count=1 ./internal/infrastructure/storage/btree/offheap/...

# 3. btree 全量测试（含并发测试）
go test -v -count=1 ./internal/infrastructure/storage/btree/...

# 4. 性能对比（1 线程）
go build -o bin/btree_perf_scheduler ./cmd/btree_perf_scheduler/
./bin/btree_perf_scheduler -op set -threads 1 -count 50000 -init 200

# 5. 多线程可扩展性
./bin/btree_perf_scheduler -op set -threads 1,2,4,8 -count 50000 -init 200

# 6. CPU profiling（验证 UpdateLeafEntry 占比下降）
go build -o bin/btree_perf_pprof ./cmd/btree_perf_pprof/
./bin/btree_perf_pprof -threads 1 -count 50000 -init 200
go tool pprof -top -flat cpu.prof

# 7. 正确性验证（并发无数据丢失）
go test -v -count=1 -run TestSetWithLeafLock_Concurrent ./internal/infrastructure/storage/btree/...
go test -v -count=1 -run TestDebug6000KeysNoLoss ./internal/infrastructure/storage/btree/...
```
