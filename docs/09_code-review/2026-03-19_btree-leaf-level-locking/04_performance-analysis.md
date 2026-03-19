# 性能分析报告

**审查维度**: 性能优化
**审查日期**: 2026-03-19
**测试工具**: go test -bench

---

## 审查目标

识别性能瓶颈、验证优化效果、提出改进建议。

---

## 性能基准数据

### 单线程性能

| 操作 | 吞吐量 | 延迟 | 对比目标 |
|------|--------|------|----------|
| Set | 654K ops/sec | 1.53 μs/op | 81% (目标 800K) |
| Get | 3.1M ops/sec | 0.33 μs/op | ✅ 超标 |
| Mixed (50:50) | 1.5M ops/sec | 0.66 μs/op | ✅ 超标 |

### 8 线程性能

| 操作 | 吞吐量 | 扩展比 | 对比目标 |
|------|--------|--------|----------|
| Set | 3.8M ops/sec | 5.8x | ✅ 152% (目标 2.5M) |
| Get | 12.5M ops/sec | 4.0x | ✅ 超标 |
| Mixed (50:50) | 5.1M ops/sec | 3.4x | ✅ 超标 |

### 32 线程性能

| 操作 | 吞吐量 | 扩展比 | 备注 |
|------|--------|--------|------|
| Set | 2.9M ops/sec | 4.4x | 轻微下降 |
| Get | 16.6M ops/sec | 5.4x | 持续增长 |
| Mixed (50:50) | 5.4M ops/sec | 3.6x | 稳定 |

---

## 性能优化验证

### 1. PageLock 懒加载优化

**代码位置**: `page_info.go:117-133`

```go
func (info *PageInfo) GetLock() *PageLock {
    // 快速路径：已经初始化
    if lock := info.pageLock.Load(); lock != nil {
        return lock.(*PageLock)
    }
    // 慢速路径：CAS 初始化
    newLock := NewPageLock()
    if info.pageLock.CompareAndSwap(nil, newLock) {
        return newLock
    }
    return info.pageLock.Load().(*PageLock)
}
```

**效果**:
- ✅ 减少 15.45% 内存分配（纯内存模式）
- ✅ 纯内存模式不创建不必要的锁
- ✅ CAS 操作确保线程安全

### 2. Delta Chain 按需增长

**代码位置**: `cow_delta_ref.go:91-104`

```go
// 性能优化：减少预分配容量，从 8 降到 0（按需增长）
// 大部分 Delta Chain 使用量很小（0-2），预分配 8 会浪费内存
deltas: make([]Delta, 0), // 按需增长，减少 22.7% 内存分配
```

**效果**:
- ✅ 减少 22.7% 内存分配
- ✅ 大部分场景 (0-2 deltas) 无预分配浪费

### 3. Cache Line 对齐

**代码位置**: `page_info.go:32-56`

```go
type PageInfo struct {
    // Cache Line 1 (64 bytes) - 热数据（高并发访问）
    pos  atomic.Int64 // 8 bytes
    page any          // 8 bytes
    pageLock atomic.Value // 8 bytes
    ...
    _    [24]byte     // padding to 64 bytes
}
```

**效果**:
- ✅ 减少伪共享 (false sharing)
- ✅ 提升并发读性能

---

## 性能瓶颈分析

### P0: findLeafPageRef 双遍历

**代码位置**: `search_path.go:160-240`

```go
func (b *BTree) findLeafPageRef(ctx context.Context, key []byte) (*PageRef, []*PageInfo, error) {
    // Step 1: 第一次遍历 - searchPath()
    path, err := b.searchPath(ctx, key)  // ← 遍历 1
    if err != nil {
        return nil, nil, err
    }

    // Step 2: 第二次遍历 - 收集 PageRef
    currentRef := b.rootRef.PageRef

    for i := 0; i < len(path)-1; i++ {  // ← 遍历 2
        ...
    }
}
```

**问题**:
- `searchPath()` 已经遍历了一次路径
- `findLeafPageRef()` 再次遍历收集 PageRef
- 重复工作浪费 CPU 时间

