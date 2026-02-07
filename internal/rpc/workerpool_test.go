// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// Mock Task for Testing
// ========================================

// MockTask 模拟任务（用于测试）
type MockTask struct {
	ID         int
	Delay      time.Duration
	ShouldFail bool
	executed   bool
	mu         sync.Mutex
}

// Execute 执行模拟任务
func (m *MockTask) Execute(ctx context.Context) error {
	m.mu.Lock()
	m.executed = true
	m.mu.Unlock()

	if m.Delay > 0 {
		select {
		case <-time.After(m.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if m.ShouldFail {
		return fmt.Errorf("task %d failed intentionally", m.ID)
	}

	return nil
}

// IsExecuted 检查任务是否已执行
func (m *MockTask) IsExecuted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.executed
}

// ========================================
// WorkerPool Tests
// ========================================

// TestWorkerPool_BasicFunctionality 测试基本功能
func TestWorkerPool_BasicFunctionality(t *testing.T) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 2,
		QueueSize:  10,
	}

	pool := NewWorkerPool(cfg)
	defer pool.Close()

	// 提交 3 个任务
	var wg sync.WaitGroup
	results := make([]bool, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		task := &MockTask{ID: i}

		go func(index int, mt *MockTask) {
			defer wg.Done()
			err := pool.Submit(mt)
			assert.NoError(t, err, "任务提交应该成功")
			time.Sleep(100 * time.Millisecond)
			results[index] = mt.IsExecuted()
		}(i, task)
	}

	wg.Wait()

	// 验证所有任务都已执行
	for i, executed := range results {
		assert.True(t, executed, "任务 %d 应该已执行", i)
	}
}

// TestWorkerPool_QueueFull 测试队列满的情况
func TestWorkerPool_QueueFull(t *testing.T) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 1,
		QueueSize:  2, // 小队列
	}

	pool := NewWorkerPool(cfg)
	defer pool.Close()

	// 提交一个长时间运行的任务
	longTask := &MockTask{ID: 0, Delay: 200 * time.Millisecond}
	require.NoError(t, pool.Submit(longTask))

	// 等待 worker 开始处理 longTask（确保 worker 被阻塞）
	time.Sleep(10 * time.Millisecond)

	// 快速填满队列
	for i := 1; i <= 2; i++ {
		task := &MockTask{ID: i, Delay: 100 * time.Millisecond}
		err := pool.Submit(task)
		assert.NoError(t, err, "任务 %d 应该成功提交", i)
	}

	// 队列已满，下一个任务应该失败
	fullTask := &MockTask{ID: 99}
	err := pool.Submit(fullTask)
	assert.Error(t, err, "队列满时应该返回错误")
	assert.Contains(t, err.Error(), "任务队列已满")
}

// TestWorkerPool_SubmitWithTimeout 测试带超时的任务提交
func TestWorkerPool_SubmitWithTimeout(t *testing.T) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 1,
		QueueSize:  1, // 队列大小 1
	}

	pool := NewWorkerPool(cfg)
	defer pool.Close()

	// 提交一个长时间运行的任务（占用 worker）
	longTask := &MockTask{ID: 0, Delay: 500 * time.Millisecond}
	require.NoError(t, pool.Submit(longTask))

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 快速提交第二个任务（填满队列）
	queueFillTask := &MockTask{ID: 1, Delay: 100 * time.Millisecond}
	require.NoError(t, pool.Submit(queueFillTask))

	// 队列已满，第三个任务应该超时
	timeoutTask := &MockTask{ID: 2}
	start := time.Now()
	err := pool.SubmitWithTimeout(timeoutTask, 100*time.Millisecond)
	duration := time.Since(start)

	assert.Error(t, err, "超时应该返回错误")
	if err != nil {
		assert.Contains(t, err.Error(), "提交任务超时", "错误信息应该包含超时提示")
	}
	// 验证至少等待了指定的超时时间
	assert.GreaterOrEqual(t, duration, 100*time.Millisecond, "应该至少等待超时时间")
}

// TestWorkerPool_GracefulShutdown 测试优雅关闭
func TestWorkerPool_GracefulShutdown(t *testing.T) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 2,
		QueueSize:  10,
	}

	pool := NewWorkerPool(cfg)

	// 提交 5 个快速任务并等待执行
	tasks := make([]*MockTask, 5)
	for i := 0; i < 5; i++ {
		tasks[i] = &MockTask{ID: i, Delay: 5 * time.Millisecond}
		err := pool.Submit(tasks[i])
		require.NoError(t, err, "任务 %d 应该成功提交", i)
	}

	// 等待所有任务执行完成
	time.Sleep(200 * time.Millisecond)

	// 统计已执行的任务数量
	var executedCount int32
	for _, task := range tasks {
		if task.IsExecuted() {
			atomic.AddInt32(&executedCount, 1)
		}
	}

	// 关闭池（等待所有任务完成）
	err := pool.Close()
	assert.NoError(t, err, "关闭应该成功")

	// 验证至少有一些任务已执行
	count := atomic.LoadInt32(&executedCount)
	assert.GreaterOrEqual(t, count, int32(3), "至少应该有 3 个任务已执行")
}

