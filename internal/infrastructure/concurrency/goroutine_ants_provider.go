// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
)

// ==========================================
// 常量定义
// ==========================================

const (
	// MinPoolCapacity 最小池容量
	MinPoolCapacity = 1
	// MaxPoolCapacity 最大池容量
	MaxPoolCapacity = 100000
	// DefaultMaxDelayedTasks 默认最大延迟任务数（P1-01: 速率限制）
	DefaultMaxDelayedTasks = 10000
)

// ==========================================
// AntsGoroutineProvider 实现
// ==========================================

// AntsGoroutineProvider 基于 ants 库的协程池实现
type AntsGoroutineProvider struct {
	pool       *ants.Pool
	config     *ProviderConfig
	stats      PoolStats
	statsMu    sync.RWMutex
	taskCount  atomic.Int64
	closed     atomic.Bool
	stopCh     chan struct{}  // 全局停止信号
	delayedWg  sync.WaitGroup // 跟踪延迟任务
	delayedSem chan struct{}  // P1-01: 延迟任务信号量（速率限制）
}

// ProviderConfig 协程池配置
type ProviderConfig struct {
	// Capacity 协程池容量
	Capacity int
	// EnablePriority 是否启用优先级
	EnablePriority bool
	// EnableDelayed 是否启用延迟任务
	EnableDelayed bool
}

// DefaultProviderConfig 默认配置
func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{
		Capacity:       100,
		EnablePriority: true,
		EnableDelayed:  true,
	}
}

// NewAntsGoroutineProvider 创建 ants 协程池提供者
func NewAntsGoroutineProvider(config *ProviderConfig) (*AntsGoroutineProvider, error) {
	if config == nil {
		config = DefaultProviderConfig()
	}

	// P0-04: 容量验证
	if config.Capacity < MinPoolCapacity || config.Capacity > MaxPoolCapacity {
		return nil, fmt.Errorf("invalid pool capacity: %d (must be between %d and %d)",
			config.Capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	pool, err := ants.NewPool(config.Capacity, ants.WithPreAlloc(true))
	if err != nil {
		return nil, err
	}

	return &AntsGoroutineProvider{
		pool:   pool,
		config: config,
		stats: PoolStats{
			Capacity:   config.Capacity,
			ByPriority: make(map[Priority]int),
		},
		stopCh:     make(chan struct{}),
		delayedSem: make(chan struct{}, DefaultMaxDelayedTasks), // P1-01: 速率限制
	}, nil
}

// ======================================
// P0-01: Panic 恢复包装器
// ======================================

// safeExecute 安全执行任务，捕获 panic
// P1-02: 添加日志记录
func (p *AntsGoroutineProvider) safeExecute(task func()) {
	defer func() {
		if r := recover(); r != nil {
			// P1-02: 使用 panicRecoveryHandler 处理 panic
			p.handlePanic(r)
		}
	}()
	task()
}

// handlePanic 处理 panic 恢复
func (p *AntsGoroutineProvider) handlePanic(r any) {
	stack := debug.Stack()
	logrus.WithFields(logrus.Fields{
		"panic": r,
		"stack": string(stack),
	}).Error("panic recovered in goroutine task")
}

// ======================================
// P0-02: 延迟任务调度（修复 goroutine 泄漏）
// ======================================

// scheduleDelayedTask 调度延迟任务（统一处理，避免泄漏）
// P1-01: 添加速率限制
func (p *AntsGoroutineProvider) scheduleDelayedTask(
	ctx context.Context,
	delay time.Duration,
	execute func(),
) error {
	// P1-01: 速率限制 - 尝试获取信号量
	select {
	case p.delayedSem <- struct{}{}:
		// 成功获取
	default:
		// 延迟任务数已达上限
		return ErrTooManyDelayedTasks
	}

	p.delayedWg.Add(1)
	go func() {
		defer func() {
			<-p.delayedSem // P1-01: 释放信号量
			p.delayedWg.Done()
		}()

		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			if !p.isClosed() {
				execute()
			}
		case <-ctx.Done():
			// Context 取消，任务放弃执行
		case <-p.stopCh:
			// 池关闭信号，任务放弃执行
		}
	}()

	return nil
}

// ======================================
// 基础方法实现
// ======================================

// Submit 实现接口
func (p *AntsGoroutineProvider) Submit(ctx context.Context, task func(context.Context)) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// P0-01: 添加 panic 恢复
	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx)
		})
	})
}

