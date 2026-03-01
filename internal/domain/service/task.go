// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/event"
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 类型定义
// ==========================================

// TaskPriority 任务优先级（与 model.TaskPriority 一致）
type TaskPriority = model.TaskPriority

// TaskPoolStats 任务池统计信息
type TaskPoolStats = model.TaskPoolStats

// TaskHealthStatus 健康状态
type TaskHealthStatus = model.TaskHealthStatus

// ==========================================
// 优先级常量
// ==========================================

const (
	// PriorityCritical 最高优先级
	PriorityCritical = model.TaskPriorityCritical
	// PriorityHigh 高优先级
	PriorityHigh = model.TaskPriorityHigh
	// PriorityNormal 普通优先级
	PriorityNormal = model.TaskPriorityNormal
	// PriorityLow 低优先级
	PriorityLow = model.TaskPriorityLow
)

// ==========================================
// 核心接口（小接口原则）
// ==========================================

// TaskExecutor 基础任务执行器接口（最小接口）
// 适用于简单场景，只需要 Submit 方法
type TaskExecutor interface {
	Submit(ctx context.Context, priority TaskPriority, task func(context.Context)) error
	Close() error
}

// TaskExecutorWithArg 带参数的任务执行器
type TaskExecutorWithArg interface {
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error
}

// TaskExecutorWithResult 带返回值的任务执行器
type TaskExecutorWithResult interface {
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) TaskResult[any]
}

// TaskScheduler 任务调度器（延迟任务）
type TaskScheduler interface {
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error
}

// TaskPriorityExecutor 优先级任务执行器
type TaskPriorityExecutor interface {
	SubmitWithPriority(ctx context.Context, priority TaskPriority, task func(context.Context)) error
}

// TaskBatcher 批量任务执行器
type TaskBatcher interface {
	// SubmitBatch 批量提交任务
	SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

	// SubmitBatchWithResult 批量提交带结果任务
	SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []TaskResult[any]
}

// TaskManager 任务池管理接口
type TaskManager interface {
	// Stats 获取任务池统计信息
	Stats() TaskPoolStats
	// Health 获取健康状态
	Health() TaskHealthStatus
	// SetCapacity 设置容量
	SetCapacity(capacity int) error
	// Close 关闭任务池
	Close() error
	// CloseWithTimeout 带超时关闭
	CloseWithTimeout(timeout time.Duration) error
}

// ==========================================
// 组合接口（按需组合）
// ==========================================

// BasicTaskExecutor 基础任务执行器
// 组合：TaskExecutor + TaskExecutorWithArg + TaskExecutorWithResult
type BasicTaskExecutor interface {
	TaskExecutor
	TaskExecutorWithArg
	TaskExecutorWithResult
}

// AsyncTaskExecutor 异步任务执行器
// 组合：BasicTaskExecutor + TaskScheduler + TaskPriorityExecutor + TaskBatcher
type AsyncTaskExecutor interface {
	BasicTaskExecutor
	TaskScheduler
	TaskPriorityExecutor
	TaskBatcher
}

// ExecutorManager 执行器管理接口
// 组合：AsyncTaskExecutor + TaskManager
type ExecutorManager interface {
	AsyncTaskExecutor
	TaskManager

	// 扩展方法（用于向后兼容，将在未来移除）
	// TODO: 逐步迁移使用代码到小接口组合，然后移除这些方法
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) TaskResult[any]

	SubmitAdvanced(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
		opts ...TaskSubmitOption,
	) TaskResult[any]

	SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error

	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error
}

// ==========================================
// 领域事件（重新导出，保持向后兼容）
// ==========================================

// 领域事件已迁移到 internal/domain/event 包
// 这里保留类型别名以保持向后兼容

// TaskSubmittedEvent 任务提交事件
type TaskSubmittedEvent = event.TaskSubmittedEvent

// TaskCompletedEvent 任务完成事件
type TaskCompletedEvent = event.TaskCompletedEvent

// TaskFailedEvent 任务失败事件
type TaskFailedEvent = event.TaskFailedEvent

// QueueFullEvent 队列满事件（背压触发）
type QueueFullEvent = event.QueueFullEvent

// ==========================================
// 选项模式定义（用于高级场景）
// ==========================================

// TaskSubmitOptions 提交选项配置
type TaskSubmitOptions struct {
	Priority TaskPriority
	Delay    time.Duration
}

// TaskSubmitOption 提交选项
type TaskSubmitOption func(*TaskSubmitOptions)

// WithTaskPriority 设置优先级
func WithTaskPriority(priority TaskPriority) TaskSubmitOption {
	return func(opts *TaskSubmitOptions) {
		opts.Priority = priority
	}
}

// WithTaskDelay 设置延迟
func WithTaskDelay(delay time.Duration) TaskSubmitOption {
	return func(opts *TaskSubmitOptions) {
		opts.Delay = delay
	}
}

// ==========================================
// Result 类型
// ==========================================

// TaskResult 异步任务结果接口
type TaskResult[T any] interface {
	// Get 获取结果（阻塞等待）
	Get(ctx context.Context) (T, error)
	// IsDone 检查是否已完成
	IsDone() bool
}
