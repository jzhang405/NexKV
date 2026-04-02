# Spike: 中间件系统 P2 优化项

> **状态**: 待实施
> **优先级**: P2（可选优化）
> **创建日期**: 2026-02-21
> **来源**: PR #82 Code Review + Security Review + Architecture Review

---

## 背景

在 PR #82（测试覆盖率 + 性能基准 + 中间件完善）的多路大神 Agent 审查中，发现了一些 P2 级别的优化项。这些优化项不阻塞当前 PR 合并，但建议在后续版本中实施以提升系统质量。

---

## 优化项清单

### 1. 统一配置管理

**当前问题**：
- 每个中间件独立配置结构
- 缺少统一配置入口
- 无法从配置文件一次性加载所有中间件

**建议方案**：

```go
// 统一中间件配置
type MiddlewareConfig struct {
    // 全局开关
    Enabled []string `yaml:"enabled"` // 启用的中间件列表

    // 各中间件配置
    RateLimit      RateLimitConfig      `yaml:"rateLimit"`
    CircuitBreaker CircuitBreakerConfig `yaml:"circuitBreaker"`
    Retry          RetryConfig          `yaml:"retry"`
    Compression    CompressionConfig    `yaml:"compression"`
}

// 从配置文件加载
func LoadMiddlewareConfig(path string) (*MiddlewareConfig, error) {
    // YAML/JSON 解析
}

// 批量创建中间件
func CreateMiddlewares(config *MiddlewareConfig) ([]service.Middleware, error) {
    var middlewares []service.Middleware
    for _, name := range config.Enabled {
        switch name {
        case "rateLimit":
            middlewares = append(middlewares, NewRateLimitMiddleware(config.RateLimit))
        case "circuitBreaker":
            middlewares = append(middlewares, NewCircuitBreakerMiddleware(config.CircuitBreaker))
        // ...
        }
    }
    return middlewares, nil
}
```

**预期收益**：
- 简化配置管理
- 支持配置文件热加载
- 便于环境差异化配置

**预估工作量**: 2-3 天

---

### 2. 中间件集成测试

**当前问题**：
- 缺少中间件顺序交互测试
- 缺少故障注入测试
- 未测试 RateLimit → CircuitBreaker → Compression → Retry 的实际交互

**建议方案**：

```go
// 中间件顺序集成测试
func TestMiddlewareChain_Integration_Order(t *testing.T) {
    chain := NewMiddlewareChain()
    chain.Use(NewRateLimitMiddleware(...))
    chain.Use(NewCircuitBreakerMiddleware(...))
    chain.Use(NewCompressionMiddleware(...))
    chain.Use(NewRetryMiddleware(...))

    // 场景 1: 限流不触发重试
    t.Run("RateLimitDoesNotTriggerRetry", func(t *testing.T) {
        // 模拟限流
        // 验证：不触发重试（Retry 检测到 ErrRateLimitExceeded）
    })

    // 场景 2: 熔断打开后不压缩
    t.Run("CircuitBreakerOpenSkipsCompression", func(t *testing.T) {
        // 触发熔断
        // 验证：直接返回错误，不执行压缩
    })

    // 场景 3: 压缩失败可重试
    t.Run("CompressionFailureTriggersRetry", func(t *testing.T) {
        // 模拟压缩失败
        // 验证：触发重试（如果错误可重试）
    })
}

// 故障注入测试
func TestMiddlewareChain_FaultInjection(t *testing.T) {
    // 模拟网络故障
    // 模拟超时
    // 模拟节点不可达
}
```

**预期收益**：
- 验证中间件顺序正确性
- 发现潜在的交互问题
- 提升系统可靠性

**预估工作量**: 2 天

---

### 3. 压缩算法协商机制

**当前问题**：
- 接收端必须支持与发送端相同的压缩算法
- 缺少算法协商机制
- 可能导致兼容性问题

**建议方案**：

```go
// 压缩算法协商器
type CompressionNegotiator struct {
    supportedAlgorithms []compressor.CompressorType
    preferredAlgorithm  compressor.CompressorType
}

// 握手阶段协商
func (n *CompressionNegotiator) Negotiate(peerAlgorithms []compressor.CompressorType) compressor.CompressorType {
    // 选择双方都支持的最优算法
    for _, algo := range n.supportedAlgorithms {
        if contains(peerAlgorithms, algo) {
            return algo
        }
    }
    return compressor.None // 不支持任何算法，不压缩
}

// 节点能力广播
type NodeCapabilities struct {
    PeerID              model.PeerID
    CompressionAlgorithms []compressor.CompressorType
}
```

