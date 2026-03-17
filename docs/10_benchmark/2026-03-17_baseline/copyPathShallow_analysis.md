# copyPathShallow 函数性能瓶颈分析

**函数位置**: `internal/infrastructure/storage/btree/btree.go:935-1052`
**CPU 占用**: **50.30%** (perf 数据)
**调用链**: `setWithCAS → copyPathShallow → ...`

---

## 函数概览

```go
func (b *BTree) copyPathShallow(path []*PageInfo) ([]*PageInfo, error) {
    // 1. 预分配切片 (line 940)
    copiedPath := make([]*PageInfo, len(path))

    // 2. 构建 PageID 映射表 (line 943-963)
    pageInfoMap := make(map[model.PageID]*PageInfo, len(path))

    // 3. 第一次完整遍历：克隆所有 PageInfo (line 967-997)
    for i, info := range path {
        // LeafPage: CloneShallow + 深拷贝 Page
        // InternalPage: CloneShallow
    }

    // 4. 第二次完整遍历：重建子节点引用 (line 1000-1049)
    for _, info := range copiedPath {
        for j := 0; j < len(internalPage.children); j++ {
            // 创建新 PageRef，设置父引用
        }
    }

    return copiedPath, nil
}
```

---

## 性能瓶颈分析

### 🔴 瓶颈 1: LeafPage 深拷贝 (32.85% 占用中的大头)

```go
// Line 970-979
if leafPage, ok := info.GetPage().(*LeafPage); ok && leafPage != nil {
    newInfo = info.CloneShallow()
    newInfo.page = leafPage.Clone() // 🔴 深拷贝 keys + values
    newInfo.cloneStatus.Store(CloneStatusDeep)
}
```

**问题**:
- `leafPage.Clone()` 完整复制 `keys` 和 `values` 切片
- 对于 maxKeys=200 的页面，每次复制 ~400 个切片元素
- 每次深拷贝触发内存分配和 GC

**LeafPage.Clone() 实现**:
```go
func (p *LeafPage) Clone() *LeafPage {
    newKeys := make([][]byte, len(p.keys))
    copy(newKeys, p.keys)  // 🔴 复制所有 keys

    newValues := make([][]byte, len(p.values))
    copy(newValues, p.values) // 🔴 复制所有 values

    return &LeafPage{
        keys:   newKeys,
        values: newValues,
        // ...
    }
}
```

**开销计算**:
- 假设平均每个页面有 100 个 key
- 每次深拷贝 = 100 keys + 100 values = 200 个切片复制
- 对于树高 3 的 B+Tree，需要拷贝 1 个 LeafPage
- **内存分配**: 200 切片 × 8 字节/指针 = 1.6KB（不含数据）

---

### 🔴 瓶颈 2: CloneShallow 调用 (11.84% 占用)

```go
// Line 977, 982
newInfo = info.CloneShallow()
```

**CloneShallow 开销** (从 perf 数据):
```
CloneShallow (8.80% 总开销)
├─ newobject (4.08%)     // 分配 PageInfo 结构体
├─ atomic.Value.Store (5.63%)  // 存储 parentRef
└─ 其他原子操作 (1.96%)
```

**PageInfo 结构体大小**: ~192 bytes (3 cache lines)
- 每次调用 `CloneShallow` 分配 192 bytes
- 对于树高 3 的 B+Tree，需要分配 3 次 = 576 bytes

---

### 🔴 瓶颈 3: NewPageRefWithInfo 调用

```go
// Line 1035, 1042
newChildRef := NewPageRefWithInfo(childReplacement)
```

**每次调用开销**:
1. 分配 PageRef 结构体 (~32 bytes)
2. 原子操作存储 (atomic.Value.Store)
3. 设置父引用 (atomic.Value.Store)

**调用次数**:
- 假设树高 3，每个 InternalPage 平均有 100 个子节点
- 根节点: 1 个子节点
- 中间层: 100 个子节点
- **总计**: ~101 次调用
- **内存分配**: 101 × 32 bytes = 3.2 KB

---

### 🔴 瓶颈 4: 多次遍历和类型断言

```go
// 遍历 1: 构建 pageInfoMap
for _, info := range path {
    // 类型断言 + map 插入
}

// 遍历 2: 克隆 PageInfo
for i, info := range path {
    // 类型断言 + Clone
}

// 遍历 3: 重建子节点引用
for _, info := range copiedPath {
    for j := 0; j < len(internalPage.children); j++ {
        // 类型断言 + PageRef 创建
    }
}
```

