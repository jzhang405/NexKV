// Package model Task 并发安全测试
//
// 测试 BaseTask 的并发安全性，包括：
// - goroutine 泄漏检测
// - context 泄漏检测
// - 死锁场景测试
// - 并发压力测试
package model

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// goroutine 泄漏检测测试
// ============================================================================

// TestBaseTask_NoGoroutineLeak 测试任务执行完成后不会有 goroutine 泄漏
func TestBaseTask_NoGoroutineLeak(t *testing.T) {
	// 获取初始 goroutine 数量
	initialGoroutines := runtime.NumGoroutine()

	// 创建并执行多个任务
	for i := 0; i < 100; i++ {
		task := NewBaseTask[struct{}](
			OpRPC,
			TaskPriorityNormal,
			SourceNetwork,
			func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
				time.Sleep(10 * time.Millisecond) // 模拟耗时操作
				return struct{}{}, nil
			},
		)

		// 执行任务
		go task.Run(context.Background(), nil)

		// 等待任务完成
		<-task.Done()
	}

	// 等待所有 goroutine 退出
	time.Sleep(100 * time.Millisecond)

	// 检查 goroutine 数量
	finalGoroutines := runtime.NumGoroutine()

	// 允许少量波动（±5个），但不应该有大量泄漏
	leakedGoroutines := finalGoroutines - initialGoroutines
	assert.LessOrEqual(t, leakedGoroutines, 5,
		"Expected no goroutine leak, but found %d leaked goroutines (initial: %d, final: %d)",
		leakedGoroutines, initialGoroutines, finalGoroutines)
}

// TestBaseTask_ContextCancellation 测试 context 取消后任务应该立即退出
func TestBaseTask_ContextCancellation(t *testing.T) {
	var cancelled int32

	task := NewBaseTask[struct{}](
		OpRPC,
		TaskPriorityNormal,
		SourceNetwork,
		func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
			// 等待 context 取消
			<-ctx.Done()
			atomic.StoreInt32(&cancelled, 1)
			return struct{}{}, ctx.Err()
		},
	)

	// 创建可取消的 context
	ctx, cancel := context.WithCancel(context.Background())

	// 启动任务
	go task.Run(ctx, nil)

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 取消 context
	cancel()

	// 等待任务完成
	select {
	case <-task.Done():
		// 任务已完成
	case <-time.After(1 * time.Second):
		t.Fatal("Task did not complete after context cancellation")
	}

	// 验证任务收到了取消信号
	assert.Equal(t, int32(1), atomic.LoadInt32(&cancelled), "Task should have been cancelled")
}

// ============================================================================
// Context 泄漏检测测试
// ============================================================================

// TestBaseTask_NoContextLeak 测试任务完成后 context 应该被释放
func TestBaseTask_NoContextLeak(t *testing.T) {
	// 创建多个任务并执行
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

		task := NewBaseTask[struct{}](
			OpRPC,
			TaskPriorityNormal,
			SourceNetwork,
			func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)

		// 执行任务
		task.Run(ctx, nil)

		// 取消 context（即使任务已完成，也应该正确处理）
		cancel()

		// 等待任务完成
		<-task.Done()
	}

	// 如果没有内存泄漏或 context 泄漏，测试通过
	// 注意：这里无法直接检测 context 泄漏，但可以通过内存分析工具验证
}

// ============================================================================
// 死锁场景测试
// ============================================================================

// TestBaseTask_NoDeadlockOnCallback 测试回调中不会发生死锁
func TestBaseTask_NoDeadlockOnCallback(t *testing.T) {
	var callbackCalled int32

	task := NewBaseTask[struct{}](
		OpRPC,
		TaskPriorityNormal,
		SourceNetwork,
		func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
			// 模拟在回调中执行一些操作
			atomic.StoreInt32(&callbackCalled, 1)
			return struct{}{}, nil
		},
	)

	// 执行任务
	done := make(chan struct{})
	go func() {
		task.Run(context.Background(), nil)
		close(done)
	}()

	// 等待任务完成，设置超时防止死锁
	select {
	case <-done:
		// 任务已完成
		assert.Equal(t, int32(1), atomic.LoadInt32(&callbackCalled))
	case <-time.After(2 * time.Second):
		t.Fatal("Task execution timed out - possible deadlock")
	}
}

