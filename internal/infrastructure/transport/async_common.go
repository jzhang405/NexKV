// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sourcegraph/conc"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// transportLog 使用 logrus 结构化日志
var transportLog = logrus.WithField("component", "transport")

// 异步操作常量
const (
	// DefaultAsyncBufferSize 默认异步缓冲区大小
	DefaultAsyncBufferSize = 256
	// MinAsyncBufferSize 最小异步缓冲区大小
	MinAsyncBufferSize = 16
	// MaxAsyncBufferSize 最大异步缓冲区大小
	MaxAsyncBufferSize = 1024
	// DefaultTimeout 默认超时时间
	DefaultTimeout = 30 * time.Second
	// DefaultMaxMessageSize 默认最大消息大小 (10MB)
	DefaultMaxMessageSize = 10 * 1024 * 1024
	// CloseTimeout 关闭超时
	CloseTimeout = 5 * time.Second
	// CloseFinalTimeout 最终关闭超时
	CloseFinalTimeout = 1 * time.Second
)

// ValidateBufferSize 验证缓冲区大小
func ValidateBufferSize(size int) int {
	if size < MinAsyncBufferSize {
		return DefaultAsyncBufferSize
	}
	if size > MaxAsyncBufferSize {
		return MaxAsyncBufferSize
	}
	return size
}

// ValidateTimeout 验证超时时间
func ValidateTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

// ValidateMaxMessageSize 验证最大消息大小
func ValidateMaxMessageSize(maxSize int) int {
	if maxSize <= 0 {
		return DefaultMaxMessageSize
	}
	return maxSize
}

// AtomicError atomic 错误存储辅助类型（P0 修复）
type AtomicError struct {
	ptr atomic.Pointer[error]
}

// Store 存储错误
func (e *AtomicError) Store(err *error) {
	e.ptr.Store(err)
}

// Load 加载错误
func (e *AtomicError) Load() error {
	if p := e.ptr.Load(); p != nil {
		return *p
	}
	return nil
}

// AsyncLifecycle 异步组件生命周期管理（基于 conc 库）
type AsyncLifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     conc.WaitGroup
	closed atomic.Bool
}

// NewAsyncLifecycle 创建新的生命周期管理器
func NewAsyncLifecycle() *AsyncLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &AsyncLifecycle{
		ctx:    ctx,
		cancel: cancel,
	}
}

// Context 返回上下文
func (l *AsyncLifecycle) Context() context.Context {
	return l.ctx
}

// Done 返回上下文 Done channel
func (l *AsyncLifecycle) Done() <-chan struct{} {
	return l.ctx.Done()
}

// Go 启动 goroutine（使用 conc wait.Group）
func (l *AsyncLifecycle) Go(f func()) {
	l.wg.Go(f)
}

// Close 关闭（返回是否首次关闭）
func (l *AsyncLifecycle) Close() bool {
	return l.closed.CompareAndSwap(false, true)
}

// IsClosed 检查是否已关闭
func (l *AsyncLifecycle) IsClosed() bool {
	return l.closed.Load()
}

// Cancel 取消上下文
func (l *AsyncLifecycle) Cancel() {
	l.cancel()
}

// WaitWithTimeout 等待 goroutine 退出（带超时）
// 返回 true 表示正常退出，false 表示超时
func (l *AsyncLifecycle) WaitWithTimeout(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// CloseAsync 执行异步关闭流程（P0/P1 优化版）
// 返回错误如果有 goroutine 泄漏
func CloseAsync(
	lifecycle *AsyncLifecycle,
	closeChFunc func(),
	closeStreamFunc func() error,
	componentName string,
) error {
	if !lifecycle.Close() {
		return nil
	}

	// 1. 取消上下文
	lifecycle.Cancel()

	// 2. 关闭 channel
	if closeChFunc != nil {
		closeChFunc()
	}

	// 3. 等待 goroutine 退出
	if lifecycle.WaitWithTimeout(CloseTimeout) {
		// 正常退出，关闭底层流
		return closeStreamFunc()
	}

	// 超时，记录警告
	transportLog.WithFields(logrus.Fields{
		"async_component": componentName,
		"timeout":         CloseTimeout,
	}).Warn("close timeout, forcing stream close")

	// 4. 强制关闭底层流
	streamErr := closeStreamFunc()

	// 5. 再次等待 goroutine
	if lifecycle.WaitWithTimeout(CloseFinalTimeout) {
		// 最终正常退出
		return streamErr
	}

	// P0 修复：返回明确的错误
	transportLog.WithFields(logrus.Fields{
		"async_component": componentName,
	}).Error("goroutine leak detected after stream close")

	if streamErr != nil {
		return streamErr
	}
	return errors.Wrap(errors.ErrAsyncExecFailed, "goroutine leak detected")
}

// NonBlockingSendError 非阻塞发送错误（P1 修复）
func NonBlockingSendError(errCh chan error, err error, componentName string) {
	if errCh == nil {
		return
	}
	select {
	case errCh <- err:
		// 成功发送
	default:
		// Channel 满或已关闭，记录警告
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
			"error":           err,
		}).Warn("error channel full or closed, dropping error")
	}
}

// NonBlockingSendResult 非阻塞发送读取结果（P1 修复）
func NonBlockingSendResult[T any](ch chan T, result T, componentName string, ctxDone <-chan struct{}) bool {
	select {
	case ch <- result:
		return true
	case <-ctxDone:
		return false
	default:
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
		}).Warn("result channel blocked, dropping result")
		return false
	}
}

// ValidateMessageSize 验证消息大小（P1 修复：提取公共逻辑）
func ValidateMessageSize(data []byte, maxSize int, componentName string) error {
	if maxSize > 0 && len(data) > maxSize {
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
			"msg_size":        len(data),
			"max_size":        maxSize,
		}).Warn("message size exceeds limit")
		return errors.ErrMessageTooLarge
	}
	return nil
}

// DrainWriteQueue 清空写入队列（P1 修复：检查 channel 关闭）
func DrainWriteQueue(ch <-chan service.WriteRequest, callback func(service.WriteRequest)) int {
	count := 0
	for {
		select {
		case req, ok := <-ch:
			if !ok {
				// Channel 已关闭
				return count
			}
			callback(req)
			count++
		default:
			// Channel 为空
			return count
		}
	}
}
