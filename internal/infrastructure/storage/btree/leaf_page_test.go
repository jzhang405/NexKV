// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers ---

// newTestLeaf creates an empty leaf page and returns its LeafPage handle.
func newTestLeaf(t *testing.T) (LeafPage, *OffheapBTreeStorage) {
	t.Helper()
	s := newTestStorage(t)
	id, err := s.AllocLeafPage()
	require.NoError(t, err)
	leaf, err := s.GetLeafPage(id)
	require.NoError(t, err)
	return leaf, s
}

// insertEntries inserts key-value pairs sequentially and returns the last LeafPage handle.
func insertEntries(t *testing.T, leaf LeafPage, keys, vals [][]byte) LeafPage {
	t.Helper()
	current := leaf
	for i := range keys {
		var err error
		current, err = current.Insert(keys[i], vals[i])
		require.NoError(t, err, "Insert(%q) failed", keys[i])
	}
	return current
}

// --- Insert + Search ---

func TestLeafInsertSearch(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}
	vals := [][]byte{[]byte("1"), []byte("2"), []byte("3"), []byte("4"), []byte("5")}
	leaf = insertEntries(t, leaf, keys, vals)

	for i, k := range keys {
		idx, found := leaf.Search(k)
		assert.True(t, found, "Search(%q) should find key", k)
		assert.Equal(t, i, idx, "Search(%q) index", k)
		assert.Equal(t, vals[i], leaf.GetValue(idx))
	}
}

func TestLeafSearchMiss(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("b"), []byte("2"))
	leaf, _ = leaf.Insert([]byte("d"), []byte("4"))

	idx, found := leaf.Search([]byte("a"))
	assert.False(t, found)
	assert.Equal(t, 0, idx)

	idx, found = leaf.Search([]byte("c"))
	assert.False(t, found)
	assert.Equal(t, 1, idx)

	idx, found = leaf.Search([]byte("e"))
	assert.False(t, found)
	assert.Equal(t, 2, idx)

	idx, found = leaf.Search([]byte("z"))
	assert.False(t, found)
	assert.Equal(t, 2, idx)
}

// --- COW Immutability ---

func TestLeafCOW(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("key1"), []byte("val1"))
	origID := leaf.PageID()

	newLeaf, err := leaf.Insert([]byte("key2"), []byte("val2"))
	require.NoError(t, err)

	assert.NotEqual(t, origID, newLeaf.PageID(), "Insert must return new pageID")
	// Original page should still have key1
	idx, found := leaf.Search([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("val1"), leaf.GetValue(idx))
}

func TestLeafCOWOriginalImmutable(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("k1"), []byte("v1"))
	origCount := leaf.Count()
	origKey := leaf.GetKey(0)
	origVal := leaf.GetValue(0)

	// Mutate via Insert
	newLeaf, err := leaf.Insert([]byte("k2"), []byte("v2"))
	require.NoError(t, err)

	// Original unchanged
	assert.Equal(t, origCount, leaf.Count(), "original count must not change")
	assert.Equal(t, origKey, leaf.GetKey(0))
	assert.Equal(t, origVal, leaf.GetValue(0))

	// New page has both entries
	assert.Equal(t, 2, newLeaf.Count())
}

// --- Key Ordering ---

func TestLeafInsertKeyOrdering(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	// Insert in reverse order
	keys := [][]byte{[]byte("e"), []byte("c"), []byte("a"), []byte("d"), []byte("b")}
	vals := [][]byte{[]byte("5"), []byte("3"), []byte("1"), []byte("4"), []byte("2")}
	leaf = insertEntries(t, leaf, keys, vals)

	count := leaf.Count()
	for i := 1; i < count; i++ {
		prev := leaf.GetKey(i - 1)
		curr := leaf.GetKey(i)
		assert.True(t, bytes.Compare(prev, curr) < 0,
			"keys must be sorted: %q < %q at idx %d", prev, curr, i)
	}
}

// --- Update ---

func TestLeafUpdateValue(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("key"), []byte("old"))
	origCount := leaf.Count()

	newLeaf, err := leaf.Update(0, []byte("new"))
	require.NoError(t, err)

	assert.Equal(t, origCount, newLeaf.Count(), "Update must not change count")
	assert.Equal(t, []byte("new"), newLeaf.GetValue(0))
}

// --- Delete ---

