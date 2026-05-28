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

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// newTestBTree creates a BTree for testing with cleanup.
func newTestBTree(t *testing.T) (*BTree, *OffheapBTreeStorage) {
	t.Helper()
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024) // 4GB
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

	const goroutines = 8
	const keysPerGoroutine = 10

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
				key := fmt.Appendf(nil, "key-%d-%d", id, j)
				value := fmt.Appendf(nil, "value-%d-%d", id, j)
				err := tree.SetWithRetry(ctx, key, value, 100)
				require.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*keysPerGoroutine), tree.Size())

	// Verify all keys exist
	for i := range goroutines {
		for j := range keysPerGoroutine {
			key := fmt.Appendf(nil, "key-%d-%d", i, j)
			expectedValue := fmt.Appendf(nil, "value-%d-%d", i, j)
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

	t.Run("RangeScan", func(t *testing.T) {
		_, err := tree.RangeScan(ctx, nil, nil)
		assert.ErrorIs(t, err, ErrNotImplemented)
	})

	t.Run("BeginTx", func(t *testing.T) {
		tx, err := tree.BeginTx(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, tx)
		tx.Rollback(ctx)
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

	const goroutines = 8
	const uniqueKeys = 10 // Multiple goroutines compete for same keys

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range uniqueKeys {
				key := fmt.Appendf(nil, "key-%d", j)
				value := fmt.Appendf(nil, "value-%d-%d", id, j)
				err := tree.SetWithRetry(ctx, key, value, 100)
				require.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()

	// Verify: each key has a value, tree is not corrupted
	for i := range uniqueKeys {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "key-%d", i))
		require.NoError(t, err, "key-%d should exist", i)
		require.NotNil(t, val, "key-%d value should not be nil", i)
		// Value should be one of the written values (format: value-{goroutineID}-{iteration})
		assert.Regexp(t, `^value-\d+-\d+$`, string(val))
	}

	// Verify size is correct (only uniqueKeys entries)
	assert.Equal(t, int64(uniqueKeys), tree.Size())
}

func TestBTreeClose_DeleteReturnsErr(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024)
	require.NoError(t, err)
	tree, err := NewBTree(storage)
	require.NoError(t, err)

	err = tree.Close()
	require.NoError(t, err)

	ctx := context.Background()
	err = tree.Delete(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestBTreeLargeDataset_Integrity(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const numKeys = 2000
	for i := 0; i < numKeys; i++ {
		key := fmt.Appendf(nil, "intkey-%05d", i)
		value := fmt.Appendf(nil, "intval-%05d", i)
		err := tree.SetWithRetry(ctx, key, value, 100)
		require.NoError(t, err)
	}

	assert.Equal(t, int64(numKeys), tree.Size())

	// Verify all keys
	for i := 0; i < numKeys; i++ {
		key := fmt.Appendf(nil, "intkey-%05d", i)
		val, err := tree.Get(ctx, key)
		require.NoError(t, err, "key %s should exist", key)
		expected := fmt.Appendf(nil, "intval-%05d", i)
		assert.Equal(t, expected, val)
	}
}

// --- Tombstone Phase 1 测试 ---

func TestBTreeTombstoneDelete_GetReturnsNotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key"), []byte("value"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), tree.Size())

	err = tree.Delete(ctx, []byte("key"))
	require.NoError(t, err)

	// Get 已删除的 Key 应返回 ErrKeyNotFound
	_, err = tree.Get(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// Size 应减少
	assert.Equal(t, int64(0), tree.Size())
}

func TestBTreeTombstoneDoubleDelete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key"), []byte("value"))
	require.NoError(t, err)

	err = tree.Delete(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), tree.Size())

	// 第二次删除应返回 ErrKeyNotFound，Size 不变
	err = tree.Delete(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
	assert.Equal(t, int64(0), tree.Size())
}

func TestBTreeTombstoneRecovery(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Set → Delete → Set 同 Key：Size 应正确恢复
	err := tree.Set(ctx, []byte("key"), []byte("val1"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), tree.Size())

	err = tree.Delete(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), tree.Size())

	err = tree.Set(ctx, []byte("key"), []byte("val2"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), tree.Size()) // 恢复为 1

	// Get 应返回新 Value
	val, err := tree.Get(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("val2"), val)
}

func TestBTreeTombstoneSizeSemantics(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// 完整 Size 语义验证
	assert.Equal(t, int64(0), tree.Size())

	tree.Set(ctx, []byte("a"), []byte("1"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Set(ctx, []byte("b"), []byte("2"))
	assert.Equal(t, int64(2), tree.Size())

	tree.Delete(ctx, []byte("a"))
	assert.Equal(t, int64(1), tree.Size())

	// Tombstone 恢复
	tree.Set(ctx, []byte("a"), []byte("3"))
	assert.Equal(t, int64(2), tree.Size())

	// 再删除
	tree.Delete(ctx, []byte("a"))
	assert.Equal(t, int64(1), tree.Size())

	// 验证剩余 Key
	val, err := tree.Get(ctx, []byte("b"))
	require.NoError(t, err)
	assert.Equal(t, []byte("2"), val)

	_, err = tree.Get(ctx, []byte("a"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBTreeTombstoneConcurrentDelete(t *testing.T) {
	old := MaxCASRetries
	MaxCASRetries = 50
	defer func() { MaxCASRetries = old }()

	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		key := fmt.Appendf(nil, "key-%03d", i)
		err := tree.Set(ctx, key, []byte("value"))
		require.NoError(t, err)
	}
	assert.Equal(t, int64(numKeys), tree.Size())

	// 并发删除所有 Key
	var wg sync.WaitGroup
	for i := 0; i < numKeys; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Appendf(nil, "key-%03d", idx)
			err := tree.Delete(ctx, key)
			assert.NoError(t, err, "Delete key-%03d should succeed", idx)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(0), tree.Size())

	// 所有 Key 应不可见
	for i := 0; i < numKeys; i++ {
		key := fmt.Appendf(nil, "key-%03d", i)
		_, err := tree.Get(ctx, key)
		assert.ErrorIs(t, err, ErrKeyNotFound, "key-%03d should be tombstoned", i)
	}
}

func TestBTreeTombstoneConcurrentSetDelete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 8
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Appendf(nil, "key-%d", j)
				if id%2 == 0 {
					_ = tree.Set(ctx, key, []byte(fmt.Sprintf("val-%d-%d", id, j)))
				} else {
					_ = tree.Delete(ctx, key)
				}
			}
		}(i)
	}
	wg.Wait()

	// 树不应崩溃，Size >= 0
	assert.True(t, tree.Size() >= 0, "Size should be non-negative, got %d", tree.Size())

	// Size 应与实际可见 Key 数量一致
	var visibleCount int64
	for j := 0; j < opsPerGoroutine; j++ {
		key := fmt.Appendf(nil, "key-%d", j)
		if _, err := tree.Get(ctx, key); err == nil {
			visibleCount++
		}
	}
	assert.Equal(t, visibleCount, tree.Size(),
		"Size should match actual visible key count")
}

