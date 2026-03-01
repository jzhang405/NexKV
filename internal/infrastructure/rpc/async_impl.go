// Package rpc 异步 RPC 实现
//
// 从 domain/service 迁移过来的 AsyncOperation 实现
// 遵循 DDD 架构：领域层保留接口，基础设施层负责实现
package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 辅助函数
// ==========================================

// submitTask 提交任务到 TaskPoolProvider 或回退到 goroutine
func submitTask(
	ctx context.Context,
	provider service.TaskExecutor,
	task func(context.Context),
	onFailure func(error),
) {
	// 包装任务添加 panic 保护
	wrappedTask := func(ctx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[AsyncOperation] task goroutine panic recovered", "panic", r)
			}
		}()
		task(ctx)
	}

	if provider != nil {
		if err := provider.Submit(ctx, service.PriorityNormal, wrappedTask); err != nil {
			// 提交失败：回退到直接启动 goroutine
			slog.Warn("[AsyncOperation] failed to submit task, falling back to direct goroutine", "error", err)
			go wrappedTask(ctx)
		}
		// 提交成功：任务由 provider 执行
		return
	}
	// 无 provider：直接启动 goroutine
	go wrappedTask(ctx)
}

// validateRPCAndConfig 验证 rpc 和 config 参数
func validateRPCAndConfig(rpc service.RPCSync, config *service.RPCAsyncConfig) error {
	if rpc == nil {
		return service.ErrNilRPC
	}
	if config == nil {
		return service.ErrNilConfig
	}
	return nil
}

// createTracker 创建 BroadcastProgress 并设置回调
func createTracker(name string, peers []model.PeerID, callback service.BroadcastListener) *BroadcastProgress {
	tracker := NewBroadcastProgress(name, peers)
	if callback != nil {
		tracker.SetCallback(callback)
	}
	return tracker
}

// convertToPeerResponses 转换响应格式
func convertToPeerResponses(peers []model.PeerID, responses []model.Message) []service.PeerResponse {
	result := make([]service.PeerResponse, 0, len(peers))
	for i, peer := range peers {
		if i < len(responses) && responses[i] != nil {
			result = append(result, service.PeerResponse{
				Peer:     peer,
				Response: responses[i],
			})
		}
	}
	return result
}

// ==========================================
// AsyncOperation[T] 实现
// ==========================================

// asyncOpImpl AsyncOperation 的通用实现
type asyncOpImpl[T any] struct {
	resultCh          chan T     // 缓冲通道，容量为1
	errCh             chan error // 同上
	done              atomic.Bool
	status            atomic.Int32 // 0=pending, 1=running, 2=completed, 3=failed, 4=canceled, 5=discarded, 6=timeout
	callbacks         map[string]func(T, error)
	cbSeq             atomic.Int64
	cbMu              sync.RWMutex
	value             T
	err               error
	goroutineProvider service.TaskExecutor
}

// newAsyncOp 创建异步操作
func newAsyncOp[T any](provider service.TaskExecutor) *asyncOpImpl[T] {
	return &asyncOpImpl[T]{
		resultCh:          make(chan T, 1),
		errCh:             make(chan error, 1),
		callbacks:         make(map[string]func(T, error)),
		goroutineProvider: provider,
	}
}

