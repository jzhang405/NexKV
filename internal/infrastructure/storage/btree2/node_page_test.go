// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// --- Helpers ---

// newTestNodeWithChildren creates a node page with given keys and children.
// children must have len(keys)+1 entries (one extraChild).
func newTestNodeWithChildren(t *testing.T, s *OffheapBTreeStorage, keys [][]byte, children []model.PageID) NodePage {
	t.Helper()
	require.Len(t, children, len(keys)+1, "children count must be keys+1")

	id, err := s.AllocNodePage()
	require.NoError(t, err)
	rawID := uint32(id)

	dataEnd := s.pa.GetDataEnd(rawID)
	for i, key := range keys {
		require.NoError(t, s.pa.InsertIndexEntry(rawID, i, key, uint32(children[i]), &dataEnd),
			"InsertIndexEntry at idx %d", i)
	}
	// Set extraChild (children[count])
	s.pa.SetChild(rawID, len(keys), uint32(children[len(keys)]))

	node, err := s.GetNodePage(id)
	require.NoError(t, err)
	return node
}

// allocDummyChildren allocates n node pages as dummy children, returns their IDs.
// Uses AllocNodePage because InsertIndexEntry rejects child==0.
// Pre-allocs a sentinel page to ensure all returned IDs are non-zero.
func allocDummyChildren(t *testing.T, s *OffheapBTreeStorage, n int) []model.PageID {
	t.Helper()
	// Ensure pageID 0 is consumed so children are always non-zero.
	// InsertIndexEntry has a constraint: child cannot be 0.
	sentinel, err := s.AllocNodePage()
	require.NoError(t, err)
	_ = sentinel

	ids := make([]model.PageID, n)
	for i := range n {
		id, err := s.AllocNodePage()
		require.NoError(t, err)
		ids[i] = id
	}
	return ids
}

// --- Search Tests ---

func TestNodeSearchChildIndex(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("c"), []byte("f"), []byte("i")}
	children := allocDummyChildren(t, s, 4)
	node := newTestNodeWithChildren(t, s, keys, children)

	tests := []struct {
		key      string
		wantIdx  int
		wantBool bool
	}{
		{"a", 0, false}, // key < "c" → child[0]
		{"c", 1, true},  // key == "c" → child[1] (right subtree)
		{"d", 1, false}, // "c" < "d" < "f" → child[1]
		{"f", 2, true},  // key == "f" → child[2]
		{"g", 2, false}, // "f" < "g" < "i" → child[2]
		{"i", 3, true},  // key == "i" → child[3]
		{"z", 3, false}, // key > "i" → child[3] (extraChild)
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			idx, found := node.Search([]byte(tt.key))
			assert.Equal(t, tt.wantIdx, idx, "key=%q", tt.key)
			assert.Equal(t, tt.wantBool, found, "key=%q", tt.key)
		})
	}
}

func TestNodeSearchEqualKey(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e"), []byte("h")}
	children := allocDummyChildren(t, s, 4)
	node := newTestNodeWithChildren(t, s, keys, children)

	// Exact match goes right: SearchChildIndex returns idx+1
	idx, found := node.Search([]byte("e"))
	assert.True(t, found)
	assert.Equal(t, 2, idx, "equal key 'e' should return child index 2 (right subtree)")

	// Verify GetChild at that index is a valid child
	childID := node.GetChild(idx)
	assert.NotEqual(t, model.PageID(0), childID, "child should not be 0")
}

// --- ReplaceChild Tests ---

func TestNodeReplaceChild(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e")}
	children := allocDummyChildren(t, s, 3)
	node := newTestNodeWithChildren(t, s, keys, children)

	newChild, err := s.AllocLeafPage()
	require.NoError(t, err)

	newNode, err := node.ReplaceChild(1, newChild)
	require.NoError(t, err)

	assert.Equal(t, node.ChildCount(), newNode.ChildCount(), "ChildCount should not change")
	assert.Equal(t, newChild, newNode.GetChild(1), "child[1] should be updated")
	assert.Equal(t, children[0], newNode.GetChild(0), "child[0] should be unchanged")
}

func TestNodeReplaceChildExtraChild(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e")}
	children := allocDummyChildren(t, s, 3)
	node := newTestNodeWithChildren(t, s, keys, children)

	newExtraChild, err := s.AllocLeafPage()
	require.NoError(t, err)

	count := node.Count()
	newNode, err := node.ReplaceChild(count, newExtraChild)
	require.NoError(t, err)

	assert.Equal(t, newExtraChild, newNode.GetChild(count), "extraChild should be updated")
}

