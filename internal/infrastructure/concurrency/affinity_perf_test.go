//go:build !darwin

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"github.com/jzhang405/NexKV/internal/domain/model"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPerCore_CPUAffinity_PerfAnalysis 用于 perf 分析的性能测试
// 运行方式：
//  1. 编译: go test -c -o /tmp/affinity_perf_test ./internal/infrastructure/concurrency/
//  2. Perf 分析:
//     perf record -g -e cycles,instructions,cache-references,cache-misses \
//     /tmp/affinity_perf_test -test.run=TestPerCore_CPUAffinity_PerfAnalysis -test.v
//  3. 查看报告: perf report
//
// 对比缓存命中率:
//
//	perf stat -e cache-references,cache-misses,cycles,instructions \
//	  /tmp/affinity_perf_test -test.run=TestPerCore_CPUAffinity_PerfAnalysis
func TestPerCore_CPUAffinity_PerfAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf analysis test in short mode")
	}

	if !isAffinitySupported() {
		t.Skip("CPU affinity not supported on this platform")
	}

	numCores := runtime.NumCPU()
	numTasks := 1000000 // 大量任务以便 perf 收集足够数据

	// PerCore 总是启用绑核
	executor, err := NewPerCoreExecutor(

		WithQueueSize(100000),
	)
	require.NoError(t, err)
	defer executor.Close()

	t.Logf("=== 性能分析配置 ===")
	t.Logf("平台: %s", runtime.GOOS)
	t.Logf("CPU 核心数: %d", numCores)
	t.Logf("CPU 绑核: true (PerCore 总是启用)")
	t.Logf("任务数量: %d", numTasks)

	// ========== 场景 1: 计算密集型任务（HLC 时钟模拟）==========
	t.Run("ComputeIntensive", func(t *testing.T) {
		// 性能测试不使用 t.Parallel()，避免与父测试的超时竞争
		// 这种测试设计用于 perf 分析，需要顺序执行
		var completed int64

		// 模拟 HLC 时钟更新
		task := func(ctx context.Context) {
			// 模拟时间戳计算
			timestamp := uint64(time.Now().UnixNano())
			logical := uint32(timestamp & 0xFFFF)
			_ = logical

			atomic.AddInt64(&completed, 1)
		}

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		// 等待所有任务完成
		for atomic.LoadInt64(&completed) < int64(numTasks) {
			time.Sleep(10 * time.Millisecond)
		}
		elapsed := time.Since(start)

		t.Logf("计算密集型: 完成 %d 任务, 耗时 %v, 吞吐量 %.2f ops/s",
			numTasks, elapsed, float64(numTasks)/elapsed.Seconds())
	})

	// ========== 场景 2: 内存密集型任务（WAL 缓冲区模拟）==========
	t.Run("MemoryIntensive", func(t *testing.T) {
		var completed int64

		// 使用 sync.Pool 为每个任务分配独立的 buffer
		bufPool := &sync.Pool{
			New: func() any {
				buf := make([]byte, 4096) // 4KB 页
				return &buf
			},
		}

		task := func(ctx context.Context) {
			// 从 pool 获取 buffer（每个任务独立）
			bufPtr := bufPool.Get().(*[]byte)
			buf := *bufPtr
			defer bufPool.Put(bufPtr)

			// 模拟写入数据（内存密集）
			for i := 0; i < len(buf); i += 64 {
				buf[i] = byte(i % 256)
			}

			atomic.AddInt64(&completed, 1)
		}

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		for atomic.LoadInt64(&completed) < int64(numTasks) {
			time.Sleep(10 * time.Millisecond)
		}
		elapsed := time.Since(start)

		t.Logf("内存密集型: 完成 %d 任务, 耗时 %v, 吞吐量 %.2f ops/s",
			numTasks, elapsed, float64(numTasks)/elapsed.Seconds())
	})

	// ========== 场景 3: 混合型任务（真实场景模拟）==========
	t.Run("MixedWorkload", func(t *testing.T) {
		var completed int64

		// 模拟真实场景：计算 + 内存访问
		task := func(ctx context.Context) {
			// 1. 获取当前时间（系统调用）
			now := time.Now()

			// 2. 简单计算
			counter := int(now.UnixNano() % 1000)
			for i := 0; i < 10; i++ {
				counter += i
			}

			// 3. 模拟状态更新
			_ = fmt.Sprintf("task-%d", counter)

			atomic.AddInt64(&completed, 1)
		}

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		for atomic.LoadInt64(&completed) < int64(numTasks) {
			time.Sleep(10 * time.Millisecond)
		}
		elapsed := time.Since(start)

		t.Logf("混合工作负载: 完成 %d 任务, 耗时 %v, 吞吐量 %.2f ops/s",
			numTasks, elapsed, float64(numTasks)/elapsed.Seconds())
	})

	t.Logf("=== 性能分析完成 ===")
	t.Logf("提示: 使用 'perf report' 查看详细分析报告")
}

// BenchmarkPerCore_CacheHitRate 测试绑核下的缓存命中率
// 运行方式:
//
//	perf stat -e cache-references,cache-misses,L1-dcache-loads,L1-dcache-load-misses,LLC-loads,LLC-load-misses,cycles,instructions \
//	  go test -bench=BenchmarkPerCore_CacheHitRate -benchtime=10s \
//	  ./internal/infrastructure/concurrency/
func BenchmarkPerCore_CacheHitRate(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	b.Run("WithAffinity", func(b *testing.B) {
		executor, _ := NewPerCoreExecutor(
			WithQueueSize(100000),
		)
		defer executor.Close()

		// 使用 sync.Pool 为每个任务分配独立的数据区域
		type WorkerLocalData struct {
			data [1024]int
		}
		dataPool := &sync.Pool{
			New: func() any {
				return &WorkerLocalData{}
			},
		}

		task := func(ctx context.Context) {
			// 从 pool 获取独立数据区域
			localData := dataPool.Get().(*WorkerLocalData)
			defer dataPool.Put(localData)

			// 访问本地数据（缓存友好）
			for i := 0; i < 16; i++ {
				localData.data[i*64] = i * 2
			}
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
			}
		})
	})
}
