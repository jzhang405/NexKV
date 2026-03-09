// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPureMemoryBTree_BasicFunctionality tests basic operations of pure memory BTree.
func TestPureMemoryBTree_BasicFunctionality(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// Get current root
	rootInfo := btree.root.Get()
	defer rootInfo.Release()

	// Verify root is a pure Node pointer
	assert.NotNil(t, rootInfo.Root)
	assert.True(t, rootInfo.Root.IsLeaf)
}

// TestPureMemoryBTree_FindPath tests FindPath with pure memory nodes.
func TestPureMemoryBTree_FindPath(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// Add some data to root
	rootInfo := btree.root.Get()
	defer rootInfo.Release()

	// Insert into root node
	err = rootInfo.Root.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	err = rootInfo.Root.Insert([]byte("key2"), []byte("value2"))
	require.NoError(t, err)

	// Test FindPath
	path, err := btree.FindPath([]byte("key1"))
	require.NoError(t, err)
	require.NotNil(t, path)
	assert.Equal(t, 1, len(path))
	assert.Equal(t, rootInfo.Root, path[0].Node)

	// Verify value
	value, err := path[0].Node.Get([]byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)
}

// TestPureMemoryBTree_CopyPathBottomUp tests CCOW path copying.
// This should be MUCH faster than the old Page.Data copying approach.
func TestPureMemoryBTree_CopyPathBottomUp(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// Create a simple path: root -> leaf
	root := NewNode(true) // Leaf node as root
	root.Insert([]byte("key1"), []byte("value1"))
	root.Insert([]byte("key2"), []byte("value2"))

	path := make(Path, 0, 1)
	path = append(path, &PathNode{
		Node:  root,
		Level: 0,
	})

	// Test CopyPathBottomUp
	modifyFunc := func(node *Node) error {
		return node.Insert([]byte("key3"), []byte("value3"))
	}

	newRoot, err := btree.CopyPathBottomUp(context.Background(), path, modifyFunc)
	require.NoError(t, err)
	require.NotNil(t, newRoot)

	// Verify old root is unchanged
	oldValue, _ := root.Get([]byte("key3"))
	assert.Nil(t, oldValue)

	// Verify new root has the modification
	newValue, err := newRoot.Get([]byte("key3"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value3"), newValue)

	// Verify original keys still exist
	oldValue1, err := newRoot.Get([]byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), oldValue1)
}

// BenchmarkPureMemory_FindPath benchmarks FindPath operation.
func BenchmarkPureMemory_FindPath(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate root
	rootInfo := btree.root.Get()
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 100)}
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = path
	}
}

// BenchmarkPureMemory_CopyPathBottomUp benchmarks CCOW path copying.
// This should be ~9.5x faster than the old Page.Data copying approach.
func BenchmarkPureMemory_CopyPathBottomUp(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	root := NewNode(true)
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		root.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := Path{
			&PathNode{Node: root, Level: 0},
		}

		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("new"), []byte("value"))
		}

		newRoot, err := btree.CopyPathBottomUp(context.Background(), path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}
		_ = newRoot
	}
}

// BenchmarkNode_Clone benchmarks Node.Clone() operation.
// This replaces the 4075-byte Page.Data copy with a shallow slice copy.
func BenchmarkNode_Clone(b *testing.B) {
	node := NewNode(true)
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := node.Clone()
		_ = cloned
	}
}
