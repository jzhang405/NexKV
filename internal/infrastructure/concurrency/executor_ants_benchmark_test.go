// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// P2-03: 性能基准测试
// ==========================================

// BenchmarkSubmit 单任务提交延迟
func BenchmarkSubmit(b *testing.B) {
	provider, _ := NewAntsTaskExecutorProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitBatch_100 批量提交 100 个任务
func BenchmarkSubmitBatch_100(b *testing.B) {
	provider, _ := NewAntsTaskExecutorProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 100; j++ {
			_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {})
		}
	}
}

// BenchmarkSubmitBatch_1000 批量提交 1000 个任务
func BenchmarkSubmitBatch_1000(b *testing.B) {
	provider, _ := NewAntsTaskExecutorProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {})
		}
	}
}

// BenchmarkConcurrentSubmit 并发提交
func BenchmarkConcurrentSubmit(b *testing.B) {
	provider, _ := NewAntsTaskExecutorProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {})
		}
	})
}

// BenchmarkCloseWithPendingTasks 关闭时的性能（有待处理任务）
func BenchmarkCloseWithPendingTasks(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider, _ := NewAntsTaskExecutorProvider(nil)
		ctx := context.Background()

		// 提交一些任务
		for j := 0; j < 100; j++ {
			_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
				time.Sleep(10 * time.Millisecond)
			})
		}

		// 等待任务开始
		time.Sleep(20 * time.Millisecond)

		// 关闭
		_ = provider.Close()
	}
}

// BenchmarkSubmitWithPriority 不同优先级提交
func BenchmarkSubmitWithPriority(b *testing.B) {
	provider, _ := NewAntsTaskExecutorProvider(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := model.TaskPriorityNormal
		if i%10 == 0 {
			priority = model.TaskPriorityHigh
		} else if i%100 == 0 {
			priority = model.TaskPriorityCritical
		}
		_ = provider.Submit(ctx, priority, func(ctx context.Context) {})
	}
}
