// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 调度开销基线测试
//
// 3 种路由模式 × 全核心（runtime.NumCPU()）
// 1. FixedShard:  shardID 固定，所有任务路由到 core[shardID % coreCount]
// 2. LoadBalance: shardID=0，动态负载均衡（selectLeastLoadedCore）
// 3. RoundRobin:  shardID 递增 1→2→...→coreCount→1，均匀分布
//
// 目标：确认每种模式下单次调度开销（ns/op）的 baseline
// ==========================================

// benchRegisterAndStartNoRelease 注册不归还 item 的 handler（用于 PureEnqueue 复用同一 item）
func benchRegisterAndStartNoRelease(b *testing.B, coreCount int) (*TaskScheduler, *atomic.Int64) {
	if coreCount <= 0 {
		coreCount = runtime.NumCPU()
	}

	scheduler := NewTaskScheduler("latency", coreCount)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			return TaskPassed
		},
		"latency-task",
		model.TaskPriorityNormal,
		0,
	)
	if err != nil {
		b.Fatal(err)
	}

	if err := scheduler.Start(nil); err != nil {
		b.Fatal(err)
	}

	return scheduler, &processed
}

// benchRegisterAndStart 注册归还 item 的 handler（用于每次创建新 item 的 benchmark）
func benchRegisterAndStart(b *testing.B, coreCount int) (*TaskScheduler, *atomic.Int64) {
	if coreCount <= 0 {
		coreCount = runtime.NumCPU()
	}

	scheduler := NewTaskScheduler("latency", coreCount)

	var processed atomic.Int64

	err := scheduler.RegisterTask(
		func(item any) TaskStatus {
			processed.Add(1)
			ReleaseBenchmarkShardItem(item.(*benchmarkShardItem))
			return TaskPassed
		},
		"latency-task",
		model.TaskPriorityNormal,
		0,
	)
	if err != nil {
		b.Fatal(err)
	}

	if err := scheduler.Start(nil); err != nil {
		b.Fatal(err)
	}

	return scheduler, &processed
}

// ==========================================
// 纯调度开销测试（排除对象分配）
//
// 预创建 item，仅测量 EnqueueWithShard 路径
// ==========================================

func BenchmarkSchedLatency_PureEnqueue_FixedShard_1Core(b *testing.B) {
	scheduler, processed := benchRegisterAndStartNoRelease(b, 1)
	defer scheduler.Stop()

	// 预创建 item（排除分配开销）
	item := NewBenchmarkShardItem(1)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
}

func BenchmarkSchedLatency_PureEnqueue_FixedShard_AllCores(b *testing.B) {
	scheduler, processed := benchRegisterAndStartNoRelease(b, 0)
	defer scheduler.Stop()

	// 预创建 item
	item := NewBenchmarkShardItem(1)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
}

func BenchmarkSchedLatency_PureEnqueue_LoadBalance_AllCores(b *testing.B) {
	scheduler, processed := benchRegisterAndStartNoRelease(b, 0)
	defer scheduler.Stop()

	item := NewBenchmarkShardItem(0)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
}

// ========== 1. FixedShard: 固定 shardID，全部路由到同一个 core ==========

func BenchmarkSchedLatency_FixedShard_1Core(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 1)
	defer scheduler.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		item := NewBenchmarkShardItem(1)
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
}

func BenchmarkSchedLatency_FixedShard_AllCores(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// 固定 shardID=1，全部路由到 core[1%coreCount]
		item := NewBenchmarkShardItem(1)
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
	b.Logf("coreCount=%d", coreCount)
}

// ========== 2. LoadBalance: shardID=0，动态负载均衡 ==========

func BenchmarkSchedLatency_LoadBalance_AllCores(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// shardID=0 触发 selectLeastLoadedCore
		item := NewBenchmarkShardItem(0)
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
	b.Logf("coreCount=%d", coreCount)
}

// ========== 3. RoundRobin: shardID 递增，均匀分布到所有 core ==========

func BenchmarkSchedLatency_RoundRobin_AllCores(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	shardID := 0
	for b.Loop() {
		shardID++
		if shardID > coreCount {
			shardID = 1
		}
		item := NewBenchmarkShardItem(shardID)
		_ = scheduler.EnqueueWithShard(item, "latency-task")
	}
	for processed.Load() < int64(b.N) {
	}
	b.Logf("coreCount=%d", coreCount)
}

// ========== 并发版本（b.RunParallel）==========

func BenchmarkSchedLatency_FixedShard_AllCores_Parallel(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			item := NewBenchmarkShardItem(1)
			_ = scheduler.EnqueueWithShard(item, "latency-task")
		}
	})
	for processed.Load() < int64(b.N) {
	}
	_ = coreCount
}

func BenchmarkSchedLatency_LoadBalance_AllCores_Parallel(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			item := NewBenchmarkShardItem(0)
			_ = scheduler.EnqueueWithShard(item, "latency-task")
		}
	})
	for processed.Load() < int64(b.N) {
	}
	_ = coreCount
}

func BenchmarkSchedLatency_RoundRobin_AllCores_Parallel(b *testing.B) {
	scheduler, processed := benchRegisterAndStart(b, 0)
	defer scheduler.Stop()

	coreCount := len(scheduler.cores)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		shardID := 0
		for pb.Next() {
			shardID++
			if shardID > coreCount {
				shardID = 1
			}
			item := NewBenchmarkShardItem(shardID)
			_ = scheduler.EnqueueWithShard(item, "latency-task")
		}
	})
	for processed.Load() < int64(b.N) {
	}
	_ = coreCount
}
