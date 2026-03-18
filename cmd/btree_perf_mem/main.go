// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Main program for BTree memory-mode performance profiling (without persistence)
// 支持 Set、Get、Mixed、BatchSet、BatchGet 等多种操作模式
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
	// 操作类型：set, get, mixed, batch-set, batch-get
	opsType string

	// 并发度选项，支持逗号分隔的多个值
	threadList string

	// 每线程操作数
	opsPerThread int

	// 初始化数据量
	initCount int

	// 实际初始化的 key 数量（用于 Get 操作）
	actualInitializedKeys int

	// 混合操作的读写比例（如 80:20 表示 80% 读 20% 写）
	ratio string

	// 是否跳过初始化
	skipInit bool

	// Batch 操作的批次大小
	batchSize int
)

func init() {
	flag.StringVar(&opsType, "ops", "set",
		"操作类型: set, get, mixed, batch-set, batch-get (默认: set)")
	flag.StringVar(&threadList, "threads", "1,2,4,8",
		"并发度列表（逗号分隔），例如: 1,2,4,8,16,32,64")
	flag.IntVar(&opsPerThread, "count", 10000,
		"每线程操作数 (默认: 10000)")
	flag.IntVar(&initCount, "init", 1000000,
		"初始化数据量 (默认: 1000000)")
	flag.StringVar(&ratio, "ratio", "80:20",
		"混合操作的读写比例，格式 读:写 (默认: 80:20)")
	flag.BoolVar(&skipInit, "skip-init", false,
		"跳过数据初始化 (默认: false)")
	flag.IntVar(&batchSize, "batch", 100,
		"Batch 操作的批次大小 (默认: 100)")
}

// OperationType 操作类型枚举
type OperationType int

const (
	OpSet OperationType = iota
	OpGet
	OpMixed
	OpBatchSet
	OpBatchGet
)

// parseOperationType 解析操作类型
func parseOperationType(s string) OperationType {
	switch strings.ToLower(s) {
	case "set":
		return OpSet
	case "get":
		return OpGet
	case "mixed":
		return OpMixed
	case "batch-set", "batchset":
		return OpBatchSet
	case "batch-get", "batchget":
		return OpBatchGet
	default:
		return OpSet
	}
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

// parseRatio 解析读写比例，返回 (readPercent, writePercent)
func parseRatio(s string) (int, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 80, 20 // 默认 80:20
	}
	read, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	write, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 80, 20
	}
	total := read + write
	if total == 0 {
		return 80, 20
	}
	// 归一化到 100
	readPercent := read * 100 / total
	writePercent := 100 - readPercent
	return readPercent, writePercent
}

func main() {
	flag.Parse()

	// 设置 GC 优化
	debug.SetGCPercent(400)

	// 使用空字符串启用纯内存模式（无持久化）
	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	ctx := context.Background()

	// 解析操作类型
	opType := parseOperationType(opsType)

	// 初始化数据（如果需要）
	if !skipInit && opType != OpSet {
		fmt.Printf("Initializing with %d keys...\n", initCount)
		initializeData(ctx, tree, initCount)
		fmt.Printf("Initialized %d keys\n\n", initCount)
	}

	// 解析线程列表
	goroutineCounts := parseThreadList(threadList)
	if len(goroutineCounts) == 0 {
		goroutineCounts = []int{1, 2, 4, 8}
	}

	// 运行单线程基准测试
	fmt.Println("=== 单线程基准测试 ===")
	runSingleThreadTest(ctx, tree, opType)

	// 运行高并发测试
	fmt.Println("\n=== 高并发性能测试 ===")
	fmt.Printf("操作类型: %s\n", opsType)
	fmt.Printf("测试并发度: %v\n\n", goroutineCounts)
	runConcurrentTests(ctx, tree, opType, goroutineCounts)
}

// initializeData 初始化测试数据
func initializeData(ctx context.Context, tree *btree.BTree, count int) {
	startTime := time.Now()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key-%07d", i)
		value := fmt.Sprintf("value-%07d", i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key %s: %v\n", key, err)
			os.Exit(1)
		}
		if i > 0 && i%100000 == 0 {
			elapsed := time.Since(startTime)
			rate := float64(i) / elapsed.Seconds()
			fmt.Printf("  Initialized %d keys... (%.0f keys/sec)\n", i, rate)
		}
	}
	// ✅ 记录实际初始化的 key 数量
	actualInitializedKeys = count
}

