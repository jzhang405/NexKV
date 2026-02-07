# RPC 监控指标说明

> NexKV RPC 模块的 Prometheus 监控指标

## 概述

RPC 模块导出了全面的 Prometheus 监控指标，用于实时监控和性能分析。

## 指标分类

### 1. Stream 指标

监控 libp2p Stream 的生命周期和复用情况。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_streams_active` | Gauge | - | 当前活跃的 Stream 数 |
| `nexkv_rpc_streams_created_total` | Counter | - | 创建的 Stream 总数 |
| `nexkv_rpc_streams_reused_total` | Counter | - | 复用的 Stream 总数 |
| `nexkv_rpc_streams_closed_total` | Counter | - | 关闭的 Stream 总数 |
| `nexkv_rpc_cache_hit_total` | Counter | - | Stream 缓存命中数 |
| `nexkv_rpc_cache_miss_total` | Counter | - | Stream 缓存未命中数 |

#### 关键计算

**Stream 复用率**
```
rate(nexkv_rpc_streams_reused_total[5m]) / rate(nexkv_rpc_streams_created_total[5m])
```
目标值：> 80%

**Stream 缓存命中率**
```
nexkv_rpc_cache_hit_total / (nexkv_rpc_cache_hit_total + nexkv_rpc_cache_miss_total)
```
目标值：> 90%

### 2. RPC 调用指标

监控 RPC 调用的性能和成功率。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_calls_total` | Counter | peer_id, method | RPC 调用总数 |
| `nexkv_rpc_calls_success_total` | Counter | peer_id, method | 成功的 RPC 调用数 |
| `nexkv_rpc_calls_errors_total` | Counter | peer_id, method, error_code | 失败的 RPC 调用数 |
| `nexkv_rpc_calls_duration_seconds` | Histogram | peer_id, method | RPC 调用延迟分布 |
| `nexkv_rpc_calls_timeout_total` | Counter | peer_id, method | RPC 调用超时数 |

#### 关键计算

**RPC 成功率**
```
rate(nexkv_rpc_calls_success_total[5m]) / rate(nexkv_rpc_calls_total[5m])
```
目标值：> 95%

**RPC 失败率**
```
rate(nexkv_rpc_calls_errors_total[5m]) / rate(nexkv_rpc_calls_total[5m])
```
目标值：< 5%

**RPC P99 延迟**
```
histogram_quantile(0.99, rate(nexkv_rpc_calls_duration_seconds_bucket[5m]))
```
目标值：< 50ms

**RPC 平均延迟**
```
rate(nexkv_rpc_calls_duration_seconds_sum[5m]) / rate(nexkv_rpc_calls_duration_seconds_count[5m])
```
目标值：< 20ms

### 3. 批量调用指标

监控批量 RPC 调用的性能。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_batch_calls_total` | Counter | - | 批量调用总数 |
| `nexkv_rpc_batch_calls_size` | Histogram | - | 批量调用大小分布 |
| `nexkv_rpc_batch_calls_duration_seconds` | Histogram | - | 批量调用延迟分布 |
| `nexkv_rpc_batch_calls_errors_total` | Counter | - | 批量调用错误数 |

#### 关键计算

**平均批量大小**
```
rate(nexkv_rpc_batch_calls_size_sum[5m]) / rate(nexkv_rpc_batch_calls_size_count[5m])
```

**批量调用成功率**
```
1 - rate(nexkv_rpc_batch_calls_errors_total[5m]) / rate(nexkv_rpc_batch_calls_total[5m])
```
目标值：> 90%

### 4. 连接指标

监控连接池的状态和性能。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_connections_total` | Counter | - | 创建的连接总数 |
| `nexkv_rpc_connections_active` | Gauge | - | 当前活跃的连接数 |
| `nexkv_rpc_connections_failed_total` | Counter | - | 失败的连接数 |

#### 关键计算

**连接失败率**
```
rate(nexkv_rpc_connections_failed_total[5m]) / rate(nexkv_rpc_connections_total[5m])
```
目标值：< 1%

### 5. 全局限流器指标

