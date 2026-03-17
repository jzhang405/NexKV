# BTree 性能基线测试摘要

**日期**: 2026-03-17
**配置**: GOGC=400, maxKeys=200, 内存模式
**数据量**: 1M keys

---

## 快速结果

```
Throughput:  21,560 ops/sec
Latency:    46.38 μs/op
Duration:   4.64 seconds (100K ops)
```

---

## Top 5 性能瓶颈

| # | 开销 | 函数 | 类型 |
|---|------|------|------|
| 1 | 11.88% | `runtime.mallocgcSmallScanNoHeader` | GC |
| 2 | 8.80% | `(*PageInfo).CloneShallow` | CCOW 拷贝 |
| 3 | 7.90% | `sync/atomic.StoreUintptr` | 原子操作 |
| 4 | 7.67% | `runtime.tryDeferToSpanScan` | Defer 开销 |
| 5 | 4.74% | `runtime.(*mspan).writeHeapBitsSmall` | GC |

---

## 关键洞察

### 🔴 主要瓶颈
1. **GC 占用 20% CPU** - 大量小对象分配
2. **Clone 占用 13% CPU** - CCOW 深拷贝开销
3. **Defer 占用 7.7% CPU** - 栈帧扫描

### 📊 优化潜力
| 优化项 | 当前开销 | 目标开销 | 预期提升 |
|--------|----------|----------|----------|
| Delta Chain | 13% | 2% | **2-3x QPS** |
| sync.Pool | 20% | 10% | 1.5x QPS |
| 减少 Defer | 7.7% | 2% | 1.1x QPS |

**综合预期**: 21.5K → **50-60K ops/sec** (2.3-2.8x)

---

## 下一步

1. ✅ **Delta Chain 优化** (最高优先级)
   - 创建 `cow_delta_ref.go`
   - 零拷贝 Clone + 增量链

2. ✅ **sync.Pool 优化**
   - PageInfo 对象池
   - LeafPage/InternalPage 对象池

---

## 完整数据

详见:
- `perf_analysis.md` - 完整分析报告
- `perf_hotspots.txt` - 热点函数列表
- `perf_report_graph.txt` - 调用图