func TestNodeReplaceChildCOW(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)
	origID := node.PageID()

	newChild, _ := s.AllocLeafPage()
	newNode, err := node.ReplaceChild(0, newChild)
	require.NoError(t, err)

	assert.NotEqual(t, origID, newNode.PageID(), "COW must return new pageID")
	// Original unchanged
	assert.Equal(t, children[0], node.GetChild(0), "original child[0] unchanged")
}

// --- InsertChild Tests ---

func TestNodeInsertChildMiddle(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("e")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)
	// Before: child[0], key="e", extraChild=child[1]
	// ChildCount = 2

	leftID, _ := s.AllocLeafPage()
	rightID, _ := s.AllocLeafPage()

	// Insert at idx=0: split child[0] into (left, right) with splitKey="c"
	newNode, err := node.InsertChild(0, []byte("c"), leftID, rightID)
	require.NoError(t, err)

	assert.Equal(t, 3, newNode.ChildCount(), "ChildCount should increase by 1")
	assert.Equal(t, 2, newNode.Count(), "key count should increase by 1")

	// Verify: child[0]=left, key[0]="c", child[1]=right, key[1]="e", extraChild=child[1](old)
	assert.Equal(t, leftID, newNode.GetChild(0), "child[0] should be left")
	assert.Equal(t, rightID, newNode.GetChild(1), "child[1] should be right")
	// extraChild should be unchanged (old children[1])
	assert.Equal(t, children[1], newNode.GetChild(2), "extraChild should be unchanged")
}

func TestNodeInsertChildAtEnd(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("e")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)
	// Before: child[0], key="e", extraChild=child[1]
	// count=1, ChildCount=2

	leftID, _ := s.AllocLeafPage()
	rightID, _ := s.AllocLeafPage()
	count := node.Count()

	// Insert at idx=count (1): split extraChild into (left, right)
	newNode, err := node.InsertChild(count, []byte("h"), leftID, rightID)
	require.NoError(t, err)

	assert.Equal(t, 3, newNode.ChildCount(), "ChildCount should increase by 1")

	// After: child[0], key="e", child[1]=left, key="h", extraChild=right
	assert.Equal(t, children[0], newNode.GetChild(0), "child[0] should be original")
	assert.Equal(t, leftID, newNode.GetChild(1), "child[1] should be left")
	assert.Equal(t, rightID, newNode.GetChild(2), "extraChild should be right")
}

func TestNodeInsertChildCOW(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("e")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)
	origID := node.PageID()
	origCount := node.Count()

	leftID, _ := s.AllocLeafPage()
	rightID, _ := s.AllocLeafPage()

	newNode, err := node.InsertChild(0, []byte("c"), leftID, rightID)
	require.NoError(t, err)

	assert.NotEqual(t, origID, newNode.PageID(), "COW must return new pageID")
	assert.Equal(t, origCount, node.Count(), "original count must not change")
}

// --- Split Tests ---

func TestNodeSplit(t *testing.T) {
	s := newTestStorage(t)

	// Create node with 5 keys: "a", "c", "e", "g", "i"
	keys := [][]byte{[]byte("a"), []byte("c"), []byte("e"), []byte("g"), []byte("i")}
	children := allocDummyChildren(t, s, 6)
	node := newTestNodeWithChildren(t, s, keys, children)
	origCount := node.Count() // 5

	left, right, splitKey, err := node.Split()
	require.NoError(t, err)
	assert.NotEmpty(t, splitKey)

	// move-up: splitKey removed, left.Count + right.Count = origCount - 1
	assert.Equal(t, origCount-1, left.Count()+right.Count(),
		"left.Count + right.Count must equal orig.Count - 1 (move-up)")

	// splitKey not in left or right
	for i := 0; i < left.Count(); i++ {
		assert.True(t, !bytes.Equal(left.GetKey(i), splitKey),
			"splitKey must not be in left page")
	}
	for i := 0; i < right.Count(); i++ {
		assert.True(t, !bytes.Equal(right.GetKey(i), splitKey),
			"splitKey must not be in right page")
	}

	// All keys in left < splitKey < all keys in right
	for i := 0; i < left.Count(); i++ {
		assert.True(t, bytes.Compare(left.GetKey(i), splitKey) < 0,
			"left key %q must be < splitKey %q", left.GetKey(i), splitKey)
	}
	for i := 0; i < right.Count(); i++ {
		assert.True(t, bytes.Compare(right.GetKey(i), splitKey) > 0,
			"right key %q must be > splitKey %q", right.GetKey(i), splitKey)
	}
}

