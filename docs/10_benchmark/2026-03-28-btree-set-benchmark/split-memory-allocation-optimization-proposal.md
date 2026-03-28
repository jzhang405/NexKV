# P0: 分裂内存分配优化 Proposal

日期: 2026-03-28
状态: Draft
CPU 占比: ~25% (mallocgc 2% + mallocgcTiny 4.1% + GC 20% + stealWork 4.1%)

## 问题分析

`SplitOffHeapLeafPage` 的当前实现存在 **3 层冗余拷贝**：

```
offheap 页面 (mmap)
  ↓ GetKey/GetValue → make([]byte) + copy     ← 第 1 次拷贝
Go 堆 [][]byte
  ↓ MaterializePageFromBytes 排序
  ↓   sortedKeys/sortedValues → make + copy   ← 第 2 次拷贝
  ↓ InsertLeafEntry → copy(key), copy(value)  ← 第 3 次拷贝
offheap 新页面 (mmap)
```

**每次分裂：2N 个 `make([]byte)` + 4N 次 `copy`**（N = 页面条目数）

### 分配热点

| 位置 | 分配类型 | 次数 | 可消除 |
|------|---------|------|--------|
| `SplitOffHeapLeafPage` L491-494 | `make([]byte)` KV 深拷贝 | 2N/次分裂 | **YES** |
| `MaterializePageFromBytes` L37-40 | `make([][]byte)` 排序临时 | 2/次分裂 | **YES** |
| `InsertLeafEntry` 内 `copy` | KV 写入新页面 | 2N/次分裂 | 不可避免 |
| `splitInternalOffHeapSync` L1094-1103 | `make([][]byte)` 索引分裂 | 2-4/次分裂 | **YES** |

## 方案: Zero-Copy 直接页面分裂

### 核心思路

直接在 offheap 页面内存中操作，**跳过 Go 堆中转**：

```
源 offheap 页面
  ↓ 直接 memcpy 页面区域（entries + KV data）
左 offheap 新页面 + 右 offheap 新页面
```

### 新增函数

#### 1. `BulkInitFromPages` — 批量从源页面拷贝条目到目标页面

```go
// offheap/page_layout.go

// BulkInitLeafFromSource 从源页面批量拷贝连续条目到目标页面
// 跳过 Go 堆分配，直接在 mmap 内存间拷贝
//
// srcPageID: 源页面
// dstPageID: 目标页面（必须已分配但未初始化）
// startIdx, endIdx: 源页面中的条目范围 [startIdx, endIdx)
//
// 返回 dataEnd 和 error
func (pa *PageAccessor) BulkInitLeafFromSource(
    srcPageID, dstPageID uint32,
    startIdx, endIdx int,
) (uint16, error) {
    srcPtr := pa.getPtr(srcPageID)
    srcHeader := (*PageHeader)(srcPtr)
    totalCount := int(srcHeader.count)

    // 边界检查
    if startIdx < 0 || endIdx > totalCount || startIdx >= endIdx {
        return 0, fmt.Errorf("invalid range [%d, %d) (count: %d)", startIdx, endIdx, totalCount)
    }

    entryCount := endIdx - startIdx

    // 计算源页面中 [startIdx, endIdx) 条目的 KV 数据范围
    // 找到这些条目引用的最小 offset（最靠近页面尾部的 KV 数据开始位置）
    minKVOff := uint32(PageSize)
    maxKVEnd := uint32(0) // 最大 offset + len

    srcEntriesBase := unsafe.Add(srcPtr, SizeofPageHeader)
    for i := startIdx; i < endIdx; i++ {
        entry := (*LeafEntry)(unsafe.Add(srcEntriesBase, uintptr(i)*SizeofLeafEntry))
        // key 范围
        if entry.keyOff < minKVOff {
            minKVOff = entry.keyOff
        }
        keyEnd := entry.keyOff + entry.keyLen
        if keyEnd > maxKVEnd {
            maxKVEnd = keyEnd
        }
        // value 范围
        if entry.valOff < minKVOff {
            minKVOff = entry.valOff
        }
        valEnd := entry.valOff + entry.valLen
        if valEnd > maxKVEnd {
            maxKVEnd = valEnd
        }
    }

    kvDataSize := uint32(PageSize) - minKVOff
    kvDataStart := minKVOff // 在源页面中的起始 offset

    // 计算需要的空间
    entryArraySize := uint32(entryCount) * uint32(SizeofLeafEntry)
    totalNeeded := uint32(SizeofPageHeader) + entryArraySize + kvDataSize

    if totalNeeded > uint32(PageSize) {
        return 0, errpkg.OffHeapPageFull(int(totalNeeded), 0, PageSize)
    }

    // 初始化目标页面 header
    dstPtr := pa.getPtr(dstPageID)
    dstHeader := (*PageHeader)(dstPtr)
    dstHeader.pageType = PageTypeLeaf
    dstHeader.count = uint16(entryCount)
    dstHeader.extraChild = 0
    dstHeader.prevPage = 0xFFFFFFFF
    dstHeader.nextPage = 0xFFFFFFFF
    dstHeader.version = srcHeader.version // 继承版本号

    // 拷贝 entry 数组（从源 startIdx 开始，连续到目标位置）
    srcEntries := unsafe.Add(srcPtr, SizeofPageHeader+uintptr(startIdx)*SizeofLeafEntry)
    dstEntries := unsafe.Add(dstPtr, SizeofPageHeader)
    entryBytes := unsafe.Slice((*byte)(srcEntries), entryCount*SizeofLeafEntry)
    dstEntryBytes := unsafe.Slice((*byte)(dstEntries), len(entryBytes))
    copy(dstEntryBytes, entryBytes)

    // 拷贝 KV 数据区（从页面尾部开始，保持紧凑）
    // 目标页面：KV 数据放在 [PageSize - kvDataSize, PageSize)
    dstKVStart := uint32(PageSize) - kvDataSize
    srcKVPtr := unsafe.Add(srcPtr, kvDataStart)
    dstKVPtr := unsafe.Add(dstPtr, dstKVStart)
    kvBytes := unsafe.Slice((*byte)(srcKVPtr), kvDataSize)
    dstKVBytes := unsafe.Slice((*byte)(dstKVPtr), kvDataSize)
    copy(dstKVBytes, kvBytes)

    // 修正目标 entry 的 offset（重定位）
    // 每个 entry 的 keyOff/valOff 需要减去偏移量
    offsetDelta := kvDataStart - dstKVStart
    for i := 0; i < entryCount; i++ {
        entry := (*LeafEntry)(unsafe.Add(dstEntries, uintptr(i)*SizeofLeafEntry))
        entry.keyOff -= offsetDelta
        entry.valOff -= offsetDelta
    }

    // 返回 dataEnd
    return uint16(kvDataSize), nil
}
```

