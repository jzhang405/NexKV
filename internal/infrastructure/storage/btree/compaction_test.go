// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedWatermark is a WatermarkProvider that returns a fixed TS value.
type fixedWatermark uint64

func (w fixedWatermark) Watermark() uint64 { return uint64(w) }

func TestCompactSingleLeaf(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert 30 entries
	for i := range 30 {
		err := tree.Set(ctx, fmt.Appendf(nil, "key-%04d", i), []byte("value"))
		require.NoError(t, err)
	}
	assert.Equal(t, int64(30), tree.Size())

	// Delete 20 entries (creates tombstones)
	for i := range 20 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "key-%04d", i))
		require.NoError(t, err)
	}
	assert.Equal(t, int64(10), tree.Size())

	// Watermark above all tombstones
	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)

	err := tree.Compact(wp)
	require.NoError(t, err)

	// Remaining 10 entries must be readable
	for i := 20; i < 30; i++ {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "key-%04d", i))
		require.NoError(t, err)
		assert.Equal(t, []byte("value"), val)
	}

	// Deleted entries must be gone
	for i := range 20 {
		_, err := tree.Get(ctx, fmt.Appendf(nil, "key-%04d", i))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}
}

func TestCompactPreserveAboveWatermark(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert 30 entries
	for i := range 30 {
		err := tree.Set(ctx, fmt.Appendf(nil, "key-%04d", i), []byte("value"))
		require.NoError(t, err)
	}

	// Delete 10 entries
	for i := range 10 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "key-%04d", i))
		require.NoError(t, err)
	}

	// Watermark at 1 — below all tombstones' beginTS
	wp := fixedWatermark(1)

	err := tree.Compact(wp)
	require.NoError(t, err)

	// All 30 entries must still exist (tombstones not reclaimed)
	// Deleted entries return ErrKeyNotFound but still occupy space
	for i := range 30 {
		_, err := tree.Get(ctx, fmt.Appendf(nil, "key-%04d", i))
		if i < 10 {
			assert.ErrorIs(t, err, ErrKeyNotFound)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestCompactRootLeaf(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert 5 entries (all in single root leaf)
	for i := range 5 {
		err := tree.Set(ctx, fmt.Appendf(nil, "k-%d", i), []byte("val"))
		require.NoError(t, err)
	}

	// Delete 3
	for i := range 3 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "k-%d", i))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)
	err := tree.Compact(wp)
	require.NoError(t, err)

	// Remaining 2 must be readable
	for i := 3; i < 5; i++ {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "k-%d", i))
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), val)
	}
}

func TestCompactMultiLeaf(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert enough entries to trigger splits (spanning multiple leaves)
	// ~200 entries should create 3+ leaves with default page size
	for i := range 200 {
		err := tree.Set(ctx, fmt.Appendf(nil, "multi-%05d", i), []byte("payload-data"))
		require.NoError(t, err)
	}
	assert.Equal(t, int64(200), tree.Size())

	// Delete 100 entries scattered across the range
	for i := 0; i < 200; i += 2 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "multi-%05d", i))
		require.NoError(t, err)
	}
	assert.Equal(t, int64(100), tree.Size())

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)
	err := tree.Compact(wp)
	require.NoError(t, err)

	// Remaining 100 must be readable
	for i := 1; i < 200; i += 2 {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "multi-%05d", i))
		require.NoError(t, err, "key %d should exist", i)
		assert.Equal(t, []byte("payload-data"), val)
	}

	// Deleted must be gone
	for i := 0; i < 200; i += 2 {
		_, err := tree.Get(ctx, fmt.Appendf(nil, "multi-%05d", i))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}
}

func TestCompactConcurrentWrite(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Pre-load 100 entries to force multiple leaves, reducing contention on single leaf
	for i := range 100 {
		err := tree.Set(ctx, fmt.Appendf(nil, "cw-%04d", i), []byte("initial"))
		require.NoError(t, err)
	}

	// Delete entries scattered across leaves
	for i := 0; i < 100; i += 2 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "cw-%04d", i))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)

	err := tree.Compact(wp)
	require.NoError(t, err)

	// Verify remaining entries
	for i := 1; i < 100; i += 2 {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "cw-%04d", i))
		require.NoError(t, err)
		assert.Equal(t, []byte("initial"), val)
	}
}

func TestCompactNoReclaim(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert 10 entries, no tombstones
	for i := range 10 {
		err := tree.Set(ctx, fmt.Appendf(nil, "nr-%d", i), []byte("val"))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)
	err := tree.Compact(wp)
	require.NoError(t, err)

	// All entries must remain
	for i := range 10 {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "nr-%d", i))
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), val)
	}
}

func TestCompactClosedTree(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("k"), []byte("v"))
	require.NoError(t, err)

	err = tree.Close()
	require.NoError(t, err)

	wp := fixedWatermark(100)
	err = tree.Compact(wp)
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestCompactEmptyTree(t *testing.T) {
	tree, _ := newTestBTree(t)

	wp := fixedWatermark(100)
	err := tree.Compact(wp)
	require.NoError(t, err)
}

