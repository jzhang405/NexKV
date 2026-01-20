# TCP/UDP 统一 TLV 消息协议详细设计文档

> **文档类型**: 详细设计文档 (DDD)
> **创建日期**: 2026-01-20
> **版本**: v2.0 (TLV 协议)
> **状态**: 📋 待评审
> **维护者**: NexKV 开发团队
> **相关 Brainstorm**: `transport_2026-01-20_tcp-udp-unified-tlv-protocol.md`

---

## 1. 概述

### 1.1 设计目标

本设计采用 **「固定头 + 变长 TLV 扩展头 + MessagePack 序列化 + CRC32 校验」** 的统一消息协议，实现：

1. **轻量级**：固定头仅 16 字节，最小化协议开销
2. **灵活扩展**：TLV 格式支持按需扩展，零冗余
3. **高效序列化**：MessagePack 替代手动字节拼接
4. **完整可靠**：CRC32 校验覆盖扩展头+业务数据
5. **跨语言兼容**：基于标准二进制协议，支持多语言

### 1.2 核心创新

| 特性 | 传统方案 | TLV 方案 | 改进 |
|------|---------|---------|------|
| **扩展机制** | 固定预留字段（4字节）| TLV 变长扩展 | ✅ 按需扩展，无限可能 |
| **序列化方式** | 手动字节拼接 | MessagePack 自动 | ✅ 支持复杂结构 |
| **校验范围** | 仅 Header CRC | CRC32 覆盖扩展头+数据 | ✅ 完整性保障 |
| **UDP 分片** | 网络层 IP 分片 | 应用层 TLV 分片 | ✅ 可靠分片+重组 |

### 1.3 适用场景

| 场景 | 协议 | 分片策略 | 原因 |
|------|------|---------|------|
| **心跳消息** | UDP | 不分片（< 1KB）| 低延迟优先 |
| **元数据同步** | UDP | 可能分片（1-60KB）| 最终一致性 |
| **数据迁移** | TCP | 不分片（可靠传输）| 大数据，可靠性 |
| **关键变更** | TCP | 不分片（可靠传输）| 强一致性 |

### 1.4 设计原则

遵循 SOLID 原则和 DRY 原则：

- **S**（单一职责）：固定头/TLV/分片/重组各司其职
- **O**（开闭原则）：新增 TLV Type 即可扩展，无需修改核心
- **D**（依赖倒置）：依赖 Transport 接口，而非具体实现
- **DRY**：TLV 序列化、CRC 校验在 TCP/UDP 间复用

---

## 2. 协议设计

### 2.1 完整消息结构

```
+==================+==================+==================+==================+
│ FixedHeader      │ VarExtHeader     │ Data             │ CRC32            │
│ (16 bytes)       │ (0~65535 bytes)  │ (变长)           │ (4 bytes)        │
+==================+==================+==================+==================+
│ 魔术字 + 节点ID   │ TLV扩展字段列表   │ 业务数据         │ CRC32-IEEE       │
│ 消息ID + 编码器   │ (按需携带)        │ (KV操作)        │                  │
+==================+==================+==================+==================+
```

**最小消息长度**：16 (固定头) + 2 (ExtTotalLen=0) + 0 (数据) + 4 (CRC) = **22 字节**

### 2.2 固定头（FixedHeader，16字节）

| 字段名 | 大小 | 类型 | 说明 |
|--------|------|------|------|
| **Magic** | 4字节 | []byte | 魔术字 `0x4E 0x58 0x55 0x54`（"NXUT"）|
| **NodeID** | 8字节 | uint64 | 发送节点 ID（全局唯一）|
| **MsgID** | 2字节 | uint16 | 消息 ID（节点内唯一，用于去重和分片匹配）|
| **CodecID** | 2字节 | uint16 | 业务数据编码器：0=JSON, 1=MessagePack, 2=Protobuf |

**字节序**：所有数值字段采用 **Big-Endian（网络字节序）**

### 2.3 变长扩展头（VarExtHeader，TLV 格式）

#### 2.3.1 整体结构

```
+==================+==================+
│ ExtTotalLen      │ TLV Fields[]     │
│ (2 bytes)        │ (变长)           │
+==================+==================+
```

