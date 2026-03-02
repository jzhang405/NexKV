// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ==========================================
// 优先级系统测试（Unix 传统：0 最高，9 最低）
// ==========================================

func TestPriorityValues(t *testing.T) {
	// 验证优先级常量定义
	assert.Equal(t, model.TaskPriorityCritical, model.TaskPriority(0), "Critical priority should be 0")
	assert.Equal(t, model.TaskPriorityHigh, model.TaskPriority(1), "High priority should be 1")
	assert.Equal(t, model.TaskPriorityUrgent, model.TaskPriority(2), "Urgent priority should be 2")
	assert.Equal(t, model.TaskPriorityImportant, model.TaskPriority(3), "Important priority should be 3")
	assert.Equal(t, model.TaskPriorityNormalHigh, model.TaskPriority(4), "NormalHigh priority should be 4")
	assert.Equal(t, model.TaskPriorityNormal, model.TaskPriority(5), "Normal priority should be 5")
	assert.Equal(t, model.TaskPriorityNormalLow, model.TaskPriority(6), "NormalLow priority should be 6")
	assert.Equal(t, model.TaskPriorityLow, model.TaskPriority(7), "Low priority should be 7")
	assert.Equal(t, model.TaskPriorityBackground, model.TaskPriority(8), "Background priority should be 8")
	assert.Equal(t, model.TaskPriorityIdle, model.TaskPriority(9), "Idle priority should be 9")
}

func TestPriorityExecution(t *testing.T) {
	// 测试：多核环境下任务执行（不验证顺序，只验证所有任务都被执行）
	executor, err := NewPerCoreExecutor(
		WithQueueSize(100),
	)
	assert.NoError(t, err)
	defer executor.Close()

	const numTasks = 100
	var executedCount atomic.Int32

	// 提交不同优先级的任务
	priorities := []model.TaskPriority{
		model.TaskPriorityCritical, // 0
		model.TaskPriorityHigh,     // 1
		model.TaskPriorityNormal,   // 5
		model.TaskPriorityLow,      // 7
		model.TaskPriorityIdle,     // 9
	}

	for i := 0; i < numTasks; i++ {
		priority := priorities[i%len(priorities)]
		_ = executor.SubmitWithPriority(context.Background(), priority, func(ctx context.Context) {
			executedCount.Add(1)
		})
	}

	// 等待所有任务执行完成
	time.Sleep(500 * time.Millisecond)

	// 验证所有任务都被执行
	assert.Equal(t, int32(numTasks), executedCount.Load(), "All tasks should be executed")

	// 验证统计信息
	stats := executor.Stats()
	assert.Equal(t, int64(numTasks), stats.TotalCompleted, "Total completed should match submitted count")
}

func TestPriorityStarvationPrevention(t *testing.T) {
	// 验证饥饿防护机制：等待超过配置的超时自动提升优先级
	shortTimeout := 100 * time.Millisecond
	queue := newTaskQueue(100, shortTimeout)

	now := time.Now().UnixNano()

	// 创建测试任务：一个等待很久的低优先级任务 vs 刚提交的高优先级任务
	oldTask := taskItem{
		priority:   model.TaskPriorityIdle,            // 9 (最低)
		submitTime: now - int64(150*time.Millisecond), // 150ms 前提交
		task:       func(context.Context) {},
	}
	newTask := taskItem{
		priority:   model.TaskPriorityCritical, // 0 (最高)
		submitTime: now,                        // 刚提交
		task:       func(context.Context) {},
	}

	// 先添加低优先级任务（已超时）
	queue.Push(oldTask)
	// 再添加高优先级任务
	queue.Push(newTask)

	// 执行 promoteStarvedTasks（模拟 Pop 中的调用）
	queue.promoteStarvedTasks(now)

	// 验证队列状态：
	// - 优先级 0 应该有 2 个任务（newTask 和被提升的 oldTask）
	// - 优先级 9 应该是空的
	assert.Equal(t, 2, len(queue.queues[0]), "Priority 0 should have 2 tasks after promotion")
	assert.Equal(t, 0, len(queue.queues[9]), "Priority 9 should be empty after promotion")

	// Pop 应该返回优先级 0 的第一个任务（先添加的先弹出）
	popped := queue.Pop()

	// 验证弹出的任务不为空
	assert.NotNil(t, popped.task, "Should pop a task")

	// 验证队列长度减少了 1
	assert.Equal(t, 1, queue.Len(), "Queue length should be 1 after popping")
}

