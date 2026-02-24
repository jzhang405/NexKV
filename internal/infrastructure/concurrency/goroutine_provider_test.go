// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ==========================================
// Result 类型测试
// ==========================================

func TestAnyResult_BasicOperations(t *testing.T) {
	t.Run("SetValue and Get", func(t *testing.T) {
		result := NewAnyResult()
		expected := "test value"

		// 异步设置值
		go func() {
			time.Sleep(10 * time.Millisecond)
			result.SetValue(expected)
		}()

		ctx := context.Background()
		val, err := result.Get(ctx)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != expected {
			t.Errorf("expected %v, got %v", expected, val)
		}
	})

	t.Run("SetError and Get", func(t *testing.T) {
		result := NewAnyResult()
		expectedErr := errors.New("test error")

		// 异步设置错误
		go func() {
			time.Sleep(10 * time.Millisecond)
			result.SetError(expectedErr)
		}()

		ctx := context.Background()
		_, err := result.Get(ctx)

		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("Get with timeout", func(t *testing.T) {
		result := NewAnyResult()

		// 不设置值，测试超时
		_, err := result.GetWithTimeout(50 * time.Millisecond)

		if err != context.DeadlineExceeded {
			t.Errorf("expected deadline exceeded, got %v", err)
		}
	})

	t.Run("IsDone and Done channel", func(t *testing.T) {
		result := NewAnyResult()

		if result.IsDone() {
			t.Error("expected IsDone to be false initially")
		}

		result.SetValue("done")

		if !result.IsDone() {
			t.Error("expected IsDone to be true after SetValue")
		}

		select {
		case <-result.Done():
			// 正确：通道已关闭
		default:
			t.Error("expected Done channel to be closed")
		}
	})

	t.Run("Multiple SetValue calls - idempotent", func(t *testing.T) {
		result := NewAnyResult()

		result.SetValue("first")
		result.SetValue("second") // 应该被忽略

		ctx := context.Background()
		val, _ := result.Get(ctx)

		if val != "first" {
			t.Errorf("expected 'first', got %v", val)
		}
	})
}

func TestTypedResult_TypeSafety(t *testing.T) {
	t.Run("Get with correct type", func(t *testing.T) {
		inner := NewAnyResult()
		inner.SetValue(42)

		typed := &TypedResult[int]{inner: inner}
		ctx := context.Background()

		val, err := typed.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}
	})

	t.Run("Get with wrong type - safe assertion", func(t *testing.T) {
		inner := NewAnyResult()
		inner.SetValue("string value") // 设置字符串

		typed := &TypedResult[int]{inner: inner} // 但期望 int
		ctx := context.Background()

		_, err := typed.Get(ctx)
		if err == nil {
			t.Error("expected type assertion error, got nil")
		}
	})

	t.Run("GetWithTimeout with correct type", func(t *testing.T) {
		inner := NewAnyResult()
		go func() {
			time.Sleep(10 * time.Millisecond)
			inner.SetValue("hello")
		}()

		typed := &TypedResult[string]{inner: inner}

		val, err := typed.GetWithTimeout(100 * time.Millisecond)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != "hello" {
			t.Errorf("expected 'hello', got %s", val)
		}
	})

	t.Run("GetWithTimeout with wrong type - safe assertion", func(t *testing.T) {
		inner := NewAnyResult()
		go func() {
			time.Sleep(10 * time.Millisecond)
			inner.SetValue(123) // 设置 int
		}()

		typed := &TypedResult[string]{inner: inner} // 但期望 string

		_, err := typed.GetWithTimeout(100 * time.Millisecond)
		if err == nil {
			t.Error("expected type assertion error, got nil")
		}
	})
}

// ==========================================
// GoroutineProvider 测试
// ==========================================

func TestAntsGoroutineProvider_BasicOperations(t *testing.T) {
	config := &ProviderConfig{
		Capacity:       10,
		EnablePriority: true,
		EnableDelayed:  true,
	}

	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("Submit simple task", func(t *testing.T) {
		done := make(chan bool)
		ctx := context.Background()

		err := provider.Submit(ctx, func(ctx context.Context) {
			done <- true
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case <-done:
			// 成功
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})

	t.Run("SubmitWithArg", func(t *testing.T) {
		received := make(chan any, 1)
		ctx := context.Background()

		err := provider.SubmitWithArg(ctx, func(ctx context.Context, arg any) {
			received <- arg
		}, "test arg")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case arg := <-received:
			if arg != "test arg" {
				t.Errorf("expected 'test arg', got %v", arg)
			}
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})

	t.Run("SubmitWithResult", func(t *testing.T) {
		ctx := context.Background()

		result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
			return 42, nil
		})

		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %v", val)
		}
	})

	t.Run("SubmitWithResult with error", func(t *testing.T) {
		ctx := context.Background()
		expectedErr := errors.New("task error")

		result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
			return nil, expectedErr
		})

		_, err := result.Get(ctx)
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("SubmitWithPriority", func(t *testing.T) {
		done := make(chan bool)
		ctx := context.Background()

		err := provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {
			done <- true
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case <-done:
			// 成功
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})

	t.Run("SubmitDelayed", func(t *testing.T) {
		done := make(chan bool)
		ctx := context.Background()
		start := time.Now()

		err := provider.SubmitDelayed(ctx, 100*time.Millisecond, func(ctx context.Context) {
			done <- true
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case <-done:
			elapsed := time.Since(start)
			if elapsed < 80*time.Millisecond {
				t.Errorf("task executed too early: %v", elapsed)
			}
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})
}

func TestAntsGoroutineProvider_ConcurrentSafety(t *testing.T) {
	config := &ProviderConfig{
		Capacity:       100,
		EnablePriority: true,
		EnableDelayed:  true,
	}

	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("Concurrent submits", func(t *testing.T) {
		const numTasks = 100
		var wg sync.WaitGroup
		counter := 0
		var mu sync.Mutex

		wg.Add(numTasks)
		ctx := context.Background()

		for i := 0; i < numTasks; i++ {
			err := provider.Submit(ctx, func(ctx context.Context) {
				defer wg.Done()
				mu.Lock()
				counter++
				mu.Unlock()
			})
			if err != nil {
				t.Errorf("submit failed: %v", err)
				wg.Done()
			}
		}

		wg.Wait()

		if counter != numTasks {
			t.Errorf("expected counter to be %d, got %d", numTasks, counter)
		}
	})

	t.Run("Concurrent results", func(t *testing.T) {
		const numTasks = 50
		results := make([]Result[any], numTasks)
		ctx := context.Background()

		// 提交任务
		for i := 0; i < numTasks; i++ {
			idx := i
			results[i] = provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
				return idx, nil
			})
		}

		// 并发获取结果
		var wg sync.WaitGroup
		sum := 0
		var mu sync.Mutex

		for i := 0; i < numTasks; i++ {
			wg.Add(1)
			go func(r Result[any]) {
				defer wg.Done()
				val, err := r.Get(ctx)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				mu.Lock()
				sum += val.(int)
				mu.Unlock()
			}(results[i])
		}

		wg.Wait()

		expectedSum := numTasks * (numTasks - 1) / 2
		if sum != expectedSum {
			t.Errorf("expected sum %d, got %d", expectedSum, sum)
		}
	})
}

func TestAntsGoroutineProvider_Lifecycle(t *testing.T) {
	t.Run("Close prevents new tasks", func(t *testing.T) {
		config := DefaultProviderConfig()
		provider, err := NewAntsGoroutineProvider(config)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		// 关闭 provider
		err = provider.Close()
		if err != nil {
			t.Errorf("expected no error on close, got %v", err)
		}

		// 尝试提交新任务应该失败
		ctx := context.Background()
		err = provider.Submit(ctx, func(ctx context.Context) {})
		if err != ErrPoolClosed {
			t.Errorf("expected ErrPoolClosed, got %v", err)
		}
	})

	t.Run("Stats and Health", func(t *testing.T) {
		config := DefaultProviderConfig()
		provider, err := NewAntsGoroutineProvider(config)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}
		defer provider.Close()

		stats := provider.Stats()
		if stats.Capacity != config.Capacity {
			t.Errorf("expected capacity %d, got %d", config.Capacity, stats.Capacity)
		}

		health := provider.Health()
		if health != HealthStatusHealthy {
			t.Errorf("expected healthy status, got %v", health)
		}
	})
}

