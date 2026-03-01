// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// OperationStatus 操作状态定义
// ==========================================

// OperationStatus 操作状态
type OperationStatus int

const (
	// StatusPending 操作待执行
	StatusPending OperationStatus = iota
	// StatusRunning 操作正在执行
	StatusRunning
	// StatusCompleted 操作成功完成
	StatusCompleted
	// StatusFailed 操作失败
	StatusFailed
	// StatusCanceled 操作被取消
	StatusCanceled
	// StatusDiscarded 操作结果被丢弃
	StatusDiscarded
	// StatusTimeout 操作超时
	StatusTimeout
)

// IsTerminal 检查是否为终态
func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
		return true
	default:
		return false
	}
}

// String 返回状态字符串表示
func (s OperationStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCanceled:
		return "canceled"
	case StatusDiscarded:
		return "discarded"
	case StatusTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// ==========================================
// 错误定义（已移至 pkg/errors）
// ==========================================

var (
	// ErrTargetsMsgsMismatch targets 和 msgs 长度不匹配
	ErrTargetsMsgsMismatch = errors.ErrTargetsMsgsMismatch
	// ErrInvalidQuorum quorum 参数无效
	ErrInvalidQuorum = errors.ErrInvalidQuorum
	// ErrInvalidTimeout timeoutMs 参数无效
	ErrInvalidTimeout = errors.ErrInvalidTimeout
	// ErrEmptyPeers peers 切片为空
	ErrEmptyPeers = errors.ErrEmptyPeers
	// ErrNilConfig config 参数为 nil
	ErrNilConfig = errors.ErrNilConfig
	// ErrNilRPC rpc 参数为 nil
	ErrNilRPC = errors.ErrNilRPC
)

// ==========================================
// RPCAsync 领域服务接口
// ==========================================

// RPCAsync 提供异步 RPC 调用能力
// 使用 AsyncOperation[T] 作为返回类型，支持类型安全的异步操作
type RPCAsync interface {
	// ====== 单播 ======

	// CallAsync 异步单播调用
	// 返回 AsyncOperation[ResponseMsg]，调用者可以链式处理结果
	CallAsync(ctx context.Context, to model.PeerID, req model.Message) AsyncOperation[ResponseMsg]

	// CallAsyncWithTimeout 带超时的异步调用
	CallAsyncWithTimeout(ctx context.Context, to model.PeerID, req model.Message, timeoutMs int64) AsyncOperation[ResponseMsg]

	// ====== 广播（同消息）======

	// BroadcastAsync 异步广播调用
	// 返回 AsyncOperation[AsyncBroadcastResult]，包含每个节点的响应
	// 可通过 opts 设置回调实时拦截事件
	BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message, opts ...BroadcastOption) AsyncOperation[AsyncBroadcastResult]

	// BroadcastQuorumAsync 异步 Quorum 调用
	// 当达到多数派响应时完成
	// 可通过 opts 设置回调实时拦截 OnMajority/OnComplete 事件
	BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...BroadcastOption) AsyncOperation[QuorumResult]

	// ====== 批量写入（不同消息）======

	// WriteVAsync 异步批量写入（单向，不等待响应）
	// 适用于日志广播、监控数据等高吞吐场景
	WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult]

	// WriteVCallAsync 异步批量写入（带响应）
	// 返回每个节点的响应结果
	WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult]

	// ====== TaskPool 管理 ======
	// SetExecutorManager 设置任务池提供者
	// 用于统一管理任务的创建和生命周期
	SetExecutorManager(provider ExecutorManager)
}

// ==========================================
// BroadcastOption 选项模式
// ==========================================

// BroadcastOption 广播选项函数
type BroadcastOption func(*BroadcastConfig)

// BroadcastConfig 广播配置（内部使用）
type BroadcastConfig struct {
	// callbacks 复用 BroadcastListener 接口
	callbacks []BroadcastListener
}

// GetCallbacks 获取回调列表（副本）
func (c *BroadcastConfig) GetCallbacks() []BroadcastListener {
	if c == nil || len(c.callbacks) == 0 {
		return nil
	}
	// 返回副本防止外部修改
	result := make([]BroadcastListener, len(c.callbacks))
	copy(result, c.callbacks)
	return result
}

