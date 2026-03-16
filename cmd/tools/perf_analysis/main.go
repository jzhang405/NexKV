// Package main RunLoop Worker 性能分析工具
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

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

func benchmarkSync() {
	const ops = 100000
	start := time.Now()

	for i := 0; i < ops; i++ {
		simulateWork()
	}

	elapsed := time.Since(start)
	opsPerSec := float64(ops) / elapsed.Seconds()

	fmt.Printf("Sync:         %10.2f ops/s, %10.0f ns/op\n",
		opsPerSec, float64(elapsed.Nanoseconds())/ops)
}

func benchmarkTaskMode() {
	const ops = 100000

	executor, err := concurrency.NewPerCoreExecutor(
		concurrency.WithQueueSize(1000),
	)
	if err != nil {
		panic(err)
	}
	defer executor.Close()

	sourceID := model.NewSourceShard("perf")

	start := time.Now()
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
	elapsed := time.Since(start)
	opsPerSec := float64(ops) / elapsed.Seconds()

	fmt.Printf("Task Mode:    %10.2f ops/s, %10.0f ns/op\n",
		opsPerSec, float64(elapsed.Nanoseconds())/ops)
}

func benchmarkRunLoop() {
	const ops = 100000

	worker := concurrency.NewEventLoop(0, 1000)
	worker.Start()
	defer worker.Close()

	start := time.Now()

	for i := 0; i < ops; i++ {
		err := worker.Submit(func() {
			simulateWork()
			opsCounter.Add(1)
		})
		if err != nil {
			panic(err)
		}
	}

	elapsed := time.Since(start)
	opsPerSec := float64(ops) / elapsed.Seconds()

	fmt.Printf("RunLoop:    %10.2f ops/s, %10.0f ns/op\n",
		opsPerSec, float64(elapsed.Nanoseconds())/ops)
}

func benchmarkRunLoopParallel() {
	const ops = 100000
	numWorkers := runtime.NumCPU()

	var workers []*concurrency.EventLoop
	for i := 0; i < numWorkers; i++ {
		w := concurrency.NewEventLoop(i, 1000)
		w.Start()
		defer w.Close()
		workers = append(workers, w)
	}

	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < ops; i++ {
		wg.Add(1)
		worker := workers[i%numWorkers]
		go func() {
			defer wg.Done()
			err := worker.Submit(func() {
				simulateWork()
				opsCounter.Add(1)
			})
			if err != nil {
				panic(err)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	opsPerSec := float64(ops) / elapsed.Seconds()

	fmt.Printf("RunLoop-P:  %10.2f ops/s, %10.0f ns/op\n",
		opsPerSec, float64(elapsed.Nanoseconds())/ops)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: perf_analysis <mode>")
		fmt.Println("Modes:")
		fmt.Println("  sync       - Synchronous execution")
		fmt.Println("  task       - Task mode (current implementation)")
		fmt.Println("  runloop    - RunLoop (single worker)")
		fmt.Println("  runloop-p  - RunLoop (parallel workers)")
		fmt.Println("")
		fmt.Println("Example with perf:")
		fmt.Println("  sudo perf record -g ./perf_analysis runloop")
		fmt.Println("  sudo perf report")
		os.Exit(1)
	}

	mode := os.Args[1]

	// 预热
	fmt.Println("Warming up...")
	for i := 0; i < 10000; i++ {
		simulateWork()
	}

	runtime.GC()
	fmt.Println("Starting benchmark...")
	fmt.Println("")

	// 运行基准测试（多次迭代以获得稳定数据）
	const iterations = 5
	for i := 0; i < iterations; i++ {
		switch mode {
		case "sync":
			benchmarkSync()
		case "task":
			benchmarkTaskMode()
		case "runloop":
			benchmarkRunLoop()
		case "runloop-p":
			benchmarkRunLoopParallel()
		default:
			fmt.Printf("Unknown mode: %s\n", mode)
			os.Exit(1)
		}
		runtime.GC()
	}

	fmt.Println("")
	fmt.Printf("Total operations: %d\n", opsCounter.Load())
}