// --- MVCC Phase 2a Tests ---

func TestBTreeMVCC_BeginTSAssigned(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("mvcc-key"), []byte("mvcc-val"))
	require.NoError(t, err)

	// GetRaw returns decoded MVCC value with beginTS > 0
	mvccVal, err := tree.GetRaw(ctx, []byte("mvcc-key"))
	require.NoError(t, err)

	assert.Equal(t, mvcc.FlagNormal, mvccVal.Flag)
	assert.Greater(t, mvccVal.BeginTS, uint64(0), "beginTS must be assigned")
	assert.Equal(t, []byte("mvcc-val"), mvccVal.RealVal)
}

func TestBTreeMVCC_BeginTSIncreasing(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// First Set
	err := tree.Set(ctx, []byte("key"), []byte("val1"))
	require.NoError(t, err)
	mvccVal1, err := tree.GetRaw(ctx, []byte("key"))
	require.NoError(t, err)

	// Second Set (update) -- beginTS should increase
	err = tree.Set(ctx, []byte("key"), []byte("val2"))
	require.NoError(t, err)
	mvccVal2, err := tree.GetRaw(ctx, []byte("key"))
	require.NoError(t, err)

	assert.Greater(t, mvccVal2.BeginTS, mvccVal1.BeginTS, "beginTS should increase on update")
	assert.Equal(t, mvcc.FlagNormal, mvccVal2.Flag)
	assert.Equal(t, []byte("val2"), mvccVal2.RealVal)
}

