// Package rpc 广播监听器实现
//
// 从 domain/service 迁移过来的监听器实现
// 遵循 DDD 架构：领域层保留接口，基础设施层负责实现
package rpc

import (
	"context"
	"log/slog"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// funcListener 函数式回调适配器
// ==========================================

// funcListener 函数式回调适配器
type funcListener struct {
	service.NoOpListener
	onMajority func(stats service.BroadcastStats)
	onFullDone func(stats service.BroadcastStats)
	onSuccess  func(peer model.PeerID, resp model.Message, stats service.BroadcastStats)
	onFailure  func(peer model.PeerID, err error, stats service.BroadcastStats)
}

func (c *funcListener) OnMajority(stats service.BroadcastStats) {
	if c.onMajority != nil {
		c.onMajority(stats)
	}
}

func (c *funcListener) OnComplete(stats service.BroadcastStats) {
	if c.onFullDone != nil {
		c.onFullDone(stats)
	}
}

func (c *funcListener) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	if c.onSuccess != nil {
		c.onSuccess(peer, resp, stats)
	}
}

func (c *funcListener) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	if c.onFailure != nil {
		c.onFailure(peer, err, stats)
	}
}

// ==========================================
// multiListener 多回调组合器
// ==========================================

// multiListener 多回调组合器
type multiListener struct {
	callbacks []service.BroadcastListener
}

func (m *multiListener) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnSuccess(peer, resp, stats)
	}
}

func (m *multiListener) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnFailure(peer, err, stats)
	}
}

func (m *multiListener) OnMajority(stats service.BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnMajority(stats)
	}
}

func (m *multiListener) OnComplete(stats service.BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnComplete(stats)
	}
}

// ==========================================
// asyncListenerWrapper 异步回调包装器
// ==========================================

// asyncListenerWrapper 通过 GoroutineProvider 异步执行回调
// 避免无限制创建 goroutine，提供资源控制
type asyncListenerWrapper struct {
	callbacks         []service.BroadcastListener
	goroutineProvider service.GoroutineProvider
}

func (w *asyncListenerWrapper) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnSuccess(peer, resp, stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnFailure(peer, err, stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnMajority(stats service.BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnMajority(stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnComplete(stats service.BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnComplete(stats) })
		})
	}
}

// safeListenerExec 安全执行回调（带 panic 恢复）
func safeListenerExec(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[AsyncCallback] panic recovered", "panic", r)
		}
	}()
	fn()
}

// ==========================================
// 选项函数
// ==========================================

// OnMajority 添加多数派达成回调
// 复用 BroadcastListener.OnMajority
func OnMajority(callback func(stats service.BroadcastStats)) service.BroadcastOption {
	return func(cfg *service.BroadcastConfig) {
		cfg.AddCallback(&funcListener{
			onMajority: callback,
		})
	}
}

// OnComplete 添加全部完成回调
// 复用 BroadcastListener.OnComplete
func OnComplete(callback func(stats service.BroadcastStats)) service.BroadcastOption {
	return func(cfg *service.BroadcastConfig) {
		cfg.AddCallback(&funcListener{
			onFullDone: callback,
		})
	}
}

// OnSuccess 添加单个成功回调
// 复用 BroadcastListener.OnSuccess
func OnSuccess(callback func(peer model.PeerID, resp model.Message, stats service.BroadcastStats)) service.BroadcastOption {
	return func(cfg *service.BroadcastConfig) {
		cfg.AddCallback(&funcListener{
			onSuccess: callback,
		})
	}
}

// OnFailure 添加单个失败回调
// 复用 BroadcastListener.OnFailure
func OnFailure(callback func(peer model.PeerID, err error, stats service.BroadcastStats)) service.BroadcastOption {
	return func(cfg *service.BroadcastConfig) {
		cfg.AddCallback(&funcListener{
			onFailure: callback,
		})
	}
}

// ApplyBroadcastOptions 应用选项并返回组合后的回调
// 如果提供了 goroutineProvider，则使用 asyncListenerWrapper 包装回调
func ApplyBroadcastOptions(opts []service.BroadcastOption, goroutineProvider service.GoroutineProvider) service.BroadcastListener {
	if len(opts) == 0 {
		return nil
	}

	cfg := &service.BroadcastConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	callbacks := cfg.GetCallbacks()
	if len(callbacks) == 0 {
		return nil
	}

	// 如果有 GoroutineProvider，使用 asyncListenerWrapper 包装
	if goroutineProvider != nil {
		return &asyncListenerWrapper{
			callbacks:         callbacks,
			goroutineProvider: goroutineProvider,
		}
	}

	// 否则直接组合回调（同步执行）
	if len(callbacks) == 1 {
		return callbacks[0]
	}

	return &multiListener{callbacks: callbacks}
}
