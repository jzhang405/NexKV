# Transport Layer Code Review Report

**审查日期**: 2026-01-25
**审查范围**: `internal/metadata/transport` 全部代码
**审查人员**: AI Code Reviewer Agent
**代码版本**: main (commit 8fbe7c0)

---

## 📋 目录

1. [架构概览](#架构概览)
2. [类图设计](#类图设计)
3. [时序图分析](#时序图分析)
4. [对上层接口](#对上层接口)
5. [P0 关键问题](#p0-关键问题)
6. [P1 中等问题](#p1-中等问题)
7. [P2 低优先级问题](#p2-低优先级问题)
8. [修复优先级建议](#修复优先级建议)

---

## 架构概览

### 🎯 Code Review 原则

**分层架构** - Transport Layer 采用清晰的分层设计，便于理解和维护

**接口抽象** - 使用 `Transport` 接口实现依赖倒置原则（SOLID-D）

**多协议支持** - 通过 `MultiTransport` 实现协议聚合和自动降级

**可靠性保证** - `RPCTransport` 提供请求-应答语义，支持超时和重试

### 📦 包结构

```
internal/metadata/transport/
├── transport.go          # 核心接口定义（Transport, Codec, Message）
├── frame.go              # TLV 帧格式定义（固定头 + 变长扩展头 + 数据 + CRC）
├── codec.go              # 编解码器接口和实现
├── msg_frame.go          # 消息帧封装（实现 Message 接口）
├── tcp_transport.go      # TCP 传输实现（连接池、保活、并发读写）
├── udp_transport.go      # UDP 传输实现（分片重组、超时清理）
├── multi_transport.go    # 多协议聚合传输（协议注册、路由、降级）
├── rpc_transport.go      # RPC 请求-应答传输层（超时、重试、reqTable）
└── *_test.go             # 单元测试和集成测试
```

### 🏗️ 架构分层

```mermaid
graph TB
    subgraph "应用层"
        APP["业务应用"]
    end

    subgraph "RPC 层 (rpc_transport.go)"
        RPC["RPCTransport<br/>请求-应答语义<br/>超时控制<br/>reqTable 管理"]
    end

    subgraph "聚合层 (multi_transport.go)"
        MULTI["MultiTransport<br/>协议注册<br/>路由决策<br/>自动降级"]
    end

    subgraph "传输层"
        TCP["TCPTransport<br/>连接池<br/>保活机制"]
        UDP["UDPTransport<br/>分片重组<br/>超时清理"]
    end

    subgraph "帧层 (frame.go)"
        FRAME["Frame<br/>TLV 格式<br/>CRC 校验"]
    end

    subgraph "编解码层 (codec.go)"
        CODEC["Codec<br/>MessagePack<br/>JSON<br/>Protobuf"]
    end

    APP --> RPC
    RPC --> MULTI
    MULTI --> TCP
    MULTI --> UDP
    TCP --> FRAME
    UDP --> FRAME
    FRAME --> CODEC

    style APP fill:#e1f5ff
    style RPC fill:#fff4e6
    style MULTI fill:#f3e5f5
    style TCP fill:#e8f5e9
    style UDP fill:#e8f5e9
    style FRAME fill:#fce4ec
    style CODEC fill:#fce4ec
```

---

## 类图设计

### 核心接口层次关系

```mermaid
classDiagram
    %% 核心接口
    class Transport {
        <<interface>>
        +Start(nodeID, stopCh) error
        +Stop() error
        +Send(ctx, addr, msg) error
        +Receive() ~chan MsgFrame
        +ForwardMessage(ctx, addrs, msg) error
        +GetNodeID() uint64
        +GenerateMsgSeq() uint64
    }

    class BatchForwardTransport {
        <<interface>>
        +BatchForwardMessage(ctx, addrs, msg) BatchResult
    }

    class Codec {
        <<interface>>
        +Encode(msg) []byte
        +Decode(data) (Message, error)
        +Name() string
        +Type() CodecType
    }

    class Message {
        <<interface>>
        +Type() MessageType
        +Priority() int
        +ExpectResponse() ResponseExpectation
        +Reliability() ReliabilityRequirement
    }

    class Conn {
        <<interface>>
        +Read(msg) (MsgFrame, error)
        +Write(msg) error
        +Close() error
        +RemoteAddr() string
        +SetDeadline(t) error
    }

    %% 实现 Transport 接口
    class TCPTransport {
        -config TransportConfig
        -codec Codec
        -connPool connPool
        -recvCh chan MsgFrame
        +Start(nodeID, stopCh) error
        +Stop() error
        +Send(ctx, addr, msg) error
        +Receive() ~chan MsgFrame
        +ForwardMessage(ctx, addrs, msg) error
        +GetNodeID() uint64
        +GenerateMsgSeq() uint64
    }

    class UDPTransport {
        -config TransportConfig
        -codec Codec
        -fragmentBuf fragmentBuffer
        -recvCh chan MsgFrame
        +Start(nodeID, stopCh) error
        +Stop() error
        +Send(ctx, addr, msg) error
        +Receive() ~chan MsgFrame
        +ForwardMessage(ctx, addrs, msg) error
        +GetNodeID() uint64
        +GenerateMsgSeq() uint64
    }

    class MultiTransport {
        -config TransportConfig
        -protocols map[ProtocolType]ProtocolTransport
        -router Router
        -degradationManager DegradationManager
        +RegisterProtocol(cfg) error
        +Start(nodeID, stopCh) error
        +Stop() error
        +Send(ctx, addr, msg) error
        +Receive() ~chan MsgFrame
        +GetNodeID() uint64
        +GenerateMsgSeq() uint64
    }

    class RPCTransport {
        -transport Transport
        -reqTable sync.Map
        -defaultTimeout time.Duration
        -maxReqTableSize int64
        +SendRequest(target, body, timeout) ([]byte, error)
        +SendResponse(target, msgID, body, isError) error
        +OnRecv(nodeID, data)
        +GetStats() map[string]interface{}
        +Close() error
    }

    %% 帧相关
    class Frame {
        +FixedHeader FixedHeader
        +VarExtHeader VarExtHeader
        +Data []byte
        +CRC32 uint32
        +Marshal() []byte
        +Unmarshal(data) error
        +WithCompress(id) *Frame
        +WithFragment(index, total) *Frame
        +WithPriority(p) *Frame
    }

    class MsgFrame {
        -frame *Frame
        +Unwrap() Message
        +GetSourceNodeID() uint64
        +GetMsgSeq() uint64
        +GetType() MessageType
        +GetProtocol() ProtocolType
    }

    %% 内部组件
    class connPool {
        -mu sync.RWMutex
        -conns map[string]*tcpConn
        +getConn(addr) (*tcpConn, error)
        +addConn(addr, conn)
        +removeConn(addr)
    }

    class fragmentBuffer {
        -mu sync.RWMutex
        -buffers map[fragmentKey]*partialMessage
        -timeout time.Duration
        +addFragment(key, index, total, data) error
        +reassembleMessage(key) (*Frame, error)
        +cleanupExpired()
    }

    class RequestCtx {
        +MsgID uint64
        +RespCh chan []byte
        +ErrorCh chan error
        +CreatedAt time.Time
        +Cancel context.CancelFunc
    }

    %% 关系
    Transport <|-- TCPTransport : implements
    Transport <|-- UDPTransport : implements
    Transport <|-- MultiTransport : implements
    BatchForwardTransport <|-- TCPTransport : implements
    BatchForwardTransport <|-- UDPTransport : implements
    BatchForwardTransport <|-- MultiTransport : implements

    TCPTransport +-- connPool : contains
    UDPTransport +-- fragmentBuffer : contains
    RPCTransport o-- Transport : depends on
    RPCTransport +-- RequestCtx : manages
    Frame *-- MsgFrame : wraps

    MultiTransport o-- Transport : aggregates
```

### 类详细说明

#### Transport 接口
所有传输层必须实现的核心接口：
- `Start()` - 启动传输层，传入 nodeID 和 stopCh
- `Stop()` - 停止传输层，清理资源
- `Send()` - 发送消息到指定地址
- `Receive()` - 接收消息通道（异步）
- `ForwardMessage()` - 转发消息到多个地址
- `GetNodeID()` - 获取本地节点 ID
- `GenerateMsgSeq()` - 生成全局唯一消息序号

#### RPCTransport (上层接口)
**作用**: 提供请求-应答语义，支持超时和重试

**核心方法**:
- `SendRequest(target, body, timeout) -> ([]byte, error)` - 发送请求并等待响应
- `SendResponse(target, msgID, body, isError) -> error` - 发送响应
- `OnRecv(nodeID, data)` - 处理接收到的消息（自动匹配 reqTable）

**reqTable 管理**:
```go
type RequestKey struct {
    NodeID uint64  // 目标节点 ID
    MsgID  uint64  // 消息序列号
}

type RequestCtx struct {
    MsgID     uint64
    RespCh    chan []byte     // 响应通道
    ErrorCh   chan error       // 错误通道
    CreatedAt time.Time       // 创建时间（超时清理）
    Cancel    context.CancelFunc
}
```

#### Frame 结构
```go
type Frame struct {
    FixedHeader  FixedHeader    // 固定头 (24 字节)
    VarExtHeader VarExtHeader   // 变长扩展头
    Data         []byte         // 数据体
    CRC32        uint32         // CRC32 校验
}

// 固定头结构
type FixedHeader struct {
    Magic         uint16  // 魔数: 0xCAFE
    Version       uint8   // 版本: 1
    Flags         uint8   // 标志位
    NodeID        uint64  // 源节点 ID
    MsgSeq        uint64  // 消息序号
    ForwardNodeID uint64  // 转发节点 ID
    Hops          uint8   // 跳数
    Reserved      uint8   // 保留
    MsgType       uint8   // 消息类型
    CodecID       uint16  // 编解码器 ID
    ExtHeaderLen  uint16  // 扩展头长度
    DataLength    uint32  // 数据长度
}
```

---

## 时序图分析

### TCP Transport 时序图

```mermaid
sequenceDiagram
    participant App as 应用层
    participant TCP as TCPTransport
    participant Pool as connPool
    participant Conn as tcpConn
    participant Net as 网络

    == Start ==
    App->>TCP: Start(nodeID, stopCh)
    TCP->>TCP: startListener()
    TCP->>TCP: acceptLoop()
    TCP->>TCP: startConnPoolManager()
    TCP-->>App: nil (启动成功)

    == Send ==
    App->>TCP: Send(ctx, addr, msg)
    TCP->>Pool: getOrCreateConn(addr)
    Pool->>Pool: 检查连接池
    alt 连接存在
        Pool-->>TCP: conn (已存在)
    else 连接不存在
        Pool->>Net: dial(addr)
        Net-->>Pool: conn
        Pool->>Pool: addConnToPool(addr, conn)
        Pool-->>TCP: conn (新创建)
    end
    TCP->>Conn: Write(msg)
    Conn->>Net: 发送数据
    Net-->>TCP: 发送成功
    TCP-->>App: nil

    == Receive (异步) ==
    Net->>TCP: 接收到连接
    TCP->>TCP: acceptLoop() 接受
    TCP->>Conn: wrapConn()
    Conn->>Conn: ReadMsgFrame()
    Conn->>Net: 读取数据
    Net-->>Conn: Frame 数据
    Conn->>TCP: recvCh <- frame
    TCP->>App: Receive() <- frame

    == ForwardMessage ==
    App->>TCP: ForwardMessage(ctx, addrs, msg)
    loop 遍历 addrs
        TCP->>Pool: getOrCreateConn(addr)
        TCP->>Conn: Write(msg)
    end

    == Stop ==
    App->>TCP: Stop()
    TCP->>TCP: closeAllConns()
    TCP->>Pool: 关闭所有连接
    TCP->>Conn: Close()
    TCP->>TCP: 停止监听
    TCP-->>App: nil
```

### UDP Transport 时序图

```mermaid
sequenceDiagram
    participant App as 应用层
    participant UDP as UDPTransport
    participant Buf as fragmentBuffer
    participant Net as UDP Conn

    == Start ==
    App->>UDP: Start(nodeID, stopCh)
    UDP->>UDP: initFragmentBuffer()
    UDP->>Buf: startCleanup()
    UDP->>UDP: receiveLoop()
    UDP-->>App: nil

    == Send (小消息) ==
    App->>UDP: Send(ctx, addr, msg)
    UDP->>UDP: encodeFrame()
    UDP->>UDP: sendDirectWithOptions()
    UDP->>Net: Write(data)
    Net-->>UDP: 发送成功
    UDP-->>App: nil

    == Send (大消息 > MTU) ==
    App->>UDP: Send(ctx, addr, msg)
    UDP->>UDP: encodeFrame()
    UDP->>UDP: sendFragmentedWithOptions()
    loop 分片发送
        UDP->>UDP: 创建 Fragment Frame
        UDP->>Net: Write(fragment)
        Net-->>UDP: 发送成功
    end
    UDP-->>App: nil

    == Receive (分片重组) ==
    Net->>UDP: 接收分片
    UDP->>UDP: processReceivedData()
    UDP->>UDP: parseFrame()
    UDP->>Buf: addFragment(key, index, total, data)

    alt 分片未完整
        Buf-->>UDP: nil (等待更多分片)
    else 分片完整
        Buf->>Buf: reassembleMessage()
        Buf-->>UDP: complete Frame
        UDP->>UDP: sendToReceiveChannel()
        UDP->>App: recvCh <- frame
    end

    == 超时清理 ==
    Note over Buf: 每 timeout 时间清理
    Buf->>Buf: cleanupExpiredFragments()
    Buf->>Buf: 删除超时的 partialMessage

    == Stop ==
    App->>UDP: Stop()
    UDP->>UDP: 关闭 UDP 连接
    UDP->>Buf: 停止清理
    UDP-->>App: nil
```

### RPCTransport 时序图

```mermaid
sequenceDiagram
    participant App as 应用层
    participant RPC as RPCTransport
    participant Transport as Transport
    participant ReqTable as reqTable

    == SendRequest (双向消息) ==
    App->>RPC: SendRequest(target, body, timeout)
    RPC->>RPC: decodeMessage(body)
    RPC->>RPC: 检查 ExpectResponse

    alt ExpectResponse == NoResponse (单向)
        RPC->>Transport: Send(ctx, target, reqMsg)
        Transport-->>RPC: nil
        RPC-->>App: nil, nil (立即返回)
    else ExpectResponse == ExpectResponse (双向)
        RPC->>RPC: 检查 reqTable 容量
        RPC->>ReqTable: 创建 RequestCtx
        RPC->>ReqTable: Store(key, ctx)
        RPC->>Transport: Send(ctx, target, reqMsg)

        alt 超时
            App->>App: 等待超时
            RPC->>ReqTable: Delete(key)
            RPC-->>App: nil, timeout error
        else 收到响应
            Note over RPC,ReqTable: OnRecv 被调用
            RPC->>ReqTable: Load(key)
            RPC->>ReqTable: RespCh <- response
            ReqTable-->>App: response, nil
        end
    end

    == OnRecv (处理响应) ==
    Transport->>RPC: OnRecv(nodeID, data)
    RPC->>RPC: decodeMessage(data)

    alt 是响应 (!IsRequest)
        RPC->>ReqTable: Load(key)
        alt reqTable 存在
            RPC->>ReqTable: 发送响应或错误
        else reqTable 不存在
            Note over RPC: 可能已超时
        end
    else 是请求 (IsRequest)
        Note over RPC: TODO: 调用业务层处理
    end

    == SendResponse ==
    App->>RPC: SendResponse(target, msgID, body, isError)
    RPC->>RPC: 构造响应消息
    RPC->>Transport: Send(ctx, target, respMsg)
    Transport-->>RPC: nil
    RPC-->>App: nil

    == 定期清理 ==
    Note over RPC: 每 1 分钟
    RPC->>ReqTable: cleanupExpiredRequests()
    RPC->>ReqTable: 删除超时的 RequestCtx
```

### MultiTransport 时序图

```mermaid
sequenceDiagram
    participant App as 应用层
    participant Multi as MultiTransport
    participant Router as Router
    participant Degradation as DegradationManager
    participant TCP as TCPTransport
    participant UDP as UDPTransport

    == 注册协议 ==
    App->>Multi: RegisterProtocol(TCP)
    Multi->>Multi: protocols[TCP] = ProtocolTransport
    App->>Multi: RegisterProtocol(UDP)
    Multi->>Multi: protocols[UDP] = ProtocolTransport

    == Start ==
    App->>Multi: Start(nodeID, stopCh)
    Multi->>TCP: Start(nodeID, stopCh)
    Multi->>UDP: Start(nodeID, stopCh)
    Multi->>Multi: receiveLoop()
    Multi->>Multi: overflowLoop()
    Multi-->>App: nil

    == Send (路由决策) ==
    App->>Multi: Send(ctx, addr, msg)
    Multi->>Router: RouteDecision(addr, msg)
    Router-->>Multi: selectedProtocol

    alt 无需降级
        Multi->>TCP: Send(ctx, addr, msg)
        TCP-->>Multi: nil
        Multi-->>App: nil
    else 需要降级
        Multi->>Degradation: ShouldDegrade(protocol)
        Degradation-->>Multi: 降级协议
        Multi->>UDP: Send(ctx, addr, msg)
        UDP-->>Multi: nil
        Multi-->>App: nil
    end

    == Receive (聚合接收) ==
    Multi->>Multi: receiveLoop()
    alt 接收通道未满
        TCP->>Multi: recvCh <- frame
        Multi->>App: Receive() <- frame
    else 接收通道已满
        TCP->>Multi: overflowCh <- frame
        Multi->>Multi: overflowLoop() 处理
        Note over Multi: 记录监控指标，丢弃消息
    end

    == ForwardMessage (批量转发) ==
    App->>Multi: ForwardMessage(ctx, addrs, msg)
    Multi->>Router: BatchRouteDecision(addrs, msg)

    alt 使用默认协议
        Multi->>TCP: BatchForwardMessage(ctx, addrs, msg)
        TCP-->>Multi: BatchResult
    else 按地址路由
        loop 遍历 addrs
            Multi->>Router: RouteDecision(addr)
            alt addr == TCP
                Multi->>TCP: Send(ctx, addr, msg)
            else addr == UDP
                Multi->>UDP: Send(ctx, addr, msg)
            end
        end
    end

    == Stop ==
    App->>Multi: Stop()
    Multi->>TCP: Stop()
    Multi->>UDP: Stop()
    Multi-->>App: nil
```

---

## 对上层接口

### RPCTransport 接口设计

**定位**: Transport Layer 的上层封装，提供请求-应答语义

```go
type RPCTransport struct {
    transport       Transport         // 底层传输（依赖倒置）
    reqTable        sync.Map          // 请求等待表
    defaultTimeout  time.Duration     // 默认超时
    maxReqTableSize int64             // 容量限制（DoS 防护）
    cleanupTicker   *time.Ticker      // 定期清理
    timeoutCount    atomic.Uint64     // 超时统计
    totalRequest    atomic.Uint64     // 请求统计
    totalLatencyNs  atomic.Uint64     // 延迟统计
}
```

#### SendRequest 方法详解

**签名**: `SendRequest(targetNode string, reqBody []byte, timeout time.Duration) ([]byte, error)`

**行为**:
1. 解析 reqBody 获取 Message 接口
2. 调用 `Message.ExpectResponse()` 检查是否需要响应
3. **单向模式** (`NoResponse`): 发送后立即返回 `nil, nil`
4. **双向模式** (`ExpectResponse`):
   - 创建 reqTable 条目
   - 阻塞等待响应或超时
   - 超时后自动清理 reqTable

**流程图**:
```mermaid
flowchart TD
    A["SendRequest(target, body, timeout)"] --> B["解析 Message"]
    B --> C["检查 ExpectResponse"]

    C -->|NoResponse| D["发送单向消息"]
    D --> E["立即返回 nil, nil"]

    C -->|ExpectResponse| F["检查 reqTable 容量"]
    F -->|已满| G["返回错误: reqTable 已满"]
    F -->|未满| H["创建 RequestCtx"]
    H --> I["存储到 reqTable"]
    I --> J["发送请求"]
    J --> K{"等待响应"}

    K -->|超时| L["清理 reqTable"]
    L --> M["返回超时错误"]

    K -->|收到响应| N["更新统计指标"]
    N --> O["返回响应数据"]

    style D fill:#e8f5e9
    style E fill:#e8f5e9
    style G fill:#ffcdd2
    style M fill:#ffcdd2
```

#### OnRecv 方法详解

**签名**: `OnRecv(nodeID string, data []byte)`

**行为**:
1. 解码消息（带容错处理）
2. **如果是响应** (`!IsRequest`):
   - 从 reqTable 查找对应的 RequestCtx
   - 发送响应到 `RespCh` 或错误到 `ErrorCh`
3. **如果是请求** (`IsRequest`):
   - 检查 `ExpectResponse`
   - **单向消息**: 只处理，不发送响应
   - **双向消息**: TODO: 调用业务层处理

#### GetStats 监控指标

```go
{
    "activeRequestCount": int64,   // 活跃请求数
    "timeoutCount":       uint64,   // 超时次数
    "totalRequestCount":  uint64,   // 总请求数
    "avgLatency":         string,   // 平均延迟
    "maxReqTableSize":    int64,    // 最大容量
}
```

---

## 📊 审查总结

| 优先级 | 数量 | 状态 |
|--------|------|------|
| **P0 (关键)** | 3 | ⚠️ 需要修复 |
| **P1 (中等)** | 3 | ⚠️ 建议修复 |
| **P2 (低)** | 3 | 可选优化 |

---

## 🚨 P0: 关键问题 (必须修复)

### P0-1: TCP 连接池 TOCTOU 竞态条件
**文件**: `tcp_transport.go:577-596`
**严重程度**: ⚠️ **Critical** - 可能导致连接泄漏和重复拨号
**置信度**: 95

**问题描述**:
```go
// 第一次检查：快速路径（无锁）
conn := t.getConnFromPool(addr)
if conn != nil && !conn.isClosed() {
    return conn, nil
}

// 需要创建新连接，加锁避免重复拨号
t.connPool.mu.Lock()
defer t.connPool.mu.Unlock()

// 第二次检查：其他协程可能已创建连接
conn = t.connPool.conns[addr]  // ⚠️ 问题：未检查 conn.isClosed()
if conn != nil && !conn.isClosed() {
    return conn, nil
}
```

**修复建议**:
```go
// 第二次检查必须调用 isClosed()
conn = t.connPool.conns[addr]
if conn != nil && !conn.isClosed() {  // ✅ 必须检查
    return conn, nil
}
```

---

### P0-2: UDP 分片重组缺少并发保护
**文件**: `udp_transport.go:611-620`
**严重程度**: ⚠️ **Critical** - 数据竞争（data race）
**置信度**: 92

**问题描述**:
```go
// 快速路径：total <= 64（无锁）
if total <= 64 {
    partial.bitmapFast |= (1 << index)  // ⚠️ 无并发保护
}
```

**修复建议**:
```go
// 使用 atomic.Uint64（推荐）
type partialMessage struct {
    bitmapFast atomic.Uint64  // ✅ 原子操作
}

// 使用:
old := partial.bitmapFast.Load()
partial.bitmapFast.Store(old | (1 << index))
```

---

### P0-3: RPC Transport reqTable 缺少优先级拒绝机制
**文件**: `rpc_transport.go:255-257`
**严重程度**: ⚠️ **Critical** - DoS 攻击风险
**置信度**: 88

**问题描述**:
- 已检查容量限制，但所有新请求一视同仁拒绝
- 恶意客户端可以填满 reqTable，阻断正常业务

**修复建议**:
```go
// 按消息类型分类限制
if r.reqTableSize.Load() >= r.maxReqTableSize {
    // 允许高优先级请求（心跳、Leader 选举）
    if msg.Priority() >= int(types.PriorityHigh) {
        r.evictLowestPriorityRequest()
    } else {
        return nil, fmt.Errorf("请求等待表已满")
    }
}
```

---

## ⚠️ P1: 中等优先级问题

### P1-1: TCP keep-alive 配置不完整
**文件**: `tcp_transport.go:282-295`
**置信度**: 85

**问题**: 未设置 `TCP_KEEPIDLE`、`TCP_KEEPCNT`、`TCP_KEEPINTVL`

---

### P1-2: MultiTransport 协议降级逻辑死锁风险
**文件**: `multi_transport.go:496-530`
**置信度**: 82

**问题**: 持有 `stateMu.Lock()` 期间调用 `degradationManager.ShouldDegrade()`

---

### P1-3: UDP 分片超时清理可能导致内存泄漏
**文件**: `udp_transport.go:691-735`
**置信度**: 80

**问题**: 单次清理可能处理过多过期分片，长时间持锁

---

## 📝 P2: 低优先级问题

### P2-1: Magic number 应使用命名常量
**文件**: `frame.go:569-570`
**置信度**: 75

---

### P2-2: 错误处理不够细化
**文件**: `tcp_transport.go:347-351`
**置信度**: 72

---

### P2-3: 编解码器缓存可能导致内存泄漏
**文件**: `udp_transport.go:437-468`
**置信度**: 70

---

## ✅ 已修复的问题

根据提交历史，以下 P0 问题已在之前修复：

- ✅ **P0-UDP-001**: UDP bitmap overflow when total=64
- ✅ **P0-Multi-002**: MultiTransport duplicate select case
- ✅ **P0-RPC-003**: RPC ExpectResponse enum validation

---

## 🎯 修复优先级建议

1. **立即修复**: P0-1, P0-2（数据竞争和资源泄漏）
2. **尽快修复**: P0-3, P1-1（DoS 防护和稳定性）
3. **计划修复**: P1-2, P1-3, P2-1, P2-2, P2-3

---

**关联 PR**: #25
**下一步**: 根据此报告创建修复任务
