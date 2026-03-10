// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSerializeNode_WithChildIDs tests serialization of internal node with ChildIDs.
func TestSerializeNode_WithChildIDs(t *testing.T) {
	// Create internal node with ChildIDs
	node := NewNode(false)
	node.PageID = 1
	node.Keys = [][]byte{[]byte("separator")}
	node.Children = []*Node{NewNode(true), NewNode(true)}
	node.ChildIDs = []model.PageID{10, 20}

	// Serialize to page
	page, err := PageFromNode(1, node)
	require.NoError(t, err)
	require.NotNil(t, page)

	// Verify page is created
	t.Logf("Page created: ID=%d, Type=%d, DataLen=%d", page.ID, page.Type, len(page.Data))
	assert.Equal(t, model.PageID(1), page.ID, "Page ID should be 1")
	assert.NotEmpty(t, page.Data)

	// Deserialize back
	deserialized, err := DeserializeNode(page)
	require.NoError(t, err)
	require.NotNil(t, deserialized)

	// Verify basic properties
	// Note: PageID is not preserved during deserialization (it's Page metadata, not Node data)
	assert.Equal(t, node.IsLeaf, deserialized.IsLeaf)
	assert.Equal(t, len(node.Keys), len(deserialized.Keys))

	// Verify ChildIDs are preserved
	assert.Equal(t, node.ChildIDs, deserialized.ChildIDs,
		"ChildIDs should be preserved during serialization")
}

// TestSerializeNode_FullInternalNode tests serialization of full internal node.
func TestSerializeNode_FullInternalNode(t *testing.T) {
	// Create a full internal node
	node := NewNode(false)
	node.PageID = 1

	for i := 0; i < model.DefaultMaxKeys; i++ {
		key := []byte{byte(i)}
		node.Keys = append(node.Keys, key)
	}

	for i := 0; i < model.DefaultMaxKeys+1; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(100 + i)
		node.Children = append(node.Children, child)
		node.ChildIDs = append(node.ChildIDs, child.PageID)
	}

	// Serialize
	page, err := PageFromNode(1, node)
	require.NoError(t, err)

	// Deserialize
	deserialized, err := DeserializeNode(page)
	require.NoError(t, err)

	// Verify (Note: PageID is not preserved during deserialization)
	assert.Equal(t, len(node.Keys), len(deserialized.Keys))
	assert.Equal(t, len(node.ChildIDs), len(deserialized.ChildIDs),
		"ChildIDs length should be preserved")

	// Verify ChildIDs values match
	for i, childID := range node.ChildIDs {
		assert.Equal(t, childID, deserialized.ChildIDs[i],
			"ChildIDs[%d] should match", i)
	}
}

// TestDeserializeNode_ValidatesChildIDs tests that deserialized nodes maintain consistency.
func TestDeserializeNode_ValidatesChildIDs(t *testing.T) {
	// Create and serialize an internal node
	original := NewNode(false)
	original.PageID = 42
	original.Keys = [][]byte{[]byte("key1"), []byte("key2")}
	original.Children = []*Node{NewNode(true), NewNode(true), NewNode(true)}
	original.ChildIDs = []model.PageID{10, 20, 30}

	page, err := PageFromNode(42, original)
	require.NoError(t, err)

	// Deserialize
	deserialized, err := DeserializeNode(page)
	require.NoError(t, err)

	// After deserialization, Children slice is empty (lazy loading)
	// but ChildIDs should be preserved
	assert.Equal(t, 0, len(deserialized.Children), "Children should be empty after deserialization")
	assert.Equal(t, len(original.ChildIDs), len(deserialized.ChildIDs), "ChildIDs should be preserved")
	assert.Equal(t, original.ChildIDs, deserialized.ChildIDs, "ChildIDs values should match")
}

