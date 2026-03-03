// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// Transport 子接口（接口隔离原则）
// ============================================================================

// PeerManager 节点管理接口
type PeerManager interface {
	// Self 返回本地节点 ID
	Self() model.PeerID

	// Connect 连接到指定地址的节点
	Connect(ctx context.Context, addr string) (model.PeerID, error)

	// Disconnect 断开与指定节点的连接
	Disconnect(peer model.PeerID) error

	// ConnectedPeers 返回当前已连接的节点列表
	ConnectedPeers() []model.PeerID

	// IsConnected 检查是否与指定节点已连接
	IsConnected(peer model.PeerID) bool
}

// StreamManager 流管理接口
type StreamManager interface {
	// OpenStream 打开到指定节点的流式连接
	OpenStream(ctx context.Context, peer model.PeerID, protocol string) (Stream, error)

	// AcceptStream 接受指定协议的入站流
	AcceptStream(protocol string) (Stream, error)
}

// ChannelManager 通道管理接口
type ChannelManager interface {
	// OpenChannel 打开到指定节点的双向通道
	OpenChannel(ctx context.Context, peer model.PeerID, protocol string) (Channel, error)
}

// ============================================================================
// Transport 核心接口（组合子接口）
// ============================================================================

// Transport 传输层核心接口
//
// 通过接口组合提供完整的传输层能力：
// - PeerManager: 节点连接管理
// - StreamManager: 流式通信管理
// - ChannelManager: 通道通信管理
type Transport interface {
	PeerManager
	StreamManager
	ChannelManager

	// Close 关闭传输层
	Close() error
}

// Stream 流式通信接口
type Stream interface {
	// ID 返回流 ID
	ID() string

	// Protocol 返回协议名称
	Protocol() string

	// RemotePeer 返回远程节点 ID
	RemotePeer() model.PeerID

	// Read 读取数据
	Read(p []byte) (n int, err error)

	// Write 写入数据
	Write(p []byte) (n int, err error)

	// Close 关闭流
	Close() error

	// SetReadDeadline 设置读超时
	SetReadDeadline(t interface{ UnixNano() int64 }) error

	// SetWriteDeadline 设置写超时
	SetWriteDeadline(t interface{ UnixNano() int64 }) error

	// Reset 重置流（发送 RST）
	Reset() error
}

// Channel 双向通道接口
type Channel interface {
	// Send 发送消息
	Send(ctx context.Context, msg []byte) error

	// Recv 接收消息
	Recv(ctx context.Context) ([]byte, error)

	// Close 关闭通道
	Close() error
}

// ============================================================================
// RPC 接口定义
// ============================================================================

// ResponseStrategy 广播响应策略
type ResponseStrategy int

const (
	// ResponseAll 等待所有节点响应（默认）
	// 适用场景：事务提交、配置变更（强一致性）
	ResponseAll ResponseStrategy = iota

	// ResponseMajority 等待多数派响应（> N/2）
	// 适用场景：3副本写入（W=2）、分片同步
	ResponseMajority

	// ResponseNone 不等待响应（单向发送）
	// 适用场景：日志广播、监控数据（高吞吐）
	ResponseNone
)

// BroadcastResult 广播结果（同消息广播）
type BroadcastResult struct {
	Responses    []model.Message // 成功响应（有序列表）
	SuccessPeers []model.PeerID  // 成功节点
	FailedPeers  []model.PeerID  // 失败/超时节点
}

// WriteVResult 批量写入结果（不同消息）
type WriteVResult struct {
	Responses    map[model.PeerID]model.Message // 成功响应（按节点映射）
	SuccessPeers []model.PeerID                 // 成功节点
	FailedPeers  []model.PeerID                 // 失败/超时节点
}

// ============================================================================
// RPC 接口定义
// ============================================================================

// 注意：RPCSync 接口定义在 rpc_sync.go
// RPCAsync 接口定义在 rpc_async.go
//
// 接口选择指南：
// - RPCSync: 阻塞式同步调用，直接返回结果
// - RPCAsync: 异步调用，返回 AsyncOp[T]，支持链式回调和超时

// RequestMsg 用于 Channel 接收请求
type RequestMsg struct {
	Ctx    context.Context
	From   model.PeerID
	Req    model.Message
	RespCh chan ResponseMsg
}

// ResponseMsg 响应消息
type ResponseMsg struct {
	Msg model.Message
	Err error
}

// ============================================================================
// RPC 配置
// ============================================================================

// RPCConfig RPC 默认配置
type RPCConfig struct {
	// 超时配置
	CallTimeout      time.Duration // 单播调用超时，默认 30s
	BroadcastTimeout time.Duration // 广播调用超时，默认 60s
	ConnectTimeout   time.Duration // 连接超时，默认 10s

	// 重试配置
	MaxRetries   int           // 最大重试次数，默认 3
	RetryBackoff time.Duration // 重试退避时间，默认 1s

	// 并发配置
	MaxConcurrentCalls int // 最大并发调用数，默认 1000
	RequestBufferSize  int // 请求缓冲区大小，默认 256
}

// DefaultRPCConfig 返回默认配置
func DefaultRPCConfig() *RPCConfig {
	return &RPCConfig{
		CallTimeout:        30 * time.Second,
		BroadcastTimeout:   60 * time.Second,
		ConnectTimeout:     10 * time.Second,
		MaxRetries:         3,
		RetryBackoff:       time.Second,
		MaxConcurrentCalls: 1000,
		RequestBufferSize:  256,
	}
}