// runSingleThreadTest 单线程基准测试
func runSingleThreadTest(ctx context.Context, tree *btree.BTree, opType OperationType) {
	// ✅ 修复：使用更小的测试量，避免超出初始化范围
	testCount := 10_000
	if opType == OpSet {
		testCount = 100_000 // Set 可以使用更大的测试量
	}
	fmt.Printf("Running %d %s operations (single-threaded)...\n", testCount, opsType)

	startTime := time.Now()
	var successCount int64

	switch opType {
	case OpSet:
		successCount = runSetOperations(ctx, tree, testCount, 0)

	case OpGet:
		successCount = runGetOperations(ctx, tree, testCount, 0)

	case OpMixed:
		readPercent, _ := parseRatio(ratio)
		readCount := testCount * readPercent / 100
		writeCount := testCount - readCount
		successCount = runMixedOperations(ctx, tree, readCount, writeCount, 0)

	case OpBatchSet:
		if testCount%batchSize != 0 {
			testCount = (testCount/batchSize + 1) * batchSize
		}
		successCount = runBatchSetOperations(ctx, tree, testCount, batchSize, 0)

	case OpBatchGet:
		if testCount%batchSize != 0 {
			testCount = (testCount/batchSize + 1) * batchSize
		}
		successCount = runBatchGetOperations(ctx, tree, testCount, batchSize, 0)
	}

	elapsed := time.Since(startTime)
	opsPerSec := float64(successCount) / elapsed.Seconds()
	avgLatency := elapsed.Nanoseconds() / int64(successCount)

	fmt.Printf("成功操作: %d\n", successCount)
	fmt.Printf("Throughput: %.0f ops/sec\n", opsPerSec)
	fmt.Printf("Average latency: %.2f μs/op\n\n", float64(avgLatency)/1000)
}

// runConcurrentTests 高并发测试
func runConcurrentTests(ctx context.Context, tree *btree.BTree, opType OperationType, goroutineCounts []int) {
	fmt.Println("并发度 | 总操作数 | 成功操作 | 吞吐量 (ops/sec) | 平均延迟 (μs/op)")
	fmt.Println("------|----------|----------|------------------|------------------")

	for _, goroutines := range goroutineCounts {
		totalOps := goroutines * opsPerThread

		// 预热
		if goroutines > 1 {
			warmup(ctx, tree, goroutines, opType)
		}

		// 正式测试
		var opsDone atomic.Int64
		var successCount atomic.Int64
		startTime := time.Now()

		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()

				var success int64
				switch opType {
				case OpSet:
					success = runSetOperations(ctx, tree, opsPerThread, goroutineID)
				case OpGet:
					success = runGetOperations(ctx, tree, opsPerThread, goroutineID)
				case OpMixed:
					readPercent, _ := parseRatio(ratio)
					readCount := opsPerThread * readPercent / 100
					writeCount := opsPerThread - readCount
					success = runMixedOperations(ctx, tree, readCount, writeCount, goroutineID)
				case OpBatchSet:
					success = runBatchSetOperations(ctx, tree, opsPerThread, batchSize, goroutineID)
				case OpBatchGet:
					success = runBatchGetOperations(ctx, tree, opsPerThread, batchSize, goroutineID)
				}

				opsDone.Add(int64(opsPerThread))
				successCount.Add(success)
			}(g)
		}
		wg.Wait()
		elapsed := time.Since(startTime)

		// 报告结果
		totalSuccess := successCount.Load()
		opsPerSec := float64(totalSuccess) / elapsed.Seconds()
		avgLatency := elapsed.Nanoseconds() / int64(totalSuccess)

		fmt.Printf("%6d | %8d | %8d | %16.0f | %16.2f\n",
			goroutines, totalOps, totalSuccess, opsPerSec, float64(avgLatency)/1000)
	}
}

// ============================================================================
// Set 操作
// ============================================================================

func runSetOperations(ctx context.Context, tree *btree.BTree, count int, workerID int) int64 {
	var success int64
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key-%07d", (workerID*count+i)%initCount)
		value := fmt.Sprintf("value-%d-%05d", workerID, i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err == nil {
			success++
		}
	}
	return success
}

