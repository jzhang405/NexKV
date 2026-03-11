# Benchmark 测试目录

本目录用于存储 NexKV 项目的所有性能测试结果和分析报告。

---

## 📁 目录结构

```
10_benchmark/
├── README.md                          # 本文件
├── templates/                          # 模板文件
│   ├── benchmark_report_template.md   # 报告模板
│   └── benchmark_checklist.md         # 检查清单
├── 2026-03-01-executor-comparison/    # Executor 对比测试（历史）
│   ├── README.md                      # 测试概述
│   ├── plan.md                        # 测试计划
│   ├── results.md                     # 测试结果
│   └── assets/                        # 数据文件
├── 2026-03-05-rpc-performance/        # RPC 性能测试（当前）
│   ├── README.md                      # 测试概述
│   ├── plan.md                        # 测试计划（8配置×6场景）
│   ├── phase1-baseline.md             # Phase 1: Baseline 测试
│   ├── phase2-design.md               # Phase 2: Design 文档测试
│   ├── phase3-comparison.md           # Phase 3: 配置对比测试
│   ├── phase4-conclusion.md           # Phase 4: 最优配置选择
│   └── assets/                        # 数据文件
│       ├── raw/                       # 原始数据（benchmark 输出）
│       ├── processed/                 # 处理后数据（CSV/JSON）
│       └── graphs/                    # 图表（PNG/SVG）
└── assets/                            # 公共资源（旧数据）
    └── 2026-03-01-perf/               # 历史数据
```

---

## 🎯 命名规范

### 测试目录命名

**格式**: `{YYYY-MM-DD}-{test-subject}`

**示例**:
- `2026-03-01-executor-comparison` - Executor 对比测试
- `2026-03-05-rpc-performance` - RPC 性能测试
- `2026-03-10-wal-throughput` - WAL 吞吐量测试

### 数据文件命名

**原始数据**: `{config}_{scenario}_{timestamp}.{ext}`

**示例**:
- `C1_AntsNetwork_p2p_20260305_143022.txt` - C1 配置点对点测试
- `C3_PerCoreShard_broadcast_20260305_143500.csv` - C3 配置广播测试

---

## 📊 Benchmark 分类

### 1. Executor 性能测试
- **目标**: 对比不同 Executor 的性能
- **指标**: 延迟、吞吐量、CPU 使用率、内存占用
- **频率**: 每次 Executor 实现变更

### 2. RPC 性能测试
- **目标**: 测试 RPC 层的性能
- **指标**: 点对点延迟、广播吞吐、并发 QPS
- **频率**: 每次 RPC 接口变更

### 3. 存储性能测试
- **目标**: 测试 WAL/BTree 的性能
- **指标**: 写入吞吐、读取延迟、空间效率
- **频率**: 每次存储引擎变更

### 4. 网络性能测试
- **目标**: 测试传输层性能
- **指标**: 带宽利用率、连接建立时间
- **频率**: 每次网络层变更

---

## 🔧 使用方法

### 创建新的 Benchmark 测试

```bash
# 1. 创建测试目录
cd docs/10_benchmark
mkdir -p 2026-03-10-{test-name}/assets/{raw,processed,graphs}

# 2. 复制模板
cp templates/benchmark_report_template.md 2026-03-10-{test-name}/README.md

# 3. 编辑测试计划
# 4. 运行测试
# 5. 记录结果
# 6. 生成报告
```

### 运行 Benchmark 测试

```bash
# RPC 性能测试
make benchmark-rpc > docs/10_benchmark/2026-03-05-rpc-performance/assets/raw/C1_baseline_$(date +%Y%m%d_%H%M%S).txt

# Executor 对比测试
make benchmark-executor > docs/10_benchmark/2026-03-01-executor-comparison/assets/raw/executor_$(date +%Y%m%d_%H%M%S).txt
```

---

## 📈 测试流程

1. **计划阶段**
   - 明确测试目标
   - 设计测试矩阵
   - 准备测试环境

2. **执行阶段**
   - 运行基准测试
   - 收集原始数据
   - 保存到 `assets/raw/`

3. **分析阶段**
   - 处理数据
   - 生成图表
   - 保存到 `assets/processed/` 和 `assets/graphs/`

4. **报告阶段**
   - 填写测试报告
   - 得出结论
   - 提出建议

---

## 📝 模板文件

- [benchmark_report_template.md](templates/benchmark_report_template.md) - 标准报告模板
- [benchmark_checklist.md](templates/benchmark_checklist.md) - 测试检查清单

---

## 🔗 相关文档

### BTree 性能测试

- [BTree Phase 1 性能优化总结](2026-03-11-btree-perf-phase1/SUMMARY.md) - NexKV BTree 优化测试总结
- [Lealone AOSE BTree 测试说明](2026-03-11-lealone-aose-btree/README.md) - Lealone BTree benchmark 文档
- [NexKV vs Lealone 性能对比](2026-03-11-lealone-aose-btree/analysis/comparison-with-nexkv.md) - 架构和性能对比分析

### 其他性能测试

- [RPC 性能测试方案](../09_code-review/2026-03-05_rpc-perf-benchmark-plan.md)
- [RPC Executor SourceID 设计](../09_code-review/2026-03-05_rpc-executor-sourceid-design.md)
- [Executor 对比报告](assets/2026-03-01-perf/executor_comparison_report.md)

---

**最后更新**: 2026-03-11
**维护人**: jzh
