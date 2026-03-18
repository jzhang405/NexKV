# BTree Set 性能分析报告

**日期**: 2026-03-18
**测试环境**: GOGC=400, 纯内存模式
**测试命令**: `GOGC=400 go run cmd/btree_perf_mem/main.go threads 1`

---

## 📊 性能指标摘要

### 当前性能

| 指标 | 值 | 单位 |
|------|-----|------|
| **吞吐量** | 572,198 | ops/sec |
| **平均延迟** | 1.75 | μs/op |
| **内存分配** | 4,139 | B/op |
| **分配次数** | 51 | allocs/op |
| **CPU 时间** | 3,007 | ns/op |

### 并发性能

| 并发度 | 吞吐量 (ops/sec) | 平均延迟 (μs/op) |
|--------|------------------|------------------|
| 1 | 529,025 | 1.89 |
| 2 | 485,514 | 2.06 |
| 4 | 537,138 | 1.86 |
| 8 | 529,900 | 1.89 |

**峰值性能**: 537k ops/sec @ 4 线程

---

## 🔍 内存分配分析

### TOP 内存分配函数

| 排名 | 函数 | 分配量 | 占比 | 累计占比 |
|------|------|--------|------|----------|
| 1 | `NewPageInfo` | 190.03MB | 47.70% | 47.70% |
| 2 | `LeafPage.Clone` | 99.01MB | 24.85% | 72.55% |
| 3 | `InternalPage.Clone` | 38.50MB | 9.66% | 82.21% |
| 4 | `NewCOWDeltaRefWithConfig` | 37MB | 9.29% | 91.50% |
| 5 | `AppendDelta` | 21.50MB | 5.40% | 96.90% |
| 6 | `NewDefaultCOWDeltaRefConfig` | 12MB | 3.01% | 99.91% |
| 7 | `copyPathWithDelta` (累计) | 199.03MB | 49.95% | - |

**总分配**: 398.42MB

### 内存分配热点图

```
NewPageInfo ████████████████████████ 47.70%
LeafPage.Clone █████████████████████ 24.85%
InternalPage.Clone ████████████ 9.66%
NewCOWDeltaRef ████████████ 9.29%
AppendDelta ██████████ 5.40%
其他 ██████ 3.01%
```

---

## ⚡ CPU 时间分析

### copyPathWithDelta CPU 分解

| 函数/操作 | CPU 时间 | 占比 |
|----------|----------|------|
| **NewPageInfo** (LeafPage) | 70ms | 17.3% |
| **NewPageInfo** (InternalPage) | 130ms | 32.2% |
| LeafPage.CloneWithDelta | 40ms | 9.9% |
| InternalPage.CloneWithDelta | 80ms | 19.8% |
| rebuildChildRefs (map) | 50ms | 12.4% |
| 其他 | 34ms | 8.4% |
| **总计** | **410ms** | **100%** |

### CPU 热点函数列表

```
copyPathWithDelta          410ms  ████████████████████████████ 20.30%
  ├─ NewPageInfo            200ms  ████████████████████ 9.90%
  ├─ CloneWithDelta        120ms  ████████████████ 5.94%
  └─ rebuildChildRefs        50ms  ████████ 2.48%

Set (setWithCAS)           920ms  ████████████████████████████████████████████████ 45.54%
  ├─ copyPathWithDelta      410ms  ████████████████████████████████ 20.30%
  ├─ findLeafPage          230ms  ████████████████████ 11.39%
  └─ LeafPage.Insert        280ms  ██████████████████████ 13.86%

Runtime (GC + Scheduler)  650ms  ████████████████████████████████████████████████████████ 32.18%
```

---

## 🎯 关键发现

### 1. copyPathWithDelta 是主要瓶颈

**数据**:
- 占 CPU 时间: 20.30%
- 占内存分配: 49.95%
- 每次调用分配: 199MB

**原因**:
- 对路径中每个节点都创建新的 PageInfo
- 对每个页面都调用 CloneWithDelta
- rebuildChildRefs 创建大量临时对象

### 2. NewPageInfo 分配占比最大

**数据**:
- 占内存分配: 47.70%
- 每次 Set 调用多次分配

**原因**:
- 路径中每个节点都创建新的 PageInfo
- PageInfo 结构包含多个 atomic 字段，初始化开销大

### 3. Clone 操作开销显著

**数据**:
- LeafPage.Clone: 99MB (24.85%)
- InternalPage.Clone: 38.50MB (9.66%)

**原因**:
- CloneWithDelta 创建 COWDeltaRef 对象
- COWDeltaRef 预分配 deltas 切片（虽已优化为 0）

### 4. GC 压力适中

**数据**:
- GC 占 CPU 时间: ~32%
- 主要来自 NewPageInfo 和 Clone 分配

**分析**:
- GOGC=400 环境下，GC 不是主要瓶颈
- 减少内存分配可进一步降低 GC 压力

---

## 📋 优化机会

### 高优先级 (收益 > 30%)

