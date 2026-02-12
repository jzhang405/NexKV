# RPC 性能调优指南

> 针对 NexKV RPC 模块的性能优化建议

## 概述

本文档提供了 RPC 模块的性能调优指南，帮助您根据实际场景优化 RPC 性能。

## 性能指标

### 关键指标

| 指标 | 说明 | 目标值 |
|------|------|--------|
| **调用延迟 (P99)** | 99% 的调用延迟 | < 50ms |
| **吞吐量** | 每秒处理请求数 | > 1000 req/s |
| **Stream 复用率** | Stream 缓存命中率 | > 80% |
| **连接池效率** | 活跃 Stream / 总 Stream | > 70% |

## 性能优化策略

### 1. 连接池优化

#### 启用连接池

连接池默认启用，确保不要禁用：

```go
// 推荐配置
client := rpc.NewClient(host)
// 连接池默认启用

// 不推荐：禁用连接池
// client.enablePool = false
```

#### 调整 Stream 池大小

根据并发量调整 `MaxStreams`：

```go
import "github.com/jzhang405/NexKV/internal/rpc"

// 低并发场景
pool := rpc.NewConnectionPool(&rpc.PoolConfig{
    MaxStreams:  5,     // 每个 peer 5 个 Stream
    StreamTTL:   5 * time.Minute,
})

// 高并发场景
pool := rpc.NewConnectionPool(&rpc.PoolConfig{
    MaxStreams:  20,    // 每个 peer 20 个 Stream
    StreamTTL:   10 * time.Minute,
})
```

#### 调整 Stream TTL

根据网络稳定性调整 `StreamTTL`：

- **稳定网络**：5-10 分钟
- **不稳定网络**：2-3 分钟
- **高动态环境**：1 分钟

### 2. 批量调用优化

#### 使用批量调用

批量调用可以减少网络往返：

```go
// 不推荐：串行调用
for _, key := range keys {
    resp, err := client.Call(ctx, peerID, "Get", []byte(key))
    // 处理响应
}

// 推荐：批量调用
reqs := make([]rpc.BatchRequest, len(keys))
for i, key := range keys {
    reqs[i] = rpc.BatchRequest{
        Method: "Get",
        Body:   []byte(key),
    }
}
result := client.CallParallel(ctx, peerID, reqs, nil)

for _, resp := range result.Responses {
    if resp.Success {
        // 处理响应
    }
}
```

#### 调整并发参数

根据系统负载调整 `MaxConcurrent`：

```go
// CPU 密集型操作
opts := &rpc.BatchOptions{
    MaxConcurrent: runtime.NumCPU(),
}

// IO 密集型操作
opts := &rpc.BatchOptions{
    MaxConcurrent: runtime.NumCPU() * 2,
}

// 低延迟优先
opts := &rpc.BatchOptions{
    MaxConcurrent: 1, // 串行执行
}
```

### 3. 限流器优化

#### 全局限流器

控制总连接数，防止过载：

```go
// 低负载场景
globalLimiter := rpc.NewRateLimiter(&rpc.RateLimiterConfig{
    MaxConnections:  1000,
    RefillRate:     50 * time.Millisecond,
    RefillAmount:   100,
    BucketSize:     1000,
})

// 高负载场景
globalLimiter := rpc.NewRateLimiter(&rpc.RateLimiterConfig{
    MaxConnections:  100,  // 限制连接数
    RefillRate:     100 * time.Millisecond,
    RefillAmount:   10,
    BucketSize:     100,
})
```

#### Peer 限流器

控制单个 peer 的调用速率：

```go
// 默认配置
peerLimiter := rpc.NewPeerRateLimiter(&rpc.PeerRateLimiterConfig{
    DefaultRate:         100, // 每秒 100 个请求
    MaxRate:             1000,
    EnableDynamicAdjust: true,  // 启用动态调整
})

// 高吞吐场景
peerLimiter := rpc.NewPeerRateLimiter(&rpc.PeerRateLimiterConfig{
    DefaultRate:         1000, // 每秒 1000 个请求
    MaxRate:             10000,
    EnableDynamicAdjust: true,
})
```

### 4. 消息大小优化

#### 调整 MaxMessageSize

根据消息大小调整 `MaxMessageSize`：

```go
// 小消息场景
client := rpc.NewClient(host)
client.SetMaxMessageSize(1024) // 1KB

// 大消息场景
client := rpc.NewClient(host)
client.SetMaxMessageSize(102400) // 100KB
```

#### 使用批量传输

对于大量数据，使用批量传输：

```go
// 不推荐：多次小消息
for _, item := range items {
    client.Call(ctx, peerID, "Add", encode(item))
}

// 推荐：批量传输
reqs := make([]rpc.BatchRequest, 0, len(items))
for _, item := range items {
    reqs = append(reqs, rpc.BatchRequest{
        Method: "BatchAdd",
        Body:   encode(item),
    })
}
client.CallParallel(ctx, peerID, reqs, nil)
```

### 5. 超时配置

#### 设置合理的超时

根据网络条件设置超时：

