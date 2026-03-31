# linearSearchLeaf TOCTOU 修复方案

**日期**: 2026-03-31
**问题**: `InsertToOffHeap` → `linearSearchLeaf` 读取被回收重用的叶子页面导致 panic
**状态**: Proposal
**严重程度**: P0（高负载下必现 panic）

---

## 1. 问题描述

### 1.1 Panic 信息

```
panic: index 12 out of range (count: 1)

goroutine 56 [running]:
offheap.(*PageAccessor).GetLeafEntry(page_layout.go:215)
offheap.(*PageAccessor).GetLeafEntryOffset(page_layout.go:532)
offheap_adapter.(*OffHeapAdapter).linearSearchLeaf(offheap_adapter.go:141)
offheap_adapter.(*OffHeapAdapter).InsertToOffHeap(offheap_adapter.go:104)
btree.(*BTree).setWithLeafLock(leaf_lock_set.go:62)
```

### 1.2 触发条件

- 8 线程 × 50,000 ops，500 初始 keys（总计 400K ops）
- 高竞争场景：大量 COW + split + epoch 回收 + 页面重用

### 1.3 调用链

```go
// leaf_lock_set.go:56-62
oldPageID := model.PageID(oldInfo.GetPageID())
newPageID, splitRequired, err = b.offheapAdapter.InsertToOffHeap(oldPageID, key, value)

// offheap_adapter.go:100-104
func (a *OffHeapAdapter) InsertToOffHeap(pageID model.PageID, key, value []byte) (...) {
    idx, found := a.linearSearchLeaf(uint32(pageID), key)  // ← panic 位置
    ...
}

// offheap_adapter.go:136-151
func (a *OffHeapAdapter) linearSearchLeaf(pageID uint32, key []byte) (int, bool) {
    count := a.pa.GetCount(pageID)           // 读到旧 count=12
    for i := 0; i < int(count); i++ {       // i=0..11
        keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(pageID, i)  // GetLeafEntry → panic!
        //                                ↑ 内部再次读 header.count，此时 count=1（页面被回收重用）
```

**关键**：`GetLeafEntryOffset` 内部调用 `GetLeafEntry`（line 532），`GetLeafEntry` **再次读取** `header.count` 并做越界检查（line 214）。如果在 `linearSearchLeaf` 读 count=12 和 `GetLeafEntry` 内部读 count=1 之间，物理页面被回收重用，就会 panic。

---

## 2. 根因分析

### 2.1 为什么 PageLock 没有阻止？

PageLock 绑定在 **PageRef** 上（`leafRef.GetLock()`），保护的是 PageRef 的 CAS 操作。但 `InsertToOffHeap` 直接操作的是**物理页面**（mmap 内存），PageLock 不保护物理页面的 mmap 内容。

当 `oldPageID` 对应的物理页面被另一个线程（通过不同的代码路径）COW 替换 + epoch 回收 + 重用后，当前线程读到的物理页面内容已经完全不同。

### 2.2 竞争时序（推测）

```
Thread A (setWithLeafLock)                 Thread B / C / ... (其他操作)
──────────────────────────                 ──────────────────────────
L22: findLeafPageRef → leafRef
L32: leafRef.GetLock()
L37: TryLock → success
L43: oldInfo = leafRef.GetPageInfo()
L56: oldPageID = oldInfo.GetPageID() → P50
                                           某个操作触发了 P50 的 COW 释放
                                           （可能是 parent update 路径，
                                            或 pageRefCache.Replace 后的 epoch 回收）
                                           P50 → epoch → freeList → 重用
                                           P50 被重新分配为新叶子页（count=1）
L62: InsertToOffHeap(P50, key, val)
  L137: GetCount(P50) → 12（旧值仍在 mmap header）
  L140: for i=0..11
  L141: GetLeafEntryOffset(P50, i=??)
    GetLeafEntry(P50, i):
      header.count = 1（被 InitLeafPage 重置）
      i >= 1 → panic!
```

**或者更简单的场景**：`linearSearchLeaf` 在 L137 读到 count=12，但在 L141 调用 `GetLeafEntryOffset` 时，`GetLeafEntry` 内部重新读取 `header.count`。如果在两次读取之间，物理页面被 `InitLeafPage` 重置（count=0 或 1），就会触发越界。

### 2.3 代码中已有的安全版本

`GetLeafEntrySafe` 已存在（`page_layout.go:234-243`），返回 error 而非 panic：

```go
func (pa *PageAccessor) GetLeafEntrySafe(pageID uint32, index int) (*LeafEntry, error) {
    ptr := pa.getPtr(pageID)
    header := (*PageHeader)(ptr)
    if index >= int(header.count) {
        return nil, fmt.Errorf("index %d out of range (count: %d)", index, header.count)
    }
    ...
}
```

但 `linearSearchLeaf` 使用的是不安全的 `GetLeafEntryOffset` → `GetLeafEntry`。

---

## 3. 修复方案

### 3.1 方案 A：linearSearchLeaf 使用安全读取 + 错误传播（推荐）

