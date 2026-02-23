// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 类型别名（向后兼容）
// ==========================================

// GoroutineProvider 协程池提供者接口（类型别名）
// 实际接口定义在 domain/service/concurrency.go
type GoroutineProvider = service.GoroutineProvider

// GoroutineSubmitOption 提交选项（类型别名）
type GoroutineSubmitOption = service.GoroutineSubmitOption

// GoroutinePriority 任务优先级（类型别名）
type GoroutinePriority = service.GoroutinePriority

// GoroutinePoolStats 协程池统计信息（类型别名）
type GoroutinePoolStats = service.GoroutinePoolStats

// GoroutineHealthStatus 健康状态（类型别名）
type GoroutineHealthStatus = service.GoroutineHealthStatus

// GoroutineResult 异步任务结果接口（类型别名）
type GoroutineResult[T any] = service.GoroutineResult[T]

// ==========================================
// 优先级常量（向后兼容）
// ==========================================

const (
	// PriorityCritical 关键优先级
	PriorityCritical = service.GoroutinePriorityCritical
	// PriorityHigh 高优先级
	PriorityHigh = service.GoroutinePriorityHigh
	// PriorityNormal 正常优先级
	PriorityNormal = service.GoroutinePriorityNormal
	// PriorityLow 低优先级
	PriorityLow = service.GoroutinePriorityLow
)

// ==========================================
// 健康状态常量（向后兼容）
// ==========================================

const (
	// HealthStatusHealthy 健康
	HealthStatusHealthy = service.GoroutineHealthStatusHealthy
	// HealthStatusUnhealthy 不健康
	HealthStatusUnhealthy = service.GoroutineHealthStatusUnhealthy
)

// ==========================================
// 选项模式定义（向后兼容）
// ==========================================

// SubmitOptions 提交选项配置（向后兼容）
// 建议使用 domain/service.GoroutineSubmitOptions
type SubmitOptions = service.GoroutineSubmitOptions

// WithPriority 设置优先级（向后兼容）
// 建议使用 domain/service.WithGoroutinePriority
func WithPriority(priority GoroutinePriority) GoroutineSubmitOption {
	return service.WithGoroutinePriority(priority)
}

// WithDelay 设置延迟（向后兼容）
// 建议使用 domain/service.WithGoroutineDelay
func WithDelay(delay time.Duration) GoroutineSubmitOption {
	return service.WithGoroutineDelay(delay)
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
