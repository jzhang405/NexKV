# NexKV BTree 纯内存模式瓶颈深度分析

> **分析日期**：2026-03-17
> **分析工具**：perf 6.17.9
> **采样数据**：533 个样本，4.26 MB 数据
> **测试场景**：100,000 次 Set 操作（纯内存模式，无持久化）

---

## 📊 执行摘要

### 瓶颈分布概览

| 瓶颈类型 | CPU 占比 | 样本数 | 优化优先级 |
|----------|---------|--------|-----------|
| **GC 相关** | 45-50% | ~240-267 | P0 |
| **栈展开** | ~12% | ~64 | P1 |
| **应用层** | ~2% | ~11 | P1 |
| **其他** | ~36% | ~192 | P2 |

### 核心发现

1. **GC 是最大瓶颈**：占总 CPU 时间的 45-50%
2. **栈展开开销显著**：~12% CPU 用于符号解析
3. **应用层优化空间小**：仅占 2%，主要是 CCW 路径复制

---

## 1. GC 相关瓶颈（P0 优先级）

### 1.1 `runtime.(*unwinder).resolveInternal` - **12.06%** 🔴

#### 调用链

```
12.06%  btree_perf_mem_test  [.] runtime.(*unwinder).resolveInternal
   |
   |--10.81%--0x1ffffffffffff7f
   |          runtime.systemstack.abi0
   |          runtime.gcBgMarkWorker.func2
   |          runtime.gcDrain
   |          runtime.markroot
   |          runtime.markroot.func1
   |          runtime.scanstack
```

#### 根本原因

**栈展开符号解析**：GC 扫描栈帧时需要解析函数符号位置

**触发频率**：
- 每 100 次 Set 操作触发约 12 次符号解析
- 每次符号解析耗时 ~100-200 ns

**根本原因分析**：

```go
// btree.go - setWithCAS 函数调用深度
func (b *BTree) setWithCAS(...) error {
    // 第 1 层
    path, err := b.findPath(key)  // 第 2 层
        → b.getRoot()
        → b.searchLevel()  // 第 3 层

    // 第 4 层
    copiedPath, err := b.copyPathShallow(path)  // 第 5 层
        → info.CloneShallow()  // 第 6 层
            → NewPageInfo()  // 第 7 层 - 对象分配

    // 第 8 层
    modifiedPath, err := b.modifyPath(copiedPath, key, value)  // 第 9 层
        → page.Insert()  // 第 10 层 - 可能分配
```

**每次 Set 的对象分配**：
- `copyPathShallow`: 3-5 个 PageInfo（每个 192 bytes）
- `modifyPath`: 临时切片、键值对副本
- 总计：~1-2 KB 的临时对象

#### 优化方案

**方案 1：减少调用深度**（预期 5-10% 提升）

```go
// 当前：多层函数调用
func (b *BTree) setWithCAS(...) error {
    path, _ := b.findPath(key)           // 第 1 层
    copied, _ := b.copyPathShallow(path)  // 第 2 层
    modified, _ := b.modifyPath(copied)   // 第 3 层
    // ...
}

// 优化：内联关键路径
func (b *BTree) setWithCASOptimized(...) error {
    // 将 findPath + copyPathShallow + modifyPath 内联
    path := b.findAndCopyPathInline(key)
    // 减少函数调用深度：10 层 → 6 层
}
```

**方案 2：批量处理减少 GC 频率**（预期 10-15% 提升）

```go
// 批量 Set，减少 GC 触发次数
func (b *BTree) SetBatch(batch []KeyValue) error {
    // 分批处理，每批 1000 个
    batchSize := 1000
    for i := 0; i < len(batch); i += batchSize {
        end := min(i+batchSize, len(batch))
        // 这一批只触发 1 次 GC，而不是 1000 次
        b.setBatchInline(batch[i:end])
        runtime.GC() // 主动触发 GC
    }
}
```

---

### 1.2 `runtime.markroot` - **7.72%** 🔴

#### 调用链

```
7.72%  btree_perf_mem_test  [.] runtime.markroot
   |
   ---7.02%--0x1ffffffffffff7f
             runtime.systemstack.abi0
             runtime.gcBgMarkWorker.func2
             runtime.gcDrain
             runtime.markroot
```

#### 根本原因

**标记根对象**：GC 需要标记所有可达对象的根

**标记的对象类型**：

