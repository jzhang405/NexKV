# BTree 内存模式性能分析报告

**测试日期**: 2026-03-17
**测试工具**: perf (Linux perf_events)
**配置**: GOGC=400, maxKeys=200, 1M keys

---

## 测试结果摘要

| 指标 | 数值 |
|------|------|
| **初始化数据量** | 1,000,000 keys |
| **测试操作数** | 100,000 Set |
| **总耗时** | 4.64 秒 |
| **吞吐量** | 21,560 ops/sec |
| **平均延迟** | 46.38 μs/op |

---

## 性能瓶颈分析 (Top 10 热点函数)

| 排名 | 开销 | 函数 | 说明 |
|------|------|------|------|
| 1 | 11.88% | `runtime.mallocgcSmallScanNoHeader` | **GC 小对象扫描** - 内存分配瓶颈 |
| 2 | 8.80% | `(*PageInfo).CloneShallow` | **浅拷贝** - CCOW 核心开销 |
| 3 | 7.90% | `sync/atomic.StoreUintptr` | 原子操作 |
| 4 | 7.67% | `runtime.tryDeferToSpanScan` | **defer 开销** - 扫描栈帧 |
| 5 | 4.74% | `runtime.(*mspan).writeHeapBitsSmall` | 堆位图写入 |
| 6 | 4.19% | `runtime.typePointers.next` | 类型指针遍历 |
| 7 | 3.38% | `runtime.scanObject` | 对象扫描 |
| 8 | 3.29% | `sync/atomic.CompareAndSwapUintptr` | CAS 原子操作 |
| 9 | 3.16% | `runtime.mallocgc` | **内存分配** |
| 10 | 2.73% | `(*BTree).copyPathShallow` | **路径拷贝** |

---

## 关键发现

### 1. GC 开销巨大 (11.88% + 4.74% + 3.38% = ~20%)

```
11.88%  runtime.mallocgcSmallScanNoHeader
 4.74%  runtime.(*mspan).writeHeapBitsSmall
 3.38%  runtime.scanObject
 3.16%  runtime.mallocgc
```

**分析**:
- 每次写操作触发大量小对象分配
- CCOW 深拷贝导致堆内存频繁增长
- GC 扫描占用大量 CPU 时间

### 2. CCOW 拷贝开销高 (8.80% + 2.73% + 1.66% = ~13%)

```
8.80%  (*PageInfo).CloneShallow
2.73%  (*BTree).copyPathShallow
1.66%  (*BTree).finalizeDeepClone
```

**分析**:
- 每次写操作都需要克隆整条路径
- 树高较高时路径拷贝开销显著
- 延迟深拷贝优化后仍有瓶颈

### 3. Defer 开销显著 (7.67%)

```
7.67%  runtime.tryDeferToSpanScan
```

**分析**:
- defer 在关键路径上
- 每次 defer 都需要扫描栈帧
- 已通过 atomic.Value 优化部分 defer，但仍存在

### 4. 原子操作频繁 (7.90% + 3.29% + 2.40% = ~13.6%)

```
7.90%  sync/atomic.StoreUintptr
3.29%  sync/atomic.CompareAndSwapUintptr
2.40%  sync/atomic.(*Value).Store
```

**分析**:
- 并发控制需要大量原子操作
- atomic.Value 虽然无锁，但仍有开销

---

## 优化建议

### 🔴 高优先级 (立即执行)

#### 1. 减少内存分配
- **目标**: 降低 GC 开销从 20% → 10%
- **方案**:
  - 使用 sync.Pool 复用 PageInfo 和 Page 对象
  - 预分配路径数组，避免 append
  - 使用 bytes.Buffer 减少字符串拼接分配

#### 2. 优化 Clone 操作
- **目标**: 降低拷贝开销从 13% → 6%
- **方案**:
  - **Delta Chain 方案**: 只记录增量变化，延迟物化
  - **引用计数 + 共享只读数据**: 避免完整深拷贝
  - **预期提升**: 2-3x QPS (21K → 50-60K)

### 🟡 中优先级 (近期优化)

#### 3. 减少 Defer 使用
- **目标**: 降低 defer 开销从 7.67% → 2%
- **方案**:
  - 在关键路径上移除 defer
  - 使用显式清理函数

#### 4. 优化原子操作
- **目标**: 减少原子操作次数
- **方案**:
  - 批量更新，减少 CAS 尝试次数
  - 使用专用数据结构（如 lock-free queue）

### 🟢 低优先级 (长期优化)

#### 5. 降低树高度
- **目标**: 减少路径拷贝开销
- **方案**:
  - 增大 maxKeys (已从 16 → 200)
  - 考虑更大的页面 (8KB → 16KB)

---

## 性能对比

| 场景 | QPS | 延迟 | 主要瓶颈 |
|------|-----|------|----------|
| **当前 (GOGC=400)** | 21.5K | 46.4 µs | GC + Clone |
| **优化前 (GOGC=100)** | ~7K | ~140 µs | GC + Split |
| **目标 (Delta Chain)** | 50-60K | ~17 µs | Clone |

---

## 下一步行动

### 立即开始
1. **实施 Delta Chain 优化**
   - 创建 `cow_delta_ref.go`
   - 修改 LeafPage 支持增量链
   - 修改 InternalPage 支持增量链

2. **添加 sync.Pool**
   - 为 PageInfo 创建对象池
   - 为 LeafPage/InternalPage 创建对象池

### 验证指标
- **Clone 开销**: < 20 ns (vs 当前 1000 ns)
- **写开销**: < 100 ns/次 (增量模式)
- **QPS**: > 50K (2.3x 提升)

---

## 附录

### 测试环境
- **CPU**: Intel Core i7-8700 @ 3.20GHz
- **Go版本**: 1.24
- **OS**: Linux 6.17.0-14-generic
- **perf 采集**: 9939 samples, 79.7 MB data

### 相关文件
- `cmd/btree_perf_mem/main.go` - 性能测试程序
- `perf_hotspots.txt` - 完整热点函数列表
- `perf_report_graph.txt` - 完整调用图