#### 2. `BulkInitIndexFromSource` — 索引页面批量拷贝

```go
// offheap/page_layout.go

// BulkInitIndexFromSource 从源索引页面批量拷贝连续条目到目标页面
func (pa *PageAccessor) BulkInitIndexFromSource(
    srcPageID, dstPageID uint32,
    startIdx, endIdx int,
    extraChild uint64, // 额外的子节点（最后一个条目右边的子节点）
) (uint16, error) {
    srcPtr := pa.getPtr(srcPageID)
    srcHeader := (*PageHeader)(srcPtr)
    totalCount := int(srcHeader.count)

    if startIdx < 0 || endIdx > totalCount || startIdx >= endIdx {
        return 0, fmt.Errorf("invalid range [%d, %d) (count: %d)", startIdx, endIdx, totalCount)
    }

    entryCount := endIdx - startIdx

    // 计算 key 数据范围
    minKeyOff := uint32(PageSize)
    maxKeyEnd := uint32(0)

    srcEntriesBase := unsafe.Add(srcPtr, SizeofPageHeader)
    for i := startIdx; i < endIdx; i++ {
        entry := (*IndexEntry)(unsafe.Add(srcEntriesBase, uintptr(i)*SizeofIndexEntry))
        if entry.keyOff < minKeyOff {
            minKeyOff = entry.keyOff
        }
        keyEnd := entry.keyOff + entry.keyLen
        if keyEnd > maxKeyEnd {
            maxKeyEnd = keyEnd
        }
    }

    keyDataSize := uint32(PageSize) - minKeyOff
    keyDataStart := minKeyOff

    // 计算空间
    entryArraySize := uint32(entryCount) * uint32(SizeofIndexEntry)
    totalNeeded := uint32(SizeofPageHeader) + entryArraySize + keyDataSize

    if totalNeeded > uint32(PageSize) {
        return 0, errpkg.OffHeapPageFull(int(totalNeeded), 0, PageSize)
    }

    // 初始化目标页面
    dstPtr := pa.getPtr(dstPageID)
    dstHeader := (*PageHeader)(dstPtr)
    dstHeader.pageType = PageTypeIndex
    dstHeader.count = uint16(entryCount)
    dstHeader.extraChild = extraChild
    dstHeader.prevPage = 0xFFFFFFFF
    dstHeader.nextPage = 0xFFFFFFFF
    dstHeader.version = srcHeader.version

    // 拷贝 entry 数组
    srcEntries := unsafe.Add(srcPtr, SizeofPageHeader+uintptr(startIdx)*SizeofIndexEntry)
    dstEntries := unsafe.Add(dstPtr, SizeofPageHeader)
    entryBytes := unsafe.Slice((*byte)(srcEntries), entryCount*SizeofIndexEntry)
    dstEntryBytes := unsafe.Slice((*byte)(dstEntries), len(entryBytes))
    copy(dstEntryBytes, entryBytes)

    // 拷贝 key 数据区
    dstKeyStart := uint32(PageSize) - keyDataSize
    srcKeyPtr := unsafe.Add(srcPtr, keyDataStart)
    dstKeyPtr := unsafe.Add(dstPtr, dstKeyStart)
    keyBytes := unsafe.Slice((*byte)(srcKeyPtr), keyDataSize)
    dstKeyBytes := unsafe.Slice((*byte)(dstKeyPtr), keyDataSize)
    copy(dstKeyBytes, keyBytes)

    // 修正 offset
    offsetDelta := keyDataStart - dstKeyStart
    for i := 0; i < entryCount; i++ {
        entry := (*IndexEntry)(unsafe.Add(dstEntries, uintptr(i)*SizeofIndexEntry))
        entry.keyOff -= offsetDelta
    }

    return uint16(keyDataSize), nil
}
```

