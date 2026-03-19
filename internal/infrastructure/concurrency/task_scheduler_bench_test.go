// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// Benchmark Helper Types
// ==========================================

// benchmarkShardItem 基准测试用的 ShardItem
type benchmarkShardItem struct {
	shardID int
	data    [128]byte // 模拟真实数据负载
}

func (i *benchmarkShardItem) ShardID() int {
	return i.shardID
}

func (i *benchmarkShardItem) MaxRetries() int {
	return 0
}

func (i *benchmarkShardItem) IncAttempts() int {
	return 0
}

// ==========================================
// ShardTask 基准测试
// ==========================================

// BenchmarkShardTask_Enqueue 测试入队性能
func BenchmarkShardTask_Enqueue(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		item := &benchmarkShardItem{shardID: i}
		_ = task.Enqueue(item)
	}
}

// BenchmarkShardTask_PeekDequeue 测试 Peek + Dequeue 性能
func BenchmarkShardTask_PeekDequeue(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 预填充队列
	for i := 0; i < 10000; i++ {
		item := &benchmarkShardItem{shardID: i}
		_ = task.Enqueue(item)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var item any
		if task.Peek(&item) {
			task.Dequeue(&item)
		}
	}
}

// ==========================================
// TaskScheduler 单核心基准测试
// ==========================================

// BenchmarkTaskScheduler_SingleTask_SingleCore 测试单任务单核心吞吐量
func BenchmarkTaskScheduler_SingleTask_SingleCore(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 1)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 1}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交任务
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := &benchmarkShardItem{shardID: workerID}
				_ = scheduler.EnqueueWithShard(item, "bench-task")
			}
		}(i)
	}
	wg.Wait()

	// 等待处理完成
	for processed.Load() < int64(b.N) {
		// busy wait for benchmark
	}
}

// BenchmarkTaskScheduler_SingleTask_FourCores 测试单任务4核心吞吐量
func BenchmarkTaskScheduler_SingleTask_FourCores(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 4)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 4}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交任务
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := &benchmarkShardItem{shardID: workerID}
				_ = scheduler.EnqueueWithShard(item, "bench-task")
			}
		}(i)
	}
	wg.Wait()

	// 等待处理完成
	for processed.Load() < int64(b.N) {
	}
}

// BenchmarkTaskScheduler_SingleTask_EightCores 测试单任务8核心吞吐量
func BenchmarkTaskScheduler_SingleTask_EightCores(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 8)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 8}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交任务
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := &benchmarkShardItem{shardID: workerID}
				_ = scheduler.EnqueueWithShard(item, "bench-task")
			}
		}(i)
	}
	wg.Wait()

	// 等待处理完成
	for processed.Load() < int64(b.N) {
	}
}

// ==========================================
// ShardID 路由性能基准测试
// ==========================================

// BenchmarkTaskScheduler_ShardRouting_Fixed 测试固定 ShardID 路由性能
func BenchmarkTaskScheduler_ShardRouting_Fixed(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 4)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 4}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 测试固定 ShardID 路由（每个 worker 使用不同的 shardID）
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(shardID int) {
			defer wg.Done()
			for j := 0; j < b.N/4; j++ {
				item := &benchmarkShardItem{shardID: shardID}
				_ = scheduler.EnqueueWithShard(item, "bench-task")
			}
		}(i)
	}
	wg.Wait()

	for processed.Load() < int64(b.N) {
	}
}

// BenchmarkTaskScheduler_ShardRouting_Dynamic 测试动态负载均衡路由性能
func BenchmarkTaskScheduler_ShardRouting_Dynamic(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 4)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 4}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 测试 shardID=0 动态负载均衡
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := &benchmarkShardItem{shardID: 0} // 动态负载均衡
				_ = scheduler.EnqueueWithShard(item, "bench-task")
			}
		}()
	}
	wg.Wait()

	for processed.Load() < int64(b.N) {
	}
}