**预期收益**：
- 支持异构节点
- 优雅降级
- 提升兼容性

**预估工作量**: 3 天

---

### 4. 中间件依赖声明机制

**当前问题**：
- 缺少中间件优先级机制
- 缺少中间件依赖声明
- 需要手动排序

**建议方案**：

```go
// 增强中间件接口
type Middleware interface {
    Name() string
    InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error
    InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error

    // 新增：声明依赖
    Dependencies() []string  // 返回依赖的中间件名称
    // 新增：优先级（数字越小越先执行）
    Priority() int
    // 生命周期
    Init() error
    Shutdown() error
}

// 自动排序
func (c *middlewareChain) autoSort() {
    sort.Slice(c.middlewares, func(i, j int) bool {
        return c.middlewares[i].Priority() < c.middlewares[j].Priority()
    })
}
```

**预期收益**：
- 自动推导顺序
- 防止顺序错误
- 简化配置

**预估工作量**: 2 天

---

### 5. 全局熔断机制

**当前问题**：
- 仅支持 Per-Peer 熔断
- 缺少全局熔断开关
- 无法应对大规模分区故障

**建议方案**：

```go
type CircuitBreakerMiddleware struct {
    breakers      sync.Map
    globalBreaker *gobreaker.CircuitBreaker // 新增：全局熔断器
    config        CircuitBreakerConfig
}

func (m *CircuitBreakerMiddleware) InterceptSend(...) error {
    // 先检查全局熔断
    if m.globalBreaker.State() == gobreaker.StateOpen {
        return errors.ErrGlobalCircuitBreakerOpen
    }
    // 再检查 Per-Peer 熔断
    breaker := m.getBreaker(peer)
    // ...
}
```

**预期收益**：
- 全局故障保护
- 快速失败
- 资源保护

**预估工作量**: 1 天

---

### 6. 监控指标增强

**当前问题**：
- 缺少中间件级别的 Prometheus 指标
- 难以监控系统健康状态

**建议方案**：

```go
// 中间件指标
var (
    rateLimitExceededTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "nexkv",
            Subsystem: "middleware",
            Name:      "rate_limit_exceeded_total",
            Help:      "Total number of rate limit exceeded events",
        },
        []string{"peer"},
    )

    circuitBreakerState = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: "nexkv",
            Subsystem: "middleware",
            Name:      "circuit_breaker_state",
            Help:      "Circuit breaker state (0=closed, 1=open, 2=half-open)",
        },
        []string{"peer"},
    )

    compressionRatio = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "nexkv",
            Subsystem: "middleware",
            Name:      "compression_ratio",
            Help:      "Compression ratio",
            Buckets:   []float64{0.1, 0.2, 0.3, 0.5, 0.7, 0.9},
        },
        []string{"algorithm"},
    )
)
```

**预期收益**：
- 实时监控
- 故障预警
- 性能分析

**预估工作量**: 2 天

---

## 实施建议

### 优先级排序

| 优先级 | 优化项 | 预估工作量 | 建议版本 |
|--------|--------|-----------|---------|
| P2.1 | 中间件集成测试 | 2 天 | v1.1 |
| P2.2 | 统一配置管理 | 2-3 天 | v1.1 |
| P2.3 | 监控指标增强 | 2 天 | v1.1 |
| P2.4 | 全局熔断机制 | 1 天 | v1.2 |
| P2.5 | 压缩算法协商 | 3 天 | v1.2 |
| P2.6 | 中间件依赖声明 | 2 天 | v1.2 |

### 依赖关系

```
P2.2 (统一配置) ──┐
                 ├──> P2.1 (集成测试)
P2.4 (全局熔断) ──┘

P2.5 (算法协商) ──> P2.3 (监控指标)
```

---

## 决策记录

- **2026-02-21**: 创建 Spike 文档，记录 P2 优化项
- **待定**: 确定实施计划和版本安排

---

## 参考文档

- [PR #82 Pre 文档](../06_PM/feature/2026-02-21_PR-test-coverage-benchmark-middleware_Pre.md)
- [代码审查报告](../09_code-review/)
- [安全审查报告](../09_code-review/)

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-02-21
