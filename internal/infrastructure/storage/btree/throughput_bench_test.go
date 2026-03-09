// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// BenchmarkBTree_WriteThroughput 测试写吞吐（顺序写入）
func BenchmarkBTree_WriteThroughput(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	writeCount := 0
	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(writeCount))
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
	for i := 0; i < numKeys; i++ {
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
		btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	readCount := 0
	for i := 0; i < b.N; i++ {
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

			for i := 0; i < b.N; i++ {
				keys := make([][]byte, batchSize)
				values := make([][]byte, batchSize)
				for j := 0; j < batchSize; j++ {
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
					btree.root.Update(ctx, newRoot, uint64(totalWrites))
					totalWrites += batchSize
				}
				rootInfo.Release()
			}

			b.ReportMetric(float64(totalWrites), "writes")
		})
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

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			localWrites := 0
			for i := 0; i < opsPerGoroutine; i++ {
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
					btree.root.Update(ctx, newRoot, uint64(localWrites))
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
	for i := 0; i < numKeys; i++ {
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
		btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	var mu sync.Mutex
	totalReads := int64(0)
	numGoroutines := 4
	opsPerGoroutine := b.N / numGoroutines

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			localReads := 0
			for i := 0; i < opsPerGoroutine; i++ {
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
	for i := 0; i < 100; i++ {
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
		btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	b.ResetTimer()
	b.ReportAllocs()

	opCount := 0
	writeRatio := 0.2
	numWrites := 0
	numReads := 0

	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(100+numWrites))
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