// AddCallback 添加回调
func (c *BroadcastConfig) AddCallback(cb BroadcastListener) {
	if c != nil {
		c.callbacks = append(c.callbacks, cb)
	}
}

// ==========================================
// 异步操作接口
// ==========================================

// AsyncOperation[T] 异步操作接口
// 封装异步计算的结果和状态
type AsyncOperation[T any] interface {
	// Await 阻塞等待结果
	Await(ctx context.Context) (T, error)

	// OnComplete 注册完成回调，返回回调 ID 用于注销
	OnComplete(callback func(T, error)) string

	// OnError 注册错误回调，返回回调 ID 用于注销
	OnError(callback func(error)) string

	// OnSuccess 注册成功回调，返回回调 ID 用于注销
	OnSuccess(callback func(T)) string

	// OffComplete 注销完成回调
	OffComplete(cbID string) error

	// WithTimeout 设置超时（P2-2: 链式超时设置）
	WithTimeout(timeout time.Duration) AsyncOperation[T]

	// IsDone 检查是否完成
	IsDone() bool

	// IsSuccess 检查是否成功
	IsSuccess() bool

	// IsFailed 检查是否失败
	IsFailed() bool

	// IsCanceled 检查是否取消
	IsCanceled() bool
}

// ==========================================
// 结果类型定义（异步专用）
// ==========================================

// AsyncBroadcastResult 异步广播结果
// 与 transport.BroadcastResult 类似，但格式更适合异步操作
type AsyncBroadcastResult struct {
	// Responses 成功响应列表
	Responses []PeerResponse
	// Errors 失败列表
	Errors []PeerError
	// Total 发送总数
	Total int
	// SuccessCount 成功数
	SuccessCount int
}

// PeerResponse 节点响应
type PeerResponse struct {
	Peer     model.PeerID
	Response model.Message
}

// PeerError 节点错误
type PeerError struct {
	Peer  model.PeerID
	Error error
}

// QuorumResult Quorum 结果
type QuorumResult struct {
	// Responses 成功响应（达到 quorum 数量）
	Responses []PeerResponse
	// Quorum Quorum 阈值
	Quorum int
	// Reached 是否达到 Quorum
	Reached bool
}

// 注意：WriteVResult 定义在 transport.go 中，直接复用

// ==========================================
// RPCAsync 实现配置
// ==========================================

// RPCAsyncConfig RPCAsync 配置
type RPCAsyncConfig struct {
	// ExecutorManager 任务池提供者（可选）
	ExecutorManager ExecutorManager
	// CallTimeoutMs 单播调用超时（毫秒），默认 30s
	CallTimeoutMs int64
	// BroadcastTimeoutMs 广播调用超时（毫秒），默认 60s
	BroadcastTimeoutMs int64
	// DefaultTimeoutMs 默认超时（毫秒）- 向后兼容，优先使用 CallTimeoutMs/BroadcastTimeoutMs
	// Deprecated: 使用 CallTimeoutMs 或 BroadcastTimeoutMs 替代
	DefaultTimeoutMs int64
	// MaxConcurrentCalls 最大并发调用数
	MaxConcurrentCalls int
}

// GetCallTimeout 获取单播超时（向后兼容）
func (c *RPCAsyncConfig) GetCallTimeout() int64 {
	if c.CallTimeoutMs > 0 {
		return c.CallTimeoutMs
	}
	return c.DefaultTimeoutMs
}

// GetBroadcastTimeout 获取广播超时（向后兼容）
func (c *RPCAsyncConfig) GetBroadcastTimeout() int64 {
	if c.BroadcastTimeoutMs > 0 {
		return c.BroadcastTimeoutMs
	}
	return c.DefaultTimeoutMs
}

// DefaultRPCAsyncConfig 默认配置
func DefaultRPCAsyncConfig() *RPCAsyncConfig {
	return &RPCAsyncConfig{
		CallTimeoutMs:      30000, // 30秒
		BroadcastTimeoutMs: 60000, // 60秒
		DefaultTimeoutMs:   30000, // 向后兼容
		MaxConcurrentCalls: 1000,
	}
}
