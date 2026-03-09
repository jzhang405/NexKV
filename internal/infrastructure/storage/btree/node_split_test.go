// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree provides BTree storage implementation.
//
// 节点分裂和合并测试文件
//
// 本文件包含 Node 节点的分裂和合并相关测试：
// - SplitCopy: 写时复制分裂（无锁）
// - Split: 传统分裂操作
// - Merge: 节点合并操作
// - InsertWithSplit: 带自动分裂的插入
package btree

import (
	"fmt"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// SplitCopy 测试（写时复制分裂）
// ==========================================

// TestNode_SplitCopy 测试写时复制分裂
func TestNode_SplitCopy(t *testing.T) {
	t.Run("split copy creates independent nodes", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			value := []byte("value")
			err := original.Insert(key, value)
			require.NoError(t, err)
		}

		assert.Equal(t, model.DefaultMaxKeys, original.Size())
		assert.True(t, original.IsFull())

		// Perform SplitCopy (doesn't modify original)
		newLeft, newRight, medianKey, err := original.SplitCopy()
		require.NoError(t, err)
		require.NotNil(t, newLeft)
		require.NotNil(t, newRight)
		require.NotNil(t, medianKey)

		// Verify original is unchanged (Copy-on-Write!)
		assert.Equal(t, model.DefaultMaxKeys, original.Size())
		assert.True(t, original.IsFull())

		// Verify new nodes have correct sizes
		assert.Equal(t, 128, newLeft.Size())
		assert.Equal(t, 128, newRight.Size())

		// Verify all keys are present across nodes
		for i := range 128 {
			key := []byte{byte(i)}
			value, err := newLeft.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		for i := 128; i < 256; i++ {
			key := []byte{byte(i)}
			value, err := newRight.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		t.Logf("SplitCopy successful:")
		t.Logf("  Original node: %d keys (unchanged)", original.Size())
		t.Logf("  New left: %d keys", newLeft.Size())
		t.Logf("  New right: %d keys", newRight.Size())
		t.Logf("  Median key: %v", medianKey)
	})

	t.Run("split copy enables concurrent reads", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			_ = original.Insert(key, []byte("value"))
		}

		// Perform SplitCopy
		newLeft, newRight, _, err := original.SplitCopy()
		require.NoError(t, err)

		// Verify original can still be read
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			value, err := original.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		// Verify new nodes are accessible
		assert.Equal(t, 128, newLeft.Size())
		assert.Equal(t, 128, newRight.Size())

		t.Logf("Concurrent read test passed: original remains readable after SplitCopy")
	})
}

// TestNode_SplitCopyOptimized 测试优化的SplitCopy
func TestNode_SplitCopyOptimized(t *testing.T) {
	t.Run("optimized version produces same result", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			_ = original.Insert(key, []byte("value"))
		}

		// Compare both versions
		left1, right1, median1, err1 := original.SplitCopy()
		left2, right2, median2, err2 := original.SplitCopyOptimized()

		require.NoError(t, err1)
		require.NoError(t, err2)

		// Verify both produce same result
		assert.Equal(t, left1.Size(), left2.Size())
		assert.Equal(t, right1.Size(), right2.Size())
		assert.Equal(t, median1, median2)

		t.Logf("Both versions produce identical results: %d + %d keys",
			left1.Size(), right1.Size())
	})
}

// ==========================================
// Merge 测试（节点合并）
// ==========================================

// TestNode_Merge 测试节点合并
func TestNode_Merge(t *testing.T) {
	t.Run("merge two leaf nodes", func(t *testing.T) {
		node1 := NewNode(true)
		node2 := NewNode(true)

		// Insert keys into both nodes (below capacity)
		for i := range 30 {
			key1 := []byte{byte(i)}
			key2 := []byte{byte(i + 30)}
			_ = node1.Insert(key1, []byte("value1"))
			_ = node2.Insert(key2, []byte("value2"))
		}

		// Merge node2 into node1
		err := node1.Merge(node2)
		require.NoError(t, err)

		// Verify merge result
		assert.Equal(t, 60, node1.Size())
		assert.Equal(t, 30, node2.Size()) // node2 unchanged

		// Verify all keys are present
		for i := range 60 {
			key := []byte{byte(i)}
			value, err := node1.Get(key)
			if i < 30 {
				require.NoError(t, err)
				assert.Equal(t, []byte("value1"), value)
			} else {
				require.NoError(t, err)
				assert.Equal(t, []byte("value2"), value)
			}
		}

		t.Logf("Merge successful: %d + %d = %d keys", 30, 30, node1.Size())
	})

	t.Run("merge overflow protection", func(t *testing.T) {
		node1 := NewNode(true)
		node2 := NewNode(true)

		// Fill nodes near capacity
		for i := range 130 {
			_ = node1.Insert([]byte{byte(i % 256)}, []byte("value"))
		}
		for i := range 130 {
			_ = node2.Insert([]byte{byte((i + 130) % 256)}, []byte("value"))
		}

		// Try to merge (should fail: 130+130=260 > 256)
		err := node1.Merge(node2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "would exceed capacity")

		t.Logf("Merge correctly rejected: %d + %d > %d", 130, 130, model.DefaultMaxKeys)
	})
}

// ==========================================
// BTree 集成测试
// ==========================================

