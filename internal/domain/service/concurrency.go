// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// GoroutinePriority 任务优先级（类型别名）
// 实际类型定义在 domain/model/goroutine.go
type GoroutinePriority = model.GoroutinePriority

// GoroutinePoolStats 协程池统计信息（类型别名）
type GoroutinePoolStats = model.GoroutinePoolStats

// GoroutineHealthStatus 健康状态（类型别名）
type GoroutineHealthStatus = model.GoroutineHealthStatus

// 常量别名（向后兼容）
const (
	GoroutinePriorityCritical = model.GoroutinePriorityCritical
	GoroutinePriorityHigh     = model.GoroutinePriorityHigh
	GoroutinePriorityNormal   = model.GoroutinePriorityNormal
	GoroutinePriorityLow      = model.GoroutinePriorityLow
)

// ==========================================
// GoroutineProvider 协程池提供者接口
// ==========================================

// GoroutineProvider 协程池提供者接口
// 注意：由于 Go 接口限制，接口方法不能有类型参数
// 泛型方法通过独立辅助函数提供（由基础设施层实现）
type GoroutineProvider interface {
	// ======================================
	// 基础方法（简单场景，快速上手）
	// ======================================

	// Submit 简单任务：无参数，无返回值
	Submit(ctx context.Context, task func(context.Context)) error

	// SubmitWithArg 带参数任务：避免闭包陷阱（使用 any 类型）
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error

	// SubmitWithResult 带返回值任务（使用 any 类型）
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) GoroutineResult[any]

	// SubmitWithArgAndResult 带参数和返回值任务（使用 any 类型）
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) GoroutineResult[any]

	// SubmitWithPriority 优先级任务
	SubmitWithPriority(ctx context.Context, priority GoroutinePriority, task func(context.Context)) error

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
		opts ...GoroutineSubmitOption,
	) GoroutineResult[any]

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
	SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []GoroutineResult[any]

	// ======================================
	// 管理方法
	// ======================================

	Stats() GoroutinePoolStats
	Health() GoroutineHealthStatus
	SetCapacity(capacity int) error
	Close() error
	CloseWithTimeout(timeout time.Duration) error
}

// ==========================================
// 选项模式定义（用于 SubmitAdvanced）
// ==========================================

// GoroutineSubmitOptions 提交选项配置
type GoroutineSubmitOptions struct {
	Priority GoroutinePriority
	Delay    time.Duration
}

// GoroutineSubmitOption 提交选项
type GoroutineSubmitOption func(*GoroutineSubmitOptions)

// WithGoroutinePriority 设置优先级
func WithGoroutinePriority(priority GoroutinePriority) GoroutineSubmitOption {
	return func(opts *GoroutineSubmitOptions) {
		opts.Priority = priority
	}
}

// WithGoroutineDelay 设置延迟
func WithGoroutineDelay(delay time.Duration) GoroutineSubmitOption {
	return func(opts *GoroutineSubmitOptions) {
		opts.Delay = delay
	}
}

// ==========================================
// Result 类型
// ==========================================

// GoroutineResult 异步任务结果接口
type GoroutineResult[T any] interface {
	// Get 获取结果（阻塞等待）
	Get(ctx context.Context) (T, error)
	// IsDone 检查是否已完成
	IsDone() bool
}