// Await 阻塞等待结果
func (op *asyncOpImpl[T]) Await(ctx context.Context) (T, error) {
	var zero T
	select {
	case v := <-op.resultCh:
		return v, nil
	case err := <-op.errCh:
		return zero, err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// OnComplete 注册完成回调，返回回调 ID 用于注销
func (op *asyncOpImpl[T]) OnComplete(callback func(T, error)) string {
	return op.registerCallback(callback)
}

// OnError 注册错误回调，返回回调 ID 用于注销
func (op *asyncOpImpl[T]) OnError(callback func(error)) string {
	return op.registerCallback(func(_ T, err error) {
		if err != nil {
			callback(err)
		}
	})
}

// OnSuccess 注册成功回调，返回回调 ID 用于注销
func (op *asyncOpImpl[T]) OnSuccess(callback func(T)) string {
	return op.registerCallback(func(v T, err error) {
		if err == nil {
			callback(v)
		}
	})
}

// registerCallback 通用的回调注册逻辑
// 返回回调 ID，用于后续注销
// 竞态安全：使用双检锁模式确保状态一致性
func (op *asyncOpImpl[T]) registerCallback(callback func(T, error)) string {
	// 第一重检查（无锁）：快速路径，如果已完成直接执行回调
	if op.done.Load() {
		op.cbMu.Lock()
		v := op.value
		e := op.err
		op.cbMu.Unlock()
		op.safeExecuteCallback(callback, v, e)
		return ""
	}

	// 第二重检查（加锁）：确保竞态安全
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if op.done.Load() {
		// 在锁内复制数据，然后在锁外执行
		v := op.value
		e := op.err
		// 使用 defer 确保解锁后执行
		defer op.safeExecuteCallback(callback, v, e)
		return ""
	}

	// 生成唯一回调 ID
	id := fmt.Sprintf("cb_%d", op.cbSeq.Add(1))
	op.callbacks[id] = callback
	return id
}

// safeExecuteCallback 安全执行回调
func (op *asyncOpImpl[T]) safeExecuteCallback(callback func(T, error), v T, err error) {
	executor := func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[AsyncOperation] callback panic recovered", "panic", r)
			}
		}()
		callback(v, err)
	}

	if op.goroutineProvider != nil {
		if submitErr := op.goroutineProvider.Submit(context.Background(), service.PriorityNormal, func(ctx context.Context) {
			executor()
		}); submitErr != nil {
			// CRITICAL FIX: Submit 失败时回退到直接启动 goroutine
			slog.Warn("[AsyncOperation] failed to submit callback, falling back to direct goroutine", "error", submitErr)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("[AsyncOperation] fallback goroutine panic recovered", "panic", r)
					}
				}()
				executor()
			}()
		}
	} else {
		go executor()
	}
}

// WithTimeout 设置超时
func (op *asyncOpImpl[T]) WithTimeout(timeout time.Duration) service.AsyncOperation[T] {
	wrapped := &timeoutAsyncOp[T]{
		inner:   op,
		timeout: timeout,
	}
	return wrapped
}

// IsDone 检查是否完成
func (op *asyncOpImpl[T]) IsDone() bool {
	return op.done.Load()
}

// IsSuccess 检查是否成功
func (op *asyncOpImpl[T]) IsSuccess() bool {
	return op.status.Load() == int32(service.StatusCompleted)
}

// IsFailed 检查是否失败
func (op *asyncOpImpl[T]) IsFailed() bool {
	return op.status.Load() == int32(service.StatusFailed)
}

// IsCanceled 检查是否取消
func (op *asyncOpImpl[T]) IsCanceled() bool {
	return op.status.Load() == int32(service.StatusCanceled)
}

// ==========================================
// pkg/async.AsyncOperation[T] 接口实现
// ==========================================

// Get 实现 pkg/async.AsyncOperation 接口 - 阻塞等待结果
func (op *asyncOpImpl[T]) Get(ctx context.Context) (T, error) {
	return op.Await(ctx)
}

// Status 返回操作状态
func (op *asyncOpImpl[T]) Status() service.OperationStatus {
	switch op.status.Load() {
	case int32(service.StatusPending):
		return service.StatusPending
	case int32(service.StatusRunning):
		return service.StatusRunning
	case int32(service.StatusCompleted):
		return service.StatusCompleted
	case int32(service.StatusFailed):
		return service.StatusFailed
	case int32(service.StatusCanceled):
		return service.StatusCanceled
	case int32(service.StatusDiscarded):
		return service.StatusDiscarded
	case int32(service.StatusTimeout):
		return service.StatusTimeout
	default:
		return service.StatusPending
	}
}

// Cancel 取消操作
func (op *asyncOpImpl[T]) Cancel() (bool, error) {
	if !op.done.CompareAndSwap(false, true) {
		return false, errors.ErrOperationAlreadyCompleted
	}
	op.status.Store(int32(service.StatusCanceled))
	var zero T
	select {
	case op.errCh <- errors.ErrOperationCanceled:
	default:
	}
	op.executeCallbacks(zero, errors.ErrOperationCanceled)
	return true, nil
}

// Discard 丢弃结果
func (op *asyncOpImpl[T]) Discard() error {
	_, err := op.Cancel()
	return err
}

// IsStarted 检查是否已启动
func (op *asyncOpImpl[T]) IsStarted() bool {
	return true
}

// OffComplete 注销完成回调
func (op *asyncOpImpl[T]) OffComplete(cbID string) error {
	if cbID == "" {
		return errors.ErrCallbackIDEmpty
	}

	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if _, exists := op.callbacks[cbID]; !exists {
		return errors.ErrCallbackNotFound
	}

	delete(op.callbacks, cbID)
	return nil
}

