// Package service 定义领域服务接口
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// P0-2: 输入验证错误定义
var (
	// ErrTargetsMsgsMismatch targets 和 msgs 长度不匹配
	ErrTargetsMsgsMismatch = errors.New("targets and msgs length mismatch")
	// ErrInvalidQuorum quorum 参数无效
	ErrInvalidQuorum = errors.New("invalid quorum value")
	// ErrInvalidTimeout timeoutMs 参数无效
	ErrInvalidTimeout = errors.New("invalid timeout value")
	// ErrEmptyPeers peers 切片为空（P1-2）
	ErrEmptyPeers = errors.New("peers slice is empty")
	// ErrNilConfig config 参数为 nil（P2-2）
	ErrNilConfig = errors.New("config is nil")
	// ErrNilRPC rpc 参数为 nil（P2-3）
	ErrNilRPC = errors.New("rpc is nil")
)

// ==========================================
// 辅助函数（P1: DRY 简化）
// ==========================================

// submitTask 提交任务到 GoroutineProvider 或回退到 goroutine
func submitTask(
	ctx context.Context,
	provider GoroutineProvider,
	task func(context.Context),
	onFailure func(error),
) {
	if provider != nil {
		if err := provider.Submit(ctx, task); err != nil {
			onFailure(fmt.Errorf("submit task failed: %w", err))
		}
	} else {
		go task(ctx)
	}
}

// validateRPCAndConfig 验证 rpc 和 config 参数
func validateRPCAndConfig(rpc RPCSync, config *RPCAsyncConfig) error {
	if rpc == nil {
		return ErrNilRPC
	}
	if config == nil {
		return ErrNilConfig
	}
	return nil
}

// ==========================================
// AsyncOperation[T] 实现
// ==========================================

// asyncOpImpl AsyncOperation 的通用实现
type asyncOpImpl[T any] struct {
	resultCh          chan T     // P1-1: 缓冲通道，容量为1，一次性操作后由 GC 回收
	errCh             chan error // P1-1: 同上
	done              atomic.Bool
	status            atomic.Int32 // 0=pending, 1=success, 2=failed, 3=canceled
	callbacks         []func(T, error)
	cbMu              sync.RWMutex
	value             T
	err               error
	goroutineProvider GoroutineProvider // P1-1: 用于执行回调
}

// newAsyncOp 创建异步操作
func newAsyncOp[T any](provider GoroutineProvider) *asyncOpImpl[T] {
	return &asyncOpImpl[T]{
		resultCh:          make(chan T, 1),
		errCh:             make(chan error, 1),
		callbacks:         make([]func(T, error), 0),
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

// OnComplete 注册完成回调
func (op *asyncOpImpl[T]) OnComplete(callback func(T, error)) AsyncOperation[T] {
	return op.registerCallback(callback)
}

// OnError 注册错误回调
func (op *asyncOpImpl[T]) OnError(callback func(error)) AsyncOperation[T] {
	return op.registerCallback(func(_ T, err error) {
		if err != nil {
			callback(err)
		}
	})
}

// OnSuccess 注册成功回调
func (op *asyncOpImpl[T]) OnSuccess(callback func(T)) AsyncOperation[T] {
	return op.registerCallback(func(v T, err error) {
		if err == nil {
			callback(v)
		}
	})
}

// registerCallback 通用的回调注册逻辑（P0: DRY 简化）
func (op *asyncOpImpl[T]) registerCallback(callback func(T, error)) AsyncOperation[T] {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if op.done.Load() {
		// 操作已完成，立即执行回调
		go op.safeExecuteCallback(callback, op.value, op.err)
		return op
	}

	op.callbacks = append(op.callbacks, callback)
	return op
}

// safeExecuteCallback 安全执行回调（P0: 统一 panic recovery）
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
		if submitErr := op.goroutineProvider.Submit(context.Background(), func(ctx context.Context) {
			executor()
		}); submitErr != nil {
			slog.Error("[AsyncOperation] failed to submit callback", "error", submitErr)
		}
	} else {
		go executor()
	}
}

