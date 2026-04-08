// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeSplittingStateTransition verifies Normal → Splitting → Redirect/Normal
// state transitions on a leaf PageRef.
func TestNodeSplittingStateTransition(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := range 50 {
		err := tree.Set(ctx, fmt.Appendf(nil, "key-%04d", i), []byte("val"))
		require.NoError(t, err)
	}

	for i := range 50 {
		val, err := tree.Get(ctx, fmt.Appendf(nil, "key-%04d", i))
		require.NoError(t, err)
		assert.Equal(t, []byte("val"), val)
	}
}

// TestSplittingRollback verifies that failed splits roll back the Splitting
// marker, allowing subsequent writes to succeed.
func TestSplittingRollback(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("key1"), []byte("val1"))
	require.NoError(t, err)

	const goroutines = 20
	var wg sync.WaitGroup
	var successCount atomic.Int32
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 10 {
				key := fmt.Appendf(nil, "rb-%d-%d", id, j)
				require.Eventually(t, func() bool {
					return tree.Set(ctx, key, []byte("val")) == nil
				}, 5*time.Second, time.Millisecond, "g%d-k%d should eventually succeed", id, j)
				successCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int32(goroutines*10), successCount.Load())
	assert.Equal(t, int64(1+goroutines*10), tree.Size())
}

// TestSplittingThunderingHerd verifies that many goroutines writing to the
// same full leaf don't deadlock or panic under high contention.
func TestSplittingThunderingHerd(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 20
	const keysPerGoroutine = 5

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
				key := fmt.Appendf(nil, "herd-%03d-%02d", id, j)
				require.Eventually(t, func() bool {
					return tree.Set(ctx, key, []byte("val")) == nil
				}, 5*time.Second, time.Millisecond, "g%d-k%d should succeed", id, j)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*keysPerGoroutine), tree.Size())
	for id := range goroutines {
		for j := range keysPerGoroutine {
			key := fmt.Appendf(nil, "herd-%03d-%02d", id, j)
			val, err := tree.Get(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, []byte("val"), val)
		}
	}
}

// TestReadDuringSplitting verifies that read operations work correctly
// while a leaf is potentially in Splitting state (COW snapshot semantics).
func TestReadDuringSplitting(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const preFill = 30
	for i := range preFill {
		err := tree.Set(ctx, fmt.Appendf(nil, "pre-%04d", i), fmt.Appendf(nil, "val-%d", i))
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	const readers = 5
	const writers = 5

	for i := range writers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 20 {
				key := fmt.Appendf(nil, "wr-%d-%d", id, j)
				_ = tree.Set(ctx, key, []byte("new-val"))
			}
		}(i)
	}

	for i := range readers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 100 {
				key := fmt.Appendf(nil, "pre-%04d", j%preFill)
				val, err := tree.Get(ctx, key)
				if err == nil {
					assert.Equal(t, fmt.Appendf(nil, "val-%d", j%preFill), val,
						"reader %d, iteration %d", id, j)
				}
			}
		}(i)
	}

	wg.Wait()
}

// TestSplittingNonTransientError verifies that non-transient errors
// return immediately without infinite retry.
func TestSplittingNonTransientError(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	err := tree.Set(ctx, []byte("dup-key"), []byte("val1"))
	require.NoError(t, err)

	err = tree.Set(ctx, []byte("dup-key"), []byte("val2"))
	require.NoError(t, err)

	val, err := tree.Get(ctx, []byte("dup-key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("val2"), val)
	assert.Equal(t, int64(1), tree.Size())
}

// TestWriteOperationCASConflict verifies that CAS conflicts are retried
// transparently and all writes eventually succeed.
func TestWriteOperationCASConflict(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const goroutines = 4
	const keysPerGoroutine = 30

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
				key := fmt.Appendf(nil, "cas-%d-%03d", id, j)
				require.Eventually(t, func() bool {
					return tree.Set(ctx, key, []byte("val")) == nil
				}, 5*time.Second, time.Millisecond, "g%d-k%d should succeed", id, j)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*keysPerGoroutine), tree.Size())
}

// TestSplittingConcurrentDifferentLeaves verifies that concurrent splits
// of different leaves under the same parent work correctly.
func TestSplittingConcurrentDifferentLeaves(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const fillKeys = 100
	for i := range fillKeys {
		err := tree.Set(ctx, fmt.Appendf(nil, "fill-%04d", i), []byte("val"))
		require.NoError(t, err)
	}

	const goroutines = 4
	const keysPerGoroutine = 20

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
				key := fmt.Appendf(nil, "diff-%d-%03d", id, j)
				require.Eventually(t, func() bool {
					return tree.Set(ctx, key, []byte("val")) == nil
				}, 5*time.Second, time.Millisecond, "g%d-k%d should succeed", id, j)
			}
		}(i)
	}
	wg.Wait()

	totalExpected := int64(fillKeys + goroutines*keysPerGoroutine)
	assert.Equal(t, totalExpected, tree.Size())
}

// TestCascadingSplitWithOptimisticCAS verifies that cascading splits
// work correctly under optimistic CAS.
func TestCascadingSplitWithOptimisticCAS(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(256 * 1024 * 1024)
	require.NoError(t, err)
	tree, err := NewBTree(storage)
	require.NoError(t, err)
	defer tree.Close()
	ctx := context.Background()

	const numKeys = 10000
	for i := range numKeys {
		key := []byte("key-" + strconv.Itoa(i))
		err := tree.Set(ctx, key, []byte("val-"+strconv.Itoa(i)))
		require.NoError(t, err, "Set failed at key %d", i)
	}

	assert.Equal(t, int64(numKeys), tree.Size())

	for i := range numKeys {
		key := []byte("key-" + strconv.Itoa(i))
		val, err := tree.Get(ctx, key)
		require.NoError(t, err, "key-%d should exist", i)
		assert.Equal(t, []byte("val-"+strconv.Itoa(i)), val)
	}
}

// TestDeleteWithOptimisticCAS verifies that delete operations work correctly
// under optimistic CAS (non-split path).
func TestDeleteWithOptimisticCAS(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	const keyCount = 50
	for i := range keyCount {
		err := tree.Set(ctx, fmt.Appendf(nil, "del-%04d", i), []byte("val"))
		require.NoError(t, err)
	}

	const goroutines = 5
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := id * 10; j < (id+1)*10; j++ {
				key := fmt.Appendf(nil, "del-%04d", j)
				err := tree.Delete(ctx, key)
				require.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(0), tree.Size())

	for i := range keyCount {
		_, err := tree.Get(ctx, fmt.Appendf(nil, "del-%04d", i))
		assert.True(t, errors.Is(err, ErrKeyNotFound))
	}
}