| 字段 | 大小 | 说明 |
|------|------|------|
| **ExtTotalLen** | 2字节 | TLV 字段列表总字节数，0 表示无扩展 |
| **TLV Fields[]** | 变长 | TLV 字段列表，每个字段独立 |

#### 2.3.2 单个 TLV 字段结构

```
+==================+==================+==================+
│ Type             │ Length           │ Value            │
│ (2 bytes)        │ (2 bytes)        │ (变长)           │
+==================+==================+==================+
```

| 字段 | 大小 | 说明 |
|------|------|------|
| **Type** | 2字节 | 扩展字段类型（预定义枚举）|
| **Length** | 2字节 | Value 部分的字节长度 |
| **Value** | 变长 | MessagePack 序列化后的字节流 |

#### 2.3.3 预定义 TLV Type 枚举

| Type 值 | 字段名 | Go 结构体 | 适用场景 |
|---------|--------|----------|---------|
| **1** | 压缩配置 | `CompressExt{CompressID uint16}` | 大 Value 传输压缩 |
| **2** | 加密配置 | `EncryptExt{EncryptID uint16, Nonce []byte, Version string}` | 敏感数据加密 |
| **3** | 分片配置 | `SegmentExt{Index uint16, Total uint16}` | **UDP 大消息分片** |
| **4** | 优先级 | `PriorityExt{Level uint8, Desc string}` | 消息优先级调度 |
| **5+** | 自定义 | 任意结构化数据 | 如超时、TraceID 等 |

### 2.4 业务数据（Data）

格式由 `FixedHeader.CodecID` 指定：

- **CodecID = 0**: JSON 格式
- **CodecID = 1**: MessagePack 格式（推荐）
- **CodecID = 2**: Protobuf 格式

### 2.5 CRC32 校验

- **算法**：CRC32-IEEE 标准
- **校验范围**：`VarExtHeader + Data` 完整字节流
- **作用**：防止数据篡改、验证消息完整性

---

## 3. UDP 自动分片机制

### 3.1 分片触发条件

**核心原则**：当 UDP 消息大小 > **1400 字节**时，自动启用应用层分片。

**为什么是 1400 字节？**

```
以太网 MTU: 1500 字节
  ↓ 减去 IP 头 (20 字节)
  ↓ 减去 UDP 头 (8 字节)
= UDP 有效载荷: 1472 字节
  ↓ 安全边界（避免路由器 MTU 差异）
= 预留 72 字节
= 实际阈值: **1400 字节**
```

### 3.2 分片策略

| 消息大小 | 是否分片 | 策略 | 原因 |
|---------|---------|------|------|
| ≤ 1400 字节 | ❌ 不分片 | 单个 UDP 包 | 性能最优，避免开销 |
| > 1400 字节 | ✅ 分片 | 应用层 TLV 分片 | 避免 IP 层分片，支持重组 |

**应用层分片 vs IP 层分片对比**：

| 特性 | IP 层分片 | 应用层 TLV 分片 |
|------|----------|----------------|
| **重组** | 路由器负责 | 接收方负责 |
| **可靠性** | 丢片即丢消息 | 可重传丢失分片 |
| **顺序保证** | 无保证 | TLV Index 保证 |
| **性能** | 低（分片丢失影响整体）| 高（仅重传丢失分片）|

### 3.3 分片实现设计

#### 3.3.1 分片发送流程

```
┌─────────────────────────────────────────────────────────────┐
│ 发送方：Segmenter 分片器                                  │
├─────────────────────────────────────────────────────────────┤
│ 1. 检查消息大小                                          │
│    - size <= 1400：直接发送                               │
│    - size > 1400：启用分片                                │
│                                                             │
│ 2. 计算分片数量                                            │
│    - available = 1400 - FixedHeader(16) - CRC(4) - Ext(10) │
│    - available ≈ 1370 bytes                                │
│    - total = ceil(payloadSize / available)                │
│                                                             │
│ 3. 构建分片消息                                            │
│    - 所有分片使用相同的 NodeID + MsgID                     │
│    - 每个分片携带 SegmentExt{Index, Total}                │
│    - 分片数据 = payload[start:end]                         │
│                                                             │
│ 4. 依次发送所有分片                                        │
└─────────────────────────────────────────────────────────────┘
```