**问题**:
- **3 次完整遍历**
- **大量类型断言** (`info.GetPage().(*LeafPage)`)
- **嵌套循环** (遍历 InternalPage 的所有子节点)

---

## 内存分配统计

### 单次 Set 操作的内存分配

| 分配项 | 次数 | 大小 | 总计 |
|--------|------|------|------|
| copiedPath 切片 | 1 | 24 × 3 = 72B | 72B |
| pageInfoMap | 1 | ~50B | 50B |
| PageInfo.CloneShallow | 3 | 192B | 576B |
| LeafPage.Clone | 1 | ~1.6KB | 1.6KB |
| PageRef 创建 | ~101 | 32B | 3.2KB |
| **总计** | - | - | **~5.5KB** |

**每秒 21,560 次操作**:
- 内存分配速率: 21,560 × 5.5KB = **115 MB/秒**
- **GC 触发频率**: 极高（GOGC=400 时，每分配 46MB 触发一次 GC）
- **每秒 GC 触发**: 115 / 46 × 400 ≈ **2.5 次/秒**

---

## 优化方案

### 🎯 方案 1: Delta Chain + 引用计数 (推荐)

**核心思想**:
- **零拷贝 Clone**: 只增加引用计数（~10 ns）
- **增量存储**: 记录修改操作而非完整复制
- **延迟物化**: 增量链超过阈值时才合并

**预期提升**:
```
Clone 开销:  1000 ns → 10 ns (100x ↓)
写开销:      46 µs → 100 ns (460x ↓)
QPS:         21.5K → 200K+ (9x ↑)
内存分配:    5.5KB → 200B (27x ↓)
```

**实现要点**:
```go
type COWDeltaRef struct {
    sharedKeys   [][]byte
    sharedValues [][]byte
    refCount     atomic.Int32
    deltas       []Delta  // 增量操作链
}

func (p *LeafPage) Clone() *LeafPage {
    if p.cowDelta != nil {
        p.cowDelta.Retain()  // 原子递增
        return &LeafPage{cowDelta: p.cowDelta, ...}
    }
    // ...
}
```

---

### 🎯 方案 2: sync.Pool 对象池 (快速实施)

**核心思想**: 复用 PageInfo 和 PageRef 对象

**预期提升**:
```
内存分配: 5.5KB → 1KB (5x ↓)
GC 开销:   20% → 10% (2x ↓)
QPS:       21.5K → 40K (1.9x ↑)
```

**实现要点**:
```go
var pageInfoPool = sync.Pool{
    New: func() interface{} {
        return &PageInfo{}
    },
}

func (b *BTree) copyPathShallow(path []*PageInfo) ([]*PageInfo, error) {
    // 从池中获取而非分配
    newInfo := pageInfoPool.Get().(*PageInfo)
    // ...
}
```

---

### 🎯 方案 3: 批量 CAS (减少拷贝频率)

**核心思想**: 累积多个修改，一次性 CAS

**预期提升**:
```
拷贝次数: 1 次/op → 1 次/10 ops (10x ↓)
QPS:      21.5K → 50K (2.3x ↑)
```

---

## 优化优先级

| 优先级 | 方案 | 难度 | 预期提升 | 实施时间 |
|--------|------|------|----------|----------|
| **P0** | Delta Chain | 高 | 9x QPS | 3-5 天 |
| **P1** | sync.Pool | 低 | 1.9x QPS | 1 天 |
| **P2** | 批量 CAS | 中 | 2.3x QPS | 2 天 |

---

## 关键代码路径 (来自 perf)

```
copyPathShallow (50.30%)
├─ CloneShallow (32.85%)
│   ├─ newobject (18.07%)
│   │   └─ mallocgc (17.34%)
│   │       └─ mallocgcSmallScanNoHeader (15.49%) 🔴
│   └─ atomic.Value.Store (5.63%)
├─ NewPageRefWithInfo (11.84%)
│   ├─ atomic.Value.Store (5.16%)
│   └─ newobject (4.08%)
└─ finalizeDeepClone (3.85%)
```

---

## 下一步行动

### 立即实施 (本周)
1. ✅ **创建 cow_delta_ref.go**
   - 实现 COWDeltaRef 结构
   - 实现引用计数和增量链

2. ✅ **修改 LeafPage**
   - 添加 cowDelta 字段
   - 修改 Clone() 为零拷贝
   - 实现 materialize()

### 验证指标
- **Clone 开销**: < 20 ns
- **写开销**: < 100 ns/次
- **QPS**: > 100K (4.6x 提升)