// ============================================================================
// Get 操作
// ============================================================================

func runGetOperations(ctx context.Context, tree *btree.BTree, count int, workerID int) int64 {
	var success int64
	// ✅ 修复：使用实际初始化的 key 数量，而不是全局 initCount
	// 避免访问未初始化的 key
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key-%07d", (workerID*count+i)%actualInitializedKeys)
		if _, err := tree.Get(ctx, []byte(key)); err == nil {
			success++
		}
	}
	return success
}

// ============================================================================
// Mixed 操作（读写混合）
// ============================================================================

func runMixedOperations(ctx context.Context, tree *btree.BTree, readCount, writeCount, workerID int) int64 {
	var success int64

	// 先执行读操作
	for i := 0; i < readCount; i++ {
		key := fmt.Sprintf("key-%07d", (workerID*100000+i)%actualInitializedKeys)
		if _, err := tree.Get(ctx, []byte(key)); err == nil {
			success++
		}
	}

	// 再执行写操作
	for i := 0; i < writeCount; i++ {
		key := fmt.Sprintf("key-%07d", (workerID*100000+readCount+i)%actualInitializedKeys)
		value := fmt.Sprintf("value-mixed-%d-%05d", workerID, i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err == nil {
			success++
		}
	}

	return success
}

// ============================================================================
// Batch Set 操作（预留接口）
// ============================================================================

func runBatchSetOperations(ctx context.Context, tree *btree.BTree, count, batchSize, workerID int) int64 {
	// TODO: 实现 BatchSet 接口
	// 当前使用循环调用 Set 作为 fallback
	var success int64
	batches := (count + batchSize - 1) / batchSize

	for b := 0; b < batches; b++ {
		currentBatchSize := batchSize
		if b == batches-1 && count%batchSize != 0 {
			currentBatchSize = count % batchSize
		}

		for i := 0; i < currentBatchSize; i++ {
			idx := b*batchSize + i
			key := fmt.Sprintf("key-%07d", (workerID*count+idx)%initCount)
			value := fmt.Sprintf("value-batch-%d-%05d", workerID, idx)
			if err := tree.Set(ctx, []byte(key), []byte(value)); err == nil {
				success++
			}
		}
	}

	return success
}

// ============================================================================
// Batch Get 操作（预留接口）
// ============================================================================

func runBatchGetOperations(ctx context.Context, tree *btree.BTree, count, batchSize, workerID int) int64 {
	// TODO: 实现 BatchGet 接口
	// 当前使用循环调用 Get 作为 fallback
	var success int64
	batches := (count + batchSize - 1) / batchSize

	for b := 0; b < batches; b++ {
		currentBatchSize := batchSize
		if b == batches-1 && count%batchSize != 0 {
			currentBatchSize = count % batchSize
		}

		for i := 0; i < currentBatchSize; i++ {
			idx := b*batchSize + i
			key := fmt.Sprintf("key-%07d", (workerID*count+idx)%actualInitializedKeys)
			if _, err := tree.Get(ctx, []byte(key)); err == nil {
				success++
			}
		}
	}

	return success
}

// ============================================================================
// 预热
// ============================================================================

// warmup 预热，避免冷启动影响
func warmup(ctx context.Context, tree *btree.BTree, goroutines int, opType OperationType) {
	warmupOps := 1000
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			switch opType {
			case OpSet:
				for i := 0; i < warmupOps; i++ {
					key := fmt.Sprintf("key-%07d", (goroutineID*warmupOps+i)%initCount)
					tree.Set(ctx, []byte(key), []byte("warmup"))
				}
			case OpGet:
				for i := 0; i < warmupOps; i++ {
					key := fmt.Sprintf("key-%07d", (goroutineID*warmupOps+i)%initCount)
					tree.Get(ctx, []byte(key))
				}
			case OpMixed, OpBatchSet, OpBatchGet:
				// 混合预热
				for i := 0; i < warmupOps; i++ {
					if i%2 == 0 {
						key := fmt.Sprintf("key-%07d", (goroutineID*warmupOps+i)%initCount)
						tree.Get(ctx, []byte(key))
					} else {
						key := fmt.Sprintf("key-%07d", (goroutineID*warmupOps+i)%initCount)
						tree.Set(ctx, []byte(key), []byte("warmup"))
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