```go
// 局域网：短超时
client.SetDefaultTimeout(100 * time.Millisecond)

// 公网：中等超时
client.SetDefaultTimeout(1 * time.Second)

// 跨国：长超时
client.SetDefaultTimeout(5 * time.Second)
```

#### 使用分级超时

不同操作使用不同超时：

```go
// 快速操作：短超时
quickCtx, quickCancel := context.WithTimeout(ctx, 100*time.Millisecond)
defer quickCancel()
client.Call(quickCtx, peerID, "Ping", req)

// 慢速操作：长超时
slowCtx, slowCancel := context.WithTimeout(ctx, 5*time.Second)
defer slowCancel()
client.Call(slowCtx, peerID, "Export", req)
```

## 性能监控

### Prometheus 指标

#### 关键指标

```bash
# Stream 复用率
rate(nexkv_rpc_streams_reused_total[5m]) / rate(nexkv_rpc_streams_created_total[5m])

# 调用延迟 (P99)
histogram_quantile(0.99, rate(nexkv_rpc_calls_duration_seconds_bucket[5m]))

# 吞吐量
rate(nexkv_rpc_calls_total[1m])

# 连接池效率
nexkv_rpc_pool_active_streams / nexkv_rpc_pool_max_streams
```

#### 告警规则

```yaml
# Prometheus 告警规则
groups:
  - name: rpc_performance
    rules:
      - alert: HighLatency
        expr: histogram_quantile(0.99, rate(nexkv_rpc_calls_duration_seconds_bucket[5m])) > 0.1
        annotations:
          summary: "RPC P99 延迟超过 100ms"

      - alert: LowStreamReuse
        expr: rate(nexkv_rpc_streams_reused_total[5m]) / rate(nexkv_rpc_streams_created_total[5m]) < 0.8
        annotations:
          summary: "Stream 复用率低于 80%"

      - alert: HighFailureRate
        expr: rate(nexkv_rpc_calls_errors_total[5m]) / rate(nexkv_rpc_calls_total[5m]) > 0.05
        annotations:
          summary: "RPC 失败率超过 5%"
```

## 性能测试

### 基准测试

运行基准测试：

```bash
# 运行所有基准测试
go test -bench=. -benchmem ./internal/rpc/

# 运行特定基准测试
go test -bench=BenchmarkRPC_Call -benchmem ./internal/rpc/

# 运行批量调用基准
go test -bench=BenchmarkRPC_BatchCall -benchmem ./internal/rpc/
```

### 压力测试

运行压力测试：

```bash
# 运行压力测试
go test -v -run TestRPC_StressHighFrequency ./internal/rpc/

# 运行大负载测试
go test -v -run TestRPC_StressLargePayload ./internal/rpc/
```

## 性能问题排查

### 高延迟

#### 症状

- P99 延迟 > 100ms
- 调用超时频繁

#### 排查步骤

1. **检查网络延迟**
   ```bash
   ping <peer_address>
   ```

2. **检查消息大小**
   ```bash
   # 查看 Prometheus 指标
   curl http://localhost:9090/metrics | grep message_size
   ```

3. **检查限流器**
   ```bash
   # 查看限流器拒绝率
   curl http://localhost:9090/metrics | grep ratelimiter_calls_throttled
   ```

#### 解决方案

- 降低 `MaxMessageSize`
- 提高 `DefaultRate`
- 启用连接池

### 低吞吐量

#### 症状

- QPS < 1000
- CPU 利用率低

#### 排查步骤

1. **检查并发数**
   ```bash
   # 查看活跃 Stream 数
   curl http://localhost:9090/metrics | grep active_streams
   ```

2. **检查批量使用**
   ```bash
   # 查看批量调用率
   curl http://localhost:9090/metrics | grep batch_calls_total
   ```

#### 解决方案

- 提高 `MaxConcurrent`
- 使用批量调用
- 提高连接池大小

### 高内存使用

#### 症状

- 内存使用持续增长
- OOM 频繁

#### 排查步骤

1. **检查 Stream 泄漏**
   ```bash
   # 查看 Stream 创建/关闭比率
   rate(nexkv_rpc_streams_created_total[5m]) / rate(nexkv_rpc_streams_closed_total[5m])
   ```

2. **检查连接池**
   ```bash
   # 查看 Stream TTL
   curl http://localhost:9090/metrics | grep stream_ttl
   ```

#### 解决方案

- 降低 `StreamTTL`
- 降低 `MaxStreams`
- 定期调用 `pool.Cleanup()`

## 性能优化清单

### 快速优化

- [ ] 启用连接池
- [ ] 使用批量调用
- [ ] 配置限流器
- [ ] 设置合理超时

### 深度优化

- [ ] 调整 Stream 池大小
- [ ] 调整批量并发数
- [ ] 优化消息序列化
- [ ] 启用 Prometheus 监控

## 参考资料

- [RPC 模块 README](../internal/rpc/README.md)
- [监控指标说明](monitoring.md)
- [libp2p 性能最佳实践](https://libp2p.io/)
