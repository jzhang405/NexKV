// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNode_InsertChild_SyncsChildIDs tests that InsertChild maintains ChildIDs consistency.
func TestNode_InsertChild_SyncsChildIDs(t *testing.T) {
	// Create internal node
	parent := NewNode(false)
	assert.False(t, parent.IsLeaf)

	// Create child nodes with PageIDs
	child1 := NewNode(true)
	child1.PageID = 10
	_ = child1.Insert([]byte("a"), []byte("a_value"))

	child2 := NewNode(true)
	child2.PageID = 20
	_ = child2.Insert([]byte("z"), []byte("z_value"))

	// Insert children using InsertChild
	err := parent.InsertChild([]byte("m"), child1)
	require.NoError(t, err)

	err = parent.InsertChild([]byte("s"), child2)
	require.NoError(t, err)

	// Verify ChildIDs are synchronized
	assert.Equal(t, 3, len(parent.Children), "Should have 3 children")
	assert.Equal(t, 3, len(parent.ChildIDs), "Should have 3 ChildIDs")

	// ChildIDs should match children's PageIDs
	assert.Equal(t, model.PageID(0), parent.ChildIDs[0], "First child should be default (0)")
	assert.Equal(t, model.PageID(10), parent.ChildIDs[1], "Second child PageID should match")
	assert.Equal(t, model.PageID(20), parent.ChildIDs[2], "Third child PageID should match")

	// Verify consistency
	err = parent.ValidateChildConsistency()
	assert.NoError(t, err, "Node should be consistent")
}

