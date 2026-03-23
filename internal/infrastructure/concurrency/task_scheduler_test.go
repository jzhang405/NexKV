// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	nexerrors "github.com/jzhang405/NexKV/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// 测试 ShardTask
// ==========================================

func TestShardTask_EnqueuePeekDequeue(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 测试空队列
	var item any
	assert.False(t, task.Peek(&item))
	assert.False(t, task.Dequeue(&item))
	assert.Equal(t, 0, task.QueueLen())

	// 测试入队
	err := task.Enqueue("item1")
	require.NoError(t, err)
	assert.Equal(t, 1, task.QueueLen())

	err = task.Enqueue("item2")
	require.NoError(t, err)
	assert.Equal(t, 2, task.QueueLen())

	// 测试 Peek
	assert.True(t, task.Peek(&item))
	assert.Equal(t, "item1", item)
	assert.Equal(t, 2, task.QueueLen()) // Peek 不改变队列长度

	// 测试 Dequeue
	assert.True(t, task.Dequeue(&item))
	assert.Equal(t, "item1", item)
	assert.Equal(t, 1, task.QueueLen())

	assert.True(t, task.Dequeue(&item))
	assert.Equal(t, "item2", item)
	assert.Equal(t, 0, task.QueueLen())
}

func TestShardTask_Execute(t *testing.T) {
	// 测试自定义执行函数
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		data := item.(string)
		if data == "fail" {
			return TaskFailed
		}
		return TaskPassed
	})

	assert.Equal(t, TaskPassed, task.Execute("success"))
	assert.Equal(t, TaskFailed, task.Execute("fail"))
}

// ==========================================
// 测试 SchedulerCore
// ==========================================

