// Package service 定义领域服务接口
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
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
	// 可通过 opts 设置回调实时拦截 OnMajority/OnFullDone 事件
	BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...BroadcastOption) AsyncOperation[QuorumResult]

	// ====== 批量写入（不同消息）======

	// WriteVAsync 异步批量写入（单向，不等待响应）
	// 适用于日志广播、监控数据等高吞吐场景
	WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult]

	// WriteVCallAsync 异步批量写入（带响应）
	// 返回每个节点的响应结果
	WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult]

	// ====== Goroutine 管理 ======
	// SetGoroutineProvider 设置 goroutine 提供者
	// 用于统一管理 goroutine 的创建和生命周期
	SetGoroutineProvider(provider GoroutineProvider)
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

// OnMajority 添加多数派达成回调
// 复用 BroadcastListener.OnMajorityReached
func OnMajority(callback func(stats BroadcastStats)) BroadcastOption {
	return func(cfg *BroadcastConfig) {
		cfg.callbacks = append(cfg.callbacks, &funcListener{
			onMajority: callback,
		})
	}
}

// OnFullDone 添加全部完成回调
// 复用 BroadcastListener.OnFullDone
func OnFullDone(callback func(stats BroadcastStats)) BroadcastOption {
	return func(cfg *BroadcastConfig) {
		cfg.callbacks = append(cfg.callbacks, &funcListener{
			onFullDone: callback,
		})
	}
}

// OnSuccess 添加单个成功回调
// 复用 BroadcastListener.OnSuccess
func OnSuccess(callback func(peer model.PeerID, resp model.Message, stats BroadcastStats)) BroadcastOption {
	return func(cfg *BroadcastConfig) {
		cfg.callbacks = append(cfg.callbacks, &funcListener{
			onSuccess: callback,
		})
	}
}

// OnFailure 添加单个失败回调
// 复用 BroadcastListener.OnFailure
func OnFailure(callback func(peer model.PeerID, err error, stats BroadcastStats)) BroadcastOption {
	return func(cfg *BroadcastConfig) {
		cfg.callbacks = append(cfg.callbacks, &funcListener{
			onFailure: callback,
		})
	}
}

// funcListener 函数式回调适配器
type funcListener struct {
	NoOpListener
	onMajority func(stats BroadcastStats)
	onFullDone func(stats BroadcastStats)
	onSuccess  func(peer model.PeerID, resp model.Message, stats BroadcastStats)
	onFailure  func(peer model.PeerID, err error, stats BroadcastStats)
}

func (c *funcListener) OnMajorityReached(stats BroadcastStats) {
	if c.onMajority != nil {
		c.onMajority(stats)
	}
}

func (c *funcListener) OnFullDone(stats BroadcastStats) {
	if c.onFullDone != nil {
		c.onFullDone(stats)
	}
}

func (c *funcListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	if c.onSuccess != nil {
		c.onSuccess(peer, resp, stats)
	}
}

func (c *funcListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	if c.onFailure != nil {
		c.onFailure(peer, err, stats)
	}
}

// multiListener 多回调组合器
type multiListener struct {
	callbacks []BroadcastListener
}

func (m *multiListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnSuccess(peer, resp, stats)
	}
}

func (m *multiListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnFailure(peer, err, stats)
	}
}

func (m *multiListener) OnMajorityReached(stats BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnMajorityReached(stats)
	}
}

func (m *multiListener) OnFullDone(stats BroadcastStats) {
	for _, cb := range m.callbacks {
		cb.OnFullDone(stats)
	}
}

// ==========================================
// asyncListenerWrapper 通过 GoroutineProvider 执行回调
// ==========================================

// asyncListenerWrapper 通过 GoroutineProvider 异步执行回调
// 避免无限制创建 goroutine，提供资源控制
type asyncListenerWrapper struct {
	callbacks         []BroadcastListener
	goroutineProvider GoroutineProvider
}

func (w *asyncListenerWrapper) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnSuccess(peer, resp, stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnFailure(peer, err, stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnMajorityReached(stats BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnMajorityReached(stats) })
		})
	}
}