#### 3.3.2 分片重组流程

```
┌─────────────────────────────────────────────────────────────┐
│ 接收方：Reassembler 重组器                                │
├─────────────────────────────────────────────────────────────┤
│ 1. 接收 UDP 包                                             │
│                                                             │
│ 2. 检查是否有 SegmentExt                                   │
│    - 无：直接投递到应用层                                  │
│    - 有：进入重组流程                                      │
│                                                             │
│ 3. 提取分片信息                                            │
│    - key = NodeID_MsgID                                    │
│    - index = SegmentExt.Index                              │
│    - total = SegmentExt.Total                              │
│                                                             │
│ 4. 存储分片                                                │
│    - partial[key].fragments[index] = data                  │
│    - partial[key].received++                               │
│                                                             │
│ 5. 检查是否收齐                                            │
│    - received == total：重组完整消息                       │
│    - received < total：等待后续分片（30秒超时）            │
│                                                             │
│ 6. 重组消息                                                │
│    - 按 index 顺序拼接所有分片                             │
│    - 投递到应用层                                          │
│                                                             │
│ 7. 清理缓存                                                │
│    - 删除已重组的消息                                      │
│    - 定期清理超时消息（30秒）                              │
└─────────────────────────────────────────────────────────────┘
```

### 3.4 分片流程图

```mermaid
flowchart TD
    Start[发送消息] --> CheckSize{消息大小 > 1400?}
    CheckSize -->|否| DirectSend[直接发送 UDP]
    CheckSize -->|是| CalcSegments[计算分片数量]
    CalcSegments --> BuildSegments[构建分片消息<br/>SegmentExt{Index, Total}]
    BuildSegments --> SendSegments[依次发送所有分片<br/>相同 NodeID + MsgID]
    SendSegments --> End1[发送完成]
    DirectSend --> End1

    subgraph 接收方重组
        Recv[接收 UDP 包] --> Parse[解析 FixedHeader]
        Parse --> CheckExt{有 SegmentExt?}
        CheckExt -->|否| Deliver[直接投递]
        CheckExt -->|是| Extract[提取 Index/Total]
        Extract --> Store[存储分片<br/>fragments[index] = data]
        Store --> CheckComplete{received == total?}
        CheckComplete -->|否| Wait[等待后续分片<br/>30秒超时]
        CheckComplete -->|是| Reassemble[重组完整消息<br/>按 Index 拼接]
        Wait --> Recv
        Reassemble --> Deliver
        Deliver --> End2[投递到应用层]
    end
```

---

## 4. 架构设计

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                      Application Layer                      │
│              (MetadataStore, Gossip, Quorum)               │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          │ Message + Protocol()
                          │
┌─────────────────────────▼───────────────────────────────────┐
│                   DualTransport                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Protocol Selection Logic:                          │   │
│  │  - ProtocolTCP → TCP                                │   │
│  │  - ProtocolUDP → UDP                                │   │
│  │  - ProtocolAuto → Size-based (1400 bytes threshold) │   │
│  └─────────────────────────────────────────────────────┘   │
└───────────┬──────────────────────────┬──────────────────────┘
            │                          │
   ┌────────▼────────┐        ┌───────▼────────┐
   │  TCPTransport   │        │  UDPTransport  │
   │  (可靠传输)     │        │  (支持分片)    │
   └────────┬────────┘        └────────┬────────┘
            │                          │
   ┌────────▼────────┐        ┌───────▼────────┐
   │  TLV Protocol   │        │  TLV Protocol   │
   │  + CRC32        │        │  + CRC32        │
   └────────┬────────┘        └────────┬────────┘
            │                          │
   ┌────────▼────────┐        ┌───────▼────────┐
   │  Segmenter      │        │  Reassembler    │
   │  (分片器)        │        │  (重组器)        │
   │  size > 1400    │        │  收齐分片        │
   └────────┬────────┘        └────────┬────────┘
            │                          │
            └──────────┬───────────────┘
                       ▼
              ┌─────────────────┐
              │  Network I/O    │
              └─────────────────┘