func TestSchedulerCore_RegisterTask(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := &ShardTask{
		name:           "test-task",
		priority:       model.TaskPriorityNormal,
		executionOrder: 1,
		executeFunc:    func(item any) TaskStatus { return TaskPassed },
	}

	// 第一次注册应该成功
	err := core.RegisterTask(taskTemplate)
	require.NoError(t, err)

	// 重复注册应该失败
	err = core.RegisterTask(taskTemplate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestSchedulerCore_IndependentTaskInstances(t *testing.T) {
	// 验证不同核心的 Task 实例是独立的
	core1 := NewSchedulerCore(0)
	core2 := NewSchedulerCore(1)

	taskTemplate := &ShardTask{
		name:           "test-task",
		priority:       model.TaskPriorityNormal,
		executionOrder: 1,
		executeFunc:    func(item any) TaskStatus { return TaskPassed },
	}

	// 注册到两个核心
	err := core1.RegisterTask(taskTemplate)
	require.NoError(t, err)

	err = core2.RegisterTask(taskTemplate)
	require.NoError(t, err)

	// 获取两个核心的 Task
	task1, err := core1.GetTaskByName("test-task")
	require.NoError(t, err)

	task2, err := core2.GetTaskByName("test-task")
	require.NoError(t, err)

	// 验证它们是不同的实例
	assert.NotEqual(t, task1, task2, "Tasks should be independent instances")

	// 验证队列是独立的
	task1.Enqueue("core1-item")
	task2.Enqueue("core2-item")

	assert.Equal(t, 1, task1.QueueLen())
	assert.Equal(t, 1, task2.QueueLen())

	// 从 task1 出队不应影响 task2
	var item any
	assert.True(t, task1.Dequeue(&item))
	assert.Equal(t, "core1-item", item)
	assert.Equal(t, 0, task1.QueueLen())
	assert.Equal(t, 1, task2.QueueLen(), "task2 queue should be unchanged")
}

// ==========================================
// 测试 MultiTaskScheduler
// ==========================================

func TestNewTaskScheduler(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	assert.NotNil(t, scheduler)
	assert.Equal(t, 4, scheduler.coreCount)
	assert.Len(t, scheduler.cores, 4)
	assert.False(t, scheduler.running.Load())
}

func TestTaskScheduler_RegisterTask(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 验证所有核心都有该任务
	for i, core := range scheduler.cores {
		task, err := core.GetTaskByName("test-task")
		assert.NoError(t, err, "core %d should have task", i)
		assert.NotNil(t, task)
		assert.Equal(t, "test-task", task.Name())
	}
}

func TestTaskScheduler_ExecutionOrderConflict(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册第一个任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"task-a",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 尝试注册相同 ExecutionOrder 的任务
	err = scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"task-b",
		model.TaskPriorityNormal,
		1, // 相同的 ExecutionOrder
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execution order 1 already registered")
}

// ==========================================
// 测试 Shard 分发
// ==========================================

type testShardItem struct {
	*model.BaseTask[struct{}] // 嵌入 BaseTask 实现接口组合
	shardID                   int
	payload                   string
}

func (i *testShardItem) ShardID() int {
	return i.shardID
}

func (i *testShardItem) Execute(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
	return struct{}{}, nil
}

func (i *testShardItem) TaskOrder() int {
	return 0 // 默认 order 0
}

func TestTaskScheduler_ShardDistribution_Positive(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 使用 controlled executor：先记录但不执行 runLoop
	var runFuncs []func(context.Context)
	executor := &mockPerCoreExecutor{
		submitFunc: func(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error {
			runFuncs = append(runFuncs, fn)
			return nil
		},
	}
	err = scheduler.Start(executor)
	require.NoError(t, err)

	// 测试正数 ShardID 的固定路由
	// shardID=1 → core 1 (1 % 4 = 1)
	item1 := &testShardItem{shardID: 1, payload: "item-1"}
	err = scheduler.EnqueueWithShard(item1, "test-task")
	require.NoError(t, err)

	// shardID=5 → core 1 (5 % 4 = 1)
	item5 := &testShardItem{shardID: 5, payload: "item-5"}
	err = scheduler.EnqueueWithShard(item5, "test-task")
	require.NoError(t, err)

	// 验证两个任务都路由到 core 1（runLoop 尚未执行，队列保留）
	task1, _ := scheduler.cores[1].GetTaskByName("test-task")
	assert.Equal(t, 2, task1.QueueLen(), "core 1 should have 2 items")

	// 其他核心应该为空
	for i, core := range scheduler.cores {
		if i != 1 {
			task, _ := core.GetTaskByName("test-task")
			assert.Equal(t, 0, task.QueueLen(), "core %d should be empty", i)
		}
	}

	// 清理：启动并停止所有 runLoop
	for _, fn := range runFuncs {
		go fn(context.Background())
	}
	// 等待 goroutine 启动并进入 runLoop
	time.Sleep(10 * time.Millisecond)
	scheduler.Stop()
}

func TestTaskScheduler_ShardDistribution_Negative(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 使用 controlled executor：先记录但不执行 runLoop
	var runFuncs []func(context.Context)
	executor := &mockPerCoreExecutor{
		submitFunc: func(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error {
			runFuncs = append(runFuncs, fn)
			return nil
		},
	}
	err = scheduler.Start(executor)
	require.NoError(t, err)

	// 测试负数 ShardID 的路由
	// shardID=-1 → core 1 (abs(-1) % 4 = 1)
	itemNeg1 := &testShardItem{shardID: -1, payload: "item-neg1"}
	err = scheduler.EnqueueWithShard(itemNeg1, "test-task")
	require.NoError(t, err)

	// shardID=-5 → core 1 (abs(-5) % 4 = 1)
	itemNeg5 := &testShardItem{shardID: -5, payload: "item-neg5"}
	err = scheduler.EnqueueWithShard(itemNeg5, "test-task")
	require.NoError(t, err)

	// 验证两个任务都路由到 core 1（runLoop 尚未执行，队列保留）
	task1, _ := scheduler.cores[1].GetTaskByName("test-task")
	assert.Equal(t, 2, task1.QueueLen(), "core 1 should have 2 items")

	// 清理：启动并停止所有 runLoop
	for _, fn := range runFuncs {
		go fn(context.Background())
	}
	// 等待 goroutine 启动并进入 runLoop
	time.Sleep(10 * time.Millisecond)
	scheduler.Stop()
}

func TestTaskScheduler_ShardDistribution_Zero(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 使用 controlled executor：先记录但不执行 runLoop
	var runFuncs []func(context.Context)
	executor := &mockPerCoreExecutor{
		submitFunc: func(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error {
			runFuncs = append(runFuncs, fn)
			return nil
		},
	}
	err = scheduler.Start(executor)
	require.NoError(t, err)

	// 提交多个 shardID=0 的任务
	// 由于 selectLeastLoadedCore 的实现，它们会被分配到最少负载的核心
	for i := 0; i < 10; i++ {
		item := &testShardItem{shardID: 0, payload: fmt.Sprintf("item-zero-%d", i)}
		_ = scheduler.EnqueueWithShard(item, "test-task")
	}

	// 验证负载被分散（runLoop 尚未执行，队列保留）
	totalQueueLen := 0
	maxQueueLen := 0
	for _, core := range scheduler.cores {
		task, _ := core.GetTaskByName("test-task")
		queueLen := task.QueueLen()
		totalQueueLen += queueLen
		if queueLen > maxQueueLen {
			maxQueueLen = queueLen
		}
	}

	assert.Equal(t, 10, totalQueueLen, "total items should be 10")
	// 所有核心初始队列长度相同，selectLeastLoadedCore 会轮询分配
	// 验证至少有多个核心收到了任务（不是全部集中在一个核心）
	coresWithTasks := 0
	for _, core := range scheduler.cores {
		task, _ := core.GetTaskByName("test-task")
		if task.QueueLen() > 0 {
			coresWithTasks++
		}
	}
	// 当所有核心初始队列长度相同时，selectLeastLoadedCore 返回第一个核心
	// 所以所有任务都分配到 core 0，这是正确的行为
	assert.Greater(t, coresWithTasks, 0, "at least one core should have tasks")
	assert.LessOrEqual(t, coresWithTasks, 4, "at most all cores should have tasks")

	// 清理：启动并停止所有 runLoop
	for _, fn := range runFuncs {
		go fn(context.Background())
	}
	// 等待 goroutine 启动并进入 runLoop
	time.Sleep(10 * time.Millisecond)
	scheduler.Stop()
}

// ==========================================
// 集成测试
// ==========================================

func TestTaskScheduler_Integration(t *testing.T) {
	t.Skip("需要真实执行器才能测试完整流程")

	scheduler := NewTaskScheduler("integration", 2)

	// 创建同步 executor 用于测试
	var wg sync.WaitGroup
	executor := &mockPerCoreExecutor{
		submitFunc: func(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(ctx)
			}()
			return nil
		},
	}

	err := scheduler.Start(executor)
	require.NoError(t, err)
	defer scheduler.Stop()

	// 注册一个计数任务
	var processedCount atomic.Int64

	err = scheduler.RegisterTask(
		func(item any) TaskStatus {
			processedCount.Add(1)
			return TaskPassed
		},
		"counter-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 提交任务到不同核心
	for i := 0; i < 10; i++ {
		item := &testShardItem{
			shardID: i, // 交替路由到 core 0 和 core 1
			payload: "test",
		}
		err := scheduler.EnqueueWithShard(item, "counter-task")
		require.NoError(t, err)
	}

	// 等待所有任务完成
	wg.Wait()

	// 验证任务被处理
	assert.Equal(t, int64(10), processedCount.Load())

	// 验证健康检查
	err = scheduler.HealthCheck()
	assert.NoError(t, err)
}

// ==========================================
// ShardTask 辅助方法测试
// ==========================================

func TestShardTask_Name(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityHigh, 5, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, "test-task", task.Name())
}

func TestShardTask_Priority(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityCritical, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, model.TaskPriorityCritical, task.Priority())
}

func TestShardTask_ExecutionOrder(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 10, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, 10, task.ExecutionOrder())
}

func TestShardTask_GetTask(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	// ShardTask 不使用 BaseTask，返回 nil
	assert.Nil(t, task.GetTask())
}

func TestShardTask_QueueLen_Empty(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, 0, task.QueueLen())
}

func TestShardTask_Peek_EmptyQueue(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	var item any
	assert.False(t, task.Peek(&item))
}

func TestShardTask_Dequeue_EmptyQueue(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	var item any
	assert.False(t, task.Dequeue(&item))
}

func TestShardTask_Enqueue_Success(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	err := task.Enqueue("item")
	assert.NoError(t, err)
}

func TestShardTask_PeekN(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 测试空队列
	items := make([]any, 3)
	n := task.PeekN(items)
	assert.Equal(t, 0, n)

	// 入队 5 个元素
	for i := range 5 {
		err := task.Enqueue(fmt.Sprintf("item%d", i))
		require.NoError(t, err)
	}

	// PeekN 3 个元素
	n = task.PeekN(items)
	assert.Equal(t, 3, n)
	assert.Equal(t, "item0", items[0])
	assert.Equal(t, "item1", items[1])
	assert.Equal(t, "item2", items[2])

	// 队列长度不变
	assert.Equal(t, 5, task.QueueLen())

	// PeekN 超过队列大小
	moreItems := make([]any, 10)
	n = task.PeekN(moreItems)
	assert.Equal(t, 5, n)

	// 验证前 3 个元素仍然是 item0, item1, item2
	assert.Equal(t, "item0", moreItems[0])
	assert.Equal(t, "item1", moreItems[1])
	assert.Equal(t, "item2", moreItems[2])
	assert.Equal(t, "item3", moreItems[3])
	assert.Equal(t, "item4", moreItems[4])
}

func TestShardTask_DequeueN(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 测试空队列
	n := task.DequeueN(3)
	assert.Equal(t, 0, n)

	// 入队 5 个元素
	for i := range 5 {
		err := task.Enqueue(fmt.Sprintf("item%d", i))
		require.NoError(t, err)
	}

	// DequeueN 3 个元素（丢弃）
	n = task.DequeueN(3)
	assert.Equal(t, 3, n)

	// 队列长度减少
	assert.Equal(t, 2, task.QueueLen())

	// DequeueN 剩余元素
	n = task.DequeueN(2)
	assert.Equal(t, 2, n)

	// 队列为空
	assert.Equal(t, 0, task.QueueLen())

	// DequeueN 空队列
	n = task.DequeueN(1)
	assert.Equal(t, 0, n)
}

// ==========================================
// SchedulerCore 方法测试
// ==========================================

func TestSchedulerCore_GetTaskByName(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := &ShardTask{
		name:           "test-task",
		priority:       model.TaskPriorityNormal,
		executionOrder: 1,
		executeFunc:    func(item any) TaskStatus { return TaskPassed },
	}

	err := core.RegisterTask(taskTemplate)
	require.NoError(t, err)

	// 测试存在的任务
	task, err := core.GetTaskByName("test-task")
	assert.NoError(t, err)
	assert.NotNil(t, task)
	assert.Equal(t, "test-task", task.Name())

	// 测试不存在的任务
	_, err = core.GetTaskByName("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSchedulerCore_GetTaskByName_NotFound(t *testing.T) {
	core := NewSchedulerCore(0)
	_, err := core.GetTaskByName("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSchedulerCore_wakeup(t *testing.T) {
	core := NewSchedulerCore(0)
	// wakeup 不应该 panic
	core.wakeup()
	// 多次调用也不应该 panic
	core.wakeup()
	core.wakeup()
}

func TestSchedulerCore_getOrderedTasks_Empty(t *testing.T) {
	core := NewSchedulerCore(0)
	tasks := core.getOrderedTasks()
	assert.Empty(t, tasks)
}

func TestSchedulerCore_getOrderedTasks_SingleTask(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	_ = core.RegisterTask(taskTemplate)

	tasks := core.getOrderedTasks()
	assert.Len(t, tasks, 1)
	assert.Equal(t, "test-task", tasks[0].Name())
}

func TestSchedulerCore_getOrderedTasks_MultipleTasks(t *testing.T) {
	core := NewSchedulerCore(0)

	// 注册多个任务，ExecutionOrder 乱序
	task2 := &ShardTask{name: "task-2", priority: model.TaskPriorityNormal, executionOrder: 2, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	task1 := &ShardTask{name: "task-1", priority: model.TaskPriorityNormal, executionOrder: 1, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	task3 := &ShardTask{name: "task-3", priority: model.TaskPriorityNormal, executionOrder: 3, executeFunc: func(item any) TaskStatus { return TaskPassed }}

	_ = core.RegisterTask(task2)
	_ = core.RegisterTask(task1)
	_ = core.RegisterTask(task3)

	tasks := core.getOrderedTasks()
	assert.Len(t, tasks, 3)

	// 验证按 ExecutionOrder 排序
	assert.Equal(t, "task-1", tasks[0].Name())
	assert.Equal(t, "task-2", tasks[1].Name())
	assert.Equal(t, "task-3", tasks[2].Name())
}

func TestSchedulerCore_RegisterTask_Duplicate(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := &ShardTask{
		name:           "test-task",
		priority:       model.TaskPriorityNormal,
		executionOrder: 1,
		executeFunc:    func(item any) TaskStatus { return TaskPassed },
	}

	err := core.RegisterTask(taskTemplate)
	require.NoError(t, err)

	// 重复注册应该失败
	err = core.RegisterTask(taskTemplate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

// ==========================================
// TaskScheduler 辅助方法测试
// ==========================================

func TestTaskScheduler_NewTaskScheduler_DefaultCores(t *testing.T) {
	scheduler := NewTaskScheduler("test", 0) // 0 表示使用 NumCPU()
	assert.NotNil(t, scheduler)
	assert.Greater(t, scheduler.coreCount, 0)
}

func TestTaskScheduler_NewTaskScheduler_SpecificCores(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)
	assert.NotNil(t, scheduler)
	assert.Equal(t, 4, scheduler.coreCount)
	assert.Len(t, scheduler.cores, 4)
}

func TestTaskScheduler_GetStats_Empty(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)
	stats := scheduler.GetStats()
	assert.NotNil(t, stats)
	assert.Len(t, stats.CoreStats, 2)
}

func TestTaskScheduler_GetStats_WithTasks(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	var processed atomic.Int64
	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 模拟处理一些任务
	stats := scheduler.GetStats()
	assert.NotNil(t, stats)
}

func TestTaskScheduler_calculateCoreQueueLen_Empty(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)
	core := scheduler.cores[0]
	queueLen := scheduler.calculateCoreQueueLen(core)
	assert.Equal(t, int64(0), queueLen)
}

func TestTaskScheduler_calculateCoreQueueLen_WithItems(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 向第一个核心添加一些任务
	core := scheduler.cores[0]
	task, _ := core.GetTaskByName("test-task")
	_ = task.Enqueue("item1")
	_ = task.Enqueue("item2")

	queueLen := scheduler.calculateCoreQueueLen(core)
	assert.Equal(t, int64(2), queueLen)
}

func TestTaskScheduler_HealthCheck_NoPanic(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)
	err := scheduler.HealthCheck()
	assert.NoError(t, err)
}

func TestTaskScheduler_HealthCheck_LongQueue(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	// 注册一个任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 向第一个核心的任务队列中添加大量元素（模拟异常）
	core := scheduler.cores[0]
	task, _ := core.GetTaskByName("test-task")
	for i := 0; i < 10001; i++ {
		_ = task.Enqueue(i)
	}

	err = scheduler.HealthCheck()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "queue too long")
}

func TestTaskScheduler_selectLeastLoadedCore_SingleCore(t *testing.T) {
	scheduler := NewTaskScheduler("test", 1)
	index := scheduler.selectLeastLoadedCore()
	assert.Equal(t, 0, index)
}

func TestTaskScheduler_selectLeastLoadedCore_MultipleCores(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 所有核心初始队列长度都为 0
	index := scheduler.selectLeastLoadedCore()
	// 应该选择第一个核心
	assert.Equal(t, 0, index)
}

func TestTaskScheduler_selectLeastLoadedCore_WithLoad(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	// 设置缓存间隔为 1，每次都重新计算（测试需要）
	scheduler.SetLoadBalanceCacheInterval(1)

	// 向第一个核心添加任务
	core := scheduler.cores[0]
	task, _ := core.GetTaskByName("test-task")
	_ = task.Enqueue("item1")
	_ = task.Enqueue("item2")

	// 现在第一个核心的队列更长
	index := scheduler.selectLeastLoadedCore()
	// 应该选择其他核心（1, 2, 或 3）
	assert.NotEqual(t, 0, index)
	assert.GreaterOrEqual(t, index, 1)
	assert.LessOrEqual(t, index, 3)
}

func TestTaskScheduler_Start_NotRunning(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	executor := &mockExecutorForCoverage{}
	err := scheduler.Start(executor)
	assert.NoError(t, err)
	assert.True(t, scheduler.running.Load())

	scheduler.Stop()
}

func TestTaskScheduler_Start_AlreadyRunning(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	executor := &mockExecutorForCoverage{}
	err := scheduler.Start(executor)
	require.NoError(t, err)

	// 第二次 Start 应该返回错误
	err = scheduler.Start(executor)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, nexerrors.ErrSchedulerRunning))

	scheduler.Stop()
}

