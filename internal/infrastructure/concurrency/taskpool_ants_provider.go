// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
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
// AntsTaskPoolProvider 实现
// ==========================================

// AntsTaskPoolProvider 基于 ants 库的任务池实现
// 实现 domain/service.ExecutorManager 接口
type AntsTaskPoolProvider struct {
	pool       *ants.Pool
	config     *ProviderConfig
	stats      TaskPoolStats
	statsMu    sync.RWMutex
	closed     atomic.Bool
	stopCh     chan struct{}  // 全局停止信号
	delayedWg  sync.WaitGroup // 跟踪延迟任务
	delayedSem chan struct{}  // P1-01: 延迟任务信号量（速率限制）
	// 缩容相关
	scaleCheckTicker *time.Ticker // 缩容检查定时器
	currentCapacity  int          // 当前实际容量
	// 扩容检查计数器
	submitCounter atomic.Int64 // Submit 调用计数器
}

// ProviderConfig 任务池配置
type ProviderConfig struct {
	// Capacity 任务池容量
	Capacity int
	// MaxCapacity 最大容量（自动扩容上限）
	MaxCapacity int
	// EnablePriority 是否启用优先级
	EnablePriority bool
	// EnableDelayed 是否启用延迟任务
	EnableDelayed bool
	// EnableAutoScale 是否启用自动扩容
	EnableAutoScale bool
	// ScaleThreshold 扩容阈值（运行中 goroutine 占比）
	ScaleThreshold float64
	// ScaleStep 每次扩容步长
	ScaleStep int
	// ScaleCheckInterval 扩容检查间隔（每N次Submit检查一次）
	ScaleCheckInterval int64
	// EnableAutoShrink 是否启用自动缩容
	EnableAutoShrink bool
	// ShrinkThreshold 缩容阈值（运行中 goroutine 占比）
	ShrinkThreshold float64
	// ShrinkStep 每次缩容步长
	ShrinkStep int
	// ShrinkCheckInterval 缩容检查间隔
	ShrinkCheckInterval time.Duration
	// ShrinkCooldown 缩容冷却时间（扩容后多久才能缩容）
	ShrinkCooldown time.Duration
	// OnError 错误回调函数（可选）
	// 当延迟任务内部提交失败时调用
	// taskType: "delayed", "priority", "batch" 等
	OnError func(err error, taskType string)
}

// DefaultProviderConfig 默认配置
// 起步内存约 80MB（10000 * 8KB），自动扩容到最大 10 万，支持自动缩容
func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{
		Capacity:            10000,  // 起步容量 1 万（约 80MB）
		MaxCapacity:         100000, // 最大容量 10 万
		EnablePriority:      true,
		EnableDelayed:       true,
		EnableAutoScale:     true,             // 启用自动扩容
		ScaleThreshold:      0.8,              // 使用率超过 80% 时扩容
		ScaleStep:           5000,             // 每次扩容 5000
		ScaleCheckInterval:  100,              // 每 100 次 Submit 检查一次扩容
		EnableAutoShrink:    true,             // 启用自动缩容
		ShrinkThreshold:     0.3,              // 使用率低于 30% 时缩容
		ShrinkStep:          5000,             // 每次缩容 5000
		ShrinkCheckInterval: 5 * time.Minute,  // 每 5 分钟检查一次缩容
		ShrinkCooldown:      10 * time.Minute, // 扩容后 10 分钟才能缩容
	}
}

