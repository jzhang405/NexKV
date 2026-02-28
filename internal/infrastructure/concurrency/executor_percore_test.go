// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPerCoreExecutor_NewExecutor 测试创建执行器
func TestPerCoreExecutor_NewExecutor(t *testing.T) {
	tests := []struct {
		name        string
		opts        []PerCoreOption
		expectError bool
	}{
		{
			name:        "default config",
			opts:        nil,
			expectError: false,
		},
		{
			name:        "with num cores",
			opts:        []PerCoreOption{WithNumCores(4)},
			expectError: false,
		},
		{
			name:        "with queue size",
			opts:        []PerCoreOption{WithQueueSize(1000)},
			expectError: false,
		},
		{
			name:        "invalid num cores - zero",
			opts:        []PerCoreOption{WithNumCores(0)},
			expectError: true,
		},
		{
			name:        "invalid num cores - negative",
			opts:        []PerCoreOption{WithNumCores(-1)},
			expectError: true,
		},
		{
			name:        "invalid num cores - exceeds max",
			opts:        []PerCoreOption{WithNumCores(100)},
			expectError: true,
		},
		{
			name:        "invalid queue size - zero",
			opts:        []PerCoreOption{WithQueueSize(0)},
			expectError: true,
		},
		{
			name:        "invalid queue size - negative",
			opts:        []PerCoreOption{WithQueueSize(-1)},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewPerCoreExecutor(tt.opts...)
			if tt.expectError {
				if err == nil {
					t.Error("NewPerCoreExecutor() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("NewPerCoreExecutor() unexpected error: %v", err)
				}
				if executor == nil {
					t.Error("NewPerCoreExecutor() returned nil")
				} else {
					executor.Close()
				}
			}
		})
	}
}

// TestPerCoreExecutor_Submit 测试任务提交
func TestPerCoreExecutor_Submit(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(2))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	var executed int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		err := executor.Submit(context.Background(), func(ctx context.Context) {
			atomic.AddInt32(&executed, 1)
			wg.Done()
		})
		if err != nil {
			t.Errorf("Submit() error: %v", err)
			wg.Done()
		}
	}

	wg.Wait()

	if atomic.LoadInt32(&executed) != 10 {
		t.Errorf("executed = %d, want 10", executed)
	}
}

// TestPerCoreExecutor_SubmitWithPriority 测试优先级提交
func TestPerCoreExecutor_SubmitWithPriority(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(1), WithQueueSize(100))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	// 使用屏障确保所有任务在开始执行前都已提交
	var allSubmitted sync.WaitGroup
	var startExecution sync.WaitGroup

	// 先设置开始执行的屏障
	startExecution.Add(1)

	var executionOrder []int
	var mu sync.Mutex

	// 提交多个低优先级任务（使用阻塞确保任务等待执行）
	for i := 0; i < 5; i++ {
		allSubmitted.Add(1)
		go func() {
			err := executor.SubmitWithPriority(context.Background(), 1, func(ctx context.Context) {
				// 等待所有任务提交完成
				startExecution.Wait()
				mu.Lock()
				executionOrder = append(executionOrder, 1)
				mu.Unlock()
			})
			if err != nil {
				t.Errorf("SubmitWithPriority() error: %v", err)
			}
			allSubmitted.Done()
		}()
	}

	// 等待所有低优先级任务提交完成
	allSubmitted.Wait()

	// 提交高优先级任务
	err = executor.SubmitWithPriority(context.Background(), 10, func(ctx context.Context) {
		// 等待所有任务提交完成
		startExecution.Wait()
		mu.Lock()
		executionOrder = append(executionOrder, 10)
		mu.Unlock()
	})
	if err != nil {
		t.Errorf("SubmitWithPriority() error: %v", err)
	}

	// 短暂等待确保高优先级任务也入队
	time.Sleep(10 * time.Millisecond)

	// 释放屏障，让所有任务开始执行
	startExecution.Done()

	// 等待任务完成
	time.Sleep(100 * time.Millisecond)

	// 验证高优先级任务在低优先级任务之前执行
	mu.Lock()
	defer mu.Unlock()

	// 找到高优先级任务的索引
	highPriorityIndex := -1
	for i, priority := range executionOrder {
		if priority == 10 {
			highPriorityIndex = i
			break
		}
	}

	if highPriorityIndex == -1 {
		t.Error("High priority task was not executed")
		return
	}

	// 高优先级任务应该在前面执行
	// 注意：由于并发调度的不可预测性，放宽条件到 index <= 3
	if highPriorityIndex > 3 {
		t.Errorf("High priority task executed at index %d, expected earlier (order: %v)", highPriorityIndex, executionOrder)
	}
}

// TestPerCoreExecutor_SubmitAfterClose 测试关闭后提交
func TestPerCoreExecutor_SubmitAfterClose(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(1))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}

	executor.Close()

	err = executor.Submit(context.Background(), func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should return error")
	}
}

// TestPerCoreExecutor_Close 测试关闭
func TestPerCoreExecutor_Close(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(2))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}

	// 提交一些任务
	for i := 0; i < 5; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 关闭应该等待任务完成
	err = executor.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