func TestPrioritySemanticNames(t *testing.T) {
	// 验证优先级的语义化名称符合业务场景
	testCases := []struct {
		priority model.TaskPriority
		name     string
	}{
		{model.TaskPriorityCritical, "Critical"},
		{model.TaskPriorityHigh, "High"},
		{model.TaskPriorityUrgent, "Urgent"},
		{model.TaskPriorityImportant, "Important"},
		{model.TaskPriorityNormalHigh, "NormalHigh"},
		{model.TaskPriorityNormal, "Normal"},
		{model.TaskPriorityNormalLow, "NormalLow"},
		{model.TaskPriorityLow, "Low"},
		{model.TaskPriorityBackground, "Background"},
		{model.TaskPriorityIdle, "Idle"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			priorityInt := int(tc.priority)
			assert.GreaterOrEqual(t, priorityInt, 0, "Priority should be >= 0")
			assert.LessOrEqual(t, priorityInt, 9, "Priority should be <= 9")
		})
	}
}

func TestSubmitWithDefaultPriority(t *testing.T) {
	// 验证 Submit() 使用默认优先级 TaskPriorityNormal (5)
	executor, err := NewPerCoreExecutor(

		WithQueueSize(100),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// Submit() 应该使用 TaskPriorityNormal (5)
	err = executor.Submit(context.Background(), func(ctx context.Context) {
		// 任务执行
	})
	assert.NoError(t, err)

	// 验证统计
	stats := executor.Stats()
	assert.Equal(t, int64(1), stats.TotalSubmitted, "Should have submitted 1 task")
}

// TestPriorityBoundary 测试优先级边界值
func TestPriorityBoundary(t *testing.T) {
	testCases := []struct {
		name     string
		priority model.TaskPriority
	}{
		{"Critical-0", model.TaskPriorityCritical},
		{"Idle-9", model.TaskPriorityIdle},
		{"Valid-5", model.TaskPriorityNormal},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, err := NewPerCoreExecutor(

				WithQueueSize(100),
			)
			assert.NoError(t, err)
			defer executor.Close()

			// 提交任务并验证
			err = executor.SubmitWithPriority(context.Background(), tc.priority, func(ctx context.Context) {})
			assert.NoError(t, err, "Priority %d should be accepted", tc.priority)
		})
	}
}

// TestStarvationTimeoutBoundary 测试饥饿防护超时边界值
func TestStarvationTimeoutBoundary(t *testing.T) {
	testCases := []struct {
		name    string
		timeout time.Duration
		valid   bool
	}{
		{"Zero", 0, true},                  // 禁用饥饿防护
		{"Normal", 10 * time.Second, true}, // 正常值
		{"Long", 60 * time.Second, true},   // 长超时
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, err := NewPerCoreExecutor(

				WithQueueSize(100),

				WithStarvationTimeout(tc.timeout),
			)

			if tc.valid {
				assert.NoError(t, err, "Timeout %v should be accepted", tc.timeout)
				if err == nil {
					defer executor.Close()
					// 验证配置已设置
					config := executor.Config()
					assert.Equal(t, tc.timeout, config.StarvationTimeout)
				}
			}
		})
	}
}

