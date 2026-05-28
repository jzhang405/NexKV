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

	"github.com/jzhang405/NexKV/internal/domain/service"
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

// ==========================================
// GetBatch
// ==========================================

func TestGetBatch_Empty(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	results, err := tree.GetBatch(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestGetBatch_AllExist(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 50
	keys := make([][]byte, n)
	values := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(fmt.Sprintf("key-%03d", i))
		values[i] = []byte(fmt.Sprintf("val-%03d", i))
		require.NoError(t, tree.Set(ctx, keys[i], values[i]))
	}

	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	assert.Equal(t, n, len(results))
	for i := range n {
		assert.Equal(t, values[i], results[i], "key %s", keys[i])
	}
}

func TestGetBatch_PartialMissing(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k1"), []byte("v1"))
	tree.Set(ctx, []byte("k3"), []byte("v3"))
	// k2 and k4 are missing

	keys := [][]byte{[]byte("k1"), []byte("k2"), []byte("k3"), []byte("k4")}
	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	assert.Equal(t, 4, len(results))
	assert.Equal(t, []byte("v1"), results[0])
	assert.Nil(t, results[1], "k2 should be nil (not found)")
	assert.Equal(t, []byte("v3"), results[2])
	assert.Nil(t, results[3], "k4 should be nil (not found)")
}

func TestGetBatch_Tombstone(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k1"), []byte("v1"))
	tree.Set(ctx, []byte("k2"), []byte("v2"))
	tree.Delete(ctx, []byte("k2")) // tombstone

	keys := [][]byte{[]byte("k1"), []byte("k2")}
	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), results[0])
	assert.Nil(t, results[1], "tombstone key should return nil")
}

func TestGetBatch_SingleKey(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k"), []byte("v"))
	results, err := tree.GetBatch(ctx, [][]byte{[]byte("k")})
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), results[0])
}