```

### 4.2 模块划分

| 模块 | 职责 | 文件位置 |
|------|------|---------|
| **Protocol** | TLV 协议定义、序列化/反序列化 | `transport/protocol.go` |
| **Message** | 消息接口、Protocol 扩展 | `transport/message.go` |
| **Segmenter** | UDP 分片器（>1400 自动分片）| `transport/udp_segmenter.go` |
| **Reassembler** | UDP 分片重组器 | `transport/udp_reassembler.go` |
| **TCPTransport** | TCP 传输实现 | `transport/tcp_transport.go` |
| **UDPTransport** | UDP 传输实现（集成分片/重组）| `transport/udp_transport.go` |
| **DualTransport** | 协议选择、路由逻辑 | `transport/dual_transport.go` |
| **Codec** | 编解码器（JSON/MessagePack/Protobuf）| `transport/codec/` |
| **Compress** | 压缩器（Snappy/Zstd/LZ4）| `transport/compress/` |

---

## 5. 核心数据结构

### 5.1 固定头

```go
package transport

const (
    MagicNXUT      = "\x4E\x58\x55\x54" // "NXUT"
    FixedHeaderLen = 16                // 16字节
)

type FixedHeader struct {
    Magic   [4]byte // 魔术字
    NodeID  uint64  // 发送节点ID
    MsgID   uint16  // 消息ID
    CodecID uint16  // 编码器ID
}

func (fh *FixedHeader) Serialize() ([]byte, error)
func (fh *FixedHeader) Deserialize(data []byte) error
```

### 5.2 TLV 扩展字段

```go
package transport

import "github.com/vmihailenco/msgpack/v5"

// ExtFieldType 扩展字段类型
type ExtFieldType uint16

const (
    ExtFieldCompress ExtFieldType = 1 // 压缩配置
    ExtFieldEncrypt  ExtFieldType = 2 // 加密配置
    ExtFieldSegment  ExtFieldType = 3 // 分片配置
    ExtFieldPriority ExtFieldType = 4 // 优先级
)

// TLV 扩展字段结构化数据（MessagePack 序列化）
type CompressExt struct {
    CompressID uint16 `msgpack:"cid"`
}

type EncryptExt struct {
    EncryptID uint16 `msgpack:"eid"`
    Nonce     []byte `msgpack:"non"`
    Version   string `msgpack:"ver"`
}

type SegmentExt struct {
    Index uint16 `msgpack:"idx"` // 分片索引（从0开始）
    Total uint16 `msgpack:"tot"` // 总分片数
}

type PriorityExt struct {
    Level uint8  `msgpack:"lvl"`
    Desc  string `msgpack:"desc"`
}

// ExtField TLV 字段
type ExtField struct {
    Type  ExtFieldType
    Value []byte // MessagePack 序列化后的字节流
}

// VarExtHeader 变长扩展头
type VarExtHeader struct {
    ExtTotalLen uint16
    Fields      []*ExtField
}
```

### 5.3 完整消息

```go
package transport

type Message struct {
    FixedHeader  *FixedHeader
    VarExtHeader *VarExtHeader
    Data         []byte
    CRC32        uint32
}

func (m *Message) Serialize() ([]byte, error)
func (m *Message) Deserialize(data []byte) error
func (m *Message) CalculateCRC() error
func (m *Message) VerifyCRC() (bool, error)
```

### 5.4 UDP 分片器

```go
package transport

const (
    UDPSafePayloadSize = 1400 // UDP 安全负载大小
)

type Segmenter struct {
    maxSegmentSize int
}

func NewSegmenter() *Segmenter

// NeedSegment 判断是否需要分片
func (s *Segmenter) NeedSegment(dataSize int) bool

// Segment 将消息分片
func (s *Segmenter) Segment(
    ctx context.Context,
    msg *Message,
    addr string,
) ([]*Message, error)
```

### 5.5 UDP 重组器

```go
package transport

const (
    ReassemblerTimeout    = 30 * time.Second
    ReassemblerMaxPending = 1000
)

type Reassembler struct {
    mu              sync.RWMutex
    pendingMessages map[string]*partialMessage
    timeout         time.Duration
}

type partialMessage struct {
    nodeID    uint64
    msgID     uint16
    total     uint16
    received  uint16
    fragments map[uint16][]byte
    firstTime time.Time
}

func NewReassembler() *Reassembler

// AddFragment 添加分片，返回重组后的数据（如果收齐）
func (r *Reassembler) AddFragment(msg *Message) ([]byte, error)

