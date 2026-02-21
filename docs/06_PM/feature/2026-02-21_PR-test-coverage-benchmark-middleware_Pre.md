# PR Pre 文档：测试覆盖率 + 性能基准 + 中间件完善

> **PR 类型**: feature
> **分支名称**: feature/test-coverage-benchmark-middleware
> **创建日期**: 2026-02-21
> **作者**: 🤖 AI Agent
> **状态**: 🔄 修订后待评审 (v1.9)

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

#### 2.1.1 覆盖率详细分解（v1.6 修正）

**当前覆盖率分析**（`test/integration/scenarios`）：

| 模块 | 文件 | 当前覆盖率 | 目标覆盖率 | 提升 | 权重 |
|------|------|-----------|-----------|------|------|
| 辅助函数 | `test_helpers.go` | 50% | 70% | +20% | 40% |
| 网络分区 | `network_partition_test.go` | 60% | 75% | +15% | 30% |
| 其他场景 | `*_test.go` | 45% | 60% | +15% | 30% |
| **加权总计** | - | **50%** | **80%** | **+30%** | 100% |

> **说明**：提升百分比按模块权重加权计算：+20%×40% + +15%×30% + +15%×30% = +14%

**关键函数覆盖状态**：

| 函数 | 当前 | 目标 | 策略 |
|------|------|------|------|
| `setupIntegrationTest` | 90.9% | 90.9% | 保持 |
| `setupIntegrationTestWithoutExecutor` | 0.0% | 80% | 新增测试用例 |
| 场景测试函数 | 45% | 70% | 补充边界用例 |

**解决方案**（v1.1 修订）：
保留 `setupIntegrationTestWithoutExecutor` 函数，创建完整的测试场景：

```go
// helpers_test.go
func TestSetupIntegrationTestWithoutExecutor_Basic(t *testing.T) {
    ctx, testCtx := setupIntegrationTestWithoutExecutor(t, DefaultTestTimeout)
    defer testCtx.Close()

    // 验证返回值
    assert.NotNil(t, ctx)
    assert.NotNil(t, testCtx)

    // 验证 context 有效
    deadline, ok := ctx.Deadline()
    assert.True(t, ok)
    assert.True(t, deadline.After(time.Now()))

    // 验证 Registry 已初始化
    assert.NotNil(t, testCtx.Registry)
}

func TestSetupIntegrationTestWithoutExecutor_Timeout(t *testing.T) {
    start := time.Now()
    ctx, _ := setupIntegrationTestWithoutExecutor(t, 5*time.Second)

    // 验证超时设置正确
    deadline, _ := ctx.Deadline()
    assert.WithinDuration(t, start.Add(5*time.Second), deadline, 100*time.Millisecond)
}
```

### 2.2 性能基准测试设计

#### 测试环境（v1.1 新增）

| 环境参数 | 规格 |
|---------|------|
| **CPU** | GitHub Actions ubuntu-latest (2 vCPU) |
| **内存** | 7GB |
| **网络** | 本地回环（无网络延迟） |
| **Go 版本** | 1.23 |

#### 对比基准（v1.1 新增）

| 系统 | 吞吐量（参考） |
|------|---------------|
| libp2p 默认 RPC | ~5K ops/sec |
| gRPC 同等条件 | ~15K ops/sec |
| **目标** | **≥10K ops/sec** |

#### 失败判定（v1.1 新增）

- 连续 3 次运行低于阈值 = 失败
- 方差范围：±10%

#### 基准测试配置（v1.3 新增）

基准测试分两个场景，分别验证无中间件和有中间件的性能：

| 场景 | 中间件 | 说明 | 目标 |
|------|--------|------|------|
| `BenchmarkRPC_Baseline` | ❌ 无 | 纯 RPC 吞吐量，对比参考 | ≥12K ops/sec |
| `BenchmarkRPC_WithMiddleware` | ✅ 有 | 完整中间件链吞吐量 | ≥10K ops/sec |

**中间件链配置**（用于 `BenchmarkRPC_WithMiddleware`）：
- Rate Limiting: 1000 req/s per peer
- Circuit Breaker: 默认配置
- Compression: Snappy, 1KB 阈值

#### 测试场景

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

### 2.4 中间件架构（v1.1 新增）

#### 执行顺序（v1.7 更新：优先级机制 + Send/Receive 双向）

**优先级机制**（v1.7 新增）：

中间件按 `Priority()` 返回值自动排序，数字越小越先执行：

