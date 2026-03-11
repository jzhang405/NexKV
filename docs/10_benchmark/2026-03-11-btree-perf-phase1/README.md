# BTree 写性能优化 Phase 1 - 基准测试报告

> **测试日期**: 2026-03-11
> **测试人**: jzhang405
> **关联 PR**: PR-XXX (待创建)
> **关联文档**: [PR 全流程文档](../../../06_project_management/pr_documents/feature/2026-03-11_PR-XXX_BTree写性能优化Phase1_全流程.md)

---

## 1. 测试概述

### 1.1 测试目标

验证 BTree 写性能优化 Phase 1 的有效性，通过对比优化前后的性能指标，确保达到以下目标：

**性能目标**：
- **写延迟**: 从 10.6 µs 降到 **< 8.6 µs**（-19%）
- **内存分配**: 从 24.9% 降到 **< 15%**（-40%）
- **GC 开销**: 从 21.6% 降到 **< 10%**（-54%）

### 1.2 优化项

本阶段包含三项优化：

| 优化项 | 优先级 | 预期收益 | 预估工期 |
|--------|--------|----------|----------|
| 分裂策略优化 (MaxKeys 256→512) | P0 | 分裂频率 -50%，写延迟 -15% ~ -20% | 1天 |
| Node 对象池 | P0 | 内存分配 -60% ~ -70%，写延迟 -15% | 2-3天 |
| Path 对象池 | P0 | Path 分配减少，写延迟 -2% ~ -3% | 1天 |

### 1.3 关键问题

1. **分裂策略优化**：增加 MaxKeys 是否会导致内存占用显著增加？
2. **对象池效果**：Node 和 Path 对象池的命中率如何？是否存在内存泄漏？
3. **性能提升**：三项优化叠加后是否能达到性能目标？
4. **副作用**：优化是否影响读性能（当前 149-228 ns/op）？

---

## 2. 测试配置

### 2.1 硬件环境

```yaml
CPU: TBD
Memory: TBD
Storage: TBD
OS: Linux 6.17.0-14-generic
Kernel: 6.17.0-14-generic
Go: 1.24
```

### 2.2 软件配置

```go
// BTree 配置（优化前）
BTreeConfig{
    PageSize:    4096,
    MaxKeys:     256,    // → 优化后: 512
    MinKeys:     128,    // → 优化后: 256
    MaxVersions: 10,
    EnablePool:  false,  // → 优化后: true
}

// 测试参数
TestDataSize:  100000     // 10万条键值对
KeySize:       16-32 bytes
ValueSize:     64-128 bytes
```

### 2.3 测试矩阵

| 配置 ID | 配置名称 | MaxKeys | 对象池 | 说明 |
|---------|---------|---------|--------|------|
| C0 | Baseline | 256 | 禁用 | 基线（优化前） |
| C1 | Split Opt Only | 512 | 禁用 | 仅分裂策略优化 |
| C2 | Pool Only | 256 | 启用 | 仅对象池优化 |
| C3 | Full Opt | 512 | 启用 | 完整优化（三项） |

---

## 3. 测试场景

### 场景 1: 基线性能测试 (C0)

**描述**: 测试优化前的基线性能

**测试代码**:
```bash
cd internal/infrastructure/storage/btree
go test -bench=BenchmarkBTree_Insert -benchmem -benchtime=10s -run=^$ > ../../../docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/C0_baseline_$(date +%Y%m%d_%H%M%S).txt 2>&1
```

**关键指标**:
- 写延迟 (ns/op)
- 内存分配 (B/op)
- 分配次数 (allocs/op)
- 分裂次数

---

### 场景 2: 分裂策略优化 (C1)

**描述**: 测试仅修改 MaxKeys/MinKeys 的效果

**修改配置**:
```go
// internal/domain/model/btree_types.go
const (
    DefaultMaxKeys = 512  // 256 → 512
    DefaultMinKeys = 256  // 128 → 256
)
```

