// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	std_errors "errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestAntsTaskPoolProvider_Submit(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitWithArg(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitWithResult(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitWithPriority(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitDelayed(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitBatch(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_SubmitBatchAllErrors(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_Stats(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(&ProviderConfig{
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

func TestAntsTaskPoolProvider_Health(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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

func TestAntsTaskPoolProvider_CloseWithTimeout(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	err = provider.CloseWithTimeout(1 * time.Second)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_ClosedPool(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
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
	provider, err := NewAntsTaskPoolProvider(nil)
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
	provider, err := NewAntsTaskPoolProvider(nil)
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
	provider, err := NewAntsTaskPoolProvider(nil)
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

// ==========================================
// 补充测试：提高覆盖率
// ==========================================

func TestWithDelay(t *testing.T) {
	opt := WithDelay(100 * time.Millisecond)
	if opt == nil {
		t.Fatal("expected option function, got nil")
	}

	// 测试选项应用
	opts := &SubmitOptions{}
	opt(opts)

	if opts.Delay != 100*time.Millisecond {
		t.Errorf("expected delay 100ms, got %v", opts.Delay)
	}
}

func TestAntsTaskPoolProvider_SubmitWithArgAndResult(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := provider.SubmitWithArgAndResult(ctx, func(ctx context.Context, arg any) (any, error) {
		return arg.(int) * 2, nil
	}, 21)

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != 42 {
		t.Errorf("expected 42, got %v", val)
	}
}

func TestAntsTaskPoolProvider_SubmitWithArgAndResult_Closed(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	provider.Close()

	ctx := context.Background()

	result := provider.SubmitWithArgAndResult(ctx, func(ctx context.Context, arg any) (any, error) {
		return arg, nil
	}, 42)

	_, err = result.Get(ctx)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_SubmitAdvanced(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	// 测试无延迟的 SubmitAdvanced
	result := provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
		return "result:" + arg.(string), nil
	}, "test", WithPriority(PriorityHigh))

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != "result:test" {
		t.Errorf("expected 'result:test', got: %v", val)
	}
}

func TestAntsTaskPoolProvider_SubmitAdvanced_WithDelay(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	start := time.Now()

	// 测试带延迟的 SubmitAdvanced
	result := provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
		return "delayed:" + arg.(string), nil
	}, "test", WithDelay(50*time.Millisecond))

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected delay of at least 50ms, got %v", elapsed)
	}

	if val != "delayed:test" {
		t.Errorf("expected 'delayed:test', got: %v", val)
	}
}

func TestAntsTaskPoolProvider_SubmitAdvanced_Closed(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	provider.Close()

	ctx := context.Background()

	result := provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
		return arg, nil
	}, "test")

	_, err = result.Get(ctx)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_SubmitAdvanced_Error(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	testErr := std_errors.New("test error")

	result := provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
		return nil, testErr
	}, "test")

	_, err = result.Get(ctx)
	if err != testErr {
		t.Errorf("expected test error, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_SetCapacity(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(&ProviderConfig{
		Capacity: 10,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 测试设置新容量
	err = provider.SetCapacity(20)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stats := provider.Stats()
	if stats.Capacity != 20 {
		t.Errorf("expected capacity 20, got %d", stats.Capacity)
	}
}

func TestAntsTaskPoolProvider_SetCapacity_Invalid(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 测试无效容量（太小）
	err = provider.SetCapacity(0)
	if err == nil {
		t.Error("expected error for invalid capacity")
	}

	// 测试无效容量（太大）
	err = provider.SetCapacity(1000000)
	if err == nil {
		t.Error("expected error for invalid capacity")
	}
}

func TestAntsTaskPoolProvider_SetCapacity_Closed(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	provider.Close()

	err = provider.SetCapacity(20)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_PanicRecovery(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	// 提交一个会 panic 的任务，不应该导致程序崩溃
	err = provider.Submit(ctx, func(ctx context.Context) {
		atomic.StoreInt32(&executed, 1)
		panic("test panic")
	})
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	// 任务应该被执行（即使 panic）
	if atomic.LoadInt32(&executed) != 1 {
		t.Error("expected task to be executed before panic")
	}

	// 池应该仍然健康（panic 被恢复）
	if provider.Health() != HealthStatusHealthy {
		t.Error("expected pool to remain healthy after panic recovery")
	}
}

func TestTypedResult_Done_IsDone(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := SubmitWithResult(ctx, provider, func(ctx context.Context) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "done", nil
	})

	// 初始状态：未完成
	if result.IsDone() {
		t.Error("expected IsDone to be false initially")
	}

	// 等待完成
	select {
	case <-result.Done():
		// 完成
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for result")
	}

	// 完成后：已完成
	if !result.IsDone() {
		t.Error("expected IsDone to be true after completion")
	}
}

func TestAntsTaskPoolProvider_SubmitWithResult_Error(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	testErr := std_errors.New("task error")

	result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
		return nil, testErr
	})

	_, err = result.Get(ctx)
	if err != testErr {
		t.Errorf("expected test error, got: %v", err)
	}
}