// reassemble 重组分片
func (r *Reassembler) reassemble(partial *partialMessage) []byte

// CleanupTimeout 清理超时消息
func (r *Reassembler) CleanupTimeout()
```

---

## 6. 接口设计

### 6.1 Transport 接口

```go
package transport

import "context"

// Transport 传输接口
type Transport interface {
    // Send 发送消息
    Send(ctx context.Context, addr string, msg *Message) error

    // Receive 接收消息
    Receive(ctx context.Context) <-chan *Message

    // Close 关闭传输器
    Close() error
}
```

### 6.2 Message 接口

```go
package transport

// MessageType 消息类型
type MessageType uint8

const (
    TypeHandshake    MessageType = 0x01
    TypeHeartbeat    MessageType = 0x02
    TypeMetadata     MessageType = 0x03
    TypeData         MessageType = 0x04
    TypeAck          MessageType = 0x05
)

// Message 消息接口
type Message interface {
    GetType() MessageType
    GetPayload() []byte
}
```

### 6.3 CodecType 枚举

```go
package transport

type CodecType uint16

const (
    CodecJSON     CodecType = 0 // JSON 编码
    CodecMsgPack  CodecType = 1 // MessagePack 编码（推荐）
    CodecProtobuf CodecType = 2 // Protobuf 编码
)
```

---

## 7. 数据流设计

### 7.1 发送流程（含分片）

```
Application Layer
      │
      │ Message
      ▼
DualTransport
      │
      ├─► 检查 Protocol()
      │
      ├─► ProtocolUDP → UDPTransport
      │    │
      │    ├─► 检查 Size
      │    │
      │    ├─► Size > 1400?
      │    │    │
      │    │    ├─► Yes → Segmenter.Segment()
      │    │    │         │
      │    │    │         ├─► 构建分片消息（SegmentExt）
      │    │    │         │
      │    │    │         └─► 发送所有分片
      │    │    │
      │    │    └─► No → 直接发送
      │    │
      │    └─► 序列化 (TLV + CRC)
      │
      └─► Network I/O
```

### 7.2 接收流程（含重组）

```
Network I/O
      │
      │ 接收 UDP 包
      ▼
UDPTransport
      │
      ├─► 反序列化 Message
      │
      ├─► 提取 SegmentExt?
      │    │
      │    ├─► No → 直接投递
      │    │
      │    └─► Yes → Reassembler.AddFragment()
      │                   │
      │                   ├─► 存储分片
      │                   │
      │                   ├─► 收齐所有分片?
      │                   │    │
      │                   ├─► Yes → Reassemble
      │                   │         │
      │                   ├─► No → 等待后续分片
      │                   │         │
      │                   └─► 超时清理（30秒）
      │
      └─► 投递到 Application
```

---

## 8. 错误处理

### 8.1 错误定义

```go
package transport

import "errors"

var (
    // 协议错误
    ErrInvalidMagic     = errors.New("invalid magic number")
    ErrInvalidFrame     = errors.New("invalid frame")
    ErrCRCMismatch      = errors.New("crc mismatch")

    // 分片错误
    ErrSegmentTimeout   = errors.New("segment reassembly timeout")
    ErrSegmentMismatch   = errors.New("segment total mismatch")
    ErrSegmentMissing   = errors.New("missing segment")

    // 序列化错误
    ErrMsgpackFailed    = errors.New("msgpack marshal failed")
    ErrCodecUnsupported  = errors.New("unsupported codec")
)
```

### 8.2 错误处理策略

| 错误类型 | 处理策略 | 是否重试 |
|---------|---------|---------|
| **CRC 不匹配** | 丢弃数据包 | 否（数据已损坏）|
| **分片超时** | 丢弃所有分片，清理缓存 | 否（避免内存泄漏）|
| **分片丢失** | 等待 30 秒，超时丢弃 | 否（依赖上层重传）|
| **序列化失败** | 记录错误，返回失败 | 否（数据问题）|

---

## 9. 性能优化

### 9.1 编码器性能对比

| 编码器 | 序列化速度 | 反序列化速度 | 数据大小 | 适用场景 |
|-------|----------|------------|---------|---------|
| **JSON** | 慢 | 慢 | 大 | 调试、开发 |
| **MessagePack** | 快 | 快 | 中 | **生产推荐** |
| **Protobuf** | 最快 | 最快 | 小 | 高性能场景 |

### 9.2 分片性能影响

| 消息大小 | 不分片延迟 | 分片延迟 | 开销 |
|---------|----------|---------|------|
| 1KB | < 1ms | N/A | 0% |
| 10KB | N/A（建议用 TCP）| ~5ms | 分片+重组 |
| 100KB | N/A（建议用 TCP）| ~50ms | 分片+重组 |

**建议**：
- ≤ 1400 字节：UDP 不分片，性能最优
- > 1400 字节：考虑用 TCP，避免分片开销

---

## 10. 兼容性设计

### 10.1 版本协商

```go
package transport

