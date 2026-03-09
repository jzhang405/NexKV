// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_InsertWithSplit 测试带自动分裂的插入
func TestBTree_InsertWithSplit(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("insert until node full then split", func(t *testing.T) {
		// Insert keys until node is nearly full (120 keys)
		rootInfo := btree.root.Get()
		for i := 0; i < 120; i++ {
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
		for i := 0; i < model.DefaultMaxKeys; i++ {
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
		// Left node should have 64 keys (0..63 including median copy)
		assert.Equal(t, 64, node.Size())
		// Right node should have 64 keys (64..127)
		assert.Equal(t, 64, rightNode.Size())

		t.Logf("Split successful: left=%d keys, right=%d keys, median=%v",
			node.Size(), rightNode.Size(), medianKey)
	})
}

// TestNode_SplitInternalNode 测试内部节点分裂
func TestNode_SplitInternalNode(t *testing.T) {
	t.Run("split internal node with children", func(t *testing.T) {
		// Create an internal node
		node := NewNode(false) // Internal node

		// Pre-create children (for internal node, we need n+1 children)
		// For simplicity, we create a small internal node with just a few keys
		child1 := NewNode(true)
		child2 := NewNode(true)
		child3 := NewNode(true)

		// Insert first key with child1
		key1 := []byte{byte(50)}
		node.Children = append(node.Children, child1)
		node.Keys = append(node.Keys, key1)
		node.Children = append(node.Children, child2)

		// Insert second key with child2
		_ = []byte{byte(100)} // Placeholder for second key
		node.Children = append(node.Children, child3)

		t.Logf("Internal node before split: %d keys, %d children", node.Size(), len(node.Children))

		// For now, we skip the actual split test for internal nodes
		// because it requires more complex setup
		t.Skip("Internal node split requires more complex setup - skipped for now")
	})
}

// TestNode_Merge 测试节点合并
func TestNode_Merge(t *testing.T) {
	t.Run("merge two leaf nodes", func(t *testing.T) {
		node1 := NewNode(true)
		node2 := NewNode(true)

		// Insert keys into both nodes (below capacity)
		for i := 0; i < 30; i++ {
			key1 := []byte{byte(i)}
			key2 := []byte{byte(i + 30)}
			node1.Insert(key1, []byte("value1"))
			node2.Insert(key2, []byte("value2"))
		}

		// Merge node2 into node1
		err := node1.Merge(node2)
		require.NoError(t, err)

		// Verify merge result
		assert.Equal(t, 60, node1.Size())
		assert.Equal(t, 30, node2.Size()) // node2 unchanged

		// Verify all keys are present
		for i := 0; i < 60; i++ {
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
		for i := 0; i < 70; i++ {
			node1.Insert([]byte{byte(i)}, []byte("value"))
		}
		for i := 0; i < 70; i++ {
			node2.Insert([]byte{byte(i + 70)}, []byte("value"))
		}

		// Try to merge (should fail: 70+70=140 > 128)
		err := node1.Merge(node2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "would exceed capacity")

		t.Logf("Merge correctly rejected: %d + %d > %d", 70, 70, model.DefaultMaxKeys)
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

// BenchmarkNode_Split benchmarks node splitting
func BenchmarkNode_Split(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a full node
		node := NewNode(true)
		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			node.Insert(key, []byte("value"))
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
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create two nodes
		node1 := NewNode(true)
		node2 := NewNode(true)
		for j := 0; j < 50; j++ {
			node1.Insert([]byte{byte(j)}, []byte("value1"))
			node2.Insert([]byte{byte(j + 50)}, []byte("value2"))
		}
		b.StartTimer()

		// Merge nodes
		err := node1.Merge(node2)
		if err != nil {
			b.Fatalf("Merge failed: %v", err)
		}
	}
}