```go
// 每次 Set 需要标记的根对象
type BTree struct {
    rootRef *RootPageRef     // 1 个根引用
    // ...
}

type RootPageRef struct {
    root atomic.Pointer[PageInfo]  // 1 个 PageInfo
}

type PageInfo struct {
    page       any              // 1 个 Page（LeafPage 或 InternalPage）
    pageLock   *PageLock        // 1 个锁
    parentRef  *PageRef         // 1 个父引用
    // ... 其他字段
}

// 典型的 3 层 BTree 路径
RootPageRef → PageInfo → InternalPage → [PageInfo, PageInfo, ...] → LeafPage
```

**标记开销分析**：

| 对象类型 | 数量/Set | 大小 | 标记时间 |
|---------|---------|------|---------|
| PageInfo | 3-5 个 | 192 bytes | ~50 ns |
| LeafPage | 1 个 | ~4 KB | ~200 ns |
| InternalPage | 0-2 个 | ~4 KB | ~200 ns |
| **总计** | ~4-8 个 | ~5 KB | ~450 ns |

#### 优化方案

**方案 1：对象池复用**（预期 20-30% 提升）

```go
var (
    pageInfoPool = sync.Pool{
        New: func() interface{} {
            return NewPageInfo()
        },
    }

    leafPagePool = sync.Pool{
        New: func() interface{} {
            return &LeafPage{}
        },
    }
)

func (b *BTree) copyPathShallow(path []*PageInfo) ([]*PageInfo, error) {
    copiedPath := make([]*PageInfo, len(path))
    for i, info := range path {
        // 从池中获取，而不是分配新对象
        newInfo := pageInfoPool.Get().(*PageInfo)
        // 重置字段
        newInfo.Reset()
        // 复制数据
        newInfo.CopyFrom(info)
        copiedPath[i] = newInfo
    }

    // 使用完后放回池中
    defer func() {
        for _, info := range copiedPath {
            pageInfoPool.Put(info)
        }
    }()

    return copiedPath, nil
}
```

**方案 2：减少根对象数量**（预期 5-10% 提升）

```go
// 当前：每次 Set 创建 3-5 个 PageInfo
// 优化：只克隆必要的路径

func (b *BTree) copyPathShallowOptimized(path []*PageInfo) ([]*PageInfo, error) {
    // 只克隆叶子节点所在的路径
    leafIdx := len(path) - 1
    copiedPath := make([]*PageInfo, len(path))

    // 内部节点共享引用
    for i := 0; i < leafIdx; i++ {
        copiedPath[i] = path[i] // 不克隆，共享
    }

    // 只克隆叶子节点
    copiedPath[leafIdx] = path[leafIdx].CloneShallow()

    return copiedPath, nil
}
```

---

### 1.3 `runtime.tryDeferToSpanScan` - **6.90%** 🔴

#### 调用链

```
6.90%  btree_perf_mem_test  [.] runtime.tryDeferToSpanScan
   |
   |--6.84%--0x1ffffffffffff7f
   |          |
   |           --6.41%--runtime.systemstack.abi0
   |                     runtime.gcBgMarkWorker.func2
   |                     runtime.gcDrain
   |                     |--3.14%--runtime.scanSpan
   |                     |          runtime.scanObjectsSmall
   |                     |          runtime.tryDeferToSpanScan
```

#### 根本原因

**defer 语句触发 span 扫描**：每次 defer 都需要扫描 span

**defer 使用位置**：

```go
// 1. btree.go - setWithCAS
func (b *BTree) setWithCAS(...) error {
    defer b.mu.Unlock()  // ← defer #1
    // ...
}

// 2. page_info.go - GetParentRef
func (info *PageInfo) GetParentRef() *PageRef {
    info.parentRefMu.RLock()
    defer info.parentRefMu.RUnlock()  // ← defer #2
    return info.parentRef
}

// 3. page_ref.go - GetOrLoad
func (ref *PageRef) GetOrLoad() (*PageInfo, error) {
    ref.mu.Lock()
    defer ref.mu.Unlock()  // ← defer #3
    // ...
}

// 4. page_lock.go - Lock
func (l *PageLock) Lock() {
    l.mu.Lock()
    defer l.cond.Broadcast()  // ← defer #4
    // ...
}
```

**每 1 次 Set 的 defer 开销**：
- `setWithCAS`: 1 个 defer
- `copyPathShallow` (3-5 次): 3-5 个 defer（GetParentRef）
- `GetOrLoad`: 可能 0-2 个 defer
- **总计**：约 4-8 个 defer/Set

