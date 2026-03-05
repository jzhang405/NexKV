# 2026-03-05 - RPC 性能测试 - Executor 和 SourceID 策略对比

> **测试日期**: 2026-03-05
> **测试类型**: RPC 性能测试
> **测试状态**: 🟡 进行中
> **关联PR**: #91

---

## 📋 测试概述

### 测试目标

对比不同 Executor 和 SourceID 策略的性能，选出最优配置。

### 关键问题

1. **Executor 选择**: AntsPoolExecutor vs PerCoreExecutor
2. **SourceID 策略**: Network vs Shard vs Client vs Node
3. **性能指标**: 延迟、吞吐量、CPU 使用率、内存占用
4. **决策依据**: 综合评分，选出最优配置

---

## 🧪 测试矩阵

### 配置组合（8 种）

| ID | Executor | SourceID 策略 | 说明 |
|----|----------|--------------|------|
| C1 | AntsPool | Network | Baseline |
| C2 | PerCore | Network | 无亲和性 |
| C3 | PerCore | Shard | 分片亲和 ⭐ |
| C4 | PerCore | Client | 客户端亲和 |
| C5 | PerCore | Node | 节点亲和 |
| C6 | AntsPool | Shard | 对照组 |
| C7 | PerCore | Mixed | 混合策略 ⭐ |
| C8 | Hybrid | Dynamic | 实验性 |

### 测试场景（6 个）

| ID | 场景 | 描述 |
|----|------|------|
| S1 | 点对点 RPC | 1 Client → 1 Server |
| S2 | 广播发送 | 1 → N (3/10/50/100) |
| S3 | 并发压力 | 多 Client (10/100/1000) |
| S4 | 异步回调 | AsyncOp 回调性能 |
| S5 | CPU 亲和性 | 相同 SourceID 路由 |
| S6 | 资源使用 | 内存和 CPU |

---

## 📊 测试进度

### Phase 1: Baseline 测试（1 天）

**状态**: ⏳ 待开始

**目标**: 建立当前性能基线

**配置**: C1 (AntsPool + SourceNetwork)

**测试场景**:
- [ ] S1: 点对点 RPC
- [ ] S2: 广播发送
- [ ] S3: 并发压力
- [ ] S4: 异步回调

**结果文件**:
- [ ] [phase1-baseline.md](2026-03-05-rpc-performance/phase1-baseline.md)
- [ ] [assets/raw/](2026-03-05-rpc-performance/assets/raw/)

### Phase 2: Design 文档测试（2 天）

**状态**: ⏳ 待开始

**目标**: 测试设计文档中的推荐配置

**配置**: C2-C5 (PerCore + 不同 SourceID)

**结果文件**:
- [ ] [phase2-design.md](2026-03-05-rpc-performance/phase2-design.md)

### Phase 3: 配置对比测试（3 天）

**状态**: ⏳ 待开始

**目标**: 全面对比所有配置组合

**配置**: C1-C8 (所有 8 种配置)

**测试矩阵**: 8 配置 × 6 场景 = 48 个测试

**结果文件**:
- [ ] [phase3-comparison.md](2026-03-05-rpc-performance/phase3-comparison.md)

### Phase 4: 最优配置选择（1 天）

**状态**: ⏳ 待开始

**目标**: 选出最优配置并验证

**决策规则**: 综合评分 ≥ 80 分

**结果文件**:
- [ ] [phase4-conclusion.md](2026-03-05-rpc-performance/phase4-conclusion.md)

---

## 📈 预期性能

### 点对点 RPC 性能

| 配置 | P50 延迟 | 吞吐量 | 提升 |
|------|---------|--------|------|
| C1: Baseline | 457.9 ns | 7.9M ops/sec | 基线 |
| C3: PerCore+Shard | **150.2 ns** | **22.5M ops/sec** | **+185%** |

### 资源使用对比

| 配置 | CPU 让步 | 上下文切换 | 缓存命中率 |
|------|---------|-----------|-----------|
| C1: Baseline | 36.81% | 1500/sec | 75% |
| C3: PerCore+Shard | **3.20%** | **150/sec** | **92%** |

---

## 📂 测试文件

### 测试计划
- [测试计划详细文档](../09_code-review/2026-03-05_rpc-perf-benchmark-plan.md)
- [RPC Executor SourceID 设计](../09_code-review/2026-03-05_rpc-executor-sourceid-design.md)

### 测试目录
- [2026-03-05-rpc-performance/](2026-03-05-rpc-performance/)
  - [README.md](2026-03-05-rpc-performance/README.md) - 测试概述
  - [assets/raw/](2026-03-05-rpc-performance/assets/raw/) - 原始数据
  - [assets/processed/](2026-03-05-rpc-performance/assets/processed/) - 处理数据
  - [assets/graphs/](2026-03-05-rpc-performance/assets/graphs/) - 图表

---

## 🔗 相关资源

### 测试命令

```bash
# Phase 1: Baseline
make benchmark-rpc > docs/10_benchmark/2026-03-05-rpc-performance/assets/raw/C1_baseline_$(date +%Y%m%d_%H%M%S).txt

# Phase 2: Design
make benchmark-rpc-full

# Phase 3: Comparison
./scripts/compare_rpc_perf.sh
```

### 相关文档
- [RPC V4 改造提案](../09_code-review/2026-03-05_proposal-transport-rpc-v4-refactor.md)
- [SourceID 设计和分配](../09_code-review/2026-03-05_sourceid-design-and-allocation.md)
- [RPC Goroutine 使用分析](../09_code-review/2026-03-05_rpc-goroutine-usage-analysis.md)

---

## 📝 备注

- 测试环境：8C16G，Go 1.21+
- 预期完成时间：7 天
- 负责人：jzh

---

**创建日期**: 2026-03-05
**最后更新**: 2026-03-05