// ==========================================
// timeoutAsyncOp 超时包装器
// ==========================================

// timeoutAsyncOp 为 AsyncOperation 添加超时功能
type timeoutAsyncOp[T any] struct {
	inner   service.AsyncOperation[T]
	timeout time.Duration
}

// Await 带超时的阻塞等待
func (op *timeoutAsyncOp[T]) Await(ctx context.Context) (T, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, op.timeout)
	defer cancel()
	return op.inner.Await(timeoutCtx)
}

// OnComplete 委托给内部操作
func (op *timeoutAsyncOp[T]) OnComplete(callback func(T, error)) string {
	return op.inner.OnComplete(callback)
}

// OnError 委托给内部操作
func (op *timeoutAsyncOp[T]) OnError(callback func(error)) string {
	return op.inner.OnError(callback)
}

// OnSuccess 委托给内部操作
func (op *timeoutAsyncOp[T]) OnSuccess(callback func(T)) string {
	return op.inner.OnSuccess(callback)
}

// OffComplete 委托给内部操作
func (op *timeoutAsyncOp[T]) OffComplete(cbID string) error {
	return op.inner.OffComplete(cbID)
}

// WithTimeout 支持链式设置超时
func (op *timeoutAsyncOp[T]) WithTimeout(timeout time.Duration) service.AsyncOperation[T] {
	newTimeout := op.timeout
	if timeout < newTimeout {
		newTimeout = timeout
	}
	return &timeoutAsyncOp[T]{
		inner:   op.inner,
		timeout: newTimeout,
	}
}

// IsDone 委托给内部操作
func (op *timeoutAsyncOp[T]) IsDone() bool {
	return op.inner.IsDone()
}

// IsSuccess 委托给内部操作
func (op *timeoutAsyncOp[T]) IsSuccess() bool {
	return op.inner.IsSuccess()
}

// IsFailed 委托给内部操作
func (op *timeoutAsyncOp[T]) IsFailed() bool {
	return op.inner.IsFailed()
}

// IsCanceled 委托给内部操作
func (op *timeoutAsyncOp[T]) IsCanceled() bool {
	return op.inner.IsCanceled()
}

// Get 实现 pkg/async.AsyncOperation 接口
func (op *timeoutAsyncOp[T]) Get(ctx context.Context) (T, error) {
	return op.Await(ctx)
}

// Status 返回操作状态
func (op *timeoutAsyncOp[T]) Status() service.OperationStatus {
	if s, ok := op.inner.(interface {
		Status() service.OperationStatus
	}); ok {
		return s.Status()
	}
	if op.inner.IsDone() {
		if op.inner.IsSuccess() {
			return service.StatusCompleted
		} else if op.inner.IsFailed() {
			return service.StatusFailed
		} else if op.inner.IsCanceled() {
			return service.StatusCanceled
		}
	}
	return service.StatusPending
}

// Cancel 取消操作
func (op *timeoutAsyncOp[T]) Cancel() (bool, error) {
	if c, ok := op.inner.(interface{ Cancel() (bool, error) }); ok {
		return c.Cancel()
	}
	return false, errors.ErrCancelNotSupported
}

// Discard 丢弃结果
func (op *timeoutAsyncOp[T]) Discard() error {
	if d, ok := op.inner.(interface{ Discard() error }); ok {
		return d.Discard()
	}
	return errors.ErrDiscardNotSupported
}

// IsStarted 检查是否已启动
func (op *timeoutAsyncOp[T]) IsStarted() bool {
	if s, ok := op.inner.(interface{ IsStarted() bool }); ok {
		return s.IsStarted()
	}
	return true
}

// ==========================================
// 内部辅助方法
// ==========================================

// complete 完成操作（成功）
func (op *asyncOpImpl[T]) complete(v T) {
	if op.done.CompareAndSwap(false, true) {
		op.value = v
		op.status.Store(int32(service.StatusCompleted))
		select {
		case op.resultCh <- v:
		default:
		}
		op.executeCallbacks(v, nil)
	}
}

// fail 完成操作（失败）
func (op *asyncOpImpl[T]) fail(err error) {
	var zero T
	if op.done.CompareAndSwap(false, true) {
		op.err = err
		op.status.Store(int32(service.StatusFailed))
		select {
		case op.errCh <- err:
		default:
		}
		op.executeCallbacks(zero, err)
	}
}