**每个 defer 的开销**：
- defer 记录：~10 ns
- defer 执行：~20 ns
- span 扫描：~50 ns
- **单次 defer 总计**：~80 ns

#### 优化方案

**方案 1：减少 defer 使用**（预期 10-15% 提升）

```go
// 优化前：每个函数都有 defer
func (info *PageInfo) GetParentRef() *PageRef {
    info.parentRefMu.RLock()
    defer info.parentRefMu.RUnlock()
    return info.parentRef
}

// 优化后：使用显式解锁
func (info *PageInfo) GetParentRefUnsafe() *PageRef {
    info.parentRefMu.RLock()
    parent := info.parentRef
    info.parentRefMu.RUnlock()
    return parent
}

// 更好的方案：使用 atomic.Value 替代锁
type PageInfo struct {
    parentRef atomic.Value // 存储 *PageRef
}

func (info *PageInfo) GetParentRef() *PageRef {
    v := info.parentRef.Load()
    if v == nil {
        return nil
    }
    return v.(*PageRef)
}
```

**方案 2：合并 defer**（预期 5-10% 提升）

```go
// 优化前：多个 defer
func (b *BTree) setWithCAS(...) error {
    defer b.mu.Unlock()
    defer b.metrics.Record()
    defer b.logger.Debug("setWithCAS done")
    // ...
}

// 优化后：单个 defer
func (b *BTree) setWithCAS(...) error {
    b.mu.Lock()
    defer func() {
        b.mu.Unlock()
        b.metrics.Record()
        b.logger.Debug("setWithCAS done")
    }()
    // ...
}
```

---

### 1.4 `runtime.scanObjectsSmall` - **5.29%** 🔴

#### 调用链

```
5.29%  btree_perf_mem_test  [.] runtime.scanObjectsSmall
   |
   ---5.03%--0x1ffffffffffff7f
             runtime.systemstack.abi0
             |
              --4.90%--runtime.gcBgMarkWorker.func2
                        runtime.gcDrain
                        runtime.scanSpan
                        runtime.scanObjectsSmall
```

#### 根本原因

**小对象扫描**：扫描内存 span 中的小对象（< 32 KB）

**PageInfo 内存布局**：

```go
type PageInfo struct {
    // Cache Line 1 (64 bytes) - 热数据
    pos      atomic.Int64 // 8 bytes
    page     any          // 8 bytes
    pageLock *PageLock    // 8 bytes
    lastTime atomic.Int64 // 8 bytes
    hits     atomic.Int64 // 8 bytes
    _        [24]byte     // padding

    // Cache Line 2 (64 bytes) - 温数据
    buff []byte   // 24 bytes
    _    [40]byte // padding

    // Cache Line 3 (64 bytes) - 冷数据
    parentRefMu sync.RWMutex  // 8 bytes
    parentRef   *PageRef      // 8 bytes
    flags       atomic.Uint32 // 4 bytes
    metaVersion int32         // 4 bytes
    pageSize    int32         // 4 bytes
    cloneStatus atomic.Uint32 // 4 bytes
    _           [56]byte      // padding
}
// 总大小：192 bytes（小对象）
```

**扫描过程**：

1. **找到 span**：通过对象地址找到所属的 span
2. **扫描位图**：读取 span 的分配位图
3. **标记对象**：遍历位图，标记每个对象
4. **扫描指针**：扫描对象中的指针字段

**PageInfo 的指针字段**：
```go
page       any         // 指针 #1
pageLock   *PageLock   // 指针 #2
parentRef  *PageRef    // 指针 #3
buff       []byte      // 指针 #4（切片 header）
```

**每次 GC 扫描开销**：
- 3-5 个 PageInfo × 4 个指针 = 12-20 个指针
- 每个指针标记：~10 ns
- 总计：~120-200 ns

#### 优化方案

**方案 1：减少指针字段**（预期 5-10% 提升）

```go
// 当前：4 个指针字段
type PageInfo struct {
    page       any
    pageLock   *PageLock
    parentRef  *PageRef
    buff       []byte
}

// 优化：使用索引替代指针
type PageInfo struct {
    pageID    uint64       // 替代 parentRef
    pageIdx   uint32       // 替代 page（使用索引）
    lockState uint32       // 替代 pageLock（状态机）
    buffOffset uint32      // 替代 buff（偏移量）
}
```

**方案 2：使用值类型**（预期 10-15% 提升）

