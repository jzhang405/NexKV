// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Main program for BTree memory-mode performance profiling (without persistence)
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

func main() {
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

	// 运行 100,000 次 Set 操作（纯内存模式）
	opsCount := 100_000
	fmt.Printf("Running %d Set operations (memory mode, no persistence)...\n", opsCount)

	startTime := time.Now()
	for i := 0; i < opsCount; i++ {
		// 更新已有键（key-0000000 到 key-0099999）
		key := fmt.Sprintf("key-%07d", i%initCount)
		value := fmt.Sprintf("value-updated-%07d", i)
		if err := tree.Set(ctx, []byte(key), []byte(value)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key %s: %v\n", key, err)
			os.Exit(1)
		}
	}
	elapsed := time.Since(startTime)

	// 报告结果
	opsPerSec := float64(opsCount) / elapsed.Seconds()
	avgLatency := elapsed.Nanoseconds() / int64(opsCount)

	fmt.Println("\n=== Memory Mode Performance Results ===")
	fmt.Printf("Total operations: %d\n", opsCount)
	fmt.Printf("Total time: %.2f seconds\n", elapsed.Seconds())
	fmt.Printf("Throughput: %.0f ops/sec\n", opsPerSec)
	fmt.Printf("Average latency: %.2f μs/op\n", float64(avgLatency)/1000)

	// ✅ 调试：PageLock TryLock 统计
	success, failure := btree.GetTryLockStats()
	total := success + failure
	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		fmt.Printf("\n=== PageLock TryLock Stats ===\n")
		fmt.Printf("Success: %d (%.1f%%)\n", success, successRate)
		fmt.Printf("Failure: %d (%.1f%%)\n", failure, float64(failure)/float64(total)*100)
		fmt.Printf("Total: %d\n", total)
	}

	fmt.Println("\nNote: This is pure memory mode (no disk I/O)")
}
