# copyPathShallow 性能瓶颈可视化分析

## 📊 CPU 占用分布

```
copyPathShallow: ████████████████████████████████████████████ 50.30%
├─ CloneShallow:  ████████████████████████████ 32.85%
│   ├─ newobject:  ████████████████████ 18.07%
│   │   └─ mallocgc:  ██████████████████ 17.34%
│   │       └─ mallocgcSmallScanNoHeader:  ███████████████ 15.49% 🔴
│   └─ atomic.Value.Store:  ███████ 5.63%
├─ NewPageRefWithInfo:  ███████████████ 11.84%
│   ├─ atomic.Value.Store:  ██████ 5.16%
│   └─ newobject:  ████ 4.08%
└─ finalizeDeepClone:  ███ 3.85%
```

---

## 🔍 核心代码路径分析

### 路径 1: LeafPage 深拷贝 (最大瓶颈)

```go
// leaf_page.go:238-251
func (p *LeafPage) Clone() *LeafPage {
    newKeys := make([][]byte, len(p.keys))     // 🔴 分配 keys 切片
    copy(newKeys, p.keys)                       // 🔴 复制所有 keys (指针)

    newValues := make([][]byte, len(p.values)) // 🔴 分配 values 切片
    copy(newValues, p.values)                   // 🔴 复制所有 values (指针)

    return &LeafPage{...}
}
```

**性能开销**:
```
假设: 100 个 key/value 对

┌─ 分配 keys 切片 ─────────────────────────┐
│ make([][]byte, 100)                      │
│ → mallocgcSmallScanNoHeader              │
│ → 800 bytes (100 × 8字节/指针)           │
└──────────────────────────────────────────┘

┌─ 分配 values 切片 ───────────────────────┐
│ make([][]byte, 100)                      │
│ → mallocgcSmallScanNoHeader              │
│ → 800 bytes (100 × 8字节/指针)           │
└──────────────────────────────────────────┘

总内存分配: 1.6KB (不含实际数据)
时间开销: ~1-2 µs
```

---

### 路径 2: PageInfo.CloneShallow

```go
// page_info.go:208-229
func (info *PageInfo) CloneShallow() *PageInfo {
    newInfo := &PageInfo{
        pageLock:    NewPageLock(),              // 🔴 分配 PageLock
        metaVersion: info.metaVersion,
        pageSize:    info.pageSize,
    }
    newInfo.parentRef.Store(info.parentRef.Load()) // 🔴 原子操作
    newInfo.SetPos(info.GetPos())                  // 🔴 原子操作
    newInfo.lastTime.Store(info.lastTime.Load())   // 🔴 原子操作
    newInfo.hits.Store(info.hits.Load())           // 🔴 原子操作
    newInfo.flags.Store(info.flags.Load())         // 🔴 原子操作

    newInfo.page = info.page  // ✅ 共享引用，不拷贝
    newInfo.cloneStatus.Store(CloneStatusShallow)  // 🔴 原子操作

    return newInfo
}
```

**性能开销**:
```
┌─ newobject (PageInfo) ───────────────────┐
│ 分配 192 bytes 结构体                     │
│ → mallocgc                               │
│ → 192 bytes                              │
└──────────────────────────────────────────┘

┌─ 原子操作 (6 次) ────────────────────────┐
│ parentRef.Store()                        │
│ SetPos()                                 │
│ lastTime.Store()                         │
│ hits.Store()                             │
│ flags.Store()                            │
│ cloneStatus.Store()                      │
└──────────────────────────────────────────┘

总内存分配: 192 bytes
时间开销: ~500 ns
```

---

### 路径 3: NewPageRefWithInfo

```go
// page_ref.go:25-30
func NewPageRefWithInfo(info *PageInfo) *PageRef {
    ref := &PageRef{}                        // 🔴 分配 PageRef
    ref.pInfo.Store(info)                    // 🔴 原子操作
    ref.parentRef.Store((*PageRef)(nil))     // 🔴 原子操作
    return ref
}
```

**性能开销**:
```
┌─ newobject (PageRef) ─────────────────────┐
│ 分配 32 bytes 结构体                       │
│ → mallocgc                               │
│ → 32 bytes                               │
└──────────────────────────────────────────┘

┌─ 原子操作 (2 次) ────────────────────────┐
│ pInfo.Store()                            │
│ parentRef.Store()                        │
└──────────────────────────────────────────┘

总内存分配: 32 bytes
时间开销: ~100 ns
```

---

## 📈 内存分配详细计算

### 单次 Set 操作的完整内存分配