// executeCallbacks 执行回调
// 执行完成后清理 callbacks map 防止内存泄漏
func (op *asyncOpImpl[T]) executeCallbacks(v T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), 0, len(op.callbacks))
	for _, cb := range op.callbacks {
		callbacks = append(callbacks, cb)
	}
	op.cbMu.RUnlock()

	for _, cb := range callbacks {
		op.safeExecuteCallback(cb, v, err)
	}

	// 清理 callbacks map 防止内存泄漏
	op.cbMu.Lock()
	op.callbacks = make(map[string]func(T, error))
	op.cbMu.Unlock()
}

// ==========================================
// NewAsyncCall 实现
// ==========================================

// asyncCall 单播异步调用
type asyncCall struct {
	*asyncOpImpl[service.ResponseMsg]
}

// submitAsyncTask 提交异步任务（统一处理 provider 和 fallback）
func submitAsyncTask(
	ctx context.Context,
	op *asyncOpImpl[service.ResponseMsg],
	provider service.TaskExecutor,
	timeoutMs int64,
	task func(ctx context.Context),
) {
	// CRITICAL FIX: 统一添加 panic 保护
	executor := func(callCtx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[AsyncOperation] task goroutine panic recovered", "panic", r)
			}
		}()
		op.status.Store(int32(service.StatusRunning))
		innerCtx, cancel := context.WithTimeout(callCtx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
		task(innerCtx)
	}

	if provider != nil {
		if err := provider.Submit(ctx, service.PriorityNormal, executor); err != nil {
			// 提交失败时回退到直接启动 goroutine
			slog.Warn("[AsyncOperation] failed to submit task, falling back to direct goroutine", "error", err)
			go executor(ctx)
		}
	} else {
		go executor(ctx)
	}
}

// NewAsyncCall 创建异步单播调用
func NewAsyncCall(
	ctx context.Context,
	rpc service.RPCSync,
	to model.PeerID,
	req model.Message,
	timeoutMs int64,
	provider service.TaskExecutor,
) service.AsyncOperation[service.ResponseMsg] {
	op := newAsyncOp[service.ResponseMsg](provider)

	// 输入验证
	if rpc == nil {
		op.fail(service.ErrNilRPC)
		return &asyncCall{asyncOpImpl: op}
	}
	if timeoutMs <= 0 {
		op.fail(errors.Wrapf(service.ErrInvalidTimeout, "timeoutMs must be positive, got %d", timeoutMs))
		return &asyncCall{asyncOpImpl: op}
	}

	// 提交异步任务
	submitAsyncTask(ctx, op, provider, timeoutMs, func(callCtx context.Context) {
		resp, err := rpc.Call(callCtx, to, req)
		if err != nil {
			op.fail(err)
			return
		}
		op.complete(service.ResponseMsg{Msg: resp, Err: nil})
	})

	return &asyncCall{asyncOpImpl: op}
}

// ==========================================
// NewAsyncBroadcast 实现
// ==========================================

// asyncBroadcast 广播异步调用
type asyncBroadcast struct {
	*asyncOpImpl[service.AsyncBroadcastResult]
}