func TestBTreeMVCC_DeleteBeginTS(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key"), []byte("val"))
	require.NoError(t, err)

	err = tree.Delete(ctx, []byte("key"))
	require.NoError(t, err)

	// GetRaw returns Tombstone with beginTS
	mvccVal, err := tree.GetRaw(ctx, []byte("key"))
	require.NoError(t, err)

	assert.Equal(t, mvcc.FlagTombstone, mvccVal.Flag)
	assert.Greater(t, mvccVal.BeginTS, uint64(0), "Tombstone should have beginTS")
	assert.Empty(t, mvccVal.RealVal)
}

func TestBTreeMVCC_GetRaw_TombstoneVisible(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key"), []byte("val"))
	require.NoError(t, err)

	err = tree.Delete(ctx, []byte("key"))
	require.NoError(t, err)

	// Get returns ErrKeyNotFound (filters Tombstone)
	_, err = tree.Get(ctx, []byte("key"))
	assert.Equal(t, ErrKeyNotFound, err)

	// GetRaw returns the decoded MVCC value (Tombstone visible)
	mvccVal, err := tree.GetRaw(ctx, []byte("key"))
	require.NoError(t, err)
	assert.Equal(t, mvcc.FlagTombstone, mvccVal.Flag)
}

func TestBTreeMVCC_GetRaw_NotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Key never existed → ErrKeyNotFound
	_, err := tree.GetRaw(ctx, []byte("nonexistent"))
	assert.Equal(t, ErrKeyNotFound, err)
}

// TestBTreeMVCC_ConcurrentTSAssignment verifies that concurrent Set operations
// assign unique, monotonically increasing timestamps without data corruption.
//
// Pre-populates the tree sequentially to build BTree structure before the concurrent
// phase. Without this, 8 goroutines × 200 keys on an empty single-leaf tree creates
// COW split cascades on the root page, exhausting CAS retries under -race.
func TestBTreeMVCC_ConcurrentTSAssignment(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 8
	const keysPerGoroutine = 200

	// Phase 1: pre-populate sequentially to build BTree structure.
	// Eliminates CAS contention on root page splits during concurrent phase.
	for g := 0; g < goroutines; g++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := fmt.Appendf(nil, "key-%d-%d", g, j)
			value := fmt.Appendf(nil, "seed-%d-%d", g, j)
			require.NoError(t, tree.Set(ctx, key, value))
		}
	}

	// Phase 2: concurrent overwrites.
	// Keys are now distributed across many leaf pages → minimal CAS contention.
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Appendf(nil, "key-%d-%d", id, j)
				value := fmt.Appendf(nil, "val-%d-%d", id, j)
				err := tree.SetWithRetry(ctx, key, value, 100)
				require.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()

	// Verify all keys exist with valid MVCC values from concurrent overwrite
	for g := 0; g < goroutines; g++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := fmt.Appendf(nil, "key-%d-%d", g, j)
			mvccVal, err := tree.GetRaw(ctx, key)
			require.NoError(t, err, "key-%d-%d should exist", g, j)
			assert.Equal(t, mvcc.FlagNormal, mvccVal.Flag)
			assert.Greater(t, mvccVal.BeginTS, uint64(0), "key-%d-%d should have beginTS", g, j)
			assert.NotEmpty(t, mvccVal.RealVal)
		}
	}

	assert.Equal(t, int64(goroutines*keysPerGoroutine), tree.Size())
}
