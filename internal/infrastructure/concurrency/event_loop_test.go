// Package concurrency RunLoop Worker 测试
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 辅助函数
// ==========================================

// simulateWork 模拟短任务（<1μs）
func simulateWork() {
	sum := 0
	for i := 0; i < 10; i++ {
		sum += i
	}
	_ = sum
}

// ==========================================
// 基准测试：RunLoop Worker
// ==========================================

// Benchmark_RunLoop_TrySubmit TrySubmit 基准
func Benchmark_RunLoop_TrySubmit(b *testing.B) {
	worker := NewEventLoop(0, 10000)
	worker.Start()
	defer worker.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := worker.TrySubmit(func() {
			simulateWork()
		})
		if err != nil {
			// 队列满时跳过
			continue
		}
	}
}

// Benchmark_RunLoop_SubmitAndWait SubmitAndWait 基准
func Benchmark_RunLoop_SubmitAndWait(b *testing.B) {
	worker := NewEventLoop(0, 10000)
	worker.Start()
	defer worker.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := worker.SubmitAndWait(func() {
			simulateWork()
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark_RunLoop_SubmitBatch_10 批量提交基准（批量大小 10）
func Benchmark_RunLoop_SubmitBatch_10(b *testing.B) {
	worker := NewEventLoop(0, 10000)
	worker.Start()
	defer worker.Close()

	batchSize := 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tasks := make([]func(), batchSize)
		for j := range tasks {
			tasks[j] = func() { simulateWork() }
		}
		err := worker.SubmitBatch(tasks)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark_RunLoop_SubmitBatch_100 批量提交基准（批量大小 100）
func Benchmark_RunLoop_SubmitBatch_100(b *testing.B) {
	worker := NewEventLoop(0, 10000)
	worker.Start()
	defer worker.Close()

	batchSize := 100

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tasks := make([]func(), batchSize)
		for j := range tasks {
			tasks[j] = func() { simulateWork() }
		}
		err := worker.SubmitBatch(tasks)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark_SyncExecution 同步执行基准
func Benchmark_SyncExecution(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		simulateWork()
	}
}

// Benchmark_TaskMode_SingleOp Task 模式单操作基准
func Benchmark_TaskMode_SingleOp(b *testing.B) {
	executor, err := NewPerCoreExecutor(
		WithQueueSize(1000),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer executor.Close()

	sourceID := model.NewSourceShard("test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(1)
		err := executor.Submit(
			context.Background(),
			sourceID,
			model.TaskPriorityNormal,
			func(ctx context.Context) {
				defer wg.Done()
				simulateWork()
			},
		)
		if err != nil {
			b.Fatal(err)
		}
		wg.Wait()
	}
}

// Benchmark_AllComparison 全面对比测试
func Benchmark_AllComparison(b *testing.B) {
	b.Run("Sync", Benchmark_SyncExecution)
	b.Run("TaskMode", Benchmark_TaskMode_SingleOp)
	b.Run("RunLoop_TrySubmit", Benchmark_RunLoop_TrySubmit)
	b.Run("RunLoop_SubmitAndWait", Benchmark_RunLoop_SubmitAndWait)
	b.Run("RunLoop_Batch_10", Benchmark_RunLoop_SubmitBatch_10)
	b.Run("RunLoop_Batch_100", Benchmark_RunLoop_SubmitBatch_100)
}

// ==========================================
// 功能测试
// ==========================================

// Test_EventLoop_BasicFunctionality 基本功能测试
func Test_EventLoop_BasicFunctionality(t *testing.T) {
	worker := NewEventLoop(0, 10)
	worker.Start()
	defer worker.Close()

	counter := 0
	for i := 0; i < 10; i++ {
		err := worker.SubmitAndWait(func() {
			counter++
		})
		if err != nil {
			t.Fatalf("SubmitAndWait failed: %v", err)
		}
	}

	if counter != 10 {
		t.Errorf("Expected counter=10, got %d", counter)
	}
}

// Test_EventLoop_TrySubmit TrySubmit 测试
func Test_EventLoop_TrySubmit(t *testing.T) {
	worker := NewEventLoop(0, 100)
	worker.Start()
	defer worker.Close()

	counter := atomic.Int64{}
	for i := 0; i < 100; i++ {
		err := worker.TrySubmit(func() {
			counter.Add(1)
		})
		if err != nil {
			t.Fatalf("TrySubmit failed: %v", err)
		}
	}

	// 等待所有任务完成
	time.Sleep(100 * time.Millisecond)
	worker.Close()

	if counter.Load() != 100 {
		t.Errorf("Expected counter=100, got %d", counter.Load())
	}
}

// Test_EventLoop_SubmitBatch 批量提交测试
func Test_EventLoop_SubmitBatch(t *testing.T) {
	worker := NewEventLoop(0, 100)
	worker.Start()
	defer worker.Close()

	batchSize := 20
	tasks := make([]func(), batchSize)
	counter := atomic.Int64{}

	for i := range tasks {
		i := i
		tasks[i] = func() {
			counter.Add(1)
		}
	}

	err := worker.SubmitBatch(tasks)
	if err != nil {
		t.Fatalf("SubmitBatch failed: %v", err)
	}

	if counter.Load() != int64(batchSize) {
		t.Errorf("Expected counter=%d, got %d", batchSize, counter.Load())
	}
}

// Test_EventLoop_ConcurrentStress 并发压力测试
func Test_EventLoop_ConcurrentStress(t *testing.T) {
	const (
		numGoroutines   = 100
		opsPerGoroutine = 1000
	)

	worker := NewEventLoop(0, 10000)
	worker.Start()
	defer worker.Close()

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				err := worker.TrySubmit(func() {
					simulateWork()
				})
				if err != nil {
					// 队列满，忽略错误
					_ = err // SA9003: 明确忽略错误
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Concurrent stress test passed")
}

func Test_EventLoop_SubmitBatch_Debug_WithLogging(t *testing.T) {
	worker := NewEventLoop(0, 100)
	worker.Start()
	defer worker.Close()

	batchSize := 5
	tasks := make([]func(), batchSize)
	counter := atomic.Int64{}

	for i := range tasks {
		i := i
		tasks[i] = func() {
			t.Logf("Task %d executing", i)
			counter.Add(1)
		}
	}

	t.Log("Submitting batch...")
	err := worker.SubmitBatch(tasks)
	t.Logf("SubmitBatch returned: err=%v, counter=%d", err, counter.Load())

	if err != nil {
		t.Fatalf("SubmitBatch failed: %v", err)
	}

	if counter.Load() != int64(batchSize) {
		t.Errorf("Expected counter=%d, got %d", batchSize, counter.Load())
	} else {
		t.Log("Test passed!")
	}
}

func Test_EventLoop_SubmitBatch_WithDelay(t *testing.T) {
	worker := NewEventLoop(0, 100)
	worker.Start()
	defer worker.Close()

	batchSize := 20

	tasks := make([]func(), batchSize)
	counter := atomic.Int64{}

	for i := range tasks {
		i := i
		tasks[i] = func() {
			counter.Add(1)
		}
	}

	// 添加超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- worker.SubmitBatch(tasks)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SubmitBatch failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("SubmitBatch timed out after 5s, counter=%d/%d", counter.Load(), batchSize)
	}

	// Verify all tasks completed (SubmitBatch already waits)
	if counter.Load() != int64(batchSize) {
		t.Errorf("Expected counter=%d, got %d", batchSize, counter.Load())
	}
}

func Test_EventLoop_SubmitBatch_OneByOne(t *testing.T) {
	worker := NewEventLoop(0, 100)
	worker.Start()
	defer worker.Close()

	// 提交 5 个任务，逐个等待
	for i := 0; i < 5; i++ {
		tasks := []func(){
			func() {
				t.Logf("Task %d executing", i)
			},
		}

		err := worker.SubmitBatch(tasks)
		if err != nil {
			t.Fatalf("SubmitBatch %d failed: %v", i, err)
		}
		t.Logf("Batch %d completed", i)
	}

	t.Log("All batches completed successfully!")
}
