# 2026-03-01 - Executor 性能对比 - AntsPool vs PerCore

> **测试日期**: 2026-03-01
> **测试类型**: Executor 性能对比
> **测试状态**: ✅ 已完成
> **关联PR**: N/A

---

## 📋 测试概述

### 测试目标

对比 AntsPoolExecutor 和 PerCoreExecutor 的性能差异。

### 关键问题

1. PerCoreExecutor 相比 AntsPoolExecutor 性能提升多少？
2. CPU 亲和性对性能的影响？
3. 上下文切换的开销？

---

## 🧪 测试配置

### 测试场景

1. Transport 场景（任务提交）
2. RPC 客户端场景
3. CPU 密集场景

### 测试环境

- CPU: 8 cores / 16 threads
- Memory: 16GB
- OS: Linux 5.15
- Go: 1.21+

---

## 📊 测试结果

### 性能对比

| Executor | 场景 | P50 延迟 | 吞吐量 | 提升 |
|----------|------|---------|--------|------|
| AntsPool | Transport | 457.9 ns | 7.9M ops/sec | 基线 |
| PerCore | Transport | 183.4 ns | 18.8M ops/sec | **+138%** |
| AntsPool | RPC Client | 660.2 ns | 5.4M ops/sec | 基线 |
| PerCore | RPC Client | 118.7 ns | 31.9M ops/sec | **+487%** |

### 资源使用

| Executor | CPU 让步 | 上下文切换 | 缓存命中率 |
|----------|---------|-----------|-----------|
| AntsPool | 36.81% | 1500/sec | 75% |
| PerCore | 4.59% | 180/sec | 85% |

---

## 📂 测试文件

### 报告和资产

- [完整报告](assets/2026-03-01-perf/executor_comparison_report.md)
- [PerCore CPU Profile](assets/2026-03-01-perf/percore_cpu.prof)
- [AntsPool CPU Profile](assets/2026-03-01-perf/ants_cpu.prof)
- [PerCore 火焰图](assets/2026-03-01-perf/percore_executor.svg)
- [AntsPool 火焰图](assets/2026-03-01-perf/ants_executor.svg)
- [PerCore perf 数据](assets/2026-03-01-perf/percore_raw.txt)
- [AntsPool perf 数据](assets/2026-03-01-perf/ants_raw.txt)

---

## 🎯 结论

### 关键发现

1. **PerCoreExecutor 性能显著优于 AntsPoolExecutor**
   - Transport 场景：+138%
   - RPC Client 场景：+487%

2. **CPU 亲和性带来显著收益**
   - 上下文切换减少 88%
   - CPU 让步时间减少 87.5%

3. **资源使用更优**
   - 缓存命中率提升 13%
   - 内存占用更稳定

### 建议

**推荐配置**: PerCoreExecutor

**适用场景**:
- ✅ 延迟敏感的 RPC 调用
- ✅ CPU 密集型任务
- ✅ 需要稳定性能的场景

**不适用场景**:
- ❌ I/O 密集型任务（AntsPool 更优）
- ❌ 任务量波动大的场景（AntsPool 自动扩缩容）

---

**创建日期**: 2026-03-01
**最后更新**: 2026-03-05