### 修改 `SplitOffHeapLeafPage`

```go
// offheap_adapter.go — 重写 SplitOffHeapLeafPage

func (a *OffHeapAdapter) SplitOffHeapLeafPage(pageID model.PageID) (model.PageID, model.PageID, []byte, error) {
    count := a.pa.GetCount(uint32(pageID))

    if count < 2 {
        return 0, 0, nil, errpkg.BTreeSplitMinKeys(int(count))
    }

    // 分配左右两个新页面
    leftPageID, err := a.pm.Alloc()
    if err != nil {
        return 0, 0, nil, errpkg.BTreeAllocLeftPage(err)
    }
    rightPageID, err := a.pm.Alloc()
    if err != nil {
        a.pm.Free(leftPageID)
        return 0, 0, nil, errpkg.BTreeAllocRightPage(err)
    }

    if leftPageID == rightPageID {
        a.pm.Free(leftPageID)
        a.pm.Free(rightPageID)
        return 0, 0, nil, errpkg.BTreeDuplicatePageIDAlloc(leftPageID)
    }

    // 智能分裂搜索（zero-copy 尝试）
    countInt := int(count)
    var splitIdx int
    var success bool

    // 尝试 50/50 分裂
    mid := countInt / 2
    _, leftErr := a.pa.BulkInitLeafFromSource(uint32(pageID), leftPageID, 0, mid)
    _, rightErr := a.pa.BulkInitLeafFromSource(uint32(pageID), rightPageID, mid, countInt)

    if leftErr == nil && rightErr == nil {
        splitIdx = mid
        success = true
    }

    // 50/50 失败，尝试其他比例
    if !success {
        for _, ratio := range []float64{0.3, 0.7, 0.2, 0.8} {
            mid = int(float64(countInt) * ratio)
            if mid <= 0 || mid >= countInt {
                continue
            }

            // 重新分配页面（上次可能已写入脏数据）
            a.pm.Free(leftPageID)
            a.pm.Free(rightPageID)
            leftPageID, err = a.pm.Alloc()
            if err != nil {
                continue
            }
            rightPageID, err = a.pm.Alloc()
            if err != nil {
                a.pm.Free(leftPageID)
                continue
            }

            _, leftErr = a.pa.BulkInitLeafFromSource(uint32(pageID), leftPageID, 0, mid)
            _, rightErr = a.pa.BulkInitLeafFromSource(uint32(pageID), rightPageID, mid, countInt)

            if leftErr == nil && rightErr == nil {
                splitIdx = mid
                success = true
                break
            }
        }
    }

    if !success {
        // 最终回退：使用原始 Go 堆方式（兼容极端情况）
        a.pm.Free(leftPageID)
        a.pm.Free(rightPageID)
        return a.splitOffHeapLeafPageFallback(pageID)
    }

    // 获取 splitKey（从右页面第一个条目）
    rightHeader := a.pa.GetHeader(rightPageID)
    if rightHeader.count == 0 {
        a.pm.Free(leftPageID)
        a.pm.Free(rightPageID)
        return 0, 0, nil, errpkg.BTreeInvalidSplitIdx(splitIdx, countInt)
    }
    firstRightEntry := a.pa.GetLeafEntry(rightPageID, 0)
    splitKey := a.pa.GetKey(rightPageID, firstRightEntry.keyOff, firstRightEntry.keyLen)
    // splitKey 必须深拷贝（右页面可能被重用）
    splitKeyCopy := make([]byte, len(splitKey))
    copy(splitKeyCopy, splitKey)

    // 设置链表指针
    oldPrevPage := a.pa.GetPrevPage(uint32(pageID))
    oldNextPage := a.pa.GetNextPage(uint32(pageID))

    a.pa.SetNextPage(leftPageID, rightPageID)
    a.pa.SetPrevPage(rightPageID, leftPageID)

    if oldPrevPage != 0xFFFFFFFF {
        a.pa.SetPrevPage(leftPageID, oldPrevPage)
    }
    if oldNextPage != 0xFFFFFFFF {
        a.pa.SetNextPage(rightPageID, oldNextPage)
    }

    return model.PageID(leftPageID), model.PageID(rightPageID), splitKeyCopy, nil
}

// splitOffHeapLeafPageFallback 回退方案（极端 KV 大小不均时使用）
func (a *OffHeapAdapter) splitOffHeapLeafPageFallback(pageID model.PageID) (model.PageID, model.PageID, []byte, error) {
    // ... 保留原有的 Go 堆拷贝逻辑作为回退 ...
    // （即当前 SplitOffHeapLeafPage 的完整实现，重命名即可）
}
```

