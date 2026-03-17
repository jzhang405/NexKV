// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Main program for BTree memory-mode performance profiling (without persistence)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

var (
	// 并发度选项，支持逗号分隔的多个值，例如: -threads 1,2,4,8,16,32,64
	threadList string
)

func init() {
	flag.StringVar(&threadList, "threads", "1,2,4,8,16,32,64",
		"并发度列表（逗号分隔），例如: 1,2,4,8,16,32,64")
}

// parseThreadList 解析线程列表字符串
func parseThreadList(s string) []int {
	parts := strings.Split(s, ",")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if val, err := strconv.Atoi(part); err == nil && val > 0 {
			result = append(result, val)
		}
	}
	return result
}

func main() {
	flag.Parse()

	// ✅ 优化：设置 GOGC=400 减少 GC 触发频率，提升性能
	// 测试结果：GOGC=400 时吞吐量提升 49.9%（193K → 290K ops/sec）
	debug.SetGCPercent(400)

	// 使用空字符串启用纯内存模式（无持久化）
	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	ctx := context.Background()

	// 初始化数据：1M 个键值对
	fmt.Println("Initializing with 1M keys...")
	initCount := 1_000_000
	for i := 0; i < initCount; i++ {
		key := fmt.Sprintf("key-%07d", i)
		value := fmt.Sprintf("value-%07d", i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key %s: %v\n", key, err)
			os.Exit(1)
		}
		if i > 0 && i%100000 == 0 {
			fmt.Printf("  Initialized %d keys...\n", i)
		}
	}
	fmt.Printf("Initialized %d keys\n\n", initCount)

	// 解析线程列表
	goroutineCounts := parseThreadList(threadList)
	if len(goroutineCounts) == 0 {
		goroutineCounts = []int{1, 2, 4, 8, 16, 32, 64}
	}

	// 运行单线程基准测试
	fmt.Println("=== 单线程基准测试 ===")
	runSingleThreadTest(ctx, tree, initCount)

	// 运行高并发测试
	fmt.Println("\n=== 高并发性能测试 ===")
	fmt.Printf("测试并发度: %v\n\n", goroutineCounts)
	runConcurrentTests(ctx, tree, initCount, goroutineCounts)
}

// runSingleThreadTest 单线程基准测试
func runSingleThreadTest(ctx context.Context, tree *btree.BTree, initCount int) {
	opsCount := 100_000
	fmt.Printf("Running %d Set operations (single-threaded)...\n", opsCount)

	startTime := time.Now()
	for i := 0; i < opsCount; i++ {
		key := fmt.Sprintf("key-%07d", i%initCount)
		value := fmt.Sprintf("value-updated-%07d", i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key %s: %v\n", key, err)
			os.Exit(1)
		}
	}
	elapsed := time.Since(startTime)

	opsPerSec := float64(opsCount) / elapsed.Seconds()
	avgLatency := elapsed.Nanoseconds() / int64(opsCount)

	fmt.Printf("Throughput: %.0f ops/sec\n", opsPerSec)
	fmt.Printf("Average latency: %.2f μs/op\n", float64(avgLatency)/1000)
}

// runConcurrentTests 高并发测试
func runConcurrentTests(ctx context.Context, tree *btree.BTree, initCount int, goroutineCounts []int) {
	opsPerGoroutine := 10_000

	fmt.Println("并发度 | 总操作数 | 吞吐量 (ops/sec) | 平均延迟 (μs/op)")
	fmt.Println("------|----------|------------------|------------------")

	for _, goroutines := range goroutineCounts {
		totalOps := goroutines * opsPerGoroutine

		// 预热
		if goroutines > 1 {
			warmup(ctx, tree, goroutines, initCount)
		}

		// 正式测试
		var opsDone atomic.Int64
		startTime := time.Now()

		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				for i := 0; i < opsPerGoroutine; i++ {
					// 每个 goroutine 写入不同的 key 范围，避免冲突
					key := fmt.Sprintf("key-%07d", (goroutineID*opsPerGoroutine+i)%initCount)
					value := fmt.Sprintf("value-g%d-%05d", goroutineID, i)
					if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to set key: %v\n", err)
						return
					}
					opsDone.Add(1)
				}
			}(g)
		}
		wg.Wait()
		elapsed := time.Since(startTime)

		// 报告结果
		opsPerSec := float64(totalOps) / elapsed.Seconds()
		avgLatency := elapsed.Nanoseconds() / int64(totalOps)

		fmt.Printf("%6d | %8d | %16.0f | %16.2f\n",
			goroutines, totalOps, opsPerSec, float64(avgLatency)/1000)
	}
}

// warmup 预热，避免冷启动影响
func warmup(ctx context.Context, tree *btree.BTree, goroutines, initCount int) {
	warmupOps := 1000
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < warmupOps; i++ {
				key := fmt.Sprintf("key-%07d", (goroutineID*warmupOps+i)%initCount)
				tree.Set(ctx, []byte(key), []byte("warmup"))
			}
		}(g)
	}
	wg.Wait()
}
