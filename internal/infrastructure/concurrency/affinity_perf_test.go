//go:build !darwin

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"github.com/jzhang405/NexKV/internal/domain/model"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// getPerfTaskCount 从环境变量获取任务数量，默认使用 defaultCount
func getPerfTaskCount(defaultCount int) int {
	if env := os.Getenv("PERF_TASK_COUNT"); env != "" {
		if count, err := strconv.Atoi(env); err == nil && count > 0 {
			return count
		}
	}
	return defaultCount
}

// waitForCompletion 等待任务完成，支持超时控制
func waitForCompletion(ctx context.Context, completed *int64, target int) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待超时: 已完成 %d/%d", atomic.LoadInt64(completed), target)
		case <-ticker.C:
			if atomic.LoadInt64(completed) >= int64(target) {
				return nil
			}
		}
	}
}

// TestPerCore_CPUAffinity_PerfAnalysis 用于 perf 分析的性能测试
//
// 运行方式：
//
//  1. 快速测试（开发/CI）:
//     go test -short ./internal/infrastructure/concurrency/
//     go test -short -run TestPerCore_CPUAffinity_PerfAnalysis ./internal/infrastructure/concurrency/
//
//  2. 自定义任务数量:
//     PERF_TASK_COUNT=10000 go test -v -run TestPerCore_CPUAffinity_PerfAnalysis ./internal/infrastructure/concurrency/
//
//  3. Perf 分析（生产环境性能调优）:
//     # 编译测试二进制
//     go test -c -o /tmp/affinity_perf_test ./internal/infrastructure/concurrency/
//
//     # CPU 性能分析
//     perf record -g -e cycles,instructions,cache-references,cache-misses \
//     /tmp/affinity_perf_test -test.run=TestPerCore_CPUAffinity_PerfAnalysis -test.v
//     perf report
//
//     # 缓存命中率分析
//     perf stat -e cache-references,cache-misses,L1-dcache-loads,L1-dcache-load-misses,LLC-loads,LLC-load-misses,cycles,instructions \
//     /tmp/affinity_perf_test -test.run=TestPerCore_CPUAffinity_PerfAnalysis
//
// 超时保护：
//   - 整个测试: 5 分钟超时
//   - 每个场景: 2 分钟超时
//   - 默认任务数: 10 万（可通过 PERF_TASK_COUNT 环境变量覆盖）
func TestPerCore_CPUAffinity_PerfAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf analysis test in short mode")
	}

	if !isAffinitySupported() {
		t.Skip("CPU affinity not supported on this platform")
	}

	// 设置测试超时保护（避免无限等待）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	numCores := runtime.NumCPU()
	// 使用环境变量控制任务数量，默认 10 万（平衡性能和数据量）
	numTasks := getPerfTaskCount(100000)

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

		// 场景超时：2 分钟
		sceneCtx, sceneCancel := context.WithTimeout(ctx, 2*time.Minute)
		defer sceneCancel()

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		// 等待所有任务完成（带超时）
		if err := waitForCompletion(sceneCtx, &completed, numTasks); err != nil {
			t.Fatalf("场景 1 失败: %v", err)
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

		// 场景超时：2 分钟
		sceneCtx, sceneCancel := context.WithTimeout(ctx, 2*time.Minute)
		defer sceneCancel()

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		if err := waitForCompletion(sceneCtx, &completed, numTasks); err != nil {
			t.Fatalf("场景 2 失败: %v", err)
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
			for i := range 10 {
				counter += i
			}

			// 3. 模拟状态更新
			_ = fmt.Sprintf("task-%d", counter)

			atomic.AddInt64(&completed, 1)
		}

		// 场景超时：2 分钟
		sceneCtx, sceneCancel := context.WithTimeout(ctx, 2*time.Minute)
		defer sceneCancel()

		start := time.Now()
		for i := 0; i < numTasks; i++ {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}

		if err := waitForCompletion(sceneCtx, &completed, numTasks); err != nil {
			t.Fatalf("场景 3 失败: %v", err)
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
			for i := range 16 {
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