### 修改 `splitInternalOffHeapSync`

同样用 `BulkInitIndexFromSource` 替换手动数组操作：

```go
// leaf_lock_set.go — splitInternalOffHeapSync 核心改动

// 替换这段代码:
//   keys := make([][]byte, 0, count)
//   children := make([]uint32, 0, count+1)
//   for i := range int(count) { ... }
// 为:

mid := int(count) / 2

// 获取 extraChild（最右子节点）
encodedLastChild := b.offheapAdapter.pa.GetChild(uint32(internalPageID), int(count))
lastChild, _ := b.offheapAdapter.pa.DecodeChildWithVersion(encodedLastChild)

// 左半部分：entries [0, mid), extraChild = children[mid]
leftEntry := b.offheapAdapter.pa.GetIndexEntry(uint32(internalPageID), mid)
midChild := leftEntry.child // mid 位置的 child 成为左半的 extraChild

_, err = b.offheapAdapter.pa.BulkInitIndexFromSource(
    uint32(internalPageID), uint32(leftPageID),
    0, mid,
    midChild, // 左半的 extraChild
)
if err != nil {
    b.offheapAdapter.pm.Free(uint32(leftPageID))
    b.offheapAdapter.pm.Free(uint32(rightPageID))
    return errpkg.BTreeMaterializeLeftIndexPage(err)
}

// 右半部分：entries [mid+1, count), extraChild = lastChild
_, err = b.offheapAdapter.pa.BulkInitIndexFromSource(
    uint32(internalPageID), uint32(rightPageID),
    mid+1, int(count),
    lastChild, // 右半的 extraChild
)
if err != nil {
    b.offheapAdapter.pm.Free(uint32(leftPageID))
    b.offheapAdapter.pm.Free(uint32(rightPageID))
    return errpkg.BTreeMaterializeRightIndexPage(err)
}

// splitKey 直接从源页面获取
splitKeyEntry := b.offheapAdapter.pa.GetIndexEntry(uint32(internalPageID), mid)
splitKey := b.offheapAdapter.pa.GetKey(uint32(internalPageID), splitKeyEntry.keyOff, splitKeyEntry.keyLen)
splitKeyCopy := make([]byte, len(splitKey))
copy(splitKeyCopy, splitKey)
```

## 关键文件

| 文件 | 改动 |
|------|------|
| `offheap/page_layout.go` | 新增 `BulkInitLeafFromSource`, `BulkInitIndexFromSource` |
| `offheap_adapter.go` | 重写 `SplitOffHeapLeafPage`，新增 fallback |
| `leaf_lock_set.go` | 重构 `splitInternalOffHeapSync` 中的数组操作 |

## 消除的分配

| 操作 | 优化前分配次数 | 优化后分配次数 | 节省 |
|------|-------------|-------------|------|
| 叶子分裂 KV 拷贝 | 2N `make([]byte)` | **0** | 100% |
| 物化排序临时数组 | 2 `make([][]byte)` | **0** | 100% |
| 索引分裂数组 | 4 `make([][]byte)` + 2 `make([]uint32)` | **0** | 100% |
| splitKey 深拷贝 | 1 `make([]byte)` | 1（不可避免） | 0% |
| memcpy 次数 | 4N (read+sort+write×2) | **2N** (直接页间拷贝) | 50% |

## 预期收益

