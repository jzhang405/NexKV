// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBatchSizeOptimization 测试不同批量大小的性能
// 目标：找到最优批量大小，预期性能提升 20-30%
func TestBatchSizeOptimization(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过批量大小优化测试（使用 -short 标志）")
	}

	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 测试不同批量大小
	batchSizes := []int{5, 10, 20, 30, 50, 100}

	t.Log("=== 批量大小性能对比测试 ===")
	t.Log("注意：这只是功能测试，性能数据请运行 benchmark")
	t.Log("运行命令：go test -bench=BenchmarkBatchSize -benchmem ./...")

	for _, batchSize := range batchSizes {
		t.Run(fmt.Sprintf("BatchSize_%d", batchSize), func(t *testing.T) {
			// 重置 BTree
			rootInfo := btree.root.Get()

			// 准备批量数据（使用零填充确保字典序 = 数字序）
			keys := make([][]byte, batchSize)
			values := make([][]byte, batchSize)
			for i := range batchSize {
				keys[i] = []byte(fmt.Sprintf("batch-key-%04d", i))
				values[i] = []byte(fmt.Sprintf("batch-value-%d", i))
			}

			// 执行批量插入
			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			batchFunc := func(node *Node) error {
				return node.BatchInsert(keys, values)
			}

			newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
			require.NoError(t, err)
			require.NotNil(t, newRoot)

			// 验证所有键都插入了
			for i := range batchSize {
				value, err := newRoot.Get(keys[i])
				require.NoError(t, err)
				require.Equal(t, values[i], value)
			}

			// 更新根节点
			err = btree.root.Update(ctx, newRoot, 0)
			require.NoError(t, err)

			rootInfo.Release()

			t.Logf("批量大小 %d: 成功插入 %d 个键", batchSize, batchSize)
		})
	}
}

// BenchmarkBatchSize_5 benchmarks batch size of 5
func BenchmarkBatchSize_5(b *testing.B) {
	benchmarkBatchSize(b, 5)
}

// BenchmarkBatchSize_10 benchmarks batch size of 10
func BenchmarkBatchSize_10(b *testing.B) {
	benchmarkBatchSize(b, 10)
}

// BenchmarkBatchSize_20 benchmarks batch size of 20
func BenchmarkBatchSize_20(b *testing.B) {
	benchmarkBatchSize(b, 20)
}

// BenchmarkBatchSize_30 benchmarks batch size of 30
func BenchmarkBatchSize_30(b *testing.B) {
	benchmarkBatchSize(b, 30)
}

// BenchmarkBatchSize_50 benchmarks batch size of 50
func BenchmarkBatchSize_50(b *testing.B) {
	benchmarkBatchSize(b, 50)
}

// BenchmarkBatchSize_100 benchmarks batch size of 100
func BenchmarkBatchSize_100(b *testing.B) {
	benchmarkBatchSize(b, 100)
}

// benchmarkBatchSize 是批量大小基准测试的辅助函数
func benchmarkBatchSize(b *testing.B, batchSize int) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0
	maxWrites := 90 // 保持在 DefaultMaxKeys (128) 以下

	b.ResetTimer()
	for b.Loop() {
		// 定期重置以避免节点满
		if writeCount > maxWrites {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		rootInfo := btree.root.Get()

		// 准备批量数据
		keys := make([][]byte, batchSize)
		values := make([][]byte, batchSize)
		for j := range batchSize {
			keys[j] = []byte(fmt.Sprintf("key-%d-%d", writeCount, j))
			values[j] = []byte(fmt.Sprintf("value-%d", j))
		}

		// 执行批量CCOW
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		batchFunc := func(node *Node) error {
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount += batchSize
		}

		rootInfo.Release()
	}
}

// BenchmarkBatchSizePerKey benchmarks per-key performance for different batch sizes
func BenchmarkBatchSizePerKey(b *testing.B) {
	batchSizes := []int{5, 10, 20, 30, 50, 100}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", batchSize), func(b *testing.B) {
			btree, _ := OpenBTree("", nil)
			defer btree.Close()

			ctx := context.Background()
			writeCount := 0
			maxWrites := 90

			b.ResetTimer()
			for b.Loop() {
				if writeCount > maxWrites {
					btree.Close()
					btree, _ = OpenBTree("", nil)
					writeCount = 0
				}

				rootInfo := btree.root.Get()

				keys := make([][]byte, batchSize)
				values := make([][]byte, batchSize)
				for j := range batchSize {
					keys[j] = []byte(fmt.Sprintf("key-%d-%d", writeCount, j))
					values[j] = []byte(fmt.Sprintf("value-%d", j))
				}

				path := make(Path, 1)
				path[0] = &PathNode{
					Node:  rootInfo.Root,
					Level: 0,
				}

				batchFunc := func(node *Node) error {
					return node.BatchInsert(keys, values)
				}

				newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
				if err == nil {
					_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
					writeCount += batchSize
				}

				rootInfo.Release()
			}

			// 报告每个键的性能
			b.ReportMetric(float64(b.N*batchSize), "keys")
		})
	}
}

// BenchmarkSingleKeyComparison benchmarks single key insertion for comparison
func BenchmarkSingleKeyComparison(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0
	maxWrites := 90

	b.ResetTimer()
	for b.Loop() {
		if writeCount > maxWrites {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		rootInfo := btree.root.Get()

		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

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
}
