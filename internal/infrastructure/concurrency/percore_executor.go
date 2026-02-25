// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// Per-Core 执行器实现（PR-087b）
// ==========================================

// PerCoreExecutorConfig Per-Core 执行器配置
type PerCoreExecutorConfig struct {
	// CoreCount 核心数量，0 表示自动检测
	CoreCount int

	// QueueSize 每核队列大小
	QueueSize int

	// ShutdownTimeout 关闭超时时间
	ShutdownTimeout time.Duration

	// EnableMetrics 是否启用指标
	EnableMetrics bool
}

// DefaultPerCoreExecutorConfig 默认配置
var DefaultPerCoreExecutorConfig = PerCoreExecutorConfig{
	CoreCount:        0,                      // 0 = auto
	QueueSize:        1000,
	ShutdownTimeout:  30 * time.Second,
	EnableMetrics:    true,
}

// PerCoreExecutor Per-Core 无锁执行器
// 每 CPU 核心一个 goroutine，绑核执行，无锁设计
type PerCoreExecutor struct {
	config     PerCoreExecutorConfig
	cpus       int
	workers    []*coreWorker
	stats      atomic.Pointer[model.TaskPoolStats]
	health     atomic.Int32
	closeCh    chan struct{}
	wg         sync.WaitGroup
	roundRobin atomic.Int32
	mu         sync.RWMutex
}

// coreWorker 每核心一个 worker
type coreWorker struct {
	taskC    chan func(context.Context)
	coreID   int
	executor *PerCoreExecutor
}

// NewPerCoreExecutor 创建 Per-Core 执行器
func NewPerCoreExecutor(config *PerCoreExecutorConfig) *PerCoreExecutor {
	if config == nil {
		config = &DefaultPerCoreExecutorConfig
	}

	// 确定核心数
	cpus := config.CoreCount
	if cpus <= 0 {
		cpus = runtime.NumCPU()
	}

	// 限制核心数，避免资源耗尽
	if cpus > 64 {
		cpus = 64
	}

	// 队列大小
	queueSize := config.QueueSize
	if queueSize <= 0 {
		queueSize = DefaultPerCoreExecutorConfig.QueueSize
	}

	e := &PerCoreExecutor{
		config:  *config,
		cpus:    cpus,
		workers: make([]*coreWorker, cpus),
		closeCh: make(chan struct{}),
	}

	// 初始化 worker
	for i := 0; i < cpus; i++ {
		e.workers[i] = &coreWorker{
			taskC:  make(chan func(context.Context), queueSize),
			coreID: i,
			executor: e,
		}
		e.wg.Add(1)
		go e.workers[i].run(&e.wg)
	}

	// 初始化统计
	stats := &model.TaskPoolStats{
		Total:      0,
		ByPriority: make(map[model.TaskPriority]int),
		Running:    0,
		Waiting:    0,
		Capacity:  cpus * queueSize,
	}
	e.stats.Store(stats)
	e.health.Store(int32(model.TaskHealthStatusHealthy))

	return e
}

// run worker 运行时循环
func (w *coreWorker) run(wg *sync.WaitGroup) {
	defer wg.Done()

	// 绑核到指定核心
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 设置 GOMAXPROCS 为 1，确保单核执行
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	for {
		select {
		case task, ok := <-w.taskC:
			if !ok {
				return
			}
			ctx := context.Background()
			task(ctx)

		case <-w.executor.closeCh:
			// 处理剩余任务
			for {
				select {
				case task, ok := <-w.taskC:
					if !ok {
						return
					}
					ctx := context.Background()
					task(ctx)
				default:
					return
				}
			}
		}
	}
}

// Submit 提交任务
func (e *PerCoreExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if e.isClosed() {
		return errors.ErrPerCoreExecutorClosed
	}

	// 选择核心：简单轮询
	coreID := e.nextCore()
	select {
	case e.workers[coreID].taskC <- task:
		return nil
	default:
		return errors.ErrPerCoreQueueFull
	}
}

// SubmitWithPriority 提交优先级任务
// 注意：当前实现忽略优先级，后续可优化
func (e *PerCoreExecutor) SubmitWithPriority(ctx context.Context, priority model.TaskPriority, task func(context.Context)) error {
	return e.Submit(ctx, task)
}

// SubmitDelayed 提交延迟任务
func (e *PerCoreExecutor) SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error {
	if e.isClosed() {
		return errors.ErrPerCoreExecutorClosed
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return e.Submit(ctx, task)
	case <-ctx.Done():
		return ctx.Err()
	case <-e.closeCh:
		return errors.ErrPerCoreExecutorClosed
	}
}

// SubmitWithResult 提交带结果任务
func (e *PerCoreExecutor) SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) service.GoroutineResult[any] {
	result := &simpleResult[any]{}

	wrappedTask := func(ctx context.Context) {
		value, err := task(ctx)
		result.set(value, err)
	}

	if err := e.Submit(ctx, wrappedTask); err != nil {
		result.set(nil, err)
	}

	return result
}

