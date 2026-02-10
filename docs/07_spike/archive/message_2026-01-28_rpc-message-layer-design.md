# RPC 消息层次设计

> **文档类型**: 📊 技术决策
> **创建日期**: 2026-01-28
> **状态**: ✅ 已决策
> **优先级**: P0 (高)

---

## 背景说明

在 PR-032 Pre 文档中定义了 `RPCRequestMessage` 和 `RPCResponseMessage`，但 transport 目录中也有消息类型定义。这违反了**单一职责原则**和**单一数据源原则**。

**核心问题**：
- 消息定义散落在多个地方（Pre 文档、transport 目录）
- RPC 语义和 Transport 传输职责混淆
- 缺少清晰的层次边界

**设计原则**：只在一个层定义消息，避免重复和混淆。

---

## 问题分析

### 当前状态

```
┌─────────────────────────────────────┐
│ PR-032 Pre 文档                      │
│ 定义：RPCRequestMessage              │
│       RPCResponseMessage             │
└─────────────────────────────────────┘
            ↓
        与 transport 定义重复？
            ↓
┌─────────────────────────────────────┐
│ transport 目录                       │
│ 定义：Message 接口                   │
│       MsgFrame 结构                  │
│       但缺少 RPC 消息定义            │
└─────────────────────────────────────┘
```

### 问题识别

| 问题 | 影响 | 严重性 |
|------|------|--------|
| **消息定义重复** | Pre 文档和代码中定义重复 | 高 |
| **职责混淆** | RPC 语义和 Transport 传输混在一起 | 高 |
| **缺少层次边界** | 无法清晰划分消息层次 | 中 |
| **维护困难** | 修改需要同步多个地方 | 中 |

---

## 设计方案：三层消息模型

### 整体架构

```
┌──────────────────────────────────────────────────────────────┐
│ Layer 3: RPC 层（调用语义）                                   │
│ 定义：RPCRequestMessage、RPCResponseMessage                  │
│ 职责：请求/响应匹配、Service/Method 路由、参数序列化          │
│ 位置：transport/rpc_messages.go                             │
└──────────────────────────────────────────────────────────────┘
                         ↓ MessagePack 序列化
┌──────────────────────────────────────────────────────────────┐
│ Layer 2: Transport 层（网络传输）                             │
│ 定义：MsgFrame、FrameHeader、TLV 扩展字段                     │
│ 职责：节点标识、消息序列号、协议路由（TCP/UDP）、帧编解码      │
│ 位置：transport/frame/frame.go                               │
└──────────────────────────────────────────────────────────────┘
                         ↓ TCP/UDP
┌──────────────────────────────────────────────────────────────┐
│ Layer 1: Network 层（TCP/UDP）                               │
│ 定义：TCP/UDP 数据包                                         │
└──────────────────────────────────────────────────────────────┘
```

### 消息流转示例

```
CLI 层调用：
  Call("TreeCoordinator.AddNode", {node_id: "node-2", addr: "192.168.1.2"})
       │
       ↓ RPC 消息（语义层）
┌──────────────────────────────────┐
│ RPCRequestMessage               │
│  - Service: "TreeCoordinator"   │
│  - Method: "AddNode"            │
│  - Params: {node_id, addr}      │
│  - RequestID: "123:456"         │
└──────┬───────────────────────────┘
       │
       ↓ 序列化
┌──────────────────────────────────┐
│ MessagePack Body                │
└──────┬───────────────────────────┘
       │
       ↓ 封装帧（传输层）
┌──────────────────────────────────┐
│ MsgFrame                        │
│  - NodeID: 123                  │
│  - MsgSeq: 456                  │
│  - MsgType: 0x01 (RPC Request)  │
│  - Body: [RPCMessage bytes]     │
└──────┬───────────────────────────┘
       │
       ↓ 网络传输
┌──────────────────────────────────┐
│ TCP Packet                     │
└──────────────────────────────────┘
```

---

## 详细设计

### Layer 3: RPC 消息（transport/rpc_messages.go）