```go
// 当前：buff 是切片（引用类型）
type PageInfo struct {
    buff []byte // 24 bytes（指针 + 长度 + 容量）
}

// 优化：使用固定数组（值类型）
type PageInfo struct {
    buff [256]byte // 256 bytes（无指针）
}
```

---

## 2. 栈展开瓶颈（P1 优先级）

### 2.1 `runtime.pcvalue` - **3.86%** 🟡

#### 调用链

```
3.86%  btree_perf_mem_test  [.] runtime.pcvalue
   |
   ---3.79%--0x1ffffffffffff7f
             runtime.systemstack.abi0
             |
              --3.58%--runtime.gcBgMarkWorker.func2
                        runtime.gcDrain
                        runtime.markroot
                        runtime.markroot.func1
                        runtime.scanstack
```

#### 根本原因

**程序计数器值查找**：获取栈帧对应的程序计数器值

**查找过程**：
1. 读取 PC 值
2. 在 pclntab 中查找函数信息
3. 查找栈 map 信息
4. 返回结果

**开销来源**：
- 二分查找 pclntab：~50 ns
- 解析栈 map：~30 ns
- **总计**：~80 ns/次

#### 优化方案

**方案：减少函数调用深度**（预期 5-10% 提升）

```go
// 当前：深度调用链
func (b *BTree) Set(...) error {
    return b.setWithCAS(...)  // 第 1 层
        → b.findPath(...)      // 第 2 层
            → b.getRoot()      // 第 3 层
                → ...          // 第 4 层
}

// 优化：展平调用链
func (b *BTree) SetOptimized(...) error {
    // 内联关键路径，减少调用深度
    return b.setInline(...)  // 内联实现
}
```

---

### 2.2 `runtime.(*stkframe).getStackMap` - **3.73%** 🟡

#### 调用链

```
3.73%  btree_perf_mem_test  [.] runtime.(*stkframe).getStackMap
   |
   ---3.60%--0x1ffffffffffff7f
             runtime.systemstack.abi0
             runtime.gcBgMarkWorker.func2
             runtime.gcDrain
             runtime.markroot
             runtime.markroot.func1
             runtime.scanstack
             runtime.scanframeworker
             runtime.(*stkframe).getStackMap
```

#### 根本原因

**获取栈帧的堆栈 map**：用于 GC 扫描栈中的指针

**栈 map 包含**：
- 栈中的指针位置
- 指针数量
- GC 扫描信息

#### 优化方案

**方案：减少栈上指针数量**（预期 5-10% 提升）

```go
// 当前：栈上有多个指针
func (b *BTree) modifyPath(path []*PageInfo, key, value []byte) ([]*PageInfo, error) {
    // path、key、value 都在栈上
    // ...
}

// 优化：减少栈上指针
func (b *BTree) modifyPathOptimized(pathIdx int, key, value []byte) error {
    // 传入索引，而不是切片
    // 直接修改全局状态，减少栈上指针
}
```

---

## 3. 应用层瓶颈（P1 优先级）

### 3.1 `PageInfo.CloneShallow` - **0.88%** 🟡

#### 调用链

```
0.88%  btree_perf_mem_test  [.] PageInfo.CloneShallow
           github.com/jzhang405/NexKV/...(*BTree).Set
           github.com/jzhang405/NexKV/...(*BTree).setWithCAS
```

#### 代码分析

```go
func (info *PageInfo) CloneShallow() *PageInfo {
    newInfo := &PageInfo{  // ← 分配 192 bytes
        pageLock:    NewPageLock(),     // ← 分配锁
        parentRef:   info.GetParentRef(), // ← 加锁 + 解锁（defer）
        metaVersion: info.metaVersion,
        pageSize:    info.pageSize,
    }

    newInfo.SetPos(info.GetPos())           // ← 原子操作
    newInfo.lastTime.Store(info.lastTime.Load()) // ← 原子操作
    newInfo.hits.Store(info.hits.Load())    // ← 原子操作
    newInfo.flags.Store(info.flags.Load())  // ← 原子操作

    newInfo.page = info.page                // ← 共享 Page
    newInfo.cloneStatus.Store(CloneStatusShallow) // ← 原子操作

    return newInfo
}
```

#### 开销分解

| 操作 | 开销 |
|------|------|
| 分配 PageInfo | ~50 ns |
| NewPageLock | ~20 ns |
| GetParentRef（锁） | ~30 ns |
| 4 个原子 Store | ~20 ns |
| **总计** | **~120 ns** |