// TestBTree_Integration_ChildIDs tests complete data flow with ChildIDs.
func TestBTree_Integration_ChildIDs(t *testing.T) {
	t.Run("insert-find-validate", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		// Insert data
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			key := []byte{byte(i)}
			value := []byte{byte(i)}
			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
					return node.Insert(key, value)
				})
				if newRoot != nil {
					rootInfo := btree.root.Get()
					rootInfo.Root = newRoot
					rootInfo.Release()
				}
			}
			ReleasePath(path)
		}

		// Find and validate
		key := []byte{5}
		path, err := btree.FindPath(key)
		require.NoError(t, err)
		assert.NotEmpty(t, path, "Path should not be empty")

		// Verify all nodes in path have valid structure
		for _, pn := range path {
			assert.NotNil(t, pn.Node, "Path node should not be nil")
			// In memory mode, PageID may be 0 (unassigned), that's OK
			assert.NoError(t, pn.Node.ValidateChildConsistency(),
				"Nodes should be internally consistent")
		}
	})

	t.Run("insertChild-maintains-consistency", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		rootInfo := btree.root.Get()
		root := rootInfo.Root

		// Insert enough data to cause splits
		for i := 0; i < 300; i++ {
			key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = root.Insert(key, value)
		}

		// Verify consistency after all inserts
		err = root.ValidateChildConsistency()
		assert.NoError(t, err, "Root node should be consistent after inserts")
		rootInfo.Release()
	})
}

// TestEdgeCase_MaxChildIDs tests node with maximum ChildIDs.
func TestEdgeCase_MaxChildIDs(t *testing.T) {
	// Create internal node with maximum children
	node := NewNode(false)
	node.PageID = 1
	node.Keys = make([][]byte, 0, model.DefaultMaxKeys)
	node.Children = make([]*Node, 0, model.DefaultMaxKeys+1)
	node.ChildIDs = make([]model.PageID, 0, model.DefaultMaxKeys+1)

	// Fill to capacity
	for i := 0; i < model.DefaultMaxKeys; i++ {
		key := []byte{byte(i)}
		node.Keys = append(node.Keys, key)
	}

	for i := 0; i < model.DefaultMaxKeys+1; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(i)
		node.Children = append(node.Children, child)
		node.ChildIDs = append(node.ChildIDs, child.PageID)
	}

	// Validate consistency
	err := node.ValidateChildConsistency()
	assert.NoError(t, err, "Full node should be consistent")
	assert.Equal(t, model.DefaultMaxKeys+1, len(node.ChildIDs),
		"Should have maximum ChildIDs")
}

// TestEdgeCase_EmptyChildIDs tests EnsureChildIDs with empty ChildIDs.
func TestEdgeCase_EmptyChildIDs(t *testing.T) {
	t.Run("internal-node-without-ChildIDs", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("key")}
		node.Children = []*Node{NewNode(true), NewNode(true)}
		// ChildIDs is empty (default)

		// EnsureChildIDs should build from Children
		node.EnsureChildIDs()

		assert.Equal(t, 2, len(node.ChildIDs), "ChildIDs should be built")
		assert.Equal(t, model.PageID(0), node.ChildIDs[0], "First child should have PageID=0")
		assert.Equal(t, model.PageID(0), node.ChildIDs[1], "Second child should have PageID=0")
	})

	t.Run("empty-internal-node", func(t *testing.T) {
		node := NewNode(false)
		// No keys, no children, no ChildIDs

		node.EnsureChildIDs()

		assert.Equal(t, 0, len(node.ChildIDs), "Empty node should have 0 ChildIDs")
	})
}

// TestEdgeCase_SingleChildID tests single child node.
func TestEdgeCase_SingleChildID(t *testing.T) {
	node := NewNode(false)
	node.PageID = 1
	node.Keys = [][]byte{}
	node.Children = []*Node{NewNode(true)}
	node.ChildIDs = []model.PageID{42}

	// Validate
	err := node.ValidateChildConsistency()
	assert.NoError(t, err)

	// EnsureChildIDs should maintain same
	node.EnsureChildIDs()
	assert.Equal(t, []model.PageID{42}, node.ChildIDs)
}

