# PR Pre 文档：测试覆盖率 + 性能基准 + 中间件完善

> **PR 类型**: feature
> **分支名称**: feature/test-coverage-benchmark-middleware
> **创建日期**: 2026-02-21
> **作者**: 🤖 AI Agent
> **状态**: 🔄 待评审

---

## 一、需求概述

### 1.1 背景

根据 M1 里程碑验收报告（`docs/06_PM/milestones/2026-02-20_M1-infrastructure-layer-acceptance.md`），存在以下待改进项：

| 项目 | 当前状态 | 目标 |
|------|---------|------|
| 集成测试覆盖率 | 50% | ≥80% |
| 性能基准测试 | 未验证 | ≥10K ops/sec |
| 传输层中间件 | 仅日志+监控 | 完整链路 |

### 1.2 目标

1. **提升覆盖率**：`test/integration/scenarios` 覆盖率从 50% 提升到 80%
2. **性能验证**：基准测试验证 RPC 层吞吐量 ≥10K ops/sec
3. **中间件完善**：实现完整中间件链（日志→监控→限流→熔断→压缩）

---

## 二、技术设计

### 2.1 覆盖率提升方案

**当前覆盖率分析**：
- `setupIntegrationTest`: 90.9% ✅
- `setupIntegrationTestWithoutExecutor`: 0.0% ❌

**解决方案**：
创建 `test/integration/scenarios/helpers_test.go`，测试辅助函数。

```go
func TestSetupIntegrationTestWithoutExecutor(t *testing.T) {
    ctx, testCtx := setupIntegrationTestWithoutExecutor(t, 30*time.Second)
    assert.NotNil(t, ctx)
    assert.NotNil(t, testCtx)
}
```

### 2.2 性能基准测试设计

**测试场景**：
| 场景 | 说明 | 目标 |
|------|------|------|
| `BenchmarkRPC_Throughput` | 单机 RPC 吞吐量 | ≥10K ops/sec |
| `BenchmarkRPC_Concurrent` | 并发 RPC 吞吐量 | ≥10K ops/sec |
| `BenchmarkRPC_Payload_Small` | 64 字节负载 | - |
| `BenchmarkRPC_Payload_Medium` | 1 KB 负载 | - |
| `BenchmarkRPC_Payload_Large` | 4 KB 负载 | - |

**实现文件**：`internal/infrastructure/transport/benchmark_test.go`

### 2.3 RPC 中间件集成

**当前问题**：
`libp2p_rpc.go` 中 `middleware` 字段存在但未真正使用。

**修复方案**：
在 `Call` 和 `Broadcast` 方法中调用 `middleware.ExecuteSend`。

### 2.4 新中间件实现

#### 2.4.1 Rate Limiting 中间件
- **算法**：令牌桶（Token Bucket）
- **依赖**：`golang.org/x/time/rate`
- **配置**：`RequestsPerSecond`、`Burst`

#### 2.4.2 Circuit Breaker 中间件
- **状态机**：Closed → Open → HalfOpen → Closed
- **配置**：`FailureThreshold`、`ResetTimeout`
- **实现**：使用 `atomic` 操作保证并发安全

#### 2.4.3 Compression 中间件
- **算法**：复用现有 `CompressorType`（Snappy、ZSTD、LZ4）
- **策略**：仅压缩超过阈值的消息
- **阈值**：默认 1KB

---

## 三、文件变更清单

### 3.1 新增文件

| 文件 | 说明 |
|------|------|
| `test/integration/scenarios/helpers_test.go` | 辅助函数单元测试 |
| `internal/infrastructure/transport/benchmark_test.go` | 性能基准测试 |
| `internal/infrastructure/transport/rate_limit_middleware.go` | 限流中间件 |
| `internal/infrastructure/transport/circuit_breaker_middleware.go` | 熔断中间件 |
| `internal/infrastructure/transport/compression_middleware.go` | 压缩中间件 |
| `internal/infrastructure/transport/rate_limit_middleware_test.go` | 限流测试 |
| `internal/infrastructure/transport/circuit_breaker_middleware_test.go` | 熔断测试 |
| `internal/infrastructure/transport/compression_middleware_test.go` | 压缩测试 |

### 3.2 修改文件

| 文件 | 修改内容 |
|------|---------|
| `internal/infrastructure/transport/libp2p_rpc.go` | 集成中间件链 |
| `test/integration/scenarios/test_helpers.go` | 移除 unused 标记 |

---

## 四、依赖变更

### 4.1 新增依赖

```go
require (
    golang.org/x/time v0.5.0 // rate limiter
)
```

---

## 五、风险评估

### 5.1 风险矩阵

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|---------|
| 性能基准不达标 | 中 | 高 | 优化序列化、连接池 |
| 中间件集成破坏现有功能 | 低 | 高 | 完整单元测试覆盖 |
| 压缩中间件性能开销 | 中 | 中 | 设置合理阈值，跳过小消息 |

### 5.2 回滚方案

- 所有修改向后兼容
- 中间件默认不启用，需显式配置
- 可通过配置关闭任何中间件

---

## 六、测试计划

### 6.1 单元测试

- [ ] `helpers_test.go`：辅助函数测试
- [ ] `rate_limit_middleware_test.go`：限流测试
- [ ] `circuit_breaker_middleware_test.go`：熔断测试
- [ ] `compression_middleware_test.go`：压缩测试

### 6.2 基准测试

- [ ] 单机吞吐量 ≥10K ops/sec
- [ ] 并发吞吐量 ≥10K ops/sec
- [ ] 不同负载大小性能对比

### 6.3 集成测试

- [ ] 中间件链完整执行
- [ ] 限流触发正确
- [ ] 熔断状态转换正确
- [ ] 压缩/解压缩正确

---

## 七、验收标准

### 7.1 功能验收

- [ ] 集成测试覆盖率 ≥80%
- [ ] 性能基准 ≥10K ops/sec
- [ ] 所有中间件单元测试通过
- [ ] RPC 中间件集成正常工作

### 7.2 质量验收

- [ ] `make lint` 0 issues
- [ ] `make test` 全部通过
- [ ] `make test-race` 无竞态
- [ ] CI 全部通过

---

## 八、时间估算

| 任务 | 时间 |
|------|------|
| Phase 1: 覆盖率 | 30 分钟 |
| Phase 2: 基准测试 | 1 小时 |
| Phase 3: RPC 集成 | 30 分钟 |
| Phase 4: 新中间件 | 2 小时 |
| Phase 5: 单元测试 | 1 小时 |
| 验证 & CI | 30 分钟 |
| **总计** | **5.5 小时** |

---

## 九、评审检查清单

### 9.1 架构师评审

- [ ] 技术方案是否合理？
- [ ] 中间件设计是否符合项目架构？
- [ ] 性能目标是否可行？
- [ ] 风险评估是否完整？

### 9.2 安全评审

- [ ] 限流策略是否防止 DoS？
- [ ] 熔断是否防止雪崩？
- [ ] 压缩是否引入安全风险？

---

**文档版本**: v1.0
**创建日期**: 2026-02-21
**最后更新**: 2026-02-21
**维护者**: 🤖 AI Agent
**状态**: 🔄 待架构师评审
