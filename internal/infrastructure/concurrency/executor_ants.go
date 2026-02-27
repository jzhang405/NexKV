// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"

	"github.com/panjf2000/ants/v2"
)

// ==========================================
// AntsDefaultExecutor - Mode 2: 默认池执行器
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

// ==========================================
// AntsPoolExecutor - Mode 3: 自定义池执行器
// ==========================================

// AntsPoolExecutor 自定义池执行器
// 封装 ants 自定义池，适用于需要独立配置的场景
type AntsPoolExecutor struct {
	pool   *ants.Pool
	mu     sync.RWMutex
	closed bool
}

// NewAntsPoolExecutor 创建自定义池执行器
func NewAntsPoolExecutor(capacity int) (*AntsPoolExecutor, error) {
	pool, err := ants.NewPool(capacity)
	if err != nil {
		return nil, err
	}

	return &AntsPoolExecutor{pool: pool}, nil
}

// Submit 提交任务
func (e *AntsPoolExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()

	if closed {
		return ErrExecutorClosed
	}

	return e.pool.Submit(func() {
		task(ctx)
	})
}

// Close 关闭执行器
func (e *AntsPoolExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	e.pool.Release()
	return nil
}

// ==========================================
// AntsFuncExecutor - Mode 4: 函数池执行器
// ==========================================

// AntsFuncExecutor 函数池执行器
// 封装 ants.PoolWithFunc，适用于高频重复任务
type AntsFuncExecutor struct {
	pool   *ants.PoolWithFunc
	mu     sync.RWMutex
	closed bool
}

// NewAntsFuncExecutor 创建函数池执行器
func NewAntsFuncExecutor(capacity int, handler func(interface{})) (*AntsFuncExecutor, error) {
	pool, err := ants.NewPoolWithFunc(capacity, handler)
	if err != nil {
		return nil, err
	}

	return &AntsFuncExecutor{pool: pool}, nil
}

// Invoke 调用函数
func (e *AntsFuncExecutor) Invoke(ctx context.Context, arg interface{}) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()

	if closed {
		return ErrExecutorClosed
	}

	return e.pool.Invoke(arg)
}

// Submit 提交任务（兼容 TaskExecutor 接口）
func (e *AntsFuncExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()

	if closed {
		return ErrExecutorClosed
	}

	// 将任务包装为可调用的形式
	return e.pool.Invoke(func() {
		task(ctx)
	})
}

// Close 关闭执行器
func (e *AntsFuncExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	e.pool.Release()
	return nil
}

// ==========================================
// AntsMultiExecutor - Mode 5: 多池执行器
// ==========================================

// AntsMultiExecutor 多池执行器
// 封装 ants.MultiPool，适用于分片场景
type AntsMultiExecutor struct {
	multiPool *ants.MultiPool
	mu        sync.RWMutex
	closed    bool
}

// NewAntsMultiExecutor 创建多池执行器
func NewAntsMultiExecutor(numPools int, poolSize int) (*AntsMultiExecutor, error) {
	multiPool, err := ants.NewMultiPool(numPools, poolSize, ants.LeastTasks)
	if err != nil {
		return nil, err
	}

	return &AntsMultiExecutor{multiPool: multiPool}, nil
}

// Submit 提交任务
func (e *AntsMultiExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()

	if closed {
		return ErrExecutorClosed
	}

	return e.multiPool.Submit(func() {
		task(ctx)
	})
}

// Close 关闭执行器
func (e *AntsMultiExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	e.multiPool.ReleaseTimeout(0)
	return nil
}