**测试代码**:
```bash
go test -bench=BenchmarkBTree_Insert -benchmem -benchtime=10s -run=^$ > ../../../docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/C1_split_opt_$(date +%Y%m%d_%H%M%S).txt 2>&1
```

**关键指标**:
- 写延迟变化
- 分裂频率变化
- 内存占用变化

---

### 场景 3: 对象池优化 (C2)

**描述**: 测试仅启用对象池的效果

**修改配置**:
```go
// internal/domain/model/btree_types.go
func NewDefaultBTreeConfig() *BTreeConfig {
    return &BTreeConfig{
        EnablePool: true,  // false → true
    }
}
```

**测试代码**:
```bash
go test -bench=BenchmarkBTree_Insert -benchmem -benchtime=10s -run=^$ > ../../../docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/C2_pool_opt_$(date +%Y%m%d_%H%M%S).txt 2>&1
```

**关键指标**:
- 写延迟变化
- 内存分配变化
- 对象池命中率

---

### 场景 4: 完整优化 (C3)

**描述**: 测试三项优化的叠加效果

**修改配置**:
```go
// internal/domain/model/btree_types.go
const (
    DefaultMaxKeys = 512
    DefaultMinKeys = 256
)

func NewDefaultBTreeConfig() *BTreeConfig {
    return &BTreeConfig{
        MaxKeys:    512,
        MinKeys:    256,
        EnablePool: true,
    }
}
```

**测试代码**:
```bash
go test -bench=BenchmarkBTree_Insert -benchmem -benchtime=10s -run=^$ > ../../../docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/C3_full_opt_$(date +%Y%m%d_%H%M%S).txt 2>&1
```

**关键指标**:
- 写延迟（是否达到 < 8.6 µs 目标）
- 内存分配（是否达到 < 15% 目标）
- GC 开销（是否达到 < 10% 目标）

---

### 场景 5: 读性能回归测试

**描述**: 验证优化不影响读性能

**测试代码**:
```bash
go test -bench=BenchmarkBTree_Get -benchmem -benchtime=10s -run=^$ > ../../../docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/C4_read_perf_$(date +%Y%m%d_%H%M%S).txt 2>&1
```

**关键指标**:
- 读延迟（应保持在 149-228 ns/op）
- 读内存分配

---

## 4. 测试结果

> **注意**: 本部分将在测试执行后填写

### 4.1 基线性能 (C0)

| 指标 | 数值 | 单位 |
|------|------|------|
| 写延迟 | TBD | ns/op |
| 内存分配 | TBD | B/op |
| 分配次数 | TBD | allocs/op |
| 分裂次数 | TBD | 次 |

**数据文件**: [assets/raw/C0_baseline_{timestamp}.txt](assets/raw/)

---

### 4.2 分裂策略优化 (C1)

| 指标 | C0 (基线) | C1 (优化) | 提升 |
|------|----------|----------|------|
| 写延迟 | TBD | TBD | TBD% |
| 内存分配 | TBD | TBD | TBD% |
| 分裂次数 | TBD | TBD | TBD% |

**数据文件**: [assets/raw/C1_split_opt_{timestamp}.txt](assets/raw/)

---

### 4.3 对象池优化 (C2)

| 指标 | C0 (基线) | C2 (优化) | 提升 |
|------|----------|----------|------|
| 写延迟 | TBD | TBD | TBD% |
| 内存分配 | TBD | TBD | TBD% |
| 分配次数 | TBD | TBD | TBD% |
| 池命中率 | TBD | - | - |

**数据文件**: [assets/raw/C2_pool_opt_{timestamp}.txt](assets/raw/)

---

### 4.4 完整优化 (C3)

| 指标 | 目标 | C0 (基线) | C3 (优化) | 达标状态 |
|------|------|----------|----------|---------|
| 写延迟 | < 8.6 µs | TBD | TBD | ⏳ |
| 内存分配 | < 15% | TBD | TBD | ⏳ |
| GC 开销 | < 10% | TBD | TBD | ⏳ |

**数据文件**: [assets/raw/C3_full_opt_{timestamp}.txt](assets/raw/)

---

