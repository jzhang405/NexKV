# RPC 模块使用指南

> 基于 libp2p Stream 的 RPC 实现

## 概述

RPC 模块实现了基于 libp2p Stream 的远程过程调用（RPC）机制，支持：
- **单播和广播**：支持单点调用和 Fanout 广播
- **批量调用**：支持批量并行调用
- **连接池**：自动 Stream 复用和连接管理
- **限流保护**：全局限流 + Peer 级别限流
- **监控指标**：Prometheus 指标导出

## 核心组件

### 1. RPC Server

RPC 服务器负责处理来自客户端的 RPC 请求。

```go
package main

import (
    "context"
    "github.com/jzhang405/NexKV/internal/rpc"
)

func main() {
    // 创建 libp2p host
    host, _ := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))

    // 创建 RPC 服务器
    server := rpc.NewServer(host)

    // 注册处理器
    server.RegisterHandlerFunc("Echo", func(ctx context.Context, req []byte) ([]byte, error) {
        return req, nil // Echo 回显
    })

    // 启动服务器
    ctx := context.Background()
    server.Start(ctx)
}
```

### 2. RPC Client

RPC 客户端负责发起 RPC 调用。

```go
// 创建客户端
client := rpc.NewClient(host)

// 发起 RPC 调用
ctx := context.Background()
response, err := client.Call(ctx, peerID, "Echo", request)
```

### 3. 批量调用

支持批量并行调用单个 peer 或多个 peer。

```go
// 单个 peer，多个请求
reqs := []rpc.BatchRequest{
    {Method: "Get", Body: []byte("key1")},
    {Method: "Get", Body: []byte("key2")},
}
result := client.CallParallel(ctx, peerID, reqs, nil)

// 多个 peer，相同请求（Fanout）
peerIDs := []peer.ID{peer1, peer2, peer3}
req := rpc.BatchRequest{Method: "Ping", Body: []byte("ping")}
results := client.CallParallelFanout(ctx, peerIDs, req, nil)
```

### 4. 连接池

自动 Stream 复用，减少连接开销。

```go
// 连接池默认启用
client := rpc.NewClient(host)

// Stream 自动复用
for i := 0; i < 100; i++ {
    client.Call(ctx, peerID, "Method", req) // 复用同一个 Stream
}
```

### 5. 限流器

全局限流 + Peer 级别限流。

```go
// 全局限流器（连接数限制）
globalLimiter := rpc.NewRateLimiter(&rpc.RateLimiterConfig{
    MaxConnections:  100,
    RefillRate:     100 * time.Millisecond,
    RefillAmount:  10,
    BucketSize:    100,
})

// Peer 级别限流器（调用速率限制）
peerLimiter := rpc.NewPeerRateLimiter(&rpc.PeerRateLimiterConfig{
    DefaultRate: 100, // 每秒 100 个请求
    MaxRate:     1000,
})
```

## 配置选项

### Server 配置

| 选项 | 默认值 | 说明 |
|------|--------|------|
| MaxMessageSize | 10KB | 单条消息最大大小 |
| DefaultTimeout | 30s | 默认 RPC 超时 |

### Client 配置

| 选项 | 默认值 | 说明 |
|------|--------|------|
| defaultTimeout | 30s | 默认 RPC 超时 |
| enablePool | true | 是否启用连接池 |

### 连接池配置

| 选项 | 默认值 | 说明 |
|------|--------|------|
| MaxStreams | 10 | 每个 peer 最大 Stream 数 |
| StreamTTL | 5 分钟 | Stream 最大存活时间 |
| MaxMessages | 1000 | 单 Stream 最大消息数 |

### 限流器配置

#### 全局限流器

| 选项 | 默认值 | 说明 |
|------|--------|------|
| MaxConnections | 100 | 最大并发连接数 |
| RefillRate | 100ms | 令牌补充间隔 |
| RefillAmount | 10 | 每次补充的令牌数 |
| BucketSize | 100 | 令牌桶大小 |

#### Peer 限流器

| 选项 | 默认值 | 说明 |
|------|--------|------|
| DefaultRate | 100/秒 | 默认调用速率 |
| MaxRate | 1000/秒 | 最大调用速率 |
| EnableDynamicAdjust | true | 是否启用动态调整 |

### 批量调用配置

| 选项 | 默认值 | 说明 |
|------|--------|------|
| MaxConcurrent | 10 | 最大并发数 |
| Timeout | 30s | 整体超时 |
| ContinueOnError | false | 遇错是否继续 |
| PreserveOrder | true | 是否保持顺序 |

## 监控指标

