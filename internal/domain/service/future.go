// Package service 提供领域服务接口和类型
package service

import (
	"context"
)

// Future[T] 表示一个异步计算的结果
// 类似 Promise 模式，用于异步任务的结果获取
type Future[T any] struct {
	result T
	err    error
	done   chan struct{}
}

// NewFuture 创建一个未完成的 Future
func NewFuture[T any]() *Future[T] {
	return &Future[T]{
		done: make(chan struct{}),
	}
}

// Resolve 设置成功结果
func (f *Future[T]) Resolve(result T) {
	f.result = result
	close(f.done)
}

// Reject 设置错误结果
func (f *Future[T]) Reject(err error) {
	f.err = err
	close(f.done)
}

// Get 阻塞等待结果
// 支持 context 取消
func (f *Future[T]) Get(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		return f.result, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// IsDone 检查是否已完成
func (f *Future[T]) IsDone() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// SubmitWithResult 提交带返回值的任务（泛型辅助函数）
// 返回 *Future[T]，通过 future.Get(ctx) 获取结果
func SubmitWithResult[T any](
	executor TaskExecutor,
	ctx context.Context,
	priority TaskPriority,
	task func(context.Context) (T, error),
) *Future[T] {
	future := NewFuture[T]()

	_ = executor.Submit(ctx, priority, func(execCtx context.Context) {
		result, err := task(execCtx)
		if err != nil {
			future.Reject(err)
		} else {
			future.Resolve(result)
		}
	})

	return future
}