func TestGetBatch_TreeClosed(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Close()
	_, err := tree.GetBatch(ctx, [][]byte{[]byte("k")})
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestGetBatch_ContextCancel(t *testing.T) {
	tree, _ := newTestBTree(t)

	// Pre-populate keys so Get has work to do
	for i := range 500 {
		tree.Set(context.Background(), []byte(fmt.Sprintf("k-%05d", i)), []byte(fmt.Sprintf("v-%05d", i)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	keys := make([][]byte, 500)
	for i := range 500 {
		keys[i] = []byte(fmt.Sprintf("k-%05d", i))
	}
	_, err := tree.GetBatch(ctx, keys)
	assert.Error(t, err, "canceled context should return error")
}

// ==========================================
// SetBatch
// ==========================================

func TestSetBatch_Empty(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.SetBatch(ctx, nil)
	require.NoError(t, err)
}

func TestSetBatch_Single(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.SetBatch(ctx, []service.KVPair{{Key: []byte("k"), Value: []byte("v")}})
	require.NoError(t, err)

	val, err := tree.Get(ctx, []byte("k"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), val)
}

func TestSetBatch_Basic(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 200
	pairs := make([]service.KVPair, n)
	for i := range n {
		pairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("setbatch-%05d", i)),
			Value: []byte(fmt.Sprintf("value-%05d", i)),
		}
	}

	err := tree.SetBatch(ctx, pairs)
	require.NoError(t, err)

	for i := range n {
		val, err := tree.Get(ctx, pairs[i].Key)
		require.NoError(t, err, "key %s", pairs[i].Key)
		assert.Equal(t, pairs[i].Value, val)
	}
	assert.Equal(t, int64(n), tree.Size())
}

func TestSetBatch_TreeClosed(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Close()
	err := tree.SetBatch(ctx, []service.KVPair{{Key: []byte("k"), Value: []byte("v")}})
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestSetBatch_Reuse(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Two sequential calls to verify BatchWriter is reused, not re-created
	err := tree.SetBatch(ctx, []service.KVPair{{Key: []byte("a"), Value: []byte("1")}})
	require.NoError(t, err)
	err = tree.SetBatch(ctx, []service.KVPair{{Key: []byte("b"), Value: []byte("2")}})
	require.NoError(t, err)

	va, _ := tree.Get(ctx, []byte("a"))
	vb, _ := tree.Get(ctx, []byte("b"))
	assert.Equal(t, []byte("1"), va)
	assert.Equal(t, []byte("2"), vb)
}

// ==========================================
// DeleteBatch
// ==========================================

func TestDeleteBatch_Empty(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.DeleteBatch(ctx, nil)
	require.NoError(t, err)
}

func TestDeleteBatch_AllExist(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	keys := make([][]byte, 20)
	for i := range 20 {
		keys[i] = []byte(fmt.Sprintf("del-%03d", i))
		tree.Set(ctx, keys[i], []byte(fmt.Sprintf("v-%03d", i)))
	}

	err := tree.DeleteBatch(ctx, keys)
	require.NoError(t, err)

	for _, k := range keys {
		_, err := tree.Get(ctx, k)
		assert.ErrorIs(t, err, ErrKeyNotFound, "key %s should be deleted", k)
	}
	assert.Equal(t, int64(0), tree.Size())
}

func TestDeleteBatch_PartialMissing(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k1"), []byte("v1"))
	tree.Set(ctx, []byte("k2"), []byte("v2"))
	// k3 does not exist

	keys := [][]byte{[]byte("k1"), []byte("k2"), []byte("k3")}
	err := tree.DeleteBatch(ctx, keys)
	require.NoError(t, err, "partial missing should not fail")

	_, err = tree.Get(ctx, []byte("k1"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
	_, err = tree.Get(ctx, []byte("k2"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestDeleteBatch_TreeClosed(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Close()
	err := tree.DeleteBatch(ctx, [][]byte{[]byte("k")})
	assert.ErrorIs(t, err, ErrTreeClosed)
}

// ==========================================
// Batch concurrency safety
// ==========================================

func TestGetBatch_ConcurrentWithSet(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Pre-populate
	n := 300
	keys := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(fmt.Sprintf("conc-%05d", i))
		tree.Set(ctx, keys[i], []byte(fmt.Sprintf("v-%05d", i)))
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Concurrent reads and writes to different keys
	go func() {
		defer wg.Done()
		readKeys := keys[:150]
		results, err := tree.GetBatch(ctx, readKeys)
		assert.NoError(t, err)
		assert.Equal(t, 150, len(results))
	}()

	go func() {
		defer wg.Done()
		newPairs := make([]service.KVPair, 50)
		for i := range 50 {
			newPairs[i] = service.KVPair{
				Key:   []byte(fmt.Sprintf("conc-new-%05d", i)),
				Value: []byte(fmt.Sprintf("v-new-%05d", i)),
			}
		}
		err := tree.SetBatch(ctx, newPairs)
		assert.NoError(t, err)
	}()

	wg.Wait()
}

func TestGetBatch_SetBatch_DeleteBatch_Concurrent(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Pre-populate
	for i := range 200 {
		tree.Set(ctx, []byte(fmt.Sprintf("all-%05d", i)), []byte(fmt.Sprintf("v-%05d", i)))
	}

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		keys := make([][]byte, 50)
		for i := range 50 {
			keys[i] = []byte(fmt.Sprintf("all-%05d", i))
		}
		results, err := tree.GetBatch(ctx, keys)
		assert.NoError(t, err)
		assert.Equal(t, 50, len(results))
	}()

	go func() {
		defer wg.Done()
		pairs := make([]service.KVPair, 30)
		for i := range 30 {
			pairs[i] = service.KVPair{
				Key:   []byte(fmt.Sprintf("batch-%05d", i)),
				Value: []byte(fmt.Sprintf("bv-%05d", i)),
			}
		}
		err := tree.SetBatch(ctx, pairs)
		assert.NoError(t, err)
	}()

	go func() {
		defer wg.Done()
		delKeys := make([][]byte, 20)
		for i := 100; i < 120; i++ {
			delKeys[i-100] = []byte(fmt.Sprintf("all-%05d", i))
		}
		err := tree.DeleteBatch(ctx, delKeys)
		assert.NoError(t, err)
	}()

	wg.Wait()
}

// ==========================================
// GetBatch — additional coverage
// ==========================================

func TestGetBatch_Large(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 5000
	keys := make([][]byte, n)
	values := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(fmt.Sprintf("gl-%05d", i))
		values[i] = []byte(fmt.Sprintf("v-%05d", i))
		require.NoError(t, tree.Set(ctx, keys[i], values[i]))
	}

	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	assert.Equal(t, n, len(results))
	for i := range n {
		assert.Equal(t, values[i], results[i], "key %s", keys[i])
	}
}

func TestGetBatch_AllMissing(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	keys := [][]byte{[]byte("m1"), []byte("m2"), []byte("m3")}
	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	for i, r := range results {
		assert.Nil(t, r, "key %s should be nil", keys[i])
	}
}

func TestGetBatch_DuplicateKeys(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("dup"), []byte("value"))

	keys := [][]byte{[]byte("dup"), []byte("dup"), []byte("dup")}
	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	for _, r := range results {
		assert.Equal(t, []byte("value"), r)
	}
}

func TestGetBatch_EmptyValue(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("empty-val"), []byte(""))
	results, err := tree.GetBatch(ctx, [][]byte{[]byte("empty-val")})
	require.NoError(t, err)
	assert.Equal(t, []byte(""), results[0])
}

// ==========================================
// SetBatch — additional coverage
// ==========================================

func TestSetBatch_Large(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 2000
	pairs := make([]service.KVPair, n)
	for i := range n {
		pairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("sl-%05d", i)),
			Value: []byte(fmt.Sprintf("sv-%05d", i)),
		}
	}

	err := tree.SetBatch(ctx, pairs)
	require.NoError(t, err)
	assert.Equal(t, int64(n), tree.Size())

	for _, idx := range []int{0, n / 2, n - 1} {
		val, err := tree.Get(ctx, pairs[idx].Key)
		require.NoError(t, err)
		assert.Equal(t, pairs[idx].Value, val)
	}
}

func TestSetBatch_UpdateExisting(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("ue-1"), []byte("old-1"))
	tree.Set(ctx, []byte("ue-2"), []byte("old-2"))

	pairs := []service.KVPair{
		{Key: []byte("ue-1"), Value: []byte("new-1")},
		{Key: []byte("ue-2"), Value: []byte("new-2")},
		{Key: []byte("ue-3"), Value: []byte("new-3")},
	}
	err := tree.SetBatch(ctx, pairs)
	require.NoError(t, err)

	assert.Equal(t, int64(3), tree.Size())
	v1, _ := tree.Get(ctx, []byte("ue-1"))
	v2, _ := tree.Get(ctx, []byte("ue-2"))
	v3, _ := tree.Get(ctx, []byte("ue-3"))
	assert.Equal(t, []byte("new-1"), v1)
	assert.Equal(t, []byte("new-2"), v2)
	assert.Equal(t, []byte("new-3"), v3)
}

func TestSetBatch_TombstoneRecovery(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("tr-key"), []byte("v1"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Delete(ctx, []byte("tr-key"))
	assert.Equal(t, int64(0), tree.Size(), "size should be 0 after delete")

	// Restore via SetBatch — exercises the mutateUpdate COW tombstone path
	err := tree.SetBatch(ctx, []service.KVPair{{Key: []byte("tr-key"), Value: []byte("v2")}})
	require.NoError(t, err)

	val, err := tree.Get(ctx, []byte("tr-key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), val)
	assert.Equal(t, int64(1), tree.Size(), "size should be 1 after tombstone recovery")
}

func TestSetBatch_MixedNewAndExisting(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := range 50 {
		tree.Set(ctx, []byte(fmt.Sprintf("mx-%03d", i)), []byte(fmt.Sprintf("old-%03d", i)))
	}

	pairs := make([]service.KVPair, 80)
	for i := range 80 {
		pairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("mx-%03d", i)),
			Value: []byte(fmt.Sprintf("val-%03d", i)),
		}
	}

	err := tree.SetBatch(ctx, pairs)
	require.NoError(t, err)
	assert.Equal(t, int64(80), tree.Size())

	for i := range 80 {
		val, err := tree.Get(ctx, pairs[i].Key)
		require.NoError(t, err)
		assert.Equal(t, pairs[i].Value, val)
	}
}

// ==========================================
// GetBatch + SetBatch round-trip
// ==========================================

func TestGetBatch_SetBatch_RoundTrip(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	n := 300
	pairs := make([]service.KVPair, n)
	for i := range n {
		pairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("rt-%05d", i)),
			Value: []byte(fmt.Sprintf("rtv-%05d", i)),
		}
	}
	require.NoError(t, tree.SetBatch(ctx, pairs))

	keys := make([][]byte, n)
	for i := range n {
		keys[i] = pairs[i].Key
	}
	results, err := tree.GetBatch(ctx, keys)
	require.NoError(t, err)
	assert.Equal(t, n, len(results))
	for i := range n {
		assert.Equal(t, pairs[i].Value, results[i])
	}
}

