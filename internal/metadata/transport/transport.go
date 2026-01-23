// Package transport 提供网络传输层抽象接口
// 支持 TCP、gRPC、Memory 等多种传输协议
package transport

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// Transport 网络传输接口
//
// 核心特性:
//   - 协议抽象：支持 TCP、gRPC、Memory 等多种实现
//   - 消息传递：异步发送/接收消息
//   - 连接管理：自动重连、连接池
//   - 生命周期：Start/Stop 控制
//   - 消息唯一标识：支持 (NodeID, MsgSeq) 全局唯一标识
//
// 使用场景:
//   - Gossip 协议通信
//   - Quorum 投票通信
//   - 2PC 协议通信
//   - 节点间元数据同步
type Transport interface {
	// Start 启动传输层
	// 初始化监听器、连接池等资源
	//
	// 扩展参数（可选，传入 nil 表示使用默认值）：
	//   - nodeID: 节点 ID（全局唯一，用于消息去重和幂等性）
	//   - msgSeqGenerator: 消息序列号生成器（nil 表示使用默认原子计数器）
	//
	// 使用示例：
	//   // 使用默认值
	//   transport.Start(nil, nil)
	//
	//   // 指定节点 ID
	//   nodeID := uint64(12345)
	//   transport.Start(&nodeID, nil)
	//
	//   // 自定义序列号生成器
	//   transport.Start(nil, func() uint64 {
	//       return uint64(time.Now().UnixNano())
	//   })
	Start(nodeID *uint64, msgSeqGenerator func() uint64) error

	// Stop 停止传输层
	// 优雅关闭所有连接、释放资源
	Stop() error

	// Send 发送消息到指定节点
	// 阻塞直到消息发送成功或失败
	//
	// 支持函数选项模式，可动态配置 TLV 扩展字段：
	//   transport.Send(ctx, addr, msg, WithHopCount(10))
	//   transport.Send(ctx, addr, msg, WithCompression(2), WithHopCount(5))
	Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error

	// Receive 返回接收消息的通道
	// 调用者需要持续从通道读取消息
	//
	// 返回 MsgFrame（网络帧），包含原始消息和 TLV 扩展字段：
	//   for msgFrame := range transport.Receive() {
	//       if msgFrame.HasHopCount() {
	//           if hop, ok := msgFrame.GetHopCount(); ok {
	//               fmt.Printf("Hop: %d/%d\n", hop.Hop, hop.TotalHop)
	//           }
	//       }
	//   }
	Receive() <-chan MsgFrame

	// ForwardMessage 转发消息到指定节点
	// 自动递减 Hop Count（如果存在），Hop Count 减至 0 时返回错误
	//
	// 使用场景：
	//   - Gossip 协议消息转发
	//   - 消息广播
	//   - 节点间消息中继
	//
	// 行为：
	//   1. 检查 msgFrame 中 Hop Count 是否存在（通过 GetHopCount()）
	//   2. 如果存在且 Hop > 0，则递减 Hop（NewHop = Hop - 1）
	//   3. 如果 Hop == 0，返回 ErrHopCountExpired（消息已过期，不再转发）
	//   4. 如果不存在，直接转发（不添加 Hop Count）
	//   5. 保留所有 TLV 扩展字段（除 Hop Count 递减外）
	//
	// 返回：
	//   - uint64: 转发的消息序列号
	//   - error: 转发失败时返回错误
	ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error)

	// GetNodeID 获取当前节点 ID
	//
	// 返回:
	//   - uint64: 节点 ID（全局唯一，用于消息去重和幂等性）
	GetNodeID() uint64

	// GenerateMsgSeq 生成下一条消息序列号
	//
	// 返回:
	//   - uint64: 消息序列号（单调递增，全局唯一）
	GenerateMsgSeq() uint64
}

// 导出 types 包中的类型别名，方便 transport 包使用

// Message 传输消息接口
type Message = types.Message

// MessageType 消息类型
type MessageType = types.MessageType

// Priority 优先级类型
type Priority = types.Priority

// GetPriority 获取消息优先级
var GetPriority = types.GetPriority

// Address 节点地址
type Address = types.Address

