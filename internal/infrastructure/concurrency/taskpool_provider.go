// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 类型导出（便捷访问）
// ==========================================

// TaskPoolProvider 任务池提供者接口
type TaskPoolProvider = service.ExecutorManager

// TaskSubmitOption 提交选项
type TaskSubmitOption = service.TaskSubmitOption

// TaskPriority 任务优先级
type TaskPriority = model.TaskPriority

// TaskPoolStats 任务池统计信息
type TaskPoolStats = service.TaskPoolStats

// TaskHealthStatus 健康状态
type TaskHealthStatus = model.TaskHealthStatus

// TaskResult 异步任务结果接口
type TaskResult[T any] = service.TaskResult[T]

// ==========================================
// 优先级常量（Unix 传统：0 最高，9 最低）
// ==========================================

const (
	PriorityCritical   = model.TaskPriorityCritical   // 0
	PriorityHigh       = model.TaskPriorityHigh       // 1
	PriorityUrgent     = model.TaskPriorityUrgent     // 2
	PriorityImportant  = model.TaskPriorityImportant  // 3
	PriorityNormalHigh = model.TaskPriorityNormalHigh // 4
	PriorityNormal     = model.TaskPriorityNormal     // 5
	PriorityNormalLow  = model.TaskPriorityNormalLow  // 6
	PriorityLow        = model.TaskPriorityLow        // 7
	PriorityBackground = model.TaskPriorityBackground // 8
	PriorityIdle       = model.TaskPriorityIdle       // 9
)

// ==========================================
// 健康状态常量
// ==========================================

const (
	HealthStatusHealthy   = model.TaskHealthStatusHealthy
	HealthStatusUnhealthy = model.TaskHealthStatusUnhealthy
)

// ==========================================
// 选项模式定义
// ==========================================

// SubmitOptions 提交选项配置
type SubmitOptions = service.TaskSubmitOptions

// WithPriority 设置优先级
func WithPriority(priority TaskPriority) TaskSubmitOption {
	return service.WithTaskPriority(priority)
}

// WithDelay 设置延迟
func WithDelay(delay time.Duration) TaskSubmitOption {
	return service.WithTaskDelay(delay)
}

// ==========================================
// 错误导出（便捷访问）
// ==========================================

var (
	// ErrPoolClosed 任务池已关闭
	ErrPoolClosed = errors.ErrPoolClosed
	// ErrPoolFull 任务池已满
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