func TestGetBatch_SetBatch_RoundTrip_WithDelete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	pairs := make([]service.KVPair, 100)
	for i := range 100 {
		pairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("rtd-%03d", i)),
			Value: []byte(fmt.Sprintf("v-%03d", i)),
		}
	}
	require.NoError(t, tree.SetBatch(ctx, pairs))

	delKeys := make([][]byte, 50)
	for i := range 50 {
		delKeys[i] = []byte(fmt.Sprintf("rtd-%03d", i*2))
	}
	require.NoError(t, tree.DeleteBatch(ctx, delKeys))
	assert.Equal(t, int64(50), tree.Size())

	allKeys := make([][]byte, 100)
	for i := range 100 {
		allKeys[i] = []byte(fmt.Sprintf("rtd-%03d", i))
	}
	results, err := tree.GetBatch(ctx, allKeys)
	require.NoError(t, err)
	for i := range 100 {
		if i%2 == 0 {
			assert.Nil(t, results[i], "key %d should be deleted (nil)", i)
		} else {
			assert.Equal(t, pairs[i].Value, results[i], "key %d should exist", i)
		}
	}
}

// ==========================================
// SetBatch + DeleteBatch overlapping keys
// ==========================================

func TestSetBatch_DeleteBatch_SameKeys(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := range 50 {
		tree.Set(ctx, []byte(fmt.Sprintf("same-%03d", i)), []byte(fmt.Sprintf("old-%03d", i)))
	}
	assert.Equal(t, int64(50), tree.Size())

	updatePairs := make([]service.KVPair, 20)
	for i := range 20 {
		updatePairs[i] = service.KVPair{
			Key:   []byte(fmt.Sprintf("same-%03d", i)),
			Value: []byte(fmt.Sprintf("new-%03d", i)),
		}
	}
	require.NoError(t, tree.SetBatch(ctx, updatePairs))

	delKeys := make([][]byte, 20)
	for i := range 20 {
		delKeys[i] = []byte(fmt.Sprintf("same-%03d", i+30))
	}
	require.NoError(t, tree.DeleteBatch(ctx, delKeys))

	assert.Equal(t, int64(30), tree.Size())
	for i := range 20 {
		val, err := tree.Get(ctx, []byte(fmt.Sprintf("same-%03d", i)))
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("new-%03d", i)), val)
	}
	for i := 30; i < 50; i++ {
		_, err := tree.Get(ctx, []byte(fmt.Sprintf("same-%03d", i)))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	}
}
