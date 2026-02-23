// Package service 定义领域服务接口
package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"
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

// 注意：RPC/RPCSync 接口定义已移至 rpc_sync.go
// RPCAsync 接口定义在 rpc_async.go
//
// 类型别名：
// - RPC = RPCSync（向后兼容）
//
// 接口选择指南：
// - RPCSync: 阻塞式同步调用，直接返回结果
// - RPCAsync: 异步调用，返回 AsyncOperation[T]，支持链式回调和超时

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
// RequestID 生成器
// ============================================================================

// RequestID 请求唯一标识符
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
// 示例: node-001-65d4a3f0-0001
//
// 设计说明：
// - nodeID: 节点唯一标识，确保跨节点不冲突
// - timestamp: Unix 时间戳（16 进制，8 位），支持跨节点时间排序
// - sequence: 自增序列号（16 进制，4 位），每秒最多 65535 个请求
//
// 优势：
// - 固定宽度：便于解析和索引
// - 16 进制：减少长度（vs 10 进制）
// - 时间排序：支持分布式追踪按时间排序
type RequestID string

// RequestIDGenerator 请求 ID 生成器（线程安全 + 时钟漂移保护）
type RequestIDGenerator struct {
	nodeID     string        // 节点 ID（启动时分配）
	lastSecond atomic.Int64  // 上次生成时间戳（秒）
	secondSeq  atomic.Uint32 // 当前秒内序列号
}

// NewRequestIDGenerator 创建请求 ID 生成器
func NewRequestIDGenerator(nodeID string) *RequestIDGenerator {
	return &RequestIDGenerator{
		nodeID:     nodeID,
		lastSecond: atomic.Int64{},
		secondSeq:  atomic.Uint32{},
	}
}

// Next 生成下一个请求 ID（线程安全 + 时钟漂移保护 + 序列号溢出保护）
//
// 时钟回退处理策略：
// - 当检测到系统时间回退（now < lastSecond）时，使用 lastSecond 作为时间戳
// - 这保证了 RequestID 单调递增，避免 ID 冲突
// - 场景：NTP 同步、闰秒、手动修改系统时间
//
// P1-1 修复：序列号溢出保护
// - 序列号格式为 4 位 16 进制（最大 0xFFFF = 65535）
// - 当序列号超过 65535 时，等待下一秒再生成
// - 这样可以保持 ID 格式的一致性
func (g *RequestIDGenerator) Next() RequestID {
	const maxSeq uint32 = 0xFFFF // 4 位 16 进制最大值

	for {
		now := time.Now().Unix()

		// 时钟漂移保护：检测时间回退
		for {
			lastSec := g.lastSecond.Load()

			if now > lastSec {
				// 时间前进：正常跨秒
				if g.lastSecond.CompareAndSwap(lastSec, now) {
					g.secondSeq.Store(0)
					break
				}
				// CAS 失败，重试
				continue
			}

			if now == lastSec {
				// 同一秒：继续递增序列号
				break
			}

			// now < lastSec：时间回退！
			// 策略：使用 lastSec 保证单调递增
			now = lastSec
			break
		}

		// 原子递增序列号
		seq := g.secondSeq.Add(1)

		// P1-1 修复：序列号溢出保护
		// 如果序列号超过 65535，等待下一秒再生成
		if seq > maxSeq {
			// 等待下一秒（最多 1 秒）
			time.Sleep(time.Until(time.Unix(now+1, 0)))
			continue
		}

		// 格式化：{NodeID}-{Timestamp:08x}-{Sequence:04x}
		return RequestID(fmt.Sprintf("%s-%08x-%04x", g.nodeID, now, seq))
	}
}

// ParseRequestID 解析请求 ID（用于日志和调试）
func ParseRequestID(id RequestID) (nodeID string, timestamp int64, sequence uint32, err error) {
	parts := strings.Split(string(id), "-")
	if len(parts) < 3 {
		return "", 0, 0, pkgerrors.Wrap(pkgerrors.ErrInvalidParam, "invalid request ID format: expected {NodeID}-{Timestamp}-{Sequence}")
	}

	// 解析时间戳（倒数第二部分）
	tsHex := parts[len(parts)-2]
	ts, err := strconv.ParseInt(tsHex, 16, 64)
	if err != nil {
		return "", 0, 0, pkgerrors.Wrapf(pkgerrors.ErrInvalidParam, "invalid timestamp: %v", err)
	}

	// 解析序列号（最后一部分）
	seqHex := parts[len(parts)-1]
	seq, err := strconv.ParseUint(seqHex, 16, 32)
	if err != nil {
		return "", 0, 0, pkgerrors.Wrapf(pkgerrors.ErrInvalidParam, "invalid sequence: %v", err)
	}

	// 节点 ID（前面所有部分）
	nodeID = strings.Join(parts[:len(parts)-2], "-")

	return nodeID, ts, uint32(seq), nil
}

// Time 返回请求 ID 中的时间戳（用于排序）
func (id RequestID) Time() time.Time {
	_, ts, _, err := ParseRequestID(id)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
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
