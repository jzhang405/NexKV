// Package transport 提供网络传输层抽象接口
// 支持 TCP、gRPC、Memory 等多种传输协议
package transport

import (
	"context"
	"fmt"
	"time"
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
	Send(ctx context.Context, addr string, msg Message) error

	// Receive 返回接收消息的通道
	// 调用者需要持续从通道读取消息
	Receive() <-chan Message

	// Close 关闭传输层并释放资源
	Close() error
}

// Message 传输消息接口
//
// 所有传输的消息都需要实现此接口
type Message interface {
	// Type 返回消息类型
	Type() MessageType

	// Marshal 序列化消息为字节流
	Marshal() ([]byte, error)

	// Unmarshal 从字节流反序列化消息
	Unmarshal(data []byte) error

	// Size 返回消息大小（字节）
	Size() int
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
}

// DefaultTransportConfig 返回默认配置
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		ListenAddr:        "0.0.0.0:9211",
		MaxMessageSize:    1024 * 1024 * 100, // 100MB
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  30 * time.Second,
		BufferSize:        4096,
	}
}

// Codec 编解码器接口
//
// 用于消息的序列化和反序列化
type Codec interface {
	// Encode 编码消息
	Encode(msg Message) ([]byte, error)

	// Decode 解码消息
	Decode(data []byte) (Message, error)

	// Name 返回编解码器名称
	Name() string
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