func TestNodeSplitChildren(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e"), []byte("h")}
	children := allocDummyChildren(t, s, 4)
	node := newTestNodeWithChildren(t, s, keys, children)

	left, right, splitKey, err := node.Split()
	require.NoError(t, err)

	// splitKey = "e" (mid=1)
	assert.Equal(t, []byte("e"), splitKey)

	// Left: child[0], key="b", extraChild=child[1] (2 children)
	assert.Equal(t, 1, left.Count())
	assert.Equal(t, 2, left.ChildCount())
	assert.Equal(t, children[0], left.GetChild(0))
	assert.Equal(t, children[1], left.GetChild(1)) // extraChild of left

	// Right: child[2], key="h", extraChild=child[3] (2 children)
	assert.Equal(t, 1, right.Count())
	assert.Equal(t, 2, right.ChildCount())
	assert.Equal(t, children[2], right.GetChild(0))
	assert.Equal(t, children[3], right.GetChild(1)) // extraChild of right
}

func TestNodeSplitTooFew(t *testing.T) {
	s := newTestStorage(t)

	// Empty node
	id, err := s.AllocNodePage()
	require.NoError(t, err)
	node, err := s.GetNodePage(id)
	require.NoError(t, err)

	_, _, _, err = node.Split()
	assert.Error(t, err, "Split on empty node should fail")

	// Single entry node
	child, _ := s.AllocLeafPage()
	child2, _ := s.AllocLeafPage()
	singleNode := newTestNodeWithChildren(t, s, [][]byte{[]byte("a")}, []model.PageID{child, child2})
	_, _, _, err = singleNode.Split()
	assert.Error(t, err, "Split on single-entry node should fail")
}

// --- Validate Tests ---

func TestNodeValidate(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e"), []byte("h")}
	children := allocDummyChildren(t, s, 4)
	node := newTestNodeWithChildren(t, s, keys, children)

	err := node.Validate()
	assert.NoError(t, err)
}

func TestNodeValidateEmpty(t *testing.T) {
	s := newTestStorage(t)
	id, err := s.AllocNodePage()
	require.NoError(t, err)
	node, err := s.GetNodePage(id)
	require.NoError(t, err)

	err = node.Validate()
	assert.NoError(t, err, "empty node should validate")
}

// --- ResolvePath Tests ---

func TestResolvePathSingleLeaf(t *testing.T) {
	s := newTestStorage(t)
	leafID, err := s.AllocLeafPage()
	require.NoError(t, err)

	path, err := resolvePath(s, leafID, []byte("any"))
	require.NoError(t, err)

	assert.Equal(t, leafID, path.LeafID)
	assert.Equal(t, 1, len(path.PageIDs))
	assert.Equal(t, -1, path.Indices[0])
}

func TestResolvePathToLeaf(t *testing.T) {
	s := newTestStorage(t)

	// Consume pageID 0 (InsertIndexEntry rejects child==0)
	sentinel, _ := s.AllocNodePage()
	_ = sentinel

	// Build a 2-level tree:
	// Root (node): keys=["e"], children=[leaf1, leaf2]
	leaf1ID, _ := s.AllocLeafPage()
	leaf2ID, _ := s.AllocLeafPage()

	// Insert some data to identify leaves
	dataEnd := s.pa.GetDataEnd(uint32(leaf1ID))
	s.pa.InsertLeafEntry(uint32(leaf1ID), 0, []byte("a"), []byte("1"), &dataEnd)
	s.pa.InsertLeafEntry(uint32(leaf1ID), 1, []byte("c"), []byte("3"), &dataEnd)

	dataEnd2 := s.pa.GetDataEnd(uint32(leaf2ID))
	s.pa.InsertLeafEntry(uint32(leaf2ID), 0, []byte("e"), []byte("5"), &dataEnd2)
	s.pa.InsertLeafEntry(uint32(leaf2ID), 1, []byte("g"), []byte("7"), &dataEnd2)

	rootID, _ := s.AllocNodePage()
	rawRootID := uint32(rootID)
	rootDataEnd := s.pa.GetDataEnd(rawRootID)
	require.NoError(t, s.pa.InsertIndexEntry(rawRootID, 0, []byte("e"), uint32(leaf1ID), &rootDataEnd))
	s.pa.SetChild(rawRootID, 1, uint32(leaf2ID)) // extraChild = leaf2

	// Search for "a" → should go to child[0] = leaf1
	path, err := resolvePath(s, rootID, []byte("a"))
	require.NoError(t, err)
	assert.Equal(t, leaf1ID, path.LeafID)
	assert.Equal(t, 2, len(path.PageIDs), "path should have 2 pages (root + leaf)")
	assert.Equal(t, 0, path.Indices[0], "should follow child[0]")

	// Search for "g" → should go to child[1] = leaf2
	path2, err := resolvePath(s, rootID, []byte("g"))
	require.NoError(t, err)
	assert.Equal(t, leaf2ID, path2.LeafID)
	assert.Equal(t, 1, path2.Indices[0], "should follow child[1] (extraChild)")
}