```go
// Middleware 接口
type Middleware interface {
    Name() string
    Priority() int  // 新增：返回优先级
    InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error
    InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error
}

// 优先级常量
const (
    MiddlewarePriorityLogging       = 5  // 日志（最外层）
    MiddlewarePriorityMetrics        = 6  // 指标
    MiddlewarePriorityRateLimit      = 10 // 限流
    MiddlewarePriorityCircuitBreaker = 20 // 熔断
    MiddlewarePriorityCompression    = 30 // 压缩
    MiddlewarePriorityRetry          = 40 // 重试（最内层）
)
```

**关键特性**：
- 无论以何种顺序调用 `chain.Use()`，中间件始终按优先级排序
- 用户无需关心添加顺序，系统自动保证正确执行
- **v1.8 简化 API**：移除 `UseFirst/UseAt` 方法，统一使用 `Use()` + `Priority()` 机制

**MiddlewareChain 接口**（v1.8 简化后）：

```go
type MiddlewareChain interface {
    // Use 添加中间件（自动按 Priority() 排序）
    Use(middleware Middleware) error
    // Remove 移除指定名称的中间件
    Remove(name string) error
    // List 获取所有中间件列表（返回快照）
    List() []Middleware
    // Freeze 冻结中间件链，禁止后续修改
    Freeze()
    // IsFrozen 检查是否已冻结
    IsFrozen() bool
    // ExecuteSend 执行发送中间件链
    ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final SendFunc) error
    // ExecuteReceive 执行接收中间件链（反向执行）
    ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final ReceiveFunc) error
    // Clear 清空所有中间件（冻结后返回错误）
    Clear() error
}
```

**Send 链执行顺序**（出站请求）：

```mermaid
graph LR
    A[请求] --> B[Logging]
    B --> C[Metrics]
    C --> D[Rate Limiting]
    D --> E[Circuit Breaker]
    E --> F[Compression]
    F --> G[Retry]
    G --> H[Transport]
```

**Receive 链执行顺序**（入站请求，v1.7 新增：反向执行）：

```mermaid
graph LR
    A[收到消息] --> B[Retry]
    B --> C[Compression<br/>解压]
    C --> D[Circuit Breaker]
    D --> E[Rate Limiting]
    E --> F[Metrics]
    F --> G[Logging]
    G --> H[业务处理]
```

**反向执行说明**（v1.7）：

| 链 | 执行顺序 | 原因 |
|----|---------|------|
| **Send** | Logging → ... → Retry → Transport | 日志记录完整生命周期，Retry 在最内层处理网络错误 |
| **Receive** | Retry → ... → Logging → 业务 | **反向执行**：先解压(Compression)，再处理业务逻辑 |

**中间件顺序说明**（v1.7 更新）：

| 顺序 | 中间件 | 优先级 | 职责 | 理由 |
|------|--------|--------|------|------|
| 1 | Logging | 5 | 记录请求开始/结束 | 最外层，完整记录生命周期 |
| 2 | Metrics | 6 | 采集延迟/成功率 | 在 Logging 后，记录完整耗时 |
| 3 | RateLimit | 10 | 节点级限流 | 保护系统，避免过载 |
| 4 | CircuitBreaker | 20 | 熔断保护 | 快速失败，防止雪崩 |
| 5 | Compression | 30 | 压缩（>1KB） | 在 Retry 之前，避免重复压缩 |
| 6 | Retry | 40 | 失败重试 | **最内层**，仅重试网络错误 |

#### 配置方式（代码配置）

```go
// 代码配置示例
rpc := NewLibp2pRPC(config)

// 添加限流中间件（按节点限流）
rpc.Use(NewRateLimitMiddleware(RateLimitConfig{
    RequestsPerSecond: 1000,
    Burst:             100,
}))

// 添加熔断中间件
rpc.Use(NewCircuitBreakerMiddleware(CircuitBreakerConfig{
    FailureThreshold: 5,
    ResetTimeout:     30 * time.Second,
}))

// 添加压缩中间件
rpc.Use(NewCompressionMiddleware(CompressionConfig{
    Algorithm: Snappy,
    Threshold: 1024, // 1KB
}))
```

### 2.5 新中间件实现

#### 2.5.1 Rate Limiting 中间件（v1.8 修订：简化锁机制）