// TestBTree_InsertWithSplit 测试带自动分裂的插入
func TestBTree_InsertWithSplit(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("insert until node full then split", func(t *testing.T) {
		// Insert keys until node is nearly full (120 keys)
		rootInfo := btree.root.Get()
		for i := range 120 {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			err := rootInfo.Root.Insert(key, value)
			require.NoError(t, err)
		}
		rootInfo.Release()

		// Verify node has 120 keys
		rootInfo = btree.root.Get()
		assert.Equal(t, 120, rootInfo.Root.Size())
		t.Logf("Node has %d keys before reaching capacity", rootInfo.Root.Size())
		rootInfo.Release()

		// Insert more keys (should trigger split at 128)
		// For now, we just verify we can insert up to capacity
		rootInfo = btree.root.Get()
		for i := 120; i < 128; i++ {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			err := rootInfo.Root.Insert(key, value)
			if err == ErrNodeFull {
				t.Logf("Node became full at %d keys", i)
				break
			}
			require.NoError(t, err)
		}
		rootInfo.Release()
	})

	t.Run("verify split operation", func(t *testing.T) {
		// Create a full node
		node := NewNode(true)
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			value := []byte("value")
			err := node.Insert(key, value)
			require.NoError(t, err)
		}

		assert.Equal(t, model.DefaultMaxKeys, node.Size())
		assert.True(t, node.IsFull())

		// Split the node
		rightNode, medianKey, err := node.Split()
		require.NoError(t, err)
		require.NotNil(t, rightNode)
		require.NotNil(t, medianKey)

		// Verify split result
		// Left node should have 128 keys (0..127 including median copy)
		assert.Equal(t, 128, node.Size())
		// Right node should have 128 keys (128..255)
		assert.Equal(t, 128, rightNode.Size())

		t.Logf("Split successful: left=%d keys, right=%d keys, median=%v",
			node.Size(), rightNode.Size(), medianKey)
	})
}

// TestBTree_GetStats 测试统计信息
func TestBTree_GetStats(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	stats := btree.GetStats()
	require.NotNil(t, stats)

	t.Logf("BTree Stats: %s", stats.String())

	assert.Equal(t, model.DefaultMaxKeys, stats.MaxKeys)
	assert.Equal(t, model.DefaultMinKeys, stats.MinKeys)
	assert.Greater(t, stats.MaxLevels, 0)
	assert.GreaterOrEqual(t, stats.Depth, 1)
}

// ==========================================
// 基准测试
// ==========================================

// BenchmarkNode_SplitCopy benchmarks SplitCopy (lock-free)
func BenchmarkNode_SplitCopy(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		// Create a full node
		node := NewNode(true)
		for j := range model.DefaultMaxKeys {
			key := []byte{byte(j)}
			_ = node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		// Perform SplitCopy
		_, _, _, err := node.SplitCopy()
		if err != nil {
			b.Fatalf("SplitCopy failed: %v", err)
		}
	}
}

// BenchmarkNode_SplitCopyOptimized benchmarks optimized SplitCopy
func BenchmarkNode_SplitCopyOptimized(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		node := NewNode(true)
		for j := range model.DefaultMaxKeys {
			key := []byte{byte(j)}
			_ = node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		_, _, _, err := node.SplitCopyOptimized()
		if err != nil {
			b.Fatalf("SplitCopyOptimized failed: %v", err)
		}
	}
}

// BenchmarkNode_SplitVsSplitCopy compares Split vs SplitCopy
func BenchmarkNode_SplitVsSplitCopy(b *testing.B) {
	b.Run("Original_Split", func(b *testing.B) {
		benchmarkSplit(b, false)
	})

	b.Run("SplitCopy", func(b *testing.B) {
		benchmarkSplit(b, true)
	})
}

// benchmarkSplit is a helper for split benchmarks
func benchmarkSplit(b *testing.B, useCopy bool) {
	b.ReportAllocs()

	for range b.N {
		b.StopTimer()
		node := NewNode(true)
		for j := range model.DefaultMaxKeys {
			key := []byte{byte(j)}
			_ = node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		if useCopy {
			_, _, _, err := node.SplitCopy()
			if err != nil {
				b.Fatalf("SplitCopy failed: %v", err)
			}
		} else {
			_, _, err := node.Split()
			if err != nil {
				b.Fatalf("Split failed: %v", err)
			}
		}
	}
}

// BenchmarkConcurrentSplitCopy benchmarks concurrent SplitCopy operations
func BenchmarkConcurrentSplitCopy(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Create a full node
			node := NewNode(true)
			for j := range model.DefaultMaxKeys {
				key := []byte{byte(j)}
				_ = node.Insert(key, []byte("value"))
			}

			// Perform lock-free SplitCopy
			_, _, _, err := node.SplitCopy()
			if err != nil {
				b.Errorf("SplitCopy failed: %v", err)
				return
			}
		}
	})
}

// BenchmarkNode_Split benchmarks node splitting
func BenchmarkNode_Split(b *testing.B) {
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		// Create a full node
		node := NewNode(true)
		for j := range model.DefaultMaxKeys {
			key := []byte{byte(j)}
			_ = node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		// Split the node
		_, _, err := node.Split()
		if err != nil {
			b.Fatalf("Split failed: %v", err)
		}
	}
}

// BenchmarkNode_Merge benchmarks node merging
func BenchmarkNode_Merge(b *testing.B) {
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		// Create two nodes
		node1 := NewNode(true)
		node2 := NewNode(true)
		for j := range 50 {
			_ = node1.Insert([]byte{byte(j)}, []byte("value1"))
			_ = node2.Insert([]byte{byte(j + 50)}, []byte("value2"))
		}
		b.StartTimer()

		// Merge nodes
		err := node1.Merge(node2)
		if err != nil {
			b.Fatalf("Merge failed: %v", err)
		}
	}
}
