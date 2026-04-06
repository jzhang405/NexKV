// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiLevelSplit inserts enough keys to trigger cascading internal node splits,
// verifying that all keys remain accessible after the tree grows beyond 2 levels.
func TestMultiLevelSplit(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(256 * 1024 * 1024)
	require.NoError(t, err)

	metrics := NewBTreeMetrics()
	tree, err := NewBTreeWithMetrics(storage, metrics)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// With "key-XXXX" format (~5-9 bytes per key) and "value-XXXX" (~7-11 bytes per value),
	// each leaf holds ~90-130 entries. Root internal node holds ~126 children.
	// 10000 keys → ~80-110 leaf splits → parent fills up → cascading split.
	const numKeys = 10000
	for i := range numKeys {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err, "Set failed at key %d", i)
	}

	assert.Equal(t, int64(numKeys), tree.Size(), "tree size should match inserted key count")

	for i := range numKeys {
		key := []byte("key-" + strconv.Itoa(i))
		val, err := tree.Get(ctx, key)
		require.NoError(t, err, "key-%d should be readable", i)
		assert.Equal(t, []byte("value-"+strconv.Itoa(i)), val, "key-%d value mismatch", i)
	}

	snap := tree.GetMetrics()
	t.Logf("Metrics: %s", snap.String())
	assert.GreaterOrEqual(t, snap.SplitCount, int64(2), "should have at least 2 splits (root leaf + cascading internal)")
	assert.GreaterOrEqual(t, snap.TreeHeightCount, int64(1), "tree height should have increased at least once")
}

// TestConcurrentSplit verifies concurrent writes triggering splits don't cause data races
// or corruption.
func TestConcurrentSplit(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(256 * 1024 * 1024)
	require.NoError(t, err)

	metrics := NewBTreeMetrics()
	tree, err := NewBTreeWithMetrics(storage, metrics)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	const goroutines = 8
	const keysPerGoroutine = 500
	totalKeys := goroutines * keysPerGoroutine

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
				key := fmt.Appendf(nil, "g%d-k%d", goroutineID, j)
				value := fmt.Appendf(nil, "v%d-%d", goroutineID, j)

				// 最多重试 100 次，超快
				for retries := 0; retries < 100; retries++ {
					err := tree.Set(ctx, key, value)
					if err == nil {
						break
					}
				}
			}
		}(g)
	}
	wg.Wait()

	var missing int
	for g := range goroutines {
		for j := range keysPerGoroutine {
			key := fmt.Appendf(nil, "g%d-k%d", g, j)
			val, err := tree.Get(ctx, key)
			if err != nil {
				missing++
				if missing <= 10 {
					t.Logf("Missing: %s", string(key))
				}
			} else {
				expected := fmt.Sprintf("v%d-%d", g, j)
				assert.Equal(t, expected, string(val), "value mismatch for %s", string(key))
			}
		}
	}

	assert.Equal(t, 0, missing, "no keys should be missing")
	assert.Equal(t, int64(totalKeys), tree.Size(), "tree size should match total keys")

	snap := tree.GetMetrics()
	t.Logf("Metrics: %s", snap.String())
}

// TestSplitMetrics verifies that SplitCount and TreeHeightCount are correctly incremented.
func TestSplitMetrics(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024)
	require.NoError(t, err)

	metrics := NewBTreeMetrics()
	tree, err := NewBTreeWithMetrics(storage, metrics)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	for i := range 500 {
		key := []byte("k" + strconv.Itoa(i))
		value := []byte("v" + strconv.Itoa(i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	snap := metrics.Snapshot()
	assert.Greater(t, snap.SplitCount, int64(0), "should have at least one split")
	assert.Equal(t, int64(500), snap.WriteCount, "should track 500 writes")
}
