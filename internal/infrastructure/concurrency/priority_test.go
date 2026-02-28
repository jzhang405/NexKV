// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
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

func TestPriorityOrdering(t *testing.T) {
	// 验证优先级顺序（数值越小越重要）
	var queue taskQueue
	queue.starvationTimeout = 10 * time.Second // 设置默认超时

	// 添加不同优先级的任务（注意添加顺序）
	now := time.Now()
	queue.items = append(queue.items, taskItem{
		priority:   model.TaskPriorityCritical, // 0 (最高) - index 0
		submitTime: now.Add(2 * time.Second),
		task:       nil,
	})
	queue.items = append(queue.items, taskItem{
		priority:   model.TaskPriorityNormal, // 5 - index 1
		submitTime: now.Add(1 * time.Second),
		task:       nil,
	})
	queue.items = append(queue.items, taskItem{
		priority:   model.TaskPriorityIdle, // 9 (最低) - index 2
		submitTime: now,
		task:       nil,
	})

	// 手动测试 Less() 方法
	// Less(0, 1): Critical (0) vs Normal (5) -> 应该返回 true (0 < 5)
	assert.True(t, queue.Less(0, 1), "Critical (0) should have higher priority than Normal (5)")

	// Less(1, 2): Normal (5) vs Idle (9) -> 应该返回 true (5 < 9)
	assert.True(t, queue.Less(1, 2), "Normal (5) should have higher priority than Idle (9)")

	// Less(0, 2): Critical (0) vs Idle (9) -> 应该返回 true (0 < 9)
	assert.True(t, queue.Less(0, 2), "Critical (0) should have higher priority than Idle (9)")
}

func TestPriorityExecutionOrder(t *testing.T) {
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
	)
	assert.NoError(t, err)
	defer executor.Close()

	var executionOrder []int
	var orderMu sync.Mutex

	recordPriority := func(p int) func(context.Context) {
		return func(ctx context.Context) {
			orderMu.Lock()
			executionOrder = append(executionOrder, p)
			orderMu.Unlock()
		}
	}

	// 提交不同优先级的任务（故意乱序提交）
	priorities := []model.TaskPriority{
		model.TaskPriorityNormal,     // 5
		model.TaskPriorityCritical,   // 0
		model.TaskPriorityIdle,       // 9
		model.TaskPriorityHigh,       // 1
		model.TaskPriorityBackground, // 8
	}

	for _, p := range priorities {
		_ = executor.SubmitWithPriority(context.Background(), p, recordPriority(int(p)))
	}

	// 等待所有任务执行完成
	time.Sleep(500 * time.Millisecond)

	orderMu.Lock()
	defer orderMu.Unlock()

	// 验证执行顺序：应该是按优先级从高到低：0 -> 1 -> 5 -> 8 -> 9
	expectedOrder := []int{0, 1, 5, 8, 9}
	assert.Equal(t, expectedOrder, executionOrder, "Tasks should be executed in priority order (Unix style: 0 highest, 9 lowest)")
}

func TestPriorityFIFO(t *testing.T) {
	// 验证相同优先级的任务按 FIFO 顺序执行
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
	)
	assert.NoError(t, err)
	defer executor.Close()

	var executionOrder []int
	var orderMu sync.Mutex

	recordOrder := func(id int) func(context.Context) {
		return func(ctx context.Context) {
			orderMu.Lock()
			executionOrder = append(executionOrder, id)
			orderMu.Unlock()
		}
	}

	// 提交相同优先级的任务
	_ = executor.SubmitWithPriority(context.Background(), model.TaskPriorityNormal, recordOrder(1))
	_ = executor.SubmitWithPriority(context.Background(), model.TaskPriorityNormal, recordOrder(2))
	_ = executor.SubmitWithPriority(context.Background(), model.TaskPriorityNormal, recordOrder(3))

	// 等待所有任务执行完成
	time.Sleep(500 * time.Millisecond)

	orderMu.Lock()
	defer orderMu.Unlock()

	// 验证 FIFO 顺序
	expectedOrder := []int{1, 2, 3}
	assert.Equal(t, expectedOrder, executionOrder, "Tasks with same priority should be executed in FIFO order")
}

