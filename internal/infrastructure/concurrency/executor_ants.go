// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"

	"github.com/panjf2000/ants/v2"
)

// ==========================================
// AntsDefaultExecutor - ants 默认池执行器
// ==========================================

// AntsDefaultExecutor 默认池执行器
// 封装 ants 默认池，适用于通用任务
type AntsDefaultExecutor struct {
	mu     sync.RWMutex
	closed bool
}

// NewAntsDefaultExecutor 创建默认池执行器
func NewAntsDefaultExecutor() *AntsDefaultExecutor {
	return &AntsDefaultExecutor{}
}

// Submit 提交任务
func (e *AntsDefaultExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()

	if closed {
		return ErrExecutorClosed
	}

	return ants.Submit(func() {
		task(ctx)
	})
}

// Close 关闭执行器
func (e *AntsDefaultExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}
