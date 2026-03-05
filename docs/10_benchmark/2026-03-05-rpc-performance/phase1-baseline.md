# Phase 1: Baseline 测试

> **测试日期**: {YYYY-MM-DD}
> **测试人**: jzh
> **配置**: C1 (AntsPool + SourceNetwork)
> **状态**: ⏳ 待测试

---

## 1. 测试目标

建立当前性能基线，作为后续对比的基准。

---

## 2. 测试配置

### 2.1 配置参数

```yaml
Executor: AntsPoolExecutor
SourceID: SourceNetwork
MaxWorkers: 100
QueueSize: 1000
```

### 2.2 测试场景

1. **S1: 点对点 RPC**
   - 并发: 1
   - Payload: 1KB
   - 持续时间: 10s

2. **S2: 广播发送**
   - 节点数: 3/10/50/100
   - Payload: 512B
   - 持续时间: 10s

3. **S3: 并发压力**
   - 并发数: 10/100/1000
   - Payload: 256B
   - 持续时间: 10s

4. **S4: 异步回调**
   - 回调数: 1000
   - Payload: 128B

---

## 3. 测试结果

### 3.1 S1: 点对点 RPC

**数据文件**: [assets/raw/C1_p2p_{timestamp}.txt](assets/raw/C1_p2p_{timestamp}.txt)

| 指标 | 值 |
|------|-----|
| P50 延迟 | {value} ns |
| P99 延迟 | {value} ns |
| 吞吐量 | {value} ops/sec |
| 内存分配 | {value} allocs/op |
| CPU 使用率 | {value}% |

### 3.2 S2: 广播发送

**数据文件**: [assets/raw/C1_broadcast_{timestamp}.txt](assets/raw/C1_broadcast_{timestamp}.txt)

| 节点数 | 完成时间 | 总吞吐量 | 内存分配 |
|--------|---------|---------|---------|
| 3 | {value} ms | {value} msgs/sec | {value} |
| 10 | {value} ms | {value} msgs/sec | {value} |
| 50 | {value} ms | {value} msgs/sec | {value} |
| 100 | {value} ms | {value} msgs/sec | {value} |

### 3.3 S3: 并发压力

**数据文件**: [assets/raw/C1_concurrent_{timestamp}.txt](assets/raw/C1_concurrent_{timestamp}.txt)

| 并发数 | QPS | CPU 使用率 | 内存使用 | Goroutines |
|--------|-----|----------|---------|-----------|
| 10 | {value} | {value}% | {value} MB | {value} |
| 100 | {value} | {value}% | {value} MB | {value} |
| 1000 | {value} | {value}% | {value} MB | {value} |

### 3.4 S4: 异步回调

**数据文件**: [assets/raw/C1_callback_{timestamp}.txt](assets/raw/C1_callback_{timestamp}.txt)

| 指标 | 值 |
|------|-----|
| 回调延迟 (P50) | {value} ns |
| 回调延迟 (P99) | {value} ns |
| 回调吞吐量 | {value} callbacks/sec |
| Goroutines | {value} |

---

## 4. 资源使用

### 4.1 CPU 使用

| 指标 | 值 |
|------|-----|
| CPU 让步时间 | {value}% |
| 上下文切换 | {value} /sec |
| CPU 缓存命中率 | {value}% |

### 4.2 内存使用

| 指标 | 值 |
|------|-----|
| 堆内存分配 | {value} MB |
| GC 次数 | {value} |
| Goroutines | {value} |

---

## 5. 分析

### 5.1 性能特点

{描述 C1 配置的性能特点}

### 5.2 瓶颈识别

{识别性能瓶颈}

### 5.3 基线总结

{总结 baseline 性能}

---

## 6. 下一步

- [ ] 完成 Phase 2: Design 文档测试
- [ ] 对比 C1 与 C2-C5 的性能差异
- [ ] 生成性能对比报告

---

**测试完成时间**: {YYYY-MM-DD HH:MM}
**下一步**: Phase 2 - Design 文档测试
