// Package event 定义领域事件
//
// 领域事件是领域中发生的业务事实，用于：
// - 表达业务规则和约束
// - 触发跨聚合的业务流程
// - 事件溯源和审计日志
package event

import (
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 任务领域事件
// ==========================================

// TaskSubmittedEvent 任务提交事件
// 当任务被成功提交到执行器时触发
type TaskSubmittedEvent struct {
	TaskID    string
	Priority  model.TaskPriority
	Timestamp time.Time
}

// TaskCompletedEvent 任务完成事件
// 当任务成功执行完成时触发
type TaskCompletedEvent struct {
	TaskID    string
	Duration  time.Duration
	Timestamp time.Time
}

// TaskFailedEvent 任务失败事件
// 当任务执行失败时触发
type TaskFailedEvent struct {
	TaskID    string
	Error     error
	Timestamp time.Time
}

// TaskCanceledEvent 任务取消事件
// 当任务被取消时触发
type TaskCanceledEvent struct {
	TaskID    string
	Reason    string
	Timestamp time.Time
}

// TaskRetryEvent 任务重试事件
// 当任务执行失败并准备重试时触发
type TaskRetryEvent struct {
	TaskID      string
	Attempt     int
	MaxAttempts int
	Error       error
	Timestamp   time.Time
}

// ==========================================
// 队列领域事件
// ==========================================

// QueueFullEvent 队列满事件
// 当任务队列达到容量上限时触发，用于背压控制
type QueueFullEvent struct {
	CoreID      int
	QueueLength int
	Strategy    string // 触发的背压策略 (drop/block/retry)
	Timestamp   time.Time
}

// QueueDrainedEvent 队列排空事件
// 当队列从满状态恢复到正常状态时触发
type QueueDrainedEvent struct {
	CoreID       int
	QueueLength  int
	DrainedCount int
	Timestamp    time.Time
}

// ==========================================
// 执行器领域事件
// ==========================================

// ExecutorStartedEvent 执行器启动事件
type ExecutorStartedEvent struct {
	ExecutorID string
	Capacity   int
	Timestamp  time.Time
}

// ExecutorStoppedEvent 执行器停止事件
type ExecutorStoppedEvent struct {
	ExecutorID  string
	Graceful    bool
	PendingTask int
	Timestamp   time.Time
}

// ExecutorCapacityChangedEvent 执行器容量变更事件
type ExecutorCapacityChangedEvent struct {
	ExecutorID  string
	OldCapacity int
	NewCapacity int
	Timestamp   time.Time
}
