package concurrency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestProvider 设置测试用的默认 provider
func setupTestProvider(t *testing.T) {
	// 使用简单的 goroutine provider 用于测试
	// 测试中使用原生 go 关键字，因为测试需要直接控制 goroutine
	SetDefaultProvider(&testGoroutineProvider{})
	t.Cleanup(func() {
		SetDefaultProvider(nil)
	})
}

// testGoroutineProvider 测试用的简单 provider
type testGoroutineProvider struct{}

func (p *testGoroutineProvider) Submit(ctx context.Context, task func(context.Context)) error {
	// 测试中使用 go 关键字直接启动 goroutine
	go task(ctx) //nolint:errcheck
	return nil
}

func (p *testGoroutineProvider) SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error {
	go task(ctx, arg) //nolint:errcheck
	return nil
}

func (p *testGoroutineProvider) SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) service.GoroutineResult[any] {
	// 简化实现，测试中不直接使用
	_ = task // 避免未使用警告
	return nil
}

func (p *testGoroutineProvider) SubmitWithArgAndResult(ctx context.Context, task func(context.Context, any) (any, error), arg any) service.GoroutineResult[any] {
	_ = task // 避免未使用警告
	return nil
}

func (p *testGoroutineProvider) SubmitWithPriority(ctx context.Context, priority model.GoroutinePriority, task func(context.Context)) error {
	go task(ctx) //nolint:errcheck
	return nil
}

func (p *testGoroutineProvider) SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error {
	go func() {
		time.Sleep(delay)
		task(ctx)
	}()
	return nil
}

func (p *testGoroutineProvider) SubmitAdvanced(ctx context.Context, task func(context.Context, any) (any, error), arg any, opts ...service.GoroutineSubmitOption) service.GoroutineResult[any] {
	go task(ctx, arg) //nolint:errcheck
	return nil
}

func (p *testGoroutineProvider) SubmitBatch(ctx context.Context, tasks []func(context.Context)) error {
	for _, task := range tasks {
		go task(ctx)
	}
	return nil
}

func (p *testGoroutineProvider) SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error {
	for i, task := range tasks {
		go task(ctx, args[i])
	}
	return nil
}

func (p *testGoroutineProvider) SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error {
	var errs []error
	for _, task := range tasks {
		go task(ctx)
	}
	return errs
}

func (p *testGoroutineProvider) SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []service.GoroutineResult[any] {
	results := make([]service.GoroutineResult[any], len(tasks))
	for i, task := range tasks {
		go func(idx int, t func(context.Context) (any, error)) {
			val, err := t(ctx)
			results[idx] = &testResult{value: val, err: err}
		}(i, task)
	}
	return results
}

func (p *testGoroutineProvider) Stats() model.GoroutinePoolStats {
	return model.GoroutinePoolStats{}
}

func (p *testGoroutineProvider) Health() model.GoroutineHealthStatus {
	return model.GoroutineHealthStatusHealthy
}

func (p *testGoroutineProvider) SetCapacity(capacity int) error {
	return nil
}

func (p *testGoroutineProvider) Close() error {
	return nil
}

func (p *testGoroutineProvider) CloseWithTimeout(timeout time.Duration) error {
	return nil
}

// testResult 测试用的 Result 实现
type testResult struct {
	value any
	err   error
	done  bool
}

func (r *testResult) Get(ctx context.Context) (any, error) {
	return r.value, r.err
}

func (r *testResult) IsDone() bool {
	return r.done
}

