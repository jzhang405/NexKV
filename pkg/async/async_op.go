// Package async 提供统一的异步操作抽象
package async

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// ==========================================
// AsyncOperation[T] 接口定义
// ==========================================

// AsyncOperation 统一异步操作接口
type AsyncOperation[T any] interface {
	// Get 阻塞等待操作完成并返回结果
	Get(ctx context.Context) (T, error)

	// Status 返回操作当前状态
	Status() OperationStatus

	// Cancel 尝试取消操作
	// 返回值：
	//   - canceled: 是否成功取消
	//   - err: 如果操作已处于终态，返回错误
	Cancel() (canceled bool, err error)

	// Discard 丢弃操作结果（不等待完成）
	// 用于释放资源，操作会继续执行但结果会被丢弃
	Discard() error

	// IsStarted 检查操作是否已启动
	IsStarted() bool

	// OnComplete 注册完成回调
	// 返回回调 ID，用于后续注销
	OnComplete(callback func(T, error)) string

	// OffComplete 注销完成回调
	OffComplete(cbID string) error
}

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
// Result[T] 结果包装器
// ==========================================

// Result 结果包装器
type Result[T any] struct {
	Value T
	Err   error
}

// ==========================================
// 选项模式定义
// ==========================================

// OpOption 异步操作选项
type OpOption func(*opConfig)

// opConfig 操作配置
type opConfig struct {
	timeout           time.Duration
	goroutineProvider service.GoroutineProvider
	lifecycle         *transport.AsyncLifecycle
}

// WithTimeout 设置超时时间
func WithTimeout(timeout time.Duration) OpOption {
	return func(c *opConfig) {
		c.timeout = timeout
	}
}

// WithGoroutineProvider 设置协程池提供者
func WithGoroutineProvider(provider service.GoroutineProvider) OpOption {
	return func(c *opConfig) {
		c.goroutineProvider = provider
	}
}

// WithLifecycle 设置生命周期管理器
func WithLifecycle(lifecycle *transport.AsyncLifecycle) OpOption {
	return func(c *opConfig) {
		c.lifecycle = lifecycle
	}
}

// ==========================================
// AsyncOp[T] 实现
// ==========================================

// AsyncOp 异步操作实现
type AsyncOp[T any] struct {
	lifecycle *transport.AsyncLifecycle
	resultCh  chan Result[T]
	done      chan struct{}
	value     T
	err       error
	callbacks map[string]func(T, error)
	cbMu      sync.RWMutex
	cbSeq     int64
	execFunc  func(ctx context.Context) (T, error)
	status    OperationStatus
	statusMu  sync.RWMutex
	started   bool
	cancel    context.CancelFunc
	discarded bool
}

// NewOp 创建异步操作
func NewOp[T any](
	ctx context.Context,
	execFunc func(ctx context.Context) (T, error),
	opts ...OpOption,
) AsyncOperation[T] {
	// 应用选项
	config := &opConfig{
		timeout:   30 * time.Second,
		lifecycle: transport.NewAsyncLifecycle(),
	}
	for _, opt := range opts {
		opt(config)
	}

	// 创建带超时的 context
	var cancel context.CancelFunc
	if config.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, config.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	// 使用提供的 lifecycle 或创建新的
	lifecycle := config.lifecycle
	if lifecycle == nil {
		lifecycle = transport.NewAsyncLifecycle()
	}

	op := &AsyncOp[T]{
		lifecycle: lifecycle,
		resultCh:  make(chan Result[T], 1),
		done:      make(chan struct{}),
		callbacks: make(map[string]func(T, error)),
		execFunc:  execFunc,
		status:    StatusPending,
		cancel:    cancel,
	}

	// 执行任务
	if config.goroutineProvider != nil {
		// 使用 GoroutineProvider
		_ = config.goroutineProvider.Submit(ctx, func(innerCtx context.Context) {
			op.execute(innerCtx, ctx)
		})
	} else {
		// 直接启动 goroutine
		lifecycle.Go(func() {
			op.execute(lifecycle.Context(), ctx)
		})
	}

	return op
}

