// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// ==========================================
// AntsDefaultExecutor 测试
// ==========================================

func TestAntsDefaultExecutor_Submit(t *testing.T) {
	executor := NewAntsDefaultExecutor()
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

func TestAntsDefaultExecutor_SubmitAfterClose(t *testing.T) {
	executor := NewAntsDefaultExecutor()
	executor.Close()

	err := executor.Submit(context.Background(), func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should return error")
	}
}

func TestAntsDefaultExecutor_ContextCancellation(t *testing.T) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := executor.Submit(ctx, func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() with cancelled context should return error")
	}
}

// ==========================================
// 基准测试
// ==========================================

func BenchmarkAntsDefaultExecutor_Submit(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}
