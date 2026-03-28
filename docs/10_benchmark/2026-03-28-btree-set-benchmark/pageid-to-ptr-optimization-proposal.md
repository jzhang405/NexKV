# PageIDToPtr 优化 Proposal

日期: 2026-03-28
状态: Draft (Code Review Updated)
CPU 占比: 3.6% flat / 3.6% cum（1T 50K ops, 纯内存模式）

## 当前实现

```go
// offheap/page_manager.go:133 — 带边界检查的版本（测试/外部调用用）
func (pm *PageManager) PageIDToPtr(pageID uint32) unsafe.Pointer {
    if pageID >= pm.total {
        panic(fmt.Sprintf("pageID %d out of range (total: %d)", pageID, pm.total))
    }
    offset := uintptr(pageID) * PageSize
    return unsafe.Add(pm.base, offset)
}

// offheap/page_manager.go:142 — 无边界检查版本（热路径内部用）
//go:nosplit
func (pm *PageManager) pageIDToPtrUnchecked(pageID uint32) unsafe.Pointer {
    return unsafe.Add(pm.base, uintptr(pageID)*PageSize)
}
```

## 已完成的优化（Kimi 实现）

代码已部分优化：

1. **`pageIDToPtrUnchecked`** — 无边界检查的内部版本
2. **`PageAccessor.getPtr()`** — 带 `lastID`/`lastPtr` 缓存的单页面指针缓存
3. **`page_layout.go` 中所有方法** 已使用 `pa.getPtr(pageID)` 替代直接调用 `PageIDToPtr`

```go
// offheap/page_layout.go:120-137
type PageAccessor struct {
    pm      *PageManager
    lastID  uint32          // 缓存：上次访问的 pageID
    lastPtr unsafe.Pointer  // 缓存：上次计算的 ptr
}

func (pa *PageAccessor) getPtr(pageID uint32) unsafe.Pointer {
    if pageID != pa.lastID {
        pa.lastID = pageID
        pa.lastPtr = pa.pm.pageIDToPtrUnchecked(pageID)
    }
    return pa.lastPtr
}
```

## Code Review 发现

### 发现 1: `getPtr()` 缓存的命中率问题

`PageAccessor` 是全局单例（在 `OffHeapAdapter` 中创建一次），被所有 goroutine 共享。

**并发竞态**: `lastID`/`lastPtr` 字段无锁保护，多线程并发读写会导致数据竞争。

**缓存命中率低**: BTree 操作的典型访问模式：
```
搜索路径: root → internal1 → internal2 → leaf
（每层 pageID 不同，缓存命中率 ≈ 0%）
```
仅在循环遍历同一页面条目时有效（如 `GetDataEnd`, `SearchKey`）。

### 发现 2: `PageIDToPtr` 未被编译器内联 — 根因是 `fmt.Sprintf`

编译器输出：
```
cannot inline (*PageManager).PageIDToPtr: function too complex: cost 87 exceeds budget 80
```

`fmt.Sprintf` 贡献了约 30+ 的 inline cost。**移除 `fmt.Sprintf` 后 cost 降到 ~57，编译器会自动内联**。

这是投入产出比最高的单行优化。

### 发现 3: `//go:nosplit` 使用正确但非最优

`//go:nosplit` 跳过栈分裂检查（适用于极小函数），但该函数已经足够简单，编译器应能自动判断。更有效的是 `//go:inline`（Go 1.22+），本项目 go.mod 使用 Go 1.25，完全支持。

### 发现 4: `SearchKey` 中的重复指针计算

`SearchKey` 在二分查找循环中，每次迭代通过 `GetLeafEntry`/`GetIndexEntry` → `getPtr()` → `pageIDToPtrUnchecked` 计算指针。由于每次都是相同 pageID，`getPtr()` 缓存命中，但仍有比较+分支开销。更好的方式是在循环外计算一次指针。

## 优化方案（按投入产出比排序）

### 方案 1: 移除 `fmt.Sprintf`（1 行改动，预期 -2% CPU）

```go
// 优化前: cost 87，不内联
func (pm *PageManager) PageIDToPtr(pageID uint32) unsafe.Pointer {
    if pageID >= pm.total {
        panic(fmt.Sprintf("pageID %d out of range (total: %d)", pageID, pm.total))
    }
    ...
}

// 优化后: cost ~57，编译器自动内联
func (pm *PageManager) PageIDToPtr(pageID uint32) unsafe.Pointer {
    if pageID >= pm.total {
        panic("pageID out of range")
    }
    return unsafe.Add(pm.base, uintptr(pageID)*PageSize)
}
```