| 指标 | 优化前 | 优化后 | 变化 |
|------|--------|--------|------|
| mallocgc + mallocgcTiny | ~6% | **~1%** | -5% |
| gcBgMarkWorker | ~20% | **~8%** | -12% |
| 总 GC 压力 | ~25% | **~9%** | **-16%** |
| 1T 吞吐量 | ~29.5K ops/s | **~36-38K ops/s** | **+22-29%** |
| 延迟 | ~33.5 μs | **~26-28 μs** | **-20%** |

## 风险与缓解

### 风险 1: 条目顺序依赖
**问题**: `BulkInitLeafFromSource` 假设源页面条目已排序。如果源页面未排序，分裂后顺序错误。
**缓解**: BTree 页面在插入时保证有序，只读快照分裂不需要排序。如果需要排序，回退到 fallback。

### 风险 2: KV 数据重定位正确性
**问题**: offset 修正 `entry.keyOff -= offsetDelta` 如果算错会导致数据损坏。
**缓解**: 在 fallback 路径中添加 `VerifyPage` 断言。生产环境先用 `GO_TEST=1` 开启验证。

### 风险 3: 大 KV 单条占满半页
**问题**: 如果单条 KV > 2KB，50/50 分裂可能失败。
**缓解**: 保留 fallback 路径处理极端情况。

## 实施步骤

1. 在 `page_layout.go` 中新增 `BulkInitLeafFromSource` 和 `BulkInitIndexFromSource`
2. 将现有 `SplitOffHeapLeafPage` 重命名为 `splitOffHeapLeafPageFallback`
3. 编写新的 `SplitOffHeapLeafPage`，优先 zero-copy，失败回退 fallback
4. 添加 `VerifyPage` 验证断言（debug 模式）
5. 重构 `splitInternalOffHeapSync` 中的索引数组操作
6. 运行全量测试 + benchmark

## 验证

```bash
# 1. 编译
go build ./...

# 2. offheap 单元测试
go test -v -count=1 ./internal/infrastructure/storage/btree/offheap/...

# 3. btree 全量测试
go test -v -count=1 ./internal/infrastructure/storage/btree/...

# 4. 性能对比
./bin/btree_perf_scheduler -op set -threads 1 -count 50000 -init 200

# 5. CPU profiling
./bin/btree_perf_pprof
go tool pprof -top -flat cpu.prof
```

---

## Code Review 审核意见 (2026-03-28)

### Critical — 发现的已有 Bug

#### Bug 1: `SplitOffHeapLeafPage` 重复物化

**位置**: `offheap_adapter.go:617-630`

搜索分裂点的循环（L537-560）已将数据写入 leftPageID/rightPageID，但 L617-630 **无条件再次物化**，导致每次分裂多做 2N 次多余 memcpy。

**修复**: L617-630 应仅在搜索循环未执行时（如 `!success` 且走了 fallback）才物化。在 zero-copy 优化中可直接消除此问题。

#### Bug 2: `MaterializePageFromBytes` 排序 Bug

**位置**: `materialize.go:42-44`

```go
sort.SliceStable(sortedKeys, func(i, j int) bool {
    return bytes.Compare(sortedKeys[i], sortedKeys[j]) < 0
})
```

`sortedKeys` 排序后，`sortedValues` **未做对应排列**，key-value 对应关系被打破。当前分裂场景中 keys 已有序所以不会触发，但作为通用函数存在正确性隐患。

**影响**: zero-copy 方案跳过排序，规避了此 Bug。但 **fallback 路径必须修复此 Bug**。

### High — Proposal 代码问题

#### Issue 1: `BulkInitLeafFromSource` KV over-copy

**问题**: 函数拷贝 `[minKVOff, PageSize)` 整个连续区域，可能包含非目标条目范围的 KV 数据（条目间 KV 数据可能不连续）。

**影响**: 逻辑上不会导致数据损坏（多余数据不被 entry 引用），但**浪费空间**，可能导致本应成功的分裂因空间不足而不必要地触发 fallback。

**修复建议**: 精确拷贝目标条目的 KV 数据（逐条拷贝），而非整块拷贝连续区域。或者接受此限制，在 over-copy 失败时再逐条尝试。

#### Issue 2: `offsetDelta` 恒为 0

**分析**:
```
kvDataSize = PageSize - minKVOff
dstKVStart = PageSize - kvDataSize = PageSize - (PageSize - minKVOff) = minKVOff
offsetDelta = kvDataStart - dstKVStart = minKVOff - minKVOff = 0
```

offset 修正循环是空操作。源和目标的 KV 数据区起始位置相同（都是从 `PageSize - kvDataSize` 开始），不需要重定位。

**修复建议**: 删除 offset 修正循环，或保留但加注释说明其为防御性代码。over-copy 场景下 offsetDelta 确实为 0。

#### Issue 3: 搜索策略覆盖不足

