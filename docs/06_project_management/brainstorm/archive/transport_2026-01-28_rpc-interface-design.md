# Transport 目录重构方案

> **文档类型**: 💡 技术建议
> **创建日期**: 2026-01-28
> **状态**: 📋 待讨论
> **优先级**: P1 (中)

---

## 背景说明

当前 `internal/metadata/transport` 目录导出了过多内容，包括：
- 底层接口：`Transport`、`Codec`、`TransportConfig`
- 内部类型别名：`Message`、`MessageType`、`Priority`、`Address`
- 内部实现：`Dispatcher`、`MsgFrame`、帧格式细节

这导致外部包（如 `cluster`、`consensus`）过度依赖 transport 内部实现，违反了接口隔离原则。

用户期望：**transport 目录只暴露 `rpc_client` 和 `rpc_server` 接口**，隐藏其他所有内部实现细节。

---

## 问题描述

### 当前导出的内容（过多）

```go
// 当前 transport 包导出了 20+ 个类型和函数：
Transport                    // 底层传输接口
Codec                        // 编解码器接口
TransportConfig              // 传输配置
Message (别名)               // 消息接口
MessageType (别名)           // 消息类型
Priority (别名)              // 优先级
Address (别名)               // 地址
BatchForwardTransport        // 批量转发接口
RPCClient                    // RPC 客户端
RPCClientConfig              // RPC 客户端配置
RPCServer                    // RPC 服务端
RPCServerConfig              // RPC 服务端配置
RPCHandler                   // RPC 处理器接口
RPCHandlerFunc               // 函数式处理器
DefaultTransportConfig()     // 工厂函数
DefaultRPCClientConfig()     // 工厂函数
DefaultRPCServerConfig()     // 工厂函数
NewRPCClient()               // 工厂函数
NewRPCServer()               // 工厂函数
```

### 问题影响

| 问题 | 影响 |
|------|------|
| **接口泄漏** | 外部包直接依赖 `Transport` 接口，耦合度高 |
| **实现暴露** | `MsgFrame`、`Codec` 等内部实现对外可见 |
| **测试困难** | 无法 Mock 接口进行单元测试 |
| **重构风险** | 修改内部实现可能影响外部包 |

---

## 建议方案

### 1. 目录结构重组

```
internal/metadata/
├── transport/                    # 【仅导出 RPC Client/Server】
│   ├── rpc_client.go            # 导出：RPCClient、RPCHandler、NewRPCClient()
│   ├── rpc_server.go            # 导出：RPCServer、RPCHandler、NewRPCServer()
│   ├── rpc_types.go             # 导出：配置类型、统计信息、批量请求
│   ├── rpc_config.go            # 导出：默认配置函数
│   └── doc.go                   # 包说明
│
├── transport/internal/           # 【内部实现 - 对外隐藏】
│   ├── frame/                   # 帧格式（编解码、TLV）
│   │   ├── frame.go             # MsgFrame、FrameHeader
│   │   ├── codec.go             # MessagePack 编解码
│   │   └── tlv.go               # TLV 扩展字段
│   ├── protocol/                # 协议实现（TCP/UDP Transport）
│   │   ├── tcp_transport.go     # TCP Transport 实现
│   │   ├── udp_transport.go     # UDP Transport 实现
│   │   └── transport.go         # Transport 接口定义（内部）
│   └── dispatcher/              # 消息分发器
│       ├── dispatcher.go        # Dispatcher 实现
│       └── worker_pool.go       # Worker Pool
```

### 2. 公共接口设计

**transport 包只导出以下内容**：

```go
// ========================================
// 导出的接口
// ========================================

// RPCClient RPC 客户端接口
type RPCClient interface {
    Call(ctx context.Context, addr string, req types.Message) (types.Message, error)
    CallBatch(ctx context.Context, requests []*RPCBatchRequest) ([]types.Message, error)
    Start() error
    Stop() error
}

// RPCServer RPC 服务端接口
type RPCServer interface {
    Start() error
    Stop() error
    GetStats() ServerStats
}

// RPCHandler RPC 请求处理器接口
type RPCHandler interface {
    HandleRequest(ctx context.Context, req types.Message) (types.Message, error)
}

// ========================================
// 导出的类型
// ========================================

// RPCBatchRequest 批量请求
type RPCBatchRequest struct {
    Addr    string
    Message types.Message
}

// ServerStats 服务端统计信息
type ServerStats struct {
    Running     bool
    Processed   uint64
    Dropped     uint64
    WorkerCount int
    QueuedMsgs  int
}

// RPCClientConfig 客户端配置
type RPCClientConfig struct {
    DialTimeout     time.Duration
    RequestTimeout  time.Duration
    MaxRetries      int
    RetryDelay      time.Duration
    EnableFastFail  bool
    FastFailTimeout time.Duration
}

// RPCServerConfig 服务端配置
type RPCServerConfig struct {
    WorkerCount    int
    QueueSize      int
    RequestTimeout time.Duration
    EnableMetrics  bool
}

// ========================================
// 导出的工厂函数
// ========================================

func NewRPCClient(nodeID *uint64, tcpAddr, udpAddr string, config *RPCClientConfig) (RPCClient, error)
func NewRPCServer(nodeID *uint64, tcpAddr, udpAddr string, handler RPCHandler, config *RPCServerConfig) (RPCServer, error)
func DefaultRPCClientConfig() *RPCClientConfig
func DefaultRPCServerConfig() *RPCServerConfig
```

