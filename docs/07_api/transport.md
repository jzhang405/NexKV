# Transport API

## 概述

NexKV 支持 libp2p Transport，提供 P2P 网络能力。

## libp2p Transport

### 初始化

#### 方式 1: 使用默认配置

```go
import "github.com/jzhang405/NexKV/internal/transport"

p2pService, err := transport.NewP2PService(
    transport.DefaultP2PServiceConfig("0.0.0.0:4001", "/tmp/libp2p_key")
)
if err != nil {
    log.Fatal(err)
}

err = p2pService.Start(ctx)
```

#### 方式 2: 使用配置文件

```go
import (
    "github.com/jzhang405/NexKV/internal/config"
    "github.com/jzhang405/NexKV/internal/transport"
)

cfg, _ := config.LoadConfig("config.yaml")
p2pService, _ := transport.NewTransportFromConfig(&cfg.Transport)
p2pService.Start(ctx)
```

### 配置选项

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| ListenAddr | string | "0.0.0.0:4001" | 监听地址 (host:port 格式) |
| KeyPath | string | 必填 | 私钥文件路径 |
| LowWater | int | 100 | 最小连接数 |
| HighWater | int | 400 | 最大连接数 |
| DiscoveryTag | string | "nexkv-discovery" | mDNS 服务标签 |
| BootstrapPeers | []peer.AddrInfo | - | Bootstrap 节点列表 |

### 消息发送

```go
protocol := p2pService.Protocol()

// 发送单条消息
msg := &transport.Message{}
msg.Type = transport.MessageTypeSync
payload := &transport.TwoPCPreparePayload{...}
msg.MustEncodePayload(payload)

err := protocol.SendMessage(ctx, peerID, msg)

// 广播消息
peerIDs := []peer.ID{peerID1, peerID2}
err := protocol.BroadcastMessage(ctx, peerIDs, msg)
```

### 消息接收

```go
// 注册消息处理器
protocol.RegisterHandler(transport.MessageTypeSync,
    transport.MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *transport.Message) error {
        payload, _ := msg.DecodePayload()
        twoPCMsg, _ := payload.(*transport.TwoPCPreparePayload)
        // 处理消息...
        return nil
    }),
)
```

## 地址格式

### multiaddr 格式

libp2p 使用 multiaddr 格式：

```
/ip4/192.168.1.1/tcp/4001/p2p/12D3KooW...
/dns4/node1.example.com/tcp/4001/p2p/12D3KooW...
```

### 从传统格式迁移

**传统格式** (IP:PORT):
```
192.168.1.1:4001
```

**multiaddr 格式**:
```
/ip4/192.168.1.1/tcp/4001
```

**完整地址** (包含 PeerID):
```
/ip4/192.168.1.1/tcp/4001/p2p/12D3KooW...
```

## 性能指标

| 指标 | 值 |
|------|-----|
| Send 延迟 | < 10ms |
| Receive 吞吐 | > 10000 msg/s |
| 节点发现 | < 5秒（局域网） |
| 批量转发 | > 500 msg/s |

## 配置文件示例

### 完整配置

```yaml
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  private_key_path: "/var/lib/nexkv/libp2p_key"
  discovery:
    mdns_enabled: true
    dht_enabled: true
```

### 最小配置

```yaml
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  private_key_path: "/var/lib/nexkv/libp2p_key"
```
