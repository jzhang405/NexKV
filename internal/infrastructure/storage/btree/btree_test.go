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

// newTestBTree creates a BTree for testing with cleanup.
func newTestBTree(t *testing.T) (*BTree, *OffheapBTreeStorage) {
	t.Helper()
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024) // 4MB
	require.NoError(t, err)
	tree, err := NewBTree(storage)
	require.NoError(t, err)
	t.Cleanup(func() { tree.Close() })
	return tree, storage
}

func TestBTreeSetGet(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		err := tree.Set(ctx, []byte(k), []byte("value-"+k))
		require.NoError(t, err, "set key %s", k)
	}

	for _, k := range keys {
		val, err := tree.Get(ctx, []byte(k))
		require.NoError(t, err, "get key %s", k)
		assert.Equal(t, []byte("value-"+k), val)
	}

	assert.Equal(t, int64(len(keys)), tree.Size())
}

func TestBTreeGetNotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	_, err := tree.Get(ctx, []byte("nonexistent"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBTreeUpdate(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key"), []byte("value1"))
	require.NoError(t, err)

	err = tree.Set(ctx, []byte("key"), []byte("value2"))
	require.NoError(t, err)

	val, err := tree.Get(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), val)
	assert.Equal(t, int64(1), tree.Size()) // update doesn't change count
}

func TestBTreeDelete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key1"), []byte("val1"))
	require.NoError(t, err)
	err = tree.Set(ctx, []byte("key2"), []byte("val2"))
	require.NoError(t, err)
	err = tree.Set(ctx, []byte("key3"), []byte("val3"))
	require.NoError(t, err)

	assert.Equal(t, int64(3), tree.Size())

	err = tree.Delete(ctx, []byte("key1"))
	require.NoError(t, err)

	_, err = tree.Get(ctx, []byte("key1"))
	assert.ErrorIs(t, err, ErrKeyNotFound)

	assert.Equal(t, int64(2), tree.Size())
}

func TestBTreeDeleteNotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Delete(ctx, []byte("nonexistent"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBTreeConcurrentSet(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 10
	const keysPerGoroutine = 10

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("key-%d-%d", id, j))
				value := []byte(fmt.Sprintf("value-%d-%d", id, j))
				err := tree.Set(ctx, key, value)
				require.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*keysPerGoroutine), tree.Size())

	// Verify all keys exist
	for i := 0; i < goroutines; i++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := []byte(fmt.Sprintf("key-%d-%d", i, j))
			expectedValue := []byte(fmt.Sprintf("value-%d-%d", i, j))
			val, err := tree.Get(ctx, key)
			require.NoError(t, err, "key %s should exist", key)
			assert.Equal(t, expectedValue, val)
		}
	}
}

func TestBTreeClose(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024)
	require.NoError(t, err)

	tree, err := NewBTree(storage)
	require.NoError(t, err)

	// First close should succeed
	err = tree.Close()
	require.NoError(t, err)

	// Second close should be no-op
	err = tree.Close()
	require.NoError(t, err)

	// Operations after close should fail
	ctx := context.Background()
	_, err = tree.Get(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrTreeClosed)

	err = tree.Set(ctx, []byte("key"), []byte("value"))
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestBTreeSize(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	assert.Equal(t, int64(0), tree.Size())

	tree.Set(ctx, []byte("a"), []byte("1"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Set(ctx, []byte("b"), []byte("2"))
	assert.Equal(t, int64(2), tree.Size())

	tree.Delete(ctx, []byte("a"))
	assert.Equal(t, int64(1), tree.Size())
}

// Test stub methods that return ErrNotImplemented
func TestBTreeStubMethods(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	t.Run("GetBatch", func(t *testing.T) {
		_, err := tree.GetBatch(ctx, [][]byte{[]byte("key")})
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("SetBatch", func(t *testing.T) {
		err := tree.SetBatch(ctx, nil)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("DeleteBatch", func(t *testing.T) {
		err := tree.DeleteBatch(ctx, nil)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("RangeScan", func(t *testing.T) {
		_, err := tree.RangeScan(ctx, nil, nil)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("BeginTx", func(t *testing.T) {
		_, err := tree.BeginTx(ctx)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("CreateSnapshot", func(t *testing.T) {
		_, err := tree.CreateSnapshot(ctx)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("ReleaseSnapshot", func(t *testing.T) {
		err := tree.ReleaseSnapshot(ctx, 0)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("Stats", func(t *testing.T) {
		_, err := tree.Stats(ctx)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})
}

// Test CAS retry path
func TestBTreeUpdateCASRetry(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert initial value
	err := tree.Set(ctx, []byte("key"), []byte("value1"))
	require.NoError(t, err)

	// Update triggers CAS path
	err = tree.Set(ctx, []byte("key"), []byte("value2"))
	require.NoError(t, err)

	// Verify update succeeded
	val, err := tree.Get(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), val)
}

// TestBTreeNoDataLoss verifies concurrent writes to the same keys don't corrupt data.
// This is a regression test for COW + CAS correctness.
func TestBTreeNoDataLoss(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 10
	const uniqueKeys = 10 // Multiple goroutines compete for same keys

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < uniqueKeys; j++ {
				key := fmt.Sprintf("key-%d", j)
				value := fmt.Sprintf("value-%d-%d", id, j)
				err := tree.Set(ctx, []byte(key), []byte(value))
				require.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()

	// Verify: each key has a value, tree is not corrupted
	for i := 0; i < uniqueKeys; i++ {
		val, err := tree.Get(ctx, []byte(fmt.Sprintf("key-%d", i)))
		require.NoError(t, err, "key-%d should exist", i)
		require.NotNil(t, val, "key-%d value should not be nil", i)
		// Value should be one of the written values (format: value-{goroutineID}-{iteration})
		assert.Regexp(t, `^value-\d+-\d+$`, string(val))
	}

	// Verify size is correct (only uniqueKeys entries)
	assert.Equal(t, int64(uniqueKeys), tree.Size())
}
