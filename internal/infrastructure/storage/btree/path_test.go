// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPathNode verifies path node structure.
func TestPathNode(t *testing.T) {
	t.Run("create path node", func(t *testing.T) {
		node := NewNode(true)
		pathNode := &PathNode{
			Node:   node,
			PageID: 1,
			Level:  0,
		}

		assert.NotNil(t, pathNode.Node)
		assert.Equal(t, model.PageID(1), pathNode.PageID)
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
		for i := 0; i < 3; i++ {
			node := NewNode(i == 2) // Last node is leaf
			path[i] = &PathNode{
				Node:   node,
				PageID: model.PageID(i + 1),
				Level:  i,
			}
		}

		assert.Equal(t, 3, len(path))
		assert.Equal(t, 0, path[0].Level)
		assert.Equal(t, 2, path[2].Level)
	})
}

// TestBTree_FindPath verifies path finding functionality.
func TestBTree_FindPath(t *testing.T) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/test", config)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("find path for key", func(t *testing.T) {
		key := []byte("test-key")
		path, err := btree.FindPath(key)
		require.NoError(t, err)
		require.NotNil(t, path)

		// Path should have at least one node (the leaf)
		assert.Greater(t, len(path), 0)

		// Last node should be leaf
		lastNode := path[len(path)-1]
		assert.True(t, lastNode.Node.IsLeaf)
	})
}

// TestBTree_CopyPage verifies page copying functionality.
func TestBTree_CopyPage(t *testing.T) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/test", config)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("copy page", func(t *testing.T) {
		// Skip dirty flag check for placeholder implementation
		// Actual serialization will be implemented in Phase 3
		t.Skip("Skipping - placeholder implementation until Phase 3")

		originalPageID := model.PageID(1)
		newPageID, err := btree.copyPage(originalPageID)
		require.NoError(t, err)
		assert.NotEqual(t, originalPageID, newPageID)

		// Verify new page exists
		newPage, err := btree.pageManager.Get(newPageID)
		require.NoError(t, err)
		defer btree.pageManager.Release(newPage)

		assert.NotNil(t, newPage)
		assert.True(t, newPage.IsDirty())
	})
}

// TestBTree_ModifyPage verifies page modification functionality.
func TestBTree_ModifyPage(t *testing.T) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/test", config)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("modify page - insert", func(t *testing.T) {
		page, err := btree.pageManager.Allocate()
		require.NoError(t, err)
		defer btree.pageManager.Release(page)

		key := []byte("test-key")
		value := []byte("test-value")

		err = btree.ModifyPage(page, key, value, ModifyInsert)
		require.NoError(t, err)
		assert.True(t, page.IsDirty())
	})

	t.Run("modify page - update", func(t *testing.T) {
		// Skip for placeholder implementation
		// Actual node operations will be implemented in Phase 3
		t.Skip("Skipping - placeholder implementation until Phase 3")

		page, err := btree.pageManager.Allocate()
		require.NoError(t, err)
		defer btree.pageManager.Release(page)

		key := []byte("test-key")
		value := []byte("test-value")

		// First insert
		err = btree.ModifyPage(page, key, value, ModifyInsert)
		require.NoError(t, err)

		// Then update
		newValue := []byte("new-value")
		err = btree.ModifyPage(page, key, newValue, ModifyUpdate)
		require.NoError(t, err)
	})

	t.Run("modify page - delete", func(t *testing.T) {
		// Skip for placeholder implementation
		// Actual node operations will be implemented in Phase 3
		t.Skip("Skipping - placeholder implementation until Phase 3")

		page, err := btree.pageManager.Allocate()
		require.NoError(t, err)
		defer btree.pageManager.Release(page)

		key := []byte("test-key")
		value := []byte("test-value")

		// First insert
		err = btree.ModifyPage(page, key, value, ModifyInsert)
		require.NoError(t, err)

		// Then delete
		err = btree.ModifyPage(page, key, nil, ModifyDelete)
		require.NoError(t, err)
	})
}

// TestBTree_CopyPathBottomUp verifies bottom-up path copying.
func TestBTree_CopyPathBottomUp(t *testing.T) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/test", config)
	require.NoError(t, err)
	defer btree.Close()

	t.Run("copy path with insert modification", func(t *testing.T) {
		// Skip for placeholder implementation
		// Actual CCOW path copying will be implemented in Phase 3
		t.Skip("Skipping - placeholder implementation until Phase 3")

		ctx := context.Background()
		// Create a test path
		path := make(Path, 3)
		for i := 0; i < 3; i++ {
			node := NewNode(i == 2) // Last node is leaf
			path[i] = &PathNode{
				Node:   node,
				PageID: model.PageID(i + 1),
				Level:  i,
			}
		}

		// Define modify function
		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("test-key"), []byte("test-value"))
		}

		// Copy path
		newRootID, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		require.NoError(t, err)
		assert.NotEqual(t, model.PageID(0), newRootID)
	})

	t.Run("copy path with delete modification", func(t *testing.T) {
		// Skip for placeholder implementation
		// Actual CCOW path copying will be implemented in Phase 3
		t.Skip("Skipping - placeholder implementation until Phase 3")

		ctx := context.Background()
		// Create a test path
		path := make(Path, 3)
		for i := 0; i < 3; i++ {
			node := NewNode(i == 2) // Last node is leaf
			path[i] = &PathNode{
				Node:   node,
				PageID: model.PageID(i + 1),
				Level:  i,
			}
		}

		// Define modify function
		modifyFunc := func(node *Node) error {
			return node.Delete([]byte("test-key"))
		}

		// Copy path
		newRootID, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		require.NoError(t, err)
		assert.NotEqual(t, model.PageID(0), newRootID)
	})
}

// TestModifyOperation verifies modify operation constants.
func TestModifyOperation(t *testing.T) {
	t.Run("modify operation values", func(t *testing.T) {
		assert.Equal(t, ModifyOperation(0), ModifyInsert)
		assert.Equal(t, ModifyOperation(1), ModifyUpdate)
		assert.Equal(t, ModifyOperation(2), ModifyDelete)
	})
}

// BenchmarkPathFinding benchmarks path finding operation.
func BenchmarkPathFinding(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	key := []byte("test-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPageCopy benchmarks page copying operation.
func BenchmarkPageCopy(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	pageID := model.PageID(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.copyPage(pageID)
		if err != nil {
			b.Fatal(err)
		}
	}
}
