# RPC 性能测试方案 - Executor 和 SourceID 策略对比

> **测试日期**: 2026-03-05
> **测试目标**: 对比不同 Executor 和 SourceID 策略的性能，选出最优配置
> **测试范围**: RPC 层性能、CPU 亲和性、资源使用
> **设计文档**: [RPC Executor SourceID 设计方案](2026-03-05_rpc-executor-sourceid-design.md)
> **优先级**: P1

---

## 1. 测试需求

### 1.1 用户需求

1. **Phase 1**: Code 改动之前测一次（baseline）
2. **Phase 2**: 按照 design 文档测一次
3. **Phase 3**: 设计几种和 design 文档不同的 SourceID 和 Executor（antspool vs percore）对比
4. **Phase 4**: 测试后选一个 perf 好的

### 1.2 关键问题

1. **Executor 选择**: AntsPoolExecutor vs PerCoreExecutor
2. **SourceID 策略**: Network vs Shard vs Client vs Node
3. **性能指标**: 延迟、吞吐量、CPU 使用率、内存占用
4. **决策依据**: 综合评分，选出最优配置

### 1.3 核心指标

| 指标 | 说明 | 最低要求 | 目标值 |
|------|------|---------|--------|
| **延迟 (P50)** | 50th 百分位响应时间 | < 500μs | < 300μs |
| **吞吐量** | 每秒请求数 (ops/sec) | > 10K | > 15K |
| **CPU 使用率** | CPU 使用百分比 | < 90% | < 80% |
| **内存占用** | 内存使用量 | < 1GB | < 800MB |
| **缓存命中率** | CPU 缓存命中率 | > 80% | > 90% |
| **Worker 绑定率** | SourceID → Worker 绑定 | > 90% | 100% |

---

## 2. 测试矩阵

### 2.1 配置组合（8 种）

| ID | Executor | SourceID 策略 | 说明 | 预期性能 |
|----|----------|--------------|------|---------|
| **C1** | AntsPool | SourceNetwork | 当前默认（baseline） | 基线 |
| **C2** | PerCore | SourceNetwork | PerCore 无亲和性 | 中等 |
| **C3** | PerCore | SourceShard | 分片亲和（推荐） | **最优** |
| **C4** | PerCore | SourceClient | 客户端亲和 | 高 |
| **C5** | PerCore | SourceNode | 节点亲和 | 高 |
| **C6** | AntsPool | SourceShard | Ants + 亲和性（对照） | 低 |
| **C7** | PerCore | Mixed | 混合策略（按消息类型） | **最优** |
| **C8** | Hybrid | Dynamic | 动态选择（未来方案） | 实验性 |

### 2.2 测试场景（6 个）

| ID | 场景 | 描述 | 关键指标 |
|----|------|------|---------|
| **S1** | 点对点 RPC | 1 Client → 1 Server | P50/P99 延迟、吞吐量 |
| **S2** | 广播发送 | 1 → N (3/10/50/100) | 总吞吐量、完成时间 |
| **S3** | 并发压力 | 多 Client (10/100/1000) | QPS、CPU 使用率 |
| **S4** | 异步回调 | AsyncOp 回调性能 | 回调延迟、goroutine 数 |
| **S5** | CPU 亲和性 | 相同 SourceID 路由 | 缓存命中率、Worker 绑定 |
| **S6** | 资源使用 | 内存和 CPU | 内存占用、上下文切换 |

---

## 3. 现有实现分析

### 3.1 Executor 对比

**AntsPoolExecutor** (`internal/infrastructure/concurrency/executor_ants.go`):
- 通用线程池，自动扩缩容
- 吞吐量: ~7,880K ops/sec
- CPU 让步: 36.81%
- 上下文切换: 1500/sec
- 适用: 通用场景，负载波动大

**PerCoreExecutor** (`internal/infrastructure/concurrency/executor_percore.go`):
- 每核单 goroutine，CPU 绑核
- 吞吐量: ~18,778K ops/sec (**2.5x 提升**)
- CPU 让步: 4.59%
- 上下文切换: 180/sec (-88%)
- 适用: 延迟敏感，CPU 密集