// WithTimeout 设置超时（P2-2: 链式超时设置）
// 返回一个新的 AsyncOperation，在指定超时内等待结果
func (op *asyncOpImpl[T]) WithTimeout(timeout time.Duration) AsyncOperation[T] {
	// 创建包装操作，在超时 context 下执行
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
	return op.status.Load() == 1
}

// IsFailed 检查是否失败
func (op *asyncOpImpl[T]) IsFailed() bool {
	return op.status.Load() == 2
}

// IsCanceled 检查是否取消
func (op *asyncOpImpl[T]) IsCanceled() bool {
	return op.status.Load() == 3
}

// ==========================================
// timeoutAsyncOp 超时包装器（P2-2）
// ==========================================

// timeoutAsyncOp 为 AsyncOperation 添加超时功能
type timeoutAsyncOp[T any] struct {
	inner   AsyncOperation[T]
	timeout time.Duration
}

// Await 带超时的阻塞等待
func (op *timeoutAsyncOp[T]) Await(ctx context.Context) (T, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, op.timeout)
	defer cancel()
	return op.inner.Await(timeoutCtx)
}

// OnComplete 委托给内部操作
func (op *timeoutAsyncOp[T]) OnComplete(callback func(T, error)) AsyncOperation[T] {
	op.inner.OnComplete(callback)
	return op
}

// OnError 委托给内部操作
func (op *timeoutAsyncOp[T]) OnError(callback func(error)) AsyncOperation[T] {
	op.inner.OnError(callback)
	return op
}

// OnSuccess 委托给内部操作
func (op *timeoutAsyncOp[T]) OnSuccess(callback func(T)) AsyncOperation[T] {
	op.inner.OnSuccess(callback)
	return op
}

// WithTimeout 支持链式设置超时（P2-1: 返回新实例，保持不可变）
func (op *timeoutAsyncOp[T]) WithTimeout(timeout time.Duration) AsyncOperation[T] {
	// 取最小值
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

// complete 完成操作（成功）
func (op *asyncOpImpl[T]) complete(v T) {
	if op.done.CompareAndSwap(false, true) {
		op.value = v
		op.status.Store(1)
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
		op.status.Store(2)
		select {
		case op.errCh <- err:
		default:
		}
		op.executeCallbacks(zero, err)
	}
}

// executeCallbacks 执行回调（P1-1: 使用 GoroutineProvider）
func (op *asyncOpImpl[T]) executeCallbacks(v T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), len(op.callbacks))
	copy(callbacks, op.callbacks)
	op.cbMu.RUnlock()

	for _, cb := range callbacks {
		op.safeExecuteCallback(cb, v, err)
	}
}

// ==========================================
// NewAsyncCall 实现
// ==========================================

// asyncCall 单播异步调用
type asyncCall struct {
	*asyncOpImpl[ResponseMsg]
}

// NewAsyncCall 创建异步单播调用
func NewAsyncCall(
	ctx context.Context,
	rpc RPCSync,
	to model.PeerID,
	req model.Message,
	timeoutMs int64,
	provider GoroutineProvider,
) AsyncOperation[ResponseMsg] {
	op := newAsyncOp[ResponseMsg](provider)

	// P0-2: 输入验证
	if timeoutMs <= 0 {
		op.fail(fmt.Errorf("%w: timeoutMs must be positive, got %d", ErrInvalidTimeout, timeoutMs))
		return &asyncCall{asyncOpImpl: op}
	}

	// P1-2: 使用 GoroutineProvider 提交任务
	if provider != nil {
		// P0-1: 处理 Submit 错误
		if err := provider.Submit(ctx, func(ctx context.Context) {
			// 创建带超时的 context
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()

			resp, err := rpc.Call(callCtx, to, req)
			if err != nil {
				op.fail(err)
				return
			}
			op.complete(ResponseMsg{Msg: resp, Err: nil})
		}); err != nil {
			op.fail(fmt.Errorf("submit task failed: %w", err))
		}
	} else {
		// 回退：直接创建 goroutine
		go func() {
			// 创建带超时的 context
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()

			resp, err := rpc.Call(callCtx, to, req)
			if err != nil {
				op.fail(err)
				return
			}
			op.complete(ResponseMsg{Msg: resp, Err: nil})
		}()
	}

	return &asyncCall{asyncOpImpl: op}
}

