# Day 2-3：libp2p 基础培训

> **培训时间**: 1天（6小时）
> **培训内容**: libp2p 去中心化通信 + 局域网实践

---

## 一、libp2p 概述（45分钟）

### 1.1 什么是 libp2p？

**libp2p** 是一个模块化的网络协议栈，用于构建去中心化应用。

**核心特性**:
- ✅ **去中心化**: 无需中心服务器
- ✅ **模块化**: 可选择不同的传输协议
- ✅ **跨平台**: 支持 Go、JavaScript、Rust、Python 等
- ✅ **NAT 穿透**: 支持 NAT 穿透和中继（NexKV 当前仅需局域网）

**NexKV 为什么选择 libp2p？**
- 符合无中心架构设计
- 节点间直接通信，低延迟
- 支持多种传输协议
- 易于扩展到公网环境

---

### 1.2 libp2p 核心组件

| 组件 | 功能 | NexKV 使用场景 |
|------|------|---------------|
| **Transport** | 传输协议（TCP、QUIC、WebSocket）| 节点间通信 |
| **Muxer** | 流多路复用（mplex、yamux）| 一个连接上多个流 |
| **Security** | 安全传输（TLS、Noise）| 加密通信 |
| **Peer Discovery** | 节点发现（mDNS、DHT）| 局域网节点发现 |
| **PubSub** | 发布订阅（GossipSub）| 广播消息 |

---

## 二、创建第一个 libp2p 节点（60分钟）

### 2.1 基本设置

**安装依赖**:
```bash
go get github.com/libp2p/go-libp2p
```

**创建节点**:
```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/host"
)

func main() {
    // 创建 libp2p 节点
    h, err := libp2p.New()
    if err != nil {
        log.Fatalf("Failed to create libp2p node: %v", err)
    }
    defer h.Close()
    
    fmt.Printf("Node ID: %s\n", h.ID())
    fmt.Printf("Node addresses:\n")
    for _, addr := range h.Addrs() {
        fmt.Printf("  %s/p2p/%s\n", addr, h.ID())
    }
}
```

**运行**:
```bash
go run main.go
```

**输出**:
```
Node ID: QmSoLMeWqB7YGVLJN3GFDXnX5JL...
Node addresses:
  /ip4/127.0.0.1/tcp/4001/p2p/QmSoLMeWqB7YGVLJN3GFDXnX5JL...
  /ip4/192.168.1.100/tcp/4001/p2p/QmSoLMeWqB7YGVLJN3GFDXnX5JL...
```

---

### 2.2 mDNS 节点发现（局域网）

**配置 mDNS**:
```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"
    
    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/host"
    "github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

// discoveryNotifee mDNS 发现通知
type discoveryNotifee struct {
    h host.Host
}

// HandlePeerFound 处理发现的节点
func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
    fmt.Printf("Found peer: %s\n", pi.ID)
    
    // 连接到发现的节点
    if err := n.h.Connect(context.Background(), pi); err != nil {
        fmt.Printf("Failed to connect to peer %s: %v\n", pi.ID, err)
    } else {
        fmt.Printf("Connected to peer: %s\n", pi.ID)
    }
}

func main() {
    // 创建节点
    h, err := libp2p.New()
    if err != nil {
        log.Fatalf("Failed to create libp2p node: %v", err)
    }
    defer h.Close()
    
    // 设置 mDNS 发现
    notifee := &discoveryNotifee{h: h}
    service := mdns.NewMdnsService(h, "nexkv-discovery", notifee)
    if err := service.Start(); err != nil {
        log.Fatalf("Failed to start mDNS service: %v", err)
    }
    defer service.Close()
    
    fmt.Printf("Node ID: %s\n", h.ID())
    fmt.Println("mDNS discovery started...")
    
    // 保持运行
    select {}
}
```

---

## 三、流和协议（60分钟）

### 3.1 流（Stream）

**流** 是 libp2p 的通信单元，支持双向数据传输。

**创建流**:
```go
// 发送方
stream, err := h.NewStream(context.Background(), peerID, "/nexkv/1.0.0")
if err != nil {
    log.Fatalf("Failed to create stream: %v", err)
}
defer stream.Close()

// 发送数据
_, err = stream.Write([]byte("Hello from NexKV!"))
if err != nil {
    log.Fatalf("Failed to write to stream: %v", err)
}
```