// SubmitWithArg 实现接口
func (p *AntsGoroutineProvider) SubmitWithArg(
	ctx context.Context,
	task func(context.Context, any),
	arg any,
) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx, arg)
		})
	})
}

// SubmitWithResult 实现接口
func (p *AntsGoroutineProvider) SubmitWithResult(
	ctx context.Context,
	task func(context.Context) (any, error),
) Result[any] {
	result := NewAnyResult()

	if p.isClosed() {
		result.SetError(ErrPoolClosed)
		return result
	}

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx)
			if err != nil {
				result.SetError(err)
			} else {
				result.SetValue(val)
			}
		})
	}); err != nil {
		result.SetError(err)
	}

	return result
}

// SubmitWithArgAndResult 实现接口
func (p *AntsGoroutineProvider) SubmitWithArgAndResult(
	ctx context.Context,
	task func(context.Context, any) (any, error),
	arg any,
) Result[any] {
	result := NewAnyResult()

	if p.isClosed() {
		result.SetError(ErrPoolClosed)
		return result
	}

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx, arg)
			if err != nil {
				result.SetError(err)
			} else {
				result.SetValue(val)
			}
		})
	}); err != nil {
		result.SetError(err)
	}

	return result
}

// SubmitWithPriority 实现接口
func (p *AntsGoroutineProvider) SubmitWithPriority(
	ctx context.Context,
	priority Priority,
	task func(context.Context),
) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// ants 不支持原生优先级，这里通过统计记录
	p.statsMu.Lock()
	p.stats.ByPriority[priority]++
	p.statsMu.Unlock()

	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx)
		})
	})
}

// SubmitDelayed 实现接口
func (p *AntsGoroutineProvider) SubmitDelayed(
	ctx context.Context,
	delay time.Duration,
	task func(context.Context),
) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// P0-02: 使用统一的延迟任务调度，P1-01: 处理速率限制错误
	return p.scheduleDelayedTask(ctx, delay, func() {
		_ = p.pool.Submit(func() {
			p.safeExecute(func() {
				task(ctx)
			})
		})
	})
}

// ======================================
// 高级方法实现
// ======================================

// SubmitAdvanced 实现接口
func (p *AntsGoroutineProvider) SubmitAdvanced(
	ctx context.Context,
	task func(context.Context, any) (any, error),
	arg any,
	opts ...SubmitOption,
) Result[any] {
	result := NewAnyResult()

	if p.isClosed() {
		result.SetError(ErrPoolClosed)
		return result
	}

	config := applyOptions(opts)

	// P0-02: 处理延迟任务（使用统一的调度方法）
	if config.delay > 0 {
		// P1-01: 处理速率限制错误
		if err := p.scheduleDelayedTask(ctx, config.delay, func() {
			if !p.isClosed() {
				p.statsMu.Lock()
				p.stats.ByPriority[config.priority]++
				p.statsMu.Unlock()

				if err := p.pool.Submit(func() {
					p.safeExecute(func() {
						val, err := task(ctx, arg)
						if err != nil {
							result.SetError(err)
						} else {
							result.SetValue(val)
						}
					})
				}); err != nil {
					result.SetError(err)
				}
			}
		}); err != nil {
			result.SetError(err)
		}
		return result
	}

	// 记录优先级统计
	p.statsMu.Lock()
	p.stats.ByPriority[config.priority]++
	p.statsMu.Unlock()

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx, arg)
			if err != nil {
				result.SetError(err)
			} else {
				result.SetValue(val)
			}
		})
	}); err != nil {
		result.SetError(err)
	}

	return result
}

// ======================================
// 批量方法实现
// ======================================

