// Package service 定义领域服务接口
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
)

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
// 使用 model.Task[T] 作为返回类型，支持类型安全的异步操作
type RPCAsync interface {
	// ====== 单播 ======

	// CallAsync 异步单播调用
	// 返回 Task[ResponseMsg]，调用者可以链式处理结果
	CallAsync(ctx context.Context, to model.PeerID, req model.Message) model.Task[ResponseMsg]

	// CallAsyncWithTimeout 带超时的异步调用
	CallAsyncWithTimeout(ctx context.Context, to model.PeerID, req model.Message, timeoutMs int64) model.Task[ResponseMsg]

	// ====== 广播（同消息）======

	// BroadcastAsync 异步广播调用
	// 返回 Task[AsyncBroadcastResult]，包含每个节点的响应
	// 可通过 opts 设置回调实时拦截事件
	BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message, opts ...BroadcastOption) model.Task[AsyncBroadcastResult]

	// BroadcastQuorumAsync 异步 Quorum 调用
	// 当达到多数派响应时完成
	// 可通过 opts 设置回调实时拦截 OnMajority/OnComplete 事件
	BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...BroadcastOption) model.Task[QuorumResult]

	// ====== 批量写入（不同消息）======

	// WriteVAsync 异步批量写入（单向，不等待响应）
	// 适用于日志广播、监控数据等高吞吐场景
	WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) model.Task[WriteVResult]

	// WriteVCallAsync 异步批量写入（带响应）
	// 返回每个节点的响应结果
	WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) model.Task[WriteVResult]

	// ====== TaskPool 管理 ======
	// SetExecutor 设置任务执行器
	// 用于任务执行
	SetExecutor(provider TaskExecutor)
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
	// Executor 任务执行器（可选）
	Executor TaskExecutor
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