// TestNode_InsertChild_MemoryNode tests InsertChild with memory-only nodes (PageID=0).
func TestNode_InsertChild_MemoryNode(t *testing.T) {
	parent := NewNode(false)

	// Create child without PageID (memory mode)
	child := NewNode(true)
	// child.PageID is 0 by default
	_ = child.Insert([]byte("key"), []byte("value"))

	err := parent.InsertChild([]byte("separator"), child)
	require.NoError(t, err)

	// Verify ChildIDs has 0 for memory nodes
	assert.Equal(t, model.PageID(0), parent.ChildIDs[1], "Memory node should have PageID=0")

	// Verify consistency
	err = parent.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_Split_SyncsChildIDs tests that Split maintains ChildIDs consistency.
func TestNode_Split_SyncsChildIDs(t *testing.T) {
	// Create a full internal node
	parent := NewNode(false)
	parent.PageID = 1

	// Add children with PageIDs
	for i := 0; i < model.DefaultMaxKeys+1; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(10 + i)
		_ = child.Insert([]byte{byte(i)}, []byte("value"))

		parent.Children = append(parent.Children, child)
		parent.ChildIDs = append(parent.ChildIDs, child.PageID)
	}

	// Add separator keys
	for i := 0; i < model.DefaultMaxKeys; i++ {
		key := []byte{byte(i * 2)}
		parent.Keys = append(parent.Keys, key)
	}

	// Split the node
	rightNode, medianKey, err := parent.Split()
	require.NoError(t, err)
	require.NotNil(t, rightNode)
	assert.NotNil(t, medianKey)

	// Verify parent node
	// For 256 keys, split is 127 (left) + 128 (right) + 1 promoted
	// So parent (left) has 127 keys, 128 children
	assert.Equal(t, (model.DefaultMaxKeys-1)/2, len(parent.Keys), "Parent keys count")
	assert.Equal(t, (model.DefaultMaxKeys-1)/2+1, len(parent.Children), "Parent children count")
	assert.Equal(t, (model.DefaultMaxKeys-1)/2+1, len(parent.ChildIDs), "Parent ChildIDs count")

	// Verify right node
	// Right node has 128 keys, 129 children
	assert.Equal(t, model.DefaultMaxKeys/2, len(rightNode.Keys), "Right node keys count")
	assert.Equal(t, model.DefaultMaxKeys/2+1, len(rightNode.Children), "Right node children count")
	assert.Equal(t, model.DefaultMaxKeys/2+1, len(rightNode.ChildIDs), "Right node ChildIDs count")

	// Verify ChildIDs are properly split
	// Parent should have first half of ChildIDs
	// Right node should have second half
	expectedParentChildIDs := (model.DefaultMaxKeys-1)/2 + 1
	expectedRightChildIDs := model.DefaultMaxKeys/2 + 1

	assert.Equal(t, expectedParentChildIDs, len(parent.ChildIDs))
	assert.Equal(t, expectedRightChildIDs, len(rightNode.ChildIDs))

	// Verify consistency
	err = parent.ValidateChildConsistency()
	assert.NoError(t, err)

	err = rightNode.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_Split_LeafNode tests that leaf node split doesn't affect ChildIDs.
func TestNode_Split_LeafNode(t *testing.T) {
	// Create a full leaf node
	leaf := NewNode(true)
	for i := 0; i < model.DefaultMaxKeys; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = leaf.Insert(key, value)
	}

	// Leaf nodes should not have ChildIDs
	assert.Equal(t, 0, len(leaf.ChildIDs))

	// Split the node
	rightNode, medianKey, err := leaf.Split()
	require.NoError(t, err)
	require.NotNil(t, rightNode)
	assert.NotNil(t, medianKey)

	// Verify ChildIDs are still empty for both nodes
	assert.Equal(t, 0, len(leaf.ChildIDs), "Left leaf should have no ChildIDs")
	assert.Equal(t, 0, len(rightNode.ChildIDs), "Right leaf should have no ChildIDs")

	// Verify consistency
	err = leaf.ValidateChildConsistency()
	assert.NoError(t, err)

	err = rightNode.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_Merge_SyncsChildIDs tests that Merge maintains ChildIDs consistency.
func TestNode_Merge_SyncsChildIDs(t *testing.T) {
	// Create two internal nodes with ChildIDs
	left := NewNode(false)
	left.PageID = 1
	left.Keys = [][]byte{[]byte("m")}
	left.Children = []*Node{NewNode(true), NewNode(true)}
	left.ChildIDs = []model.PageID{10, 20}

	right := NewNode(false)
	right.PageID = 2
	right.Keys = [][]byte{[]byte("z")}
	right.Children = []*Node{NewNode(true), NewNode(true)}
	right.ChildIDs = []model.PageID{30, 40}

	// Merge right into left
	err := left.Merge(right)
	require.NoError(t, err)

	// Verify merged node
	assert.Equal(t, 2, len(left.Keys))
	assert.Equal(t, 4, len(left.Children))
	assert.Equal(t, 4, len(left.ChildIDs))

	// Verify ChildIDs are merged
	expectedChildIDs := []model.PageID{10, 20, 30, 40}
	assert.Equal(t, expectedChildIDs, left.ChildIDs)

	// Verify consistency
	err = left.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_Merge_LeafNodes tests that leaf node merge works correctly.
func TestNode_Merge_LeafNodes(t *testing.T) {
	left := NewNode(true)
	left.PageID = 1
	_ = left.Insert([]byte("key1"), []byte("value1"))

	right := NewNode(true)
	right.PageID = 2
	_ = right.Insert([]byte("key2"), []byte("value2"))

	// Merge
	err := left.Merge(right)
	require.NoError(t, err)

	// Verify merged node
	assert.Equal(t, 2, len(left.Keys))
	assert.Equal(t, 2, len(left.Values))

	// Verify consistency (leaf nodes should have no children)
	err = left.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_ValidateChildConsistency tests consistency validation.
func TestNode_ValidateChildConsistency(t *testing.T) {
	t.Run("consistent internal node", func(t *testing.T) {
		node := NewNode(false)
		node.PageID = 1
		node.Keys = [][]byte{[]byte("m")}

		child1 := NewNode(true)
		child1.PageID = 10
		node.Children = []*Node{child1, NewNode(true)}
		node.ChildIDs = []model.PageID{10, 0}

		err := node.ValidateChildConsistency()
		assert.NoError(t, err)
	})

	t.Run("inconsistent lengths", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("m")}
		node.Children = []*Node{NewNode(true), NewNode(true)}
		node.ChildIDs = []model.PageID{10} // Wrong length

		err := node.ValidateChildConsistency()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "length mismatch")
	})

	t.Run("leaf node with children", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("key"), []byte("value"))
		// Leaf nodes shouldn't have children, but let's test the validator
		node.Children = []*Node{NewNode(true)} // Invalid

		err := node.ValidateChildConsistency()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "should not have children")
	})

	t.Run("leaf node with ChildIDs", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("key"), []byte("value"))
		node.ChildIDs = []model.PageID{10} // Invalid for leaf

		err := node.ValidateChildConsistency()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "should not have ChildIDs")
	})
}

