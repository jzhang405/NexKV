//go:build ignore

// Package main 提供 Executor 吞吐量性能测试工具
//
// 用途：
//   对比 PerCoreExecutor 和 AntsTaskExecutorProvider 的吞吐量性能
//
// 测试场景：
//   模拟 Transport 层的网络 I/O 密集型任务，包括：
//   - 微秒级网络延迟
//   - 数据序列化/反序列化（1KB）
//   - 校验和计算
//
// 运行方式：
//   cd internal/infrastructure/concurrency
//   go run tool_executor_throughput.go <percore|ants>
//
// 示例：
//   # 测试 PerCoreExecutor
//   go run tool_executor_throughput.go percore
//
//   # 测试 AntsTaskExecutorProvider
//   go run tool_executor_throughput.go ants
//
// 预期输出：
//   === Benchmarking PerCoreExecutor ===
//   PerCore: Completed 1000000 tasks in 2.5s, throughput: 400000.00 ops/s
//
//   === Benchmarking AntsTaskExecutorProvider ===
//   AntsPool: Completed 1000000 tasks in 3.2s, throughput: 312500.00 ops/s
//
// 注意事项：
//   - 本工具使用 `//go:build ignore`，不会被 `go test` 自动运行
//   - 测试任务数量为 100 万，可能需要数秒到数十秒完成
//   - 建议在空闲机器上运行，避免其他进程干扰
//   - 可用于验证 CPU 绑定优化效果
//
// 性能指标说明：
//   - ops/s: 每秒完成任务数（吞吐量）
//   - elapsed: 总耗时
//   - PerCore 通常比 Ants 快 20-40%（取决于 CPU 核心数）
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
	fmt.Println("\n=== Benchmarking AntsTaskExecutorProvider ===")
	exec, _ := concurrency.NewAntsExecutor(nil)
	defer exec.Close()

	ctx := context.Background()
	taskCount := 1000000
	var completed atomic.Int64

	start := time.Now()
	for i := 0; i < taskCount; i++ {
		_ = exec.Submit(ctx, 0, func(ctx context.Context) {
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
	if len(os.Args) < 2 {
		fmt.Println("Executor Throughput Benchmark Tool")
		fmt.Println("\nUsage:")
		fmt.Println("  go run tool_executor_throughput.go <percore|ants>")
		fmt.Println("\nExamples:")
		fmt.Println("  go run tool_executor_throughput.go percore  # Benchmark PerCoreExecutor")
		fmt.Println("  go run tool_executor_throughput.go ants      # Benchmark AntsTaskExecutorProvider")
		fmt.Println("\nOptions:")
		fmt.Println("  percore  - Benchmark PerCoreExecutor (CPU-bound, low latency)")
		fmt.Println("  ants     - Benchmark AntsTaskExecutorProvider (general purpose)")
		os.Exit(1)
	}

	mode := os.Args[1]
	switch mode {
	case "percore":
		benchmarkPerCore()
	case "ants":
		benchmarkAntsDefault()
	default:
		fmt.Printf("Error: Unknown mode '%s'\n\n", mode)
		fmt.Println("Available modes:")
		fmt.Println("  percore  - Benchmark PerCoreExecutor")
		fmt.Println("  ants     - Benchmark AntsTaskExecutorProvider")
		os.Exit(1)
	}
}