// ==========================================
// 泛型辅助函数测试
// ==========================================

func TestSubmitWithArg_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitWithArg with int", func(t *testing.T) {
		received := make(chan int, 1)
		ctx := context.Background()

		err := SubmitWithArg(ctx, provider, func(ctx context.Context, val int) {
			received <- val
		}, 42)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case val := <-received:
			if val != 42 {
				t.Errorf("expected 42, got %d", val)
			}
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})

	t.Run("SubmitWithArg with string", func(t *testing.T) {
		received := make(chan string, 1)
		ctx := context.Background()

		err := SubmitWithArg(ctx, provider, func(ctx context.Context, val string) {
			received <- val
		}, "hello")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case val := <-received:
			if val != "hello" {
				t.Errorf("expected 'hello', got %s", val)
			}
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})
}

func TestSubmitWithResult_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitWithResult with int", func(t *testing.T) {
		ctx := context.Background()

		result := SubmitWithResult(ctx, provider, func(ctx context.Context) (int, error) {
			return 42, nil
		})

		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}
	})

	t.Run("SubmitWithResult with struct", func(t *testing.T) {
		type TestStruct struct {
			Name  string
			Value int
		}

		ctx := context.Background()
		expected := TestStruct{Name: "test", Value: 100}

		result := SubmitWithResult(ctx, provider, func(ctx context.Context) (TestStruct, error) {
			return expected, nil
		})

		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != expected {
			t.Errorf("expected %+v, got %+v", expected, val)
		}
	})
}

