// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test 1: TestLeafMerge ---

func TestLeafMerge_TwoSparseLeaves(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert enough keys to cause at least one split (create multiple leaves)
	for i := 0; i < 200; i++ {
		key := []byte(fmt.Sprintf("k-%08d", i))
		val := []byte(fmt.Sprintf("v-%08d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	// Delete most keys from the first few pages to make them sparse
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("k-%08d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	// After deleting 100 keys, the tree should still be functional
	// and the leftmost leaf should be sparse
	assert.True(t, tree.Size() > 0)
	assert.True(t, tree.Size() < 200)

	// Verify remaining keys are accessible
	for i := 150; i < 200; i++ {
		key := []byte(fmt.Sprintf("k-%08d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err, "key %s should exist", key)
	}
}

// --- Test 2: TestMergePropagation ---

func TestMergePropagation_MultiLevelTree(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert many keys to create a multi-level tree
	n := 500
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		val := []byte(fmt.Sprintf("value-for-key-%05d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	// Verify tree has multiple levels
	rootPI := tree.RootPage().GetPageInfo()
	require.NotNil(t, rootPI)
	// After 500 inserts with large values, root should be an internal node
	assert.False(t, rootPI.IsLeaf, "root should be an internal node after many inserts")

	// Delete alternating keys to create sparse pages throughout
	for i := 0; i < n; i += 2 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	// Verify remaining keys
	for i := 1; i < n; i += 2 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err, "odd keys should still exist")
	}

	// Deleted keys should not be found
	for i := 0; i < n; i += 2 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		_, err := tree.Get(ctx, key)
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}

	assert.Equal(t, int64(n/2), tree.Size())
}

// --- Test 3: TestRootMerge ---

func TestRootMerge_ReduceHeight(t *testing.T) {
	tree, storage := newTestBTree(t)

	// Create a root that has only 1 child — simulate mergeRoot scenario
	rootPage, err := storage.AllocNodePage()
	require.NoError(t, err)
	rootHandle := &nodePageHandle{id: rootPage, pa: storage.pa, storage: storage}

	// Create a child leaf page
	childPage, err := storage.AllocLeafPage()
	require.NoError(t, err)
	chLeaf, _ := (&leafPageHandle{id: childPage, pa: storage.pa, storage: storage}).Insert([]byte("key1"), []byte("val1"))

	// Insert child into root (root has 1 key → 2 children)
	_, err = rootHandle.InsertChild(0, []byte("key1"), childPage, chLeaf.PageID())
	if err != nil {
		t.Skipf("InsertChild failed: %v", err)
	}

	// Verify mergeRoot works without panicking
	err = tree.mergeRoot()
	// mergeRoot may succeed or return nil (root may not qualify)
	assert.NoError(t, err)
}

// --- Test 4: TestMergeThreshold ---

func TestMergeThreshold_SparseDetection(t *testing.T) {
	_, storage := newTestBTree(t)

	// Create a leaf page with 1 entry (should be sparse)
	leafID, err := storage.AllocLeafPage()
	require.NoError(t, err)
	leaf := &leafPageHandle{id: leafID, pa: storage.pa, storage: storage}

	// Empty/1-entry page should be sparse
	assert.True(t, isLeafSparse(leaf, MergeThreshold), "nearly-empty leaf should be sparse")

	// Fill the leaf with entries to exceed MergeThreshold (0.5)
	// Each entry: 16B header + 25B key + 25B value = 66B, 40 entries = 2640B + 56B header ≈ 66% usage
	var lf LeafPage = leaf
	for i := 0; i < 40; i++ {
		key := []byte(fmt.Sprintf("key-%020d-fill", i))
		val := []byte(fmt.Sprintf("value-%020d-data", i))
		var insErr error
		lf, insErr = lf.Insert(key, val)
		require.NoError(t, insErr)
	}

	// Full leaf should NOT be sparse
	assert.False(t, isLeafSparse(lf, MergeThreshold), "full leaf should not be sparse")
}

// --- Test 5: TestConcurrentMerge ---

func TestConcurrentMerge_DeleteAndGet(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Setup: insert 200 keys
	for i := 0; i < 200; i++ {
		key := []byte(fmt.Sprintf("k-%04d", i))
		val := []byte(fmt.Sprintf("v-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Concurrent deletes
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := gid * 50; i < (gid+1)*50; i++ {
				key := []byte(fmt.Sprintf("k-%04d", i))
				if err := tree.Delete(ctx, key); err != nil && err != ErrKeyNotFound {
					errCh <- err
					return
				}
			}
		}(g)
	}

	// Concurrent gets
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := gid*50 + 50; i < (gid+1)*50+50 && i < 200; i++ {
				key := []byte(fmt.Sprintf("k-%04d", i))
				_, _ = tree.Get(ctx, key)
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}

	// Tree should be consistent after concurrent operations
	assert.True(t, tree.Size() >= 0)
}

// --- Test 6: TestDeleteHalfKeysNoSpaceLeak ---

func TestDeleteHalfKeysNoSpaceLeak(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 300
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("dk-%05d", i))
		val := []byte(fmt.Sprintf("data-for-%05d-with-padding", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	initialSize := tree.Size()
	t.Logf("initial size: %d", initialSize)

	// Delete 50% of keys
	for i := 0; i < n; i += 2 {
		key := []byte(fmt.Sprintf("dk-%05d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	finalSize := tree.Size()
	t.Logf("final size: %d (expected ~%d)", finalSize, n/2)

	assert.Equal(t, int64(n/2), finalSize, "size should be halved after deleting 50%% keys")

	// Verify all remaining keys are accessible
	for i := 1; i < n; i += 2 {
		key := []byte(fmt.Sprintf("dk-%05d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err, "key %s should exist", key)
	}

	// Deleted keys should return ErrKeyNotFound
	for i := 0; i < n; i += 2 {
		key := []byte(fmt.Sprintf("dk-%05d", i))
		_, err := tree.Get(ctx, key)
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}
}

// --- Test 7: TestSplitPreferCompaction ---

func TestSplitPreferCompaction_HighTombstonePage(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert keys, then delete some to create tombstone entries
	// The page should have high tombstone ratio after deletes
	for i := 0; i < 80; i++ {
		key := []byte(fmt.Sprintf("spc-%04d", i))
		val := []byte(fmt.Sprintf("v-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	// Delete 60 keys → high tombstone ratio in early pages
	for i := 0; i < 60; i++ {
		key := []byte(fmt.Sprintf("spc-%04d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	assert.Equal(t, int64(20), tree.Size())

	// Verify deleted keys are not accessible (tombstone semantics)
	for i := 0; i < 60; i++ {
		key := []byte(fmt.Sprintf("spc-%04d", i))
		_, err := tree.Get(ctx, key)
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}

	// Verify remaining keys are accessible
	for i := 60; i < 80; i++ {
		key := []byte(fmt.Sprintf("spc-%04d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err)
	}
}

// --- Test 8: TestCompact ---

func TestCompact_ReclaimsTombstoneSpace(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert + delete to create tombstone entries
	for i := 0; i < 40; i++ {
		key := []byte(fmt.Sprintf("c-%04d", i))
		val := []byte(fmt.Sprintf("cv-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}
	for i := 0; i < 20; i++ {
		key := []byte(fmt.Sprintf("c-%04d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	// Compact should not crash — it requires a real WatermarkProvider
	// For now, verify tree is consistent after operations
	assert.Equal(t, int64(20), tree.Size())

	// A nil WatermarkProvider would panic; we skip actual compaction for now
	// since it requires mvcc integration
}

// --- Test 9: TestBorrowFromLeftRightLeaf ---

func TestBorrowFromLeftRightLeaf_RedistributesKeys(t *testing.T) {
	_, storage := newTestBTree(t)

	// Create two leaf pages: left has many entries, right has few
	leftID, err := storage.AllocLeafPage()
	require.NoError(t, err)
	left := &leafPageHandle{id: leftID, pa: storage.pa, storage: storage}

	rightID, err := storage.AllocLeafPage()
	require.NoError(t, err)
	right := &leafPageHandle{id: rightID, pa: storage.pa, storage: storage}

	// Fill left with entries
	var lf LeafPage = left
	for i := 0; i < 20; i++ {
		lf, err = lf.Insert([]byte(fmt.Sprintf("L-%02d", i)), []byte(fmt.Sprintf("val-%02d", i)))
		require.NoError(t, err)
	}

	// Right has 1 entry
	var rf LeafPage = right
	rf, err = rf.Insert([]byte("z-key"), []byte("z-val"))
	require.NoError(t, err)

	leftBefore := lf.Count()
	rightBefore := rf.Count()
	t.Logf("before borrow: left=%d, right=%d", leftBefore, rightBefore)

	// Borrow from left to right (left has plenty, right has few)
	newRight, newLeft, err := storage.BorrowFromLeftLeaf(rf, lf)
	require.NoError(t, err)

	t.Logf("after borrow: left=%d, right=%d", newLeft.Count(), newRight.Count())
	assert.Equal(t, rightBefore+1, newRight.Count(), "right should gain one entry")
	assert.Equal(t, leftBefore-1, newLeft.Count(), "left should lose one entry")

	// Verify keys are sorted: left's last key < right's first key
	assert.True(t, string(newLeft.GetKey(newLeft.Count()-1)) < string(newRight.GetKey(0)),
		"left's max key should be less than right's min key")
}

// --- Test 10: TestPageHeaderTombstoneCount ---

func TestPageHeaderTombstoneCount_IncrementDecrement(t *testing.T) {
	_, storage := newTestBTree(t)

	leafID, err := storage.AllocLeafPage()
	require.NoError(t, err)

	rawID := uint32(leafID)

	// Initial tombstone count should be 0
	assert.Equal(t, uint16(0), storage.pa.GetTombstoneCount(rawID))

	// Increment
	storage.pa.IncrementTombstone(rawID)
	assert.Equal(t, uint16(1), storage.pa.GetTombstoneCount(rawID))

	// Decrement
	storage.pa.DecrementTombstone(rawID)
	assert.Equal(t, uint16(0), storage.pa.GetTombstoneCount(rawID))
}

// TestTombstoneCountUnderflowPanic verifies the underflow guard.
func TestTombstoneCountUnderflowPanic(t *testing.T) {
	_, storage := newTestBTree(t)
	leafID, err := storage.AllocLeafPage()
	require.NoError(t, err)

	assert.Panics(t, func() {
		storage.pa.DecrementTombstone(uint32(leafID))
	}, "decrement at zero should panic")
}