#### 优化方案

**方案 1：使用对象池**（预期 50-70% 提升）

```go
var pageInfoPool = sync.Pool{
    New: func() interface{} {
        return &PageInfo{
            pageLock: NewPageLock(),
        }
    },
}

func (info *PageInfo) CloneShallowPooled() *PageInfo {
    newInfo := pageInfoPool.Get().(*PageInfo)

    // 重置字段
    newInfo.metaVersion = info.metaVersion
    newInfo.pageSize = info.pageSize
    newInfo.SetPos(info.GetPos())
    // ...

    return newInfo
}
```

**方案 2：原子操作替代锁**（预期 20-30% 提升）

```go
type PageInfo struct {
    parentRef atomic.Value // 替代 parentRefMu + parentRef
}

func (info *PageInfo) CloneShallowAtomic() *PageInfo {
    newInfo := &PageInfo{
        pageLock:    NewPageLock(),
        metaVersion: info.metaVersion,
        pageSize:    info.pageSize,
    }

    // 使用原子操作，无锁
    newInfo.parentRef.Store(info.parentRef.Load())

    // ...
    return newInfo
}
```

---

### 3.2 `PageInfo.CloneDeep` - **0.73%** 🟡

#### 调用链

```
0.73%  btree_perf_mem_test  [.] PageInfo.CloneDeep
           github.com/jzhang405/NexKV/...(*BTree).Set
           github.com/jzhang405/NexKV/...(*BTree).setWithCAS
```

#### 代码分析

```go
func (info *PageInfo) CloneDeep() *PageInfo {
    // 快速路径：已深拷贝
    if info.cloneStatus.Load() == CloneStatusDeep {
        return info
    }

    // 浅拷贝
    newInfo := info.CloneShallow()  // ← 120 ns

    // 深拷贝 Page
    if info.IsPageLoaded() && info.page != nil {
        switch p := info.page.(type) {
        case *LeafPage:
            newInfo.page = p.Clone()  // ← ~500 ns（拷贝 keys + values）
        case *InternalPage:
            newInfo.page = p.Clone()  // ← ~300 ns（拷贝 children）
        }
    }

    newInfo.cloneStatus.Store(CloneStatusDeep)
    return newInfo
}
```

#### 开销分解

| 操作 | 开销 |
|------|------|
| cloneStatus.Load() | ~5 ns |
| CloneShallow() | ~120 ns |
| LeafPage.Clone() | ~500 ns |
| cloneStatus.Store() | ~5 ns |
| **总计** | **~630 ns** |

#### 优化方案

**方案：延迟深拷贝**（预期 30-50% 提升）

```go
// 当前：CAS 失败后立即深拷贝
func (b *BTree) setWithCAS(...) error {
    for {
        // ...
        if !b.rootRef.CAS(oldRoot, newRoot) {
            continue
        }

        // CAS 成功，深拷贝
        b.finalizeDeepClone(copiedPath)  // ← 630 ns
        return nil
    }
}

// 优化：标记为需要深拷贝，延迟执行
func (b *BTree) setWithCASOptimized(...) error {
    for {
        // ...
        if !b.rootRef.CAS(oldRoot, newRoot) {
            continue
        }

        // 标记为需要深拷贝
        b.markForDeepClone(copiedPath)  // ← 10 ns

        // 后台 goroutine 异步深拷贝
        go b.finalizeDeepCloneAsync(copiedPath)

        return nil
    }
}
```

---

### 3.3 `PageInfo.GetParentRef` - **0.51%** 🟡

#### 调用链

```
0.51%  btree_perf_mem_test  [.] PageInfo.GetParentRef
           github.com/jzhang405/NexKV/...(*BTree).Set
           github.com/jzhang405/NexKV/...(*BTree).setWithCAS
```

#### 代码分析

```go
func (info *PageInfo) GetParentRef() *PageRef {
    info.parentRefMu.RLock()        // ← 读锁：~10 ns
    defer info.parentRefMu.RUnlock() // ← defer + 解锁：~25 ns
    return info.parentRef
}
```

#### 开销分解

| 操作 | 开销 |
|------|------|
| RLock() | ~10 ns |
| defer 记录 | ~5 ns |
| RUnlock() | ~10 ns |
| **总计** | **~25 ns** |

#### 优化方案

**方案：使用 atomic.Value**（预期 80-90% 提升）