// TestStarvationTimeoutDefault 测试默认饥饿防护超时（10秒）
func TestStarvationTimeoutDefault(t *testing.T) {
	executor, err := NewPerCoreExecutor(

		WithQueueSize(100),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 验证配置
	config := executor.Config()
	assert.Equal(t, 10*time.Second, config.StarvationTimeout, "Default starvation timeout should be 10 seconds")
}

// TestStarvationTimeoutCustom 测试自定义饥饿防护超时
func TestStarvationTimeoutCustom(t *testing.T) {
	customTimeout := 5 * time.Second
	executor, err := NewPerCoreExecutor(

		WithQueueSize(100),

		WithStarvationTimeout(customTimeout),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 验证配置
	config := executor.Config()
	assert.Equal(t, customTimeout, config.StarvationTimeout, "Custom starvation timeout should be 5 seconds")
}

// TestStarvationTimeoutDisabled 测试禁用饥饿防护
func TestStarvationTimeoutDisabled(t *testing.T) {
	executor, err := NewPerCoreExecutor(

		WithQueueSize(100),

		WithStarvationTimeout(0), // 禁用饥饿防护
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 验证配置
	config := executor.Config()
	assert.Equal(t, time.Duration(0), config.StarvationTimeout, "Starvation timeout should be 0 (disabled)")
}

// TestMultiLevelQueueBasic 测试多级队列基本功能
func TestMultiLevelQueueBasic(t *testing.T) {
	queue := newTaskQueue(100, 0) // 禁用饥饿防护

	// 测试 Push 和 Len
	queue.Push(taskItem{
		priority:   model.TaskPriorityNormal, // 5
		submitTime: time.Now().UnixNano(),
		task:       nil,
	})

	assert.Equal(t, 1, queue.Len(), "Queue length should be 1")

	queue.Push(taskItem{
		priority:   model.TaskPriorityCritical, // 0 (最高)
		submitTime: time.Now().UnixNano(),
		task:       nil,
	})

	assert.Equal(t, 2, queue.Len(), "Queue length should be 2")

	// 测试 Pop - 应该先返回 Critical (0)，再返回 Normal (5)
	item1 := queue.Pop()
	assert.Equal(t, model.TaskPriorityCritical, item1.priority, "Should pop Critical task first")

	item2 := queue.Pop()
	assert.Equal(t, model.TaskPriorityNormal, item2.priority, "Should pop Normal task second")

	assert.Equal(t, 0, queue.Len(), "Queue should be empty")
}

// TestMultiLevelQueueFIFO 测试同优先级 FIFO 顺序
func TestMultiLevelQueueFIFO(t *testing.T) {
	queue := newTaskQueue(100, 0)

	now := time.Now().UnixNano()

	// 添加 3 个相同优先级的任务
	queue.Push(taskItem{
		priority:   model.TaskPriorityNormal,
		submitTime: now,
		task:       func(context.Context) {},
	})
	queue.Push(taskItem{
		priority:   model.TaskPriorityNormal,
		submitTime: now + 1, // 稍晚
		task:       func(context.Context) {},
	})
	queue.Push(taskItem{
		priority:   model.TaskPriorityNormal,
		submitTime: now + 2, // 更晚
		task:       func(context.Context) {},
	})

	// 应该按添加顺序弹出（FIFO）
	item1 := queue.Pop()
	item2 := queue.Pop()
	item3 := queue.Pop()

	assert.NotNil(t, item1.task)
	assert.NotNil(t, item2.task)
	assert.NotNil(t, item3.task)

	// 验证顺序
	assert.Equal(t, now, item1.submitTime, "First item should have earliest submit time")
	assert.Equal(t, now+1, item2.submitTime, "Second item should have middle submit time")
	assert.Equal(t, now+2, item3.submitTime, "Third item should have latest submit time")
}

// TestMultiLevelQueuePriorityClamp 测试优先级边界处理
func TestMultiLevelQueuePriorityClamp(t *testing.T) {
	queue := newTaskQueue(100, 0)

	// 测试低于 0 的优先级
	queue.Push(taskItem{
		priority:   model.TaskPriority(-1), // 低于最小值
		submitTime: time.Now().UnixNano(),
		task:       nil,
	})

	// 测试高于 9 的优先级
	queue.Push(taskItem{
		priority:   model.TaskPriority(100), // 高于最大值
		submitTime: time.Now().UnixNano(),
		task:       nil,
	})

	// 两个任务都应该能正常添加
	assert.Equal(t, 2, queue.Len(), "Should handle out-of-range priorities")

	// 验证优先级被正确限制
	// 第一个任务应该被限制到 0（最高）
	// 第二个任务应该被限制到 9（最低）
	item1 := queue.Pop()
	assert.Equal(t, model.TaskPriority(0), item1.priority, "Negative priority should be clamped to 0")

	item2 := queue.Pop()
	assert.Equal(t, model.TaskPriority(9), item2.priority, "Large priority should be clamped to 9")
}
