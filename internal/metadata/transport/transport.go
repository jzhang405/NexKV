// Package transport 提供网络传输层抽象接口
// 支持 TCP、gRPC、Memory 等多种传输协议
package transport

import (
	"context"
	"fmt"
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
//
// 使用场景:
//   - Gossip 协议通信
//   - Quorum 投票通信
//   - 2PC 协议通信
//   - 节点间元数据同步
type Transport interface {
	// Start 启动传输层
	// 初始化监听器、连接池等资源
	Start() error

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
	// 返回 MsgExt（增强消息），包含原始消息和 TLV 扩展字段：
	//   for msgExt := range transport.Receive() {
	//       if msgExt.HasHopCount() {
	//           fmt.Printf("Hop: %d/%d\n", msgExt.HopCount.Hop, msgExt.HopCount.TotalHop)
	//       }
	//   }
	Receive() <-chan MsgExt

	// ForwardMessage 转发消息到指定节点
	// 自动递减 Hop Count（如果存在），Hop Count 减至 0 时返回错误
	//
	// 使用场景：
	//   - Gossip 协议消息转发
	//   - 消息广播
	//   - 节点间消息中继
	//
	// 行为：
	//   1. 检查 msgExt.HopCount 是否存在
	//   2. 如果存在且 Hop > 0，则递减 Hop（NewHop = Hop - 1）
	//   3. 如果 Hop == 0，返回 ErrHopCountExpired（消息已过期，不再转发）
	//   4. 如果不存在，直接转发（不添加 Hop Count）
	//   5. 保留所有 TLV 扩展字段（除 Hop Count 递减外）
	//
	// 返回：
	//   - uint32: 转发的消息序列号
	//   - error: 转发失败时返回错误
	ForwardMessage(ctx context.Context, addr string, msgExt MsgExt) (uint32, error)
}

// Message 传输消息接口
//
// 所有传输的消息都需要实现此接口
type Message interface {
	// Type 返回消息类型
	Type() MessageType

	// Priority 返回消息优先级（0-4，0最低，4最高）
	// 用于流量控制：接收端过载时优先丢弃低优先级消息
	Priority() int
}

// MessageType 消息类型
//
// 消息类型范围分配:
//   - 100-149: 元数据操作（Put、Delete、Get 等）
//   - 150-199: Gossip 协议消息
//   - 200-249: Quorum 协议消息
//   - 250-299: 2PC 协议消息
//   - 300-349: 节点管理消息
//   - 350-399: 集群管理消息
type MessageType uint16

const (
	// 元数据操作消息 (100-149)
	MessageTypeGet         MessageType = 100 // 获取元数据
	MessageTypePut         MessageType = 101 // 更新元数据
	MessageTypeDelete      MessageType = 102 // 删除元数据
	MessageTypeGetReply    MessageType = 103 // Get 响应
	MessageTypePutReply    MessageType = 104 // Put 响应
	MessageTypeDeleteReply MessageType = 105 // Delete 响应

	// Gossip 协议消息 (150-199)
	MessageTypeGossipSync        MessageType = 150 // Gossip 同步请求
	MessageTypeGossipSyncReply   MessageType = 151 // Gossip 同步响应
	MessageTypeGossipDigest      MessageType = 152 // Gossip 摘要
	MessageTypeGossipDigestReply MessageType = 153 // Gossip 摘要响应

	// Quorum 协议消息 (200-249)
	MessageTypeQuorumPropose MessageType = 200 // Quorum 提案
	MessageTypeQuorumVote    MessageType = 201 // Quorum 投票
	MessageTypeQuorumDecide  MessageType = 202 // Quorum 决策

	// 2PC 协议消息 (250-299)
	MessageType2PCPrepare       MessageType = 250 // 2PC 准备阶段
	MessageType2PCPrepareReply  MessageType = 251 // 2PC 准备响应
	MessageType2PCCommit        MessageType = 252 // 2PC 提交阶段
	MessageType2PCRollback      MessageType = 253 // 2PC 回滚阶段
	MessageType2PCCommitReply   MessageType = 254 // 2PC 提交响应
	MessageType2PCRollbackReply MessageType = 255 // 2PC 回滚响应

	// 节点管理消息 (300-349)
	MessageTypeNodePing       MessageType = 300 // 节点心跳
	MessageTypeNodePong       MessageType = 301 // 心跳响应
	MessageTypeNodeJoin       MessageType = 302 // 节点加入
	MessageTypeNodeLeave      MessageType = 303 // 节点离开
	MessageTypeNodeSync       MessageType = 304 // 节点同步
	MessageTypeClockSync      MessageType = 305 // 时钟同步请求
	MessageTypeClockSyncReply MessageType = 306 // 时钟同步响应

	// 集群管理消息 (350-399)
	MessageTypeClusterStatus      MessageType = 350 // 集群状态查询
	MessageTypeClusterStatusReply MessageType = 351 // 集群状态响应
	MessageTypeLeaderElection     MessageType = 352 // Leader 选举
)

// String 返回消息类型的字符串表示
func (t MessageType) String() string {
	switch t {
	case MessageTypeGet:
		return "Get"
	case MessageTypePut:
		return "Put"
	case MessageTypeDelete:
		return "Delete"
	case MessageTypeGetReply:
		return "GetReply"
	case MessageTypePutReply:
		return "PutReply"
	case MessageTypeDeleteReply:
		return "DeleteReply"
	case MessageTypeGossipSync:
		return "GossipSync"
	case MessageTypeGossipSyncReply:
		return "GossipSyncReply"
	case MessageTypeGossipDigest:
		return "GossipDigest"
	case MessageTypeGossipDigestReply:
		return "GossipDigestReply"
	case MessageTypeQuorumPropose:
		return "QuorumPropose"
	case MessageTypeQuorumVote:
		return "QuorumVote"
	case MessageTypeQuorumDecide:
		return "QuorumDecide"
	case MessageType2PCPrepare:
		return "2PCPrepare"
	case MessageType2PCPrepareReply:
		return "2PCPrepareReply"
	case MessageType2PCCommit:
		return "2PCCommit"
	case MessageType2PCRollback:
		return "2PCRollback"
	case MessageType2PCCommitReply:
		return "2PCCommitReply"
	case MessageType2PCRollbackReply:
		return "2PCRollbackReply"
	case MessageTypeNodePing:
		return "NodePing"
	case MessageTypeNodePong:
		return "NodePong"
	case MessageTypeNodeJoin:
		return "NodeJoin"
	case MessageTypeNodeLeave:
		return "NodeLeave"
	case MessageTypeNodeSync:
		return "NodeSync"
	case MessageTypeClockSync:
		return "ClockSync"
	case MessageTypeClockSyncReply:
		return "ClockSyncReply"
	case MessageTypeClusterStatus:
		return "ClusterStatus"
	case MessageTypeClusterStatusReply:
		return "ClusterStatusReply"
	case MessageTypeLeaderElection:
		return "LeaderElection"
	default:
		return "Unknown"
	}
}

// 流量控制优先级常量（从低到高）
//
// 使用场景: 接收端过载时，优先丢弃低优先级消息
const (
	// PriorityLowest 最低优先级（可丢弃）
	PriorityLowest = 0

	// PriorityLow 低优先级
	PriorityLow = 1

	// PriorityNormal 正常优先级
	PriorityNormal = 2

	// PriorityHigh 高优先级
	PriorityHigh = 3

	// PriorityCritical 关键优先级（不可丢弃）
	PriorityCritical = 4
)

// GetPriority 获取消息优先级
//
// 参数:
//   - msgType: 消息类型
//
// 返回:
//   - int: 优先级等级（0-4，0最低，4最高）
func GetPriority(msgType MessageType) int {
	switch msgType {
	// 最低优先级
	case MessageTypeGossipDigest, MessageTypeGossipDigestReply:
		return PriorityLowest

	// 低优先级
	case MessageTypeGossipSyncReply, MessageTypeNodePing:
		return PriorityLow
	case MessageTypeClockSync, MessageTypeClockSyncReply:
		return PriorityLow

	// 关键优先级
	case MessageType2PCCommit, MessageType2PCRollback:
		return PriorityHigh
	case MessageType2PCCommitReply, MessageType2PCRollbackReply:
		return PriorityHigh
	case MessageTypeQuorumDecide:
		return PriorityCritical

	// 正常优先级
	default:
		return PriorityNormal
	}
}

// Address 节点地址
type Address struct {
	Host string // 主机名或 IP
	Port int    // 端口号
}

// String 返回地址的字符串表示 (host:port)
func (a *Address) String() string {
	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

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
	Decode(msgType MessageType, data []byte) (Message, error)

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
	SeqID uint32 // 消息序列号（成功时）
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
	//   - msgExt: 要转发的增强消息
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
	BatchForwardMessage(ctx context.Context, addrs []string, msgExt MsgExt) BatchForwardMessageResult
}
