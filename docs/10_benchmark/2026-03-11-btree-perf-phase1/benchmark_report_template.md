# {Test Name} 性能测试报告

> **测试日期**: {YYYY-MM-DD}
> **测试人**: {name}
> **测试版本**: {commit-hash}
> **测试环境**: {hardware-spec}

---

## 1. 测试概述

### 1.1 测试目标

{描述测试的目标，例如：对比 X 和 Y 的性能}

### 1.2 测试范围

- **测试对象**: {object}
- **测试场景**: {scenarios}
- **排除项**: {exclusions}

### 1.3 关键问题

1. {question-1}
2. {question-2}
3. {question-3}

---

## 2. 测试配置

### 2.1 硬件环境

```yaml
CPU: {cpu-model} ({cores} cores / {threads} threads)
Memory: {memory-size}
Storage: {storage-type}
Network: {network-speed}
OS: {os-version}
Kernel: {kernel-version}
Go: {go-version}
```

### 2.2 软件配置

```go
// Executor 配置
{executor-config}

// 测试参数
{test-params}
```

### 2.3 测试矩阵

| 配置 ID | 配置名称 | 参数 1 | 参数 2 | 说明 |
|---------|---------|--------|--------|------|
| C1 | {name} | {val1} | {val2} | {desc} |
| C2 | {name} | {val1} | {val2} | {desc} |

---

## 3. 测试场景

### 场景 1: {scenario-name}

**描述**: {description}

**测试参数**:
- 并发数: {concurrency}
- 请求大小: {payload-size}
- 持续时间: {duration}

**测试代码**:
```go
{test-code}
```

---

## 4. 测试结果

### 4.1 场景 1: {scenario-name}

#### 原始数据

**数据文件**: [assets/raw/C1_{scenario}_{timestamp}.txt](assets/raw/C1_{scenario}_{timestamp}.txt)

#### 性能指标

| 配置 | P50 延迟 | P99 延迟 | 吞吐量 | CPU 使用率 | 内存占用 |
|------|---------|---------|--------|----------|---------|
| C1 | {val} | {val} | {val} | {val} | {val} |
| C2 | {val} | {val} | {val} | {val} | {val} |

#### 性能对比

| 配置 | 基线 | 提升 | 状态 |
|------|------|------|------|
| C1 | 基线 | - | ✅ |
| C2 | C1 | +{percent}% | ✅ |

#### 图表

![{scenario} 性能对比](assets/graphs/{scenario}_comparison.png)

**说明**: {chart-description}

---

## 5. 资源使用分析

### 5.1 CPU 使用

| 配置 | CPU 让步 | 上下文切换 | CPU 缓存命中率 |
|------|---------|-----------|---------------|
| C1 | {val} | {val} | {val} |
| C2 | {val} | {val} | {val} |

### 5.2 内存使用

| 配置 | 内存分配 | GC 次数 | Goroutine 数 |
|------|---------|---------|-------------|
| C1 | {val} | {val} | {val} |
| C2 | {val} | {val} | {val} |

### 5.3 性能分析（可选）

**CPU Profile**: [assets/processed/cpu.prof](assets/processed/cpu.prof)

**Memory Profile**: [assets/processed/mem.prof](assets/processed/mem.prof)

**Flame Graph**: ![CPU Flame Graph](assets/graphs/flamegraph.svg)

---

## 6. 分析与结论

### 6.1 关键发现

1. **{finding-1}**
   - 数据: {data}
   - 原因: {reason}
   - 影响: {impact}

2. **{finding-2}**
   - 数据: {data}
   - 原因: {reason}
   - 影响: {impact}

### 6.2 性能瓶颈

1. **{bottleneck-1}**
   - 位置: {location}
   - 原因: {reason}
   - 优化建议: {suggestion}

2. **{bottleneck-2}**
   - 位置: {location}
   - 原因: {reason}
   - 优化建议: {suggestion}

### 6.3 结论

{总结测试结果，回答第1节的关键问题}

---

## 7. 建议

### 7.1 配置推荐

**推荐配置**: {config-name}

**理由**:
1. {reason-1}
2. {reason-2}
3. {reason-3}

**预期收益**:
- 性能提升: {percent}%
- 资源节省: {resource}
- 延迟降低: {latency}

### 7.2 优化建议

1. **{optimization-1}**
   - 优先级: P0/P1/P2
   - 预期收益: {benefit}
   - 工作量: {effort}

2. **{optimization-2}**
   - 优先级: P0/P1/P2
   - 预期收益: {benefit}
   - 工作量: {effort}

### 7.3 后续测试

- [ ] {followup-test-1}
- [ ] {followup-test-2}
- [ ] {followup-test-3}

---

## 8. 附录

### 8.1 测试命令

```bash
# 运行测试
make benchmark-{name}

# 生成报告
./scripts/generate_report.sh
```

### 8.2 数据文件

- [原始数据](assets/raw/)
- [处理数据](assets/processed/)
- [图表](assets/graphs/)

### 8.3 相关文档

- [测试计划]({plan-doc})
- [设计文档]({design-doc})
- [相关 PR]({pr-link})

---

**报告版本**: v1.0
**最后更新**: {YYYY-MM-DD}
**审核人**: {reviewer}