```go
// Package transport 提供 RPC 消息定义
package transport

import (
    "fmt"

    "github.com/jzhang405/NexKV/internal/metadata/clock"
    "github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// RPC 消息类型（Layer 3: RPC 层语义）
// ========================================

// RPCRequestMessage RPC 请求消息
//
// 用于跨节点 RPC 调用，封装 Service、Method 和参数
type RPCRequestMessage struct {
    // RequestID 请求 ID（用于匹配请求-响应）
    // 格式："{NodeID}:{MsgSeq}"
    RequestID string `msgpack:"request_id"`

    // Service 目标服务名称（如 "TreeCoordinator"）
    Service string `msgpack:"service"`

    // Method 目标方法名称（如 "AddNode"）
    Method string `msgpack:"method"`

    // Params 方法参数（MessagePack 序列化的字节数组）
    // 调用方负责序列化，响应方负责反序列化
    Params []byte `msgpack:"params"`

    // Timestamp HLC 时间戳（用于排序和去重）
    Timestamp clock.HLCTimestamp `msgpack:"timestamp"`

    // ExpectResponse 是否期望响应（默认 true）
    ExpectResponse types.ExpectResponseFlag `msgpack:"expect_response"`
}

// RPCResponseMessage RPC 响应消息
//
// 用于返回 RPC 调用结果
type RPCResponseMessage struct {
    // RequestID 请求 ID（必须匹配请求中的 RequestID）
    RequestID string `msgpack:"request_id"`

    // Success 调用是否成功
    Success bool `msgpack:"success"`

    // Error 错误信息（失败时返回）
    Error string `msgpack:"error"`

    // Result 返回结果（MessagePack 序列化的字节数组）
    // 调用方负责反序列化
    Result []byte `msgpack:"result"`

    // Timestamp HLC 时间戳
    Timestamp clock.HLCTimestamp `msgpack:"timestamp"`
}

// ========================================
// 消息类型断言（types.Message 接口）
// ========================================

// Type 实现 types.Message 接口
func (m *RPCRequestMessage) Type() types.MessageType {
    return types.MessageTypeRPCRequest
}

// MsgRole 实现消息角色（请求/响应）
func (m *RPCRequestMessage) MsgRole() types.MessageRole {
    return types.MsgRoleRequest
}

// ProtocolType 返回协议类型（TCP/UDP）
func (m *RPCRequestMessage) ProtocolType() types.ProtocolType {
    return types.ProtocolTCP // RPC 默认使用 TCP
}

// ExpectResponse 返回是否期望响应
func (m *RPCRequestMessage) ExpectResponse() types.ExpectResponseFlag {
    return m.ExpectResponse
}

// Type 实现 types.Message 接口
func (m *RPCResponseMessage) Type() types.MessageType {
    return types.MessageTypeRPCResponse
}

// MsgRole 实现消息角色
func (m *RPCResponseMessage) MsgRole() types.MessageRole {
    return types.MsgRoleResponse
}

// ProtocolType 返回协议类型
func (m *RPCResponseMessage) ProtocolType() types.ProtocolType {
    return types.ProtocolTCP
}

// ========================================
// 辅助方法
// ========================================

// NewRPCRequestMessage 创建 RPC 请求消息
func NewRPCRequestMessage(
    nodeID, msgSeq uint64,
    service, method string,
    params []byte,
) *RPCRequestMessage {
    return &RPCRequestMessage{
        RequestID:      formatRequestID(nodeID, msgSeq),
        Service:        service,
        Method:         method,
        Params:         params,
        Timestamp:      clock.NewHLC().Now(),
        ExpectResponse: types.ExpectResponse,
    }
}

// NewRPCResponseMessage 创建 RPC 响应消息
func NewRPCResponseMessage(
    requestID string,
    success bool,
    errMsg string,
    result []byte,
) *RPCResponseMessage {
    return &RPCResponseMessage{
        RequestID: requestID,
        Success:   success,
        Error:     errMsg,
        Result:    result,
        Timestamp: clock.NewHLC().Now(),
    }
}

// formatRequestID 格式化请求 ID
func formatRequestID(nodeID, msgSeq uint64) string {
    return fmt.Sprintf("%d:%d", nodeID, msgSeq)
}

// ParseRequestID 解析请求 ID
func ParseRequestID(requestID string) (nodeID, msgSeq uint64, err error) {
    _, err = fmt.Sscanf(requestID, "%d:%d", &nodeID, &msgSeq)
    return nodeID, msgSeq, err
}
```

### Layer 2: Transport 帧（transport/frame/frame.go）

```go
// MsgFrame 网络帧（Layer 2: Transport 层）
//
// 只负责传输，不关心 RPC 语义
type MsgFrame struct {
    // Header 帧头（16 字节）
    Header  FrameHeader

    // Body 消息体（已反序列化的 Message）
    Message types.Message

    // TLV 扩展字段
    TLV []TLVField

    // CorrelationID 关联 ID（从 RequestID 提取）
    correlationID string

    // SourceAddr 源地址
    SourceAddr string

    // ConnID TCP 连接 ID（用于连接复用）
    ConnID string
}

// FrameHeader 帧头（Layer 2: Transport 层）
type FrameHeader struct {
    NodeID  uint64  // 发送节点 ID（8 字节）
    MsgSeq  uint32  // 消息序列号（4 字节）
    MsgType uint16  // 消息类型（2 字节）
    BodyLen uint16  // 消息体长度（2 字节）
}

// CorrelationID 获取关联 ID
func (f *MsgFrame) CorrelationID() string {
    if f.correlationID != "" {
        return f.correlationID
    }

    // 从 RPC 消息中提取 RequestID
    if rpcMsg, ok := f.Message.(*RPCRequestMessage); ok {
        return rpcMsg.RequestID
    }
    return ""
}
```

---

## 目录结构（更新）