监控全局限流器的状态。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_global_ratelimiter_connection_total` | Counter | - | 连接尝试总数 |
| `nexkv_rpc_global_ratelimiter_connection_accepted` | Counter | - | 接受的连接数 |
| `nexkv_rpc_global_ratelimiter_connection_rejected` | Counter | - | 拒绝的连接数 |
| `nexkv_rpc_global_ratelimiter_connection_active` | Gauge | - | 当前活跃连接数 |
| `nexkv_rpc_global_ratelimiter_token_bucket_refill` | Counter | - | 令牌补充次数 |
| `nexkv_rpc_global_ratelimiter_token_bucket_exhausted` | Counter | - | 令牌桶耗尽次数 |

#### 关键计算

**连接接受率**
```
rate(nexkv_rpc_global_ratelimiter_connection_accepted[5m]) / rate(nexkv_rpc_global_ratelimiter_connection_total[5m])
```
目标值：> 95%

**限流拒绝率**
```
rate(nexkv_rpc_global_ratelimiter_connection_rejected[5m]) / rate(nexkv_rpc_global_ratelimiter_connection_total[5m])
```
目标值：< 5%

### 6. Peer 限流器指标

监控 Peer 级别限流器的状态。

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `nexkv_rpc_peer_ratelimiter_calls_total` | Counter | peer_id | Peer 调用总数 |
| `nexkv_rpc_peer_ratelimiter_calls_allowed_total` | Counter | peer_id | 允许的调用数 |
| `nexkv_rpc_peer_ratelimiter_calls_throttled_total` | Counter | peer_id | 限流的调用数 |
| `nexkv_rpc_peer_ratelimiter_rate_adjustments_total` | Counter | peer_id | 速率调整次数 |
| `nexkv_rpc_peer_ratelimiter_rate_ups_total` | Counter | peer_id | 速率提升次数 |
| `nexkv_rpc_peer_ratelimiter_rate_downs_total` | Counter | peer_id | 速率降低次数 |
| `nexkv_rpc_peer_ratelimiter_response_time_seconds` | Histogram | peer_id | 响应时间分布 |

#### 关键计算

**Peer 限流接受率**
```
rate(nexkv_rpc_peer_ratelimiter_calls_allowed_total[5m]) / rate(nexkv_rpc_peer_ratelimiter_calls_total[5m])
```
目标值：> 90%

**Peer 限流拒绝率**
```
rate(nexkv_rpc_peer_ratelimiter_calls_throttled_total[5m]) / rate(nexkv_rpc_peer_ratelimiter_calls_total[5m])
```
目标值：< 10%

**Peer P99 响应时间**
```
histogram_quantile(0.99, rate(nexkv_rpc_peer_ratelimiter_response_time_seconds_bucket[5m]))
```

## Grafana 仪表板

### 推荐面板配置

#### 概览面板

```
# RPC 总体状态
- RPC QPS (rate(nexkv_rpc_calls_total[1m]))
- 成功率 (rate(nexkv_rpc_calls_success_total[1m]) / rate(nexkv_rpc_calls_total[1m]))
- P99 延迟 (histogram_quantile(0.99, rate(nexkv_rpc_calls_duration_seconds_bucket[5m])))
- 活跃 Stream (nexkv_rpc_streams_active)
```

#### Stream 性能面板

```
# Stream 复用效率
- Stream 复用率 (rate(nexkv_rpc_streams_reused_total[5m]) / rate(nexkv_rpc_streams_created_total[5m]))
- 缓存命中率 (nexkv_rpc_cache_hit_total / (nexkv_rpc_cache_hit_total + nexkv_rpc_cache_miss_total))
- 活跃 Stream (nexkv_rpc_streams_active)
- Stream 创建速率 (rate(nexkv_rpc_streams_created_total[5m]))
```

#### 限流器面板

```
# 全局限流器状态
- 连接接受率 (rate(nexkv_rpc_global_ratelimiter_connection_accepted[5m]) / rate(nexkv_rpc_global_ratelimiter_connection_total[5m]))
- 活跃连接 (nexkv_rpc_global_ratelimiter_connection_active)
- 令牌桶耗尽 (nexkv_rpc_global_ratelimiter_token_bucket_exhausted)