```go
type RateLimitMiddleware struct {
    limiters sync.Map // peer.ID -> *rate.Limiter（v1.8: 移除冗余 mu 锁）
    config   RateLimitConfig
}

type RateLimitConfig struct {
    RequestsPerSecond int
    Burst             int
}

func (m *RateLimitMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error {
    limiter := m.getLimiter(peer)
    if !limiter.Allow() {
        return errors.Wrap(errors.ErrRateLimitExceeded, "rate limit for peer "+string(peer))
    }
    return next(ctx, peer, msg)
}

func (m *RateLimitMiddleware) getLimiter(peer model.PeerID) *rate.Limiter {
    if limiter, ok := m.limiters.Load(peer); ok {
        return limiter.(*rate.Limiter)
    }

    // v1.8: 使用 LoadOrStore 替代 double-check locking，更简洁高效
    newLimiter := rate.NewLimiter(rate.Limit(m.config.RequestsPerSecond), m.config.Burst)
    actual, _ := m.limiters.LoadOrStore(peer, newLimiter)
    return actual.(*rate.Limiter)
}
```

#### 2.5.2 Circuit Breaker 中间件（v1.4 修订：使用 gobreaker）

**选型说明**：使用成熟的 `github.com/sony/gobreaker` 库，避免重复造轮子，状态机实现经过充分验证。

**依赖**：
```go
import "github.com/sony/gobreaker"
```

**状态机设计**（gobreaker 内置）：

```mermaid
stateDiagram-v2
    [*] --> Closed
    Closed --> Open: 失败次数 >= 阈值
    Open --> HalfOpen: 超时后
    HalfOpen --> Closed: 成功请求
    HalfOpen --> Open: 任何失败
```

**实现代码**：

```go
package transport

import (
    "context"
    "sync"
    "time"

    "github.com/sony/gobreaker"
    "github.com/jzhang405/NexKV/pkg/errors"
    "github.com/jzhang405/NexKV/pkg/model"
)

// CircuitBreakerMiddleware 熔断中间件（基于 gobreaker）
type CircuitBreakerMiddleware struct {
    breakers sync.Map // peer.ID -> *gobreaker.CircuitBreaker
    config   CircuitBreakerConfig
}

// CircuitBreakerConfig 熔断配置
type CircuitBreakerConfig struct {
    Name          string        // 熔断器名称（用于日志和指标）
    MaxRequests   uint32        // HalfOpen 状态最大请求数（默认 1）
    Interval      time.Duration // 统计窗口（默认 0，持续统计）
    Timeout       time.Duration // Open → HalfOpen 超时（默认 30s）
    ReadyToTrip   func(counts gobreaker.Counts) bool // 触发熔断条件
    OnStateChange func(name string, from, to gobreaker.State) // 状态变更回调
}

// DefaultCircuitBreakerConfig 默认配置
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
    return CircuitBreakerConfig{
        Name:        "rpc-circuit-breaker",
        MaxRequests: 3,  // HalfOpen 状态允许 3 个请求
        Timeout:     30 * time.Second,
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            // 连续 5 次失败或失败率 > 50%（至少 10 个请求）
            failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
            return counts.Requests >= 10 && failureRatio >= 0.5 || counts.ConsecutiveFailures >= 5
        },
    }
}

// NewCircuitBreakerMiddleware 创建熔断中间件
func NewCircuitBreakerMiddleware(config CircuitBreakerConfig) *CircuitBreakerMiddleware {
    if config.ReadyToTrip == nil {
        config.ReadyToTrip = DefaultCircuitBreakerConfig().ReadyToTrip
    }
    return &CircuitBreakerMiddleware{config: config}
}

// InterceptSend 拦截发送请求
func (m *CircuitBreakerMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error {
    breaker := m.getBreaker(peer)

    _, err := breaker.Execute(func() (interface{}, error) {
        return nil, next(ctx, peer, msg)
    })

    if err == gobreaker.ErrOpenState {
        return errors.Wrap(errors.ErrCircuitBreakerOpen, "circuit breaker is open for peer "+string(peer))
    }
    if err == gobreaker.ErrTooManyRequests {
        return errors.Wrap(errors.ErrCircuitBreakerOpen, "too many requests in half-open state")
    }

    return err
}

// getBreaker 获取或创建节点的熔断器
func (m *CircuitBreakerMiddleware) getBreaker(peer model.PeerID) *gobreaker.CircuitBreaker {
    if breaker, ok := m.breakers.Load(peer); ok {
        return breaker.(*gobreaker.CircuitBreaker)
    }

    // 为每个节点创建独立的熔断器
    st := gobreaker.Settings{
        Name:        m.config.Name + "-" + string(peer),
        MaxRequests: m.config.MaxRequests,
        Interval:    m.config.Interval,
        Timeout:     m.config.Timeout,
        ReadyToTrip: m.config.ReadyToTrip,
        OnStateChange: func(name string, from, to gobreaker.State) {
            if m.config.OnStateChange != nil {
                m.config.OnStateChange(name, from, to)
            }
            // 可选：记录状态变更到 Metrics
            // metrics.RecordCircuitBreakerState(name, from.String(), to.String())
        },
    }

    breaker := gobreaker.NewCircuitBreaker(st)
    actual, _ := m.breakers.LoadOrStore(peer, breaker)
    return actual.(*gobreaker.CircuitBreaker)
}
```

