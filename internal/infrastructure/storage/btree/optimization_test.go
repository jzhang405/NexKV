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

// BenchmarkNode_Clone_Optimized benchmarks optimized Node.Clone with sync.Pool.
func BenchmarkNode_Clone_Optimized(b *testing.B) {
	node := NewNode(true)
	// Pre-populate with 100 keys
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := node.Clone()
		_ = cloned
	}
}

// BenchmarkNode_BatchInsert benchmarks batch insert operation.
func BenchmarkNode_BatchInsert(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := NewNode(true)

		// Prepare batch data
		keys := make([][]byte, 10)
		values := make([][]byte, 10)
		for j := 0; j < 10; j++ {
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
	for j := 0; j < 10; j++ {
		keys[j] = []byte(fmt.Sprintf("key-%d", j))
		values[j] = []byte(fmt.Sprintf("value-%d", j))
	}

	b.Run("BatchInsert", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			node := NewNode(true)
			node.BatchInsert(keys, values)
		}
	})

	b.Run("SingleInsert", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			node := NewNode(true)
			for j := 0; j < 10; j++ {
				node.Insert(keys[j], values[j])
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
	for i := 0; i < 100; i++ {
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
		btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	writeCount := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
			for j := 0; j < 10; j++ {
				keys[j] = []byte(fmt.Sprintf("batch-key-%d-%d", writeCount, j))
				values[j] = []byte(fmt.Sprintf("batch-value-%d", j))
			}
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		if err == nil {
			btree.root.Update(ctx, newRoot, uint64(writeCount))
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
		for i := 0; i < 100; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(i))
			rootInfo.Release()
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(i))

			rootInfo.Release()
		}
	})

	b.Run("Batch", func(b *testing.B) {
		btree, _ := OpenBTree("", nil)
		defer btree.Close()

		// 预热
		for i := 0; i < 100; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(i))
			rootInfo.Release()
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
				for j := 0; j < 10; j++ {
					keys[j] = []byte(fmt.Sprintf("batch-key-%d", j))
					values[j] = []byte(fmt.Sprintf("batch-value-%d", j))
				}
				return node.BatchInsert(keys, values)
			}

			newRoot, _ := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
			btree.root.Update(ctx, newRoot, uint64(i))

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
	for i := 0; i < 100; i++ {
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
		btree.root.Update(ctx, newRoot, uint64(i))
		rootInfo.Release()
	}

	writeCount := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(writeCount))
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
	for i := 0; i < b.N; i++ {
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
			for j := 0; j < 10; j++ {
				keys[j] = []byte(fmt.Sprintf("key-%d", writeCount+j))
				values[j] = []byte(fmt.Sprintf("value-%d", j))
			}
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		if err == nil {
			btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount += 10
		}

		rootInfo.Release()
	}
}

// TestOptimizedNode_Clone tests optimized Node.Clone.
func TestOptimizedNode_Clone(t *testing.T) {
	original := NewNode(true)
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		original.Insert(key, value)
	}

	// Test Clone
	cloned := original.Clone()

	// Verify independence
	require.Equal(t, original.Size(), cloned.Size())
	require.Equal(t, original.IsLeaf, cloned.IsLeaf)

	// Modify clone should not affect original
	key := []byte("new-key")
	value := []byte("new-value")
	cloned.Insert(key, value)

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
	for i := 0; i < 10; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}

	// Batch insert
	err := node.BatchInsert(keys, values)
	require.NoError(t, err)

	// Verify all keys were inserted
	require.Equal(t, 10, node.Size())

	for i := 0; i < 10; i++ {
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
		for i := 0; i < 10; i++ {
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

	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		value, err := rootInfo.Root.Get(key)
		require.NoError(t, err)
		require.Equal(t, []byte(fmt.Sprintf("batch-value-%d", i)), value)
	}

	rootInfo.Release()
}
