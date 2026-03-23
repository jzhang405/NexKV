# BTree 并发优化 - 性能瓶颈分析

**测试日期**: 2026-03-23
**测试场景**: 随机前缀 key（分散在不同页面）
**测试配置**: 8 核心，50000 操作/线程

---

## 性能对比

### 默认 GOGC (100)

| 模式 | 吞吐量 | 延迟(μs) | 对比 |
|------|---------|----------|------|
| Direct | 1.06M ops/sec | 0.94 | - |
| Scheduler | 1.11M ops/sec | 0.90 | **+4.7%** ✅ |

### GOGC=500 (降低 GC 频率)

| 模式 | 吞吐量 | 延迟(μs) | 对比 | vs GOGC=100 |
|------|---------|----------|------|-----------|
| Direct | 1.55M ops/sec | 0.65 | - | **+46%** |
| Scheduler | 1.63M ops/sec | 0.61 | - | **+47%** |

**GOGC=500 下 Scheduler 优势**: **+5.0%** (1.63M vs 1.55M)

**关键发现**: 降低 GC 频率带来了 **47% 性能提升**！

---

## 瓶颈分析 (GOGC=500)

### GC 压力大幅降低

**GOGC=500 热点对比**：

| 函数 | GOGC=100 | GOGC=500 | 变化 |
|------|----------|----------|------|
| runtime.tryDeferToSpanScan | 36.15% | **17.37%** | -52% ✅ |
| runtime.wbBufFlush1 | 16.58% | **8.41%** | -49% ✅ |
| GC 总计 | ~70% | **~35%** | -50% ✅ |

GC 压力降低后，**实际业务逻辑占比增加**：
- `LeafPage.materialize` 相对占比提升
- `insertSlice` 相对占比提升

---

## 瓶颈分析 (GOGC=100 - 默认)

### 1. GC 压力是主要瓶颈

**Scheduler 模式热点** (GOGC=100):
- `runtime.tryDeferToSpanScan`: **36.15%**
- `runtime.gcDrain` / `runtime.gcDrainN`: 17.97%
- `runtime.wbBufFlush1`: 16.58%
- **总计 GC 相关**: ~70%

**Direct 模式热点** (GOGC=100):
- `runtime.tryDeferToSpanScan`: **30.27%**
- `runtime.gcDrain` / `runtime.gcDrainN`: 15.95%
- `runtime.wbBufFlush1`: 12.05%
- **总计 GC 相关**: ~58%

**结论**: GC 是主要瓶颈，Scheduler 模式由于队列缓冲等原因，GC 压力略高。

### 2. 内存分配热点

**LeafPage.materialize** (materialize Delta Chain):
- Scheduler: ~10% (包含 wbBufFlush 中的 9.47%)
- Direct: ~7.3%

这是 BTree 写操作的核心路径：
1. Delta Chain 累积
2. materialize 创建新的页面快照
3. insertSlice 添加 key/value

### 3. 页面分裂开销

**LeafPage.Split**:
- Direct: ~0.58% (偶尔触发)
- Scheduler: 相对较少（队列缓冲减少了同时分裂的概率）

---

## 优化建议

### P0: 减少 GC 压力 ✅ (已验证有效)

1. **使用 GOGC=500**: **+47% 性能提升** ✅
   - 生产环境可设置为 `GOGC=500` 或更高
   - 注意：会增加内存使用量

2. **对象池复用** (未实施):
   - 使用 `sync.Pool` 复用 PageInfo、LeafPage 对象
   - 预估收益：在 GOGC=500 基础上再提升 20-30%
   - 目标：达到 2.0M+ ops/sec

3. **减少 Delta Chain materialize** (未实施):
   - 增加 Delta Chain 容量（当前可能频繁触发 materialize）
   - 延迟 materialize 到必要时
   - 预估收益：减少 5-10% CPU 时间

### P1: 优化内存分配

1. **避免小块内存分配**：
   - `insertSlice` 中的 makeslice 是热点
   - 使用预分配 buffer

2. **减少写屏障开销**：
   - `bulkBarrierPreWrite` 占用 5-9%
   - 考虑使用 `unsafe.Pointer` 绕过写屏障（谨慎）

### P2: 并发优化

1. **批量处理调优**：
   - 当前 batchSize 8 在随机 key 下最优
   - 可根据队列长度动态调整

2. **锁竞争优化**：
   - 减少持有锁的时间
   - 使用更细粒度的锁

---

## 性能目标评估

**GOGC=500 性能**: 1.63M ops/sec @ 8核
**目标**: 3.1-3.2M ops/sec @ 8核
**剩余差距**: 约 2x

**路径分析** (基于 GOGC=500):
- 对象池复用（P0）: +20-30% → 2.0-2.1M
- 减少内存分配（P1）: +15-20% → 2.3-2.5M
- 并发优化（P2）: +10-15% → 2.5-2.9M
- **需要更深层次的架构优化** 才能达到 3.1-3.2M

**建议**:
1. 短期：使用 **GOGC=500** + 对象池复用 → 目标 2.0M ops/sec
2. 长期：重新评估目标合理性，或考虑更激进的优化（如 Leaf-Level CAS）

---

## 测试文件

- `perf_report_scheduler.txt`: Scheduler 模式 CPU 火焰图 (GOGC=100)
- `perf_report_direct.txt`: Direct 模式 CPU 火焰图 (GOGC=100)
- `perf_report_goc500_scheduler.txt`: Scheduler 模式 CPU 火焰图 (GOGC=500)
