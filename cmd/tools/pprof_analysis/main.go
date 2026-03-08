// Package main RunLoop Worker 性能分析工具（使用 pprof）
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	_ "net/http/pprof"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

var (
	opsCounter atomic.Int64
)

func simulateWork() {
	// 模拟短任务（类似 KV 操作）
	sum := 0
	for i := 0; i < 10; i++ {
		sum += i
	}
	_ = sum
}

func benchmarkRunLoop() {
	const ops = 10000

	worker := concurrency.NewRunLoopWorker(0, 1000)
	worker.Start()
	defer worker.Close()

	for i := 0; i < ops; i++ {
		err := worker.Submit(func() {
			simulateWork()
			opsCounter.Add(1)
		})
		if err != nil {
			panic(err)
		}
	}
}

func benchmarkTaskMode() {
	const ops = 10000

	executor, err := concurrency.NewPerCoreExecutor(
		concurrency.WithQueueSize(1000),
	)
	if err != nil {
		panic(err)
	}
	defer executor.Close()

	sourceID := model.NewSourceShard("perf")

	var wg sync.WaitGroup
	for i := 0; i < ops; i++ {
		wg.Add(1)
		err := executor.Submit(
			context.Background(),
			sourceID,
			model.TaskPriorityNormal,
			func(ctx context.Context) {
				defer wg.Done()
				simulateWork()
				opsCounter.Add(1)
			},
		)
		if err != nil {
			panic(err)
		}
	}
	wg.Wait()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: pprof_analysis <mode>")
		fmt.Println("Modes:")
		fmt.Println("  task       - Task mode (current implementation)")
		fmt.Println("  runloop    - RunLoop")
		fmt.Println("")
		fmt.Println("With pprof:")
		fmt.Println("  go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30")
		os.Exit(1)
	}

	mode := os.Args[1]

	// 启动 pprof HTTP 服务器
	go func() {
		fmt.Println("pprof available at http://localhost:6060/debug/pprof/")
	}()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	fmt.Println("Starting benchmark (will run for 30 seconds)...")

	start := time.Now()
	for time.Since(start) < 30*time.Second {
		switch mode {
		case "task":
			benchmarkTaskMode()
		case "runloop":
			benchmarkRunLoop()
		default:
			fmt.Printf("Unknown mode: %s\n", mode)
			os.Exit(1)
		}
	}

	fmt.Printf("Total operations: %d\n", opsCounter.Load())
	fmt.Printf("Throughput: %.2f ops/s\n", float64(opsCounter.Load())/30.0)
	fmt.Println("\nBenchmark completed. You can now:")
	fmt.Println("1. Get CPU profile: curl -o cpu.prof http://localhost:6060/debug/pprof/profile?seconds=30")
	fmt.Println("2. Analyze: go tool pprof cpu.prof")
	fmt.Println("3. Or use web: go tool pprof -http=:8080 cpu.prof")

	// 保持运行以允许 pprof 访问
	select {}
}