**问题**: 原始代码搜索策略为 30/70 → 2/3, 3/4, ..., 9/10 → splitIdx=1,0。Proposal 简化为 50/50 → 0.3/0.7/0.2/0.8，**覆盖范围变窄**。

**修复建议**: 保持与原始代码相同的搜索策略（或使用 zero-copy 直接尝试，失败后 Free+ReAlloc 再试下一个比例）。Free+Alloc 方式在分配器层面开销极低（页池复用）。

### Medium

#### Issue 4: 编码后 child 值包含旧版本号

`BulkInitIndexFromSource` 直接拷贝 entry 中的 `child` 字段（`uint64` 编码值），其中包含子节点的版本号。分裂后子节点版本号不变，不会导致数据不一致，但可能导致不必要的搜索路径重试（版本号检查失败 → ErrRetry）。

**影响**: 低。版本号主要用于检测 COW 冲突，分裂后引用关系未变。

#### Issue 5: `maxKVEnd` 死代码

Proposal 中 `maxKVEnd` 被计算但未使用。应删除。

#### Issue 6: 失败重试不应 Free+Alloc

搜索循环中失败后 `Free(leftPageID)` + `Alloc()` 是不必要的。可以直接覆写已分配的页面（`InitPage` 会清零 header）。

**修复**: 重试时直接调用 `BulkInitLeafFromSource` 覆写，省去 Free/Alloc 开销。

### 审核总结

| 级别 | 问题 | 行动 |
|------|------|------|
| **Critical** | 重复物化 Bug（L617-630） | zero-copy 方案直接消除 |
| **Critical** | 排序 Bug（materialize.go:42-44） | fallback 路径必须修复 |
| **High** | KV over-copy 浪费空间 | 改为逐条精确拷贝 |
| **High** | offsetDelta=0，修正循环无效 | 删除或注释说明 |
| **High** | 搜索策略覆盖不足 | 保持原始策略 |
| **Medium** | child 旧版本号 | 可接受 |
| **Medium** | maxKVEnd 死代码 | 删除 |
| **Medium** | 失败重试不应 Free+Alloc | 改为直接覆写 |

---

## 二次审核 — 核实结果 (2026-03-28)

针对 Proposal 代码的逐项核实结果。

### 🔴 Issue 1: 链表指针双向更新遗漏 — **唯一需要修复的严重问题**

**位置**: Proposal L331-343（`SplitOffHeapLeafPage` 链表设置部分）

**问题**: Proposal 代码设置了 left↔right 的内部链接和 left.prev/right.next 的外部链接，但**遗漏了相邻页面的反向指针更新**：

```go
// Proposal 当前代码（不完整）:
a.pa.SetNextPage(leftPageID, rightPageID)
a.pa.SetPrevPage(rightPageID, leftPageID)
if oldPrevPage != 0xFFFFFFFF {
    a.pa.SetPrevPage(leftPageID, oldPrevPage)
    // ❌ 缺少: a.pa.SetNextPage(oldPrevPage, leftPageID)
}
if oldNextPage != 0xFFFFFFFF {
    a.pa.SetNextPage(rightPageID, oldNextPage)
    // ❌ 缺少: a.pa.SetPrevPage(oldNextPage, rightPageID)
}
```

**影响**: 叶子链表双向遍历会断裂。`oldPrevPage.nextPage` 仍指向旧页面（已分裂），`oldNextPage.prevPage` 仍指向旧页面。范围扫描等依赖链表遍历的操作会丢失数据。

**修复**:
```go
if oldPrevPage != 0xFFFFFFFF {
    a.pa.SetPrevPage(leftPageID, oldPrevPage)
    a.pa.SetNextPage(oldPrevPage, leftPageID)  // ← 补充
}
if oldNextPage != 0xFFFFFFFF {
    a.pa.SetNextPage(rightPageID, oldNextPage)
    a.pa.SetPrevPage(oldNextPage, rightPageID)  // ← 补充
}
```

### ❌ Issue 2: `offsetDelta` 可能为负 — **不成立**

**结论**: 数学证明 `offsetDelta` 恒为 0，不会为负。

```
kvDataSize = PageSize - minKVOff
dstKVStart = PageSize - kvDataSize = minKVOff
offsetDelta = kvDataStart - dstKVStart = minKVOff - minKVOff = 0
```

offset 修正循环是空操作，可删除或保留为防御性代码。已在首次审核 Issue 2 中正确指出。

### ⚠️ Issue 3: 排序假设 — **部分成立**

`MaterializePageFromBytes` 排序 bug 确实存在（排序 keys 不排序 values），但 **zero-copy 路径完全跳过排序**，不受影响。此 bug 仅影响 fallback 路径，实施时须修复。

### ✅ Issue 4: fallback 未提供 — **成立**

