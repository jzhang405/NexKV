// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAntsGoroutineProvider_Submit(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	err = provider.Submit(ctx, func(ctx context.Context) {
		atomic.AddInt32(&executed, 1)
	})

	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Errorf("expected 1 execution, got %d", atomic.LoadInt32(&executed))
	}
}

func TestAntsGoroutineProvider_SubmitWithArg(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var result int32

	err = provider.SubmitWithArg(ctx, func(ctx context.Context, arg any) {
		atomic.StoreInt32(&result, int32(arg.(int)))
	}, 42)

	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&result) != 42 {
		t.Errorf("expected result 42, got %d", atomic.LoadInt32(&result))
	}
}

func TestAntsGoroutineProvider_SubmitWithResult(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
		return "hello world", nil
	})

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != "hello world" {
		t.Errorf("expected 'hello world', got: %v", val)
	}
}

func TestAntsGoroutineProvider_SubmitWithPriority(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	err = provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {
		atomic.StoreInt32(&executed, 1)
	})

	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("expected task to be executed")
	}
}

func TestAntsGoroutineProvider_SubmitDelayed(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	err = provider.SubmitDelayed(ctx, 50*time.Millisecond, func(ctx context.Context) {
		atomic.StoreInt32(&executed, 1)
	})

	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// 等待延迟任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("expected task to be executed after delay")
	}
}

func TestAntsGoroutineProvider_SubmitBatch(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var counter int32

	tasks := []func(context.Context){
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
	}

	err = provider.SubmitBatch(ctx, tasks)
	if err != nil {
		t.Fatalf("failed to submit batch: %v", err)
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&counter) != 3 {
		t.Errorf("expected counter 3, got %d", atomic.LoadInt32(&counter))
	}
}

func TestAntsGoroutineProvider_SubmitBatchAllErrors(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var counter int32

	tasks := []func(context.Context){
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
		func(ctx context.Context) { atomic.AddInt32(&counter, 1) },
	}

	errors := provider.SubmitBatchAllErrors(ctx, tasks)
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d", len(errors))
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&counter) != 3 {
		t.Errorf("expected counter 3, got %d", atomic.LoadInt32(&counter))
	}
}

func TestAntsGoroutineProvider_Stats(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(&ProviderConfig{
		Capacity: 10,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	stats := provider.Stats()
	if stats.Capacity != 10 {
		t.Errorf("expected capacity 10, got %d", stats.Capacity)
	}
}

func TestAntsGoroutineProvider_Health(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 健康状态
	if provider.Health() != HealthStatusHealthy {
		t.Error("expected healthy status")
	}

	provider.Close()

	// 关闭后不健康
	if provider.Health() != HealthStatusUnhealthy {
		t.Error("expected unhealthy status after close")
	}
}

func TestAntsGoroutineProvider_CloseWithTimeout(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	err = provider.CloseWithTimeout(1 * time.Second)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAntsGoroutineProvider_ClosedPool(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	provider.Close()

	ctx := context.Background()

	// 提交到已关闭的池应该返回错误
	err = provider.Submit(ctx, func(ctx context.Context) {})
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got: %v", err)
	}
}

// ==========================================
// 泛型辅助函数测试
// ==========================================

func TestSubmitWithArg_Generic(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var result int32

	err = SubmitWithArg(ctx, provider, func(ctx context.Context, arg int) {
		atomic.StoreInt32(&result, int32(arg))
	}, 42)

	if err != nil {
		t.Fatalf("failed to submit: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&result) != 42 {
		t.Errorf("expected result 42, got %d", atomic.LoadInt32(&result))
	}
}

func TestSubmitWithResult_Generic(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := SubmitWithResult(ctx, provider, func(ctx context.Context) (string, error) {
		return "hello", nil
	})

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != "hello" {
		t.Errorf("expected 'hello', got: %s", val)
	}
}

func TestSubmitAdvanced_Generic(t *testing.T) {
	provider, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := SubmitAdvanced(ctx, provider, func(ctx context.Context, key string) (string, error) {
		return "value:" + key, nil
	}, "test", WithPriority(PriorityHigh))

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != "value:test" {
		t.Errorf("expected 'value:test', got: %s", val)
	}
}