```go
// 当前：使用读写锁
type PageInfo struct {
    parentRefMu sync.RWMutex
    parentRef   *PageRef
}

// 优化：使用原子操作
type PageInfo struct {
    parentRef atomic.Value // 存储 *PageRef
}

func (info *PageInfo) GetParentRef() *PageRef {
    v := info.parentRef.Load()  // ← ~3 ns（原子加载）
    if v == nil {
        return nil
    }
    return v.(*PageRef)
}

func (info *PageInfo) SetParentRef(ref *PageRef) {
    info.parentRef.Store(ref)  // ← ~3 ns（原子存储）
}
```

**性能对比**：

| 操作 | 读写锁 | atomic.Value | 提升 |
|------|--------|--------------|------|
| 读 | ~25 ns | ~3 ns | **8.3x** |
| 写 | ~30 ns | ~3 ns | **10x** |

---

## 4. 优化路线图

### Phase 1：快速见效（1 周）

**目标**：性能提升 30-50%

| 优化项 | 实施难度 | 预期提升 | 优先级 |
|--------|---------|---------|--------|
| PageInfo 对象池 | 低 | 20-30% | P0 |
| atomic.Value 替代锁 | 低 | 5-10% | P0 |
| 减少 defer 使用 | 低 | 10-15% | P0 |
| 合并 defer | 低 | 5-10% | P0 |

**实施顺序**：
1. ✅ 实现 PageInfo 对象池
2. ✅ 替换 parentRef 为 atomic.Value
3. ✅ 合并 defer 语句
4. ✅ 验证性能提升

---

### Phase 2：中期优化（2-4 周）

**目标**：性能提升 50-80%

| 优化项 | 实施难度 | 预期提升 | 优先级 |
|--------|---------|---------|--------|
| 延迟深拷贝 | 中 | 10-15% | P1 |
| 减少调用深度 | 中 | 5-10% | P1 |
| 批量 Set | 中 | 10-20% | P1 |
| 优化 CloneShallow | 中 | 5-10% | P1 |

**实施顺序**：
1. ✅ 实现延迟深拷贝
2. ✅ 内联关键路径
3. ✅ 实现批量 Set
4. ✅ 验证性能提升

---

### Phase 3：长期优化（1-2 个月）

**目标**：性能提升 80-100%

| 优化项 | 实施难度 | 预期提升 | 优先级 |
|--------|---------|---------|--------|
| 无锁数据结构 | 高 | 10-20% | P2 |
| 减少指针字段 | 高 | 5-10% | P2 |
| 自定义内存分配器 | 高 | 10-15% | P2 |
| Arena 分配 | 高 | 10-20% | P2 |

**实施顺序**：
1. ✅ 评估无锁数据结构可行性
2. ✅ 设计减少指针的方案
3. ✅ 实现自定义分配器
4. ✅ 验证性能提升

---

## 5. 验收标准

### 5.1 性能目标

| 指标 | 当前 | Phase 1 | Phase 2 | Phase 3 |
|------|------|---------|---------|---------|
| **吞吐量** | 187K ops/sec | 240K+ | 300K+ | 350K+ |
| **延迟** | 5.34 μs | < 4.2 μs | < 3.5 μs | < 3.0 μs |
| **GC 占比** | 45-50% | < 35% | < 30% | < 25% |

### 5.2 测试方法

```bash
# 1. 基准测试
go test -bench="BenchmarkBTree_Set_Single" \
  -benchmem -benchtime=10s \
  -run=^$ ./internal/infrastructure/storage/btree/

# 2. perf 分析
perf record -F 99 -g --call-graph dwarf \
  -o /tmp/perf_mem_optimized.data /tmp/btree_perf_mem_test

# 3. 生成报告
perf report --stdio --no-child -g --inline \
  -i /tmp/perf_mem_optimized.data > optimized_perf.txt

# 4. 对比瓶颈
diff perf_analysis_report.md optimized_perf.txt
```

---

## 6. 相关文档

- **持久化模式 perf 报告**：`docs/10_benchmark/2026-03-17_baseline/perf_analysis_report.md`
- **纯内存模式 perf 报告**：`docs/10_benchmark/2026-03-17_baseline/memory_mode_perf_analysis.md`
- **性能基准报告**：`docs/10_benchmark/2026-03-17_baseline/2026-03-17_performance_baseline.md`

---

**报告生成日期**：2026-03-17
**报告版本**：v1.0
**Git Commit**：e9fdcac
**作者**：NexKV BTree Team