// SubmitBatch 批量提交任务
func (e *PerCoreExecutor) SubmitBatch(ctx context.Context, tasks []func(context.Context)) error {
	if e.isClosed() {
		return errors.ErrPerCoreExecutorClosed
	}

	var errs []error
	for _, task := range tasks {
		if err := e.Submit(ctx, task); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(errors.ErrPerCoreQueueFull, "failed to submit %d tasks", len(errs))
	}
	return nil
}

// SubmitBatchWithArg 批量提交带参数任务
func (e *PerCoreExecutor) SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error {
	if len(tasks) != len(args) {
		return errors.ErrTaskArgLengthMismatch
	}

	var errs []error
	for i, task := range tasks {
		wrapped := func(ctx context.Context) {
			task(ctx, args[i])
		}
		if err := e.Submit(ctx, wrapped); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(errors.ErrPerCoreQueueFull, "failed to submit %d tasks", len(errs))
	}
	return nil
}

// SubmitBatchAllErrors 批量提交，收集所有错误
func (e *PerCoreExecutor) SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error {
	if e.isClosed() {
		return []error{errors.ErrPerCoreExecutorClosed}
	}

	var errs []error
	for _, task := range tasks {
		if err := e.Submit(ctx, task); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// SubmitBatchWithResult 批量提交带结果任务
func (e *PerCoreExecutor) SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []service.GoroutineResult[any] {
	results := make([]service.GoroutineResult[any], len(tasks))
	for i, task := range tasks {
		results[i] = e.SubmitWithResult(ctx, task)
	}
	return results
}

// SubmitWithArg 带参数任务提交
func (e *PerCoreExecutor) SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error {
	wrapped := func(ctx context.Context) {
		task(ctx, arg)
	}
	return e.Submit(ctx, wrapped)
}

// SubmitWithArgAndResult 带参数和结果任务提交
func (e *PerCoreExecutor) SubmitWithArgAndResult(ctx context.Context, task func(context.Context, any) (any, error), arg any) service.GoroutineResult[any] {
	wrapped := func(ctx context.Context) (any, error) {
		return task(ctx, arg)
	}
	return e.SubmitWithResult(ctx, wrapped)
}

// SubmitAdvanced 高级提交
func (e *PerCoreExecutor) SubmitAdvanced(ctx context.Context, task func(context.Context, any) (any, error), arg any, opts ...service.GoroutineSubmitOption) service.GoroutineResult[any] {
	// 当前实现忽略高级选项
	return e.SubmitWithArgAndResult(ctx, task, arg)
}

// Stats 获取统计信息
func (e *PerCoreExecutor) Stats() model.TaskPoolStats {
	stats := e.stats.Load()
	if stats == nil {
		return model.TaskPoolStats{}
	}
	return *stats
}

// Health 获取健康状态
func (e *PerCoreExecutor) Health() model.TaskHealthStatus {
	return model.TaskHealthStatus(e.health.Load())
}

// SetCapacity 设置容量
func (e *PerCoreExecutor) SetCapacity(capacity int) error {
	// Per-Core 执行器不支持动态调整容量
	return errors.ErrPerCoreNotSupported
}

// Close 关闭执行器
func (e *PerCoreExecutor) Close() error {
	return e.release()
}

// CloseWithTimeout 带超时关闭
func (e *PerCoreExecutor) CloseWithTimeout(timeout time.Duration) error {
	// 通知关闭
	close(e.closeCh)

	// 等待完成
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.ErrPerCoreShutdownTimeout
	}
}

// Release 释放资源（Close 别名）
func (e *PerCoreExecutor) Release() error {
	return e.Close()
}

// ReleaseWithTimeout 带超时释放
func (e *PerCoreExecutor) ReleaseWithTimeout(timeout time.Duration) error {
	return e.CloseWithTimeout(timeout)
}

// nextCore 选择下一个核心（简单轮询）
func (e *PerCoreExecutor) nextCore() int {
	return int(e.roundRobin.Add(1)) % e.cpus
}

// isClosed 检查是否已关闭
func (e *PerCoreExecutor) isClosed() bool {
	select {
	case <-e.closeCh:
		return true
	default:
		return false
	}
}

// release 释放资源
func (e *PerCoreExecutor) release() error {
	if e.isClosed() {
		return nil
	}

	e.health.Store(int32(model.TaskHealthStatusUnhealthy))
	close(e.closeCh)
	e.wg.Wait()

	return nil
}

// simpleResult 简单结果实现
type simpleResult[T any] struct {
	value   T
	err     error
	ready   chan struct{}
	once    sync.Once
}

func (r *simpleResult[T]) set(value T, err error) {
	r.once.Do(func() {
		if r.ready == nil {
			r.ready = make(chan struct{})
		}
		r.value = value
		r.err = err
		close(r.ready)
	})
}

func (r *simpleResult[T]) Get(ctx context.Context) (T, error) {
	select {
	case <-r.ready:
		return r.value, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (r *simpleResult[T]) IsDone() bool {
	select {
	case <-r.ready:
		return true
	default:
		return false
	}
}

// 确保接口实现
var _ service.GoroutineProvider = (*PerCoreExecutor)(nil)
