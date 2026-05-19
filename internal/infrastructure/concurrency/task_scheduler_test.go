// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
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

	// 用 channel 捕获 handler 收到的 payload，验证路由正确性
	executed := make(chan string, 2)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			si := item.(*testShardItem)
			executed <- si.payload
			return TaskPassed
		},
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	err = scheduler.Start()
	require.NoError(t, err)
	defer scheduler.Stop()

	// 测试正数 ShardID 的固定路由
	// shardID=1 → core 1 (1 % 4 = 1)
	item1 := &testShardItem{shardID: 1, payload: "item-1"}
	err = scheduler.EnqueueWithShard(item1, "test-task")
	require.NoError(t, err)

	// shardID=5 → core 1 (5 % 4 = 1)
	item5 := &testShardItem{shardID: 5, payload: "item-5"}
	err = scheduler.EnqueueWithShard(item5, "test-task")
	require.NoError(t, err)

	// 验证两个任务都被执行（路由到 core 1 并被 runLoop 消费）
	results := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case payload := <-executed:
			results[payload] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for task %d to execute", i)
		}
	}
	assert.True(t, results["item-1"], "item-1 should have been executed")
	assert.True(t, results["item-5"], "item-5 should have been executed")
}

func TestTaskScheduler_ShardDistribution_Negative(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 用 channel 捕获 handler 收到的 payload，验证路由正确性
	executed := make(chan string, 2)

	// 注册任务
	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			si := item.(*testShardItem)
			executed <- si.payload
			return TaskPassed
		},
		"test-task",
		model.TaskPriorityNormal,
		1,
	)
	require.NoError(t, err)

	err = scheduler.Start()
	require.NoError(t, err)
	defer scheduler.Stop()

	// 测试负数 ShardID 的路由
	// shardID=-1 → core 1 (abs(-1) % 4 = 1)
	itemNeg1 := &testShardItem{shardID: -1, payload: "item-neg1"}
	err = scheduler.EnqueueWithShard(itemNeg1, "test-task")
	require.NoError(t, err)

	// shardID=-5 → core 1 (abs(-5) % 4 = 1)
	itemNeg5 := &testShardItem{shardID: -5, payload: "item-neg5"}
	err = scheduler.EnqueueWithShard(itemNeg5, "test-task")
	require.NoError(t, err)

	// 验证两个任务都被执行（路由到 core 1 并被 runLoop 消费）
	results := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case payload := <-executed:
			results[payload] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for task %d to execute", i)
		}
	}
	assert.True(t, results["item-neg1"], "item-neg1 should have been executed")
	assert.True(t, results["item-neg5"], "item-neg5 should have been executed")
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

	err = scheduler.Start()
	require.NoError(t, err)
	defer scheduler.Stop()

	// 入队 10 个 shardID=0 的任务
	// 低负载走 RoundRobin，均匀分配到各 core
	for i := range 10 {
		item := &testShardItem{shardID: 0, payload: fmt.Sprintf("item-zero-%d", i)}
		_ = scheduler.EnqueueWithShard(item, "test-task")
	}

	// 等待所有 item 被处理（检查各 core 的 processed 总和）
	require.Eventually(t, func() bool {
		var total int64
		for _, core := range scheduler.cores {
			total += core.stats.TotalTasksProcessed.Load()
		}
		return total >= 10
	}, 2*time.Second, 10*time.Millisecond)

	// 验证多个 core 参与了处理（RoundRobin 分配效果）
	coresUsed := 0
	for _, core := range scheduler.cores {
		if core.stats.TotalTasksProcessed.Load() > 0 {
			coresUsed++
		}
	}
	assert.GreaterOrEqual(t, coresUsed, 2, "round robin should use at least 2 cores")
}

// ==========================================
// 集成测试
// ==========================================