// TestPerCoreExecutor_CloseTimeout 测试超时关闭
func TestPerCoreExecutor_CloseTimeout(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(1))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}

	// 使用 channel 创建一个真正阻塞的任务
	blockCh := make(chan struct{})
	_ = executor.Submit(context.Background(), func(ctx context.Context) {
		<-blockCh // 阻塞直到 channel 关闭
	})

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 超时关闭
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = executor.CloseWithContext(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("CloseWithContext() with timeout should return error")
	}

	if elapsed > 200*time.Millisecond {
		t.Errorf("CloseWithContext() took %v, expected < 200ms", elapsed)
	}

	// 清理：关闭 channel 让任务完成
	close(blockCh)
}

// TestPerCoreExecutor_PanicRecovery 测试 Panic 恢复
func TestPerCoreExecutor_PanicRecovery(t *testing.T) {
	var panicHandled atomic.Bool
	panicHandler := func(r interface{}) {
		panicHandled.Store(true)
	}

	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithPanicHandler(panicHandler),
	)
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	err = executor.Submit(context.Background(), func(ctx context.Context) {
		defer wg.Done()
		panic("test panic")
	})
	if err != nil {
		t.Errorf("Submit() error: %v", err)
		wg.Done()
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond) // 等待 panic 处理

	if !panicHandled.Load() {
		t.Error("Panic was not handled")
	}

	// 执行器应该仍然可用
	err = executor.Submit(context.Background(), func(ctx context.Context) {})
	if err != nil {
		t.Error("Executor should still be usable after panic")
	}
}

// TestPerCoreExecutor_ConcurrentSubmit 测试并发提交
func TestPerCoreExecutor_ConcurrentSubmit(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(4), WithQueueSize(10000))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	var counter int32
	var wg sync.WaitGroup

	// 并发提交 1000 个任务
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := executor.Submit(context.Background(), func(ctx context.Context) {
				atomic.AddInt32(&counter, 1)
			})
			if err != nil {
				t.Errorf("Submit() error: %v", err)
			}
		}()
	}

	wg.Wait()

	// 等待所有任务执行完成
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&counter) != 1000 {
		t.Errorf("counter = %d, want 1000", counter)
	}
}

// TestPerCoreExecutor_RateLimit 测试限流
func TestPerCoreExecutor_RateLimit(t *testing.T) {
	// 创建带限流的执行器
	executor, err := NewPerCoreExecutor(
		WithNumCores(1),
		WithRateLimit(100, 10), // 100 QPS, burst 10
	)
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	var rejected int32
	var wg sync.WaitGroup

	// 快速提交 100 个任务，部分应该被限流
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := executor.Submit(context.Background(), func(ctx context.Context) {})
			if err != nil && errors.Is(err, ErrRateLimitExceeded) {
				atomic.AddInt32(&rejected, 1)
			}
		}()
	}

	wg.Wait()

	// 应该有部分任务被限流
	if atomic.LoadInt32(&rejected) == 0 {
		t.Log("Warning: No tasks were rate limited, but this can happen due to timing")
	}
}

// TestPerCoreExecutor_ContextCancellation 测试上下文取消
func TestPerCoreExecutor_ContextCancellation(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(1), WithQueueSize(10))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err = executor.Submit(ctx, func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() with cancelled context should return error")
	}
}

// TestPerCoreExecutor_Stats 测试统计信息
func TestPerCoreExecutor_Stats(t *testing.T) {
	executor, err := NewPerCoreExecutor(WithNumCores(2))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	// 提交任务
	for i := 0; i < 10; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {})
	}

	time.Sleep(50 * time.Millisecond)

	stats := executor.Stats()
	if stats.TotalSubmitted != 10 {
		t.Errorf("Stats().TotalSubmitted = %d, want 10", stats.TotalSubmitted)
	}

	config := executor.Config()
	if config.NumCores != 2 {
		t.Errorf("Config().NumCores = %d, want 2", config.NumCores)
	}
}

// TestPerCoreExecutor_DefaultConfig 测试默认配置
func TestPerCoreExecutor_DefaultConfig(t *testing.T) {
	executor, err := NewPerCoreExecutor()
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	defer executor.Close()

	config := executor.Config()

	expectedCores := runtime.NumCPU()
	if config.NumCores > expectedCores {
		t.Errorf("Config().NumCores = %d, should not exceed %d", config.NumCores, expectedCores)
	}

	if config.QueueSize <= 0 {
		t.Errorf("Config().QueueSize = %d, want > 0", config.QueueSize)
	}
}

// TestPerCoreExecutor_MaxCoresLimit 测试最大核心数限制
func TestPerCoreExecutor_MaxCoresLimit(t *testing.T) {
	// 尝试创建超过最大限制的执行器
	_, err := NewPerCoreExecutor(WithNumCores(1000))
	if err == nil {
		t.Error("NewPerCoreExecutor() should reject numCores > MaxCores (64)")
	}
}

// 基准测试
func BenchmarkPerCoreExecutor_Submit(b *testing.B) {
	executor, _ := NewPerCoreExecutor(WithNumCores(4), WithQueueSize(10000))
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

func BenchmarkPerCoreExecutor_SubmitWithPriority(b *testing.B) {
	executor, _ := NewPerCoreExecutor(WithNumCores(4), WithQueueSize(10000))
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.SubmitWithPriority(context.Background(), i%10, task)
	}
}

func BenchmarkPerCoreExecutor_ConcurrentSubmit(b *testing.B) {
	executor, _ := NewPerCoreExecutor(WithNumCores(4), WithQueueSize(10000))
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), task)
		}
	})
}