// NewAntsTaskPoolProvider 创建 ants 任务池提供者
func NewAntsTaskPoolProvider(config *ProviderConfig) (*AntsTaskPoolProvider, error) {
	if config == nil {
		config = DefaultProviderConfig()
	}

	// P0-04: 容量验证
	if config.Capacity < MinPoolCapacity || config.Capacity > MaxPoolCapacity {
		return nil, errors.Wrapf(errors.ErrInvalidParam, "invalid pool capacity: %d (must be between %d and %d)",
			config.Capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	pool, err := ants.NewPool(
		config.Capacity,
		ants.WithPreAlloc(true),           // 预分配，起步内存 80MB
		ants.WithNonblocking(false),       // 阻塞提交，避免任务丢失
		ants.WithMaxBlockingTasks(100000), // 最大阻塞任务数
		ants.WithPanicHandler(func(i any) {
			logrus.WithField("panic", i).Error("ants pool panic recovered")
		}),
	)
	if err != nil {
		return nil, err
	}

	provider := &AntsTaskPoolProvider{
		pool:            pool,
		config:          config,
		currentCapacity: config.Capacity,
		stats: TaskPoolStats{
			Capacity:   config.Capacity,
			ByPriority: make(map[TaskPriority]int),
		},
		stopCh:     make(chan struct{}),
		delayedSem: make(chan struct{}, DefaultMaxDelayedTasks), // P1-01: 速率限制
	}

	// 启动缩容检查协程
	if config.EnableAutoShrink {
		provider.startShrinkChecker()
	}

	return provider, nil
}

// ======================================
// P0-01: Panic 恢复包装器
// ======================================

// safeExecute 安全执行任务，捕获 panic
// P1-02: 添加日志记录
func (p *AntsTaskPoolProvider) safeExecute(task func()) {
	defer func() {
		if r := recover(); r != nil {
			// P1-02: 使用 panicRecoveryHandler 处理 panic
			p.handlePanic(r)
		}
	}()
	task()
}

// handlePanic 处理 panic 恢复
func (p *AntsTaskPoolProvider) handlePanic(r any) {
	stack := debug.Stack()
	logrus.WithFields(logrus.Fields{
		"panic": r,
		"stack": string(stack),
	}).Error("panic recovered in goroutine task")
}

// handleTaskError 处理任务内部错误（P1-03: 延迟任务错误处理）
// 优先调用用户配置的回调，否则使用 logrus 记录
func (p *AntsTaskPoolProvider) handleTaskError(err error, taskType string) {
	if p.config.OnError != nil {
		p.config.OnError(err, taskType)
		return
	}
	// 默认使用 logrus 记录
	logrus.WithFields(logrus.Fields{
		"task_type": taskType,
		"error":     err.Error(),
	}).Warn("task submission failed")
}

// ======================================
// P0-02: 延迟任务调度（修复 goroutine 泄漏）
// ======================================

// scheduleDelayedTask 调度延迟任务（统一处理，避免泄漏）
// P1-01: 添加速率限制
func (p *AntsTaskPoolProvider) scheduleDelayedTask(
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
func (p *AntsTaskPoolProvider) Submit(ctx context.Context, task func(context.Context)) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// 自动扩容检查（每 N 次 Submit 检查一次）
	if p.config.ScaleCheckInterval > 0 && p.submitCounter.Add(1)%p.config.ScaleCheckInterval == 0 {
		p.autoScale()
	}

	// P0-01: 添加 panic 恢复
	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx)
		})
	})
}