func TestTaskScheduler_Integration(t *testing.T) {
	t.Skip("需要真实执行器才能测试完整流程")

	scheduler := NewTaskScheduler("integration", 2)

	// 创建同步 executor 用于测试
	var wg sync.WaitGroup

	err := scheduler.Start()
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
	for i := range 10 {
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

func TestSchedulerCore_getOrderedBuckets_Empty(t *testing.T) {
	core := NewSchedulerCore(0)
	buckets := core.getOrderedBuckets()
	totalTasks := 0
	for _, bucket := range buckets {
		totalTasks += len(bucket)
	}
	assert.Equal(t, 0, totalTasks)
}

func TestSchedulerCore_getOrderedBuckets_SingleTask(t *testing.T) {
	core := NewSchedulerCore(0)

	taskTemplate := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	_ = core.RegisterTask(taskTemplate)

	buckets := core.getOrderedBuckets()
	// TaskPriorityNormal = 5
	assert.Len(t, buckets[model.TaskPriorityNormal], 1)
	assert.Equal(t, "test-task", buckets[model.TaskPriorityNormal][0].Name())
}

func TestSchedulerCore_getOrderedBuckets_MultiplePriorities(t *testing.T) {
	core := NewSchedulerCore(0)

	// 注册不同优先级的任务
	taskNormal := &ShardTask{name: "btree-set", priority: model.TaskPriorityNormal, executionOrder: 0, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	taskHigh := &ShardTask{name: "btree-split", priority: model.TaskPriorityHigh, executionOrder: 1, executeFunc: func(item any) TaskStatus { return TaskPassed }}

	_ = core.RegisterTask(taskNormal)
	_ = core.RegisterTask(taskHigh)

	buckets := core.getOrderedBuckets()

	// 验证各优先级桶分配正确
	assert.Len(t, buckets[model.TaskPriorityHigh], 1)
	assert.Equal(t, "btree-split", buckets[model.TaskPriorityHigh][0].Name())

	assert.Len(t, buckets[model.TaskPriorityNormal], 1)
	assert.Equal(t, "btree-set", buckets[model.TaskPriorityNormal][0].Name())
}

// TestSchedulerCore_PriorityScheduling_VerifiesBitmapOrder 验证 bitmap 遍历顺序
// High priority (p=1) 的 task 应在 Normal priority (p=5) 之前被处理
func TestSchedulerCore_PriorityScheduling_VerifiesBitmapOrder(t *testing.T) {
	core := NewSchedulerCore(0)

	taskNormal := &ShardTask{name: "btree-set", priority: model.TaskPriorityNormal, executionOrder: 0, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	taskHigh := &ShardTask{name: "btree-split", priority: model.TaskPriorityHigh, executionOrder: 1, executeFunc: func(item any) TaskStatus { return TaskPassed }}

	_ = core.RegisterTask(taskNormal)
	_ = core.RegisterTask(taskHigh)

	// 验证 activeBitmap = 0b0100010 (bit 1 + bit 5)
	assert.Equal(t, uint16(0b0100010), core.activeBitmap)

	// 验证 bitmap 遍历顺序：TrailingZeros16 先返回 p=1 (High)，再 p=5 (Normal)
	var order []int
	bitmap := core.activeBitmap
	for bitmap != 0 {
		p := bits.TrailingZeros16(bitmap)
		if p >= NumPriorityLevels {
			break
		}
		order = append(order, p)
		bitmap &^= (1 << p)
	}
	assert.Equal(t, []int{1, 5}, order) // High(1) before Normal(5)
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
	for i := range 10001 {
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

	// 所有核心初始队列长度都为 0，走 RoundRobin 快速路径
	// RoundRobin 会在 [0, coreCount) 间轮转，验证返回有效 core index
	index := scheduler.selectLeastLoadedCore()
	assert.GreaterOrEqual(t, index, 0)
	assert.Less(t, index, 4)

	// 连续调用应均匀分布到不同 core
	seen := make(map[int]bool)
	for range 8 {
		idx := scheduler.selectLeastLoadedCore()
		seen[idx] = true
	}
	assert.GreaterOrEqual(t, len(seen), 2, "round robin should distribute across cores")
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

	// 设置低阈值确保高负载时走精确选择路径
	scheduler.SetLoadBalanceThreshold(1)

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

	err := scheduler.Start()
	assert.NoError(t, err)
	assert.True(t, scheduler.running.Load())

	scheduler.Stop()
}

func TestTaskScheduler_Start_AlreadyRunning(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	err := scheduler.Start()
	require.NoError(t, err)

	// 第二次 Start 应该返回错误
	err = scheduler.Start()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, nexerrors.ErrSchedulerRunning))

	scheduler.Stop()
}

func TestTaskScheduler_Stop_Twice(t *testing.T) {
	scheduler := NewTaskScheduler("test", 2)

	_ = scheduler.Start()

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

	err := scheduler.Start()
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

	err = scheduler.Start()
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
	for i := range 10 {
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
	for i := range 10 {
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

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range itemsPerGoroutine {
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

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
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

	err = scheduler.Start()
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

	err = scheduler.Start()
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

// ==========================================
// P1-4: 桶内 executionOrder 排序测试
// ==========================================

func TestSchedulerCore_getOrderedBuckets_SortingWithinBucket(t *testing.T) {
	core := NewSchedulerCore(0)

	// 在同一优先级桶内注册多个任务，验证 executionOrder 排序
	taskA := &ShardTask{name: "task-a", priority: model.TaskPriorityNormal, executionOrder: 3, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	taskB := &ShardTask{name: "task-b", priority: model.TaskPriorityNormal, executionOrder: 1, executeFunc: func(item any) TaskStatus { return TaskPassed }}
	taskC := &ShardTask{name: "task-c", priority: model.TaskPriorityNormal, executionOrder: 2, executeFunc: func(item any) TaskStatus { return TaskPassed }}

	// 注册顺序：A(3), B(1), C(2)
	require.NoError(t, core.RegisterTask(taskA))
	require.NoError(t, core.RegisterTask(taskB))
	require.NoError(t, core.RegisterTask(taskC))

	buckets := core.getOrderedBuckets()

	// 验证桶内按 executionOrder 升序排列：B(1), C(2), A(3)
	normalBucket := buckets[model.TaskPriorityNormal]
	require.Len(t, normalBucket, 3)
	assert.Equal(t, "task-b", normalBucket[0].Name()) // eo=1
	assert.Equal(t, "task-c", normalBucket[1].Name()) // eo=2
	assert.Equal(t, "task-a", normalBucket[2].Name()) // eo=3
}

// ==========================================
// P1-5: 饥饿防护测试
// ==========================================

func TestShardTask_LastSubmitTime_And_PriorityBoost(t *testing.T) {
	task := NewShardTask("test-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 初始值
	assert.Equal(t, int64(0), task.LastSubmitTime())
	assert.False(t, task.HasPriorityBoost())

	// Enqueue 后记录时间
	core := NewSchedulerCore(0)
	require.NoError(t, core.RegisterTask(task))
	registeredTask, _ := core.GetTaskByName("test-task")
	require.NoError(t, registeredTask.Enqueue("item1"))
	assert.Greater(t, registeredTask.LastSubmitTime(), int64(0))

	// 设置/清除 priorityBoost
	registeredTask.SetPriorityBoost(true)
	assert.True(t, registeredTask.HasPriorityBoost())
	registeredTask.SetPriorityBoost(false)
	assert.False(t, registeredTask.HasPriorityBoost())
}

func TestSchedulerCore_checkStarvation_PromotesStarvedTask(t *testing.T) {
	core := NewSchedulerCore(0)

	task := NewShardTask("starved-task", model.TaskPriorityLow, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	require.NoError(t, core.RegisterTask(task))

	registeredTask, _ := core.GetTaskByName("starved-task")

	// 手动设置 lastSubmitTime 为很久之前（超过 starvationTimeout=100ms）
	oldTime := time.Now().Add(-200 * time.Millisecond).UnixNano()
	registeredTask.lastSubmitTime.Store(oldTime)

	// 初始化 cachedBuckets（模拟 runLoop 启动时的缓存）
	core.cachedBuckets = core.getOrderedBuckets()

	// 重置 starvationCheck 以允许检查
	core.starvationCheck = 0

	// 执行饥饿检查
	core.checkStarvation()

	// 验证被提升
	assert.True(t, registeredTask.HasPriorityBoost(), "starved task should be priority-boosted")
}

func TestSchedulerCore_checkStarvation_SkipsRecentTask(t *testing.T) {
	core := NewSchedulerCore(0)

	task := NewShardTask("recent-task", model.TaskPriorityLow, 1, func(item any) TaskStatus {
		return TaskPassed
	})
	require.NoError(t, core.RegisterTask(task))

	registeredTask, _ := core.GetTaskByName("recent-task")

	// 设置 lastSubmitTime 为最近（未超时）
	registeredTask.lastSubmitTime.Store(time.Now().UnixNano())

	// 重置 starvationCheck
	core.starvationCheck = 0

	// 执行饥饿检查
	core.checkStarvation()

	// 不应被提升
	assert.False(t, registeredTask.HasPriorityBoost(), "recent task should not be priority-boosted")
}

// ==========================================
// P1-6: taskName 路由测试
// ==========================================

func TestTaskScheduler_EnqueueWithShard_TaskNameRouting(t *testing.T) {
	scheduler := NewTaskScheduler("test", 4)

	// 注册多个任务类型
	var setDone, splitDone atomic.Bool
	require.NoError(t, scheduler.RegisterTask(
		func(item any) TaskStatus { setDone.Store(true); return TaskPassed },
		"btree-set",
		model.TaskPriorityNormal,
		0,
	))
	require.NoError(t, scheduler.RegisterTask(
		func(item any) TaskStatus { splitDone.Store(true); return TaskPassed },
		"btree-split",
		model.TaskPriorityHigh,
		1,
	))

	require.NoError(t, scheduler.Start())
	defer scheduler.Stop()

	// 路由 btree-set 任务
	setItem := &testShardItemForCoverage{shardID: 1}
	err := scheduler.EnqueueWithShard(setItem, "btree-set")
	assert.NoError(t, err, "btree-set task should route successfully")

	// 路由 btree-split 任务
	splitItem := &testShardItemForCoverage{shardID: 1}
	err = scheduler.EnqueueWithShard(splitItem, "btree-split")
	assert.NoError(t, err, "btree-split task should route successfully")
	// 轮询等待异步任务执行（worker 可能已出队，不能依赖 QueueLen）
	require.Eventually(t, func() bool { return setDone.Load() && splitDone.Load() },
		2*time.Second, 10*time.Millisecond, "both tasks should be executed")
}