const (
    CurrentProtocolVersion uint8 = 1
)

// Handshake 握手消息
type HandshakeMessage struct {
    Version          uint8
    SupportedCodecs  []uint16
    SupportedSegment bool // 是否支持分片
}
```

### 10.2 向后兼容策略

| 场景 | 处理方式 |
|------|---------|
| **旧客户端（无 TLV）** | 解析失败，返回错误 |
| **新客户端（有 TLV）** | 正常处理 |
| **旧客户端收到分片** | 无法识别，丢弃 |
| **新客户端收到分片** | 正常重组 |

---

## 11. 测试计划

### 11.1 单元测试

| 模块 | 测试用例数 | 覆盖率目标 |
|------|----------|----------|
| **Protocol（TLV）** | 20 个 | 95% |
| **Segmenter** | 15 个 | 90% |
| **Reassembler** | 20 个 | 90% |
| **UDP Transport** | 25 个 | 85% |
| **Dual Transport** | 15 个 | 85% |

### 11.2 集成测试

| 测试场景 | 描述 | 预期结果 |
|---------|------|---------|
| **端到端通信** | 发送 → 接收 → 验证 | 消息完整无损 |
| **UDP 小消息** | 1KB 消息（不分片）| 单包传输，延迟 < 1ms |
| **UDP 大消息** | 10KB 消息（分片）| 分片传输，重组成功 |
| **分片丢失** | 模拟分片丢失 | 30 秒超时清理 |
| **分片乱序** | 随机顺序接收分片 | 正确重组 |
| **CRC 校验** | 篡改数据 | 校验失败，丢弃数据包 |

### 11.3 性能测试

| 指标 | 目标值 |
|------|--------|
| **序列化延迟** | < 100μs |
| **分片延迟** | < 5ms (10KB 消息) |
| **重组延迟** | < 1ms |
| **端到端延迟** | UDP < 2ms, TCP < 5ms |
| **吞吐量** | > 100K msg/s |

---

## 12. 实施计划

### 12.1 里程碑

| 阶段 | 任务 | 预估时间 | 交付物 |
|------|------|---------|--------|
| **阶段 1** | 实现 TLV 协议（固定头+扩展头+CRC）| 1 周 | `protocol.go` + 单元测试 |
| **阶段 2** | 实现 MessagePack 序列化 | 3 天 | `codec/msgpack.go` + 测试 |
| **阶段 3** | 实现分片器（Segmenter）| 3 天 | `udp_segmenter.go` + 测试 |
| **阶段 4** | 实现重组器（Reassembler）| 3 天 | `udp_reassembler.go` + 测试 |
| **阶段 5** | 更新 UDP Transport（集成分片/重组）| 1 周 | `udp_transport.go` + 测试 |
| **阶段 6** | 更新 TCP Transport（使用 TLV 协议）| 3 天 | `tcp_transport.go` + 测试 |
| **阶段 7** | DualTransport 集成 | 3 天 | `dual_transport.go` + 测试 |
| **阶段 8** | 集成测试和性能测试 | 1 周 | 测试用例 + 性能报告 |
| **阶段 9** | 文档更新 | 3 天 | API 文档 + 使用示例 |

**总计**：约 6 周

### 12.2 依赖关系

```
阶段 1 (TLV 协议)
    ↓
阶段 2 (MessagePack) ← 独立
    ↓               ↓
阶段 3 (Segmenter)   阶段 6 (TCP Transport)
    ↓