func TestResolvePathNavigation(t *testing.T) {
	s := newTestStorage(t)

	// Consume pageID 0 (InsertIndexEntry rejects child==0)
	sentinel, _ := s.AllocNodePage()
	_ = sentinel

	// 3-level tree: root → internal → leaf
	leafID, _ := s.AllocLeafPage()
	internalChildID, _ := s.AllocNodePage()

	// Internal child: keys=["d"], children=[leaf, leaf2]
	leaf2ID, _ := s.AllocLeafPage()
	intDataEnd := s.pa.GetDataEnd(uint32(internalChildID))
	require.NoError(t, s.pa.InsertIndexEntry(uint32(internalChildID), 0, []byte("d"), uint32(leafID), &intDataEnd))
	s.pa.SetChild(uint32(internalChildID), 1, uint32(leaf2ID))

	// Root: keys=["h"], children=[internalChild, leaf3]
	leaf3ID, _ := s.AllocLeafPage()
	rootID, _ := s.AllocNodePage()
	rootDataEnd := s.pa.GetDataEnd(uint32(rootID))
	require.NoError(t, s.pa.InsertIndexEntry(uint32(rootID), 0, []byte("h"), uint32(internalChildID), &rootDataEnd))
	s.pa.SetChild(uint32(rootID), 1, uint32(leaf3ID))

	// Search "a" → root child[0] → internal child[0] → leaf
	path, err := resolvePath(s, rootID, []byte("a"))
	require.NoError(t, err)
	assert.Equal(t, leafID, path.LeafID)
	assert.Equal(t, 3, len(path.PageIDs), "3-level path")
	assert.Equal(t, 0, path.Indices[0], "root: follow child[0]")
	assert.Equal(t, 0, path.Indices[1], "internal: follow child[0]")

	// Search "z" → root child[1] → leaf3
	path2, err := resolvePath(s, rootID, []byte("z"))
	require.NoError(t, err)
	assert.Equal(t, leaf3ID, path2.LeafID)
}

// --- Edge case: InsertChild preserves all existing data ---

func TestNodeInsertChildPreservesOtherKeys(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("b"), []byte("e"), []byte("h"), []byte("k")}
	children := allocDummyChildren(t, s, 5)
	node := newTestNodeWithChildren(t, s, keys, children)
	// Before: c0, "b", c1, "e", c2, "h", c3, "k", c4

	leftID, _ := s.AllocLeafPage()
	rightID, _ := s.AllocLeafPage()

	// Insert at idx=2 (split child c2 between "e" and "h")
	newNode, err := node.InsertChild(2, []byte("g"), leftID, rightID)
	require.NoError(t, err)

	assert.Equal(t, 5, newNode.Count(), "key count should increase by 1")
	assert.Equal(t, 6, newNode.ChildCount(), "child count should increase by 1")

	// Verify key ordering
	for i := 1; i < newNode.Count(); i++ {
		prev := newNode.GetKey(i - 1)
		curr := newNode.GetKey(i)
		assert.True(t, bytes.Compare(prev, curr) < 0,
			"keys must be sorted: %q < %q at idx %d", prev, curr, i)
	}

	// Verify: extraChild should be unchanged (children[4])
	assert.Equal(t, children[4], newNode.GetChild(5), "extraChild should be unchanged")
}

// --- fmt guard for unused import ---

func TestNodeGetKeyFormat(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("test")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)
	assert.Equal(t, "test", string(node.GetKey(0)))
	_ = fmt.Sprintf("node %d", node.PageID())
}

// --- Error path ---

func TestNodeReplaceChildOutOfBounds(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("a")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)

	_, err := node.ReplaceChild(-1, model.PageID(99))
	assert.Error(t, err)

	_, err = node.ReplaceChild(5, model.PageID(99))
	assert.Error(t, err)
}

func TestNodeInsertChildOutOfBounds(t *testing.T) {
	s := newTestStorage(t)
	keys := [][]byte{[]byte("a")}
	children := allocDummyChildren(t, s, 2)
	node := newTestNodeWithChildren(t, s, keys, children)

	_, err := node.InsertChild(-1, []byte("x"), 1, 2)
	assert.Error(t, err)

	_, err = node.InsertChild(5, []byte("x"), 1, 2)
	assert.True(t, errors.Is(err, ErrInvalidPage) || err != nil, "out of bounds should error")
}
