// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// BTree Set/Get 操作性能测试工具
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"

	"net/http"
	_ "net/http/pprof"
)

var (
	threadList   string
	opsPerThread int
	mode         string
	initCount    int
	opType       string // "set", "get", "mixed"
)

func init() {
	flag.StringVar(&threadList, "threads", "1,2,4,8",
		"并发度列表（逗号分隔），例如: 1,2,4,8,16")
	flag.IntVar(&opsPerThread, "count", 50000,
		"每线程操作数 (默认: 50000)")
	flag.StringVar(&mode, "mode", "builtin",
		"测试模式: builtin(BTree内置Scheduler), custom(自定义Scheduler对比测试)")
	flag.IntVar(&initCount, "init", 0,
		"初始化数据量 (默认: 0, 每线程 opsPerThread/10)")
	flag.StringVar(&opType, "op", "set",
		"操作类型: set, get, mixed(50%%set+50%%get)")
}

func main() {
	flag.Parse()

	// 启动 pprof HTTP server
	go func() {
		fmt.Fprintf(os.Stderr, "pprof server: http://localhost:6060/debug/pprof/\n")
		if err := http.ListenAndServe("localhost:6060", nil); err != nil {
			fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
		}
	}()

	threads := parseThreadList(threadList)
	if len(threads) == 0 {
		fmt.Fprintf(os.Stderr, "无效的并发度列表: %s\n", threadList)
		os.Exit(1)
	}

	// Get 模式需要先有数据
	if opType == "get" && initCount == 0 {
		initCount = opsPerThread * threads[len(threads)-1]
	}
	if initCount == 0 {
		initCount = opsPerThread * threads[len(threads)-1] / 10
		if initCount < 1000 {
			initCount = 1000
		}
	}

	fmt.Printf("========================================\n")
	fmt.Printf("BTree 性能测试\n")
	fmt.Printf("========================================\n")
	fmt.Printf("操作类型: %s\n", opType)
	fmt.Printf("测试模式: %s\n", mode)
	fmt.Printf("并发度: %v\n", threads)
	fmt.Printf("每线程操作数: %d\n", opsPerThread)
	fmt.Printf("初始化数据量: %d\n", initCount)
	fmt.Printf("========================================\n\n")

	runBenchmark(threads)
}

func parseThreadList(s string) []int {
	parts := splitAndTrim(s, ",")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		var val int
		if _, err := fmt.Sscanf(part, "%d", &val); err == nil && val > 0 {
			result = append(result, val)
		}
	}
	return result
}