**收益**: 消除 ~2% CPU（函数调用开销 + fmt.Sprintf 的 cost 阻塞内联）
**风险**: 无。panic 消息丢失了 pageID/total 数值，但不影响正确性（debug 时可通过 pprof 定位）

### 方案 2: 重构 `SearchKey` 内联指针计算（~30 行改动，预期 -1-1.5% CPU）

```go
// 当前: 每次迭代通过 getPtr() → 比较 lastID → pageIDToPtrUnchecked
func (pa *PageAccessor) SearchKey(pageID uint32, key []byte, isLeaf bool) (int, bool) {
    for low, high := 0, int(pa.GetCount(pageID))-1; low <= high; {
        mid := (low + high) / 2
        // 每次迭代: GetLeafEntry → getPtr(pageID) → 比较+分支
        ...
    }
}

// 优化: 循环外计算一次 ptr，循环内直接用
func (pa *PageAccessor) SearchKey(pageID uint32, key []byte, isLeaf bool) (int, bool) {
    ptr := pa.getPtr(pageID)
    header := (*PageHeader)(ptr)
    count := int(header.count)
    // 直接在 ptr 上做二分查找，不再调用 getPtr()
    ...
}
```

**收益**: 消除二分查找循环内的 lastID 比较+分支（~1-1.5% CPU）
**风险**: 低。SearchKey 是只读操作，无并发写入风险

### 方案 3: 修复 `getPtr()` 并发安全问题

```go
// 当前: 无锁保护，多线程不安全
type PageAccessor struct {
    pm      *PageManager
    lastID  uint32
    lastPtr unsafe.Pointer
}

// 选项 A: 移除缓存（如果命中率本身就低）
type PageAccessor struct {
    pm *PageManager
}

func (pa *PageAccessor) getPtr(pageID uint32) unsafe.Pointer {
    return pa.pm.pageIDToPtrUnchecked(pageID)
}

// 选项 B: 使用 goroutine-local PageAccessor（每请求创建）
// 如果确认性能收益大于分配开销
```

**收益**: 消除数据竞争风险（correctness fix，非性能优化）
**风险**: 选项 A 可能略降低单线程性能（失去缓存）；选项 B 增加 GC 压力

### 方案 4: 给 `pageIDToPtrUnchecked` 添加 `//go:inline`（1 行改动）

```go
//go:inline
func (pm *PageManager) pageIDToPtrUnchecked(pageID uint32) unsafe.Pointer {
    return unsafe.Add(pm.base, uintptr(pageID)*PageSize)
}
```

**收益**: 强制编译器内联，消除所有调用点的函数调用开销
**风险**: 极低。函数体仅一行

## 推荐执行顺序

| 优先级 | 方案 | 改动量 | 预期收益 | 风险 |
|--------|------|--------|---------|------|
| P0 | 方案 1: 移除 fmt.Sprintf | 1 行 | -2% CPU | 无 |
| P1 | 方案 4: 添加 //go:inline | 1 行 | -0.5% CPU | 极低 |
| P2 | 方案 3: 修复并发安全 | ~10 行 | 正确性修复 | 低 |
| P3 | 方案 2: 重构 SearchKey | ~30 行 | -1-1.5% CPU | 低 |

## 预期总收益

| 指标 | 优化前 | P0+P1 后 | 全部后 |
|------|--------|----------|--------|
| PageIDToPtr 相关 CPU | 3.6% | ~1.5% | ~0.5% |
| 1T 吞吐量 | ~27.5K ops/s | ~28K ops/s | ~28.5K ops/s |
| 延迟 | ~36 μs | ~35.5 μs | ~35 μs |

## 不推荐的方案（原 Proposal 中被否决）

- **原方案 A（手动内联 30+ 处）**: 过于复杂，`//go:inline` 可达到同样效果且零改动
- **原方案 B（指针缓存）**: 已部分实现但并发不安全，且缓存命中率低
- **原方案 C（Debug 模式分离）**: 已有 `pageIDToPtrUnchecked` 实现

## 原始 Proposal 的错误纠正

| 原始论断 | 实际情况 |
|---------|---------|
| "Go 不内联跨包方法调用" | ❌ Go 编译器基于 cost/budget 决定内联，与包无关。`PageIDToPtr` 不内联是因为 `fmt.Sprintf` 使 cost=87 超预算 |
| "30+ 处直接调用 PageIDToPtr" | ❌ 已被 Kimi 优化为 `getPtr()`，当前 `page_layout.go` 中无直接调用 |
| "`//go:nosplit` 消除边界检查" | ❌ `//go:nosplit` 跳过栈分裂检查，与边界检查无关 |
| 预期收益 3.6% → 0.5% | ❌ 过于乐观。考虑到已有缓存，实际可优化空间约 2-3% |