// TestNode_EnsureChildIDs tests EnsureChildIDs helper function.
func TestNode_EnsureChildIDs(t *testing.T) {
	t.Run("builds ChildIDs from Children", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("m")}

		child1 := NewNode(true)
		child1.PageID = 10
		child2 := NewNode(true)
		child2.PageID = 20

		node.Children = []*Node{child1, child2}
		// Don't set ChildIDs

		// EnsureChildIDs should build ChildIDs from Children
		node.EnsureChildIDs()

		assert.Equal(t, []model.PageID{10, 20}, node.ChildIDs)
	})

	t.Run("handles nil children", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = [][]byte{[]byte("m")}

		// Mix of non-nil and nil children
		child1 := NewNode(true)
		child1.PageID = 10
		node.Children = []*Node{child1, nil, NewNode(true)}

		node.EnsureChildIDs()

		assert.Equal(t, []model.PageID{10, 0, 0}, node.ChildIDs)
	})
}

// TestNode_BatchInsert_SyncsChildIDs tests that BatchInsert doesn't need ChildIDs sync.
func TestNode_BatchInsert_SyncsChildIDs(t *testing.T) {
	// BatchInsert is only for leaf nodes, so it shouldn't affect ChildIDs
	node := NewNode(true)

	keys := make([][]byte, 5)
	values := make([][]byte, 5)
	for i := range 5 {
		keys[i] = []byte{byte(i)}
		values[i] = []byte("value")
	}

	err := node.BatchInsert(keys, values)
	require.NoError(t, err)

	// ChildIDs should remain empty for leaf nodes
	assert.Equal(t, 0, len(node.ChildIDs))
}

// TestBTree_InsertChild_SyncsPageID tests that BTree insert operations sync ChildIDs.
func TestBTree_InsertChild_SyncsPageID(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// This test verifies that when we perform operations that trigger InsertChild,
	// the ChildIDs are properly synchronized.

	// Note: Current implementation doesn't directly trigger InsertChild in normal flow,
	// but when we integrate with split/merge logic, ChildIDs should be maintained.

	// For now, we verify that nodes with ChildIDs can be properly validated
	rootInfo := btree.root.Get()

	// Manually create a scenario that would use InsertChild
	parent := NewNode(false)
	parent.PageID = 1

	child1 := NewNode(true)
	child1.PageID = 10
	_ = child1.Insert([]byte("a"), []byte("a"))

	child2 := NewNode(true)
	child2.PageID = 20
	_ = child2.Insert([]byte("z"), []byte("z_val"))

	// Insert children
	err = parent.InsertChild([]byte("m"), child1)
	require.NoError(t, err)
	err = parent.InsertChild([]byte("s"), child2)
	require.NoError(t, err)

	// Verify synchronization
	assert.Equal(t, 3, len(parent.ChildIDs))
	assert.Equal(t, model.PageID(10), parent.ChildIDs[1])
	assert.Equal(t, model.PageID(20), parent.ChildIDs[2])

	// Validate consistency
	err = parent.ValidateChildConsistency()
	assert.NoError(t, err)

	rootInfo.Release()
}

