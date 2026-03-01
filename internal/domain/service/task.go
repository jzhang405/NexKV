// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 类型别名
// ==========================================

// TaskPriority 任务优先级
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
// 核心接口（4个）
// ==========================================

// TaskExecutor 基础任务执行器接口
type TaskExecutor interface {
	// Submit 提交任务执行
	Submit(ctx context.Context, priority TaskPriority, task func(context.Context)) error
	// Close 关闭执行器
	Close() error
}

// PriorityExecutor 优先级执行器
// TaskExecutor 已包含 priority 参数，此接口用于类型标识
type PriorityExecutor interface {
	TaskExecutor
}

// TaskScheduler 任务调度器（延迟任务）
type TaskScheduler interface {
	// SubmitDelayed 延迟提交任务（支持优先级）
	SubmitDelayed(ctx context.Context, delay time.Duration, priority TaskPriority, task func(context.Context)) error
}

// ==========================================
// 监控和管理接口（分离职责）
// ==========================================

// Observable 可观测接口（只读查询）
type Observable interface {
	// Stats 获取统计信息
	Stats() TaskPoolStats
	// Health 获取健康状态
	Health() TaskHealthStatus
}

// Manageable 可管理接口（读写操作）
type Manageable interface {
	// SetCapacity 设置容量
	SetCapacity(capacity int) error
	// CloseWithTimeout 带超时关闭
	CloseWithTimeout(timeout time.Duration) error
}

// ==========================================
// 组合接口
// ==========================================

// AsyncTaskExecutor 异步任务执行器
// 组合：TaskExecutor + TaskScheduler + Observable + Manageable
type AsyncTaskExecutor interface {
	TaskExecutor
	TaskScheduler
	Observable
	Manageable
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