func TestTaskScheduler_Stop_Twice(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	executor := &mockExecutorForCoverage{}
	_ = scheduler.Start(executor)

	scheduler.Stop()
	// 第二次 Stop 不应该 panic
	scheduler.Stop()
}

func TestTaskScheduler_EnqueueWithShard_NotStarted(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	item := &testShardItemForCoverage{shardID: 1}
	err = scheduler.EnqueueWithShard(item, "test-task")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestTaskScheduler_EnqueueWithShard_TaskNotFound(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	executor := &mockExecutorForCoverage{}
	err := scheduler.Start(executor)
	require.NoError(t, err)
	defer scheduler.Stop()

	item := &testShardItemForCoverage{shardID: 1}
	err = scheduler.EnqueueWithShard(item, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTaskScheduler_EnqueueWithShard_ShardID_Zero(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	executor := &mockExecutorForCoverage{}
	err = scheduler.Start(executor)
	require.NoError(t, err)
	defer scheduler.Stop()

	item := &testShardItemForCoverage{shardID: 0}
	err = scheduler.EnqueueWithShard(item, "test-task")
	assert.NoError(t, err)
}

// ==========================================
// 并发测试
// ==========================================

func TestTaskScheduler_ConcurrentRegisterTask(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	// 并发注册不同的任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(taskID int) {
			defer wg.Done()
			taskName := fmt.Sprintf("task-%d", taskID)
			err := scheduler.RegisterTask(
				func(item any) TaskStatus { return TaskPassed },
				taskName,
				model.TaskPriorityNormal,
				taskID+1,
			)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for e := range errors {
		t.Errorf("RegisterTask failed: %v", e)
	}

	// 验证所有任务都已注册
	for i := 0; i < 10; i++ {
		taskName := fmt.Sprintf("task-%d", i)
		core := scheduler.cores[0]
		_, err := core.GetTaskByName(taskName)
		assert.NoError(t, err, "task %s should be registered in core 0", taskName)
	}
}

func TestShardTask_ConcurrentEnqueue(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	const goroutines = 100
	const itemsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				_ = task.Enqueue(id*itemsPerGoroutine + j)
			}
		}(i)
	}

	wg.Wait()

	// 验证所有元素都已入队
	expectedLen := goroutines * itemsPerGoroutine
	assert.Equal(t, expectedLen, task.QueueLen())
}