func TestSubmitWithArgAndResult_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitWithArgAndResult multiply", func(t *testing.T) {
		ctx := context.Background()

		result := SubmitWithArgAndResult(ctx, provider, func(ctx context.Context, x int) (int, error) {
			return x * 2, nil
		}, 21)

		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}
	})
}

func TestSubmitAdvanced_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitAdvanced with options", func(t *testing.T) {
		ctx := context.Background()

		result := SubmitAdvanced(ctx, provider, func(ctx context.Context, x int) (int, error) {
			return x + 10, nil
		}, 32, WithPriority(PriorityHigh))

		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %d", val)
		}
	})
}

// ==========================================
// 超时和错误处理测试
// ==========================================

// mockResult 是一个模拟的 Result[any]，永远不会完成
type mockResult struct{}

func (m *mockResult) Get(ctx context.Context) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockResult) GetWithTimeout(timeout time.Duration) (any, error) {
	return m.Get(context.Background())
}

func (m *mockResult) Done() <-chan struct{} {
	return make(chan struct{})
}

func (m *mockResult) IsDone() bool {
	return false
}

func TestWrapAnyResult_Timeout(t *testing.T) {
	t.Run("wrapAnyResult timeout", func(t *testing.T) {
		mock := &mockResult{}

		// 使用较短的超时时间进行测试
		// 注意：这会创建一个 goroutine，测试完成后需要等待超时
		result := wrapAnyResult[string](mock)

		// 等待 wrapAnyResult 内部的 goroutine 超时
		_, err := result.GetWithTimeout(100 * time.Millisecond)
		if err != context.DeadlineExceeded {
			t.Errorf("expected deadline exceeded, got %v", err)
		}
	})
}

func TestSubmitBatchWithArg_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitBatchWithArg", func(t *testing.T) {
		ctx := context.Background()
		results := make([]int, 3)
		var mu sync.Mutex

		tasks := []func(context.Context, int){
			func(ctx context.Context, arg int) {
				mu.Lock()
				results[0] = arg
				mu.Unlock()
			},
			func(ctx context.Context, arg int) {
				mu.Lock()
				results[1] = arg
				mu.Unlock()
			},
			func(ctx context.Context, arg int) {
				mu.Lock()
				results[2] = arg
				mu.Unlock()
			},
		}
		args := []int{10, 20, 30}

		err := SubmitBatchWithArg(ctx, provider, tasks, args)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		// 等待任务完成
		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()
		for i, expected := range args {
			if results[i] != expected {
				t.Errorf("expected results[%d] = %d, got %d", i, expected, results[i])
			}
		}
	})

	t.Run("SubmitBatchWithArg length mismatch", func(t *testing.T) {
		ctx := context.Background()

		tasks := []func(context.Context, int){
			func(ctx context.Context, arg int) {},
		}
		args := []int{1, 2} // 长度不匹配

		err := SubmitBatchWithArg(ctx, provider, tasks, args)
		if err != ErrTaskArgLengthMismatch {
			t.Errorf("expected ErrTaskArgLengthMismatch, got %v", err)
		}
	})
}

func TestSubmitBatchWithResult_Typed(t *testing.T) {
	config := DefaultProviderConfig()
	provider, err := NewAntsGoroutineProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("SubmitBatchWithResult", func(t *testing.T) {
		ctx := context.Background()

		tasks := []func(context.Context) (int, error){
			func(ctx context.Context) (int, error) { return 1, nil },
			func(ctx context.Context) (int, error) { return 2, nil },
			func(ctx context.Context) (int, error) { return 3, nil },
		}

		results := SubmitBatchWithResult(ctx, provider, tasks)

		if len(results) != 3 {
			t.Errorf("expected 3 results, got %d", len(results))
		}

		for i, result := range results {
			val, err := result.Get(ctx)
			if err != nil {
				t.Errorf("result %d: unexpected error: %v", i, err)
				continue
			}
			expected := i + 1
			if val != expected {
				t.Errorf("result %d: expected %d, got %d", i, expected, val)
			}
		}
	})
}

// ==========================================
// 补充缺失的测试
// ==========================================

// 注意：SubmitBatchAllErrors, SetCapacity, CloseWithTimeout 的测试
// 已在 ants_provider_test.go 中定义，这里不再重复
