# Phase 1: Baseline 测试

> **测试日期**: 2026-03-05
> **测试人**: jzh
> **配置**: C1 (AntsPool + SourceNetwork)
> **状态**: ✅ 已完成（Transport 层 + RPC 层 baseline）

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

### 3.1 S1: 点对点 RPC (Transport 层)

**数据文件**: [assets/raw/C1_transport_all_benchmarks_20260305_174251.txt](assets/raw/C1_transport_all_benchmarks_20260305_174251.txt)

**注**: 当前测试使用 Mock Transport，仅测试中间件链性能，未包含真实网络传输。

#### 3.1.1 无中间件 (Baseline)

| 指标 | 值 |
|------|-----|
| 吞吐量 | 53.7M ops/sec |
| 延迟 (平均) | 112.9 ns/op |
| 内存分配 | 264 B/op |
| 内存分配次数 | 6 allocs/op |

#### 3.1.2 完整中间件链 (RateLimit → CircuitBreaker → Compression → Retry)

| 指标 | 值 |
|------|-----|
| 吞吐量 | 15.9M ops/sec |
| 延迟 (平均) | 366.4 ns/op |
| 内存分配 | 566 B/op |
| 内存分配次数 | 13 allocs/op |

#### 3.1.3 不同负载大小

| 负载大小 | 吞吐量 | 延迟 | 内存分配 |
|---------|--------|------|---------|
| 64B (Small) | 6.1M ops/sec | 977.4 ns/op | 596 B/op |
| 1KB (Medium) | 5.3M ops/sec | 1118.0 ns/op | 1675 B/op |
| 4KB (Large) | 177K ops/sec | 36013.0 ns/op | 157802 B/op |

### 3.2 S2: 广播发送

**状态**: ⏳ 待实现

**说明**: 需要创建 RPC 层的广播 benchmark 测试，模拟向多个节点发送消息的场景。

**计划测试场景**:
| 节点数 | 完成时间 | 总吞吐量 | 内存分配 |
|--------|---------|---------|---------|
| 3 | - | - | - |
| 10 | - | - | - |
| 50 | - | - | - |
| 100 | - | - | - |

### 3.3 S3: 并发压力 (Transport 层)

**数据文件**: [assets/raw/C1_transport_all_benchmarks_20260305_174251.txt](assets/raw/C1_transport_all_benchmarks_20260305_174251.txt)

**注**: 当前测试使用 10 个 goroutine 并发执行（BenchmarkRPC_Concurrent）。

| 并发数 (goroutines) | 吞吐量 | 延迟 (平均) | 内存分配 |
|---------------------|--------|------------|---------|
| 10 | 14.4M ops/sec | 406.3 ns/op | 583 B/op |

**说明**: 需要扩展测试，增加 100 和 1000 并发场景。

### 3.4 S4: 异步回调

**数据文件**: [assets/raw/C1_rpc_async_benchmarks_20260305_181955.txt](assets/raw/C1_rpc_async_benchmarks_20260305_181955.txt)

**RPC 层核心性能** (AsyncOp 和回调):

| 指标 | 吞吐量 | 延迟 (平均) | 内存分配 |
|------|--------|------------|---------|
| AsyncOp 创建 | **4.17B ops/sec** | 0.24 ns/op | 0 B/op |
| AsyncOp 等待 | **710M ops/sec** | 1.41 ns/op | 0 B/op |
| 回调执行 | **171M ops/sec** | 5.84 ns/op | 0 B/op |
| 并发回调 (10 goroutines) | **69M ops/sec** | 14.51 ns/op | 0 B/op |
| 并发 AsyncOp (100 goroutines) | **54.5M ops/sec** | 18.34 ns/op | 0 B/op |

**消息创建性能**:

| 场景 | 吞吐量 | 延迟 (平均) | 内存分配 |
|------|--------|------------|---------|
| 单线程 (1KB) | **5.3M ops/sec** | 190.0 ns/op | 1024 B/op |
| 并发 (10 goroutines, 512B) | **9.2M ops/sec** | 108.6 ns/op | 512 B/op |

**说明**: AsyncOp 和回调性能极快（纳秒级），不是性能瓶颈。

---

## 4. 资源使用