// TestNode_Clone_SyncsChildIDs tests that Clone properly copies ChildIDs.
func TestNode_Clone_SyncsChildIDs(t *testing.T) {
	// Create internal node with ChildIDs
	original := NewNode(false)
	original.PageID = 1
	original.Keys = [][]byte{[]byte("m")}
	original.Children = []*Node{NewNode(true), NewNode(true)}
	original.ChildIDs = []model.PageID{10, 20}

	// Clone the node
	cloned := original.Clone()

	// Verify ChildIDs are copied
	assert.Equal(t, original.PageID, cloned.PageID)
	assert.Equal(t, len(original.ChildIDs), len(cloned.ChildIDs))
	assert.Equal(t, original.ChildIDs, cloned.ChildIDs)

	// Verify independence
	cloned.ChildIDs[0] = 999
	assert.Equal(t, model.PageID(10), original.ChildIDs[0], "Original should not be modified")

	// Verify consistency
	err := cloned.ValidateChildConsistency()
	assert.NoError(t, err)
}

// TestNode_Clear_ClearsChildIDs tests that Clear() properly clears ChildIDs.
func TestNode_Clear_ClearsChildIDs(t *testing.T) {
	node := NewNode(false)
	node.Keys = [][]byte{[]byte("key")}
	node.Children = []*Node{NewNode(true), NewNode(true)}
	node.ChildIDs = []model.PageID{10, 20}

	// Clear the node
	node.Clear()

	// Verify everything is cleared
	assert.Equal(t, 0, len(node.Keys))
	assert.Equal(t, 0, len(node.Values))
	assert.Equal(t, 0, len(node.Children))
	assert.Equal(t, 0, len(node.ChildIDs), "ChildIDs should be cleared")
}

// TestConsistency_AfterMultipleOperations tests consistency after complex operations.
func TestConsistency_AfterMultipleOperations(t *testing.T) {
	node := NewNode(false)
	node.PageID = 1

	// Add initial children (small number for basic operations)
	for i := 0; i < 3; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(10 + i)
		_ = child.Insert([]byte{byte(i)}, []byte("value"))

		err := node.InsertChild([]byte{byte(i * 2)}, child)
		require.NoError(t, err)
	}

	// Validate after inserts
	err := node.ValidateChildConsistency()
	assert.NoError(t, err)

	// Clone the node to test Clone consistency
	cloned := node.Clone()
	err = cloned.ValidateChildConsistency()
	assert.NoError(t, err)

	// Clear and validate
	node.Clear()
	err = node.ValidateChildConsistency()
	assert.NoError(t, err)

	// Now test Split with a full node
	fullNode := NewNode(false)
	fullNode.PageID = 2

	// Add enough children to make it full
	for i := 0; i < model.DefaultMaxKeys+1; i++ {
		child := NewNode(true)
		child.PageID = model.PageID(100 + i)
		_ = child.Insert([]byte{byte(i)}, []byte("value"))

		fullNode.Children = append(fullNode.Children, child)
		fullNode.ChildIDs = append(fullNode.ChildIDs, child.PageID)
	}

	// Add separator keys
	for i := 0; i < model.DefaultMaxKeys; i++ {
		key := []byte{byte(i)}
		fullNode.Keys = append(fullNode.Keys, key)
	}

	// Split the full node
	rightNode, _, err := fullNode.Split()
	require.NoError(t, err)

	// Validate after split
	err = fullNode.ValidateChildConsistency()
	assert.NoError(t, err)

	err = rightNode.ValidateChildConsistency()
	assert.NoError(t, err)

	// Merge back
	err = fullNode.Merge(rightNode)
	require.NoError(t, err)

	// Validate after merge
	err = fullNode.ValidateChildConsistency()
	assert.NoError(t, err)
}
