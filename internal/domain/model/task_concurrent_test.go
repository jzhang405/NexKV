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
			TaskPriorityNormal,
			0, // maxRetries = 0
			func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
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
		TaskPriorityNormal,
		0,
		func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
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
	<-task.Done()

	// 验证任务检测到了取消
	assert.Equal(t, int32(1), atomic.LoadInt32(&cancelled))
}

// TestBaseTask_ConcurrentRun 测试并发调用 Run，确保只执行一次
func TestBaseTask_ConcurrentRun(t *testing.T) {
	var runCount int32
	task := NewBaseTask[struct{}](
		TaskPriorityNormal,
		0,
		func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
			atomic.AddInt32(&runCount, 1)
			time.Sleep(100 * time.Millisecond) // 模拟耗时操作
			return struct{}{}, nil
		},
	)

	// 并发调用 Run，确保只执行一次
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task.Run(context.Background(), nil)
		}()
	}

	wg.Wait()

	// 确保只执行了一次
	assert.Equal(t, int32(1), atomic.LoadInt32(&runCount))
	assert.True(t, task.IsDone())
}

// ============================================================================
// 死锁场景测试
// ============================================================================

// TestBaseTask_NoDeadlockOnResultAccess 测试访问结果时不会发生死锁
func TestBaseTask_NoDeadlockOnResultAccess(t *testing.T) {
	task := NewBaseTask[int](
		TaskPriorityNormal,
		0,
		func(ctx context.Context, trCtx TaskRunnerContext) (int, error) {
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
			result, err := task.Wait(context.Background())
			require.NoError(t, err)
			assert.Equal(t, 42, result)
		}()
	}

	wg.Wait()
	assert.True(t, task.IsDone())
}

// TestBaseTask_NoDeadlockOnErrorAccess 测试访问错误时不会发生死锁
func TestBaseTask_NoDeadlockOnErrorAccess(t *testing.T) {
	task := NewBaseTask[int](
		TaskPriorityNormal,
		0,
		func(ctx context.Context, trCtx TaskRunnerContext) (int, error) {
			time.Sleep(10 * time.Millisecond)
			return 0, assert.AnError
		},
	)

	// 启动任务
	go task.Run(context.Background(), nil)

	// 并发访问错误
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := task.Wait(context.Background())
			assert.Error(t, err)
		}()
	}

	wg.Wait()
	assert.True(t, task.IsDone())
}

// ============================================================================
// 并发压力测试
// ============================================================================

// TestBaseTask_ConcurrentStressTest 并发压力测试
func TestBaseTask_ConcurrentStressTest(t *testing.T) {
	const goroutines = 100
	const tasksPerGoroutine = 100

	var completedTasks int64

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < tasksPerGoroutine; j++ {
				task := NewBaseTask[struct{}](
					TaskPriorityNormal,
					0,
					func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
						atomic.AddInt64(&completedTasks, 1)
						return struct{}{}, nil
					},
				)

				go task.Run(context.Background(), nil)
				<-task.Done()
			}
		}(i)
	}

	// 等待所有任务完成
	for atomic.LoadInt64(&completedTasks) < int64(goroutines*tasksPerGoroutine) {
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, int64(goroutines*tasksPerGoroutine), atomic.LoadInt64(&completedTasks))
}

// TestBaseTask_WaitTimeout 测试 Wait 超时
func TestBaseTask_WaitTimeout(t *testing.T) {
	task := NewBaseTask[struct{}](
		TaskPriorityNormal,
		0,
		func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
			time.Sleep(1 * time.Second) // 超过超时时间
			return struct{}{}, nil
		},
	)

	// 创建 100ms 超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 启动任务
	go task.Run(context.Background(), nil)

	// Wait 应该超时
	_, err := task.Wait(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}