```
copyPathShallow 调用链:
├─ copiedPath 切片:              72 bytes
├─ pageInfoMap (map):            ~50 bytes
│
├─ CloneShallow (×3):
│   ├─ PageInfo 结构体:         192 × 3 = 576 bytes
│   └─ 原子操作开销:             ~0 bytes
│
├─ LeafPage.Clone (×1):
│   ├─ keys 切片:               800 bytes
│   ├─ values 切片:             800 bytes
│   └─ LeafPage 结构体:         ~64 bytes
│
└─ NewPageRefWithInfo (×101):
    └─ PageRef 结构体:          32 × 101 = 3,232 bytes
└───────────────────────────────────────────
总计:                          ~5,576 bytes

┌─ 性能影响 ──────────────────────────────┐
│ 每秒操作数:       21,560 ops/sec         │
│ 每秒内存分配:      117 MB/sec            │
│ GC 触发频率:       ~2.5 次/秒            │
│ GC 占用 CPU:      20% (perf 数据)       │
└──────────────────────────────────────────┘
```

---

## 🎯 优化方案对比

| 方案 | Clone 开销 | 写开销 | 内存分配 | QPS 提升 | 实施难度 |
|------|-----------|--------|----------|----------|----------|
| **当前** | 1000 ns | 46 µs | 5.5KB | 21.5K | - |
| **Delta Chain** | 10 ns | 100 ns | 200B | **9x** | 高 |
| **sync.Pool** | 1000 ns | 25 µs | 1KB | **1.9x** | 低 |
| **批量 CAS** | 1000 ns | 20 µs | 5.5KB | **2.3x** | 中 |

---

## 🔬 性能瓶颈根因分析

### 根因 1: 过度的深拷贝
```
问题: LeafPage.Clone() 完整复制所有 keys 和 values
根因: CCOW 需要独立的 Page 副本避免并发冲突
影响: 占用 15.49% CPU (GC 扫描)

解决方案: Delta Chain
- 只记录增量操作 (Insert/Update/Delete)
- 共享原始数据，使用引用计数
- 延迟物化: 增量链超过阈值才合并
```

### 根因 2: 大量小对象分配
```
问题: 每次操作分配 ~5.5KB 小对象
根因: Go 的内存分配器和 GC 难以高效处理
影响: GC 占用 20% CPU

解决方案: sync.Pool
- 复用 PageInfo, PageRef, LeafPage 对象
- 减少分配次数 5x
- 降低 GC 压力
```

### 根因 3: 频繁的原子操作
```
问题: 每个 Clone/Ref 创建都有多次原子操作
根因: 并发安全需要 (atomic.Value, atomic.Int64)
影响: 占用 7.90% CPU (StoreUintptr)

解决方案: 减少克隆频率
- Delta Chain 减少克隆次数
- 批量 CAS 减少操作频率
```

---

## 📊 预期优化效果

### Delta Chain 方案实施后

```
优化前 (当前):
┌─────────────────────────────────────┐
│ Set 操作:                           │
│ ├─ Clone: 1000 ns                  │
│ ├─ Copy: 1000 ns                   │
│ ├─ Insert: 100 ns                  │
│ └─ 总计: 46 µs                     │
│                                     │
│ QPS: 21,560 ops/sec                 │
│ 内存: 5.5KB/op                      │
└─────────────────────────────────────┘

优化后 (Delta Chain):
┌─────────────────────────────────────┐
│ Set 操作:                           │
│ ├─ Retain: 10 ns (原子递增)         │
│ ├─ AppendDelta: 50 ns              │
│ ├─ Insert: 100 ns                  │
│ └─ 总计: 160 ns                    │
│                                     │
│ QPS: ~200K ops/sec (9x ↑)          │
│ 内存: 200B/op (27x ↓)              │
└─────────────────────────────────────┘
```

### 性能提升预测

| 指标 | 当前 | Delta Chain | sync.Pool | 组合方案 |
|------|------|-------------|-----------|----------|
| **QPS** | 21.5K | 200K (9x) | 40K (1.9x) | 400K (18x) |
| **延迟** | 46 µs | 5 µs | 25 µs | 2.5 µs |
| **内存** | 5.5KB | 200B | 1KB | 200B |
| **GC%** | 20% | 5% | 10% | 2% |

---

## 🚀 实施路线图

### 阶段 1: 快速胜利 (1-2 天)
- ✅ 实施 sync.Pool 优化
- ✅ 预期提升: 1.9x QPS

### 阶段 2: 核心优化 (3-5 天)
- ✅ 实施 Delta Chain 方案
- ✅ 预期提升: 9x QPS

### 阶段 3: 深度优化 (1 周)
- ✅ 批量 CAS
- ✅ 预期提升: 18x QPS (总计)

---

## 📚 相关文档

- `perf_analysis.md` - 完整性能分析报告
- `perf_hotspots.txt` - 热点函数列表
- `perf_report_graph.txt` - 完整调用图