**改进** (P0):
```go
// 方案 1: 修改 searchPath 返回 PageRef 链
func (b *BTree) searchPathWithRefs(ctx context.Context, key []byte) ([]*PageInfo, []*PageRef, error) {
    // 一次遍历同时收集 PageInfo 和 PageRef
}

// 方案 2: 在 searchPath 中记录 PageRef
type pathNode struct {
    info  *PageInfo
    ref   *PageRef
}
```

**预期收益**: ~5% 写入性能提升

### P1: handleSplitSync 不必要的数组拷贝

**代码位置**: `leaf_lock_set.go:143-146`

```go
// 保存原始状态，用于 CAS 失败时恢复
originalKeys := make([][]byte, len(leafPage.keys))
copy(originalKeys, leafPage.keys)
originalValues := make([][]byte, len(leafPage.values))
copy(originalValues, leafPage.values)
```

**问题**:
- 每次分裂都拷贝 keys 和 values
- CAS 失败概率极低（锁保护下）
- 大部分拷贝是浪费的

**改进** (P1):
```go
// 方案 1: 懒拷贝（仅在 CAS 失败时拷贝）
var originalKeys, originalValues [][]byte
var copied bool

// CAS 失败时才拷贝
func restoreIfNeeded() error {
    if !copied {
        // 此时才拷贝
        originalKeys = make([][]byte, len(leafPage.keys))
        copy(originalKeys, leafPage.keys)
        ...
    }
    ...
}
```

**预期收益**: ~2% 分裂性能提升

### P2: 内存分配热点

**Delta Chain append** (cow_delta_ref.go:131-136)

```go
func (r *COWDeltaRef) AppendDelta(delta Delta) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.deltas = append(r.deltas, delta)  // ← 可能触发数组扩容
    r.version.Add(1)
}
```

**问题**:
- append 可能触发数组扩容和内存分配
- 高频写入场景下增加 GC 压力

**改进** (P2):
```go
// 预分配一定容量（平衡内存和性能）
deltas: make([]Delta, 0, 8),  // 预分配 8 个
```

**权衡**: 需要评估内存浪费 vs 扩容开销

---

## 内存分析

### 内存分配热点

| 分配点 | 频率 | 大小 | 优化状态 |
|--------|------|------|----------|
| Delta Chain append | 高 | 可变 | ✅ 已优化（按需增长） |
| PageLock 创建 | 低 | 固定 | ✅ 已优化（懒加载） |
| 路径克隆 | 中 | 可变 | ⚠️ 可优化 |

### 内存泄漏检查

✅ **结论**: 无明显内存泄漏
- COWDeltaRef 使用引用计数，有 Release() 机制
- PageRef 使用原子指针，无循环引用
- race detector 通过，无数据竞争

---

## 性能改进建议

### P0: 合并 findLeafPageRef 双遍历

**当前**:
```go
path, _ := b.searchPath(ctx, key)      // 遍历 1
currentRef := b.rootRef.PageRef
for i := 0; i < len(path)-1; i++ {      // 遍历 2
    ...
}
```

**优化**:
```go
path, refs, _ := b.searchPathWithRefs(ctx, key)  // 一次遍历
```

**预期收益**: ~5% 写入性能提升

### P1: 懒拷贝优化

**当前**: 每次分裂都拷贝 keys/values
**优化**: 仅在 CAS 失败时拷贝

**预期收益**: ~2% 分裂性能提升

### P2: Delta Chain 预分配

**当前**: `deltas: make([]Delta, 0)`
**优化**: `deltas: make([]Delta, 0, 8)`

**预期收益**: 减少 append 扩容开销

---

## 总结

| 评估项 | 评分 | 说明 |
|--------|------|------|
| 性能目标达成 | 9/10 | 8 线程 3.8M (目标 2.5M) ✅ |
| 优化有效性 | 10/10 | PageLock 懒加载、Delta 按需增长 |
| 扩展性 | 9/10 | 8 线程 5.8x 扩展比 |
| 内存效率 | 9/10 | 减少内存分配，Cache Line 对齐 |
| 性能瓶颈识别 | 8/10 | 发现 P0 双遍历问题 |
| **总体评分** | **9/10** | **性能优化显著** |

**结论**: Leaf-Level Locking 性能优化达到预期，8 线程性能提升 5.8x。建议合并前解决 P0 问题（findLeafPageRef 双遍历），预期额外提升 5%。
