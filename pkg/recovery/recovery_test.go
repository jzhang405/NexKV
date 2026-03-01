// Package recovery 测试统一 panic 恢复机制
package recovery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSafe 测试 Safe 函数
func TestSafe(t *testing.T) {
	t.Run("正常执行", func(t *testing.T) {
		called := false
		err := Safe(func() {
			called = true
		})
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("捕获 panic", func(t *testing.T) {
		panicValue := "test panic"
		err := Safe(func() {
			panic(panicValue)
		})
		assert.Error(t, err)
		// 直接类型断言
		panicErr, ok := err.(*PanicError)
		assert.True(t, ok)
		assert.Equal(t, panicValue, panicErr.R)
		assert.NotEmpty(t, panicErr.Stack)
	})

	t.Run("自定义处理器", func(t *testing.T) {
		var capturedR any
		var capturedStack []byte
		customHandler := func(r any, stack []byte) {
			capturedR = r
			capturedStack = stack
		}

		err := Safe(func() {
			panic("custom")
		}, customHandler)

		assert.Error(t, err)
		assert.Equal(t, "custom", capturedR)
		assert.NotEmpty(t, capturedStack)
	})
}

// TestSafeContext 测试 SafeContext 函数
func TestSafeContext(t *testing.T) {
	t.Run("正常执行", func(t *testing.T) {
		ctx := context.Background()
		called := false
		err := SafeContext(ctx, func(ctx context.Context) {
			called = true
		})
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("捕获 panic", func(t *testing.T) {
		ctx := context.Background()
		panicValue := errors.New("context panic")
		err := SafeContext(ctx, func(ctx context.Context) {
			panic(panicValue)
		})
		assert.Error(t, err)
		// 直接类型断言
		panicErr, ok := err.(*PanicError)
		assert.True(t, ok)
		assert.Equal(t, panicValue, panicErr.R)
	})

	t.Run("上下文取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		called := false
		err := SafeContext(ctx, func(ctx context.Context) {
			called = true
		})
		// SafeContext 本身不检查 context 取消，只捕获 panic
		// 所以这里应该没有错误
		assert.NoError(t, err)
		assert.True(t, called)
	})
}

// TestPanicError 测试 PanicError 类型
func TestPanicError(t *testing.T) {
	t.Run("Error 方法", func(t *testing.T) {
		err := &PanicError{R: "test panic"}
		assert.Contains(t, err.Error(), "test panic")
	})

	t.Run("Unwrap 返回原始 error", func(t *testing.T) {
		originalErr := errors.New("original")
		err := &PanicError{R: originalErr}
		assert.Equal(t, originalErr, err.Unwrap())
	})

	t.Run("Unwrap 非 error 类型返回 nil", func(t *testing.T) {
		err := &PanicError{R: "string panic"}
		assert.Nil(t, err.Unwrap())
	})
}

// TestGo 测试 Go 函数
func TestGo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine test in short mode")
	}

	done := make(chan struct{})
	Go(func() {
		close(done)
	})

	select {
	case <-done:
		// 成功
	case <-time.After(time.Second):
		t.Fatal("goroutine did not complete")
	}
}

// TestGoContext 测试 GoContext 函数
func TestGoContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine test in short mode")
	}

	ctx := context.Background()
	done := make(chan struct{})
	GoContext(ctx, func(ctx context.Context) {
		close(done)
	})

	select {
	case <-done:
		// 成功
	case <-time.After(time.Second):
		t.Fatal("goroutine did not complete")
	}
}

// TestGoPanicRecovery 测试 Go 函数的 panic 恢复
func TestGoPanicRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping panic test in short mode")
	}

	done := make(chan struct{})
	Go(func() {
		defer close(done)
		panic("goroutine panic")
	})

	select {
	case <-done:
		// panic 被恢复，goroutine 正常退出
	case <-time.After(time.Second):
		t.Fatal("goroutine did not complete (panic not recovered?)")
	}

	// 等待处理器执行
	time.Sleep(10 * time.Millisecond)
}

// TestGetPanicCount 测试 panic 计数
func TestGetPanicCount(t *testing.T) {
	// 重置计数
	ResetPanicCount()
	assert.Equal(t, int64(0), GetPanicCount())

	// 发生 panic 后计数增加
	_ = Safe(func() {
		panic("count test")
	})

	// 计数应该增加了
	assert.Equal(t, int64(1), GetPanicCount())
}

// TestConcurrentSafe 测试并发安全性
func TestConcurrentSafe(t *testing.T) {
	const goroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var panicCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := Safe(func() {
					if j%10 == 0 {
						panic("test panic")
					}
					successCount.Add(1)
				})
				if err != nil {
					panicCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Success: %d, Panics: %d", successCount.Load(), panicCount.Load())
	assert.Greater(t, successCount.Load(), int64(0))
	assert.Greater(t, panicCount.Load(), int64(0))
}

// TestSafe_NilFunction 测试 nil 函数处理
func TestSafe_NilFunction(t *testing.T) {
	// nil 函数会导致 panic，应该被捕获
	err := Safe(nil)
	assert.Error(t, err)
	_, ok := err.(*PanicError)
	assert.True(t, ok)
}
