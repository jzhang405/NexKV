package implementations

import (
	"context"
	"time"
)

// MessageType 消息类型
type MessageType int

const (
	// Gossip 消息类型
	GossipExchange MessageType = iota

	// 投票相关
	ProposeVote
	AckVote

	// 决策相关
	DecisionNotify
	FollowDecision

	// 2PC 协议消息
	PrepareRequest
	PrepareResponse
	VoteRequest
	VoteResponse
	DecisionRequest
	DecisionAck

	// 心跳
	Heartbeat
)

// String 返回消息类型的字符串表示
func (mt MessageType) String() string {
	switch mt {
	case GossipExchange:
		return "GossipExchange"
	case ProposeVote:
		return "ProposeVote"
	case AckVote:
		return "AckVote"
	case DecisionNotify:
		return "DecisionNotify"
	case FollowDecision:
		return "FollowDecision"
	case PrepareRequest:
		return "PrepareRequest"
	case PrepareResponse:
		return "PrepareResponse"
	case VoteRequest:
		return "VoteRequest"
	case VoteResponse:
		return "VoteResponse"
	case DecisionRequest:
		return "DecisionRequest"
	case DecisionAck:
		return "DecisionAck"
	case Heartbeat:
		return "Heartbeat"
	default:
		return "Unknown"
	}
}

// Message 传输层统一消息格式
type Message struct {
	// 消息元数据
	Type      MessageType // 消息类型
	From      string      // 发送节点 ID
	To        string      // 目标节点 ID（空表示广播）
	Timestamp int64       // 时间戳（纳秒）

	// 消息负载
	Payload []byte // 序列化的数据

	// 上下文
	Context context.Context // 用于取消和超时控制
}

// TransportStatus 传输层状态
type TransportStatus struct {
	NodeID          string        // 节点 ID
	Type            string        // Transport 类型（"null", "memory", "grpc"）
	IsRunning       bool          // 是否正在运行
	MessageLatency  time.Duration // 消息延迟（用于测试模拟）
	BytesSent       int64         // 发送字节数
	BytesReceived   int64         // 接收字节数
	MessagesSent    int64         // 发送消息数
	MessagesReceived int64        // 接收消息数
	Peers           map[string]string // 对等节点列表
}

// Transport 传输层抽象接口
//
// Transport 定义了节点间通信的抽象层，支持：
// - Null 传输：零开销直接调用，用于性能基线测试
// - 内存传输：通过 channel 通信，用于快速测试和验证
// - gRPC 传输：真实网络通信，用于分布式部署
//
// 所有实现必须保证线程安全。
type Transport interface {
	// Send 发送消息到指定节点
	//
	// 参数：
	//   - targetID: 目标节点 ID
	//   - msg: 要发送的消息
	//
	// 返回：
	//   - error: 发送失败时返回错误
	Send(targetID string, msg Message) error

	// Broadcast 广播消息到所有节点
	//
	// 参数：
	//   - msg: 要广播的消息（msg.To 应该为空）
	//
	// 返回：
	//   - error: 广播失败时返回错误
	Broadcast(msg Message) error

	// Receive 返回接收消息的通道
	//
	// 调用者应该从这个通道持续读取消息。
	// 通道关闭表示传输层已停止。
	//
	// 返回：
	//   - <-chan Message: 消息接收通道
	Receive() <-chan Message

	// Start 启动传输层
	//
	// 在发送或接收消息之前必须调用 Start。
	//
	// 返回：
	//   - error: 启动失败时返回错误
	Start() error

	// Stop 停止传输层
	//
	// Stop 会关闭 Receive() 返回的通道，并释放所有资源。
	// 已发送的消息可能仍然会被接收。
	//
	// 返回：
	//   - error: 停止失败时返回错误
	Stop() error

	// Status 获取传输层当前状态
	//
	// 返回：
	//   - TransportStatus: 当前状态快照
	Status() TransportStatus
}

// MessageHandler 消息处理器接口
//
// MessageHandler 定义了节点如何处理接收到的消息。
// 每个 Transport 应该关联一个 MessageHandler 来处理收到的消息。
type MessageHandler interface {
	// HandleGossip 处理 Gossip 交换请求
	HandleGossip(from string, knowledge Knowledge) error

	// HandleVote 处理投票请求
	HandleVote(from string, version int) error

	// HandleDecision 处理决策通知
	HandleDecision(from string, decision DecisionState) error
}

// TransportConfig 传输层配置
type TransportConfig struct {
	// 节点配置
	NodeID string // 本节点 ID

	// 网络配置
	// 仅用于网络传输
	Peers map[string]string // 节点 ID -> 地址（如 "n1": "localhost:5001"）

	// 性能配置
	BufferSize int // 接收缓冲区大小（默认 1000）

	// 测试配置
	SimulatedLatency time.Duration // 模拟延迟（仅用于内存传输测试）
}

// DefaultTransportConfig 返回默认配置
func DefaultTransportConfig(nodeID string) *TransportConfig {
	return &TransportConfig{
		NodeID:     nodeID,
		Peers:      make(map[string]string),
		BufferSize: 1000,
	}
}

// TransportFactory 传输层工厂函数类型
type TransportFactory func(config *TransportConfig) (Transport, error)

// 注册的传输层工厂
var transportFactories = make(map[string]TransportFactory)

// RegisterTransport 注册传输层工厂
// 用于注册新的传输层实现
func RegisterTransport(name string, factory TransportFactory) {
	transportFactories[name] = factory
}

// CreateTransport 创建传输层实例
//
// 参数：
//   - transportType: 传输层类型（"null", "memory" 或 "grpc"）
//   - config: 传输层配置
//
// 返回：
//   - Transport: 传输层实例
//   - error: 类型不存在或创建失败时返回错误
func CreateTransport(transportType string, config *TransportConfig) (Transport, error) {
	factory, ok := transportFactories[transportType]
	if !ok {
		return nil, ErrTransportNotFound
	}
	return factory(config)
}

// 错误定义
var (
	// ErrTransportNotFound 传输层类型不存在
	ErrTransportNotFound = &TransportError{Type: "transport_not_found", Message: "transport type not found"}

	// ErrTransportNotStarted 传输层未启动
	ErrTransportNotStarted = &TransportError{Type: "not_started", Message: "transport not started"}

	// ErrTransportStopped 传输层已停止
	ErrTransportStopped = &TransportError{Type: "stopped", Message: "transport stopped"}

	// ErrNodeNotFound 节点不存在
	ErrNodeNotFound = &TransportError{Type: "node_not_found", Message: "target node not found"}
)

// TransportError 传输层错误
type TransportError struct {
	Type    string // 错误类型
	Message string // 错误消息
	Err     error  // 底层错误
}

// Error 实现 error 接口
func (e *TransportError) Error() string {
	if e.Err != nil {
		return e.Type + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Type + ": " + e.Message
}

// Unwrap 支持错误包装
func (e *TransportError) Unwrap() error {
	return e.Err
}