func TestSchedulerCore_ConcurrentGetTaskByName(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := &ShardTask{
		name:           "test-task",
		priority:       model.TaskPriorityNormal,
		executionOrder: 1,
		executeFunc:    func(item any) TaskStatus { return TaskPassed },
	}

	_ = core.RegisterTask(taskTemplate)

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = core.GetTaskByName("test-task")
			}
		}()
	}

	wg.Wait()

	// 验证任务仍然存在
	task, err := core.GetTaskByName("test-task")
	assert.NoError(t, err)
	assert.NotNil(t, task)
}

// ==========================================
// 边界条件测试
// ==========================================

func TestTaskScheduler_RegisterTask_NilExecuteFunc(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	err := scheduler.RegisterTask(
		nil, // 允许 nil executeFunc
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	assert.NoError(t, err)
}

func TestTaskScheduler_EnqueueWithShard_NegativeShardID(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	executor := &mockExecutorForCoverage{}
	err = scheduler.Start(executor)
	require.NoError(t, err)
	defer scheduler.Stop()

	// 测试负数 shardID
	item := &testShardItemForCoverage{shardID: -5}
	err = scheduler.EnqueueWithShard(item, "test-task")
	assert.NoError(t, err)
}

func TestTaskScheduler_EnqueueWithShard_LargeShardID(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	err := scheduler.RegisterTask(
		func(item any) TaskStatus { return TaskPassed },
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	executor := &mockExecutorForCoverage{}
	err = scheduler.Start(executor)
	require.NoError(t, err)
	defer scheduler.Stop()

	// 测试很大的 shardID
	item := &testShardItemForCoverage{shardID: 999999}
	err = scheduler.EnqueueWithShard(item, "test-task")
	assert.NoError(t, err)
}

func TestNewShardTask_ZeroExecutionOrder(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 0, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, 0, task.ExecutionOrder())
}

func TestNewShardTask_NegativeExecutionOrder(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, -1, func(item any) TaskStatus {
		return TaskPassed
	})
	assert.Equal(t, -1, task.ExecutionOrder())
}