// TestBaseTask_NoDeadlockOnResultAccess 测试访问结果时不会发生死锁
func TestBaseTask_NoDeadlockOnResultAccess(t *testing.T) {
	task := NewBaseTask[int](
		OpRPC,
		TaskPriorityNormal,
		SourceNetwork,
		func(ctx context.Context, pipeline PipelineContext) (int, error) {
			time.Sleep(100 * time.Millisecond)
			return 42, nil
		},
	)

	// 启动任务
	go task.Run(context.Background(), nil)

	// 并发访问结果
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-task.Done()
			result, err := task.GetResult()
			assert.NoError(t, err)
			assert.Equal(t, 42, result)
		}()
	}

	// 等待所有访问完成，设置超时防止死锁
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有访问已完成
	case <-time.After(2 * time.Second):
		t.Fatal("Result access timed out - possible deadlock")
	}
}

// ============================================================================
// 并发压力测试
// ============================================================================

// TestBaseTask_HighConcurrency 测试高并发场景
func TestBaseTask_HighConcurrency(t *testing.T) {
	const taskCount = 1000
	const concurrency = 100

	var completedTasks int32
	var wg sync.WaitGroup

	// 使用 semaphore 控制并发度
	sem := make(chan struct{}, concurrency)

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		sem <- struct{}{} // 获取信号量

		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量

			task := NewBaseTask[int](
				OpRPC,
				TaskPriorityNormal,
				SourceNetwork,
				func(ctx context.Context, pipeline PipelineContext) (int, error) {
					time.Sleep(time.Millisecond) // 模拟短时操作
					return id, nil
				},
			)

			task.Run(context.Background(), nil)
			<-task.Done()

			atomic.AddInt32(&completedTasks, 1)
		}(i)
	}

	// 等待所有任务完成
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有任务已完成
		assert.Equal(t, int32(taskCount), atomic.LoadInt32(&completedTasks))
	case <-time.After(10 * time.Second):
		t.Fatalf("High concurrency test timed out - only %d/%d tasks completed",
			atomic.LoadInt32(&completedTasks), taskCount)
	}
}

// TestBaseTask_LongRunning 测试长时间运行场景
func TestBaseTask_LongRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long running test in short mode")
	}

	const duration = 5 * time.Second
	const taskInterval = 10 * time.Millisecond

	var taskCount int32
	var completedTasks int32

	timeout := time.After(duration)

	for {
		select {
		case <-timeout:
			// 测试结束
			t.Logf("Completed %d tasks in %v", atomic.LoadInt32(&completedTasks), duration)
			require.Eventually(t, func() bool {
				return atomic.LoadInt32(&completedTasks) == atomic.LoadInt32(&taskCount)
			}, 2*time.Second, 100*time.Millisecond, "Not all tasks completed")
			return

		default:
			// 创建并执行任务
			atomic.AddInt32(&taskCount, 1)

			task := NewBaseTask[struct{}](
				OpRPC,
				TaskPriorityNormal,
				SourceNetwork,
				func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
					time.Sleep(5 * time.Millisecond)
					return struct{}{}, nil
				},
			)

			go func() {
				task.Run(context.Background(), nil)
				<-task.Done()
				atomic.AddInt32(&completedTasks, 1)
			}()

			time.Sleep(taskInterval)
		}
	}
}

// ============================================================================
// 状态转换测试
// ============================================================================

// TestBaseTask_StateTransitions 测试状态转换的正确性
func TestBaseTask_StateTransitions(t *testing.T) {
	task := NewBaseTask[struct{}](
		OpRPC,
		TaskPriorityNormal,
		SourceNetwork,
		func(ctx context.Context, pipeline PipelineContext) (struct{}, error) {
			return struct{}{}, nil
		},
	)

	// 初始状态应该是 Pending
	assert.Equal(t, StatusPending, task.Status())
	assert.False(t, task.IsDone())

	// 执行任务
	task.Run(context.Background(), nil)

	// 等待完成
	<-task.Done()

	// 最终状态应该是 Completed
	assert.Equal(t, StatusCompleted, task.Status())
	assert.True(t, task.IsDone())
}

// TestBaseTask_ConcurrentStateTransitions 测试并发状态转换
func TestBaseTask_ConcurrentStateTransitions(t *testing.T) {
	task := NewBaseTask[int](
		OpRPC,
		TaskPriorityNormal,
		SourceNetwork,
		func(ctx context.Context, pipeline PipelineContext) (int, error) {
			time.Sleep(50 * time.Millisecond)
			return 42, nil
		},
	)

	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	// 并发检查状态
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				status := task.Status()
				switch status {
				case StatusPending, StatusRunning, StatusCompleted, StatusFailed:
					// 状态合法
					atomic.AddInt32(&successCount, 1)
				default:
					// 状态非法
					atomic.AddInt32(&failCount, 1)
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// 同时执行任务
	go task.Run(context.Background(), nil)

	// 等待所有检查完成
	wg.Wait()

	// 验证所有状态检查都是合法的
	assert.Equal(t, int32(0), atomic.LoadInt32(&failCount),
		"All state transitions should be valid")
}
