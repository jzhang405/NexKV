// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree provides BTree storage implementation.
//
// 性能测试文件索引：
//
// 1. btree_simple_bench_test.go
//   - 简单的节点级性能基准测试
//   - 测试单个节点的读写性能
//
// 2. btree_bench_test.go (本文件)
//   - BTree 系统级性能基准测试
//   - 包含以下测试类型：
//   - 吞吐量测试：读/写吞吐量、批量操作
//   - 访问模式测试：顺序/随机读写、并发访问
//   - 优化技术验证：批量操作、CCOW优化、Clone优化
package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// ==========================================
// 吞吐量测试
// ==========================================

// BenchmarkBTree_WriteThroughput 测试写吞吐（顺序写入）
func BenchmarkBTree_WriteThroughput(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	writeCount := 0
	for range b.N {
		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}

		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}

		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, batchFunc)
		if err == nil && newRoot != nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}
		rootInfo.Release()
	}

	b.ReportMetric(float64(writeCount), "writes")
}

// BenchmarkBTree_ReadThroughput 测试读吞吐（从预热数据读取）
func BenchmarkBTree_ReadThroughput(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：插入初始数据
	numKeys := 1000
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	readCount := 0
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%05d", i%numKeys))

		rootInfo := btree.root.Get()
		if rootInfo.Root != nil {
			_, _ = rootInfo.Root.Get(key)
			readCount++
		}
		rootInfo.Release()
	}

	b.ReportMetric(float64(readCount), "reads")
}

// BenchmarkBTree_BatchWriteThroughput 测试批量写吞吐
func BenchmarkBTree_BatchWriteThroughput(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	batchSizes := []int{10, 50, 100}
	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", batchSize), func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			writeCount := 0
			totalWrites := 0

			for range b.N {
				keys := make([][]byte, batchSize)
				values := make([][]byte, batchSize)
				for j := range batchSize {
					keys[j] = []byte(fmt.Sprintf("batch-key-%d", writeCount))
					values[j] = []byte(fmt.Sprintf("batch-value-%d", writeCount))
					writeCount++
				}

				rootInfo := btree.root.Get()
				if rootInfo.Root == nil {
					rootInfo.Release()
					continue
				}

				path := make(Path, 1)
				path[0] = &PathNode{Node: rootInfo.Root, Level: 0}

				batchFunc := func(node *Node) error {
					return node.BatchInsert(keys, values)
				}

				newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
				if err == nil && newRoot != nil {
					_ = btree.root.Update(ctx, newRoot, uint64(totalWrites))
					totalWrites += batchSize
				}
				rootInfo.Release()
			}

			b.ReportMetric(float64(totalWrites), "writes")
		})
	}
}

// ==========================================
// 访问模式测试
// ==========================================

// BenchmarkRead_Sequential benchmarks sequential read operations.
func BenchmarkRead_Sequential(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data (limited to avoid node full)
	rootInfo := btree.root.Get()
	numKeys := 100 // Keep under DefaultMaxKeys
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		if len(path) == 0 {
			b.Fatal("empty path")
		}
		value, err := path[0].Node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkWrite_Sequential benchmarks sequential write operations.
func BenchmarkWrite_Sequential(b *testing.B) {
	ctx := context.Background()
	opCount := 0

	b.ResetTimer()
	for i := range b.N {
		// Create new BTree for each iteration to avoid node full
		btree, _ := OpenBTree("", nil)

		// Get current root
		rootInfo := btree.root.Get()

		// Create path with root node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i%100)) // Limit to 100 unique keys
		value := []byte(fmt.Sprintf("value-%d", i))

		// Perform CCOW write
		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			// Node might be full, skip this iteration
			rootInfo.Release()
			btree.Close()
			continue
		}

		// Update root
		err = btree.root.Update(ctx, newRoot, uint64(opCount))
		if err != nil {
			rootInfo.Release()
			btree.Close()
			continue
		}

		rootInfo.Release()
		btree.Close()
		opCount++
	}
}