func TestLeafDeleteMiddle(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}
	vals := [][]byte{[]byte("1"), []byte("2"), []byte("3"), []byte("4")}
	leaf = insertEntries(t, leaf, keys, vals)

	// Delete "b" (idx 1)
	newLeaf, err := leaf.Delete(1)
	require.NoError(t, err)

	assert.Equal(t, 3, newLeaf.Count())

	// "b" should not be found
	_, found := newLeaf.Search([]byte("b"))
	assert.False(t, found)

	// Remaining keys should be ordered
	assert.Equal(t, []byte("a"), newLeaf.GetKey(0))
	assert.Equal(t, []byte("c"), newLeaf.GetKey(1))
	assert.Equal(t, []byte("d"), newLeaf.GetKey(2))
}

func TestLeafDeleteFirst(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("a"), []byte("1"))
	leaf, _ = leaf.Insert([]byte("b"), []byte("2"))
	leaf, _ = leaf.Insert([]byte("c"), []byte("3"))

	newLeaf, err := leaf.Delete(0)
	require.NoError(t, err)

	assert.Equal(t, 2, newLeaf.Count())
	assert.Equal(t, []byte("b"), newLeaf.GetKey(0))
	assert.Equal(t, []byte("c"), newLeaf.GetKey(1))
}

func TestLeafDeleteLast(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("a"), []byte("1"))
	leaf, _ = leaf.Insert([]byte("b"), []byte("2"))
	leaf, _ = leaf.Insert([]byte("c"), []byte("3"))

	newLeaf, err := leaf.Delete(2)
	require.NoError(t, err)

	assert.Equal(t, 2, newLeaf.Count())
	assert.Equal(t, []byte("a"), newLeaf.GetKey(0))
	assert.Equal(t, []byte("b"), newLeaf.GetKey(1))
}

func TestLeafDeleteNotFound(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("a"), []byte("1"))

	_, err := leaf.Delete(-1)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound))

	_, err = leaf.Delete(5)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound))
}

// --- Split ---

func TestLeafSplit(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	// Fill with enough entries
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		val := []byte(fmt.Sprintf("val-%03d", i))
		var err error
		leaf, err = leaf.Insert(key, val)
		require.NoError(t, err)
	}

	origCount := leaf.Count()
	left, right, splitKey, err := leaf.Split()
	require.NoError(t, err)

	assert.Equal(t, origCount, left.Count()+right.Count(),
		"left.Count + right.Count must equal original count")
	assert.NotEmpty(t, splitKey)

	// splitKey boundary: all left keys < splitKey <= all right keys
	for i := 0; i < left.Count(); i++ {
		assert.True(t, bytes.Compare(left.GetKey(i), splitKey) < 0,
			"left key %q must be < splitKey %q", left.GetKey(i), splitKey)
	}
	for i := 0; i < right.Count(); i++ {
		assert.True(t, bytes.Compare(right.GetKey(i), splitKey) >= 0,
			"right key %q must be >= splitKey %q", right.GetKey(i), splitKey)
	}
}

func TestLeafSplitKeyBoundary(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}
	vals := [][]byte{[]byte("1"), []byte("2"), []byte("3"), []byte("4"), []byte("5")}
	leaf = insertEntries(t, leaf, keys, vals)

	left, right, splitKey, err := leaf.Split()
	require.NoError(t, err)

	// splitKey must be the first key of right page
	assert.Equal(t, right.GetKey(0), splitKey)
	_ = left // left is valid
}

func TestLeafSplitEvenOdd(t *testing.T) {
	for _, n := range []int{4, 5, 6, 7, 10, 11} {
		t.Run(fmt.Sprintf("count=%d", n), func(t *testing.T) {
			leaf, _ := newTestLeaf(t)

			for i := 0; i < n; i++ {
				key := []byte(fmt.Sprintf("k%03d", i))
				val := []byte(fmt.Sprintf("v%03d", i))
				var err error
				leaf, err = leaf.Insert(key, val)
				require.NoError(t, err)
			}

			left, right, splitKey, err := leaf.Split()
			require.NoError(t, err)
			assert.Equal(t, n, left.Count()+right.Count())
			assert.NotEmpty(t, splitKey)
		})
	}
}

func TestLeafSplitTooFew(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	_, _, _, err := leaf.Split()
	assert.Error(t, err, "Split on empty page should fail")

	leaf, _ = leaf.Insert([]byte("a"), []byte("1"))
	_, _, _, err = leaf.Split()
	assert.Error(t, err, "Split on single-entry page should fail")
}

// --- GetKey/GetValue returns copy ---

func TestLeafGetKeyReturnsCopy(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("original"), []byte("val"))
	key := leaf.GetKey(0)
	key[0] = 'X' // mutate returned copy

	// Original should be unchanged
	assert.Equal(t, []byte("original"), leaf.GetKey(0))
}

