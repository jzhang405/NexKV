// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"errors"
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
// TaskPoolProvider 基础测试
// ==========================================

func TestAntsTaskExecutorProvider_BasicSubmit(t *testing.T) {
	config := &ProviderConfig{
		Capacity: 10,
	}

	provider, err := NewAntsTaskExecutorProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("Submit simple task", func(t *testing.T) {
		done := make(chan bool)
		ctx := context.Background()

		err := provider.Submit(ctx, PriorityNormal, func(ctx context.Context) {
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

	t.Run("Submit with different priorities", func(t *testing.T) {
		done := make(chan bool, 3)
		ctx := context.Background()

		// 提交不同优先级的任务
		_ = provider.Submit(ctx, PriorityHigh, func(ctx context.Context) {
			done <- true
		})
		_ = provider.Submit(ctx, PriorityNormal, func(ctx context.Context) {
			done <- true
		})
		_ = provider.Submit(ctx, PriorityLow, func(ctx context.Context) {
			done <- true
		})

		// 等待所有任务完成
		count := 0
		timeout := time.After(time.Second)
		for {
			select {
			case <-done:
				count++
				if count == 3 {
					return // 所有任务完成
				}
			case <-timeout:
				if count != 3 {
					t.Errorf("expected 3 tasks, got %d", count)
				}
				return
			}
		}
	})
}