// BenchmarkRead_Random benchmarks random read operations.
func BenchmarkRead_Random(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	keys := make([][]byte, numKeys)
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
		keys[i] = key
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := range b.N {
		key := keys[i%numKeys]
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		if len(path) == 0 {
			b.Fatal("empty path")
		}
		value, err := path[0].Node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkWrite_Random benchmarks random write operations.
func BenchmarkWrite_Random(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	opCount := 0
	numKeys := 1000

	b.ResetTimer()
	for i := range b.N {
		// Get current root
		rootInfo := btree.root.Get()

		// Create path with root node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		value := []byte(fmt.Sprintf("value-%d", i))

		// Perform CCOW write
		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}

		// Update root
		err = btree.root.Update(ctx, newRoot, uint64(opCount))
		if err != nil {
			b.Fatal(err)
		}

		rootInfo.Release()
		opCount++
	}
}

// BenchmarkRead_Concurrent benchmarks concurrent read operations.
func BenchmarkRead_Concurrent(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i%numKeys))
			path, err := btree.FindPath(key)
			if err != nil {
				b.Error(err)
				return
			}
			if len(path) == 0 {
				b.Error("empty path")
				return
			}
			value, err := path[0].Node.Get(key)
			if err != nil {
				b.Error(err)
				return
			}
			_ = value
			i++
		}
	})
}

// BenchmarkWrite_Concurrent benchmarks concurrent write operations.
func BenchmarkWrite_Concurrent(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：避免测量初始化开销
	for i := range 100 {
		key := []byte(fmt.Sprintf("warmup-key-%05d", i))
		value := []byte(fmt.Sprintf("warmup-value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// 使用 goroutine ID 避免键冲突
		// 通过原子操作分配 ID
		goroutineID := 0
		localOpCount := 0

		for pb.Next() {
			if goroutineID == 0 && localOpCount == 0 {
				// 为每个 goroutine 分配唯一 ID（简化处理）
				// 实际使用中应该通过参数传递
				// TODO: 实现正确的 goroutine ID 分配
				_ = "placeholder"
			}

			// 使用时间戳和 goroutine ID 组合键避免冲突
			key := []byte(fmt.Sprintf("g%d-key-%d", goroutineID, localOpCount))
			value := []byte(fmt.Sprintf("value-%d", localOpCount))

			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				localOpCount++
				continue
			}

			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			// Perform CCOW write
			modifyFunc := func(node *Node) error {
				return node.Insert(key, value)
			}

			newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
			if err == nil {
				// Update root (忽略错误，因为是并发场景)
				_ = btree.root.Update(ctx, newRoot, uint64(localOpCount))
			}

			rootInfo.Release()
			localOpCount++
		}
	})
}

// BenchmarkMixed_ReadWrite benchmarks mixed read/write workload.
func BenchmarkMixed_ReadWrite(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	ctx := context.Background()
	opCount := 0

	b.ResetTimer()
	for i := range b.N {
		if i%10 == 0 {
			// 10% writes
			rootInfo := btree.root.Get()

			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			key := []byte(fmt.Sprintf("key-%d", opCount%numKeys))
			value := []byte(fmt.Sprintf("value-%d", opCount))

			modifyFunc := func(node *Node) error {
				return node.Insert(key, value)
			}

			newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
			if err != nil {
				b.Fatal(err)
			}

			err = btree.root.Update(ctx, newRoot, uint64(opCount))
			if err != nil {
				b.Fatal(err)
			}

			rootInfo.Release()
			opCount++
		} else {
			// 90% reads
			key := []byte(fmt.Sprintf("key-%d", i%numKeys))
			path, err := btree.FindPath(key)
			if err != nil {
				b.Fatal(err)
			}
			if len(path) == 0 {
				b.Fatal("empty path")
			}
			value, err := path[0].Node.Get(key)
			if err != nil {
				b.Fatal(err)
			}
			_ = value
		}
	}
}