**关键特性**：

| 特性 | 说明 |
|------|------|
| **按节点熔断** | 每个 PeerID 独立熔断器，单节点故障不影响其他节点 |
| **不持久化** | 重启后所有熔断器重置为 Closed 状态 |
| **状态回调** | 支持 `OnStateChange` 回调，便于日志和监控 |
| **灵活触发** | 可自定义 `ReadyToTrip` 函数，支持失败率/连续失败等策略 |

**gobreaker 状态常量**：

```go
const (
    StateClosed gobreaker.State = iota
    StateHalfOpen
    StateOpen
)
```

#### 2.5.3 Retry 中间件（v1.5 新增：使用 retry-go）

**选型说明**：使用 `github.com/avast/retry-go/v4`，支持灵活配置、指数退避、熔断联动。

**依赖**：
```go
import "github.com/avast/retry-go/v4"
```

**实现代码**：

```go
package transport

import (
    "context"
    "errors"
    "time"

    "github.com/avast/retry-go/v4"
    "github.com/jzhang405/NexKV/pkg/model"
)

// RetryMiddleware 重试中间件
type RetryMiddleware struct {
    config RetryConfig
}

// RetryConfig 重试配置
type RetryConfig struct {
    MaxAttempts     int           // 最大重试次数（默认 3）
    InitialDelay    time.Duration // 初始延迟（默认 100ms）
    MaxDelay        time.Duration // 最大延迟（默认 5s）
    RetryOn         func(error) bool // 判断是否可重试的错误
    OnRetry         func(n uint, err error) // 重试回调（用于日志）
    DelayType       retry.DelayTypeFunc // 延迟类型（默认指数退避）
}

// DefaultRetryConfig 默认配置
func DefaultRetryConfig() RetryConfig {
    return RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     5 * time.Second,
        RetryOn:      isRetryableError,
        DelayType:    retry.BackOffDelay,
    }
}

// isRetryableError 判断是否为可重试错误
func isRetryableError(err error) bool {
    if err == nil {
        return false
    }
    // 网络错误、超时错误可重试
    // 业务错误、熔断错误不可重试
    var netErr net.Error
    if errors.As(err, &netErr) {
        return true
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return true
    }
    if errors.Is(err, context.Canceled) {
        return false // 用户取消，不重试
    }
    // 熔断器打开时不重试
    if errors.Is(err, errors.ErrCircuitBreakerOpen) {
        return false
    }
    // 限流错误不重试
    if errors.Is(err, errors.ErrRateLimitExceeded) {
        return false
    }
    return false
}

// NewRetryMiddleware 创建重试中间件
func NewRetryMiddleware(config RetryConfig) *RetryMiddleware {
    if config.MaxAttempts == 0 {
        config.MaxAttempts = DefaultRetryConfig().MaxAttempts
    }
    if config.InitialDelay == 0 {
        config.InitialDelay = DefaultRetryConfig().InitialDelay
    }
    return &RetryMiddleware{config: config}
}

// InterceptSend 拦截发送请求
func (m *RetryMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error {
    var lastErr error

    opts := []retry.Option{
        retry.Attempts(uint(m.config.MaxAttempts)),
        retry.Delay(m.config.InitialDelay),
        retry.MaxDelay(m.config.MaxDelay),
        retry.DelayType(m.config.DelayType),
        retry.Context(ctx),
    }

    if m.config.RetryOn != nil {
        opts = append(opts, retry.RetryIf(func(err error) bool {
            return m.config.RetryOn(err)
        }))
    }

    if m.config.OnRetry != nil {
        opts = append(opts, retry.OnRetry(m.config.OnRetry))
    }

    err := retry.Do(func() error {
        lastErr = next(ctx, peer, msg)
        return lastErr
    }, opts...)

    return err
}
```

