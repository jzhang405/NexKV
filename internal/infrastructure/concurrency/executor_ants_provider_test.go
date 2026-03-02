// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	std_errors "errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAntsTaskExecutorProvider_Submit(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	err = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
		atomic.AddInt32(&executed, 1)
	})

	require.NoError(t, err)

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Errorf("expected 1 execution, got %d", atomic.LoadInt32(&executed))
	}
}

func TestAntsTaskExecutorProvider_SubmitWithPriority(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	defer provider.Close()

	ctx := context.Background()
	var executed int32

	err = provider.Submit(ctx, model.TaskPriorityHigh, func(ctx context.Context) {
		atomic.AddInt32(&executed, 1)
	})

	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Errorf("expected 1 execution, got %d", atomic.LoadInt32(&executed))
	}
}

func TestAntsTaskExecutorProvider_Stats(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	defer provider.Close()

	stats := provider.Stats()

	if stats.Capacity <= 0 {
		t.Errorf("expected positive capacity, got %d", stats.Capacity)
	}
}

func TestAntsTaskExecutorProvider_Health(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	defer provider.Close()

	health := provider.Health()

	if health != model.TaskHealthStatusHealthy {
		t.Errorf("expected healthy status, got %v", health)
	}
}

func TestAntsTaskExecutorProvider_CloseWithTimeout(t *testing.T) {
	t.Run("正常关闭", func(t *testing.T) {
		provider, err := NewAntsTaskExecutorProvider(nil)
		require.NoError(t, err)

		var executed atomic.Int32
		ctx := context.Background()

		// 提交任务并等待执行
		for i := 0; i < 10; i++ {
			_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
				executed.Add(1)
			})
		}

		// 等待任务执行完成（轮询检查）
		for i := 0; i < 100; i++ {
			if provider.Stats().Running == 0 && executed.Load() == 10 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		// 关闭应该成功
		err = provider.CloseWithTimeout(time.Second)
		require.NoError(t, err)
		require.Equal(t, int32(10), executed.Load(), "all tasks should execute")
	})

	t.Run("空池关闭", func(t *testing.T) {
		provider, err := NewAntsTaskExecutorProvider(nil)
		require.NoError(t, err)

		// 不提交任何任务，直接关闭
		err = provider.CloseWithTimeout(time.Second)
		require.NoError(t, err)
	})

	t.Run("多次关闭幂等性", func(t *testing.T) {
		provider, err := NewAntsTaskExecutorProvider(nil)
		require.NoError(t, err)

		// 第一次关闭
		err = provider.CloseWithTimeout(time.Second)
		require.NoError(t, err)

		// 第二次关闭应该也成功（幂等）
		err = provider.CloseWithTimeout(time.Second)
		require.NoError(t, err)
	})
}

func TestAntsTaskExecutorProvider_ClosedPool(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	provider.Close()

	ctx := context.Background()

	err = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
		t.Error("task should not execute after pool is closed")
	})

	if err == nil {
		t.Error("expected error when submitting to closed pool, got nil")
	}

	if !std_errors.Is(err, errors.ErrPoolClosed) {
		t.Errorf("expected errors.ErrPoolClosed, got: %v", err)
	}
}

func TestAntsTaskExecutorProvider_SetCapacity(t *testing.T) {
	provider, err := NewAntsTaskExecutorProvider(nil)
	require.NoError(t, err)
	defer provider.Close()

	// 获取初始容量
	stats := provider.Stats()
	initialCapacity := stats.Capacity

	// 设置新容量
	err = provider.SetCapacity(initialCapacity / 2)
	require.NoError(t, err)

	// 验证容量已更改
	newStats := provider.Stats()
	if newStats.Capacity != initialCapacity/2 {
		t.Errorf("expected capacity %d, got %d", initialCapacity/2, newStats.Capacity)
	}
}
