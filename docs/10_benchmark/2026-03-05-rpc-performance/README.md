# RPC 性能测试 - Executor 和 SourceID 策略对比

> **测试日期**: 2026-03-05
> **测试人**: jzh
> **测试版本**: feature/transport-rpc-v4-refactor-phase0
> **测试环境**: 8C16G, Go 1.21+

---

## 1. 测试概述

### 1.1 测试目标

对比不同 Executor 和 SourceID 策略的性能，选出最优配置。

### 1.2 测试范围

- **测试对象**: RPC 层（Transport + RPCAsync）
- **测试场景**: 点对点、广播、并发、回调、亲和性、资源
- **排除项**: 网络延迟、磁盘 I/O

### 1.3 关键问题

1. **Executor 选择**: AntsPoolExecutor vs PerCoreExecutor
2. **SourceID 策略**: Network vs Shard vs Client vs Node
3. **性能指标**: 延迟、吞吐量、CPU 使用率、内存占用
4. **决策依据**: 综合评分，选出最优配置

---

## 2. 测试配置

### 2.1 硬件环境

```yaml
CPU: 8 cores / 16 threads
Memory: 16GB
Network: localhost (排除网络变量)
OS: Linux 5.15
Go: 1.21+
```

### 2.2 测试矩阵

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

---

## 3. 测试进度

### Phase 1: Baseline 测试

**状态**: ⏳ 待开始

**目标**: 建立当前性能基线

**配置**: C1 (AntsPool + SourceNetwork)

**结果**: [phase1-baseline.md](phase1-baseline.md)

### Phase 2: Design 文档测试

**状态**: ⏳ 待开始

**目标**: 测试设计文档中的推荐配置

**配置**: C2-C5 (PerCore + 不同 SourceID)

**结果**: [phase2-design.md](phase2-design.md)

### Phase 3: 配置对比测试

**状态**: ⏳ 待开始

**目标**: 全面对比所有配置组合

**配置**: C1-C8 (所有 8 种配置)

**结果**: [phase3-comparison.md](phase3-comparison.md)

### Phase 4: 最优配置选择

**状态**: ⏳ 待开始

**目标**: 选出最优配置并验证

**结果**: [phase4-conclusion.md](phase4-conclusion.md)

---

## 4. 测试文件

### 测试计划

- **详细计划**: [../../../09_code-review/2026-03-05_rpc-perf-benchmark-plan.md](../../../09_code-review/2026-03-05_rpc-perf-benchmark-plan.md)
- **设计文档**: [../../../09_code-review/2026-03-05_rpc-executor-sourceid-design.md](../../../09_code-review/2026-03-05_rpc-executor-sourceid-design.md)

### 测试结果

- [phase1-baseline.md](phase1-baseline.md) - Baseline 测试结果
- [phase2-design.md](phase2-design.md) - Design 文档测试结果
- [phase3-comparison.md](phase3-comparison.md) - 配置对比结果
- [phase4-conclusion.md](phase4-conclusion.md) - 最终结论

### 数据文件

- [assets/raw/](assets/raw/) - 原始测试数据
- [assets/processed/](assets/processed/) - 处理后数据
- [assets/graphs/](assets/graphs/) - 图表

---

## 5. 预期性能

### 点对点 RPC 性能

| 配置 | P50 延迟 | 吞吐量 | 提升 |
|------|---------|--------|------|
| C1: Baseline | 457.9 ns | 7.9M ops/sec | 基线 |
| C3: PerCore+Shard | **150.2 ns** | **22.5M ops/sec** | **+185%** |

---

## 6. 相关资源

### 测试命令

```bash
# Phase 1: Baseline
make benchmark-rpc > assets/raw/C1_baseline_$(date +%Y%m%d_%H%M%S).txt

# Phase 2-3: Full test
make benchmark-rpc-full

# Generate report
./scripts/generate_rpc_report.sh
```

### 相关文档

- [RPC V4 改造提案](../../../09_code-review/2026-03-05_proposal-transport-rpc-v4-refactor.md)
- [SourceID 设计和分配](../../../09_code-review/2026-03-05_sourceid-design-and-allocation.md)
- [RPC Goroutine 使用分析](../../../09_code-review/2026-03-05_rpc-goroutine-usage-analysis.md)

---

**测试状态**: 🟡 进行中
**预计完成**: 2026-03-12
**最后更新**: 2026-03-05