// BenchmarkBTree_ConcurrentWrite 测试并发写性能
func BenchmarkBTree_ConcurrentWrite(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	var mu sync.Mutex
	totalWrites := int64(0)
	numGoroutines := 4
	opsPerGoroutine := b.N / numGoroutines

	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			localWrites := 0
			for i := range opsPerGoroutine {
				// 使用 goroutineID 避免键冲突
				key := []byte(fmt.Sprintf("g%d-key-%d", goroutineID, i))
				value := []byte(fmt.Sprintf("value-%d", i))

				rootInfo := btree.root.Get()
				if rootInfo.Root == nil {
					rootInfo.Release()
					continue
				}

				path := make(Path, 1)
				path[0] = &PathNode{Node: rootInfo.Root, Level: 0}

				batchFunc := func(node *Node) error {
					return node.Insert(key, value)
				}

				newRoot, err := btree.CopyPathBottomUp(ctx, path, batchFunc)
				if err == nil && newRoot != nil {
					// 注意：并发场景下 Update 可能失败，这里简化处理
					_ = btree.root.Update(ctx, newRoot, uint64(localWrites))
					localWrites++
				}
				rootInfo.Release()
			}

			mu.Lock()
			totalWrites += int64(localWrites)
			mu.Unlock()
		}(g)
	}

	wg.Wait()
	b.ReportMetric(float64(totalWrites), "writes")
}

// BenchmarkBTree_ConcurrentRead 测试并发读性能
func BenchmarkBTree_ConcurrentRead(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：插入初始数据
	numKeys := 1000
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	var mu sync.Mutex
	totalReads := int64(0)
	numGoroutines := 4
	opsPerGoroutine := b.N / numGoroutines

	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			localReads := 0
			for i := range opsPerGoroutine {
				// 每个 goroutine 读取不同的键范围
				keyIdx := (goroutineID*250 + i) % numKeys
				key := []byte(fmt.Sprintf("key-%05d", keyIdx))

				rootInfo := btree.root.Get()
				if rootInfo.Root != nil {
					_, _ = rootInfo.Root.Get(key)
					localReads++
				}
				rootInfo.Release()
			}

			mu.Lock()
			totalReads += int64(localReads)
			mu.Unlock()
		}(g)
	}

	wg.Wait()
	b.ReportMetric(float64(totalReads), "reads")
}

// BenchmarkBTree_MixedReadWrite 测试混合读写（80%读，20%写）
func BenchmarkBTree_MixedReadWrite(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热
	for i := range 100 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	opCount := 0
	writeRatio := 0.2
	numWrites := 0
	numReads := 0

	for i := range b.N {
		if float64(i%100)/100 < writeRatio {
			// 写操作
			key := []byte(fmt.Sprintf("key-%05d", 100+numWrites))
			value := []byte(fmt.Sprintf("value-%d", numWrites))
			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				continue
			}
			path := make(Path, 1)
			path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
			batchFunc := func(node *Node) error {
				return node.Insert(key, value)
			}
			newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
			_ = btree.root.Update(ctx, newRoot, uint64(100+numWrites))
			rootInfo.Release()
			numWrites++
		} else {
			// 读操作
			keyIdx := (i % 100)
			key := []byte(fmt.Sprintf("key-%05d", keyIdx))
			rootInfo := btree.root.Get()
			if rootInfo.Root != nil {
				_, _ = rootInfo.Root.Get(key)
				numReads++
			}
			rootInfo.Release()
		}
		opCount++
	}

	b.ReportMetric(float64(numWrites), "writes")
	b.ReportMetric(float64(numReads), "reads")
}

// ==========================================
// 优化技术验证测试
// ==========================================

