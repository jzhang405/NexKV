// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// Result 接口定义
// ==========================================

// Result[T] 异步执行结果接口（类型别名）
// 实际接口定义在 domain/service/concurrency.go
type Result[T any] = GoroutineResult[T]

// ==========================================
// AnyResult any 类型的结果实现
// ==========================================

// AnyResult any 类型的结果实现
type AnyResult struct {
	value any
	err   error
	done  chan struct{}
	once  sync.Once
	mu    sync.RWMutex
}

// NewAnyResult 创建新的 AnyResult
func NewAnyResult() *AnyResult {
	return &AnyResult{
		done: make(chan struct{}),
	}
}

// Get 实现 Result 接口
func (r *AnyResult) Get(ctx context.Context) (any, error) {
	select {
	case <-r.done:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.value, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetWithTimeout 带超时等待结果
func (r *AnyResult) GetWithTimeout(timeout time.Duration) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.Get(ctx)
}

// Done 返回完成通道
func (r *AnyResult) Done() <-chan struct{} {
	return r.done
}

// IsDone 检查是否完成
func (r *AnyResult) IsDone() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// SetValue 设置值（内部方法）
// 注意：使用 sync.Once 确保只设置一次，后续调用会被忽略
func (r *AnyResult) SetValue(val any) {
	r.once.Do(func() {
		r.mu.Lock()
		r.value = val
		r.mu.Unlock()
		close(r.done)
	})
}

// SetError 设置错误（内部方法）
// 注意：使用 sync.Once 确保只设置一次，后续调用会被忽略
func (r *AnyResult) SetError(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
	})
}

// ==========================================
// TypedResult 类型安全结果包装器
// ==========================================

// TypedResult 类型安全的结果包装器
type TypedResult[T any] struct {
	inner *AnyResult
}

// Get 阻塞等待结果
func (r *TypedResult[T]) Get(ctx context.Context) (T, error) {
	var zero T
	anyVal, err := r.inner.Get(ctx)
	if err != nil {
		return zero, err
	}
	// 安全类型断言，避免 panic
	val, ok := anyVal.(T)
	if !ok {
		return zero, errors.Wrapf(errors.ErrAsyncExecFailed, "type assertion failed: expected %T, got %T", zero, anyVal)
	}
	return val, nil
}

// GetWithTimeout 带超时等待结果
func (r *TypedResult[T]) GetWithTimeout(timeout time.Duration) (T, error) {
	var zero T
	anyVal, err := r.inner.GetWithTimeout(timeout)
	if err != nil {
		return zero, err
	}
	// 安全类型断言，避免 panic
	val, ok := anyVal.(T)
	if !ok {
		return zero, errors.Wrapf(errors.ErrAsyncExecFailed, "type assertion failed: expected %T, got %T", zero, anyVal)
	}
	return val, nil
}

// Done 返回完成通道
func (r *TypedResult[T]) Done() <-chan struct{} {
	return r.inner.Done()
}

// IsDone 检查是否完成
func (r *TypedResult[T]) IsDone() bool {
	return r.inner.IsDone()
}