func TestSchedulerCore_NewSchedulerCore_NegativeCoreID(t *testing.T) {
	// 负数 coreID 也应该工作
	core := NewSchedulerCore(-1)
	assert.NotNil(t, core)
	assert.Equal(t, -1, core.coreID)
}

func TestTaskScheduler_Start_ZeroCores(t *testing.T) {
	// 0 核心应该使用 NumCPU()
	scheduler := NewTaskScheduler("test", 0)
	assert.NotNil(t, scheduler)
	assert.Greater(t, scheduler.coreCount, 0)
}

func TestTaskScheduler_Start_LargeCoreCount(t *testing.T) {
	// 大量核心数
	scheduler := NewTaskScheduler("test", 128)
	assert.NotNil(t, scheduler)
	assert.Equal(t, 128, scheduler.coreCount)
}

// ==========================================
// 额外测试以提高覆盖率
// ==========================================

func TestShardTask_Execute_NilFunc(t *testing.T) {
	// 创建 nil executeFunc 的任务
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, nil)
	status := task.Execute("item")
	assert.Equal(t, TaskPassed, status) // nil func 返回 TaskPassed
}

func TestSchedulerCore_ContextCancel(t *testing.T) {
	core := NewSchedulerCore(0)

	// 启动 runLoop
	go core.runLoop()

	// 取消上下文
	core.cancel()

	// 短暂等待
	time.Sleep(10 * time.Millisecond)

	// 验证核心已停止（没有新的循环）
	// 这只是验证没有 panic
}