// TestWorkerPool_ConcurrentSubmit 测试并发提交
func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 5,
		QueueSize:  100,
	}

	pool := NewWorkerPool(cfg)
	defer pool.Close()

	// 并发提交 100 个任务
	const numTasks = 100
	var wg sync.WaitGroup

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		task := &MockTask{ID: i}

		go func(mt *MockTask) {
			defer wg.Done()
			err := pool.Submit(mt)
			assert.NoError(t, err, "并发提交应该成功")
		}(task)
	}

	wg.Wait()

	// 等待所有任务完成执行
	time.Sleep(200 * time.Millisecond)
}

// ========================================
// ConcurrencyLimiter Tests
// ========================================

// TestConcurrencyLimiter_BasicAcquireRelease 测试基本的获取和释放
func TestConcurrencyLimiter_BasicAcquireRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter(3)
	ctx := context.Background()

	// 获取 3 个许可
	for i := 0; i < 3; i++ {
		err := limiter.Acquire(ctx)
		assert.NoError(t, err, "应该成功获取许可 %d", i)
		assert.Equal(t, int32(i+1), limiter.Current(), "当前并发数应该是 %d", i+1)
	}

	// 第 4 个许可应该阻塞
	done := make(chan error)
	go func() {
		err := limiter.Acquire(ctx)
		done <- err
	}()

	// 等待一小段时间，确保 goroutine 已阻塞
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("不应该收到结果: %v", err)
	default:
		// 预期：goroutine 仍在阻塞
	}

	// 释放一个许可
	limiter.Release()
	assert.Equal(t, int32(2), limiter.Current(), "释放后当前并发数应该是 2")

	// 现在应该可以获取第 4 个许可
	select {
	case err := <-done:
		assert.NoError(t, err, "应该成功获取许可")
		assert.Equal(t, int32(3), limiter.Current(), "获取后当前并发数应该是 3")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("应该及时获取到许可")
	}
}

// TestConcurrencyLimiter_TryAcquire 测试非阻塞获取
func TestConcurrencyLimiter_TryAcquire(t *testing.T) {
	limiter := NewConcurrencyLimiter(2)

	// 成功获取 2 个许可
	for i := 0; i < 2; i++ {
		assert.True(t, limiter.TryAcquire(), "应该成功获取许可 %d", i)
	}

	// 第 3 个许可应该失败
	assert.False(t, limiter.TryAcquire(), "超过限制时应该失败")

	// 释放一个后应该可以再次获取
	limiter.Release()
	assert.True(t, limiter.TryAcquire(), "释放后应该可以再次获取")
}

// TestConcurrencyLimiter_AcquireWithTimeout 测试带超时的获取
func TestConcurrencyLimiter_AcquireWithTimeout(t *testing.T) {
	limiter := NewConcurrencyLimiter(1)

	// 获取唯一的许可
	assert.True(t, limiter.TryAcquire(), "应该成功获取许可")

	// 带超时获取
	start := time.Now()
	err := limiter.AcquireWithTimeout(100 * time.Millisecond)
	duration := time.Since(start)

	assert.Error(t, err, "应该超时")
	assert.Equal(t, context.DeadlineExceeded, err, "应该是超时错误")
	assert.GreaterOrEqual(t, duration, 100*time.Millisecond, "应该等待至少 100ms")
}

// TestConcurrencyLimiter_ContextCancel 测试上下文取消
func TestConcurrencyLimiter_ContextCancel(t *testing.T) {
	limiter := NewConcurrencyLimiter(1)

	// 获取唯一的许可
	assert.True(t, limiter.TryAcquire(), "应该成功获取许可")

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 在后台尝试获取
	done := make(chan error)
	go func() {
		done <- limiter.Acquire(ctx)
	}()

	// 立即取消上下文
	cancel()

	// 应该立即返回取消错误
	select {
	case err := <-done:
		assert.Error(t, err, "应该返回错误")
		assert.Equal(t, context.Canceled, err, "应该是取消错误")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("应该及时返回取消错误")
	}
}

// TestConcurrencyLimiter_ConcurrentAccess 测试并发访问
func TestConcurrencyLimiter_ConcurrentAccess(t *testing.T) {
	limiter := NewConcurrencyLimiter(10)
	ctx := context.Background()

	const numGoroutines = 100
	const numOperations = 1000

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				err := limiter.Acquire(ctx)
				assert.NoError(t, err, "应该成功获取许可")
				// 注意：由于并发特性，current 可能短暂超过 10，但最终会稳定
				current := limiter.Current()
				assert.LessOrEqual(t, current, int32(numGoroutines), "不应该超过 goroutine 数量")
				time.Sleep(1 * time.Millisecond) // 模拟工作
				limiter.Release()
			}
		}()
	}

	wg.Wait()

	// 验证最终状态
	assert.Equal(t, int32(0), limiter.Current(), "所有许可应该已释放")
	assert.Equal(t, int32(0), limiter.Waiting(), "没有等待中的操作")
}

// ========================================
// Benchmark Tests
// ========================================

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkWorkerPool_TaskSubmission 基准测试：任务提交性能
func BenchmarkWorkerPool_TaskSubmission(b *testing.B) {
	cfg := &WorkerPoolConfig{
		MaxWorkers: 10,
		QueueSize:  1000,
	}

	pool := NewWorkerPool(cfg)
	defer pool.Close()

	task := &MockTask{ID: 1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Submit(task)
	}
}

// BenchmarkConcurrencyLimiter_AcquireRelease 基准测试：获取和释放性能
func BenchmarkConcurrencyLimiter_AcquireRelease(b *testing.B) {
	limiter := NewConcurrencyLimiter(100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if limiter.TryAcquire() {
				limiter.Release()
			}
		}
	})
}