**关键特性**：

| 特性 | 说明 |
|------|------|
| **指数退避** | 默认 BackOffDelay，避免重试风暴 |
| **可重试判断** | 仅网络错误、超时错误重试，业务错误/熔断错误不重试 |
| **Context 支持** | 支持取消重试，避免资源浪费 |
| **重试回调** | 支持 `OnRetry` 回调，便于日志记录 |

**与熔断器联动**：
- Retry 在 CircuitBreaker 之后执行
- 熔断器 Open 时，Retry 不重试（`ErrCircuitBreakerOpen` 不可重试）
- 避免重试请求"欺骗"熔断器统计

#### 2.5.4 Compression 中间件（v1.3 修订：明确依赖）

**依赖说明**：

本项目使用 **本地封装的 Compressor 接口**，位于 `pkg/compressor/compressor.go`，支持多种压缩算法：

```go
// pkg/compressor/compressor.go（已存在）
type Compressor interface {
    Compress(data []byte) ([]byte, error)
    Decompress(data []byte) ([]byte, error)
    DecompressWithLimit(data []byte, maxBytes int) ([]byte, error) // v1.8 新增：压缩炸弹保护
}

type CompressorType string

const (
    Snappy CompressorType = "snappy"
    ZSTD   CompressorType = "zstd"
    LZ4    CompressorType = "lz4"
    None   CompressorType = "none"
)
```

**外部依赖**（`go.mod` 新增）：

```go
require (
    github.com/golang/snappy v0.0.4  // Snappy 压缩（默认）
    github.com/pierrec/lz4/v4 v4.1.21 // LZ4 压缩（可选）
    github.com/klauspost/compress v1.17.8 // ZSTD 压缩（可选）
)
```

**中间件实现**：

```go
type CompressionMiddleware struct {
    compressor compressor.Compressor
    algorithm  compressor.CompressorType
    threshold  int // 最小压缩阈值（字节）
}

type CompressionConfig struct {
    Algorithm compressor.CompressorType // 可选: Snappy（默认）, ZSTD, LZ4, None
    Threshold int                       // 默认 1024 字节（1KB）
}

func NewCompressionMiddleware(config CompressionConfig) *CompressionMiddleware {
    if config.Algorithm == "" {
        config.Algorithm = compressor.Snappy // 默认使用 Snappy
    }
    if config.Threshold == 0 {
        config.Threshold = 1024 // 默认 1KB
    }

    return &CompressionMiddleware{
        compressor: compressor.New(config.Algorithm),
        algorithm:  config.Algorithm,
        threshold:  config.Threshold,
    }
}

func (m *CompressionMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error {
    if len(msg.Payload) > m.threshold {
        compressed, err := m.compressor.Compress(msg.Payload)
        if err == nil {
            msg.Payload = compressed
            if msg.Metadata == nil {
                msg.Metadata = make(map[string]string)
            }
            msg.Metadata["compression"] = string(m.algorithm)
        }
    }
    return next(ctx, peer, msg)
}

func (m *CompressionMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error {
    if algo, ok := msg.Metadata["compression"]; ok {
        comp := compressor.New(compressor.CompressorType(algo))
        // v1.8: 使用 DecompressWithLimit 防止压缩炸弹攻击
        decompressed, err := comp.DecompressWithLimit(msg.Payload, compressor.DefaultMaxDecompressedSize)
        if err == nil {
            msg.Payload = decompressed
        }
    }
    return next(ctx, peer, msg)
}
```

**压缩炸弹保护（v1.8 新增）**：

| 保护机制 | 说明 |
|---------|------|
| `DecompressWithLimit` | 所有压缩器实现此方法，使用 `io.LimitReader` 限制解压大小 |
| `DefaultMaxDecompressedSize` | 默认 10MB，超过此限制返回 `ErrDecompressionTooBig` |
| 流式解压 | LZ4/Snappy 使用流式 API，避免一次性加载大文件 |

**算法性能对比**：

| 算法 | 压缩比 | 压缩速度 | 解压速度 | 适用场景 |
|------|--------|---------|---------|---------|
| **Snappy** | 2.0x | 250 MB/s | 500 MB/s | **默认推荐**，平衡压缩比和速度 |
| LZ4 | 2.1x | 400 MB/s | 1000 MB/s | 高吞吐场景 |
| ZSTD | 2.8x | 150 MB/s | 400 MB/s | 高压缩比场景 |
| None | 1.0x | - | - | 禁用压缩 |

