// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// BTree Set/Get 操作性能测试工具 — 对比 direct vs scheduler 路径
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"

	"net/http"
	_ "net/http/pprof"
)

var (
	threadList   string
	opsPerThread int
	benchMode    string
	initCount    int
	opType       string // "set", "get", "mixed"
)

func init() {
	flag.StringVar(&threadList, "threads", "1,2,4,8",
		"并发度列表（逗号分隔），例如: 1,2,4,8,16")
	flag.IntVar(&opsPerThread, "count", 50000,
		"每线程操作数 (默认: 50000)")
	flag.StringVar(&benchMode, "mode", "direct",
		"测试模式: direct(直接路径) 或 scheduler(TaskScheduler路径)")
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

	useScheduler := benchMode == "scheduler"
	modeLabel := "direct"
	if useScheduler {
		modeLabel = "scheduler"
	}

	fmt.Printf("========================================\n")
	fmt.Printf("BTree 性能测试 (%s 模式)\n", modeLabel)
	fmt.Printf("========================================\n")
	fmt.Printf("操作类型: %s\n", opType)
	fmt.Printf("路径: %s\n", modeLabel)
	fmt.Printf("并发度: %v\n", threads)
	fmt.Printf("每线程操作数: %d\n", opsPerThread)
	fmt.Printf("初始化数据量: %d\n", initCount)
	fmt.Printf("========================================\n\n")

	fmt.Printf("%-10s | %-12s | %-14s | %-14s | %-14s\n",
		"并发度", "操作", "总操作数", "吞吐量(ops/s)", "平均延迟(μs)")
	fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")

	for _, numThreads := range threads {
		totalOps := numThreads * opsPerThread
		throughput, latency := runTest(numThreads, totalOps, useScheduler)
		fmt.Printf("%-10d | %-12s | %-14d | %-14.0f | %-14.2f\n",
			numThreads, opType, totalOps, throughput, latency)

		if numThreads != threads[len(threads)-1] {
			fmt.Printf("-----------|--------------|--------------|----------------|----------------\n")
		}
	}
}

func parseThreadList(s string) []int {
	result := make([]int, 0)
	current := 0
	for _, ch := range s {
		if ch == ',' {
			if current > 0 {
				result = append(result, current)
			}
			current = 0
		} else if ch >= '0' && ch <= '9' {
			current = current*10 + int(ch-'0')
		}
	}
	if current > 0 {
		result = append(result, current)
	}
	return result
}

func runTest(numThreads, totalOps int, useScheduler bool) (float64, float64) {
	ctx := context.Background()

	// 核心区别：DisableScheduler 控制是否走 TaskScheduler 路径
	config := &model.BTreeConfig{
		DisableScheduler: !useScheduler,
	}
	tree, err := btree.OpenBTree("", config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	// 初始化数据
	if initCount > 0 {
		initializeData(ctx, tree, initCount)
	}

	// 预热
	warmup(ctx, tree, 1000)

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
		for j := 0; j < opsPerThread; j++ {
			td.keys[j] = []byte(fmt.Sprintf("key-%d-%d", i, j%initCount))
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
					opErr = tree.Set(ctx, key, value)
				case "mixed":
					if j%2 == 0 {
						value := td.values[j]
						opErr = tree.Set(ctx, key, value)
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

	// 预生成 keys/values
	initKeys := make([][]byte, count)
	initValues := make([][]byte, count)
	for j := 0; j < count; j++ {
		initKeys[j] = []byte(fmt.Sprintf("key-%d", j))
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

func warmup(ctx context.Context, tree *btree.BTree, count int) {
	// 预生成 keys/values
	warmupKeys := make([][]byte, count)
	warmupValues := make([][]byte, count)
	for i := 0; i < count; i++ {
		warmupKeys[i] = []byte(fmt.Sprintf("warmup-key-%d", i%100))
		warmupValues[i] = []byte(fmt.Sprintf("warmup-value-%d", i))
	}

	for i := 0; i < count; i++ {
		tree.Set(ctx, warmupKeys[i], warmupValues[i])
	}
}