// SubmitBatch 实现接口
func (p *AntsGoroutineProvider) SubmitBatch(ctx context.Context, tasks []func(context.Context)) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	for _, task := range tasks {
		task := task // 捕获循环变量
		if err := p.pool.Submit(func() {
			p.safeExecute(func() {
				task(ctx)
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

// SubmitBatchWithArg 实现接口
func (p *AntsGoroutineProvider) SubmitBatchWithArg(
	ctx context.Context,
	tasks []func(context.Context, any),
	args []any,
) error {
	if len(tasks) != len(args) {
		return ErrTaskArgLengthMismatch
	}

	if p.isClosed() {
		return ErrPoolClosed
	}

	for i, task := range tasks {
		task := task // 捕获循环变量
		arg := args[i]
		if err := p.pool.Submit(func() {
			p.safeExecute(func() {
				task(ctx, arg)
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

// SubmitBatchAllErrors 实现接口
// SubmitBatchAllErrors 实现接口
// P2-01: 优化锁竞争 - 使用预分配切片 + 原子索引
func (p *AntsGoroutineProvider) SubmitBatchAllErrors(
	ctx context.Context,
	tasks []func(context.Context),
) []error {
	if p.isClosed() {
		return []error{ErrPoolClosed}
	}

	// P2-01: 预分配错误切片，避免动态扩容
	errors := make([]error, len(tasks))
	var errorCount atomic.Int32

	var wg sync.WaitGroup

	for _, task := range tasks {
		task := task
		wg.Add(1)
		if err := p.pool.Submit(func() {
			defer wg.Done()
			p.safeExecute(func() {
				task(ctx)
			})
		}); err != nil {
			// P2-01: 使用原子索引，避免锁竞争
			idx := int(errorCount.Add(1)) - 1
			if idx < len(errors) {
				errors[idx] = err
			}
			wg.Done()
		}
	}

	wg.Wait()

	// P2-01: 返回实际错误数量的切片
	count := int(errorCount.Load())
	if count == 0 {
		return nil
	}
	return errors[:count]
}

// SubmitBatchWithResult 实现接口
func (p *AntsGoroutineProvider) SubmitBatchWithResult(
	ctx context.Context,
	tasks []func(context.Context) (any, error),
) []Result[any] {
	results := make([]Result[any], len(tasks))

	if p.isClosed() {
		for i := range results {
			r := NewAnyResult()
			r.SetError(ErrPoolClosed)
			results[i] = r
		}
		return results
	}

	for i, task := range tasks {
		task := task
		result := NewAnyResult()
		results[i] = result

		// P0-03: 检查 Submit 错误
		if err := p.pool.Submit(func() {
			p.safeExecute(func() {
				val, err := task(ctx)
				if err != nil {
					result.SetError(err)
				} else {
					result.SetValue(val)
				}
			})
		}); err != nil {
			result.SetError(err)
		}
	}

	return results
}

// ======================================
// 类型安全函数（供泛型辅助函数调用）
// ======================================

// SubmitWithArgTyped 类型安全的带参数函数
func SubmitWithArgTyped[T any](
	p *AntsGoroutineProvider,
	ctx context.Context,
	task func(context.Context, T),
	arg T,
) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx, arg)
		})
	})
}

// SubmitWithResultTyped 类型安全的带返回值函数
func SubmitWithResultTyped[T any](
	p *AntsGoroutineProvider,
	ctx context.Context,
	task func(context.Context) (T, error),
) *TypedResult[T] {
	result := &TypedResult[T]{inner: NewAnyResult()}

	if p.isClosed() {
		result.inner.SetError(ErrPoolClosed)
		return result
	}

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx)
			if err != nil {
				result.inner.SetError(err)
			} else {
				result.inner.SetValue(val)
			}
		})
	}); err != nil {
		result.inner.SetError(err)
	}

	return result
}