// NewAsyncBroadcast 创建异步广播调用
func NewAsyncBroadcast(
	ctx context.Context,
	rpc service.RPCSync,
	peers []model.PeerID,
	req model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
	provider service.TaskExecutor,
) service.AsyncOperation[service.AsyncBroadcastResult] {
	op := newAsyncOp[service.AsyncBroadcastResult](provider)

	// 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncBroadcast{asyncOpImpl: op}
	}

	// 空切片检查
	if len(peers) == 0 {
		op.fail(service.ErrEmptyPeers)
		return &asyncBroadcast{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := createTracker("async-broadcast", peers, callback)

		result, err := rpc.BroadcastCall(callCtx, peers, req, service.ResponseAll, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		asyncResult := service.AsyncBroadcastResult{
			Total:        len(peers),
			SuccessCount: len(result.SuccessPeers),
		}

		for i, peer := range result.SuccessPeers {
			if i < len(result.Responses) {
				asyncResult.Responses = append(asyncResult.Responses, service.PeerResponse{
					Peer:     peer,
					Response: result.Responses[i],
				})
			}
		}

		for _, peer := range result.FailedPeers {
			asyncResult.Errors = append(asyncResult.Errors, service.PeerError{
				Peer:  peer,
				Error: service.ErrPeerUnreachable,
			})
		}

		op.complete(asyncResult)
	}

	submitTask(ctx, provider, task, op.fail)

	return &asyncBroadcast{asyncOpImpl: op}
}

// ==========================================
// NewAsyncQuorum 实现
// ==========================================

// asyncQuorum Quorum 异步调用
type asyncQuorum struct {
	*asyncOpImpl[service.QuorumResult]
}

// NewAsyncQuorum 创建异步 Quorum 调用
func NewAsyncQuorum(
	ctx context.Context,
	rpc service.RPCSync,
	peers []model.PeerID,
	req model.Message,
	quorum int,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
	provider service.TaskExecutor,
) service.AsyncOperation[service.QuorumResult] {
	op := newAsyncOp[service.QuorumResult](provider)

	// 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncQuorum{asyncOpImpl: op}
	}

	// 空切片检查
	if len(peers) == 0 {
		op.fail(service.ErrEmptyPeers)
		return &asyncQuorum{asyncOpImpl: op}
	}

	// 输入验证
	if quorum <= 0 || quorum > len(peers) {
		op.fail(errors.Wrapf(service.ErrInvalidQuorum, "quorum must be between 1 and %d, got %d", len(peers), quorum))
		return &asyncQuorum{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := createTracker("async-quorum", peers, callback)

		result, err := rpc.BroadcastCall(callCtx, peers, req, service.ResponseMajority, tracker)
		if err != nil {
			op.complete(service.QuorumResult{
				Responses: convertToPeerResponses(result.SuccessPeers, result.Responses),
				Quorum:    quorum,
				Reached:   false,
			})
			return
		}

		op.complete(service.QuorumResult{
			Responses: convertToPeerResponses(result.SuccessPeers, result.Responses),
			Quorum:    quorum,
			Reached:   len(result.SuccessPeers) >= quorum,
		})
	}

	submitTask(ctx, provider, task, op.fail)

	return &asyncQuorum{asyncOpImpl: op}
}

// ==========================================
// NewAsyncWriteV 实现
// ==========================================

// asyncWriteV 批量写入异步调用（单向）
type asyncWriteV struct {
	*asyncOpImpl[service.WriteVResult]
}

// NewAsyncWriteV 创建异步批量写入（单向）
func NewAsyncWriteV(
	ctx context.Context,
	rpc service.RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
	provider service.TaskExecutor,
) service.AsyncOperation[service.WriteVResult] {
	op := newAsyncOp[service.WriteVResult](provider)

	// 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncWriteV{asyncOpImpl: op}
	}

	// 输入验证
	if len(targets) != len(msgs) {
		op.fail(errors.Wrapf(service.ErrTargetsMsgsMismatch, "targets (%d) vs msgs (%d)", len(targets), len(msgs)))
		return &asyncWriteV{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := createTracker("async-writev", targets, callback)

		err := rpc.WriteV(callCtx, targets, msgs, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		op.complete(service.WriteVResult{
			SuccessPeers: targets,
			FailedPeers:  nil,
			Responses:    nil,
		})
	}

	submitTask(ctx, provider, task, op.fail)

	return &asyncWriteV{asyncOpImpl: op}
}

// ==========================================
// NewAsyncWriteVCall 实现
// ==========================================

// asyncWriteVCall 批量写入异步调用（带响应）
type asyncWriteVCall struct {
	*asyncOpImpl[service.WriteVResult]
}

// NewAsyncWriteVCall 创建异步批量写入（带响应）
func NewAsyncWriteVCall(
	ctx context.Context,
	rpc service.RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
	provider service.TaskExecutor,
) service.AsyncOperation[service.WriteVResult] {
	op := newAsyncOp[service.WriteVResult](provider)

	// 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncWriteVCall{asyncOpImpl: op}
	}

	// 输入验证
	if len(targets) != len(msgs) {
		op.fail(errors.Wrapf(service.ErrTargetsMsgsMismatch, "targets (%d) vs msgs (%d)", len(targets), len(msgs)))
		return &asyncWriteVCall{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := createTracker("async-writevcall", targets, callback)

		result, err := rpc.WriteVCall(callCtx, targets, msgs, service.ResponseAll, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		op.complete(result)
	}

	submitTask(ctx, provider, task, op.fail)

	return &asyncWriteVCall{asyncOpImpl: op}
}