### 3. 内部包隔离

**`transport/internal/protocol/transport.go`**（内部接口，对外不可见）：

```go
// Package protocol 提供传输层协议实现（内部包）
package protocol

// Transport 传输层接口（内部，不导出）
type Transport interface {
    Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error
    Stop() error
    Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error
    Reply(ctx context.Context, addr string, msg Message, nodeID uint64, msgSeq uint64, connID string, opts ...SendOpt) error
    Receive() <-chan MsgFrame
    GetNodeID() uint64
    GenerateMsgSeq() uint64
}

// Message 消息接口（内部）
type Message interface {
    Type() MessageType
    ProtocolType() ProtocolType
    // ... 其他方法 ...
}

// MsgFrame 消息帧（内部）
type MsgFrame struct {
    Header  FrameHeader
    Message Message
    TLV     []TLVField
    // ... 其他字段 ...
}
```

### 4. 外部使用对比

#### 重构前（过度依赖内部实现）

```go
import "github.com/jzhang405/NexKV/internal/metadata/transport"

// ❌ 需要了解 Transport 接口
tcpTransport, err := transport.NewTCPTransport(&nodeID, "0.0.0.0:9211")
udpTransport, err := transport.NewUDPTransport(&nodeID, "0.0.0.0:9212")

// ❌ 需要了解 Transport 配置
config := transport.DefaultTransportConfig()
config.ListenAddr = "0.0.0.0:9211"

// ❌ 需要手动传递 Transport
client, err := transport.NewRPCClient(tcpTransport, udpTransport, nil)
server, err := transport.NewRPCServer(tcpTransport, udpTransport, handler, nil)
```

#### 重构后（只依赖 RPC 接口）

```go
import "github.com/jzhang405/NexKV/internal/metadata/transport"

// ✅ 只需要知道 RPC 接口
client, err := transport.NewRPCClient(
    &nodeID,
    "0.0.0.0:9211",  // TCP
    "0.0.0.0:9212",  // UDP
    transport.DefaultRPCClientConfig(),
)

server, err := transport.NewRPCServer(
    &nodeID,
    "0.0.0.0:9211",  // TCP
    "0.0.0.0:9212",  // UDP
    &myHandler{},
    transport.DefaultRPCServerConfig(),
)

// ✅ 使用 RPC 接口（不依赖 Transport 实现）
resp, err := client.Call(ctx, addr, req)
```

---

## 核心设计原则

| 原则 | 说明 | 示例 |
|------|------|------|
| **包最小化** | 每个包只导出必要的接口 | `transport` 包只导出 RPCClient/RPCServer |
| **内部包隔离** | 使用 `internal/` 隐藏实现 | `transport/internal/protocol` 对外不可见 |
| **接口隔离** | RPC 接口不依赖 Transport 实现细节 | `RPCClient` 隐藏 TCP/UDP 选择逻辑 |
| **依赖方向** | 外部依赖 RPC，RPC 依赖内部协议 | CLI → RPCClient → internal/protocol |

---

## 实施建议

### 阶段 1: 创建新包结构（不影响现有代码）

1. 创建 `transport/internal/protocol` 包
2. 创建 `transport/internal/frame` 包
3. 创建 `transport/internal/dispatcher` 包
4. 移动现有实现到对应的内部包

### 阶段 2: 重构 transport 包

1. 创建新的 `rpc_client.go` 和 `rpc_server.go`
2. 定义导出的 `RPCClient` 和 `RPCServer` 接口
3. 实现工厂函数，内部使用 `internal/protocol`
4. 添加废弃标记到旧的导出类型

### 阶段 3: 迁移外部依赖

1. 更新 `cluster` 包，使用新的 RPC 接口
2. 更新 `consensus` 包，使用新的 RPC 接口
3. 更新其他依赖 transport 的包

### 阶段 4: 清理和文档

1. 删除废弃的类型和函数
2. 更新包文档（`doc.go`）
3. 更新使用示例

---

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **破坏性变更** | 外部包需要更新导入路径 | 分阶段实施，先添加新接口再废弃旧接口 |
| **性能开销** | 接口封装可能带来性能损耗 | 基准测试验证，内联关键路径 |
| **测试覆盖** | 需要为新接口编写测试 | TDD 方法，先写测试再重构 |

---

## 参考资料

- Go 包组织最佳实践：https://go.dev/doc/effective_go#packages
- 内部包使用规范：https://go.dev/ref/mod#internal-packages
- 接口隔离原则（ISP）：SOLID 原则
- 现有代码位置：
  - `internal/metadata/transport/transport.go`
  - `internal/metadata/transport/rpc_client.go`
  - `internal/metadata/transport/rpc_server.go`

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-01-28
