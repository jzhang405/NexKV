package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkMultiLevelQueue_PushPop 测试多级队列 Push/Pop 性能
func BenchmarkMultiLevelQueue_PushPop(b *testing.B) {
	queue := newTaskQueue(10000, 10*time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := model.TaskPriority(i % 10)
		queue.Push(taskItem{
			priority:   priority,
			submitTime: time.Now().UnixNano(),
			task:       func(ctx context.Context) {},
		})
		item := queue.Pop()
		_ = item
	}
}

// BenchmarkExecutor_SubmitHighPriority 测试高优先级任务提交性能
func BenchmarkExecutor_SubmitHighPriority(b *testing.B) {
	exec, _ := NewPerCoreExecutor(
		WithNumCores(4),
		WithQueueSize(10000),
	)
	defer exec.Close()

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exec.SubmitWithPriority(ctx, model.TaskPriorityCritical, func(ctx context.Context) {})
	}
}

// BenchmarkExecutor_SubmitLowPriority 测试低优先级任务提交性能
func BenchmarkExecutor_SubmitLowPriority(b *testing.B) {
	exec, _ := NewPerCoreExecutor(
		WithNumCores(4),
		WithQueueSize(10000),
	)
	defer exec.Close()

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exec.SubmitWithPriority(ctx, model.TaskPriorityIdle, func(ctx context.Context) {})
	}
}

// BenchmarkExecutor_SubmitMixedPriority 测试混合优先级任务提交性能
func BenchmarkExecutor_SubmitMixedPriority(b *testing.B) {
	exec, _ := NewPerCoreExecutor(
		WithNumCores(4),
		WithQueueSize(10000),
	)
	defer exec.Close()

	ctx := context.Background()
	priorities := []model.TaskPriority{
		model.TaskPriorityCritical,
		model.TaskPriorityHigh,
		model.TaskPriorityNormal,
		model.TaskPriorityLow,
		model.TaskPriorityIdle,
	}

	b.ResetTimer()
	priorityIdx := 0
	for i := 0; i < b.N; i++ {
		_ = exec.SubmitWithPriority(ctx, priorities[priorityIdx%5], func(ctx context.Context) {})
		priorityIdx++
	}
}