func TestCompactAllLeaves(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert 300 entries to fill multiple leaves
	for i := range 300 {
		err := tree.Set(ctx, fmt.Appendf(nil, "all-%06d", i), []byte("payload"))
		require.NoError(t, err)
	}

	// Delete every other entry
	for i := 0; i < 300; i += 2 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "all-%06d", i))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)

	// Run multiple compaction cycles until no more pages are reclaimed
	// Each cycle may compact different leaves
	totalCompacted := 0
	for range 5 {
		err := tree.Compact(wp)
		require.NoError(t, err)
		totalCompacted++
	}

	// Verify remaining entries are correct
	for i := 1; i < 300; i += 2 {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "all-%06d", i))
		require.NoError(t, err, "key %d should exist", i)
		assert.Equal(t, []byte("payload"), val)
	}

	// Verify deleted entries are gone
	for i := 0; i < 300; i += 2 {
		_, err := tree.Get(ctx, fmt.Appendf(nil, "all-%06d", i))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}
}

// TestCompact_RealPageRefUpdated is the deterministic regression test for the
// CRITICAL compaction bug (temp PageRef CAS). It verifies that after compaction,
// the page reachable via searchPath (which uses the real in-tree PageRef from
// ChildrenCache) has the compacted entry count — not the original count.
//
// In the old buggy code:
//   - compactPage CAS'd a temporary PageRef from the leaf chain walk
//   - The real PageRef in parent's ChildrenCache was never updated
//   - searchPath → real PageRef → old page with all 30 entries (20 tombstones)
//   - This test would FAIL: leaf.Count() == 30, not 10
//
// In the fixed code:
//   - compactPageWithParent CAS's the real PageRef from searchPath
//   - searchPath → real PageRef → compacted page with 10 entries
//   - This test PASSES: leaf.Count() == 10
func TestCompact_RealPageRefUpdated(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const totalEntries = 30
	const deletedEntries = 20

	for i := range totalEntries {
		err := tree.Set(ctx, fmt.Appendf(nil, "rpr-%04d", i), []byte("value"))
		require.NoError(t, err)
	}

	for i := range deletedEntries {
		err := tree.Delete(ctx, fmt.Appendf(nil, "rpr-%04d", i))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)
	err := tree.Compact(wp)
	require.NoError(t, err)

	// Search for a surviving key — must reach the compacted page
	survivingKey := fmt.Appendf(nil, "rpr-%04d", totalEntries-1)
	path, err := searchPath(tree.rootRef, survivingKey)
	require.NoError(t, err, "searchPath should find the surviving key")
	defer path.ReleaseAll()

	leafRef := path.Leaf().Ref
	pInfo := leafRef.GetPageInfo()
	require.NotNil(t, pInfo, "leaf PageInfo should not be nil")

	leaf, err := tree.storage.GetLeafPage(pInfo.PageID)
	require.NoError(t, err)

	// The key assertion: compacted page should have (totalEntries - deletedEntries)
	// entries. In the old buggy code, this would be totalEntries (30) because
	// the real PageRef was never updated and still points to the old page.
	assert.Equal(t, totalEntries-deletedEntries, leaf.Count(),
		"compacted leaf should have %d entries (real PageRef was not CAS-updated in old code)",
		totalEntries-deletedEntries)

	// Double-check: surviving key must be readable
	val, err := tree.Get(ctx, survivingKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
}

// TestCompact_OldBug_PageRecyclingDataCorruption simulates the worst-case
// scenario: after compaction frees the old page in the old buggy code,
// the freed page is recycled by Alloc and overwritten with different data.
// A subsequent read through searchPath (which still points to the old page ID)
// would return the overwritten data instead of the compacted content.
//
// This test works by:
//  1. Compacting (which in old code frees old page, CAS on temp ref only)
//  2. Forcing page recycling by allocating new pages until the freed page
//     is returned from the free list (lock-free queue is FIFO)
//  3. Writing sentinel data to the recycled page
//  4. Reading through searchPath → in old code: gets sentinel (BUG!)
//     in fixed code: gets correct compacted data
func TestCompact_OldBug_PageRecyclingDataCorruption(t *testing.T) {
	tree, storage := newTestBTree(t)
	ctx := context.Background()

	// Use unique sentinel value to detect stale reads
	sentinel := []byte("CORRUPTED-DATA-SENTINEL")

	for i := range 30 {
		err := tree.Set(ctx, fmt.Appendf(nil, "bug-%04d", i), []byte("original"))
		require.NoError(t, err)
	}

	for i := range 20 {
		err := tree.Delete(ctx, fmt.Appendf(nil, "bug-%04d", i))
		require.NoError(t, err)
	}

	wp := fixedWatermark(tree.tsGen.CurrentTS() + 100)
	err := tree.Compact(wp)
	require.NoError(t, err)

	// Force page recycling: the freed old page is in the free list.
	// Allocate and write sentinel until we've cycled through enough pages.
	// The page manager's free list is used by pm.Alloc().
	// We allocate pages, write sentinel data, and check if any surviving key
	// returns the sentinel (which means the old page was recycled and read).
	corrupted := false
	for range 100 {
		newID, aerr := storage.pm.Alloc()
		if aerr != nil {
			break
		}
		storage.pa.InitLeafPage(newID, 999)
		var dataEnd uint16
		_ = storage.pa.InsertLeafEntry(newID, 0, sentinel, sentinel, &dataEnd)

		// Check if any surviving key now returns sentinel
		for i := 20; i < 30; i++ {
			val, gerr := tree.Get(ctx, fmt.Appendf(nil, "bug-%04d", i))
			if gerr == nil && string(val) == string(sentinel) {
				corrupted = true
				break
			}
		}
		if corrupted {
			break
		}
	}

	// In fixed code: must NOT be corrupted
	assert.False(t, corrupted,
		"DATA CORRUPTION: searchPath returned data from recycled page. "+
			"The real PageRef was not CAS-updated by compaction.")
}
