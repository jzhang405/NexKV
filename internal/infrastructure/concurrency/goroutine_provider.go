// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// GoroutineProvider 接口定义
// ==========================================

// GoroutineProvider 协程池提供者接口
// 注意：由于 Go 接口限制，接口方法不能有类型参数
// 泛型方法通过独立辅助函数提供（见 helpers.go）
type GoroutineProvider interface {
	// ======================================
	// 基础方法（简单场景，快速上手）
	// ======================================

	// Submit 简单任务：无参数，无返回值
	Submit(ctx context.Context, task func(context.Context)) error

	// SubmitWithArg 带参数任务：避免闭包陷阱（使用 any 类型）
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error

	// SubmitWithResult 带返回值任务（使用 any 类型）
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) Result[any]

	// SubmitWithArgAndResult 带参数和返回值任务（使用 any 类型）
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) Result[any]

	// SubmitWithPriority 优先级任务
	SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error

	// SubmitDelayed 延迟任务
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

	// ======================================
	// 高级方法（选项模式）
	// ======================================

	// SubmitAdvanced 高级任务提交（使用 any 类型）
	SubmitAdvanced(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
		opts ...SubmitOption,
	) Result[any]

	// ======================================
	// 批量方法（语义清晰，单独列出）
	// ======================================

	// SubmitBatch 批量提交：快速执行多个任务（无参数，无返回值）
	SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

	// SubmitBatchWithArg 批量提交：带参数（使用 any 类型）
	SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error

	// SubmitBatchAllErrors 批量提交：收集所有错误（无参数）
	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error

	// SubmitBatchWithResult 批量提交：带返回值（使用 any 类型）
	SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []Result[any]

	// ======================================
	// 管理方法
	// ======================================

	Stats() PoolStats
	Health() HealthStatus
	SetCapacity(capacity int) error
	Close() error
	CloseWithTimeout(timeout time.Duration) error
}

// ==========================================
// 选项模式定义（用于 SubmitAdvanced）
// ==========================================

// SubmitOption 提交选项
type SubmitOption func(*submitOptions)

// submitOptions 提交选项配置
type submitOptions struct {
	priority Priority
	delay    time.Duration
}

// WithPriority 设置优先级
func WithPriority(priority Priority) SubmitOption {
	return func(opts *submitOptions) {
		opts.priority = priority
	}
}

// WithDelay 设置延迟
func WithDelay(delay time.Duration) SubmitOption {
	return func(opts *submitOptions) {
		opts.delay = delay
	}
}

// 未来可扩展：
// func WithTimeout(timeout time.Duration) SubmitOption
// func WithRetry(count int) SubmitOption
// func WithCallback(cb func()) SubmitOption

// applyOptions 应用选项
func applyOptions(opts []SubmitOption) *submitOptions {
	config := &submitOptions{
		priority: PriorityNormal,
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// ==========================================
// 类型定义
// ==========================================

// Priority 任务优先级
type Priority int

const (
	PriorityCritical Priority = iota
	PriorityHigh
	PriorityNormal
	PriorityLow
)

// String 返回优先级字符串
func (p Priority) String() string {
	switch p {
	case PriorityCritical:
		return "critical"
	case PriorityHigh:
		return "high"
	case PriorityNormal:
		return "normal"
	case PriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// ==========================================
// 错误别名（从 pkg/errors 导入）
// ==========================================

var (
	// ErrPoolClosed 协程池已关闭
	ErrPoolClosed = errors.ErrPoolClosed
	// ErrPoolFull 协程池已满
	ErrPoolFull = errors.ErrPoolFull
	// ErrTaskArgLengthMismatch 任务和参数长度不匹配
	ErrTaskArgLengthMismatch = errors.ErrTaskArgLengthMismatch
	// ErrTaskCanceled 任务已取消
	ErrTaskCanceled = errors.ErrTaskCanceled
	// ErrTaskTimeout 任务超时
	ErrTaskTimeout = errors.ErrTaskTimeout
	// ErrTooManyDelayedTasks 延迟任务数已达上限
	ErrTooManyDelayedTasks = errors.ErrTooManyDelayedTasks
)

// ==========================================
// 统计和健康状态
// ==========================================

// PoolStats 协程池统计信息
type PoolStats struct {
	Total      int
	ByPriority map[Priority]int
	Running    int
	Waiting    int
	Capacity   int
}

// HealthStatus 健康状态
type HealthStatus int

const (
	HealthStatusHealthy HealthStatus = iota
	HealthStatusUnhealthy
)

// String 返回健康状态字符串
func (s HealthStatus) String() string {
	switch s {
	case HealthStatusHealthy:
		return "healthy"
	case HealthStatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}