---

## 三、文件变更清单

### 3.1 新增文件

| 文件 | 说明 |
|------|------|
| `test/integration/scenarios/helpers_test.go` | 辅助函数单元测试 |
| `internal/infrastructure/transport/benchmark_test.go` | 性能基准测试 |
| `internal/infrastructure/transport/rate_limit_middleware.go` | 限流中间件 |
| `internal/infrastructure/transport/circuit_breaker_middleware.go` | 熔断中间件 |
| `internal/infrastructure/transport/retry_middleware.go` | 重试中间件（v1.5 新增） |
| `internal/infrastructure/transport/compression_middleware.go` | 压缩中间件 |
| `internal/infrastructure/transport/middleware_builder.go` | 中间件构建器（v1.7 新增，配置化构建） |
| `internal/infrastructure/transport/middleware_helpers.go` | 共享工具函数（v1.7 新增） |
| `internal/infrastructure/transport/rate_limit_middleware_test.go` | 限流测试 |
| `internal/infrastructure/transport/circuit_breaker_middleware_test.go` | 熔断测试 |
| `internal/infrastructure/transport/retry_middleware_test.go` | 重试测试（v1.5 新增） |
| `internal/infrastructure/transport/compression_middleware_test.go` | 压缩测试 |
| `pkg/compressor/compressor.go` | 压缩接口封装 |
| `pkg/compressor/snappy.go` | Snappy 实现 |
| `pkg/compressor/lz4.go` | LZ4 实现（v1.7 新增） |
| `pkg/compressor/zstd.go` | ZSTD 实现（v1.7 新增） |

### 3.2 修改文件

| 文件 | 修改内容 |
|------|---------|
| `internal/domain/service/transport.go` | 新增 `Priority()` 方法，优先级常量（v1.7），移除 `UseFirst/UseAt`（v1.8） |
| `internal/infrastructure/transport/middleware_chain.go` | 自动按优先级排序，Receive 链反向执行（v1.7），移除 `UseFirst/UseAt` 实现（v1.8） |
| `internal/infrastructure/transport/middleware_rate_limit.go` | 移除冗余 `mu` 锁，使用 `LoadOrStore`（v1.8） |
| `internal/infrastructure/transport/metrics_middleware.go` | 添加 nil 消息保护（v1.8） |
| `internal/infrastructure/transport/middleware_compression.go` | 使用 `DecompressWithLimit` 防止压缩炸弹（v1.8） |
| `internal/infrastructure/transport/middleware_test.go` | 移除 `UseFirst/UseAt` 测试（v1.8） |
| `pkg/compressor/compressor.go` | 新增 `DecompressWithLimit` 接口方法（v1.8） |
| `pkg/compressor/snappy.go` | 流式 API + `io.LimitReader`（v1.8） |
| `pkg/compressor/lz4.go` | `io.LimitReader` 压缩炸弹保护（v1.8） |
| `pkg/compressor/zstd.go` | 大小检查保护（v1.8） |
| `pkg/compressor/none.go` | 添加大小检查（v1.8） |
| `pkg/errors/errors.go` | 添加 `ErrDecompressionTooBig`（v1.8） |
| `internal/infrastructure/transport/libp2p_rpc.go` | 集成中间件链 |
| `go.mod` | 添加中间件依赖（v1.5） |

---

## 四、依赖变更

### 4.1 新增依赖（v1.5 更新）

```go
require (
    // 中间件核心依赖
    golang.org/x/time v0.5.0              // rate limiter（令牌桶限流）
    github.com/sony/gobreaker v0.5.0      // Circuit Breaker（熔断器）
    github.com/avast/retry-go/v4 v4.6.0   // Retry（重试机制）

    // 压缩依赖
    github.com/golang/snappy v0.0.4       // Snappy 压缩（默认算法）
    github.com/pierrec/lz4/v4 v4.1.21     // LZ4 压缩（可选）
    github.com/klauspost/compress v1.17.8 // ZSTD 压缩（可选）
)
```

**兼容性检查**：与现有 `go.mod` 中的版本兼容，无需 `replace` 指令。

### 4.2 组件选型说明（v1.5 更新）