func splitAndTrim(s, sep string) []string {
	parts := make([]string, 0)
	current := ""
	for _, ch := range s {
		if string(ch) == sep {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else if ch != ' ' && ch != '\t' {
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

type TestMode int

const (
	ModeBuiltin TestMode = iota
	ModeCustom
)

func parseMode(s string) TestMode {
	switch s {
	case "builtin":
		return ModeBuiltin
	case "custom":
		return ModeCustom
	case "direct":
		return ModeBuiltin
	case "scheduler":
		return ModeCustom
	default:
		return ModeBuiltin
	}
}

func runBenchmark(threads []int) {
	testMode := parseMode(mode)

	fmt.Printf("%-10s | %-12s | %-14s | %-14s | %-14s\n",
		"并发度", "操作", "总操作数", "吞吐量(ops/s)", "平均延迟(μs)")
	fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")

	for _, numThreads := range threads {
		totalOps := numThreads * opsPerThread

		if testMode == ModeBuiltin {
			throughput, latency := runTest(numThreads, totalOps, false)
			fmt.Printf("%-10d | %-12s | %-14d | %-14.0f | %-14.2f\n",
				numThreads, opType, totalOps, throughput, latency)
		}

		if testMode == ModeCustom {
			throughput, latency := runTest(numThreads, totalOps, true)
			fmt.Printf("%-10d | %-12s | %-14d | %-14.0f | %-14.2f\n",
				numThreads, opType, totalOps, throughput, latency)
		}

		if numThreads != threads[len(threads)-1] {
			fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")
		}
	}
}

func runTest(numThreads, totalOps int, useScheduler bool) (float64, float64) {
	ctx := context.Background()

	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	var scheduler *concurrency.TaskScheduler
	var schedulerAdapter btree.TaskScheduler
	if useScheduler {
		schedulerCores := runtime.NumCPU()
		scheduler = concurrency.NewTaskScheduler("btree-perf", schedulerCores)
		schedulerAdapter = &TaskSchedulerAdapter{scheduler: scheduler}
		defer scheduler.Stop()
	}

	// 初始化数据
	if initCount > 0 {
		initializeData(ctx, tree, initCount)
	}

	// 预热
	warmup(ctx, tree, schedulerAdapter, useScheduler, 1000)

	successCount := atomic.Int64{}
	var wg sync.WaitGroup
	startTime := time.Now()

	// 预生成 keys/values（消除热路径 fmt.Sprintf 开销）
	type threadData struct {
		keys   [][]byte
		values [][]byte
	}
	threadDataArr := make([]threadData, numThreads)
	for i := 0; i < numThreads; i++ {
		td := threadData{}
		td.keys = make([][]byte, opsPerThread)
		td.values = make([][]byte, opsPerThread)
		randBytes := make([]byte, opsPerThread)
		for r := range randBytes {
			randBytes[r] = byte(r % 256)
		}
		for j := 0; j < opsPerThread; j++ {
			var key string
			switch opType {
			case "get":
				idx := j % initCount
				key = fmt.Sprintf("%ckey-%d", randBytes[idx], idx)
			case "set":
				key = fmt.Sprintf("%ckey-%d-%d", randBytes[j], i, j%initCount)
			case "mixed":
				key = fmt.Sprintf("%ckey-%d-%d", randBytes[j], i, j%initCount)
			}
			td.keys[j] = []byte(key)
			td.values[j] = []byte(fmt.Sprintf("value-%d", j))
		}
		threadDataArr[i] = td
	}

	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(threadID int, td threadData) {
			defer wg.Done()

			for j := 0; j < opsPerThread; j++ {
				key := td.keys[j]

				var opErr error
				switch opType {
				case "get":
					_, opErr = tree.Get(ctx, key)
				case "set":
					value := td.values[j]
					if useScheduler {
						opErr = tree.SetWithRetryAndQueue(ctx, schedulerAdapter, key, value)
					} else {
						opErr = tree.Set(ctx, key, value)
					}
				case "mixed":
					if j%2 == 0 {
						value := td.values[j]
						if useScheduler {
							opErr = tree.SetWithRetryAndQueue(ctx, schedulerAdapter, key, value)
						} else {
							opErr = tree.Set(ctx, key, value)
						}
					} else {
						_, opErr = tree.Get(ctx, key)
					}
				}

				if opErr == nil {
					successCount.Add(1)
				}
			}
		}(i, threadDataArr[i])
	}

	wg.Wait()
	duration := time.Since(startTime)

	success := successCount.Load()
	if success == 0 {
		success = 1
	}

	throughput := float64(success) / duration.Seconds()
	latency := float64(duration.Nanoseconds()) / float64(success) / 1000

	return throughput, latency
}

func initializeData(ctx context.Context, tree *btree.BTree, count int) {
	fmt.Printf("初始化 %d 条数据...\n", count)

	start := time.Now()
	batchSize := 1000

	randBytes := make([]byte, count)
	for i := range randBytes {
		randBytes[i] = byte(i % 256)
	}

	// 预生成 keys/values
	initKeys := make([][]byte, count)
	initValues := make([][]byte, count)
	for j := 0; j < count; j++ {
		initKeys[j] = []byte(fmt.Sprintf("%ckey-%d", randBytes[j], j))
		initValues[j] = []byte(fmt.Sprintf("init-value-%d", j))
	}

	for i := 0; i < count; i += batchSize {
		end := min(i+batchSize, count)

		for j := i; j < end; j++ {
			if err := tree.Set(ctx, initKeys[j], initValues[j]); err != nil {
				fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
			}
		}

		if (i/batchSize)%10 == 0 {
			fmt.Printf("  进度: %d/%d (%.1f%%)\r", end, count, float64(end)*100/float64(count))
		}
	}

	fmt.Printf("\n初始化完成，耗时: %v\n\n", time.Since(start))
}

func warmup(ctx context.Context, tree *btree.BTree, scheduler btree.TaskScheduler, useScheduler bool, count int) {
	// 预生成 keys/values
	warmupKeys := make([][]byte, count)
	warmupValues := make([][]byte, count)
	for i := 0; i < count; i++ {
		warmupKeys[i] = []byte(fmt.Sprintf("warmup-key-%d", i%100))
		warmupValues[i] = []byte(fmt.Sprintf("warmup-value-%d", i))
	}

	for i := 0; i < count; i++ {
		if useScheduler && scheduler != nil {
			tree.SetWithRetryAndQueue(ctx, scheduler, warmupKeys[i], warmupValues[i])
		} else {
			tree.Set(ctx, warmupKeys[i], warmupValues[i])
		}
	}

	deleteKeys := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		deleteKeys[i] = []byte(fmt.Sprintf("warmup-key-%d", i))
	}
	for i := 0; i < 100; i++ {
		tree.Delete(ctx, deleteKeys[i])
	}

	runtime.GC()
}

type TaskSchedulerAdapter struct {
	scheduler *concurrency.TaskScheduler
}

func (a *TaskSchedulerAdapter) EnqueueWithShard(item any, taskName string) error {
	if shardItem, ok := item.(concurrency.ShardItem); ok {
		return a.scheduler.EnqueueWithShard(shardItem, taskName)
	}
	return fmt.Errorf("item does not implement ShardItem interface")
}