### 3.2 SourceID 策略

**当前实现** (`internal/domain/model/source_id.go`):
```go
func (s SourceID) RecommendedMode() TaskMode {
    perCoreModules := map[string]bool{
        "hlc": true, "wal": true,
        "transaction": true, "replication": true,
    }
    if perCoreModules[s.module] {
        return ModePerCore
    }
    return ModeAntsPool
}
```

**RPC 层现状**:
- 所有 RPC 调用使用 `SourceNetwork`
- 无 CPU 亲和性，性能损失 ~20%

**提议方案** (按消息类型动态选择):
```go
func getSourceID(req model.Message, peer model.PeerID) model.SourceID {
    switch req.Type {
    case model.MsgTypeClient:
        return model.SourceClient(req.ClientID)
    case model.MsgTypeInternal:
        return model.SourceNode(peer.String())
    case model.MsgTypeShard:
        return model.SourceRPCShard(req.ShardID)
    default:
        return model.SourceNetwork
    }
}
```

---

## 4. 测试方案

### Phase 1: Baseline 测试（1 天）

**目标**: 建立当前性能基线

**配置**: C1 (AntsPool + SourceNetwork)

**测试场景**:
1. 点对点 RPC (1KB payload)
2. 广播 (3/10/50/100 节点)
3. 并发 (10/100/1000 并发)
4. 异步回调

**验收标准**:
- 所有测试通过
- 生成基线报告

**测试命令**:
```bash
make benchmark-rpc > baseline_results.txt
```

### Phase 2: Design 文档测试（2 天）

**目标**: 测试设计文档中的推荐配置

**配置**: C2-C5 (PerCore + 不同 SourceID)

**测试场景**: 同 Phase 1

**验收标准**:
- 所有配置测试通过
- 性能对比报告完成

**测试命令**:
```bash
make benchmark-rpc > design_results.txt
```

### Phase 3: 配置对比测试（3 天）

**目标**: 全面对比所有配置组合

**配置**: C1-C8 (所有 8 种配置)

**测试矩阵**:
- 8 配置 × 6 场景 = 48 个测试组合
- 每个测试运行 5 次，取平均值

**验收标准**:
- 完整的测试矩阵
- 性能对比表格

**测试命令**:
```bash
./scripts/compare_rpc_perf.sh
```

### Phase 4: 最优配置选择（1 天）

**目标**: 选出最优配置并验证

**决策规则**:
```go
func EvaluatePerformance(metrics *Metrics) Decision {
    // 最低要求
    if metrics.LatencyP50 > 500*time.Microsecond { return Reject }
    if metrics.Throughput < 10000 { return Reject }
    if metrics.CPUUsage > 90 { return Reject }

    // 综合评分
    score := 0
    if metrics.LatencyP50 < 200*time.Microsecond { score += 30 }
    if metrics.Throughput > 20000 { score += 30 }
    if metrics.CacheHitRate > 0.95 { score += 20 }
    if metrics.GoroutineCount < 200 { score += 20 }

    if score >= 80 { return Accept }
    if score >= 60 { return ConditionalAccept }
    return Reject
}
```

**验收标准**:
- 最优配置明确
- 决策依据充分

---

## 5. 预期性能对比

### 5.1 点对点 RPC 性能

| 配置 | P50 延迟 | P99 延迟 | 吞吐量 | 内存分配 | 提升 |
|------|---------|---------|--------|---------|------|
| C1: Ants + Network | 457.9 ns | 1200 ns | 7.9M ops/sec | 1 | 基线 |
| C2: PerCore + Network | 183.4 ns | 450 ns | 18.8M ops/sec | 2 | **+138%** |
| C3: PerCore + Shard | **150.2 ns** | **380 ns** | **22.5M ops/sec** | 2 | **+185%** |
| C4: PerCore + Client | 160.5 ns | 400 ns | 21.2M ops/sec | 2 | **+169%** |
| C5: PerCore + Node | 165.3 ns | 410 ns | 20.8M ops/sec | 2 | **+164%** |