| 组件 | 推荐库 | 核心优势 | 适配要点 |
|------|--------|---------|---------|
| **RateLimit** | `golang.org/x/time/rate` | 官方、轻量、令牌桶算法 | 节点级限流，联动 Metrics 记录限流次数 |
| **CircuitBreaker** | `github.com/sony/gobreaker` | 状态机完整、测试友好 | 不持久化状态，重启重置为 Closed |
| **Retry** | `github.com/avast/retry-go/v4` | 灵活配置、指数退避、熔断联动 | 仅重试临时错误，熔断器 Open 时跳过重试 |
| **Compression** | `snappy` + `lz4` + `zstd` | 多算法支持、可配置 | 默认 Snappy，支持运行时切换 |
| **Logging** | `zap` + `lumberjack`（已有） | 高性能、结构化、日志轮转 | 记录 PeerID/中间件状态等上下文 |
| **Metrics** | `prometheus/client_golang`（已有） | 云原生、多维度指标 | 按 peer_id 拆分限流/熔断指标 |

### 4.3 依赖用途说明

| 依赖 | 用途 | 必需性 |
|------|------|--------|
| `golang.org/x/time/rate` | 令牌桶限流算法 | **必需** |
| `github.com/sony/gobreaker` | 熔断器实现 | **必需** |
| `github.com/avast/retry-go/v4` | 重试机制 | **必需** |
| `github.com/golang/snappy` | 默认压缩算法 | **必需** |
| `github.com/pierrec/lz4/v4` | 可选压缩算法 | 可选 |
| `github.com/klauspost/compress` | ZSTD 压缩算法 | 可选 |

---

## 五、风险评估

### 5.1 风险矩阵（v1.1 更新）

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|---------|
| 性能基准不达标 | 中 | 高 | 优化序列化、连接池 |
| 中间件集成破坏现有功能 | 低 | 高 | 完整单元测试覆盖 |
| 压缩中间件性能开销 | 中 | 中 | 设置合理阈值，跳过小消息 |
| **中间件增加延迟**（新增） | 高 | 中 | 1. 简化中间件逻辑 2. 使用原子操作 |
| **HalfOpen 状态抖动**（新增） | 中 | 中 | 添加最小恢复时间窗口 |
| **压缩阈值不合理**（新增） | 中 | 低 | 支持运行时通过 API 调整 |

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
- [ ] `retry_middleware_test.go`：重试测试（v1.5 新增）
- [ ] `compression_middleware_test.go`：压缩测试
- [ ] `middleware_chain_test.go`：中间件链测试（v1.7 新增）
  - [x] `TestMiddlewareChain_PriorityOrdering`：优先级排序测试
  - [x] `TestMiddlewareChain_PriorityOrderingWithRealMiddleware`：真实中间件优先级测试
  - [x] `TestMiddlewareChain_ReceiveReverseOrder`：Receive 链反向执行测试
- [ ] `compressor/*_test.go`：压缩炸弹保护测试（v1.8 新增）
  - [x] `TestSnappy_DecompressWithLimit`：Snappy 压缩炸弹保护
  - [x] `TestLZ4_DecompressWithLimit`：LZ4 压缩炸弹保护
  - [x] `TestZSTD_DecompressWithLimit`：ZSTD 压缩炸弹保护

### 6.2 基准测试

```bash
# 运行命令
go test -bench=. -benchtime=10s -count=3 ./internal/infrastructure/transport/...
```

- [ ] 单机吞吐量 ≥10K ops/sec
- [ ] 并发吞吐量 ≥10K ops/sec
- [ ] 不同负载大小性能对比
- [ ] 方差范围 ±10%

### 6.3 集成测试

- [ ] 中间件链完整执行
- [ ] 限流触发正确
- [ ] 熔断状态转换正确
- [ ] 压缩/解压缩正确
- [ ] 多中间件组合测试（v1.1 新增）

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

### 7.3 文档验收（v1.1 新增）

- [ ] 更新 README 中间件使用说明
- [ ] 更新 API 文档

### 7.4 向后兼容性（v1.1 新增）

- [ ] 现有代码无需修改即可编译
- [ ] 现有测试全部通过

---

## 八、时间估算（v1.1 调整）

| 任务 | 原估算 | 调整后 |
|------|--------|--------|
| Phase 1: 覆盖率 | 30 分钟 | 30 分钟 |
| Phase 2: 基准测试 | 1 小时 | 1 小时 |
| Phase 3: RPC 集成 | 30 分钟 | 30 分钟 |
| Phase 4: 新中间件 | 2 小时 | **3.5 小时** |
| Phase 5: 单元测试 | 1 小时 | **2 小时** |
| 验证 & CI | 30 分钟 | **1 小时** |
| **总计** | 5.5 小时 | **8.5 小时** |

