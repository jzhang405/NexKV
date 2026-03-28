// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// BTree Set 操作性能对比测试工具
// 对比 Set() vs SetWithRetryAndQueue() + TaskScheduler 的性能
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
	// 并发度列表
	threadList string

	// 每线程操作数
	opsPerThread int

	// 测试模式：direct, scheduler, both
	mode string

	// 初始化数据量
	initCount int
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

	// 解析并发度列表
	threads := parseThreadList(threadList)
	if len(threads) == 0 {
		fmt.Fprintf(os.Stderr, "无效的并发度列表: %s\n", threadList)
		os.Exit(1)
	}

	// 自动设置初始化数据量
	if initCount == 0 {
		initCount = opsPerThread * threads[len(threads)-1] / 10
		if initCount < 1000 {
			initCount = 1000
		}
	}

	fmt.Printf("========================================\n")
	fmt.Printf("BTree Set 性能测试\n")
	fmt.Printf("========================================\n")
	fmt.Printf("测试模式: %s\n", mode)
	fmt.Printf("并发度: %v\n", threads)
	fmt.Printf("每线程操作数: %d\n", opsPerThread)
	fmt.Printf("初始化数据量: %d\n", initCount)
	fmt.Printf("说明: Builtin=内置Scheduler, Custom=嵌套Scheduler(对比用)\n")
	fmt.Printf("========================================\n\n")

	// 运行性能测试
	runBenchmark(threads)
}

// parseThreadList 解析线程列表
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

// splitAndTrim 分割字符串并去除空格
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

// TestMode 测试模式
type TestMode int

const (
	ModeBuiltin TestMode = iota // 使用 BTree 内置 TaskScheduler (推荐，无嵌套)
	ModeCustom                  // 使用自定义 TaskScheduler (对比测试，嵌套 scheduler)
)

func parseMode(s string) TestMode {
	switch s {
	case "builtin":
		return ModeBuiltin
	case "custom":
		return ModeCustom
	// 兼容旧参数名
	case "direct":
		return ModeBuiltin
	case "scheduler":
		return ModeCustom
	default:
		return ModeBuiltin
	}
}

// runBenchmark 运行性能测试
func runBenchmark(threads []int) {
	testMode := parseMode(mode)

	// 结果表格
	fmt.Printf("%-10s | %-12s | %-12s | %-14s | %-14s\n",
		"并发度", "模式", "总操作数", "吞吐量(ops/s)", "平均延迟(μs)")
	fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")

	for _, numThreads := range threads {
		totalOps := numThreads * opsPerThread

		// 测试 Builtin 模式 (BTree 内置 TaskScheduler，推荐)
		if testMode == ModeBuiltin {
			throughput, latency := runTest(numThreads, totalOps, false)
			fmt.Printf("%-10d | %-12s | %-12d | %-14.0f | %-14.2f\n",
				numThreads, "Builtin", totalOps, throughput, latency)
		}

		// 测试 Custom 模式 (自定义 TaskScheduler，对比测试)
		if testMode == ModeCustom {
			throughput, latency := runTest(numThreads, totalOps, true)
			fmt.Printf("%-10d | %-12s | %-12d | %-14.0f | %-14.2f\n",
				numThreads, "Custom", totalOps, throughput, latency)
		}

		if numThreads != threads[len(threads)-1] {
			fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")
		}
	}
}

// runTest 运行单次测试
func runTest(numThreads, totalOps int, useScheduler bool) (float64, float64) {
	ctx := context.Background()

	// 创建 BTree
	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	// 创建 TaskScheduler（如果需要）
	var scheduler *concurrency.TaskScheduler
	var schedulerAdapter btree.TaskScheduler
	if useScheduler {
		// 自动检测 CPU 核心数
		schedulerCores := runtime.NumCPU()
		scheduler = concurrency.NewTaskScheduler("btree-perf", schedulerCores)
		schedulerAdapter = &TaskSchedulerAdapter{scheduler: scheduler}
		defer scheduler.Stop()
	}

	// 初始化数据（如果需要）
	if initCount > 0 {
		initializeData(ctx, tree, initCount)
	}

	// 预热
	warmup(ctx, tree, schedulerAdapter, useScheduler, 1000)

	// 运行测试
	successCount := atomic.Int64{}
	var wg sync.WaitGroup
	startTime := time.Now()

	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()

			// 每个线程预生成随机字节
			randBytes := make([]byte, opsPerThread)
			for r := range randBytes {
				randBytes[r] = byte(r % 256)
			}

			for j := 0; j < opsPerThread; j++ {
				// 使用随机字节作为第一个字节，增加 key 分散性
				key := fmt.Sprintf("%ckey-%d-%d", randBytes[j], threadID, j%initCount)
				value := fmt.Sprintf("value-%d", j)

				var err error
				if useScheduler {
					err = tree.SetWithRetryAndQueue(ctx, schedulerAdapter, []byte(key), []byte(value))
				} else {
					err = tree.Set(ctx, []byte(key), []byte(value))
				}

				if err == nil {
					successCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	success := successCount.Load()
	if success == 0 {
		success = 1 // 避免除零
	}

	// 计算吞吐量和延迟
	throughput := float64(success) / duration.Seconds()
	latency := float64(duration.Nanoseconds()) / float64(success) / 1000 // μs

	return throughput, latency
}

// initializeData 初始化数据
func initializeData(ctx context.Context, tree *btree.BTree, count int) {
	fmt.Printf("初始化 %d 条数据...\n", count)

	start := time.Now()
	batchSize := 1000

	// 预生成随机字节用于 key 前缀
	randBytes := make([]byte, count)
	for i := range randBytes {
		randBytes[i] = byte(i % 256)
	}

	for i := 0; i < count; i += batchSize {
		end := min(i+batchSize, count)

		for j := i; j < end; j++ {
			// 使用随机字节作为第一个字节
			key := fmt.Sprintf("%ckey-%d", randBytes[j], j)
			value := fmt.Sprintf("init-value-%d", j)
			if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
				fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
			}
		}

		if (i/batchSize)%10 == 0 {
			fmt.Printf("  进度: %d/%d (%.1f%%)\r", end, count, float64(end)*100/float64(count))
		}
	}

	fmt.Printf("\n初始化完成，耗时: %v\n\n", time.Since(start))
}

// warmup 预热
func warmup(ctx context.Context, tree *btree.BTree, scheduler btree.TaskScheduler, useScheduler bool, count int) {
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("warmup-key-%d", i%100)
		value := fmt.Sprintf("warmup-value-%d", i)

		if useScheduler && scheduler != nil {
			tree.SetWithRetryAndQueue(ctx, scheduler, []byte(key), []byte(value))
		} else {
			tree.Set(ctx, []byte(key), []byte(value))
		}
	}

	// 清理预热数据
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("warmup-key-%d", i)
		tree.Delete(ctx, []byte(key))
	}

	runtime.GC()
}

// TaskSchedulerAdapter 适配器，将 *concurrency.TaskScheduler 转换为 btree.TaskScheduler
type TaskSchedulerAdapter struct {
	scheduler *concurrency.TaskScheduler
}

func (a *TaskSchedulerAdapter) EnqueueWithShard(item any, taskName string) error {
	if shardItem, ok := item.(concurrency.ShardItem); ok {
		return a.scheduler.EnqueueWithShard(shardItem, taskName)
	}
	return fmt.Errorf("item does not implement ShardItem interface")
}
