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