// TransportConfig 传输层配置
type TransportConfig struct {
	// ListenAddr 监听地址
	ListenAddr string

	// MaxMessageSize 最大消息大小（字节）
	MaxMessageSize int64

	// ReadTimeout 读超时
	ReadTimeout time.Duration

	// WriteTimeout 写超时
	WriteTimeout time.Duration

	// KeepAliveInterval 保活间隔
	KeepAliveInterval time.Duration

	// KeepAliveTimeout 保活超时
	KeepAliveTimeout time.Duration

	// BufferSize 缓冲区大小
	BufferSize int

	// ChannelSendTimeout 通道发送超时（P2-2：接收通道阻塞检测）
	// 说明：当接收通道阻塞超过此时间时，认为消费者处理缓慢，丢弃消息并记录日志
	ChannelSendTimeout time.Duration
}

// DefaultTransportConfig 返回默认配置
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		ListenAddr:         "0.0.0.0:9211",
		MaxMessageSize:     1024 * 1024 * 100, // 100MB
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second, // P2-2: 默认 5 秒通道发送超时
	}
}

// Codec 编解码器接口
//
// 用于消息的序列化和反序列化
type Codec interface {
	// Encode 编码消息
	Encode(msg Message) ([]byte, error)

	// Decode 解码消息（创建新实例）
	Decode(msgType types.MessageType, data []byte) (Message, error)

	// DecodeInto 解码消息到指定实例（避免创建新消息）
	// 当消息类型已知时（如从 FixedHeader 读取）使用此方法更高效
	DecodeInto(data []byte, msg Message) error

	// Name 返回编解码器名称
	Name() string

	// Type 返回编解码器类型
	Type() types.CodecType
}

// Conn 连接接口
//
// 表示与远程节点的连接
type Conn interface {
	// Read 读取数据
	Read(p []byte) (n int, err error)

	// Write 写入数据
	Write(p []byte) (n int, err error)

	// Close 关闭连接
	Close() error

	// RemoteAddr 返回远程地址
	RemoteAddr() string

	// LocalAddr 返回本地地址
	LocalAddr() string

	// SetDeadline 设置读写超时
	SetDeadline(t time.Time) error

	// SetReadDeadline 设置读超时
	SetReadDeadline(t time.Time) error

	// SetWriteDeadline 设置写超时
	SetWriteDeadline(t time.Time) error
}

// Listener 监听器接口
//
// 用于接受传入连接
type Listener interface {
	// Accept 接受新连接
	Accept() (Conn, error)

	// Close 关闭监听器
	Close() error

	// Addr 返回监听地址
	Addr() string
}

// Dialer 拨号器接口
//
// 用于建立到远程节点的连接
type Dialer interface {
	// Dial 建立连接
	Dial(addr string) (Conn, error)

	// DialTimeout 建立连接（带超时）
	DialTimeout(addr string, timeout time.Duration) (Conn, error)
}

// ========================================
// 批量转发相关数据结构和接口
// ========================================

// BatchForwardMessageResult 批量转发结果
//
// 统计批量转发的成功/失败数量
type BatchForwardMessageResult struct {
	SuccessCount int                  // 成功数量
	FailureCount int                  // 失败数量
	Results      []BatchForwardResult // 详细结果列表
}

// BatchForwardResult 单次转发结果
//
// 记录单个地址的转发结果
type BatchForwardResult struct {
	Addr  string // 目标地址
	SeqID uint64 // 消息序列号（成功时）
	Error error  // 错误信息（失败时）
}

// BatchForwardTransport 批量转发传输接口
//
// 扩展 Transport 接口，支持批量转发
type BatchForwardTransport interface {
	Transport

	// BatchForwardMessage 批量转发消息
	//
	// 并发转发消息到多个目标地址，部分失败不影响其他转发
	//
	// 参数：
	//   - ctx: 上下文（支持取消和超时）
	//   - addrs: 目标地址列表
	//   - msgExt: 要转发的网络帧
	//
	// 返回：
	//   - BatchForwardMessageResult: 批量转发结果（成功/失败统计）
	//
	// 行为：
	//   1. 并发调用 ForwardMessage 转发到每个地址
	//   2. 单个地址失败不影响其他转发
	//   3. 收集所有转发的结果
	//   4. 返回成功/失败统计
	//
	// 使用场景：
	//   - Gossip 协议批量转发到随机节点
	//   - 消息广播到集群节点
	BatchForwardMessage(ctx context.Context, addrs []string, msgExt MsgFrame) BatchForwardMessageResult
}