Proposal L348-352 只有函数签名，缺少完整实现。实施时需将当前 `SplitOffHeapLeafPage` 完整代码复制为 `splitOffHeapLeafPageFallback`。

### ⚠️ Issue 5: 多线程安全 — **部分成立**

`handleSplitOffHeapSync` 通过 `splitMu` 串行化分裂、`pageLock` 保护源页面。但 zero-copy 直接读取源页面 mmap 内存期间，需确认 COW 机制下源页面版本号不变。建议在实施文档中明确源页面生命周期保障策略。

### 二次审核总结

| Issue | 审核判断 | 核实结果 | 修复优先级 |
|-------|---------|---------|-----------|
| 1 链表指针遗漏 | 🔴 严重 | ✅ **确认成立** | **P0 — 必须修复** |
| 2 offsetDelta 为负 | 🔴 严重 | ❌ 不成立（恒为 0） | 无需修复 |
| 3 排序假设 | 🟡 中等 | ⚠️ 部分成立 | P1 — fallback 路径需修复 |
| 4 fallback 未提供 | 🟡 中等 | ✅ 成立 | P1 — 实施时补充 |
| 5 多线程安全 | 🟡 中等 | ⚠️ 部分成立 | P2 — 文档补充 |

---

## 三次审核 — 全面核实结果 (2026-03-28)

对二次审核中提出的所有问题进行源码级核实。

### ✅ 已有 Bug 核实

#### 重复物化 Bug — ✅ 确认

**位置**: `offheap_adapter.go:617-630`

搜索循环（L529-600）已将数据写入 leftPageID/rightPageID并设置 `success=true`，但 L617-630 **无条件再次调用 `MaterializePageFromBytes`**，导致每次分裂多做 2N 次 memcpy。

**行动**: zero-copy 方案直接消除（跳过 Go 堆物化）。

#### 排序 Bug — ✅ 确认

**位置**: `materialize.go:42-44`

`sort.SliceStable(sortedKeys, ...)` 只排序 keys，`sortedValues` 保持原始顺序，key-value 错配。

**实际影响**: BTree 正常操作中 keys 已有序，sort 为空操作，**当前不会触发**。仅当外部传入无序 keys 时才会触发（如 fallback 路径中 keys 已有序所以安全，但作为通用函数存在正确性隐患）。

**行动**: P1 — fallback 实施前修复。

### ✅ Proposal 代码问题核实

#### KV over-copy — ✅ 确认

`BulkInitLeafFromSource` 拷贝 `[minKVOff, PageSize)` 整块区域，可能包含非目标条目范围的 KV 数据。不损坏数据（多余数据不被 entry 引用），但浪费空间，可能导致不必要的 fallback。

**行动**: P1 — 注释说明此限制，或改为逐条精确拷贝。

#### 搜索策略收窄 — ✅ 确认

当前代码: 30/70 → 2/3, 3/4, ..., 9/10 → splitIdx=1, 0（**11 个比例**）
Proposal: 50/50 → 0.3, 0.7, 0.2, 0.8（**5 个比例**）

**行动**: P1 — 恢复原始 11 个搜索比例。

#### 死代码 maxKVEnd — ✅ 确认

Proposal L86-98 计算了 `maxKVEnd` 但从未使用。

**行动**: P2 — 实施时删除。

#### 链表更新策略 — ⚠️ 不是缺陷，需统一注释

**核实**: 当前代码 L641-653 **故意不在函数内更新相邻页面的反向指针**：

```go
// 注意：不修改前驱节点的 nextPage，因为旧页面可能还在使用
// 调用者负责在 CAS 成功后更新链接
```

这是正确的 COW 策略——旧页面在 CAS 前仍对并发读可见，提前修改相邻指针会破坏遍历。Proposal 代码（L331-343）也采用了同样策略。

**结论**: 两处策略一致，均正确。非缺陷，仅需在 Proposal 注释中说明"调用者负责在 CAS 成功后更新 oldPrevPage.nextPage 和 oldNextPage.prevPage"。

**行动**: P2 — 统一注释说明。

#### Fallback 排序 Bug — ✅ 确认（与排序 Bug 同项）

Fallback 路径复用 `MaterializePageFromBytes`。排序 bug 在 keys 无序时会破坏数据，但 BTree 正常操作中 keys 已有序，实际触发概率极低。

**行动**: P1 — 与排序 Bug 修复同项。

### 实施优先级（最终版）

