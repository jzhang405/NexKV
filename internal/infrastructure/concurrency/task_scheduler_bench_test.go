// Package concurrency 并发控制和任务调度机制基准测试
package concurrency

import (
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 队列操作基准测试
// ==========================================

func BenchmarkSchedulerBaseTask_Enqueue(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task.Enqueue(i)
	}
}

func BenchmarkSchedulerBaseTask_Peek(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	// 预填充队列
	for i := 0; i < 1000; i++ {
		task.Enqueue(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	var item any
	for i := 0; i < b.N; i++ {
		task.Peek(&item)
	}
}

func BenchmarkSchedulerBaseTask_Dequeue(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每次入队后立即出队
		task.Enqueue(i)
		var item any
		task.Dequeue(&item)
	}
}

// ==========================================
// 优先级排序基准测试
// ==========================================

func BenchmarkTaskScheduler_GetSortedTasks_Small(b *testing.B) {
	scheduler := NewTaskScheduler("bench-scheduler")

	// 注册 5 个任务
	for i := 0; i < 5; i++ {
		task := NewSchedulerBaseTask("task", model.TaskPriorityNormal, i+1)
		scheduler.RegisterTask(task, i+1)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler.getOrderedTasks()
	}
}

func BenchmarkTaskScheduler_GetSortedTasks_Medium(b *testing.B) {
	scheduler := NewTaskScheduler("bench-scheduler")

	// 注册 20 个任务
	for i := 0; i < 20; i++ {
		task := NewSchedulerBaseTask("task", model.TaskPriorityNormal, i+1)
		scheduler.RegisterTask(task, i+1)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler.getOrderedTasks()
	}
}

func BenchmarkTaskScheduler_GetSortedTasks_Large(b *testing.B) {
	scheduler := NewTaskScheduler("bench-scheduler")

	// 注册 100 个任务
	for i := 0; i < 100; i++ {
		task := NewSchedulerBaseTask("task", model.TaskPriorityNormal, i+1)
		scheduler.RegisterTask(task, i+1)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler.getOrderedTasks()
	}
}

// ==========================================
// 并发入队基准测试
// ==========================================

func BenchmarkSchedulerBaseTask_ConcurrentEnqueue_SingleGoroutine(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task.Enqueue(i)
	}
}

func BenchmarkSchedulerBaseTask_ConcurrentEnqueue_4Goroutines(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	goroutines := 4
	perGoroutine := b.N / goroutines

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				task.Enqueue(j)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkSchedulerBaseTask_ConcurrentEnqueue_8Goroutines(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	goroutines := 8
	perGoroutine := b.N / goroutines

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				task.Enqueue(j)
			}
		}()
	}
	wg.Wait()
}

// ==========================================
// 调度器性能基准测试
// ==========================================

func BenchmarkTaskScheduler_RegisterTask(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler := NewTaskScheduler("bench-scheduler")
		task := NewSchedulerBaseTask("task", model.TaskPriorityNormal, 1)
		scheduler.RegisterTask(task, 1)
	}
}

func BenchmarkTaskScheduler_UnregisterTask(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		scheduler := NewTaskScheduler("bench-scheduler")
		task := NewSchedulerBaseTask("task", model.TaskPriorityNormal, 1)
		scheduler.RegisterTask(task, 1)
		b.StartTimer()

		scheduler.UnregisterTask("task")
	}
}

// ==========================================
// Wakeup 机制基准测试
// ==========================================

func BenchmarkTaskScheduler_Wakeup(b *testing.B) {
	scheduler := NewTaskScheduler("bench-scheduler")
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)
	scheduler.RegisterTask(task, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler.wakeup()
	}
}

func BenchmarkTaskScheduler_EnqueueWithWakeup(b *testing.B) {
	scheduler := NewTaskScheduler("bench-scheduler")
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)
	scheduler.RegisterTask(task, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task.Enqueue(i)
	}
}

// ==========================================
// 吞吐量基准测试
// ==========================================

func BenchmarkSchedulerBaseTask_Throughput_EnqueueDequeue(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task.Enqueue(i)
		var item any
		task.Dequeue(&item)
	}
}

func BenchmarkSchedulerBaseTask_Throughput_PeekDequeue(b *testing.B) {
	task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)

	// 预填充队列
	for i := 0; i < 1000; i++ {
		task.Enqueue(i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	var item any
	for i := 0; i < b.N; i++ {
		task.Peek(&item)
		task.Dequeue(&item)
		task.Enqueue(i) // 保持队列有元素
	}
}

// ==========================================
// 内存分配基准测试
// ==========================================

func BenchmarkSchedulerBaseTask_Allocations(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)
		task.Enqueue(i)
	}
}

func BenchmarkTaskScheduler_Allocations(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scheduler := NewTaskScheduler("bench-scheduler")
		task := NewSchedulerBaseTask("bench-task", model.TaskPriorityNormal, 1)
		scheduler.RegisterTask(task, 1)
	}
}