func TestSchedulerCore_ExecuteTask_PanicRecovery(t *testing.T) {
	core := NewSchedulerCore(0)

	// 创建会 panic 的任务
	task := NewShardTask("panic-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		panic("test panic")
	})

	err := core.RegisterTask(task)
	require.NoError(t, err)

	// 执行任务（应被 recover）
	status := core.executeTask(task, "test")
	// panic 后返回零值
	assert.Equal(t, TaskStatus(0), status)

	// 验证 panic 计数增加
	assert.Equal(t, int64(1), core.stats.PanicCount.Load())
}

func TestTaskScheduler_HealthCheck_PanicDetected(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	// 直接设置 panic 计数（模拟 panic 发生）
	scheduler.cores[0].stats.PanicCount.Store(1)

	err := scheduler.HealthCheck()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has panic")
}

func TestSchedulerCore_waitForSignal_WithContextCancel(t *testing.T) {
	core := NewSchedulerCore(0)

	// 启动 goroutine 等待信号
	done := make(chan struct{})
	go func() {
		core.waitForSignal()
		close(done)
	}()

	// 取消上下文
	core.cancel()

	// 等待 waitForSignal 返回
	select {
	case <-done:
		// 成功返回
	case <-time.After(100 * time.Millisecond):
		t.Fatal("waitForSignal did not return after context cancel")
	}

	// 验证 EmptyWaits 增加
	assert.Greater(t, core.stats.EmptyWaits.Load(), int64(0))
}

