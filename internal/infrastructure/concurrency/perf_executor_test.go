//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

// 模拟 Transport 场景：网络 I/O 密集型任务
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

func benchmarkPerCore() {
	fmt.Println("=== Benchmarking PerCoreExecutor ===")
	exec, _ := concurrency.NewPerCoreExecutor(
		concurrency.WithNumCores(runtime.NumCPU()),
		concurrency.WithQueueSize(10000),
	)
	defer exec.Close()

	ctx := context.Background()
	taskCount := 1000000
	var completed atomic.Int64

	start := time.Now()
	for i := 0; i < taskCount; i++ {
		_ = exec.Submit(ctx, func(ctx context.Context) {
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

func benchmarkAntsDefault() {
	fmt.Println("\n=== Benchmarking AntsDefaultExecutor ===")
	exec := concurrency.NewAntsDefaultExecutor()
	defer exec.Close()

	ctx := context.Background()
	taskCount := 1000000
	var completed atomic.Int64

	start := time.Now()
	for i := 0; i < taskCount; i++ {
		_ = exec.Submit(ctx, func(ctx context.Context) {
			simulateTransportTask(ctx)
			completed.Add(1)
		})
	}

	// 等待所有任务完成
	for completed.Load() < int64(taskCount) {
		time.Sleep(10 * time.Millisecond)
	}

	elapsed := time.Since(start)
	fmt.Printf("AntsDefault: Completed %d tasks in %v, throughput: %.2f ops/s\n",
		taskCount, elapsed, float64(taskCount)/elapsed.Seconds())
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: perf_executor_test <percore|ants>")
		os.Exit(1)
	}

	mode := os.Args[1]
	switch mode {
	case "percore":
		benchmarkPerCore()
	case "ants":
		benchmarkAntsDefault()
	default:
		fmt.Println("Unknown mode:", mode)
		os.Exit(1)
	}
}