// ==========================================
// 多任务并发基准测试
// ==========================================

// BenchmarkTaskScheduler_MultiTasks 测试多任务并发性能
func BenchmarkTaskScheduler_MultiTasks(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 4)

	var processed atomic.Int64

	// 注册 4 个不同的任务
	for i := 0; i < 4; i++ {
		taskName := fmt.Sprintf("task-%d", i)
		err := scheduler.RegisterTask(
			func(item any) TaskStatus {
				processed.Add(1)
				return TaskPassed
			},
			taskName,
			model.TaskPriorityNormal,
			i+1,
		)
		if err != nil {
			b.Fatal(err)
		}
	}

	executor := &syncExecutor{maxWorkers: 4}
	err := scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交到不同任务
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(taskID int) {
			defer wg.Done()
			taskName := fmt.Sprintf("task-%d", taskID)
			for j := 0; j < b.N/4; j++ {
				item := &benchmarkShardItem{shardID: taskID}
				_ = scheduler.EnqueueWithShard(item, taskName)
			}
		}(i)
	}
	wg.Wait()

	for processed.Load() < int64(b.N) {
	}
}

// ==========================================
// 扩展性基准测试
// ==========================================

// BenchmarkTaskScheduler_Scalability 测试不同核心数的扩展性
func BenchmarkTaskScheduler_Scalability(b *testing.B) {
	coreCounts := []int{1, 2, 4, 8}

	for _, cores := range coreCounts {
		b.Run(fmt.Sprintf("%d-cores", cores), func(b *testing.B) {
			scheduler := NewTaskScheduler("bench", cores)

			var processed atomic.Int64

			err := scheduler.RegisterTask(
				func(item any) TaskStatus {
					processed.Add(1)
					return TaskPassed
				},
				"bench-task",
				model.TaskPriorityNormal,
				1,
			)
			if err != nil {
				b.Fatal(err)
			}

			executor := &syncExecutor{maxWorkers: cores}
			err = scheduler.Start(executor)
			if err != nil {
				b.Fatal(err)
			}
			defer scheduler.Stop()

			b.ResetTimer()

			// 并发提交任务
			var wg sync.WaitGroup
			for i := 0; i < cores; i++ {
				wg.Add(1)
				go func(shardID int) {
					defer wg.Done()
					for j := 0; j < b.N/cores; j++ {
						item := &benchmarkShardItem{shardID: shardID}
						_ = scheduler.EnqueueWithShard(item, "bench-task")
					}
				}(i)
			}
			wg.Wait()

			for processed.Load() < int64(b.N) {
			}
		})
	}
}

// ==========================================
// 并发提交基准测试
// ==========================================

// BenchmarkTaskScheduler_ConcurrentSubmit 测试并发提交性能
func BenchmarkTaskScheduler_ConcurrentSubmit(b *testing.B) {
	scheduler := NewTaskScheduler("bench", 4)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"bench-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor := &syncExecutor{maxWorkers: 4}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		shardID := 0
		for pb.Next() {
			shardID++
			if shardID > 100 {
				shardID = 0
			}
			item := &benchmarkShardItem{shardID: shardID}
			_ = scheduler.EnqueueWithShard(item, "bench-task")
		}
	})

	// 等待所有任务完成
	for processed.Load() < int64(b.N) {
	}
}

// ==========================================
// Mock Executor
// ==========================================

// syncExecutor 同步执行器（用于基准测试）
type syncExecutor struct {
	maxWorkers int
	wg         sync.WaitGroup
	sem        chan struct{}
}

func (e *syncExecutor) Submit(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, fn func(context.Context)) error {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		// 模拟受限的并发执行
		if e.sem != nil {
			<-e.sem
			defer func() { e.sem <- struct{}{} }()
		}
		fn(ctx)
	}()
	return nil
}

func (e *syncExecutor) Close() error {
	e.wg.Wait()
	return nil
}