// SubmitWithArgAndResultTyped 类型安全的带参数和返回值函数
func SubmitWithArgAndResultTyped[T any, R any](
	p *AntsGoroutineProvider,
	ctx context.Context,
	task func(context.Context, T) (R, error),
	arg T,
) *TypedResult[R] {
	result := &TypedResult[R]{inner: NewAnyResult()}

	if p.isClosed() {
		result.inner.SetError(ErrPoolClosed)
		return result
	}

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx, arg)
			if err != nil {
				result.inner.SetError(err)
			} else {
				result.inner.SetValue(val)
			}
		})
	}); err != nil {
		result.inner.SetError(err)
	}

	return result
}

// SubmitAdvancedTyped 类型安全的高级函数
func SubmitAdvancedTyped[T any, R any](
	p *AntsGoroutineProvider,
	ctx context.Context,
	task func(context.Context, T) (R, error),
	arg T,
	opts ...SubmitOption,
) *TypedResult[R] {
	result := &TypedResult[R]{inner: NewAnyResult()}

	if p.isClosed() {
		result.inner.SetError(ErrPoolClosed)
		return result
	}

	config := applyOptions(opts)

	// P0-02: 处理延迟任务
	if config.delay > 0 {
		// P1-01: 处理速率限制错误
		if err := p.scheduleDelayedTask(ctx, config.delay, func() {
			if !p.isClosed() {
				p.statsMu.Lock()
				p.stats.ByPriority[config.priority]++
				p.statsMu.Unlock()

				if err := p.pool.Submit(func() {
					p.safeExecute(func() {
						val, err := task(ctx, arg)
						if err != nil {
							result.inner.SetError(err)
						} else {
							result.inner.SetValue(val)
						}
					})
				}); err != nil {
					result.inner.SetError(err)
				}
			}
		}); err != nil {
			result.inner.SetError(err)
		}
		return result
	}

	p.statsMu.Lock()
	p.stats.ByPriority[config.priority]++
	p.statsMu.Unlock()

	// P0-03: 检查 Submit 错误
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task(ctx, arg)
			if err != nil {
				result.inner.SetError(err)
			} else {
				result.inner.SetValue(val)
			}
		})
	}); err != nil {
		result.inner.SetError(err)
	}

	return result
}

// ======================================
// 管理方法实现
// ======================================

// Stats 实现接口
func (p *AntsGoroutineProvider) Stats() PoolStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	stats := p.stats
	stats.Running = p.pool.Running()
	stats.Waiting = p.pool.Waiting()
	stats.Total = p.pool.Free() + stats.Running
	return stats
}

// Health 实现接口
func (p *AntsGoroutineProvider) Health() HealthStatus {
	if p.isClosed() {
		return HealthStatusUnhealthy
	}

	// 检查协程池健康状态
	if p.pool.IsClosed() {
		return HealthStatusUnhealthy
	}

	return HealthStatusHealthy
}

// SetCapacity 实现接口
func (p *AntsGoroutineProvider) SetCapacity(capacity int) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// P0-04: 容量验证
	if capacity < MinPoolCapacity || capacity > MaxPoolCapacity {
		return fmt.Errorf("invalid capacity: %d (must be between %d and %d)",
			capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	p.pool.Tune(capacity)

	p.statsMu.Lock()
	p.stats.Capacity = capacity
	p.statsMu.Unlock()

	return nil
}

// Close 实现接口
func (p *AntsGoroutineProvider) Close() error {
	// 使用 atomic 确保幂等性
	if p.closed.Swap(true) {
		return nil
	}

	// P0-02: 通知所有延迟任务停止
	close(p.stopCh)

	// 等待所有延迟任务完成
	p.delayedWg.Wait()

	// 释放池资源
	p.pool.Release()
	return nil
}

// CloseWithTimeout 实现接口
func (p *AntsGoroutineProvider) CloseWithTimeout(timeout time.Duration) error {
	done := make(chan error, 1)

	go func() {
		done <- p.Close()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		// 超时后强制标记为关闭
		p.closed.Store(true)
		return fmt.Errorf("close timeout after %v: %w", timeout, ErrTaskTimeout)
	}
}

// ======================================
// 内部方法
// ======================================

func (p *AntsGoroutineProvider) isClosed() bool {
	return p.closed.Load()
}
