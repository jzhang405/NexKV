// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

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
// 任务执行器接口（拆分后的小接口）
// ==========================================

// TaskExecutor 基础任务执行器接口（最小接口）
// 适用于简单场景，只需要 Submit 方法
type TaskExecutor interface {
	Submit(ctx context.Context, task func(context.Context)) error
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

// CoreTaskExecutor 绑核任务执行器
// 将任务绑定到特定 CPU 核心执行，减少跨核调度开销
type CoreTaskExecutor interface {
	// ExecuteOnCore 在指定核心上执行任务
	ExecuteOnCore(ctx context.Context, coreID int, task func(context.Context)) error

	// GetCurrentCore 获取当前 goroutine 绑定的核心 ID
	GetCurrentCore() int
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
// Level 2: 组合接口（3个）
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

// FullTaskExecutor 完整任务执行器
// 组合：AsyncTaskExecutor + TaskManager
type FullTaskExecutor interface {
	AsyncTaskExecutor
	TaskManager
}

// ==========================================
// Level 3: TaskPoolProvider 完整接口
// ==========================================

// TaskPoolProvider 任务池提供者接口
// 注意：由于 Go 接口限制，接口方法不能有类型参数
// 泛型方法通过独立辅助函数提供（由基础设施层实现）
type TaskPoolProvider interface {
	FullTaskExecutor

	// ======================================
	// 扩展方法（组合接口未覆盖）
	// ======================================

	// SubmitWithArgAndResult 带参数和返回值任务（使用 any 类型）
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) TaskResult[any]

	// SubmitAdvanced 高级任务提交（使用 any 类型）
	SubmitAdvanced(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
		opts ...TaskSubmitOption,
	) TaskResult[any]

	// SubmitBatchWithArg 批量提交：带参数（使用 any 类型）
	SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error

	// SubmitBatchAllErrors 批量提交：收集所有错误（无参数）
	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error
}

// ==========================================
// 领域事件定义
// ==========================================

// TaskSubmittedEvent 任务提交事件
type TaskSubmittedEvent struct {
	TaskID    string
	Priority  TaskPriority
	Timestamp time.Time
}

// TaskCompletedEvent 任务完成事件
type TaskCompletedEvent struct {
	TaskID    string
	Duration  time.Duration
	Timestamp time.Time
}

// TaskFailedEvent 任务失败事件
type TaskFailedEvent struct {
	TaskID    string
	Error     error
	Timestamp time.Time
}

// QueueFullEvent 队列满事件（背压触发）
type QueueFullEvent struct {
	CoreID      int
	QueueLength int
	Strategy    string // 触发的背压策略
	Timestamp   time.Time
}

// ==========================================
// 选项模式定义（用于 SubmitAdvanced）
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