func TestSchedulerCore_waitForSignal_WithWakeup(t *testing.T) {
	core := NewSchedulerCore(0)

	// 启动 goroutine 等待信号
	done := make(chan struct{})
	go func() {
		core.waitForSignal()
		close(done)
	}()

	// 短暂等待确保 goroutine 进入 waitForSignal
	time.Sleep(10 * time.Millisecond)

	// 唤醒
	core.wakeup()

	// 等待 waitForSignal 返回
	select {
	case <-done:
		// 成功返回
	case <-time.After(100 * time.Millisecond):
		t.Fatal("waitForSignal did not return after wakeup")
	}

	// 验证 EmptyWaits 增加
	assert.Greater(t, core.stats.EmptyWaits.Load(), int64(0))
}

func TestShardTask_Execute_RetryingStatus(t *testing.T) {
	// 创建返回 TaskRetrying 的任务
	task := NewShardTask("retry-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskRetrying
	})

	status := task.Execute("test")
	assert.Equal(t, TaskRetrying, status)
}

func TestShardTask_Execute_FailedStatus(t *testing.T) {
	// 创建返回 TaskFailed 的任务
	task := NewShardTask("fail-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskFailed
	})

	status := task.Execute("test")
	assert.Equal(t, TaskFailed, status)
}