### Prometheus 指标

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| nexkv_rpc_streams_active | Gauge | - | 活跃 Stream 数 |
| nexkv_rpc_streams_created_total | Counter | - | 创建的 Stream 总数 |
| nexkv_rpc_streams_reused_total | Counter | - | 复用的 Stream 总数 |
| nexkv_rpc_calls_total | Counter | peer, method | RPC 调用总数 |
| nexkv_rpc_calls_duration_seconds | Histogram | peer, method | RPC 调用延迟 |
| nexkv_rpc_batch_calls_total | Counter | - | 批量调用总数 |
| nexkv_rpc_connections_total | Counter | - | 连接总数 |
| nexkv_rpc_peer_ratelimiter_calls_total | Counter | peer | Peer 限流器调用数 |

访问指标：
```bash
curl http://localhost:9090/metrics
```

## 最佳实践

### 1. 使用连接池

连接池默认启用，自动管理 Stream 生命周期：

```go
// 推荐：使用默认配置
client := rpc.NewClient(host)

// 不推荐：禁用连接池
client := rpc.NewClient(host)
client.enablePool = false
```

### 2. 设置合理的超时

```go
// 设置默认超时
client := rpc.NewClient(host)
client.SetDefaultTimeout(10 * time.Second)

// 或使用带超时的 Context
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
client.Call(ctx, peerID, "Method", req)
```

### 3. 使用批量调用

对于多个调用，使用批量 API 减少网络往返：

```go
// 不推荐：串行调用
for _, key := range keys {
    client.Call(ctx, peerID, "Get", []byte(key))
}

// 推荐：批量调用
reqs := make([]rpc.BatchRequest, len(keys))
for i, key := range keys {
    reqs[i] = rpc.BatchRequest{Method: "Get", Body: []byte(key)}
}
result := client.CallParallel(ctx, peerID, reqs, nil)
```

### 4. 配置限流器

保护系统免受过载：

```go
// 全局限流：限制总连接数
globalLimiter := rpc.NewRateLimiter(&rpc.RateLimiterConfig{
    MaxConnections: 100,
})

// Peer 限流：限制单个 peer 的调用速率
peerLimiter := rpc.NewPeerRateLimiter(&rpc.PeerRateLimiterConfig{
    DefaultRate: 100, // 每秒 100 个请求
})
```

### 5. 处理错误

```go
resp, err := client.Call(ctx, peerID, "Method", req)
if err != nil {
    // 检查错误类型
    if rpcErr, ok := err.(*rpc.RPCError); ok {
        switch rpcErr.Code {
        case rpc.ErrCodeTimeout:
            // 处理超时
        case rpc.ErrCodeStreamClosed:
            // 处理 Stream 关闭
        default:
            // 其他错误
        }
    }
}
```

## 性能建议

### 1. 启用连接池

连接池可以显著提高性能，减少 Stream 创建开销。

### 2. 使用批量调用

批量调用可以减少网络往返，提高吞吐量。

### 3. 调整限流器参数

根据系统负载调整限流器参数：
- 高负载：降低 `DefaultRate` 和 `MaxConnections`
- 低负载：提高 `DefaultRate` 和 `MaxConnections`

### 4. 监控指标

定期检查 Prometheus 指标，及时发现性能瓶颈。

## 错误处理

### 错误码

| 错误码 | 值 | 说明 |
|-------|---|------|
| ErrCodeTimeout | 1001 | RPC 调用超时 |
| ErrCodeStreamClosed | 1002 | Stream 已关闭 |
| ErrCodeMessageTooLarge | 1003 | 消息过大 |
| ErrCodeHandlerNotFound | 2001 | 处理器未找到 |
| ErrCodeHandlerPanic | 2002 | 处理器 panic |
| ErrCodeCodecError | 3001 | 编解码错误 |

### RPCError 结构

```go
type RPCError struct {
    Code    int32  // 错误码
    Message string // 错误消息
}
```

## 示例代码

### 完整示例

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/jzhang405/NexKV/internal/rpc"
    libp2p "github.com/libp2p/go-libp2p"
)

func main() {
    // 创建 libp2p host
    host, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
    if err != nil {
        log.Fatal(err)
    }

    // 创建 RPC 服务器
    server := rpc.NewServer(host)

    // 注册处理器
    server.RegisterHandlerFunc("Echo", echoHandler)
    server.RegisterHandlerFunc("Get", getHandler)

    // 启动服务器
    ctx := context.Background()
    go func() {
        if err := server.Start(ctx); err != nil {
            log.Printf("Server error: %v", err)
        }
    }()

    // 创建客户端
    client := rpc.NewClient(host)

    // 发起 RPC 调用
    resp, err := client.Call(ctx, host.ID(), "Echo", []byte("Hello"))
    if err != nil {
        log.Fatalf("RPC call failed: %v", err)
    }

    fmt.Printf("Response: %s\n", resp)
}

func echoHandler(ctx context.Context, req []byte) ([]byte, error) {
    return req, nil
}

func getHandler(ctx context.Context, req []byte) ([]byte, error) {
    return []byte("value"), nil
}
```

## 参考资料

- [libp2p 官方文档](https://libp2p.io/)
- [Prometheus 指标最佳实践](https://prometheus.io/docs/practices/)
- [性能调优指南](../performance-tuning.md)
