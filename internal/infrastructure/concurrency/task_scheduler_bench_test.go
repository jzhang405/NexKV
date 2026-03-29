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
	*model.BaseTask[struct{}]
	shardID int
}

// BenchmarkShardItemPool 使用 model.GetPooledTask 的对象池
var BenchmarkShardItemPool = sync.Pool{
	New: func() any {
		return &benchmarkShardItem{
			BaseTask: model.GetPooledTask(
				model.TaskPriorityNormal,
				0, // 不重试
				func(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
					return struct{}{}, nil
				},
			),
		}
	},
}

func NewBenchmarkShardItem(shardID int) *benchmarkShardItem {
	item := BenchmarkShardItemPool.Get().(*benchmarkShardItem)
	item.shardID = shardID
	return item
}

// ReleaseBenchmarkShardItem 归还对象到池
func ReleaseBenchmarkShardItem(item *benchmarkShardItem) {
	// 先归还 BaseTask 到对象池
	model.ReleasePooledTask(item.BaseTask)
	// 再归还 wrapper
	BenchmarkShardItemPool.Put(item)
}

func (i *benchmarkShardItem) ShardID() int {
	return i.shardID
}

func (i *benchmarkShardItem) Execute(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
	return struct{}{}, nil
}

func (i *benchmarkShardItem) TaskOrder() int {
	return 0 // 默认 order 0
}

// ==========================================
// ShardTask 基准测试
// ==========================================

// BenchmarkShardTask_Enqueue 测试入队性能（使用简单 int 避免对象分配干扰）
func BenchmarkShardTask_Enqueue(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		// 使用简单 int，真实测试 Enqueue 性能（不含对象分配）
		_ = task.Enqueue(i)
	}
}

// BenchmarkShardTask_Enqueue_WithObject 测试入队性能（使用真实对象，包含分配开销）
func BenchmarkShardTask_Enqueue_WithObject(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		item := NewBenchmarkShardItem(i)
		_ = task.Enqueue(item)
	}
}

// ==========================================
// 轻量级 ShardItem（优化分配）
// ==========================================

// lightweightShardItem 轻量级 ShardItem，减少分配开销
type lightweightShardItem struct {
	shardID int
}

func (i *lightweightShardItem) ShardID() int { return i.shardID }

// BenchmarkShardTask_Enqueue_Lightweight 测试入队性能（轻量级对象）
func BenchmarkShardTask_Enqueue_Lightweight(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		// 只分配 shardID 字段，无其他开销
		item := &lightweightShardItem{shardID: i}
		_ = task.Enqueue(item)
	}
}

// ==========================================
// 零分配版本：使用 sync.Pool 和预分配切片
// ==========================================

var lightweightItemPool = sync.Pool{
	New: func() any {
		return &lightweightShardItem{}
	},
}

// BenchmarkShardTask_Enqueue_ZeroAlloc 测试零分配入队（使用对象池）
// 注意：这仅适用于可复用对象的场景
func BenchmarkShardTask_Enqueue_ZeroAlloc(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 预分配对象池
	for i := 0; i < 1000; i++ {
		lightweightItemPool.Put(&lightweightShardItem{})
	}

	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		item := lightweightItemPool.Get().(*lightweightShardItem)
		item.shardID = i
		_ = task.Enqueue(item)
		// 注意：实际场景需要在任务处理完后归还到池
		// 这里仅演示零分配 Enqueue 的理论性能
	}
}

// BenchmarkShardTask_Enqueue_LightweightBatch 批量入队测试
func BenchmarkShardTask_Enqueue_LightweightBatch(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	b.ResetTimer()
	b.ReportAllocs()

	// 批量提交：减少锁获取次数
	batchSize := 100
	items := make([]any, batchSize)
	for i := 0; i < b.N; i += batchSize {
		// 准备批次
		for j := 0; j < batchSize && i+j < b.N; j++ {
			items[j] = &lightweightShardItem{shardID: i + j}
		}
		// 批量入队
		for j := 0; j < batchSize && i+j < b.N; j++ {
			_ = task.Enqueue(items[j])
		}
	}
}

// BenchmarkShardTask_FullLifecycle_Pooled 完整生命周期（获取-执行-归还）
func BenchmarkShardTask_FullLifecycle_Pooled(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		item := NewBenchmarkShardItem(i)
		// 模拟执行
		item.Execute(context.Background(), nil)
		// 归还到池
		ReleaseBenchmarkShardItem(item)
	}
}

// BenchmarkShardTask_FullLifecycle_New 完整生命周期（新建）
func BenchmarkShardTask_FullLifecycle_New(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		i++
		item := &benchmarkShardItem{
			BaseTask: model.NewBaseTask(
				model.TaskPriorityNormal,
				0,
				func(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
					return struct{}{}, nil
				},
			),
			shardID: i,
		}
		// 模拟执行
		item.Execute(context.Background(), nil)
	}
}

// BenchmarkShardTask_PeekDequeue 测试 Peek + Dequeue 性能
func BenchmarkShardTask_PeekDequeue(b *testing.B) {
	task := NewShardTask("bench-task", model.TaskPriorityNormal, 1, func(item any) TaskStatus {
		return TaskPassed
	})

	// 预填充队列
	for i := 0; i < 10000; i++ {
		item := NewBenchmarkShardItem(i)
		_ = task.Enqueue(item)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
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

	err = scheduler.Start()
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
				item := NewBenchmarkShardItem(workerID)
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

	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交任务
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := NewBenchmarkShardItem(workerID)
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

	defer scheduler.Stop()

	b.ResetTimer()

	// 并发提交任务
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := NewBenchmarkShardItem(workerID)
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

	defer scheduler.Stop()

	b.ResetTimer()

	// 测试固定 ShardID 路由（每个 worker 使用不同的 shardID）
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(shardID int) {
			defer wg.Done()
			for j := 0; j < b.N/4; j++ {
				item := NewBenchmarkShardItem(shardID)
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

	defer scheduler.Stop()

	b.ResetTimer()

	// 测试 shardID=0 动态负载均衡
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < b.N/10; j++ {
				item := NewBenchmarkShardItem(0) // 动态负载均衡
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

	err := scheduler.Start()
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
				item := NewBenchmarkShardItem(taskID)
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

			defer scheduler.Stop()

			b.ResetTimer()

			// 并发提交任务
			var wg sync.WaitGroup
			for i := 0; i < cores; i++ {
				wg.Add(1)
				go func(shardID int) {
					defer wg.Done()
					for j := 0; j < b.N/cores; j++ {
						item := NewBenchmarkShardItem(shardID)
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

	defer scheduler.Stop()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		shardID := 0
		for pb.Next() {
			shardID++
			if shardID > 100 {
				shardID = 0
			}
			item := NewBenchmarkShardItem(shardID)
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

