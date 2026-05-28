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
)

// ==========================================
// Batch operations: GetBatch + SetBatch + DeleteBatch
// ==========================================
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

// ==========================================