```
internal/metadata/transport/
├── rpc_messages.go           # 【新增】RPC 消息定义（Layer 3）
├── rpc_client.go             # RPC 客户端实现
├── rpc_server.go             # RPC 服务端实现
├── frame/
│   └── frame.go             # 网络帧定义（Layer 2）
├── codec/
│   └── msgpack_codec.go     # MessagePack 编解码器（Layer 2）
└── internal/                # 内部实现
    ├── protocol/            # 协议实现（Layer 1）
    │   ├── tcp_transport.go
    │   └── udp_transport.go
    └── dispatcher/          # 消息分发器
        └── dispatcher.go
```

---

## 关键设计原则

| 原则 | 说明 | 示例 |
|------|------|------|
| **职责分离** | RPC 层定义调用语义，Transport 层定义传输格式 | RPCRequestMessage vs MsgFrame |
| **单向依赖** | RPC 层依赖 Transport 层，反之不依赖 | RPC → Transport → Network |
| **单一数据源** | 每层消息只在一个地方定义 | `transport/rpc_messages.go` |
| **接口适配** | RPC 消息实现 `types.Message` 接口 | `Type()`, `MsgRole()` 方法 |
| **序列化隔离** | RPC 层使用 MessagePack，Transport 层只传输字节 | `Params []byte` 不关心内容 |

---

## 消息类型映射

| Layer | 消息类型 | 文件位置 | 职责 |
|-------|---------|---------|------|
| **Layer 3** | RPCRequestMessage | `transport/rpc_messages.go` | 调用语义（Service/Method） |
| **Layer 3** | RPCResponseMessage | `transport/rpc_messages.go` | 响应语义（Success/Result） |
| **Layer 3** | QuorumProposeMessage | `transport/quorum_messages.go` | 投票语义 |
| **Layer 3** | GossipDigestMessage | `transport/gossip_messages.go` | 同步语义 |
| **Layer 2** | MsgFrame | `transport/frame/frame.go` | 传输格式（NodeID/MsgSeq） |
| **Layer 2** | FrameHeader | `transport/frame/frame.go` | 帧头格式 |
| **Layer 1** | TCP Packet | 操作系统内核 | 网络传输 |

---

## 使用示例

### 发送 RPC 请求

```go
// 1. 构造 RPC 请求消息（Layer 3）
params, _ := msgpack.Marshal(map[string]interface{}{
    "node_id": "node-2",
    "addr":    "192.168.1.2:9211",
})

rpcMsg := transport.NewRPCRequestMessage(
    nodeID,      // 123
    msgSeq,      // 456
    "TreeCoordinator",
    "AddNode",
    params,
)

// 2. 封装到 Transport 帧（Layer 2）
msgFrame := transport.MsgFrame{
    Message: rpcMsg,  // 实现 types.Message 接口
    // Transport 层自动填充 Header
}

// 3. 发送（自动序列化）
transport.Send(ctx, addr, msgFrame)
```

### 接收和处理 RPC 请求

```go
// 1. Transport 层接收（Layer 2）
for msgFrame := range transport.Receive() {
    // msgFrame.Message 已反序列化
}

// 2. 类型断言获取 RPC 消息（Layer 3）
if rpcMsg, ok := msgFrame.Message.(*transport.RPCRequestMessage); ok {
    // 3. 路由到具体 Service 和 Method
    handler := router.GetHandler(rpcMsg.Service)
    result := handler.Call(rpcMsg.Method, rpcMsg.Params)

    // 4. 构造响应（Layer 3）
    respMsg := transport.NewRPCResponseMessage(
        rpcMsg.RequestID,
        true,  // Success
        "",    // Error
        result,
    )

    // 5. 发送响应
    transport.Reply(ctx, sourceAddr, respMsg, nodeID, msgSeq, connID)
}
```

---

## 迁移计划

### 阶段 1: 创建新文件（不影响现有代码）

1. 创建 `transport/rpc_messages.go`
2. 定义 `RPCRequestMessage` 和 `RPCResponseMessage`
3. 实现 `types.Message` 接口方法

### 阶段 2: 更新 RPCClient 和 RPCServer

1. 更新 `transport/rpc_client.go`，使用新的 RPC 消息类型
2. 更新 `transport/rpc_server.go`，使用新的 RPC 消息类型
3. 确保与现有代码兼容

### 阶段 3: 清理和文档

1. 删除 Pre 文档中的重复消息定义
2. 更新 PR-032 Pre 文档的 3.6.6 节（消息类型体系）
3. 添加消息层次设计说明

---

## 参考资料

- **三层模型**：OSI 网络模型启发（物理层 → 数据链路层 → 网络层）
- **职责分离**：SOLID 原则中的单一职责原则（SRP）
- **接口隔离**：SOLID 原则中的接口隔离原则（ISP）
- **相关文档**：
  - PR-032 Pre 文档：`docs/06_project_management/pr_documents/feature/2026-01-27_PR-032_节点管理增强_全流程.md`
  - Transport 重构方案：`docs/06_project_management/brainstorm/transport_2026-01-28_rpc-interface-design.md`

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-01-28
**状态**: ✅ 已决策，等待实施