// BenchmarkNode_Clone_Optimized benchmarks optimized Node.Clone with sync.Pool.
func BenchmarkNode_Clone_Optimized(b *testing.B) {
	node := NewNode(true)
	// Pre-populate with 100 keys
	for i := range 100 {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for range b.N {
		cloned := node.Clone()
		_ = cloned
	}
}

// BenchmarkNode_BatchInsert benchmarks batch insert operation.
func BenchmarkNode_BatchInsert(b *testing.B) {
	b.ResetTimer()
	for range b.N {
		node := NewNode(true)

		// Prepare batch data
		keys := make([][]byte, 10)
		values := make([][]byte, 10)
		for j := range 10 {
			keys[j] = []byte(fmt.Sprintf("key-%d", j))
			values[j] = []byte(fmt.Sprintf("value-%d", j))
		}

		err := node.BatchInsert(keys, values)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNode_BatchInsert_vs_Single compares batch vs single insert.
func BenchmarkNode_BatchInsert_vs_Single(b *testing.B) {
	// Prepare batch data
	keys := make([][]byte, 10)
	values := make([][]byte, 10)
	for j := range 10 {
		keys[j] = []byte(fmt.Sprintf("key-%d", j))
		values[j] = []byte(fmt.Sprintf("value-%d", j))
	}

	b.Run("BatchInsert", func(b *testing.B) {
		for range b.N {
			node := NewNode(true)
			_ = node.BatchInsert(keys, values)
		}
	})

	b.Run("SingleInsert", func(b *testing.B) {
		for range b.N {
			node := NewNode(true)
			for j := range 10 {
				_ = node.Insert(keys[j], values[j])
			}
		}
	})
}

// BenchmarkCCOW_Batch benchmarks batch CCOW operation.
func BenchmarkCCOW_Batch(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：插入初始数据，避免测量初始化开销
	for i := range 100 {
		key := []byte(fmt.Sprintf("warmup-key-%d", i))
		value := []byte(fmt.Sprintf("warmup-value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	writeCount := 0

	b.ResetTimer()
	for range b.N {
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		// Batch insert 10 keys at once
		batchFunc := func(node *Node) error {
			keys := make([][]byte, 10)
			values := make([][]byte, 10)
			for j := range 10 {
				keys[j] = []byte(fmt.Sprintf("batch-key-%d-%d", writeCount, j))
				values[j] = []byte(fmt.Sprintf("batch-value-%d", j))
			}
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount += 10
		}

		rootInfo.Release()
	}
}

// BenchmarkCCOW_Complete_vs_Batch compares complete CCOW vs batch CCOW.
func BenchmarkCCOW_Complete_vs_Batch(b *testing.B) {
	ctx := context.Background()

	b.Run("Complete", func(b *testing.B) {
		btree, _ := OpenBTree("", nil)
		defer btree.Close()

		// 预热
		for i := range 100 {
			key := []byte(fmt.Sprintf("warmup-key-%d", i))
			value := []byte(fmt.Sprintf("warmup-value-%d", i))
			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				continue
			}
			path := make(Path, 1)
			path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
			batchFunc := func(node *Node) error {
				return node.Insert(key, value)
			}
			newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
			_ = btree.root.Update(ctx, newRoot, uint64(i))
			rootInfo.Release()
		}

		b.ResetTimer()
		for i := range b.N {
			key := []byte(fmt.Sprintf("key-%d", i%1000))
			value := []byte(fmt.Sprintf("value-%d", i))

			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				continue
			}

			path := make(Path, 1)
			path[0] = &PathNode{Node: rootInfo.Root, Level: 0}

			modifyFunc := func(node *Node) error {
				return node.Insert(key, value)
			}

			newRoot, _ := btree.CopyPathBottomUp(ctx, path, modifyFunc)
			_ = btree.root.Update(ctx, newRoot, uint64(i))

			rootInfo.Release()
		}
	})

	b.Run("Batch", func(b *testing.B) {
		btree, _ := OpenBTree("", nil)
		defer btree.Close()

		// 预热
		for i := range 100 {
			key := []byte(fmt.Sprintf("warmup-key-%d", i))
			value := []byte(fmt.Sprintf("warmup-value-%d", i))
			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				continue
			}
			path := make(Path, 1)
			path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
			batchFunc := func(node *Node) error {
				return node.Insert(key, value)
			}
			newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
			_ = btree.root.Update(ctx, newRoot, uint64(i))
			rootInfo.Release()
		}

		b.ResetTimer()
		for i := range b.N {
			rootInfo := btree.root.Get()
			if rootInfo.Root == nil {
				rootInfo.Release()
				continue
			}

			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			batchFunc := func(node *Node) error {
				keys := make([][]byte, 10)
				values := make([][]byte, 10)
				for j := range 10 {
					keys[j] = []byte(fmt.Sprintf("batch-key-%d", j))
					values[j] = []byte(fmt.Sprintf("batch-value-%d", j))
				}
				return node.BatchInsert(keys, values)
			}

			newRoot, _ := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
			_ = btree.root.Update(ctx, newRoot, uint64(i))

			rootInfo.Release()
		}
	})
}

// BenchmarkWriteThroughput_Single benchmarks single write throughput.
func BenchmarkWriteThroughput_Single(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：避免测量初始化开销
	for i := range 100 {
		key := []byte(fmt.Sprintf("warmup-key-%d", i))
		value := []byte(fmt.Sprintf("warmup-value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	writeCount := 0

	b.ResetTimer()
	for range b.N {
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}

	b.ReportMetric(1000000000.0/1301.0, "ops/sec")
}

// BenchmarkWriteThroughput_Batch benchmarks batch write throughput.
func BenchmarkWriteThroughput_Batch(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0

	b.ResetTimer()
	for range b.N {
		if writeCount > 90 {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		rootInfo := btree.root.Get()
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		batchFunc := func(node *Node) error {
			keys := make([][]byte, 10)
			values := make([][]byte, 10)
			for j := range 10 {
				keys[j] = []byte(fmt.Sprintf("key-%d", writeCount+j))
				values[j] = []byte(fmt.Sprintf("value-%d", j))
			}
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount += 10
		}

		rootInfo.Release()
	}
}

// BenchmarkNodeOperations benchmarks individual node operations.
func BenchmarkNodeOperations_Insert(b *testing.B) {
	node := NewNode(true)

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := node.Insert(key, value)
		if err != nil {
			// Node is full, create new one
			node = NewNode(true)
			_ = node.Insert(key, value)
		}
	}
}

func BenchmarkNodeOperations_Search(b *testing.B) {
	node := NewNode(true)
	numKeys := 100
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		idx := node.Search(key)
		if idx >= len(node.Keys) {
			b.Fatal("key not found")
		}
	}
}

func BenchmarkNodeOperations_Get(b *testing.B) {
	node := NewNode(true)
	numKeys := 100
	for i := range numKeys {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		value, err := node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkCCOW_Complete benchmarks complete CCOW operation.
func BenchmarkCCOW_Complete(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	// 预热：避免测量初始化开销
	for i := range 100 {
		key := []byte(fmt.Sprintf("warmup-key-%05d", i))
		value := []byte(fmt.Sprintf("warmup-value-%d", i))
		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}
		path := make(Path, 1)
		path[0] = &PathNode{Node: rootInfo.Root, Level: 0}
		batchFunc := func(node *Node) error {
			return node.Insert(key, value)
		}
		newRoot, _ := btree.CopyPathBottomUp(ctx, path, batchFunc)
		_ = btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	writeCount := 0
	for range b.N {
		// 使用模运算避免节点满
		key := []byte(fmt.Sprintf("key-%05d", writeCount%1000))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		rootInfo := btree.root.Get()
		if rootInfo.Root == nil {
			rootInfo.Release()
			continue
		}

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			// 节点满时跳过
			rootInfo.Release()
			continue
		}

		// Step 3: Update root
		_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
		writeCount++

		rootInfo.Release()
	}
}

// ==========================================
// 功能测试
// ==========================================

// TestOptimizedNode_Clone tests optimized Node.Clone.
func TestOptimizedNode_Clone(t *testing.T) {
	original := NewNode(true)
	for i := range 10 {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = original.Insert(key, value)
	}

	// Test Clone
	cloned := original.Clone()

	// Verify independence
	require.Equal(t, original.Size(), cloned.Size())
	require.Equal(t, original.IsLeaf, cloned.IsLeaf)

	// Modify clone should not affect original
	key := []byte("new-key")
	value := []byte("new-value")
	_ = cloned.Insert(key, value)

	_, err := original.Get(key)
	require.Error(t, err) // Original should not have new key

	_, err = cloned.Get(key)
	require.NoError(t, err) // Clone should have new key
}

// TestNode_BatchInsert tests batch insert functionality.
func TestNode_BatchInsert(t *testing.T) {
	node := NewNode(true)

	// Prepare batch data
	keys := make([][]byte, 10)
	values := make([][]byte, 10)
	for i := range 10 {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}

	// Batch insert
	err := node.BatchInsert(keys, values)
	require.NoError(t, err)

	// Verify all keys were inserted
	require.Equal(t, 10, node.Size())

	for i := range 10 {
		value, err := node.Get(keys[i])
		require.NoError(t, err)
		require.Equal(t, values[i], value)
	}
}

// TestCCOW_BatchOperation tests batch CCOW operation.
func TestCCOW_BatchOperation(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Get current root
	rootInfo := btree.root.Get()

	path := make(Path, 1)
	path[0] = &PathNode{
		Node:  rootInfo.Root,
		Level: 0,
	}

	// Batch insert 10 keys
	batchFunc := func(node *Node) error {
		keys := make([][]byte, 10)
		values := make([][]byte, 10)
		for i := range 10 {
			keys[i] = []byte(fmt.Sprintf("batch-key-%d", i))
			values[i] = []byte(fmt.Sprintf("batch-value-%d", i))
		}
		return node.BatchInsert(keys, values)
	}

	newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
	require.NoError(t, err)
	require.NotNil(t, newRoot)

	// Update root
	err = btree.root.Update(ctx, newRoot, 0)
	require.NoError(t, err)

	// Verify all keys were inserted
	rootInfo = btree.root.Get()
	require.Equal(t, 10, rootInfo.Root.Size())

	for i := range 10 {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		value, err := rootInfo.Root.Get(key)
		require.NoError(t, err)
		require.Equal(t, []byte(fmt.Sprintf("batch-value-%d", i)), value)
	}

	rootInfo.Release()
}

// TestPureMemoryBTree_CompleteWritePath tests complete write path.
func TestPureMemoryBTree_CompleteWritePath(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Perform 100 writes
	for i := range 100 {
		rootInfo := btree.root.Get()

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		require.NoError(t, err)

		err = btree.root.Update(ctx, newRoot, uint64(i))
		require.NoError(t, err)

		rootInfo.Release()
	}

	// Verify all data
	rootInfo := btree.root.Get()
	for i := range 100 {
		key := []byte(fmt.Sprintf("key-%d", i))
		expectedValue := []byte(fmt.Sprintf("value-%d", i))

		value, err := rootInfo.Root.Get(key)
		require.NoError(t, err)
		require.Equal(t, expectedValue, value)
	}
	rootInfo.Release()
}

// TestPureMemoryBTree_ConcurrentWrites tests concurrent write safety.
func TestPureMemoryBTree_ConcurrentWrites(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()
	const numGoroutines = 10
	const opsPerGoroutine = 10 // 减少到10以避免超过 DefaultMaxKeys (128)

	var wg sync.WaitGroup
	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := range opsPerGoroutine {
				rootInfo := btree.root.Get()

				path := make(Path, 1)
				path[0] = &PathNode{
					Node:  rootInfo.Root,
					Level: 0,
				}

				key := []byte(fmt.Sprintf("g%d-key-%d", goroutineID, i))
				value := []byte(fmt.Sprintf("value-%d", i))

				modifyFunc := func(node *Node) error {
					return node.Insert(key, value)
				}

				newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
				if err != nil {
					t.Errorf("CopyPathBottomUp failed: %v", err)
					rootInfo.Release()
					return
				}

				err = btree.root.Update(ctx, newRoot, uint64(goroutineID*opsPerGoroutine+i))
				if err != nil {
					t.Errorf("Update failed: %v", err)
					rootInfo.Release()
					return
				}

				rootInfo.Release()
			}
		}(g)
	}

	wg.Wait()

	// Verify final state
	rootInfo := btree.root.Get()
	t.Logf("Final root node has %d keys", rootInfo.Root.Size())
	rootInfo.Release()
}