func (w *asyncListenerWrapper) OnFullDone(stats BroadcastStats) {
	for _, cb := range w.callbacks {
		cb := cb
		_ = w.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			safeListenerExec(func() { cb.OnFullDone(stats) })
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

// applyBroadcastOptions 应用选项并返回组合后的回调
// 如果提供了 goroutineProvider，则使用 asyncListenerWrapper 包装回调
func applyBroadcastOptions(opts []BroadcastOption, goroutineProvider GoroutineProvider) BroadcastListener {
	if len(opts) == 0 {
		return nil
	}

	cfg := &BroadcastConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if len(cfg.callbacks) == 0 {
		return nil
	}

	// 如果有 GoroutineProvider，使用 asyncListenerWrapper 包装
	if goroutineProvider != nil {
		return &asyncListenerWrapper{
			callbacks:         cfg.callbacks,
			goroutineProvider: goroutineProvider,
		}
	}

	// 否则直接组合回调（同步执行）
	if len(cfg.callbacks) == 1 {
		return cfg.callbacks[0]
	}

	return &multiListener{callbacks: cfg.callbacks}
}

// ==========================================
// 异步操作接口
// ==========================================

// AsyncOperation[T] 异步操作接口
// 封装异步计算的结果和状态
type AsyncOperation[T any] interface {
	// Await 阻塞等待结果
	Await(ctx context.Context) (T, error)

	// OnComplete 注册完成回调
	OnComplete(callback func(T, error)) AsyncOperation[T]

	// OnError 注册错误回调
	OnError(callback func(error)) AsyncOperation[T]

	// OnSuccess 注册成功回调
	OnSuccess(callback func(T)) AsyncOperation[T]

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
	// GoroutineProvider 协程池提供者（可选）
	GoroutineProvider GoroutineProvider
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

// ==========================================
// RPCAsyncAdapter 适配器（桥接同步和异步接口）
// ==========================================

// RPCAsyncAdapter 将 RPCSync 接口适配为 RPCAsync 接口
// 通过封装同步调用，提供 AsyncOperation[T] 风格的异步 API
type RPCAsyncAdapter struct {
	rpc    RPCSync // 同步 RPC 接口（RPC 是 RPCSync 的类型别名）
	config *RPCAsyncConfig
}

// NewRPCAsyncAdapter 创建 RPCAsync 适配器
func NewRPCAsyncAdapter(rpc RPCSync, config *RPCAsyncConfig) *RPCAsyncAdapter {
	if config == nil {
		config = DefaultRPCAsyncConfig()
	}
	return &RPCAsyncAdapter{
		rpc:    rpc,
		config: config,
	}
}

// CallAsync 实现 RPCAsync 接口
func (a *RPCAsyncAdapter) CallAsync(ctx context.Context, to model.PeerID, req model.Message) AsyncOperation[ResponseMsg] {
	return NewAsyncCall(ctx, a.rpc, to, req, a.config.DefaultTimeoutMs, a.config.GoroutineProvider)
}

// CallAsyncWithTimeout 实现带超时的异步调用
func (a *RPCAsyncAdapter) CallAsyncWithTimeout(ctx context.Context, to model.PeerID, req model.Message, timeoutMs int64) AsyncOperation[ResponseMsg] {
	return NewAsyncCall(ctx, a.rpc, to, req, timeoutMs, a.config.GoroutineProvider)
}

// BroadcastAsync 实现异步广播
func (a *RPCAsyncAdapter) BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message, opts ...BroadcastOption) AsyncOperation[AsyncBroadcastResult] {
	callback := applyBroadcastOptions(opts, a.config.GoroutineProvider)
	return NewAsyncBroadcast(ctx, a.rpc, peers, req, a.config, callback, a.config.GoroutineProvider)
}

// BroadcastQuorumAsync 实现异步 Quorum 调用
func (a *RPCAsyncAdapter) BroadcastQuorumAsync(ctx context.Context, peers []model.PeerID, req model.Message, quorum int, opts ...BroadcastOption) AsyncOperation[QuorumResult] {
	callback := applyBroadcastOptions(opts, a.config.GoroutineProvider)
	return NewAsyncQuorum(ctx, a.rpc, peers, req, quorum, a.config, callback, a.config.GoroutineProvider)
}

// WriteVAsync 实现异步批量写入（单向）
func (a *RPCAsyncAdapter) WriteVAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult] {
	callback := applyBroadcastOptions(opts, a.config.GoroutineProvider)
	return NewAsyncWriteV(ctx, a.rpc, targets, msgs, a.config, callback, a.config.GoroutineProvider)
}

// WriteVCallAsync 实现异步批量写入（带响应）
func (a *RPCAsyncAdapter) WriteVCallAsync(ctx context.Context, targets []model.PeerID, msgs []model.Message, opts ...BroadcastOption) AsyncOperation[WriteVResult] {
	callback := applyBroadcastOptions(opts, a.config.GoroutineProvider)
	return NewAsyncWriteVCall(ctx, a.rpc, targets, msgs, a.config, callback, a.config.GoroutineProvider)
}