**原理**：将 `linearSearchLeaf` 中的 `GetLeafEntryOffset` 替换为安全版本，遇到越界返回错误，由调用方触发 ErrRetry。

**改动文件**: `offheap_adapter.go`

#### 改动 1: linearSearchLeaf 返回 error

```go
// 改前
func (a *OffHeapAdapter) linearSearchLeaf(pageID uint32, key []byte) (int, bool) {
    count := a.pa.GetCount(pageID)
    for i := 0; i < int(count); i++ {
        keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(pageID, i)  // ← panic!
        existingKey := a.pa.GetKey(pageID, keyOff, keyLen)
        ...
    }
    return int(count), false
}

// 改后
func (a *OffHeapAdapter) linearSearchLeaf(pageID uint32, key []byte) (int, bool, error) {
    count := a.pa.GetCount(pageID)
    for i := 0; i < int(count); i++ {
        entry, err := a.pa.GetLeafEntrySafe(pageID, i)  // ← 安全读取，返回 error
        if err != nil {
            // 页面已被回收重用，count 发生变化
            return 0, false, fmt.Errorf("linearSearchLeaf: page %d TOCTOU: %w", pageID, err)
        }
        existingKey := a.pa.GetKey(pageID, entry.keyOff, entry.keyLen)
        cmp := bytes.Compare(key, existingKey)
        if cmp == 0 {
            return i, true, nil
        } else if cmp < 0 {
            return i, false, nil
        }
    }
    return int(count), false, nil
}
```

#### 改动 2: InsertToOffHeap 处理新错误

```go
func (a *OffHeapAdapter) InsertToOffHeap(pageID model.PageID, key, value []byte) (model.PageID, bool, error) {
    idx, found, err := a.linearSearchLeaf(uint32(pageID), key)
    if err != nil {
        return pageID, false, err  // TOCTOU 错误，上层转为 ErrRetry
    }
    ...
}
```

**改动量**：~15 行
**风险**：低。安全读取替代不安全读取，错误传播到上层变成 ErrRetry。

### 3.2 方案 B：linearSearchLeaf 内部 count 一致性校验（防御增强）

在方案 A 基础上，循环结束后校验 count 未变化：

```go
func (a *OffHeapAdapter) linearSearchLeaf(pageID uint32, key []byte) (int, bool, error) {
    count := a.pa.GetCount(pageID)
    for i := 0; i < int(count); i++ {
        entry, err := a.pa.GetLeafEntrySafe(pageID, i)
        if err != nil {
            return 0, false, fmt.Errorf("linearSearchLeaf: page %d TOCTOU: %w", pageID, err)
        }
        ...
    }
    // 二次校验：确认页面未被回收重用
    if a.pa.GetCount(pageID) != count {
        return 0, false, fmt.Errorf("linearSearchLeaf: page %d count changed during scan", pageID)
    }
    return int(count), false, nil
}
```

**建议**：与方案 A 一同实施。

### 3.3 InsertToOffHeap 原地写入路径的防御

`InsertToOffHeap` 的**原地插入路径**（L121-128）也存在 TOCTOU：

```go
dataEnd := a.pa.GetDataEnd(uint32(pageID))
insertErr := a.pa.InsertLeafEntry(uint32(pageID), idx, key, value, &dataEnd)
```

`InsertLeafEntry` 直接修改物理页面，没有安全检查。如果页面已被回收重用，会损坏其他页面的数据。

**修复**：在 `InsertLeafEntry` 调用前，校验页面类型和 count：

```go
// 原地插入前校验
if a.pa.IsLeaf(uint32(pageID)) == false {
    // 页面已被回收重用为非叶子页
    return pageID, false, fmt.Errorf("InsertToOffHeap: page %d is not a leaf page", pageID)
}
currentCount := a.pa.GetCount(uint32(pageID))
if currentCount != uint16(count) {
    // count 已变化，页面可能已被回收重用
    return pageID, false, fmt.Errorf("InsertToOffHeap: page %d count changed (%d → %d)", pageID, count, currentCount)
}
```

---

## 4. 推荐实施

| 步骤 | 改动 | 文件 |
|------|------|------|
| 1 | `linearSearchLeaf` 改用 `GetLeafEntrySafe`，返回 error | `offheap_adapter.go` |
| 2 | `InsertToOffHeap` 处理 `linearSearchLeaf` 返回的 error | `offheap_adapter.go` |
| 3 | 原地插入路径增加 IsLeaf + count 校验 | `offheap_adapter.go` |
| 4 | 循环结束后二次 count 校验 | `offheap_adapter.go` |

**总改动量**：~25 行

---

## 5. 验证

```bash
# 之前会 panic 的场景
go run ./cmd/btree_perf_pprof -threads=8 -count=50000 -init=500
# 预期：无 panic，ErrRetry 吸收 TOCTOU 错误

# 正确性测试
go test -v -race ./internal/infrastructure/storage/btree/...

# 8 线程 100K ops（之前通过的场景仍应通过）
go run ./cmd/btree_perf_pprof -threads=8 -count=100000 -init=500
```