// ==========================================
// NewAsyncBroadcast 实现
// ==========================================

// asyncBroadcast 广播异步调用
type asyncBroadcast struct {
	*asyncOpImpl[AsyncBroadcastResult]
}

// NewAsyncBroadcast 创建异步广播调用
func NewAsyncBroadcast(
	ctx context.Context,
	rpc RPCSync,
	peers []model.PeerID,
	req model.Message,
	config *RPCAsyncConfig,
	callback BroadcastListener,
	provider GoroutineProvider,
) AsyncOperation[AsyncBroadcastResult] {
	op := newAsyncOp[AsyncBroadcastResult](provider)

	// P1: 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncBroadcast{asyncOpImpl: op}
	}

	// P1-2: 空切片检查
	if len(peers) == 0 {
		op.fail(ErrEmptyPeers)
		return &asyncBroadcast{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := NewBroadcastProgress("async-broadcast", peers)
		if callback != nil {
			tracker.SetCallback(callback)
		}

		result, err := rpc.BroadcastCall(callCtx, peers, req, ResponseAll, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		asyncResult := AsyncBroadcastResult{
			Total:        len(peers),
			SuccessCount: len(result.SuccessPeers),
		}

		for i, peer := range result.SuccessPeers {
			if i < len(result.Responses) {
				asyncResult.Responses = append(asyncResult.Responses, PeerResponse{
					Peer:     peer,
					Response: result.Responses[i],
				})
			}
		}

		for _, peer := range result.FailedPeers {
			asyncResult.Errors = append(asyncResult.Errors, PeerError{
				Peer:  peer,
				Error: ErrPeerUnreachable,
			})
		}

		op.complete(asyncResult)
	}

	// P1: 使用统一提交函数
	submitTask(ctx, provider, task, op.fail)

	return &asyncBroadcast{asyncOpImpl: op}
}

// ==========================================
// NewAsyncQuorum 实现
// ==========================================

// asyncQuorum Quorum 异步调用
type asyncQuorum struct {
	*asyncOpImpl[QuorumResult]
}

// NewAsyncQuorum 创建异步 Quorum 调用
func NewAsyncQuorum(
	ctx context.Context,
	rpc RPCSync,
	peers []model.PeerID,
	req model.Message,
	quorum int,
	config *RPCAsyncConfig,
	callback BroadcastListener,
	provider GoroutineProvider,
) AsyncOperation[QuorumResult] {
	op := newAsyncOp[QuorumResult](provider)

	// P1: 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncQuorum{asyncOpImpl: op}
	}

	// P1-2: 空切片检查
	if len(peers) == 0 {
		op.fail(ErrEmptyPeers)
		return &asyncQuorum{asyncOpImpl: op}
	}

	// P0-2: 输入验证
	if quorum <= 0 || quorum > len(peers) {
		op.fail(fmt.Errorf("%w: quorum must be between 1 and %d, got %d", ErrInvalidQuorum, len(peers), quorum))
		return &asyncQuorum{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := NewBroadcastProgress("async-quorum", peers)
		if callback != nil {
			tracker.SetCallback(callback)
		}

		result, err := rpc.BroadcastCall(callCtx, peers, req, ResponseMajority, tracker)
		if err != nil {
			op.complete(QuorumResult{
				Responses: convertToPeerResponses(result.SuccessPeers, result.Responses),
				Quorum:    quorum,
				Reached:   false,
			})
			return
		}

		op.complete(QuorumResult{
			Responses: convertToPeerResponses(result.SuccessPeers, result.Responses),
			Quorum:    quorum,
			Reached:   len(result.SuccessPeers) >= quorum,
		})
	}

	// P1: 使用统一提交函数
	submitTask(ctx, provider, task, op.fail)

	return &asyncQuorum{asyncOpImpl: op}
}

// convertToPeerResponses 转换响应格式
func convertToPeerResponses(peers []model.PeerID, responses []model.Message) []PeerResponse {
	result := make([]PeerResponse, 0, len(peers))
	for i, peer := range peers {
		if i < len(responses) && responses[i] != nil {
			result = append(result, PeerResponse{
				Peer:     peer,
				Response: responses[i],
			})
		}
	}
	return result
}

// ==========================================
// NewAsyncWriteV 实现
// ==========================================

// asyncWriteV 批量写入异步调用（单向）
type asyncWriteV struct {
	*asyncOpImpl[WriteVResult]
}

// NewAsyncWriteV 创建异步批量写入（单向）
func NewAsyncWriteV(
	ctx context.Context,
	rpc RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *RPCAsyncConfig,
	callback BroadcastListener,
	provider GoroutineProvider,
) AsyncOperation[WriteVResult] {
	op := newAsyncOp[WriteVResult](provider)

	// P1: 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncWriteV{asyncOpImpl: op}
	}

	// P0-2: 输入验证
	if len(targets) != len(msgs) {
		op.fail(fmt.Errorf("%w: targets (%d) vs msgs (%d)", ErrTargetsMsgsMismatch, len(targets), len(msgs)))
		return &asyncWriteV{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := NewBroadcastProgress("async-writev", targets)
		if callback != nil {
			tracker.SetCallback(callback)
		}

		err := rpc.WriteV(callCtx, targets, msgs, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		op.complete(WriteVResult{
			SuccessPeers: targets,
			FailedPeers:  nil,
			Responses:    nil,
		})
	}

	// P1: 使用统一提交函数
	submitTask(ctx, provider, task, op.fail)

	return &asyncWriteV{asyncOpImpl: op}
}

// ==========================================
// NewAsyncWriteVCall 实现
// ==========================================

// asyncWriteVCall 批量写入异步调用（带响应）
type asyncWriteVCall struct {
	*asyncOpImpl[WriteVResult]
}

// NewAsyncWriteVCall 创建异步批量写入（带响应）
func NewAsyncWriteVCall(
	ctx context.Context,
	rpc RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *RPCAsyncConfig,
	callback BroadcastListener,
	provider GoroutineProvider,
) AsyncOperation[WriteVResult] {
	op := newAsyncOp[WriteVResult](provider)

	// P1: 使用统一验证函数
	if err := validateRPCAndConfig(rpc, config); err != nil {
		op.fail(err)
		return &asyncWriteVCall{asyncOpImpl: op}
	}

	// P0-2: 输入验证
	if len(targets) != len(msgs) {
		op.fail(fmt.Errorf("%w: targets (%d) vs msgs (%d)", ErrTargetsMsgsMismatch, len(targets), len(msgs)))
		return &asyncWriteVCall{asyncOpImpl: op}
	}

	task := func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
		defer cancel()

		tracker := NewBroadcastProgress("async-writevcall", targets)
		if callback != nil {
			tracker.SetCallback(callback)
		}

		result, err := rpc.WriteVCall(callCtx, targets, msgs, ResponseAll, tracker)
		if err != nil {
			op.fail(err)
			return
		}

		op.complete(result)
	}

	// P1: 使用统一提交函数
	submitTask(ctx, provider, task, op.fail)

	return &asyncWriteVCall{asyncOpImpl: op}
}