// execute 执行异步操作
func (op *AsyncOp[T]) execute(lifecycleCtx, timeoutCtx context.Context) {
	defer close(op.done)

	// 更新状态为运行中
	op.statusMu.Lock()
	op.status = StatusRunning
	op.started = true
	op.statusMu.Unlock()

	// 检查是否已丢弃
	if op.discarded {
		op.statusMu.Lock()
		op.status = StatusDiscarded
		op.statusMu.Unlock()
		return
	}

	// 执行任务
	value, err := op.execFunc(lifecycleCtx)

	// 更新最终状态
	op.statusMu.Lock()
	if timeoutCtx.Err() == context.DeadlineExceeded {
		op.status = StatusTimeout
		op.err = timeoutCtx.Err()
	} else if lifecycleCtx.Err() == context.Canceled {
		op.status = StatusCanceled
		op.err = lifecycleCtx.Err()
	} else if err != nil {
		op.status = StatusFailed
		op.err = err
		op.value = value
	} else {
		op.status = StatusCompleted
		op.value = value
	}
	op.statusMu.Unlock()

	// 发送结果（非阻塞）
	select {
	case op.resultCh <- Result[T]{Value: op.value, Err: op.err}:
	default:
	}

	// 执行回调
	op.executeCallbacks(op.value, op.err)
}

// Get 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Get(ctx context.Context) (T, error) {
	select {
	case result := <-op.resultCh:
		return result.Value, result.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-op.done:
		// 操作已完成，返回存储的结果
		return op.value, op.err
	}
}

// Status 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Status() OperationStatus {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.status
}

// Cancel 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Cancel() (bool, error) {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return false, fmt.Errorf("operation already in terminal state: %v", op.status)
	}

	op.status = StatusCanceled
	if op.cancel != nil {
		op.cancel()
	}
	if op.lifecycle != nil {
		op.lifecycle.Cancel()
	}
	return true, nil
}

// Discard 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Discard() error {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return fmt.Errorf("cannot discard operation in terminal state: %v", op.status)
	}

	op.discarded = true
	op.status = StatusDiscarded
	if op.cancel != nil {
		op.cancel()
	}
	if op.lifecycle != nil {
		op.lifecycle.Cancel()
	}
	return nil
}

// IsStarted 实现 AsyncOperation 接口
func (op *AsyncOp[T]) IsStarted() bool {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.started
}

// OnComplete 实现 AsyncOperation 接口
func (op *AsyncOp[T]) OnComplete(callback func(T, error)) string {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	op.cbSeq++
	cbID := fmt.Sprintf("cb-%d", op.cbSeq)

	// 如果操作已完成，立即执行回调
	select {
	case <-op.done:
		go safeCallback(callback, op.value, op.err)
	default:
		op.callbacks[cbID] = callback
	}

	return cbID
}

// OffComplete 实现 AsyncOperation 接口
func (op *AsyncOp[T]) OffComplete(cbID string) error {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if _, exists := op.callbacks[cbID]; !exists {
		return fmt.Errorf("callback not found: %s", cbID)
	}

	delete(op.callbacks, cbID)
	return nil
}

// ResultChan 返回结果通道（扩展方法）
func (op *AsyncOp[T]) ResultChan() <-chan Result[T] {
	return op.resultCh
}

// executeCallbacks 执行所有回调
func (op *AsyncOp[T]) executeCallbacks(value T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), 0, len(op.callbacks))
	for _, cb := range op.callbacks {
		callbacks = append(callbacks, cb)
	}
	op.cbMu.RUnlock()

	for _, cb := range callbacks {
		cb := cb
		go safeCallback(cb, value, err)
	}
}

// safeCallback 安全执行回调（带 panic 恢复）
func safeCallback[T any](callback func(T, error), value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			// 记录 panic 但不影响主流程
			// TODO: 集成日志系统后记录详细日志
			fmt.Printf("[async] callback panic recovered: %v\n", r)
		}
	}()
	callback(value, err)
}
