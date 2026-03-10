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

// TestPathNode verifies path node structure.
func TestPathNode(t *testing.T) {
	t.Run("create path node", func(t *testing.T) {
		node := NewNode(true)
		pathNode := &PathNode{
			Node:  node,
			Level: 0,
		}

		assert.NotNil(t, pathNode.Node)
		assert.Equal(t, 0, pathNode.Level)
		assert.True(t, pathNode.Node.IsLeaf)
	})
}

// TestPath verifies path structure.
func TestPath(t *testing.T) {
	t.Run("create empty path", func(t *testing.T) {
		path := make(Path, 0)
		assert.NotNil(t, path)
		assert.Equal(t, 0, len(path))
	})

	t.Run("create path with nodes", func(t *testing.T) {
		path := make(Path, 3)
		for i := range 3 {
			node := NewNode(i == 2) // Last node is leaf
			path[i] = &PathNode{
				Node:  node,
				Level: i,
			}
		}

		assert.Equal(t, 3, len(path))
		assert.Equal(t, 0, path[0].Level)
		assert.Equal(t, 2, path[2].Level)
	})
}

// TestAcquireReleasePath verifies path pool operations.
func TestAcquireReleasePath(t *testing.T) {
	t.Run("acquire path from pool", func(t *testing.T) {
		path := AcquirePath()
		assert.NotNil(t, path)
		ReleasePath(path)
	})

	t.Run("acquire and reuse path", func(t *testing.T) {
		// First acquisition
		path1 := AcquirePath()
		path1 = append(path1, &PathNode{Node: NewNode(true), Level: 0})
		ReleasePath(path1)

		// Second acquisition (may reuse the same path)
		path2 := AcquirePath()
		// Path should be reset to zero length
		assert.Equal(t, 0, len(path2))
		ReleasePath(path2)
	})
}

// TestBTree_FindPath verifies path finding functionality.
func TestBTree_FindPath(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("find path for key in empty tree", func(t *testing.T) {
		key := []byte("test-key")
		path, err := btree.FindPath(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		// Path should have at least one node (the leaf/root)
		assert.Greater(t, len(path), 0)

		// Last node should be leaf
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)
	})

	t.Run("find path for key in populated tree", func(t *testing.T) {
		// Insert some keys
		rootInfo := btree.root.Get()
		for i := range 10 {
			key := []byte{byte(i)}
			value := []byte("value")
			_ = rootInfo.Root.Insert(key, value)
		}
		rootInfo.Release()

		// Find path
		key := []byte{5}
		path, err := btree.FindPath(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		assert.Greater(t, len(path), 0)
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)
	})
}

// TestBTree_CopyPathBottomUp verifies CCOW path copying.
func TestBTree_CopyPathBottomUp(t *testing.T) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("copy and modify single node path", func(t *testing.T) {
		rootInfo := btree.root.Get()

		// Create path with single node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		// Modify function: insert a key-value pair
		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("new-key"), []byte("new-value"))
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		require.NoError(t, err)
		require.NotNil(t, newRoot)

		// Verify new root has the new key
		value, err := newRoot.Get([]byte("new-key"))
		require.NoError(t, err)
		assert.Equal(t, []byte("new-value"), value)

		// Verify old root is unchanged
		_, err = rootInfo.Root.Get([]byte("new-key"))
		assert.Error(t, err)

		rootInfo.Release()
	})

	t.Run("copy empty path returns error", func(t *testing.T) {
		path := make(Path, 0)
		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("key"), []byte("value"))
		}

		_, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		assert.Error(t, err)
	})
}

