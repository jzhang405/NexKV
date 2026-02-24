package concurrency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParallelExecute(t *testing.T) {
	t.Run("empty tasks", func(t *testing.T) {
		err := ParallelExecute(context.Background(), 0, 10, func(ctx context.Context, i int) error {
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("all success", func(t *testing.T) {
		var counter atomic.Int32
		err := ParallelExecute(context.Background(), 100, 10, func(ctx context.Context, i int) error {
			counter.Add(1)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, int32(100), counter.Load())
	})

	t.Run("with error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		err := ParallelExecute(context.Background(), 10, 5, func(ctx context.Context, i int) error {
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
		err := ParallelExecute(context.Background(), 100, 5, func(ctx context.Context, i int) error {
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

		err := ParallelExecute(ctx, 100, 5, func(ctx context.Context, i int) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		}, WithFailFast(true))
		// 上下文已取消，应返回错误
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestParallelExecuteWithResult(t *testing.T) {
	t.Run("empty tasks", func(t *testing.T) {
		results := ParallelExecuteWithResult[int](context.Background(), 0, 10, func(ctx context.Context, i int) (int, error) {
			return 0, nil
		})
		assert.Nil(t, results)
	})

	t.Run("all success", func(t *testing.T) {
		results := ParallelExecuteWithResult(context.Background(), 10, 5, func(ctx context.Context, i int) (int, error) {
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
		results := ParallelExecuteWithResult(context.Background(), 10, 5, func(ctx context.Context, i int) (int, error) {
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
	t.Run("empty args", func(t *testing.T) {
		err := ParallelExecuteWithArg(context.Background(), []int{}, 10, func(ctx context.Context, arg int) error {
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("with args", func(t *testing.T) {
		args := []int{1, 2, 3, 4, 5}
		var sum atomic.Int32
		err := ParallelExecuteWithArg(context.Background(), args, 2, func(ctx context.Context, arg int) error {
			sum.Add(int32(arg))
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, int32(15), sum.Load())
	})
}

func TestParallelExecuteWithArgAndResult(t *testing.T) {
	t.Run("empty args", func(t *testing.T) {
		results := ParallelExecuteWithArgAndResult[int, int](context.Background(), []int{}, 10, func(ctx context.Context, arg int) (int, error) {
			return 0, nil
		})
		assert.Nil(t, results)
	})

	t.Run("with args and results", func(t *testing.T) {
		args := []int{1, 2, 3, 4, 5}
		results := ParallelExecuteWithArgAndResult(context.Background(), args, 2, func(ctx context.Context, arg int) (int, error) {
			return arg * 10, nil
		})
		require.Len(t, results, 5)

		resultMap := make(map[int]int)
		for _, r := range results {
			resultMap[r.Value/10] = r.Value
		}
		assert.Equal(t, 10, resultMap[1])
		assert.Equal(t, 20, resultMap[2])
		assert.Equal(t, 30, resultMap[3])
		assert.Equal(t, 40, resultMap[4])
		assert.Equal(t, 50, resultMap[5])
	})
}

func TestParallelConfig(t *testing.T) {
	t.Run("unlimited concurrency", func(t *testing.T) {
		cfg := &ParallelConfig{}
		for _, opt := range []ParallelOption{WithMaxConcurrent(0)} {
			opt(cfg)
		}
		// 0 表示无限制，实际使用时会被设为 taskCount
		assert.Equal(t, 0, cfg.MaxConcurrent)
	})

	t.Run("with options", func(t *testing.T) {
		cfg := &ParallelConfig{}
		WithMaxConcurrent(100)(cfg)
		WithFailFast(true)(cfg)
		assert.Equal(t, 100, cfg.MaxConcurrent)
		assert.True(t, cfg.FailFast)
	})
}