### 4.5 读性能回归测试

| 指标 | C0 (基线) | C3 (优化) | 变化 | 状态 |
|------|----------|----------|------|------|
| 读延迟 | 149-228 ns | TBD | TBD% | ⏳ |

**数据文件**: [assets/raw/C4_read_perf_{timestamp}.txt](assets/raw/)

---

## 5. 资源使用分析

### 5.1 内存使用

| 配置 | 内存分配 | GC 次数 | 对象池大小 |
|------|---------|---------|-----------|
| C0 | TBD | TBD | N/A |
| C1 | TBD | TBD | N/A |
| C2 | TBD | TBD | TBD |
| C3 | TBD | TBD | TBD |

---

### 5.2 性能分析（可选）

**CPU Profile**: [assets/processed/cpu.prof](assets/processed/)

**Memory Profile**: [assets/processed/mem.prof](assets/processed/)

**Trace**: [assets/processed/trace.out](assets/processed/)

---

## 6. 分析与结论

### 6.1 关键发现

> **待测试完成后填写**

1. **{finding-1}**
   - 数据: {data}
   - 原因: {reason}
   - 影响: {impact}

2. **{finding-2}**
   - 数据: {data}
   - 原因: {reason}
   - 影响: {impact}

---

### 6.2 性能瓶颈

> **待测试完成后填写**

1. **{bottleneck-1}**
   - 位置: {location}
   - 原因: {reason}
   - 优化建议: {suggestion}

---

### 6.3 结论

> **待测试完成后填写**

{回答第 1.3 节的关键问题}

---

## 7. 建议

### 7.1 配置推荐

> **待测试完成后填写**

**推荐配置**: {C0/C1/C2/C3}

**理由**:
1. {reason-1}
2. {reason-2}

**预期收益**:
- 写延迟: {percent}%
- 内存分配: {percent}%
- GC 开销: {percent}%

---

### 7.2 后续优化

基于测试结果，确定 Phase 2 优化方向：

| 优先级 | 优化项 | 预期收益 | 工作量 |
|--------|--------|----------|--------|
| 高 | {optimization-1} | {benefit} | {effort} |
| 中 | {optimization-2} | {benefit} | {effort} |

**可能的 Phase 2 优化项**（根据 PR 文档）：
- [ ] 值指针方案（ValueRef）- 需要 POC 验证
- [ ] 缓存优化（PageCache）
- [ ] 并发写入优化（分段 CAS）

---

## 8. 附录

### 8.1 测试命令

```bash
# 进入测试目录
cd internal/infrastructure/storage/btree

# 运行所有基准测试
go test -bench=. -benchmem -benchtime=10s -run=^$

# 运行特定测试
go test -bench=BenchmarkBTree_Insert -benchmem -benchtime=10s -run=^$
go test -bench=BenchmarkBTree_Get -benchmem -benchtime=10s -run=^$

# 生成 CPU profile
go test -bench=BenchmarkBTree_Insert -cpuprofile=cpu.prof -benchtime=30s -run=^$
go tool pprof cpu.prof

# 生成 memory profile
go test -bench=BenchmarkBTree_Insert -memprofile=mem.prof -benchtime=30s -run=^$
go tool pprof mem.prof

# 生成 trace
go test -bench=BenchmarkBTree_Insert -trace=trace.out -benchtime=30s -run=^$
go tool trace trace.out
```

---

### 8.2 数据文件

- [原始数据](assets/raw/)
- [处理数据](assets/processed/)
- [图表](assets/graphs/)

---

### 8.3 相关文档

- [PR 全流程文档](../../../06_project_management/pr_documents/feature/2026-03-11_PR-XXX_BTree写性能优化Phase1_全流程.md)
- [性能优化计划](../../../09_code-review/2026-03-10-btree-write-performance-tuning-plan.md)
- [Delta POC 总结](../../../08_postmortem/2026-03-11-delta-write-optimization-poc-summary.md)

---

**报告版本**: v1.0
**最后更新**: 2026-03-11
**状态**: ⏳ 待测试
