// Package model 任务对象池性能测试
package model

import (
	"context"
	"testing"
)

// BenchmarkBaseTask_New baseline: 创建新任务
func BenchmarkBaseTask_New(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task := NewBaseTask(
			TaskPriorityNormal,
			0,
			func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)
		// 模拟任务执行
		task.Run(context.Background(), nil)
	}
}

// BenchmarkBaseTask_Pooled 使用对象池
func BenchmarkBaseTask_Pooled(b *testing.B) {
	ctx := context.Background()

	// 预热对象池
	for i := 0; i < 100; i++ {
		task := GetPooledTask(TaskPriorityNormal, 0, func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
			return struct{}{}, nil
		})
		task.Run(ctx, nil)
		ReleasePooledTask(task)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task := GetPooledTask(TaskPriorityNormal, 0, func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
			return struct{}{}, nil
		})
		task.Run(ctx, nil)
		ReleasePooledTask(task)
	}
}

// BenchmarkBaseTask_Pooled_NoRelease 测试不复用的情况（模拟任务被队列持有）
func BenchmarkBaseTask_Pooled_NoRelease(b *testing.B) {

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task := GetPooledTask(TaskPriorityNormal, 0, func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
			return struct{}{}, nil
		})
		// 不归还，模拟任务被持有
		_ = task
	}
}

// BenchmarkBaseTask_EnqueueCompare 对比 Enqueue 场景
func BenchmarkBaseTask_EnqueueCompare(b *testing.B) {

	b.Run("New", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			task := NewBaseTask(
				TaskPriorityNormal,
				0,
				func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
					return struct{}{}, nil
				},
			)
			// 仅创建，不执行
			_ = task
		}
	})

	b.Run("Pooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			task := GetPooledTask(TaskPriorityNormal, 0, func(ctx context.Context, trCtx TaskRunnerContext) (struct{}, error) {
				return struct{}{}, nil
			})
			// 仅创建，不执行
			_ = task
		}
	})
}
