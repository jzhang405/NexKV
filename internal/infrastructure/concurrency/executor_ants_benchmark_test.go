// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// P2-03: 性能基准测试
// ==========================================

// BenchmarkSubmit 单任务提交延迟
func BenchmarkSubmit(b *testing.B) {
	provider, _ := NewAntsExecutor(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = provider.Submit(ctx, model.SourceDefault, service.PriorityNormal, func(ctx context.Context) {})
	}
}

// BenchmarkSubmitBatch_100 批量提交 100 个任务
func BenchmarkSubmitBatch_100(b *testing.B) {
	provider, _ := NewAntsExecutor(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		for range 100 {
			_ = provider.Submit(ctx, model.SourceDefault, service.PriorityNormal, func(ctx context.Context) {})
		}
	}
}

// BenchmarkSubmitBatch_1000 批量提交 1000 个任务
func BenchmarkSubmitBatch_1000(b *testing.B) {
	provider, _ := NewAntsExecutor(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		for range 1000 {
			_ = provider.Submit(ctx, model.SourceDefault, service.PriorityNormal, func(ctx context.Context) {})
		}
	}
}

// BenchmarkConcurrentSubmit 并发提交
func BenchmarkConcurrentSubmit(b *testing.B) {
	provider, _ := NewAntsExecutor(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = provider.Submit(ctx, model.SourceDefault, service.PriorityNormal, func(ctx context.Context) {})
		}
	})
}

// BenchmarkCloseWithPendingTasks 关闭时的性能（有待处理任务）
func BenchmarkCloseWithPendingTasks(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		provider, _ := NewAntsExecutor(nil)
		ctx := context.Background()

		// 提交一些任务
		for range 100 {
			_ = provider.Submit(ctx, model.SourceDefault, service.PriorityNormal, func(ctx context.Context) {
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
	provider, _ := NewAntsExecutor(nil)
	defer provider.Close()
	ctx := context.Background()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		priority := service.PriorityNormal
		if i%10 == 0 {
			priority = service.PriorityHigh
		} else if i%100 == 0 {
			priority = service.PriorityCritical
		}
		_ = provider.Submit(ctx, model.SourceDefault, priority, func(ctx context.Context) {})
	}
}
