// Package event 提供领域事件测试
package event

import (
	"errors"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ==========================================
// 任务事件测试
// ==========================================

func TestTaskSubmittedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := TaskSubmittedEvent{
		TaskID:    "task-001",
		Priority:  model.TaskPriorityHigh,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", evt.TaskID)
	assert.Equal(t, model.TaskPriorityHigh, evt.Priority)
	assert.Equal(t, now, evt.Timestamp)
}

func TestTaskSubmittedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := TaskSubmittedEvent{
		TaskID:    "task-001",
		Priority:  model.TaskPriorityHigh,
		Timestamp: now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "task-001", evt.AggregateID())
	assert.Equal(t, "task.submitted", evt.EventType())
}

func TestTaskCompletedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := TaskCompletedEvent{
		TaskID:    "task-001",
		Duration:  100 * time.Millisecond,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", evt.TaskID)
	assert.Equal(t, 100*time.Millisecond, evt.Duration)
	assert.Equal(t, now, evt.Timestamp)
}

func TestTaskCompletedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := TaskCompletedEvent{
		TaskID:    "task-001",
		Duration:  100 * time.Millisecond,
		Timestamp: now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "task-001", evt.AggregateID())
	assert.Equal(t, "task.completed", evt.EventType())
}

func TestTaskFailedEvent_Fields(t *testing.T) {
	now := time.Now()
	testErr := errors.New("test error")
	evt := TaskFailedEvent{
		TaskID:    "task-001",
		Error:     testErr,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", evt.TaskID)
	assert.Equal(t, testErr, evt.Error)
	assert.Equal(t, now, evt.Timestamp)
}

func TestTaskFailedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := TaskFailedEvent{
		TaskID:    "task-001",
		Error:     errors.New("test error"),
		Timestamp: now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "task-001", evt.AggregateID())
	assert.Equal(t, "task.failed", evt.EventType())
}

func TestTaskCanceledEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := TaskCanceledEvent{
		TaskID:    "task-001",
		Reason:    "user requested",
		Timestamp: now,
	}

	assert.Equal(t, "task-001", evt.TaskID)
	assert.Equal(t, "user requested", evt.Reason)
	assert.Equal(t, now, evt.Timestamp)
}

func TestTaskCanceledEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := TaskCanceledEvent{
		TaskID:    "task-001",
		Reason:    "user requested",
		Timestamp: now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "task-001", evt.AggregateID())
	assert.Equal(t, "task.canceled", evt.EventType())
}

func TestTaskRetryEvent_Fields(t *testing.T) {
	now := time.Now()
	testErr := errors.New("temporary error")
	evt := TaskRetryEvent{
		TaskID:      "task-001",
		Attempt:     2,
		MaxAttempts: 3,
		Error:       testErr,
		Timestamp:   now,
	}

	assert.Equal(t, "task-001", evt.TaskID)
	assert.Equal(t, 2, evt.Attempt)
	assert.Equal(t, 3, evt.MaxAttempts)
	assert.Equal(t, testErr, evt.Error)
	assert.Equal(t, now, evt.Timestamp)
}

func TestTaskRetryEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := TaskRetryEvent{
		TaskID:      "task-001",
		Attempt:     2,
		MaxAttempts: 3,
		Error:       errors.New("temporary error"),
		Timestamp:   now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "task-001", evt.AggregateID())
	assert.Equal(t, "task.retry", evt.EventType())
}

// ==========================================
// 队列事件测试
// ==========================================

func TestQueueFullEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := QueueFullEvent{
		CoreID:      2,
		QueueLength: 100,
		Strategy:    "block",
		Timestamp:   now,
	}

	assert.Equal(t, 2, evt.CoreID)
	assert.Equal(t, 100, evt.QueueLength)
	assert.Equal(t, "block", evt.Strategy)
	assert.Equal(t, now, evt.Timestamp)
}

func TestQueueFullEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := QueueFullEvent{
		CoreID:      2,
		QueueLength: 100,
		Strategy:    "block",
		Timestamp:   now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "", evt.AggregateID()) // 队列事件没有聚合ID
	assert.Equal(t, "queue.full", evt.EventType())
}

func TestQueueDrainedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := QueueDrainedEvent{
		CoreID:       2,
		QueueLength:  10,
		DrainedCount: 90,
		Timestamp:    now,
	}

	assert.Equal(t, 2, evt.CoreID)
	assert.Equal(t, 10, evt.QueueLength)
	assert.Equal(t, 90, evt.DrainedCount)
	assert.Equal(t, now, evt.Timestamp)
}

func TestQueueDrainedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := QueueDrainedEvent{
		CoreID:       2,
		QueueLength:  10,
		DrainedCount: 90,
		Timestamp:    now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "", evt.AggregateID())
	assert.Equal(t, "queue.drained", evt.EventType())
}

// ==========================================
// 执行器事件测试
// ==========================================

func TestExecutorStartedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := ExecutorStartedEvent{
		ExecutorID: "executor-001",
		Capacity:   100,
		Timestamp:  now,
	}

	assert.Equal(t, "executor-001", evt.ExecutorID)
	assert.Equal(t, 100, evt.Capacity)
	assert.Equal(t, now, evt.Timestamp)
}

func TestExecutorStartedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := ExecutorStartedEvent{
		ExecutorID: "executor-001",
		Capacity:   100,
		Timestamp:  now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "executor-001", evt.AggregateID())
	assert.Equal(t, "executor.started", evt.EventType())
}

func TestExecutorStoppedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := ExecutorStoppedEvent{
		ExecutorID:  "executor-001",
		Graceful:    true,
		PendingTask: 5,
		Timestamp:   now,
	}

	assert.Equal(t, "executor-001", evt.ExecutorID)
	assert.True(t, evt.Graceful)
	assert.Equal(t, 5, evt.PendingTask)
	assert.Equal(t, now, evt.Timestamp)
}

func TestExecutorStoppedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := ExecutorStoppedEvent{
		ExecutorID:  "executor-001",
		Graceful:    false,
		PendingTask: 10,
		Timestamp:   now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "executor-001", evt.AggregateID())
	assert.Equal(t, "executor.stopped", evt.EventType())
}

func TestExecutorCapacityChangedEvent_Fields(t *testing.T) {
	now := time.Now()
	evt := ExecutorCapacityChangedEvent{
		ExecutorID:  "executor-001",
		OldCapacity: 100,
		NewCapacity: 200,
		Timestamp:   now,
	}

	assert.Equal(t, "executor-001", evt.ExecutorID)
	assert.Equal(t, 100, evt.OldCapacity)
	assert.Equal(t, 200, evt.NewCapacity)
	assert.Equal(t, now, evt.Timestamp)
}

func TestExecutorCapacityChangedEvent_DomainEvent(t *testing.T) {
	now := time.Now()
	evt := ExecutorCapacityChangedEvent{
		ExecutorID:  "executor-001",
		OldCapacity: 100,
		NewCapacity: 200,
		Timestamp:   now,
	}

	assert.Equal(t, now, evt.OccurredAt())
	assert.Equal(t, "executor-001", evt.AggregateID())
	assert.Equal(t, "executor.capacity_changed", evt.EventType())
}

// ==========================================
// DomainEvent 接口测试
// ==========================================

func TestDomainEvent_Interface(t *testing.T) {
	// 确保所有事件都实现了 DomainEvent 接口
	var _ DomainEvent = TaskSubmittedEvent{}
	var _ DomainEvent = TaskCompletedEvent{}
	var _ DomainEvent = TaskFailedEvent{}
	var _ DomainEvent = TaskCanceledEvent{}
	var _ DomainEvent = TaskRetryEvent{}
	var _ DomainEvent = QueueFullEvent{}
	var _ DomainEvent = QueueDrainedEvent{}
	var _ DomainEvent = ExecutorStartedEvent{}
	var _ DomainEvent = ExecutorStoppedEvent{}
	var _ DomainEvent = ExecutorCapacityChangedEvent{}
}