**接收流**:
```go
// 接收方
h.SetStreamHandler("/nexkv/1.0.0", func(stream network.Stream) {
    defer stream.Close()
    
    // 读取数据
    buf := make([]byte, 1024)
    n, err := stream.Read(buf)
    if err != nil {
        log.Printf("Failed to read from stream: %v", err)
        return
    }
    
    fmt.Printf("Received: %s\n", string(buf[:n]))
    
    // 发送响应
    _, err = stream.Write([]byte("Message received!"))
    if err != nil {
        log.Printf("Failed to write response: %v", err)
    }
})
```

---

### 3.2 协议（Protocol）

**协议** 是流的标识符，格式为 `/protocol-name/version`。

**示例**:
```go
// NexKV RPC 协议
/nexkv/rpc/1.0.0

// NexKV 复制协议
/nexkv/replication/1.0.0

// NexKV 心跳协议
/nexkv/heartbeat/1.0.0
```

---

## 四、RPC 调用实现（60分钟）

### 4.1 RPC 请求和响应

**定义 RPC 消息**:
```go
// RPCRequest RPC 请求
type RPCRequest struct {
    Method string      `json:"method"`
    Params interface{} `json:"params"`
    ID     string      `json:"id"`
}

// RPCResponse RPC 响应
type RPCResponse struct {
    Result interface{} `json:"result"`
    Error  string      `json:"error"`
    ID     string      `json:"id"`
}
```

**RPC 客户端**:
```go
// RPClient RPC 客户端
type RPClient struct {
    h      host.Host
    codec  Codec
}

// Call 发起 RPC 调用
func (c *RPClient) Call(ctx context.Context, peerID peer.ID, method string, params interface{}) (*RPCResponse, error) {
    // 创建流
    stream, err := c.h.NewStream(ctx, peerID, "/nexkv/rpc/1.0.0")
    if err != nil {
        return nil, err
    }
    defer stream.Close()
    
    // 编码请求
    req := &RPCRequest{
        Method: method,
        Params: params,
        ID:     generateID(),
    }
    
    if err := c.codec.Encode(stream, req); err != nil {
        return nil, err
    }
    
    // 解码响应
    resp := &RPCResponse{}
    if err := c.codec.Decode(stream, resp); err != nil {
        return nil, err
    }
    
    return resp, nil
}
```

**RPC 服务器**:
```go
// RPCServer RPC 服务器
type RPCServer struct {
    h      host.Host
    codec  Codec
    handlers map[string]RPCHandler
}

// RPCHandler RPC 处理器
type RPCHandler func(params interface{}) (interface{}, error)

// Start 启动服务器
func (s *RPCServer) Start() {
    s.h.SetStreamHandler("/nexkv/rpc/1.0.0", func(stream network.Stream) {
        defer stream.Close()
        
        // 解码请求
        req := &RPCRequest{}
        if err := s.codec.Decode(stream, req); err != nil {
            log.Printf("Failed to decode request: %v", err)
            return
        }
        
        // 处理请求
        handler, ok := s.handlers[req.Method]
        if !ok {
            s.sendError(stream, req.ID, "method not found")
            return
        }
        
        result, err := handler(req.Params)
        if err != nil {
            s.sendError(stream, req.ID, err.Error())
            return
        }
        
        // 编码响应
        resp := &RPCResponse{
            Result: result,
            ID:     req.ID,
        }
        s.codec.Encode(stream, resp)
    })
}
```

---

## 五、实践环节（60分钟）

### 5.1 实现一个简单的 KV 存储节点

**要求**:
1. 使用 mDNS 进行节点发现
2. 实现 RPC Put/Get 接口
3. 支持 3 节点集群通信
4. 测试读写功能

**示例代码结构**:
```
examples/libp2p-kvstore/
├── main.go
├── node.go
├── rpc.go
└── store.go
```

---

### 5.2 性能测试

**测试指标**:
- 吞吐量：ops/sec
- 延迟：P50、P95、P99
- 连接建立时间

**测试命令**:
```bash
go test -bench=. -benchmem
```

---

## 六、总结和 Q&A（15分钟）

### 6.1 关键要点

1. ✅ **libp2p 基础**:
   - 去中心化通信协议栈
   - 模块化设计
   - 支持多种传输协议

2. ✅ **NexKV 使用场景**:
   - 局域网节点发现（mDNS）
   - RPC 调用
   - 数据复制

3. ✅ **最佳实践**:
   - 使用协议标识符
   - 实现流多路复用
   - 添加错误处理和超时

---

**培训师**: 架构师
**培训日期**: 2026-02-19
**文档版本**: v1.0