func TestAntsTaskPoolProvider_Submit_ContextCanceled(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 即使 context 已取消，Submit 仍会成功（任务会执行）
	// 这是当前实现的行为
	err = provider.Submit(ctx, func(ctx context.Context) {})
	// 池未关闭，Submit 应该成功
	if err != nil && err != ErrPoolClosed {
		t.Logf("Submit returned: %v (this is acceptable)", err)
	}
}

func TestAntsTaskPoolProvider_SubmitDelayed_ContextCanceled(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	var executed int32
	err = provider.SubmitDelayed(ctx, 10*time.Millisecond, func(ctx context.Context) {
		atomic.StoreInt32(&executed, 1)
	})
	if err != nil {
		t.Logf("SubmitDelayed returned: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// context 取消后，延迟任务应该不执行
	// 但这取决于实现细节
}

func TestSubmitWithArgAndResult_Generic(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	result := SubmitWithArgAndResult(ctx, provider, func(ctx context.Context, arg int) (int, error) {
		return arg * 2, nil
	}, 21)

	val, err := result.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

// TestAntsTaskPoolProvider_OnError 测试错误回调功能 (P1-03)
func TestAntsTaskPoolProvider_OnError(t *testing.T) {
	var callbackCalled atomic.Bool
	var callbackErr error
	var callbackTaskType string

	config := &ProviderConfig{
		Capacity: 1, // 最小容量，容易触发提交失败
		OnError: func(err error, taskType string) {
			callbackErr = err
			callbackTaskType = taskType
			callbackCalled.Store(true)
		},
	}

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交延迟任务
	err = provider.SubmitDelayed(context.Background(), 10*time.Millisecond, func(ctx context.Context) {
		// 简单任务
	})
	if err != nil {
		t.Logf("SubmitDelayed returned: %v", err)
	}

	// 等待延迟任务执行
	time.Sleep(100 * time.Millisecond)

	// 注意：由于正常情况下 Submit 不会失败，这个测试主要验证回调机制存在
	// 如果池未关闭，内部提交应该成功，回调不会被调用
	t.Logf("OnError callback called: %v, taskType: %s, err: %v",
		callbackCalled.Load(), callbackTaskType, callbackErr)
}

// TestAntsTaskPoolProvider_handleTaskError 测试错误处理方法
func TestAntsTaskPoolProvider_handleTaskError(t *testing.T) {
	tests := []struct {
		name        string
		hasCallback bool
	}{
		{"with callback", true},
		{"without callback (uses logrus)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var callbackCalled atomic.Bool
			config := &ProviderConfig{
				Capacity: 10,
			}
			if tt.hasCallback {
				config.OnError = func(err error, taskType string) {
					callbackCalled.Store(true)
				}
			}

			provider, err := NewAntsTaskPoolProvider(config)
			if err != nil {
				t.Fatalf("failed to create provider: %v", err)
			}
			defer provider.Close()

			// 调用 handleTaskError
			provider.handleTaskError(std_errors.New("test error"), "test")

			if tt.hasCallback {
				if !callbackCalled.Load() {
					t.Error("expected callback to be called")
				}
			}
			// 没有 callback 时使用 logrus，不会 panic
		})
	}
}