func TestLeafGetValueReturnsCopy(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("key"), []byte("original"))
	val := leaf.GetValue(0)
	val[0] = 'X' // mutate returned copy

	assert.Equal(t, []byte("original"), leaf.GetValue(0))
}

// --- IsFull / Capacity ---

func TestLeafIsFull(t *testing.T) {
	leaf, _ := newTestLeaf(t)
	assert.False(t, leaf.IsFull(), "empty page should not be full")

	// Fill until full or until alloc fails
	current := leaf
	for i := 0; i < 200; i++ {
		key := []byte(fmt.Sprintf("k%03d", i))
		val := []byte(fmt.Sprintf("v%03d", i))
		newLeaf, insertErr := current.Insert(key, val)
		if insertErr != nil {
			// Page full — this is expected
			require.NotNil(t, current, "current leaf must not be nil before break")
			break
		}
		current = newLeaf
	}
	require.NotNil(t, current, "current leaf must not be nil")
	assert.True(t, current.Capacity() > 0.8, "page should be mostly full after many inserts")
}

func TestLeafCapacity(t *testing.T) {
	leaf, _ := newTestLeaf(t)
	// Empty page has header overhead but no KV data; capacity should be very small
	assert.True(t, leaf.Capacity() < 0.05, "empty page capacity should be near 0, got %f", leaf.Capacity())

	leaf, _ = leaf.Insert([]byte("key"), []byte("val"))
	assert.True(t, leaf.Capacity() > 0, "page with data should have capacity > 0")
	assert.True(t, leaf.Capacity() < 1.0, "page with one entry should have capacity < 1.0")
}

// --- Duplicate Insert ---

func TestLeafDuplicateInsert(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("key"), []byte("val1"))
	_, err := leaf.Insert([]byte("key"), []byte("val2"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateKey))
}

// --- Reverse order insert ---

func TestLeafInsertReverseOrder(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	keys := [][]byte{[]byte("z"), []byte("y"), []byte("x"), []byte("w"), []byte("v")}
	vals := [][]byte{[]byte("5"), []byte("4"), []byte("3"), []byte("2"), []byte("1")}
	leaf = insertEntries(t, leaf, keys, vals)

	// All keys should be searchable
	for _, k := range keys {
		_, found := leaf.Search(k)
		assert.True(t, found, "Search(%q) should find key", k)
	}

	// Keys should be in ascending order
	for i := 1; i < leaf.Count(); i++ {
		assert.True(t, bytes.Compare(leaf.GetKey(i-1), leaf.GetKey(i)) < 0)
	}
}

// --- Empty key ---

func TestLeafInsertEmptyKey(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, err := leaf.Insert([]byte{}, []byte("val"))
	require.NoError(t, err, "Insert empty key should not panic")

	idx, found := leaf.Search([]byte{})
	assert.True(t, found, "Search empty key should find it")
	assert.Equal(t, 0, idx)
	assert.Equal(t, []byte("val"), leaf.GetValue(idx))
}

// --- Validate ---

func TestLeafValidate(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	vals := [][]byte{[]byte("1"), []byte("2"), []byte("3")}
	leaf = insertEntries(t, leaf, keys, vals)

	err := leaf.Validate()
	assert.NoError(t, err)
}

// --- Update Tests ---

func TestLeafUpdate_Success(t *testing.T) {
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("key1"), []byte("val1"))
	leaf, _ = leaf.Insert([]byte("key2"), []byte("val2"))

	// Update with same-size value (OverwriteLeafValue path)
	updated, err := leaf.Update(0, []byte("new1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("new1"), updated.GetValue(0))
	assert.Equal(t, []byte("val2"), updated.GetValue(1))
}

func TestLeafUpdate_OutOfRange(t *testing.T) {
	leaf, _ := newTestLeaf(t)
	leaf, _ = leaf.Insert([]byte("key1"), []byte("val1"))

	_, err := leaf.Update(-1, []byte("x"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound))

	_, err = leaf.Update(99, []byte("x"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound))
}

func TestLeafUpdate_LargerValue_NoPanic(t *testing.T) {
	// This test covers the rebuild path in Update() when OverwriteLeafValue
	// returns false (value is larger than original slot).
	leaf, _ := newTestLeaf(t)

	leaf, _ = leaf.Insert([]byte("k"), []byte("v"))
	leaf, _ = leaf.Insert([]byte("k2"), []byte("v2"))

	bigVal := make([]byte, 200)
	for i := range bigVal {
		bigVal[i] = byte(i)
	}

	// The main goal: Update with larger value should not panic or return unexpected error
	updated, err := leaf.Update(0, bigVal)
	require.NoError(t, err)
	assert.NotNil(t, updated)
	assert.Equal(t, 2, updated.Count())
}