func TestParallelExecute(t *testing.T) {
	setupTestProvider(t)

	t.Run("empty tasks", func(t *testing.T) {
		err := ParallelExecute(context.Background(), nil, 0, 10, func(ctx context.Context, i int) error {
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("all success", func(t *testing.T) {
		var counter atomic.Int32
		err := ParallelExecute(context.Background(), nil, 100, 10, func(ctx context.Context, i int) error {
			counter.Add(1)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, int32(100), counter.Load())
	})

	t.Run("with error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		err := ParallelExecute(context.Background(), nil, 10, 5, func(ctx context.Context, i int) error {
			if i == 3 {
				return expectedErr
			}
			return nil
		})
		// 非快速失败模式，不返回错误
		assert.NoError(t, err)
	})

	t.Run("fail fast", func(t *testing.T) {
		expectedErr := errors.New("test error")
		var executed atomic.Int32
		err := ParallelExecute(context.Background(), nil, 100, 5, func(ctx context.Context, i int) error {
			executed.Add(1)
			if i == 3 {
				return expectedErr
			}
			time.Sleep(10 * time.Millisecond)
			return nil
		}, WithFailFast(true))
		assert.ErrorIs(t, err, expectedErr)
		// 快速失败应该减少执行次数
		assert.Less(t, executed.Load(), int32(100))
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		// 立即取消
		cancel()

		err := ParallelExecute(ctx, nil, 100, 5, func(ctx context.Context, i int) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		}, WithFailFast(true))
		// 上下文已取消，应返回错误
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestParallelExecuteWithResult(t *testing.T) {
	setupTestProvider(t)

	t.Run("empty tasks", func(t *testing.T) {
		results := ParallelExecuteWithResult[int](context.Background(), nil, 0, 10, func(ctx context.Context, i int) (int, error) {
			return 0, nil
		})
		assert.Nil(t, results)
	})

	t.Run("all success", func(t *testing.T) {
		results := ParallelExecuteWithResult(context.Background(), nil, 10, 5, func(ctx context.Context, i int) (int, error) {
			return i * 2, nil
		})
		require.Len(t, results, 10)
		for _, r := range results {
			assert.NoError(t, r.Err)
			assert.Equal(t, r.Index*2, r.Value)
		}
	})

	t.Run("mixed results", func(t *testing.T) {
		expectedErr := errors.New("odd error")
		results := ParallelExecuteWithResult(context.Background(), nil, 10, 5, func(ctx context.Context, i int) (int, error) {
			if i%2 == 1 {
				return 0, expectedErr
			}
			return i * 2, nil
		})
		require.Len(t, results, 10)
		for _, r := range results {
			if r.Index%2 == 1 {
				assert.ErrorIs(t, r.Err, expectedErr)
			} else {
				assert.NoError(t, r.Err)
				assert.Equal(t, r.Index*2, r.Value)
			}
		}
	})
}

func TestParallelExecuteWithArg(t *testing.T) {
	setupTestProvider(t)

	t.Run("empty args", func(t *testing.T) {
		err := ParallelExecuteWithArg(context.Background(), nil, []int{}, 10, func(ctx context.Context, arg int) error {
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("with args", func(t *testing.T) {
		args := []int{1, 2, 3, 4, 5}
		var sum atomic.Int32
		err := ParallelExecuteWithArg(context.Background(), nil, args, 2, func(ctx context.Context, arg int) error {
			sum.Add(int32(arg))
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, int32(15), sum.Load())
	})
}

func TestParallelExecuteWithArgAndResult(t *testing.T) {
	setupTestProvider(t)

	t.Run("empty args", func(t *testing.T) {
		results := ParallelExecuteWithArgAndResult[int, int](context.Background(), nil, []int{}, 10, func(ctx context.Context, arg int) (int, error) {
			return 0, nil
		})
		assert.Nil(t, results)
	})

	t.Run("with args and results", func(t *testing.T) {
		args := []int{1, 2, 3, 4, 5}
		results := ParallelExecuteWithArgAndResult(context.Background(), nil, args, 2, func(ctx context.Context, arg int) (int, error) {
			return arg * 10, nil
		})
		require.Len(t, results, 5)

		resultMap := make(map[int]int)
		for _, r := range results {
			resultMap[r.Value/10] = r.Value
		}

		for i, arg := range args {
			assert.Equal(t, arg*10, resultMap[arg], "index %d", i)
		}
	})
}

func TestDefaultProvider(t *testing.T) {
	t.Run("set and get default provider", func(t *testing.T) {
		// 初始为 nil
		assert.Nil(t, GetDefaultProvider())

		// 设置 provider
		provider := &testGoroutineProvider{}
		SetDefaultProvider(provider)

		// 获取 provider
		assert.Equal(t, provider, GetDefaultProvider())

		// 清理
		SetDefaultProvider(nil)
		assert.Nil(t, GetDefaultProvider())
	})
}