阶段 4 (Reassembler)
    ↓               ↓
阶段 5 (UDP Transport) ←───────┘
    ↓
阶段 7 (Dual Transport)
    ↓
阶段 8 (集成测试)
    ↓
阶段 9 (文档更新)
```

---

## 13. 风险评估

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **分片性能开销** | 中 | 中 | 提供 TCP 选项，大数据建议用 TCP |
| **分片丢失率** | 高 | 中 | 实现超时清理，避免内存泄漏 |
| **协议兼容性** | 高 | 低 | 版本协商机制 |
| **实施复杂度** | 中 | 中 | 分阶段实施，充分测试 |

---

## 14. 参考资料

### 14.1 相关文档

- **Brainstorm**: `docs/06_project_management/brainstorm/transport_2026-01-20_tcp-udp-unified-tlv-protocol.md`
- **UDP 分片改进**: `docs/06_project_management/brainstorm/transport_2026-01-20_udp-fragmentation-improvements.md`
- **系统架构设计**: `docs/02_design/architecture/01_系统架构设计.md`

### 14.2 技术参考

- **MessagePack**: https://msgpack.org/
- **CRC32**: https://en.wikipedia.org/wiki/Cyclic_redundancy_check
- **TLV 编码**: https://en.wikipedia.org/wiki/Type-length-value
- **以太网 MTU**: https://en.wikipedia.org/wiki/Maximum_transmission_unit

---

## 附录 A: 代码示例

### A.1 发送方示例

```go
package main

import (
    "context"
    "fmt"
    "github.com/jzhang405/NexKV/internal/metadata/transport"
)

func main() {
    // 1. 创建 DualTransport
    tcp := transport.NewTCPTransport(":9211")
    udp := transport.NewUDPTransport(":9212")
    dual := transport.NewDualTransport(tcp, udp)

    // 2. 构建消息
    msg := &transport.Message{
        FixedHeader: &transport.FixedHeader{
            Magic:   [4]byte(transport.MagicNXUT),
            NodeID:  12345,
            MsgID:   67890,
            CodecID: uint16(transport.CodecMsgPack),
        },
        VarExtHeader: &transport.VarExtHeader{
            Fields: []*transport.ExtField{
                // 压缩扩展
                {
                    Type: transport.ExtFieldCompress,
                    Value: msgpackMarshal(&transport.CompressExt{CompressID: 2}),
                },
            },
        },
        Data: msgpackMarshal(map[string]any{
            "op":    "Put",
            "key":   "user:100",
            "value": []byte("hello"),
        }),
    }

    // 3. 发送（自动选择协议 + 自动分片）
    if err := dual.Send(context.Background(), "127.0.0.1:9211", msg); err != nil {
        fmt.Printf("发送失败: %v\n", err)
        return
    }

    fmt.Println("✅ 发送成功")
}
```

### A.2 接收方示例

```go
package main

import (
    "context"
    "github.com/jzhang405/NexKV/internal/metadata/transport"
)

func main() {
    // 1. 创建 UDP Transport（自动重组分片）
    udp := transport.NewUDPTransport(":9212")

    // 2. 接收消息
    ch := udp.Receive(context.Background())

    // 3. 处理消息
    for msg := range ch {
        fmt.Printf("收到消息: NodeID=%d, MsgID=%d, Size=%d\n",
            msg.FixedHeader.NodeID,
            msg.FixedHeader.MsgID,
            len(msg.Data))

        // 4. 解析扩展字段
        for _, field := range msg.VarExtHeader.Fields {
            switch field.Type {
            case transport.ExtFieldSegment:
                seg := &transport.SegmentExt{}
                transport.DecodeExtField(field, seg)
                fmt.Printf("  分片: Index=%d, Total=%d\n", seg.Index, seg.Total)

            case transport.ExtFieldCompress:
                compress := &transport.CompressExt{}
                transport.DecodeExtField(field, compress)
                fmt.Printf("  压缩: CompressID=%d\n", compress.CompressID)
            }
        }
    }
}
```

---

**文档版本**: v2.0 (TLV 协议 + UDP 自动分片)
**创建日期**: 2026-01-20
**最后更新**: 2026-01-20
**维护者**: NexKV 开发团队
**状态**: 📋 待评审