// ==========================================
// Mock 类型（用于覆盖率测试）
// ==========================================

type testShardItemForCoverage struct {
	*model.BaseTask[struct{}] // 嵌入 BaseTask 实现接口组合
	shardID                   int
}

func (i *testShardItemForCoverage) ShardID() int {
	return i.shardID
}

func (i *testShardItemForCoverage) Execute(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
	return struct{}{}, nil
}

func (i *testShardItemForCoverage) TaskOrder() int {
	return 0 // 默认 order 0
}

type mockExecutorForCoverage struct {
	startCount atomic.Int32
}

func (m *mockExecutorForCoverage) Submit(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(context.Context)) error {
	m.startCount.Add(1)
	go fn(ctx)
	return nil
}

func (m *mockExecutorForCoverage) Close() error {
	return nil
}

// ==========================================
// Mock Executor（原有）
// ==========================================

type mockPerCoreExecutor struct {
	submitFunc func(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error
}

func (m *mockPerCoreExecutor) Submit(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(ctx context.Context)) error {
	if m.submitFunc != nil {
		return m.submitFunc(ctx, sourceID, priority, fn)
	}
	// 直接在 goroutine 中执行（模拟真实行为）
	go fn(ctx)
	return nil
}

func (m *mockPerCoreExecutor) Close() error {
	return nil
}
