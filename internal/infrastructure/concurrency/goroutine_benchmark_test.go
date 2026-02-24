// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"testing"
	"time"
)

// ==========================================
// P2-03: 性能基准测试
// ==========================================

// BenchmarkSubmit 单任务提交延迟
func BenchmarkSubmit(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = provider.Submit(ctx, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitWithResult 带结果任务延迟
func BenchmarkSubmitWithResult(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
			return i, nil
		})
		_, _ = result.Get(ctx)
	}
}

// BenchmarkSubmitBatch_100 批量提交 100 任务
func BenchmarkSubmitBatch_100(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	tasks := make([]func(context.Context), 100)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) {}
	}

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitBatch(ctx, tasks)
	}
}

// BenchmarkSubmitBatch_1000 批量提交 1000 任务
func BenchmarkSubmitBatch_1000(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
		Capacity:       1000,
		EnablePriority: true,
		EnableDelayed:  true,
	})
	defer provider.Close()
	ctx := context.Background()

	tasks := make([]func(context.Context), 1000)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) {}
	}

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitBatch(ctx, tasks)
	}
}

// BenchmarkSubmitBatchAllErrors 批量提交（收集错误）
func BenchmarkSubmitBatchAllErrors(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
		Capacity:       1000,
		EnablePriority: true,
		EnableDelayed:  true,
	})
	defer provider.Close()
	ctx := context.Background()

	tasks := make([]func(context.Context), 100)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) {}
	}

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitBatchAllErrors(ctx, tasks)
	}
}

// BenchmarkSubmitDelayed 延迟任务调度开销
func BenchmarkSubmitDelayed(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitDelayed(ctx, 1*time.Millisecond, func(ctx context.Context) {})
	}
}

// BenchmarkConcurrentSubmit 并发提交锁竞争测试
func BenchmarkConcurrentSubmit(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
		Capacity:       10000,
		EnablePriority: true,
		EnableDelayed:  true,
	})
	defer provider.Close()
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = provider.Submit(ctx, func(ctx context.Context) {})
		}
	})
}

// BenchmarkConcurrentSubmitWithResult 并发带结果提交
func BenchmarkConcurrentSubmitWithResult(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
		Capacity:       10000,
		EnablePriority: true,
		EnableDelayed:  true,
	})
	defer provider.Close()
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
				return 1, nil
			})
			_, _ = result.Get(ctx)
		}
	})
}

// BenchmarkTypedResult_Get TypedResult Get 操作
func BenchmarkTypedResult_Get(b *testing.B) {
	inner := NewAnyResult()
	inner.SetValue(42)
	typed := &TypedResult[int]{inner: inner}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = typed.Get(ctx)
	}
}

// BenchmarkAnyResult_SetValue AnyResult SetValue 操作
func BenchmarkAnyResult_SetValue(b *testing.B) {
	b.ResetTimer()
	for i := range b.N {
		result := NewAnyResult()
		result.SetValue(i)
	}
}

// BenchmarkCloseWithPendingTasks 关闭时有待处理任务的开销
func BenchmarkCloseWithPendingTasks(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
			Capacity:       100,
			EnablePriority: true,
			EnableDelayed:  true,
		})
		ctx := context.Background()

		// 提交一些任务
		for range 50 {
			_ = provider.Submit(ctx, func(ctx context.Context) {
				time.Sleep(1 * time.Millisecond)
			})
		}

		_ = provider.Close()
	}
}

// BenchmarkSubmitWithPriority 带优先级提交
func BenchmarkSubmitWithPriority(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitAdvanced 高级提交（带选项）
func BenchmarkSubmitAdvanced(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
			return arg, nil
		}, 42, WithPriority(PriorityHigh))
	}
}