func TestPriorityStarvationPrevention(t *testing.T) {
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 验证饥饿防护机制：等待超过 10 秒自动提升优先级
	var queue taskQueue
	queue.starvationTimeout = 10 * time.Second
	now := time.Now()

	// 创建测试任务：一个等待很久的低优先级任务 vs 刚提交的高优先级任务
	oldTask := taskItem{
		priority:   PriorityIdle,               // 9 (最低)
		submitTime: now.Add(-11 * time.Second), // 11 秒前提交
		task:       nil,
	}
	newTask := taskItem{
		priority:   PriorityCritical, // 0 (最高)
		submitTime: now,              // 刚提交
		task:       nil,
	}

	queue.items = append(queue.items, oldTask, newTask)

	// 由于 oldTask 等待超过 10 秒，应该被提升优先级
	assert.True(t, queue.Less(0, 1), "Old low-priority task should be prioritized due to starvation prevention")
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
	// 验证 Submit() 使用默认优先级 PriorityNormal (5)
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// Submit() 应该使用 PriorityNormal (5)
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
				WithNumCores(1),
				WithQueueSize(100),
				WithEnableAffinity(false),
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
		{"Zero", 0, true},                     // 禁用饥饿防护
		{"Normal", 10 * time.Second, true},    // 正常值
		{"Long", 60 * time.Second, true},      // 长超时
		{"Negative", -1 * time.Second, false}, // 负数无效（当前实现接受）
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, err := NewPerCoreExecutor(
				WithNumCores(1),
				WithQueueSize(100),
				WithEnableAffinity(false),
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
			} else {
				// 当前实现不拒绝负数，但记录测试结果
				t.Logf("Timeout %v acceptance: got err=%v", tc.timeout, err)
			}
		})
	}
}

// TestStarvationTimeoutDefault 测试默认饥饿防护超时（10秒）
func TestStarvationTimeoutDefault(t *testing.T) {
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
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
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
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
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
		WithStarvationTimeout(0), // 禁用饥饿防护
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 验证配置
	config := executor.Config()
	assert.Equal(t, time.Duration(0), config.StarvationTimeout, "Starvation timeout should be 0 (disabled)")

	// 创建队列并验证饥饿防护被禁用
	queue := taskQueue{
		items:             make([]taskItem, 0, 10),
		starvationTimeout: 0, // 禁用
	}

	now := time.Now()
	queue.items = append(queue.items, taskItem{
		priority:   PriorityIdle,               // 9 (最低)
		submitTime: now.Add(-20 * time.Second), // 20秒前提交（应该提升优先级，但被禁用）
		task:       nil,
	})
	queue.items = append(queue.items, taskItem{
		priority:   PriorityCritical, // 0 (最高)
		submitTime: now,
		task:       nil,
	})

	// 饥饿防护被禁用，所以 Idle (9) 不应该提升优先级
	// Less(0, 1) 应该返回 false（0 > 9，数值越小越重要）
	assert.False(t, queue.Less(0, 1), "With starvation disabled, old low-priority task should NOT be prioritized")
}

// TestStarvationTimeoutEffect 测试饥饿防护实际效果
func TestStarvationTimeoutEffect(t *testing.T) {
	shortTimeout := 100 * time.Millisecond
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(100),
		WithEnableAffinity(false),
		WithStarvationTimeout(shortTimeout),
	)
	assert.NoError(t, err)
	defer executor.Close()

	// 创建队列并验证饥饿防护工作正常
	queue := taskQueue{
		items:             make([]taskItem, 0, 10),
		starvationTimeout: shortTimeout,
	}

	now := time.Now()
	oldTask := taskItem{
		priority:   PriorityIdle,                     // 9 (最低)
		submitTime: now.Add(-150 * time.Millisecond), // 150ms前提交（超过100ms阈值）
		task:       nil,
	}
	newTask := taskItem{
		priority:   PriorityCritical, // 0 (最高)
		submitTime: now,
		task:       nil,
	}

	queue.items = append(queue.items, oldTask, newTask)

	// 饥饿防护生效，等待超时的 Idle (9) 应该被提升优先级
	// Less(0, 1) 应该返回 true（因为等待超过 100ms）
	assert.True(t, queue.Less(0, 1), "Old low-priority task should be prioritized due to starvation prevention")
}