// TestBTree_CopyPathBottomUpBatch verifies batch CCOW operations.
func TestBTree_CopyPathBottomUpBatch(t *testing.T) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("batch insert multiple keys", func(t *testing.T) {
		rootInfo := btree.root.Get()

		// Create path with single node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		// Batch insert function
		batchFunc := func(node *Node) error {
			keys := make([][]byte, 5)
			values := make([][]byte, 5)
			for i := range 5 {
				keys[i] = []byte{byte(i)}
				values[i] = []byte("value")
			}
			return node.BatchInsert(keys, values)
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		require.NoError(t, err)
		require.NotNil(t, newRoot)

		// Verify all keys were inserted
		for i := range 5 {
			key := []byte{byte(i)}
			value, err := newRoot.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		rootInfo.Release()
	})

	t.Run("batch insert with empty key list", func(t *testing.T) {
		rootInfo := btree.root.Get()

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		// Empty batch
		batchFunc := func(node *Node) error {
			return node.BatchInsert([][]byte{}, [][]byte{})
		}

		newRoot, err := btree.CopyPathBottomUpBatch(ctx, path, batchFunc)
		require.NoError(t, err)
		require.NotNil(t, newRoot)

		rootInfo.Release()
	})
}

// TestNodeCloningInCCOW verifies that CCOW creates independent copies.
func TestNodeCloningInCCOW(t *testing.T) {
	t.Run("clone creates independent node", func(t *testing.T) {
		original := NewNode(true)
		_ = original.Insert([]byte("key1"), []byte("value1"))
		_ = original.Insert([]byte("key2"), []byte("value2"))

		cloned := original.Clone()

		// Modify clone
		_ = cloned.Insert([]byte("key3"), []byte("value3"))

		// Verify original is unchanged
		assert.Equal(t, 2, original.Size())
		_, err := original.Get([]byte("key3"))
		assert.Error(t, err)

		// Verify clone has new key
		assert.Equal(t, 3, cloned.Size())
		value, err := cloned.Get([]byte("key3"))
		require.NoError(t, err)
		assert.Equal(t, []byte("value3"), value)
	})

	t.Run("clone preserves all data", func(t *testing.T) {
		original := NewNode(true)
		for i := range 10 {
			key := []byte{byte(i)}
			value := []byte("value")
			_ = original.Insert(key, value)
		}

		cloned := original.Clone()

		// Verify all keys are present
		assert.Equal(t, original.Size(), cloned.Size())
		for i := range 10 {
			key := []byte{byte(i)}
			value1, err1 := original.Get(key)
			value2, err2 := cloned.Get(key)
			require.NoError(t, err1)
			require.NoError(t, err2)
			assert.Equal(t, value1, value2)
		}
	})
}

// TestBTree_FindPathPageID verifies PageID-based path finding with lazy loading.
func TestBTree_FindPathPageID(t *testing.T) {
	t.Run("memory mode fallback", func(t *testing.T) {
		// Memory-only mode (no PageManager)
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		// Insert some keys
		rootInfo := btree.root.Get()
		for i := range 10 {
			key := []byte{byte(i)}
			value := []byte("value")
			_ = rootInfo.Root.Insert(key, value)
		}
		rootInfo.Release()

		// FindPathPageID should fall back to direct pointer mode
		key := []byte{5}
		path, err := btree.FindPathPageID(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		// Path should have at least one node
		assert.Greater(t, len(path), 0)

		// Last node should be leaf
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)

		ReleasePath(path)
	})

	t.Run("find path in empty tree", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		key := []byte("test-key")
		path, err := btree.FindPathPageID(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		// Path should have at least one node (the leaf/root)
		assert.Greater(t, len(path), 0)

		// Last node should be leaf
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)

		ReleasePath(path)
	})

	t.Run("path levels are correct", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		// Insert enough keys to potentially create internal nodes
		rootInfo := btree.root.Get()
		for i := range 100 {
			key := []byte{byte(i)}
			value := []byte("value")
			_ = rootInfo.Root.Insert(key, value)
		}
		rootInfo.Release()

		key := []byte{50}
		path, err := btree.FindPathPageID(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		// Verify path levels decrease from root to leaf
		for i := 0; i < len(path)-1; i++ {
			assert.Greater(t, path[i].Level, path[i+1].Level,
				"Path levels should decrease from root to leaf")
		}

		// Last node should be leaf
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)

		ReleasePath(path)
	})

	t.Run("compare FindPath and FindPathPageID results", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		// Insert keys
		rootInfo := btree.root.Get()
		for i := range 50 {
			key := []byte{byte(i)}
			value := []byte("value")
			_ = rootInfo.Root.Insert(key, value)
		}
		rootInfo.Release()

		// Test multiple keys
		testKeys := []byte{10, 25, 40}
		for _, keyByte := range testKeys {
			key := []byte{keyByte}

			// Find path using direct pointer method
			path1, err1 := btree.FindPath(key)
			require.NoError(t, err1)
			defer ReleasePath(path1)

			// Find path using PageID method
			path2, err2 := btree.FindPathPageID(key)
			require.NoError(t, err2)
			defer ReleasePath(path2)

			// Both should return the same path length
			assert.Equal(t, len(path1), len(path2),
				"Path lengths should match for key %d", keyByte)

			// Both should end at leaf nodes
			assert.True(t, path1[len(path1)-1].Node.IsLeaf)
			assert.True(t, path2[len(path2)-1].Node.IsLeaf)
		}
	})
}

// TestFindPathPageID_NotFound verifies handling of non-existent keys.
func TestFindPathPageID_NotFound(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// Insert some keys
	rootInfo := btree.root.Get()
	for i := range 10 {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Try to find a key that doesn't exist
	// Path finding should still succeed, just return the path to where it would be
	key := []byte{255} // Way outside the range
	path, err := btree.FindPathPageID(key)
	require.NoError(t, err)
	require.NotNil(t, path)

	// Should still have a valid path
	assert.Greater(t, len(path), 0)

	ReleasePath(path)
}

// TestFindPathPageID_Concurrent verifies concurrent path finding.
func TestFindPathPageID_Concurrent(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// Insert keys
	rootInfo := btree.root.Get()
	for i := range 100 {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Concurrent path finding
	const numGoroutines = 10
	const numOpsPerGoroutine = 100

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < numOpsPerGoroutine; j++ {
				key := []byte{byte(j % 100)}
				path, err := btree.FindPathPageID(key)
				if err == nil {
					// Verify path is valid
					if len(path) > 0 {
						lastNode := path[len(path)-1]
						if !lastNode.Node.IsLeaf {
							t.Errorf("Expected leaf node at end of path")
						}
					}
					ReleasePath(path)
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}