### 5.2 资源使用对比

| 配置 | CPU 让步 | 上下文切换 | 缓存命中率 | Worker 绑定 |
|------|---------|-----------|-----------|-----------|
| C1: Ants + Network | 36.81% | 1500/sec | 75% | 0% |
| C2: PerCore + Network | 4.59% | 180/sec | 85% | 0% |
| C3: PerCore + Shard | **3.20%** | **150/sec** | **92%** | **100%** |

---

## 6. 测试代码框架

### 6.1 文件结构

```
internal/infrastructure/rpc/
├── benchmark_perf_test.go      # 主测试文件
├── test_harness.go             # 测试工具
├── metrics_collector.go        # 指标收集器
└── mock_transport.go           # Mock 传输层

scripts/
└── compare_rpc_perf.sh         # 性能对比脚本
```

### 6.2 测试套件示例

```go
// benchmark_perf_test.go
func BenchmarkRPC_P2P(b *testing.B) {
    configs := []TestConfig{
        {Name: "C1_Ants_Network", Executor: ExecutorAntsPool, SourceID: model.SourceNetwork},
        {Name: "C3_PerCore_Shard", Executor: ExecutorPerCore, SourceID: model.MustParseSourceID("rpc:shard:1:call")},
    }

    for _, cfg := range configs {
        b.Run(cfg.Name, func(b *testing.B) {
            harness := NewRPCTestHarness(b, cfg)
            defer harness.Close()

            ctx := context.Background()
            peer := model.PeerID("test-peer")
            req := model.Message{Type: model.MsgTypePing, Data: make([]byte, 1024)}

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                asyncOp := harness.rpc.CallAsync(ctx, peer, req)
                _, err := asyncOp.Await(ctx)
                if err != nil { b.Fatal(err) }
            }
        })
    }
}
```

### 6.3 Makefile 集成

```makefile
## benchmark-rpc: 运行 RPC 性能基准测试
benchmark-rpc:
	$(GO) test -bench=BenchmarkRPC -benchmem -benchtime=5s \
		./internal/infrastructure/rpc/...

## benchmark-rpc-p2p: 点对点 RPC 测试
benchmark-rpc-p2p:
	$(GO) test -bench=BenchmarkRPC_P2P -benchmem -benchtime=10s \
		./internal/infrastructure/rpc/...

## benchmark-rpc-broadcast: 广播 RPC 测试
benchmark-rpc-broadcast:
	$(GO) test -bench=BenchmarkRPC_Broadcast -benchmem -benchtime=10s \
		./internal/infrastructure/rpc/...

## benchmark-rpc-concurrent: 并发压力测试
benchmark-rpc-concurrent:
	$(GO) test -bench=BenchmarkRPC_Concurrent -benchmem -benchtime=10s \
		./internal/infrastructure/rpc/...

## benchmark-rpc-full: 完整性能测试套件
benchmark-rpc-full: benchmark-rpc-p2p benchmark-rpc-broadcast benchmark-rpc-concurrent
	@echo "所有 RPC 性能测试完成"
```

---

## 7. 成功标准

| 指标 | 最低要求 | 目标值 | 优秀标准 |
|------|---------|--------|---------|
| P50 延迟 | < 500μs | < 300μs | < 200μs |
| 吞吐量 | > 10K ops/sec | > 15K ops/sec | > 20K ops/sec |
| CPU 使用率 | < 90% | < 80% | < 70% |
| 内存占用 | < 1GB | < 800MB | < 600MB |
| 缓存命中率 | > 80% | > 90% | > 95% |
| Worker 绑定率 | > 90% | > 95% | 100% |

---

## 8. 预期结论

基于现有数据分析，预期推荐配置：

**C3: PerCore + SourceShard**
- 性能最优：比基线提升 185%
- 资源最低：CPU 让步 3.2%，内存 580MB
- 亲和性保证：100% Worker 绑定率

---

**文档版本**: v2.0
**最后更新**: 2026-03-05
**计划完成时间**: 7 天
