// Package concurrency 简单的端到端性能测试
package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// 简单的整数加 1 任务
type SimpleTask struct {
	*model.BaseTask[int]
	input int
}

func NewSimpleTask(input int) *SimpleTask {
	return &SimpleTask{
		BaseTask: model.NewBaseTask(
			model.TaskPriorityNormal,
			0,
			func(ctx context.Context, pipeline model.TaskRunnerContext) (int, error) {
				return input + 1, nil
			},
		),
		input: input,
	}
}

// 完整流程分解测试
func BenchmarkE2E_Full_Create_Execute_Wait(b *testing.B) {

	i := 0
	b.ResetTimer()
	for b.Loop() {
		i++
		// 1. 创建任务
		task := &SimpleTask{
			BaseTask: model.NewBaseTask(
				model.TaskPriorityNormal,
				0,
				func(ctx context.Context, pipeline model.TaskRunnerContext) (int, error) {
					return i + 1, nil
				},
			),
			input: i,
		}

		// 2. 直接执行（同步）
		task.Run(context.Background(), nil)

		// 3. 获取结果（Run 已完成）
		result, _ := task.GetResult()
		_ = result
	}
}

// 仅创建 + 执行（不含 Wait）
func BenchmarkE2E_Create_Execute(b *testing.B) {

	i := 0
	b.ResetTimer()
	for b.Loop() {
		i++
		task := &SimpleTask{
			BaseTask: model.NewBaseTask(
				model.TaskPriorityNormal,
				0,
				func(ctx context.Context, pipeline model.TaskRunnerContext) (int, error) {
					return i + 1, nil
				},
			),
			input: i,
		}

		_, _ = task.Execute(context.Background(), nil)
	}
}

// 仅创建任务
func BenchmarkE2E_CreateOnly(b *testing.B) {

	i := 0
	b.ResetTimer()
	for b.Loop() {
		i++
		_ = &SimpleTask{
			BaseTask: model.NewBaseTask(
				model.TaskPriorityNormal,
				0,
				func(ctx context.Context, pipeline model.TaskRunnerContext) (int, error) {
					return i + 1, nil
				},
			),
			input: i,
		}
	}
}

// ==========================================
// ShardID 路由性能对比测试
// ==========================================

// simpleShardItem 简单的分片项（整数加 1）
type simpleShardItem struct {
	*model.BaseTask[int]
	shardID int
	input   int
}

func NewSimpleShardItem(shardID, input int) *simpleShardItem {
	return &simpleShardItem{
		shardID: shardID,
		input:   input,
		BaseTask: model.NewBaseTask(
			model.TaskPriorityNormal,
			0,
			func(ctx context.Context, trCtx model.TaskRunnerContext) (int, error) {
				return input + 1, nil
			},
		),
	}
}

func (i *simpleShardItem) ShardID() int {
	return i.shardID
}

func (i *simpleShardItem) Execute(ctx context.Context, trCtx model.TaskRunnerContext) (int, error) {
	return i.BaseTask.Execute(ctx, trCtx)
}

// ==========================================
// Benchmark: 固定 ShardID vs 负载均衡
// ==========================================

// BenchmarkShardRouting_Fixed 固定 ShardID=1，只使用 Core 1
func BenchmarkShardRouting_Fixed(b *testing.B) {
	cores := 6
	scheduler := NewTaskScheduler("bench", cores)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"simple-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor, err := NewPerCoreExecutor()
	if err != nil {
		b.Fatal(err)
	}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	// 预创建一个 ShardItem（避免对象创建干扰路由性能测量）
	item := NewSimpleShardItem(1, 0)

	var maxQueueLen int
	b.ResetTimer()
	for b.Loop() {
		_ = scheduler.EnqueueWithShard(item, "simple-task")

		// 采样队列长度（每 1000 次采样一次）
		if b.N%1000 == 0 {
			task, _ := scheduler.cores[1].GetTaskByName("simple-task")
			if ql := task.QueueLen(); ql > maxQueueLen {
				maxQueueLen = ql
			}
		}
	}
	b.StopTimer()

	// 输出队列长度统计
	b.Logf("Fixed (Core 1): max queue length = %d", maxQueueLen)

	// 等待处理完成（带超时）- 不计入基准测试时间
	timeout := time.After(5 * time.Second)
	for processed.Load() < int64(b.N) {
		select {
		case <-timeout:
			b.Fatalf("timeout waiting for tasks to complete, processed=%d, expected=%d",
				processed.Load(), b.N)
		default:
		}
	}
}

// BenchmarkShardRouting_RoundRobin ShardID 递增，轮询所有 Core
func BenchmarkShardRouting_RoundRobin(b *testing.B) {
	cores := 6
	scheduler := NewTaskScheduler("bench", cores)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"simple-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor, err := NewPerCoreExecutor()
	if err != nil {
		b.Fatal(err)
	}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	// RoundRobin: shardID 递增轮询 (1,2,3,4,5,6,1,2,...)，预创建对象复用
	// 避免对象创建开销，公平对比路由性能
	items := make([]*simpleShardItem, cores)
	for i := 0; i < cores; i++ {
		items[i] = NewSimpleShardItem(i+1, 0)
	}

	var maxQueueLen int
	idx := 0
	b.ResetTimer()
	for b.Loop() {
		_ = scheduler.EnqueueWithShard(items[idx], "simple-task")
		idx = (idx + 1) % cores

		// 采样队列长度
		if b.N%1000 == 0 {
			for _, core := range scheduler.cores {
				task, _ := core.GetTaskByName("simple-task")
				if ql := task.QueueLen(); ql > maxQueueLen {
					maxQueueLen = ql
				}
			}
		}
	}
	b.StopTimer()

	b.Logf("RoundRobin (pre-allocated): max queue length = %d", maxQueueLen)

	// 等待处理完成（带超时）
	timeout := time.After(5 * time.Second)
	for processed.Load() < int64(b.N) {
		select {
		case <-timeout:
			b.Fatalf("timeout waiting for tasks to complete, processed=%d, expected=%d",
				processed.Load(), b.N)
		default:
		}
	}
}

// BenchmarkShardRouting_LoadBalance ShardID=0，动态负载均衡
func BenchmarkShardRouting_LoadBalance(b *testing.B) {
	cores := 6
	scheduler := NewTaskScheduler("bench", cores)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"simple-task",
		model.TaskPriorityNormal,
		1,
	)
	if err != nil {
		b.Fatal(err)
	}

	executor, err := NewPerCoreExecutor()
	if err != nil {
		b.Fatal(err)
	}
	err = scheduler.Start(executor)
	if err != nil {
		b.Fatal(err)
	}
	defer scheduler.Stop()

	// LoadBalance 使用 shardID=0，复用同一个对象
	item := NewSimpleShardItem(0, 0)
	var maxQueueLen int
	b.ResetTimer()
	for b.Loop() {
		_ = scheduler.EnqueueWithShard(item, "simple-task")

		// 采样队列长度
		if b.N%1000 == 0 {
			for _, core := range scheduler.cores {
				task, _ := core.GetTaskByName("simple-task")
				if ql := task.QueueLen(); ql > maxQueueLen {
					maxQueueLen = ql
				}
			}
		}
	}
	b.StopTimer()

	b.Logf("LoadBalance: max queue length = %d", maxQueueLen)

	// 等待处理完成（带超时）
	timeout := time.After(5 * time.Second)
	for processed.Load() < int64(b.N) {
		select {
		case <-timeout:
			b.Fatalf("timeout waiting for tasks to complete, processed=%d, expected=%d",
				processed.Load(), b.N)
		default:
		}
	}
}
