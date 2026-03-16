// Package concurrency 并发控制和任务调度机制测试
package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// TaskStatus 测试
// ==========================================

func TestTaskStatus_String(t *testing.T) {
	tests := []struct {
		status   TaskStatus
		expected string
	}{
		{TaskQueued, "queued"},
		{TaskExecuting, "executing"},
		{TaskPassed, "passed"},
		{TaskFailed, "failed"},
		{TaskRetrying, "retrying"},
		{TaskTimeout, "timeout"},
		{TaskStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

// ==========================================
// TaskScheduler 基础测试
// ==========================================

func TestNewTaskScheduler(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")

	assert.NotNil(t, scheduler)
	assert.Equal(t, "test-scheduler", scheduler.name)
	assert.False(t, scheduler.running.Load())
	assert.NotNil(t, scheduler.cond)
	assert.NotNil(t, scheduler.ctx)
	assert.NotNil(t, scheduler.cancel)
}

// ==========================================
// 任务注册测试
// ==========================================

func TestTaskScheduler_RegisterTask(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	err := scheduler.RegisterTask(task, 1)
	assert.NoError(t, err)

	// 验证任务已注册
	scheduler.mu.RLock()
	assert.Len(t, scheduler.tasks, 1)
	assert.Contains(t, scheduler.taskMap, "test-task")
	scheduler.mu.RUnlock()

	// 重复注册应该失败（使用不同的 ExecutionOrder 来测试任务名重复）
	err = scheduler.RegisterTask(task, 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestTaskScheduler_UnregisterTask(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	// 注册任务
	err := scheduler.RegisterTask(task, 1)
	require.NoError(t, err)

	// 注销任务
	err = scheduler.UnregisterTask("test-task")
	assert.NoError(t, err)

	// 验证任务已注销
	scheduler.mu.RLock()
	assert.Len(t, scheduler.tasks, 0)
	assert.NotContains(t, scheduler.taskMap, "test-task")
	scheduler.mu.RUnlock()

	// 注销不存在的任务应该失败
	err = scheduler.UnregisterTask("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ==========================================
// SchedulerBaseTask 测试
// ==========================================

func TestNewSchedulerBaseTask(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityHigh, 1)

	assert.NotNil(t, task)
	assert.Equal(t, "test-task", task.name)
	assert.Equal(t, model.TaskPriorityHigh, task.priority)
	assert.NotNil(t, task.BaseTask)
	assert.NotNil(t, task.queue)
	assert.Empty(t, task.queue)
	assert.Equal(t, TaskQueued, task.GetTaskStatus())
}

// ==========================================
// Task 接口测试
// ==========================================

func TestSchedulerBaseTask_QueueLen(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	assert.Equal(t, 0, task.QueueLen())

	task.mu.Lock()
	task.queue = append(task.queue, "item1", "item2", "item3")
	task.mu.Unlock()

	assert.Equal(t, 3, task.QueueLen())
}

func TestSchedulerBaseTask_Enqueue(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	err := task.Enqueue("item1")
	assert.NoError(t, err)
	assert.Equal(t, 1, task.QueueLen())
	assert.Equal(t, TaskQueued, task.GetTaskStatus())

	err = task.Enqueue("item2")
	assert.NoError(t, err)
	assert.Equal(t, 2, task.QueueLen())
}

func TestSchedulerBaseTask_Peek(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	// 空队列 Peek 应该返回 false
	var item any
	assert.False(t, task.Peek(&item))

	// 入队一个元素
	err := task.Enqueue("item1")
	require.NoError(t, err)

	// Peek 应该返回队首元素
	assert.True(t, task.Peek(&item))
	assert.Equal(t, "item1", item)
	assert.Equal(t, TaskExecuting, task.GetTaskStatus())
	assert.Equal(t, 1, task.QueueLen()) // 元素仍在队列
}

func TestSchedulerBaseTask_Dequeue(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	// 空队列 Dequeue 应该返回 false
	var item any
	assert.False(t, task.Dequeue(&item))

	// 入队元素
	err := task.Enqueue("item1")
	require.NoError(t, err)
	err = task.Enqueue("item2")
	require.NoError(t, err)

	// Dequeue 应该移除队首元素
	assert.True(t, task.Dequeue(&item))
	assert.Equal(t, "item1", item)
	assert.Equal(t, 1, task.QueueLen()) // 队列剩一个元素

	// 再次 Dequeue
	assert.True(t, task.Dequeue(&item))
	assert.Equal(t, "item2", item)
	assert.Equal(t, 0, task.QueueLen())
}

// ==========================================
// 优先级调度测试
// ==========================================

func TestTaskScheduler_ExecutionOrderScheduling(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")

	// 创建不同 ExecutionOrder 的任务
	firstTask := NewSchedulerBaseTask("first-task", model.TaskPriorityNormal, 0)
	secondTask := NewSchedulerBaseTask("second-task", model.TaskPriorityNormal, 0)
	thirdTask := NewSchedulerBaseTask("third-task", model.TaskPriorityNormal, 0)

	err := scheduler.RegisterTask(firstTask, 1)
	require.NoError(t, err)
	err = scheduler.RegisterTask(secondTask, 2)
	require.NoError(t, err)
	err = scheduler.RegisterTask(thirdTask, 3)
	require.NoError(t, err)

	// 验证按 ExecutionOrder 排序（不影响注册顺序）
	tasks := scheduler.getOrderedTasks()
	assert.Equal(t, 3, len(tasks))
	assert.Equal(t, "first-task", tasks[0].Name())  // ExecutionOrder 1
	assert.Equal(t, "second-task", tasks[1].Name()) // ExecutionOrder 2
	assert.Equal(t, "third-task", tasks[2].Name())  // ExecutionOrder 3
}

// ==========================================
// Peek + Execute + Dequeue 三阶段测试
// ==========================================

func TestSchedulerBaseTask_PeekExecuteDequeue(t *testing.T) {
	// 创建一个自定义任务，控制执行结果
	task := NewTestTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		data := item.(string)
		if data == "fail-retry" {
			return TaskRetrying // 需要重试
		}
		return TaskPassed // 成功
	})

	scheduler := NewTaskScheduler("test-scheduler")
	err := scheduler.RegisterTask(task, 1)
	require.NoError(t, err)

	// 测试成功场景
	err = task.Enqueue("success")
	require.NoError(t, err)

	var item any
	assert.True(t, task.Peek(&item))
	assert.Equal(t, "success", item)

	status := task.Execute(item)
	assert.Equal(t, TaskPassed, status)

	var dequeued any
	assert.True(t, task.Dequeue(&dequeued))
	assert.Equal(t, "success", dequeued)
	assert.Equal(t, 0, task.QueueLen()) // 队列已空

	// 测试重试场景
	err = task.Enqueue("fail-retry")
	require.NoError(t, err)

	assert.True(t, task.Peek(&item))
	assert.Equal(t, "fail-retry", item)

	status = task.Execute(item)
	assert.Equal(t, TaskRetrying, status)

	// Retrying 状态不应 Dequeue
	// item 仍在队列中，QueueLen 应该还是 1
	assert.Equal(t, 1, task.QueueLen())
}

// ==========================================
// 统计信息测试
// ==========================================

func TestTaskScheduler_Stats(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")

	// 获取统计信息
	stats := scheduler.GetStats()
	// TaskExecutions map 应该被初始化
	assert.Equal(t, 0, len(stats.TaskExecutions))
}

// ==========================================
// 并发安全测试
// ==========================================

func TestSchedulerBaseTask_ConcurrentEnqueue(t *testing.T) {
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	const goroutines = 100
	const itemsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				task.Enqueue(id*itemsPerGoroutine + j)
			}
		}(i)
	}

	wg.Wait()

	// 验证所有元素都已入队
	assert.Equal(t, goroutines*itemsPerGoroutine, task.QueueLen())
}

// ==========================================
// wakeup 机制测试
// ==========================================

func TestTaskScheduler_WakeupMechanism(t *testing.T) {
	scheduler := NewTaskScheduler("test-scheduler")
	task := NewSchedulerBaseTask("test-task", model.TaskPriorityNormal, 1)

	err := scheduler.RegisterTask(task, 1)
	require.NoError(t, err)

	// 测试 Enqueue 时自动 wakeup
	// 注意：这个测试不启动调度器循环，只测试 wakeup 调用本身
	// 在实际运行中，wakeup 会唤醒 cond.Wait()

	err = task.Enqueue("item1")
	assert.NoError(t, err)
	assert.Equal(t, 1, task.QueueLen())
}

// ==========================================
// 辅助函数和类型
// ==========================================

// TestTask 测试任务（可以自定义 Execute 行为）
type TestTask struct {
	*SchedulerBaseTask
	executeFunc func(any) TaskStatus
}

// NewTestTask 创建测试任务
func NewTestTask(name string, priority model.TaskPriority, executionOrder int, executeFunc func(any) TaskStatus) *TestTask {
	base := NewSchedulerBaseTask(name, priority, executionOrder)
	return &TestTask{
		SchedulerBaseTask: base,
		executeFunc:       executeFunc,
	}
}

// Execute 重写执行方法
func (t *TestTask) Execute(item any) TaskStatus {
	if t.executeFunc != nil {
		return t.executeFunc(item)
	}
	return TaskPassed
}

// ==========================================
// 集成测试
// ==========================================

func TestTaskScheduler_Integration(t *testing.T) {
	scheduler := NewTaskScheduler("integration-test")

	// 创建多个任务
	var processedCount atomic.Int64

	task1 := NewTestTask("task1", model.TaskPriorityHigh, 1, func(item any) TaskStatus {
		processedCount.Add(1)
		return TaskPassed
	})

	task2 := NewTestTask("task2", model.TaskPriorityNormal, 2, func(item any) TaskStatus {
		processedCount.Add(1)
		return TaskPassed
	})

	err := scheduler.RegisterTask(task1, 1)
	require.NoError(t, err)
	err = scheduler.RegisterTask(task2, 2)
	require.NoError(t, err)

	// 入队任务
	for i := 0; i < 10; i++ {
		task1.Enqueue(i)
		task2.Enqueue(i)
	}

	// 验证任务已入队
	assert.Equal(t, 10, task1.QueueLen())
	assert.Equal(t, 10, task2.QueueLen())
}

// ==========================================
// 边界情况测试
// ==========================================

func TestTaskScheduler_EmptyTasks(t *testing.T) {
	scheduler := NewTaskScheduler("empty-test")

	tasks := scheduler.getOrderedTasks()
	assert.Empty(t, tasks)
}

func TestSchedulerBaseTask_EmptyQueue(t *testing.T) {
	task := NewSchedulerBaseTask("empty-task", model.TaskPriorityNormal, 1)

	var item any
	assert.False(t, task.Peek(&item))
	assert.False(t, task.Dequeue(&item))
	assert.Equal(t, 0, task.QueueLen())
}

func TestTaskScheduler_ContextCancellation(t *testing.T) {
	scheduler := NewTaskScheduler("cancel-test")

	// 取消上下文
	scheduler.cancel()

	// 验证上下文已取消
	select {
	case <-scheduler.ctx.Done():
		// 上下文已取消
	default:
		t.Fatal("context should be cancelled")
	}
}
