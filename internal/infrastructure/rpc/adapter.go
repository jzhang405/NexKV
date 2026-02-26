// Package rpc 异步 RPC 实现
//
// RPCAsyncAdapter 将 RPCSync 接口适配为 RPCAsync 接口
// 通过封装同步调用，提供 AsyncOperation[T] 风格的异步 API
package rpc

import (
	"context"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// RPCAsyncAdapter 适配器（桥接同步和异步接口）
// ==========================================

// RPCAsyncAdapter 将 RPCSync 接口适配为 RPCAsync 接口
// 通过封装同步调用，提供 AsyncOperation[T] 风格的异步 API
type RPCAsyncAdapter struct {
	rpc    service.RPCSync // 同步 RPC 接口
	config *service.RPCAsyncConfig
	mu     sync.RWMutex // 保护 config 的并发访问
}

// NewRPCAsyncAdapter 创建 RPCAsync 适配器
func NewRPCAsyncAdapter(rpc service.RPCSync, config *service.RPCAsyncConfig) service.RPCAsync {
	if config == nil {
		config = service.DefaultRPCAsyncConfig()
	}
	return &RPCAsyncAdapter{
		rpc:    rpc,
		config: config,
	}
}

// applyOptions 提取公共的选项处理逻辑
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) applyOptions(opts []service.BroadcastOption) service.BroadcastListener {
	a.mu.RLock()
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return ApplyBroadcastOptions(opts, provider)
}

// CallAsync 实现 RPCAsync 接口
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) CallAsync(ctx context.Context, to model.PeerID, req model.Message) service.AsyncOperation[service.ResponseMsg] {
	a.mu.RLock()
	timeoutMs := a.config.DefaultTimeoutMs
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncCall(ctx, a.rpc, to, req, timeoutMs, provider)
}

// CallAsyncWithTimeout 实现带超时的异步调用
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) CallAsyncWithTimeout(ctx context.Context, to model.PeerID, req model.Message, timeoutMs int64) service.AsyncOperation[service.ResponseMsg] {
	a.mu.RLock()
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncCall(ctx, a.rpc, to, req, timeoutMs, provider)
}

// BroadcastAsync 实现异步广播
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message, opts ...service.BroadcastOption) service.AsyncOperation[service.AsyncBroadcastResult] {
	callback := a.applyOptions(opts)
	a.mu.RLock()
	config := a.config
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncBroadcast(ctx, a.rpc, peers, req, config, callback, provider)
}

// BroadcastQuorumAsync 实现异步 Quorum 调用
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...service.BroadcastOption) service.AsyncOperation[service.QuorumResult] {
	callback := a.applyOptions(opts)
	a.mu.RLock()
	config := a.config
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncQuorum(ctx, a.rpc, peers, req, quorum, config, callback, provider)
}

// WriteVAsync 实现异步批量写入（单向）
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...service.BroadcastOption) service.AsyncOperation[service.WriteVResult] {
	callback := a.applyOptions(opts)
	a.mu.RLock()
	config := a.config
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncWriteV(ctx, a.rpc, targets, msgs, config, callback, provider)
}

// WriteVCallAsync 实现异步批量写入（带响应）
// 并发安全：使用读锁访问 config
func (a *RPCAsyncAdapter) WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...service.BroadcastOption) service.AsyncOperation[service.WriteVResult] {
	callback := a.applyOptions(opts)
	a.mu.RLock()
	config := a.config
	provider := a.config.TaskPoolProvider
	a.mu.RUnlock()
	return NewAsyncWriteVCall(ctx, a.rpc, targets, msgs, config, callback, provider)
}

// SetTaskPoolProvider 设置 任务池提供者
// 并发安全：使用写锁保护 config
func (a *RPCAsyncAdapter) SetTaskPoolProvider(provider service.TaskPoolProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.TaskPoolProvider = provider
}