# Peer 限流器状态（Top 10）
- Peer 限流拒绝率 (topk(10, rate(nexkv_rpc_peer_ratelimiter_calls_throttled_total[5m]) / rate(nexkv_rpc_peer_ratelimiter_calls_total[5m])))
```

## 告警规则

### 推荐告警规则

```yaml
groups:
  - name: rpc_alerts
    interval: 30s
    rules:
      # 高延迟告警
      - alert: RPCHighLatency
        expr: |
          histogram_quantile(0.99, rate(nexkv_rpc_calls_duration_seconds_bucket[5m])) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "RPC P99 延迟过高"
          description: "RPC P99 延迟超过 100ms"

      # 低成功率告警
      - alert: RPCLowSuccessRate
        expr: |
          rate(nexkv_rpc_calls_success_total[5m]) / rate(nexkv_rpc_calls_total[5m]) < 0.95
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "RPC 成功率过低"
          description: "RPC 成功率低于 95%"

      # Stream 复用率低告警
      - alert: RPCLowStreamReuse
        expr: |
          rate(nexkv_rpc_streams_reused_total[5m]) / rate(nexkv_rpc_streams_created_total[5m]) < 0.8
        for: 10m
        labels:
          severity: info
        annotations:
          summary: "Stream 复用率低"
          description: "Stream 复用率低于 80%"

      # 限流拒绝率告警
      - alert: RPCHighThrottleRate
        expr: |
          rate(nexkv_rpc_global_ratelimiter_connection_rejected[5m]) / rate(nexkv_rpc_global_ratelimiter_connection_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "限流拒绝率过高"
          description: "限流拒绝率超过 10%"
```

## 指标采集

### Prometheus 配置

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'nexkv-rpc'
    static_configs:
      - targets: ['localhost:9090']
    scrape_interval: 15s
    metrics_path: /metrics
```

### 访问指标

```bash
# 访问指标端点
curl http://localhost:9090/metrics

# 查看特定指标
curl http://localhost:9090/metrics | grep nexkv_rpc_calls_total

# 查看 Stream 相关指标
curl http://localhost:9090/metrics | grep nexkv_rpc_streams
```

## 性能基准

### 推荐基线值

| 指标 | 基线值 | 目标值 |
|------|--------|--------|
| **P99 延迟** | < 100ms | < 50ms |
| **平均延迟** | < 30ms | < 20ms |
| **成功率** | > 95% | > 99% |
| **Stream 复用率** | > 70% | > 80% |
| **吞吐量** | > 500 req/s | > 1000 req/s |

### 性能测试

运行性能基准测试：

```bash
# 运行所有基准测试
go test -bench=. -benchmem ./internal/rpc/

# 运行特定基准测试
go test -bench=BenchmarkRPC_Call -benchmem ./internal/rpc/
```

## 故障排查

### 高延迟

**检查指标：**
- `nexkv_rpc_calls_duration_seconds` - P99 延迟
- `nexkv_rpc_peer_ratelimiter_response_time_seconds` - 限流器响应时间

**可能原因：**
- 网络延迟高
- 限流器排队
- 消息过大
- CPU 负载高

### 低吞吐量

**检查指标：**
- `nexkv_rpc_calls_total` - 调用速率
- `nexkv_rpc_batch_calls_total` - 批量调用速率
- `nexkv_rpc_streams_active` - 活跃 Stream 数

**可能原因：**
- 连接池配置不当
- 限流器配置过严
- 未使用批量调用
- 客户端并发不足

### 高失败率

**检查指标：**
- `nexkv_rpc_calls_errors_total` - 错误数（按 error_code 分类）
- `nexkv_rpc_calls_timeout_total` - 超时数
- `nexkv_rpc_global_ratelimiter_connection_rejected` - 限流拒绝数

**可能原因：**
- 超时设置过短
- 限流器配置过严
- 网络不稳定
- 服务器过载

## 参考资料

- [Prometheus 查询语言](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [性能调优指南](performance-tuning.md)
- [RPC 模块 README](../internal/rpc/README.md)