---

## 九、评审检查清单

### 9.1 架构师评审（v1.3 更新）

- [x] 技术方案是否合理？
- [x] 中间件设计是否符合项目架构？
- [x] 性能目标是否可行？（v1.3：已明确双场景配置）
- [x] 风险评估是否完整？
- [x] 覆盖率方案是否明确？（v1.3：已细化 50%→80% 分解）
- [x] 配置方式是否说明？（v1.1）
- [x] 中间件依赖是否明确？（v1.3：压缩库依赖说明）

### 9.2 安全评审（v1.3 已完成）

- [x] **限流策略是否防止 DoS？**
  - ✅ 按 Peer 限流，单节点最多 `RequestsPerSecond + Burst` 请求/秒
  - ✅ 超限返回 `ErrRateLimitExceeded`，快速失败
  - ✅ 防止单个恶意节点消耗过多资源

- [x] **熔断是否防止雪崩？**
  - ✅ 失败达到阈值自动熔断，阻止请求继续发送
  - ✅ HalfOpen 机制允许逐步恢复，避免瞬间流量冲击
  - ✅ 不持久化，重启后重置，避免残留错误状态

- [x] **压缩是否引入安全风险？**
  - ✅ 使用成熟的开源库（snappy、lz4、zstd），无已知安全漏洞
  - ✅ 压缩算法通过 Metadata 协商，不支持动态加载外部算法
  - ✅ 阈值限制（默认 1KB），小消息不压缩，防止压缩炸弹
  - ✅ **解压后数据大小限制（v1.8 强化）**：
    - `DecompressWithLimit` 方法使用 `io.LimitReader` 流式限制
    - 默认最大解压大小 10MB（`DefaultMaxDecompressedSize`）
    - 超过限制返回 `ErrDecompressionTooBig` 错误
    - 所有压缩器（Snappy/LZ4/ZSTD/None）均实现此保护

---

## 十、修订记录

| 版本 | 日期 | 修订内容 |
|------|------|---------|
| v1.0 | 2026-02-21 | 初始版本 |
| v1.1 | 2026-02-21 | 响应架构师评审 v1：补充覆盖率方案、性能环境规格、中间件架构图、配置方式、风险评估、调整时间估算 |
| v1.2 | 2026-02-21 | 响应架构师评审 v2：补充限流模式（PerPeer+Global）、熔断按节点状态、压缩算法可选 |
| v1.3 | 2026-02-21 | 响应架构师评审 v3：(1) 覆盖率详细分解 50%→80% 路径 (2) HalfOpen 完整状态机逻辑 (3) 压缩依赖说明 (4) 基准测试双场景配置 (5) 安全评审完成 |
| v1.4 | 2026-02-21 | 响应架构师组件推荐：(1) 采用 `sony/gobreaker` 替代自研熔断器 (2) 新增组件选型说明表 (3) 更新依赖清单 |
| v1.5 | 2026-02-21 | 响应架构师最终建议：(1) 新增 Retry 中间件（`avast/retry-go/v4`） (2) 调整中间件执行顺序（Retry 在 CircuitBreaker 之后） (3) 确认指标命名前缀 `nexkv_transport_` |
| v1.6 | 2026-02-21 | 修正覆盖率分解表数学问题（加权计算），确认中间件顺序正确 |
| v1.7 | 2026-02-21 | Code Review 后改进：(1) 新增优先级机制 `Priority()` 自动排序 (2) Receive 链反向执行 (3) Compression 在 Retry 之前 (4) 新增优先级排序测试 |
| v1.8 | 2026-02-21 | Code Review v2 改进：(1) **移除 UseFirst/UseAt 方法**（简化 API） (2) **压缩炸弹保护** `DecompressWithLimit` (3) RateLimit 移除冗余锁 (4) Metrics nil 消息保护 (5) 新增 `ErrDecompressionTooBig` 错误 |
| v1.9 | 2026-02-21 | 安全审查修复：**P1-1 RateLimit 配置上限验证**（MaxRequestsPerSecond=10万, MaxBurst=1万）。P1-2 节点数量限制延迟到下一版本。 |

---

**文档版本**: v1.8
**创建日期**: 2026-02-21
**最后更新**: 2026-02-21
**维护者**: 🤖 AI Agent
**状态**: 🔄 修订后待架构师评审
