// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
// AntsPoolExecutor 测试
// ==========================================

func TestAntsPoolExecutor_Submit(t *testing.T) {
	executor, err := NewAntsPoolExecutor(10)
	if err != nil {
		t.Fatalf("NewAntsPoolExecutor() error: %v", err)
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

func TestAntsPoolExecutor_SubmitAfterClose(t *testing.T) {
	executor, _ := NewAntsPoolExecutor(10)
	executor.Close()

	err := executor.Submit(context.Background(), func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should return error")
	}
}

// ==========================================
// AntsFuncExecutor 测试
// ==========================================

func TestAntsFuncExecutor_Invoke(t *testing.T) {
	var counter int32
	var wg sync.WaitGroup

	handler := func(i interface{}) {
		if n, ok := i.(int); ok {
			atomic.AddInt32(&counter, int32(n))
			wg.Done() // 在 handler 中调用 Done
		}
	}

	executor, err := NewAntsFuncExecutor(10, handler)
	if err != nil {
		t.Fatalf("NewAntsFuncExecutor() error: %v", err)
	}
	defer executor.Close()

	for i := 0; i < 10; i++ {
		wg.Add(1)
		err := executor.Invoke(context.Background(), 1)
		if err != nil {
			t.Errorf("Invoke() error: %v", err)
			wg.Done() // 如果提交失败，需要手动 Done
		}
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&counter) != 10 {
		t.Errorf("counter = %d, want 10", counter)
	}
}

func TestAntsFuncExecutor_Submit(t *testing.T) {
	executor, err := NewAntsFuncExecutor(10, func(i interface{}) {})
	if err != nil {
		t.Fatalf("NewAntsFuncExecutor() error: %v", err)
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

func TestAntsFuncExecutor_InvokeAfterClose(t *testing.T) {
	executor, _ := NewAntsFuncExecutor(10, func(i interface{}) {})
	executor.Close()

	err := executor.Invoke(context.Background(), 1)
	if err == nil {
		t.Error("Invoke() after Close() should return error")
	}
}

// ==========================================
// AntsMultiExecutor 测试
// ==========================================

func TestAntsMultiExecutor_Submit(t *testing.T) {
	executor, err := NewAntsMultiExecutor(4, 10)
	if err != nil {
		t.Fatalf("NewAntsMultiExecutor() error: %v", err)
	}
	defer executor.Close()

	var executed int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
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

	if atomic.LoadInt32(&executed) != 100 {
		t.Errorf("executed = %d, want 100", executed)
	}
}

func TestAntsMultiExecutor_SubmitAfterClose(t *testing.T) {
	executor, _ := NewAntsMultiExecutor(2, 10)
	executor.Close()

	err := executor.Submit(context.Background(), func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should return error")
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

func BenchmarkAntsPoolExecutor_Submit(b *testing.B) {
	executor, _ := NewAntsPoolExecutor(100)
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

func BenchmarkAntsMultiExecutor_Submit(b *testing.B) {
	executor, _ := NewAntsMultiExecutor(4, 100)
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}
