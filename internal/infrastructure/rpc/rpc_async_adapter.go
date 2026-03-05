// Package rpc 异步 RPC 实现
//
// RPCAsyncAdapter 将 RPCSync 接口适配为 RPCAsync 接口
// 通过封装同步调用，提供 Task[T] 风格的异步 API
package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// RPCAsyncAdapter 适配器（桥接同步和异步接口）
// ==========================================

// RPCAsyncAdapter 将 RPCSync 接口适配为 RPCAsync 接口
// 通过封装同步调用，提供 Task[T] 风格的异步 API
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

// ==========================================
// 内部辅助方法
// ==========================================

// getConfig 安全获取 config 和 executor（读锁保护）
// 用于避免重复的 RLock/RUnlock 模式
func (a *RPCAsyncAdapter) getConfig() (*service.RPCAsyncConfig, service.TaskExecutor) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config, a.config.Executor
}

// getTimeout 安全获取默认超时（读锁保护）
func (a *RPCAsyncAdapter) getTimeout() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.DefaultTimeoutMs
}

// ==========================================
// RPCAsync 接口实现
// ==========================================

// applyOptions 提取公共的选项处理逻辑
func (a *RPCAsyncAdapter) applyOptions(opts []service.BroadcastOption) service.BroadcastListener {
	_, provider := a.getConfig()
	return ApplyBroadcastOptions(opts, provider)
}

// submitTask 提交任务执行
// 如果执行器为 nil，则在当前 goroutine 中同步执行
func (a *RPCAsyncAdapter) submitTask(task model.TaskRunner) {
	_, executor := a.getConfig()

	if executor != nil {
		// 提交给执行器异步执行
		_ = executor.Submit(context.Background(), task.SourceID(), task.Priority(), func(ctx context.Context) {
			// 创建一个空的 PipelineContext，因为 CallAsync 不需要提交子任务
			task.Run(ctx, nil)
		})
	} else {
		// 没有执行器，同步执行（用于测试场景）
		task.Run(context.Background(), nil)
	}
}

// CallAsync 实现 RPCAsync 接口
func (a *RPCAsyncAdapter) CallAsync(ctx context.Context, to model.PeerID, req model.Message) model.Task[service.ResponseMsg] {
	timeoutMs := a.getTimeout()
	task := NewRPCCallTask(
		a.rpc,
		to,
		req,
		model.SourceNetwork,
		time.Duration(timeoutMs)*time.Millisecond,
	)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// CallAsyncWithTimeout 实现带超时的异步调用
func (a *RPCAsyncAdapter) CallAsyncWithTimeout(ctx context.Context, to model.PeerID, req model.Message, timeoutMs int64) model.Task[service.ResponseMsg] {
	task := NewRPCCallTask(
		a.rpc,
		to,
		req,
		model.SourceNetwork,
		time.Duration(timeoutMs)*time.Millisecond,
	)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// BroadcastAsync 实现异步广播
func (a *RPCAsyncAdapter) BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message, opts ...service.BroadcastOption) model.Task[service.AsyncBroadcastResult] {
	callback := a.applyOptions(opts)
	config, _ := a.getConfig()

	task := NewRPCBroadcastTask(a.rpc, peers, req, config, callback)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// BroadcastQuorumAsync 实现异步 Quorum 调用
func (a *RPCAsyncAdapter) BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...service.BroadcastOption) model.Task[service.QuorumResult] {
	callback := a.applyOptions(opts)
	config, _ := a.getConfig()

	task := NewRPCQuorumTask(a.rpc, peers, req, quorum, config, callback)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// WriteVAsync 实现异步批量写入（单向）
func (a *RPCAsyncAdapter) WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...service.BroadcastOption) model.Task[service.WriteVResult] {
	callback := a.applyOptions(opts)
	config, _ := a.getConfig()

	task := NewRPCWriteVTask(a.rpc, targets, msgs, config, callback)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// WriteVCallAsync 实现异步批量写入（带响应）
func (a *RPCAsyncAdapter) WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...service.BroadcastOption) model.Task[service.WriteVResult] {
	callback := a.applyOptions(opts)
	config, _ := a.getConfig()

	task := NewRPCWriteVCallTask(a.rpc, targets, msgs, config, callback)

	// 提交任务执行
	a.submitTask(task)

	return task
}

// SetExecutor 设置任务执行器（写锁保护）
func (a *RPCAsyncAdapter) SetExecutor(provider service.TaskExecutor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.Executor = provider
}