// TestErrorHandling_ChildIDsMismatch tests error when Children and ChildIDs don't match.
func TestErrorHandling_ChildIDsMismatch(t *testing.T) {
	t.Run("length-mismatch", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("key")}
		node.Children = []*Node{NewNode(true), NewNode(true)}
		node.ChildIDs = []model.PageID{10} // Wrong length

		err := node.ValidateChildConsistency()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "length mismatch")
	})

	t.Run("pageid-mismatch", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("key")}

		child1 := NewNode(true)
		child1.PageID = 10
		child2 := NewNode(true)
		child2.PageID = 20

		node.Children = []*Node{child1, child2}
		node.ChildIDs = []model.PageID{10, 99} // Wrong PageID

		err := node.ValidateChildConsistency()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "doesn't match")
	})
}

// TestErrorHandling_LeafNodeWithChildren tests leaf node validation.
func TestErrorHandling_LeafNodeWithChildren(t *testing.T) {
	node := NewNode(true)
	_ = node.Insert([]byte("key"), []byte("value"))

	// Invalid: leaf node with children
	node.Children = []*Node{NewNode(true)}

	err := node.ValidateChildConsistency()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "should not have children")
}

// TestErrorHandling_LeafNodeWithChildIDs tests leaf node with ChildIDs.
func TestErrorHandling_LeafNodeWithChildIDs(t *testing.T) {
	node := NewNode(true)
	_ = node.Insert([]byte("key"), []byte("value"))

	// Invalid: leaf node with ChildIDs
	node.ChildIDs = []model.PageID{10}

	err := node.ValidateChildConsistency()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "should not have ChildIDs")
}

// TestConcurrency_ValidateChildConsistency tests concurrent validation.
func TestConcurrency_ValidateChildConsistency(t *testing.T) {
	node := NewNode(false)
	node.PageID = 1

	// Fill with children
	for i := 0; i < 10; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(10 + i)
		_ = child.Insert([]byte{byte(i)}, []byte("value"))
		node.Children = append(node.Children, child)
		node.ChildIDs = append(node.ChildIDs, child.PageID)

		if i < 9 {
			key := []byte{byte(i)}
			node.Keys = append(node.Keys, key)
		}
	}

	// Concurrent validation
	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				_ = node.ValidateChildConsistency()
			}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	// Final validation should still pass
	err := node.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestEnsureChildIDs_RebuildsFromChildren tests EnsureChildIDs rebuilding.
func TestEnsureChildIDs_RebuildsFromChildren(t *testing.T) {
	node := NewNode(false)
	node.PageID = 1
	node.Keys = [][]byte{[]byte("key1"), []byte("key2")}

	child1 := NewNode(true)
	child1.PageID = 10
	_ = child1.Insert([]byte("a"), []byte("a"))

	child2 := NewNode(true)
	child2.PageID = 20
	_ = child2.Insert([]byte("z"), []byte("z"))

	child3 := NewNode(true)
	child3.PageID = 30
	_ = child3.Insert([]byte("m"), []byte("m"))

	node.Children = []*Node{child1, child2, child3}
	// Don't set ChildIDs

	// EnsureChildIDs should rebuild from Children
	node.EnsureChildIDs()

	expected := []model.PageID{10, 20, 30}
	assert.Equal(t, expected, node.ChildIDs)
}

// TestEnsureChildIDs_HandlesNilChildren tests EnsureChildIDs with nil children.
func TestEnsureChildIDs_HandlesNilChildren(t *testing.T) {
	node := NewNode(false)
	node.PageID = 1
	node.Keys = [][]byte{[]byte("key1"), []byte("key2")}

	child1 := NewNode(true)
	child1.PageID = 10
	node.Children = []*Node{child1, nil, NewNode(true)}

	// EnsureChildIDs should handle nil
	node.EnsureChildIDs()

	expected := []model.PageID{10, 0, 0}
	assert.Equal(t, expected, node.ChildIDs)
}
