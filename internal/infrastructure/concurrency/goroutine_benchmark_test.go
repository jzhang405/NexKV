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
	for i := 0; i < b.N; i++ {
		provider.Submit(ctx, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitWithResult 带结果任务延迟
func BenchmarkSubmitWithResult(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
			return i, nil
		})
		result.Get(ctx)
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
	for i := 0; i < b.N; i++ {
		provider.SubmitBatch(ctx, tasks)
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
	for i := 0; i < b.N; i++ {
		provider.SubmitBatch(ctx, tasks)
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
	for i := 0; i < b.N; i++ {
		provider.SubmitBatchAllErrors(ctx, tasks)
	}
}

// BenchmarkSubmitDelayed 延迟任务调度开销
func BenchmarkSubmitDelayed(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider.SubmitDelayed(ctx, 1*time.Millisecond, func(ctx context.Context) {})
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
			provider.Submit(ctx, func(ctx context.Context) {})
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
			result.Get(ctx)
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
	for i := 0; i < b.N; i++ {
		typed.Get(ctx)
	}
}

// BenchmarkAnyResult_SetValue AnyResult SetValue 操作
func BenchmarkAnyResult_SetValue(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := NewAnyResult()
		result.SetValue(i)
	}
}

// BenchmarkCloseWithPendingTasks 关闭时有待处理任务的开销
func BenchmarkCloseWithPendingTasks(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider, _ := NewAntsGoroutineProvider(&ProviderConfig{
			Capacity:       100,
			EnablePriority: true,
			EnableDelayed:  true,
		})
		ctx := context.Background()

		// 提交一些任务
		for j := 0; j < 50; j++ {
			provider.Submit(ctx, func(ctx context.Context) {
				time.Sleep(1 * time.Millisecond)
			})
		}

		provider.Close()
	}
}

// BenchmarkSubmitWithPriority 带优先级提交
func BenchmarkSubmitWithPriority(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitAdvanced 高级提交（带选项）
func BenchmarkSubmitAdvanced(b *testing.B) {
	provider, _ := NewAntsGoroutineProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider.SubmitAdvanced(ctx, func(ctx context.Context, arg any) (any, error) {
			return arg, nil
		}, 42, WithPriority(PriorityHigh))
	}
}