**注**: 当前测试未采集详细的 CPU 和内存使用数据。建议使用 pprof 和 perf 工具进行更深入的性能分析。

### 4.1 CPU 使用

| 指标 | 值 | 备注 |
|------|-----|------|
| CPU 让步时间 | - | 需使用 perf 采集 |
| 上下文切换 | - | 需使用 perf 采集 |
| CPU 缓存命中率 | - | 需使用 perf 采集 |

### 4.2 内存使用

| 指标 | 值 | 备注 |
|------|-----|------|
| 堆内存分配 | - | 需使用 pprof 采集 |
| GC 次数 | - | 需使用 pprof 采集 |
| Goroutines | - | 需使用 pprof 采集 |

---

## 5. 分析

### 5.1 性能特点

**Transport 层性能** (使用 Mock Transport):
- **无中间件**: 53.7M ops/sec，延迟极低 (112.9 ns/op)
- **完整中间件链**: 15.9M ops/sec，性能下降约 70%（符合预期）
- **并发性能**: 14.4M ops/sec (10 goroutines)

**RPC 层性能** (AsyncOp 和回调):
- **AsyncOp 开销**: 极低（0.24-1.41 ns/op）
- **回调性能**: 极快（5.84-14.51 ns/op）
- **消息创建**: 190 ns/op (1KB)，并发下提升到 108.6 ns/op

**中间件开销**:
- RateLimit: 33.5M ops/sec (207.3 ns/op)
- CircuitBreaker: 43.8M ops/sec (128.3 ns/op) - 开销最小
- Compression (Snappy): 192K ops/sec (30697 ns/op) - 开销最大

**负载大小影响**:
- 64B → 1KB: 吞吐量下降 13%，延迟增加 14%
- 1KB → 4KB: 吞吐量下降 97%，延迟增加 31x (压缩中间件影响)

### 5.2 瓶颈识别

1. **压缩中间件**: Snappy 压缩是最大瓶颈（30μs/op）
2. **内存分配**: 大负载导致内存分配显著增加 (4KB: 157KB/op)
3. **缺少 RPC 层测试**: 当前仅测试了 Transport 层，未包含真实的 RPC 调用流程

### 5.3 基线总结

**Transport 层 baseline** (C1: AntsPool + SourceNetwork):
- ✅ 无中间件吞吐量: 53.7M ops/sec
- ✅ 完整中间件链吞吐量: 15.9M ops/sec
- ✅ 并发性能: 14.4M ops/sec (10 goroutines)

**RPC 层 baseline** (AsyncOp + 回调):
- ✅ AsyncOp 创建: 4.17B ops/sec (0.24 ns/op)
- ✅ AsyncOp 等待: 710M ops/sec (1.41 ns/op)
- ✅ 回调执行: 171M ops/sec (5.84 ns/op)
- ✅ 并发回调: 69M ops/sec (14.51 ns/op, 10 goroutines)
- ✅ 并发 AsyncOp: 54.5M ops/sec (18.34 ns/op, 100 goroutines)
- ✅ 消息创建: 5.3M ops/sec (190 ns/op, 1KB)

**总体评估**:
- ✅ RPC 层开销极低（纳秒级），不是性能瓶颈
- ✅ Transport 层性能良好（使用 Mock Transport）
- ⚠️ 压缩中间件是主要瓶颈（30μs/op）
- ⚠️ 大负载（4KB+）性能显著下降
- ⚠️ 缺少真实网络传输测试
- ⚠️ 缺少详细的 CPU/内存资源使用数据

---

## 6. 下一步

**Phase 1 补充工作** (可选，优先级 P2):
- [ ] 采集 CPU/内存资源使用数据 (使用 pprof 和 perf)
- [ ] 添加广播性能测试（如果需要）

**Phase 2 准备** (优先级 P0):
- [x] Transport 层 baseline 已完成
- [x] RPC 层 AsyncOp/回调 baseline 已完成
- [ ] 准备 C2-C5 配置的测试环境
- [ ] 开始 Phase 2: Design 文档测试

---

**测试完成时间**: 2026-03-05 18:20
**下一步**: 进入 Phase 2 - Design 文档测试（C2-C5 配置）
