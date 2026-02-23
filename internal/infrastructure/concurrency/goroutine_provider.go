// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"time"
)

// ==========================================
// GoroutineProvider 接口定义
// ==========================================

// GoroutineProvider 协程池提供者接口
// 混合设计：简洁方法 + 选项模式（架构师评审通过 v2.7）
type GoroutineProvider interface {
	// ======================================
	// 基础方法（简单场景，快速上手）
	// ======================================

	// Submit 简单任务：无参数，无返回值
	Submit(ctx context.Context, task func(context.Context)) error

	// SubmitWithArg 带参数：避免闭包陷阱
	SubmitWithArg[T any](ctx context.Context, task func(context.Context, T), arg T) error

	// SubmitWithResult 带返回值：需要异步结果
	SubmitWithResult[T any](ctx context.Context, task func(context.Context) (T, error)) Result[T]

	// SubmitWithArgAndResult 带参数和返回值：完整功能
	SubmitWithArgAndResult[T any, R any](
		ctx context.Context,
		task func(context.Context, T) (R, error),
		arg T,
	) Result[R]

	// ======================================
	// 快捷方法（高频需求，意图明确）
	// ======================================

	// SubmitWithPriority 优先级任务
	SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error

	// SubmitDelayed 延迟任务
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

	// ======================================
	// 高级方法（复杂场景，选项模式）
	// ======================================

	// SubmitAdvanced 灵活组合：优先级 + 延迟 + 未来扩展
	SubmitAdvanced[T any, R any](
		ctx context.Context,
		task func(context.Context, T) (R, error),
		arg T,
		opts ...SubmitOption,
	) Result[R]

	// ======================================
	// 批量方法（语义清晰，单独列出）
	// ======================================

	// SubmitBatch 批量提交：快速执行多个任务（无参数，无返回值）
	SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

	// SubmitBatchWithArg 批量提交：快速执行多个任务（带参数，无返回值）
	SubmitBatchWithArg[T any](
		ctx context.Context,
		tasks []func(context.Context, T),
		args []T,
	) error

	// SubmitBatchAllErrors 批量提交：收集所有错误（无参数）
	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error

	// SubmitBatchWithArgAllErrors 批量提交：收集所有错误（带参数）
	SubmitBatchWithArgAllErrors[T any](
		ctx context.Context,
		tasks []func(context.Context, T),
		args []T,
	) []error

	// SubmitBatchWithResult 批量提交：带返回值（无参数）
	SubmitBatchWithResult[R any](
		ctx context.Context,
		tasks []func(context.Context) (R, error),
	) []Result[R]

	// SubmitBatchWithArgAndResult 批量提交：带参数和返回值
	SubmitBatchWithArgAndResult[T any, R any](
		ctx context.Context,
		tasks []func(context.Context, T) (R, error),
		args []T,
	) []Result[R]

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

// Result[T] 异步执行结果
type Result[T any] struct {
	Value T
	Err   error
}

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