| 优先级 | 内容 | 说明 |
|--------|------|------|
| **P0** | 修复链表指针双向更新（二次审核 Issue 1） | 唯一会导致数据损坏的严重问题 |
| **P1** | 修复 `MaterializePageFromBytes` 排序 bug | fallback 路径安全保障，正常操作不触发 |
| **P1** | 恢复原始 11 个搜索策略比例 | 覆盖极端 KV 分布 |
| **P1** | KV over-copy 注释说明或逐条精确拷贝 | 减少不必要的 fallback 触发 |
| **P1** | 补充 `splitOffHeapLeafPageFallback` 完整实现 | 极端情况回退保障 |
| **P2** | 删除 `maxKVEnd` 死代码 | 代码清理 |
| **P2** | 统一链表更新策略注释 | 文档完善，两处策略一致 |
| **P2** | 明确 zero-copy 源页面生命周期 | 多线程安全文档补充 |

**Proposal 整体方向正确，修复 P0 链表指针问题后可开始实施。**

---

## 四次审核 — Agent 全面核实 (2026-03-28)

三个 Agent 并行审核 Proposal 的 BulkInit 函数、SplitOffHeapLeafPage 重写、splitInternalOffHeapSync 改动。

### 结论：未发现新的严重问题

所有之前已识别的问题均得到源码级确认，无新增 Critical/High 级别问题。

### BulkInitLeafFromSource / BulkInitIndexFromSource 核实

| 审核点 | 结论 | 详情 |
|--------|------|------|
| offsetDelta = 0 | ✅ 数学证明确认 | `dstKVStart = PageSize - (PageSize - minKVOff) = minKVOff`，修正循环是空操作 |
| Over-copy 安全性 | ✅ 不损坏数据 | 多余 KV 数据不被 entry 引用，逻辑安全 |
| Over-copy 空间浪费 | ⚠️ 最坏浪费 ~2KB | 源页面 50% 数据可能属于非目标范围，可能导致不必要的 fallback |
| Index child 旧版本号 | ✅ 可接受 | 分裂后引用关系未变，COW 冲突检测正常工作 |
| 边界：1 个条目 | ✅ 正确 | startIdx=0, endIdx=1：entry 数组 1 条 + 对应 KV 数据 |
| 边界：全部条目 | ✅ 正确 | startIdx=0, endIdx=count：完整页面拷贝 |
| 重试时 Free+Alloc 不必要 | ✅ BulkInit 覆写完整 | InitPage 清零 header，BulkInit 覆写所有字段，无需 Free+Alloc |

### SplitOffHeapLeafPage 重写核实

| 审核点 | 结论 | 详情 |
|--------|------|------|
| 搜索策略收窄 | ✅ 确认 | 当前 11 个比例 → Proposal 5 个，极端 KV 分布可能失败 |
| splitKey 深拷贝 | ✅ 必要且正确 | 右页面后续可能被修改，必须深拷贝 |
| 链表策略一致性 | ✅ 与当前代码一致 | COW 策略，调用者负责 CAS 后更新相邻页面反向指针 |
| Fallback 排序 bug | ✅ 确认 | `MaterializePageFromBytes` 排序 keys 不排序 values，正常操作不触发 |

### splitInternalOffHeapSync 改动核实

| 审核点 | 结论 | 详情 |
|--------|------|------|
| extraChild 语义 | ✅ 正确 | 左半 extraChild = children[mid]，右半 extraChild = lastChild |
| splitKey 选择 | ✅ 符合 B+Tree 标准 | mid 位置 key 提升到父节点，不出现在子页面中 |
| 内存分配消除 | ✅ 显著 | 当前 6+2N 次分配 → Proposal 0 次分配 |
| 核心逻辑变化 | ✅ 仅替换调用方式 | 数组操作 + Materialize → BulkInitIndexFromSource，逻辑不变 |

### 实施优先级（最终确认版）

| 优先级 | 内容 | 说明 |
|--------|------|------|
| **P0** | 修复链表指针双向更新（二次审核 Issue 1） | 唯一会导致数据损坏的严重问题 |
| **P1** | 修复 `MaterializePageFromBytes` 排序 bug | fallback 路径安全保障，正常操作不触发 |
| **P1** | 恢复原始 11 个搜索策略比例 | 覆盖极端 KV 分布 |
| **P1** | KV over-copy 注释说明或逐条精确拷贝 | 减少不必要的 fallback 触发 |
| **P1** | 补充 `splitOffHeapLeafPageFallback` 完整实现 | 极端情况回退保障 |
| **P2** | 删除 `maxKVEnd` 死代码 | 代码清理 |
| **P2** | 统一链表更新策略注释 | 文档完善，两处策略一致 |
| **P2** | 重试循环去除 Free+Alloc | BulkInit 可直接覆写，无需重新分配 |
| **P2** | 明确 zero-copy 源页面生命周期 | 多线程安全文档补充 |

**四次审核完成，Proposal 可实施。**
