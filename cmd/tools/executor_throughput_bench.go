// Package main 提供任务池性能对比测试工具
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

// simulateTransportTask 模拟 Transport 场景：网络 I/O 密集型任务
// 包括：
//   - 10μs 网络延迟
//   - 1KB 数据处理
//   - 校验和计算
func simulateTransportTask(ctx context.Context) {
	// 模拟网络延迟（微秒级）
	start := time.Now()
	for time.Since(start) < 10*time.Microsecond {
		runtime.Gosched()
	}

	// 模拟数据序列化/反序列化
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 模拟校验和计算
	checksum := uint32(0)
	for _, b := range data {
		checksum += uint32(b)
	}
	_ = checksum
}

func benchmarkPerCore(taskCount int) {
	fmt.Println("=== Benchmarking PerCoreExecutor ===")
	exec, _ := concurrency.NewPerCoreExecutor(
		concurrency.WithQueueSize(10000),
	)
	defer exec.Close()

	ctx := context.Background()
	var completed atomic.Int64

	start := time.Now()
	for i := 0; i < taskCount; i++ {
		// 使用新的 Submit 接口：Submit(ctx, sourceID, priority, task)
		_ = exec.Submit(ctx, model.SourceNetwork, model.TaskPriorityNormal, func(ctx context.Context) {
			simulateTransportTask(ctx)
			completed.Add(1)
		})
	}

	// 等待所有任务完成
	for completed.Load() < int64(taskCount) {
		time.Sleep(10 * time.Millisecond)
	}

	elapsed := time.Since(start)
	fmt.Printf("PerCore: Completed %d tasks in %v, throughput: %.2f ops/s\n",
		taskCount, elapsed, float64(taskCount)/elapsed.Seconds())
}

func benchmarkAntsDefault(taskCount int) {
	fmt.Println("\n=== Benchmarking AntsTaskExecutorProvider ===")
	exec, _ := concurrency.NewAntsExecutor(nil)
	defer exec.Close()

	ctx := context.Background()
	var completed atomic.Int64

	start := time.Now()
	for i := 0; i < taskCount; i++ {
		// 使用新的 Submit 接口：Submit(ctx, sourceID, priority, task)
		_ = exec.Submit(ctx, model.SourceDefault, model.TaskPriorityNormal, func(ctx context.Context) {
			simulateTransportTask(ctx)
			completed.Add(1)
		})
	}

	// 等待所有任务完成
	for completed.Load() < int64(taskCount) {
		time.Sleep(10 * time.Millisecond)
	}

	elapsed := time.Since(start)
	fmt.Printf("AntsPool: Completed %d tasks in %v, throughput: %.2f ops/s\n",
		taskCount, elapsed, float64(taskCount)/elapsed.Seconds())
}

func main() {
	taskCount := flag.Int("tasks", 1000000, "number of tasks to run")
	flag.Parse()

	if *taskCount <= 0 {
		fmt.Println("Error: task count must be positive")
		os.Exit(1)
	}

	fmt.Printf("Running benchmark with %d tasks...\n\n", *taskCount)
	benchmarkPerCore(*taskCount)
	benchmarkAntsDefault(*taskCount)
}