// SubmitWithArg 实现接口
func (p *AntsTaskPoolProvider) SubmitWithArg(
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
func (p *AntsTaskPoolProvider) SubmitWithResult(
	ctx context.Context,
	task func(context.Context) (any, error),
) Result[any] {
	return p.submitWithResult(ctx, func() (any, error) {
		return task(ctx)
	})
}

// SubmitWithArgAndResult 实现接口
func (p *AntsTaskPoolProvider) SubmitWithArgAndResult(
	ctx context.Context,
	task func(context.Context, any) (any, error),
	arg any,
) Result[any] {
	return p.submitWithResult(ctx, func() (any, error) {
		return task(ctx, arg)
	})
}

// submitWithResult 统一的结果任务提交逻辑
func (p *AntsTaskPoolProvider) submitWithResult(
	ctx context.Context,
	task func() (any, error),
) *AnyResult {
	result := NewAnyResult()

	if p.isClosed() {
		result.SetError(ErrPoolClosed)
		return result
	}

	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task()
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
func (p *AntsTaskPoolProvider) SubmitWithPriority(
	ctx context.Context,
	priority TaskPriority,
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
func (p *AntsTaskPoolProvider) SubmitDelayed(
	ctx context.Context,
	delay time.Duration,
	task func(context.Context),
) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// P0-02: 使用统一的延迟任务调度，P1-01: 处理速率限制错误
	return p.scheduleDelayedTask(ctx, delay, func() {
		// P1-03: 处理内部提交错误
		if err := p.pool.Submit(func() {
			p.safeExecute(func() {
				task(ctx)
			})
		}); err != nil {
			p.handleTaskError(err, "delayed")
		}
	})
}

// ======================================
// 高级方法实现
// ======================================

// submitOptions 提交选项配置（内部使用）
// 使用 domain/service.TaskSubmitOptions 的别名
type submitOptions = service.TaskSubmitOptions

// applyOptions 应用选项
func applyOptions(opts []TaskSubmitOption) *submitOptions {
	config := &submitOptions{
		Priority: PriorityNormal,
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// SubmitAdvanced 实现接口
func (p *AntsTaskPoolProvider) SubmitAdvanced(
	ctx context.Context,
	task func(context.Context, any) (any, error),
	arg any,
	opts ...TaskSubmitOption,
) TaskResult[any] {
	return p.submitAdvancedInternal(ctx, func() (any, error) {
		return task(ctx, arg)
	}, opts...)
}

// submitAdvancedInternal 统一的高级任务提交逻辑
func (p *AntsTaskPoolProvider) submitAdvancedInternal(
	ctx context.Context,
	task func() (any, error),
	opts ...TaskSubmitOption,
) *AnyResult {
	result := NewAnyResult()

	if p.isClosed() {
		result.SetError(ErrPoolClosed)
		return result
	}

	config := applyOptions(opts)

	// 处理延迟任务
	if config.Delay > 0 {
		if err := p.scheduleDelayedTask(ctx, config.Delay, func() {
			if !p.isClosed() {
				p.executeWithPriorityAndResult(ctx, task, config.Priority, result)
			}
		}); err != nil {
			result.SetError(err)
		}
		return result
	}

	// 立即执行任务
	p.executeWithPriorityAndResult(ctx, task, config.Priority, result)
	return result
}

// executeWithPriorityAndResult 带优先级统计和结果的任务执行
func (p *AntsTaskPoolProvider) executeWithPriorityAndResult(
	ctx context.Context,
	task func() (any, error),
	priority TaskPriority,
	result *AnyResult,
) {
	// 记录优先级统计
	p.statsMu.Lock()
	p.stats.ByPriority[priority]++
	p.statsMu.Unlock()

	// 提交任务
	if err := p.pool.Submit(func() {
		p.safeExecute(func() {
			val, err := task()
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

// ======================================
// 批量方法实现
// ======================================

// SubmitBatch 实现接口
func (p *AntsTaskPoolProvider) SubmitBatch(ctx context.Context, tasks []func(context.Context)) error {
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
func (p *AntsTaskPoolProvider) SubmitBatchWithArg(
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
func (p *AntsTaskPoolProvider) SubmitBatchAllErrors(
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
func (p *AntsTaskPoolProvider) SubmitBatchWithResult(
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
		results[i] = p.submitWithResult(ctx, func() (any, error) {
			return task(ctx)
		})
	}

	return results
}

// ======================================
// 类型安全函数（供泛型辅助函数调用）
// ======================================

// SubmitWithArgTyped 类型安全的带参数函数
func SubmitWithArgTyped[T any](
	p *AntsTaskPoolProvider,
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
	p *AntsTaskPoolProvider,
	ctx context.Context,
	task func(context.Context) (T, error),
) *TypedResult[T] {
	return &TypedResult[T]{
		inner: p.submitWithResult(ctx, func() (any, error) {
			return task(ctx)
		}),
	}
}

// SubmitWithArgAndResultTyped 类型安全的带参数和返回值函数
func SubmitWithArgAndResultTyped[T any, R any](
	p *AntsTaskPoolProvider,
	ctx context.Context,
	task func(context.Context, T) (R, error),
	arg T,
) *TypedResult[R] {
	return &TypedResult[R]{
		inner: p.submitWithResult(ctx, func() (any, error) {
			return task(ctx, arg)
		}),
	}
}

// SubmitAdvancedTyped 类型安全的高级函数
func SubmitAdvancedTyped[T any, R any](
	p *AntsTaskPoolProvider,
	ctx context.Context,
	task func(context.Context, T) (R, error),
	arg T,
	opts ...TaskSubmitOption,
) *TypedResult[R] {
	return &TypedResult[R]{
		inner: p.submitAdvancedInternal(ctx, func() (any, error) {
			return task(ctx, arg)
		}, opts...),
	}
}

// ======================================
// 管理方法实现
// ======================================

// Stats 实现接口
func (p *AntsTaskPoolProvider) Stats() TaskPoolStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	stats := p.stats
	stats.Running = p.pool.Running()
	stats.Waiting = p.pool.Waiting()
	stats.Total = p.pool.Free() + stats.Running
	return stats
}

// Health 实现接口
func (p *AntsTaskPoolProvider) Health() TaskHealthStatus {
	if p.isClosed() {
		return HealthStatusUnhealthy
	}

	// 检查任务池健康状态
	if p.pool.IsClosed() {
		return HealthStatusUnhealthy
	}

	return HealthStatusHealthy
}

// SetCapacity 实现接口
func (p *AntsTaskPoolProvider) SetCapacity(capacity int) error {
	if p.isClosed() {
		return ErrPoolClosed
	}

	// P0-04: 容量验证
	if capacity < MinPoolCapacity || capacity > MaxPoolCapacity {
		return errors.Wrapf(errors.ErrInvalidParam, "invalid capacity: %d (must be between %d and %d)",
			capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	p.pool.Tune(capacity)

	p.statsMu.Lock()
	p.stats.Capacity = capacity
	p.statsMu.Unlock()

	return nil
}

// Close 实现接口
func (p *AntsTaskPoolProvider) Close() error {
	// 使用 atomic 确保幂等性
	if p.closed.Swap(true) {
		return nil
	}

	// 停止缩容检查定时器
	if p.scaleCheckTicker != nil {
		p.scaleCheckTicker.Stop()
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
func (p *AntsTaskPoolProvider) CloseWithTimeout(timeout time.Duration) error {
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
		return errors.Wrapf(errors.ErrTaskTimeout, "close timeout after %v", timeout)
	}
}

// ======================================
// 内部方法
// ======================================

func (p *AntsTaskPoolProvider) isClosed() bool {
	return p.closed.Load()
}

// ======================================
// 自动扩缩容支持
// ======================================

// autoScale 检查并执行自动扩容
func (p *AntsTaskPoolProvider) autoScale() {
	if !p.config.EnableAutoScale {
		return
	}

	stats := p.pool.Running()
	capacity := p.pool.Cap()

	// 计算使用率
	usage := float64(stats) / float64(capacity)

	// 超过阈值且未达到最大容量时扩容
	if usage >= p.config.ScaleThreshold && capacity < p.config.MaxCapacity {
		newCapacity := capacity + p.config.ScaleStep
		if newCapacity > p.config.MaxCapacity {
			newCapacity = p.config.MaxCapacity
		}

		// 使用 Tune 动态调整容量
		p.pool.Tune(newCapacity)
		p.currentCapacity = newCapacity

		logrus.WithFields(logrus.Fields{
			"old_capacity": capacity,
			"new_capacity": newCapacity,
			"running":      stats,
			"usage":        usage,
		}).Info("goroutine pool auto scaled up")
	}
}

// startShrinkChecker 启动缩容检查协程
func (p *AntsTaskPoolProvider) startShrinkChecker() {
	p.scaleCheckTicker = time.NewTicker(p.config.ShrinkCheckInterval)

	go func() {
		for {
			select {
			case <-p.scaleCheckTicker.C:
				p.checkAndShrink()
			case <-p.stopCh:
				return
			}
		}
	}()
}

// checkAndShrink 检查并执行缩容
func (p *AntsTaskPoolProvider) checkAndShrink() {
	if !p.config.EnableAutoShrink || p.isClosed() {
		return
	}

	running := p.pool.Running()
	capacity := p.pool.Cap()

	// 计算使用率
	usage := float64(running) / float64(capacity)

	// 使用率低于阈值且高于初始容量时缩容
	if usage < p.config.ShrinkThreshold && capacity > p.config.Capacity {
		newCapacity := capacity - p.config.ShrinkStep
		if newCapacity < p.config.Capacity {
			newCapacity = p.config.Capacity
		}

		// 确保不会缩到小于当前运行中的数量
		if newCapacity < running {
			newCapacity = running + p.config.ShrinkStep/2
		}

		// 使用 Tune 调整容量（ants 会释放多余的 worker）
		p.pool.Tune(newCapacity)
		p.currentCapacity = newCapacity

		logrus.WithFields(logrus.Fields{
			"old_capacity": capacity,
			"new_capacity": newCapacity,
			"running":      running,
			"usage":        usage,
		}).Info("goroutine pool auto scaled down")
	}
}