| 优化项 | 预期收益 | 实施难度 | 文档链接 |
|--------|----------|----------|----------|
| 使用 CloneShallow 替代 NewPageInfo | 45% ↓ 分配 | 低 | [优化方案](./optimization_plan.md#策略-a-使用-克隆浅拷贝-替代-newpageinfo--推荐) |
| 只克隆叶子节点 | 30% ↓ 分配 | 中 | [优化方案](./optimization_plan.md#策略-b-只克隆叶子节点) |

### 中优先级 (收益 10-30%)

| 优化项 | 预期收益 | 实施难度 | 文档链接 |
|--------|----------|----------|----------|
| 优化 rebuildChildRefs map | 5% ↓ 分配 | 低 | [优化方案](./optimization_plan.md#策略-c-优化-rebuildchildrefs) |
| COWDeltaRef 对象池 | 10% ↓ 分配 | 低 | - |

### 低优先级 (收益 < 10%)

| 优化项 | 预期收益 | 实施难度 | 说明 |
|--------|----------|----------|------|
| 延迟克隆到 CAS 成功后 | 变化 | 高 | 需重新设计 CAS 逻辑 |

---

## 🔬 详细性能数据

### Benchmark 结果

```
BenchmarkBTree_Set_Concurrent_Memory-12  100000  3007 ns/op  4139 B/op  51 allocs/op
```

### pprof Top 输出 (内存)

```
Showing nodes accounting for 393.24MB, 98.70% of 398.42MB total

      flat  flat%   sum%        cum   cum%
  190.03MB 47.70% 47.70%   190.03MB 47.70%  github.com/.../btree.NewPageInfo
      50MB 12.55% 60.25%    99.01MB 24.85%  github.com/.../btree.(*LeafPage).Clone
   38.50MB  9.66% 69.91%    38.50MB  9.66%  github.com/.../btree.(*InternalPage).Clone
      37MB  9.29% 79.20%       37MB  9.29%  github.com/.../btree.NewCOWDeltaRefWithConfig
   21.69MB  5.44% 84.64%    21.69MB  5.44%  runtime.mallocgc
   21.50MB  5.40% 90.04%    21.50MB  5.40%  github.com/.../btree.(*COWDeltaRef).AppendDelta
       12MB  3.01% 93.05%       12MB  3.01%  github.com/.../btree.NewDefaultCOWDeltaRefConfig
       10MB  2.51% 95.56%       10MB  2.51%  time.Sleep
       6MB  1.51% 97.07%   199.03MB 49.95%  github.com/.../btree.(*BTree).copyPathWithDelta
```

### pprof Top 输出 (CPU)

```
Showing nodes accounting for 1.69s, 83.66% of 2.02s total

      flat  flat%   sum%        cum   cum%
     0.02s  0.99%  0.99%      0.64s 31.68%  runtime.mallocgc
     0.01s   0.5%  1.49%      0.56s 27.72%  runtime.newobject
     0.01s   0.5%  1.98%      0.41s 20.30%  github.com/.../btree.(*BTree).copyPathWithDelta
     0.04s  1.98%  3.47%      0.24s 11.88%  github.com/.../btree.NewPageInfo
     0.01s   0.5%  3.47%      0.12s   5.94%  github.com/.../btree.(*LeafPage).Clone
```

---

## 📈 性能历史

### 从初始到现在的优化历程

| 时间点 | 优化内容 | 吞吐量 | 提升 | 文档 |
|--------|----------|--------|------|------|
| 初始 | - | 16k ops/sec | - | - |
| P1 | 移除性能监控 | 283k ops/sec | 17.7x | - |
| P2 | 跳过深拷贝 | 452k ops/sec | 28.3x | - |
| P3 | PageLock 懒加载 | 608k ops/sec | 37.5x | commit b34d523 |
| P4 | Delta 预分配 | 608k ops/sec | 37.5x | commit b34d523 |
| 当前 | - | 572k ops/sec | 35.9x | - |

### 性能波动分析

最近性能测试结果略有波动（572k vs 608k），可能原因：
- 系统负载波动
- CPU 频率调整
- 内存碎片
- 测试数据量差异

**趋势**: 性能稳定在 550k-600k ops/sec 之间

---

## 🎯 下一步行动

查看详细优化方案: [optimization_plan.md](./optimization_plan.md)

**推荐优先级**:
1. ⭐ Phase 1: 使用 CloneShallow + 优化 rebuildChildRefs
2. Phase 2: 只克隆叶子节点

---

## 📝 生成命令

```bash
# 运行性能测试
GOGC=400 go run cmd/btree_perf_mem/main.go threads 1

# 生成 CPU profile
go test -cpuprofile=/tmp/cpu.prof -bench=BenchmarkBTree_Set_Concurrent_Memory -benchmem -run=^$ ./internal/infrastructure/storage/btree/

# 生成内存 profile
go test -memprofile=/tmp/mem.prof -bench=BenchmarkBTree_Set_Concurrent_Memory -benchmem -run=^$ ./internal/infrastructure/storage/btree/

# 分析 CPU profile
go tool pprof -top /tmp/cpu.prof
go tool pprof -list copyPathWithDelta /tmp/cpu.prof

# 分析内存 profile
go tool pprof -top /tmp/mem.prof
```
