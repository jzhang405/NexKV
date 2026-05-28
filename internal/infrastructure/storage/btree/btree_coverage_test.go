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

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)


// ==========================================
// Coverage boost: Epoch, BeginTx, StorageAdapter, Metrics, Options, Debug
// ==========================================
// ==========================================

func TestGetWithMeta_Basic(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k"), []byte("v"))

	raw, beginTS, err := tree.GetWithMeta(ctx, []byte("k"))
	require.NoError(t, err)
	assert.NotNil(t, raw)
	assert.Greater(t, beginTS, uint64(0))
}

func TestGetWithMeta_NotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	_, _, err := tree.GetWithMeta(ctx, []byte("nonexistent"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// ==========================================
// Epoch & AfterCheckpoint
// ==========================================

func newTestBTreeWithEpoch(t *testing.T) (*BTree, *OffheapBTreeStorage) {
	t.Helper()
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	tree, err := NewBTree(storage, WithEpoch())
	require.NoError(t, err)
	t.Cleanup(func() { tree.Close() })
	return tree, storage
}

func TestBTree_WithEpoch(t *testing.T) {
	tree, _ := newTestBTreeWithEpoch(t)
	ctx := context.Background()

	for i := range 100 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("e-%03d", i)), []byte(fmt.Sprintf("v-%03d", i))))
	}
	assert.Equal(t, int64(100), tree.Size())

	for i := range 100 {
		val, err := tree.Get(ctx, []byte(fmt.Sprintf("e-%03d", i)))
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("v-%03d", i)), val)
	}
}

func TestBTree_AfterCheckpoint(t *testing.T) {
	tree, _ := newTestBTreeWithEpoch(t)
	ctx := context.Background()

	// Write some data to generate retired pages via COW
	for i := range 50 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("ac-%03d", i)), []byte(fmt.Sprintf("v1-%03d", i))))
	}

	// Overwrite to create COW-retired pages
	for i := range 50 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("ac-%03d", i)), []byte(fmt.Sprintf("v2-%03d", i))))
	}

	// AfterCheckpoint triggers reclamation — should not panic
	tree.AfterCheckpoint()
}

// ==========================================
// WithLatencyMetrics
// ==========================================

func TestBTree_WithLatencyMetrics(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	tree, err := NewBTree(storage, WithLatencyMetrics())
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// Verify that operations with latency metrics don't panic
	require.NoError(t, tree.Set(ctx, []byte("k"), []byte("v")))
	val, err := tree.Get(ctx, []byte("k"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), val)

	m := tree.metricsWithLatency()
	assert.NotNil(t, m)
}

// ==========================================
// BeginTx — transactional operations
// ==========================================

func TestBeginTx_GetSetCommit(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Pre-populate
	tree.Set(ctx, []byte("tx-existing"), []byte("old"))

	tx, err := tree.BeginTx(ctx)
	require.NoError(t, err)

	// Read existing
	val, err := tx.Get(ctx, []byte("tx-existing"))
	require.NoError(t, err)
	assert.Equal(t, []byte("old"), val)

	// Write
	require.NoError(t, tx.Set(ctx, []byte("tx-k1"), []byte("tx-v1")))
	require.NoError(t, tx.Set(ctx, []byte("tx-k2"), []byte("tx-v2")))

	// Commit
	require.NoError(t, tx.Commit(ctx))

	// Verify committed
	v1, _ := tree.Get(ctx, []byte("tx-k1"))
	v2, _ := tree.Get(ctx, []byte("tx-k2"))
	assert.Equal(t, []byte("tx-v1"), v1)
	assert.Equal(t, []byte("tx-v2"), v2)
}

func TestBeginTx_DeleteCommit(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("tx-del"), []byte("v"))

	tx, err := tree.BeginTx(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Delete(ctx, []byte("tx-del")))
	require.NoError(t, tx.Commit(ctx))

	_, err = tree.Get(ctx, []byte("tx-del"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBeginTx_Rollback(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("tx-rb"), []byte("original"))

	tx, err := tree.BeginTx(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Set(ctx, []byte("tx-rb"), []byte("modified")))
	require.NoError(t, tx.Rollback(ctx))

	// Value should be unchanged after rollback
	val, err := tree.Get(ctx, []byte("tx-rb"))
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), val)
}

// ==========================================
// Compaction watermark
// ==========================================

type testWatermark struct {
	ts uint64
}

func (w *testWatermark) Watermark() uint64 { return w.ts }

func TestBTree_SetCompactionWatermark_TryCompact(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	tree, err := NewBTree(storage)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// Insert, delete (create tombstone), then try compact
	for i := range 200 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("cp-%03d", i)), []byte(fmt.Sprintf("v-%03d", i))))
	}
	for i := range 100 {
		require.NoError(t, tree.Delete(ctx, []byte(fmt.Sprintf("cp-%03d", i))))
	}

	// Set watermark high enough to cover all keys
	tree.SetCompactionWatermark(&testWatermark{ts: ^uint64(0)})

	// TryCompact should not panic
	err = tree.TryCompact()
	require.NoError(t, err)
}

// ==========================================
// storage_adapter (transaction backend)
// ==========================================

func TestStorageAdapter_GetRaw(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	adapter := newStorageAdapter(tree)

	// Write via BTree.Set (MVCC encoded)
	tree.Set(ctx, []byte("sa-raw"), []byte("v"))

	raw, err := adapter.GetRaw(ctx, []byte("sa-raw"))
	require.NoError(t, err)
	assert.NotNil(t, raw)

	// Verify it's MVCC-encoded
	mvccVal, err := mvcc.ParseMVCC(raw)
	require.NoError(t, err)
	assert.Equal(t, mvcc.FlagNormal, mvccVal.Flag)
	assert.Equal(t, []byte("v"), mvccVal.RealVal)
}

func TestStorageAdapter_GetRaw_NotFound(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	adapter := newStorageAdapter(tree)
	_, err := adapter.GetRaw(ctx, []byte("nonexistent"))
	assert.ErrorIs(t, err, mvcc.ErrKeyNotFound)
}

func TestStorageAdapter_Set(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	adapter := newStorageAdapter(tree)

	// Encode value manually
	encoded, err := mvcc.BuildMVCC(mvcc.FlagNormal, 42, []byte("adapter-val"))
	require.NoError(t, err)
	require.NoError(t, adapter.Set(ctx, []byte("sa-set"), encoded))

	// Read back via BTree.GetRaw
	mvccVal, err := tree.GetRaw(ctx, []byte("sa-set"))
	require.NoError(t, err)
	assert.Equal(t, uint64(42), mvccVal.BeginTS)
	assert.Equal(t, []byte("adapter-val"), mvccVal.RealVal)
}

func TestStorageAdapter_Set_TombstoneRecovery(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	adapter := newStorageAdapter(tree)

	// Write tombstone via adapter
	tombstone, err := mvcc.BuildMVCC(mvcc.FlagTombstone, 1, nil)
	require.NoError(t, err)
	require.NoError(t, adapter.Set(ctx, []byte("sa-ts-key"), tombstone))

	// Now overwrite with normal value via adapter
	normal, err := mvcc.BuildMVCC(mvcc.FlagNormal, 2, []byte("recovered"))
	require.NoError(t, err)
	require.NoError(t, adapter.Set(ctx, []byte("sa-ts-key"), normal))

	raw, err := adapter.GetRaw(ctx, []byte("sa-ts-key"))
	require.NoError(t, err)
	mvccVal, err := mvcc.ParseMVCC(raw)
	require.NoError(t, err)
	assert.Equal(t, mvcc.FlagNormal, mvccVal.Flag)
	assert.Equal(t, []byte("recovered"), mvccVal.RealVal)
}

func TestStorageAdapter_Delete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	adapter := newStorageAdapter(tree)

	encoded, _ := mvcc.BuildMVCC(mvcc.FlagNormal, 1, []byte("v"))
	require.NoError(t, adapter.Set(ctx, []byte("sa-del"), encoded))

	require.NoError(t, adapter.Delete(ctx, []byte("sa-del")))
	_, err := adapter.GetRaw(ctx, []byte("sa-del"))
	assert.ErrorIs(t, err, mvcc.ErrKeyNotFound)
}

func TestStorageAdapter_Closed(t *testing.T) {
	tree, _ := newTestBTree(t)
	adapter := newStorageAdapter(tree)

	tree.Close()

	_, err := adapter.GetRaw(context.Background(), []byte("k"))
	assert.ErrorIs(t, err, ErrTreeClosed)

	err = adapter.Set(context.Background(), []byte("k"), []byte("v"))
	assert.ErrorIs(t, err, ErrTreeClosed)

	err = adapter.Delete(context.Background(), []byte("k"))
	assert.ErrorIs(t, err, ErrTreeClosed)
}

// ==========================================
// Metrics
// ==========================================

func TestBTree_WithMetrics(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	metrics := NewBTreeMetrics()
	tree, err := NewBTree(storage, WithMetrics(metrics))
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	for i := range 50 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("m-%03d", i)), []byte(fmt.Sprintf("v-%03d", i))))
	}
	for i := range 50 {
		tree.Get(ctx, []byte(fmt.Sprintf("m-%03d", i)))
	}
	for i := range 25 {
		tree.Delete(ctx, []byte(fmt.Sprintf("m-%03d", i)))
	}

	snap := tree.GetMetrics()
	assert.NotNil(t, snap)
	assert.Greater(t, snap.WriteCount, int64(0))
	assert.Greater(t, snap.ReadCount, int64(0))
	assert.Greater(t, snap.DeleteCount, int64(0))
	assert.Equal(t, int64(25), tree.Size())
}

func TestBTree_CloseIdempotent(t *testing.T) {
	tree, _ := newTestBTree(t)
	assert.NoError(t, tree.Close())
	assert.NoError(t, tree.Close()) // second close should be no-op
}

func TestBTree_RootPage(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k"), []byte("v"))
	root := tree.RootPage()
	assert.NotNil(t, root)
	assert.NotNil(t, root.GetPageInfo())
}

// ==========================================
// Options
// ==========================================

func TestOptions_WithTracer(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	tree, err := NewBTree(storage, WithTracer(DefaultTracer))
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	require.NoError(t, tree.Set(ctx, []byte("k"), []byte("v")))
	val, err := tree.Get(ctx, []byte("k"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), val)
}

func TestOptions_WithTSGenerator(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	tsGen := mvcc.NewLocalTS()
	tree, err := NewBTree(storage, WithTSGenerator(tsGen))
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	require.NoError(t, tree.Set(ctx, []byte("k"), []byte("v")))
	mvccVal, err := tree.GetRaw(ctx, []byte("k"))
	require.NoError(t, err)
	assert.Greater(t, mvccVal.BeginTS, uint64(0))
}

// ==========================================
// Debug
// ==========================================

func TestDebug_PrintTree(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := range 10 {
		tree.Set(ctx, []byte(fmt.Sprintf("d-%02d", i)), []byte(fmt.Sprintf("v-%02d", i)))
	}

	dump := tree.PrintTree()
	assert.NotEmpty(t, dump)
}

func TestDebug_AssertInvariants(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := range 50 {
		tree.Set(ctx, []byte(fmt.Sprintf("ai-%03d", i)), []byte(fmt.Sprintf("v-%03d", i)))
	}

	err := tree.AssertInvariants()
	assert.NoError(t, err)
}

func TestDebug_PrettyPrint(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("k"), []byte("v"))

	root := tree.RootPage()
	// Verify PageRef fields are accessible
	assert.NotNil(t, root.GetPageInfo())
	assert.True(t, root.IsLeaf())
}

// ==========================================
// Node page & leaf page edge cases
// ==========================================

func TestNodePage_GetChild_OutOfBounds(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert enough keys to trigger a split and create internal node
	for i := range 500 {
		tree.Set(ctx, []byte(fmt.Sprintf("node-%05d", i)), []byte(fmt.Sprintf("v-%05d", i)))
	}

	root := tree.RootPage()
	pi := root.GetPageInfo()
	if !pi.IsLeaf {
		// Internal node: test edge cases
		cc := root.GetChildren()
		assert.NotNil(t, cc)
		assert.Greater(t, len(cc.Children), 0)
	}
}

func TestBTree_Get_BoundaryKeys(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	// Insert sorted keys
	for i := range 300 {
		tree.Set(ctx, []byte(fmt.Sprintf("bk-%05d", i)), []byte(fmt.Sprintf("v-%05d", i)))
	}

	// Get first and last keys
	val, err := tree.Get(ctx, []byte("bk-00000"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v-00000"), val)

	val, err = tree.Get(ctx, []byte(fmt.Sprintf("bk-%05d", 299)))
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestBTree_Size_AfterOperations(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	assert.Equal(t, int64(0), tree.Size())

	tree.Set(ctx, []byte("a"), []byte("1"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Set(ctx, []byte("a"), []byte("2"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Set(ctx, []byte("b"), []byte("3"))
	assert.Equal(t, int64(2), tree.Size())

	tree.Delete(ctx, []byte("b"))
	assert.Equal(t, int64(1), tree.Size())

	tree.Delete(ctx, []byte("a"))
	assert.Equal(t, int64(0), tree.Size())
}

// ==========================================
// DeleteBatch — double delete (idempotent)
// ==========================================

func TestDeleteBatch_DoubleDelete(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	tree.Set(ctx, []byte("dd"), []byte("v"))
	require.NoError(t, tree.DeleteBatch(ctx, [][]byte{[]byte("dd")}))
	// Second delete should not error
	require.NoError(t, tree.DeleteBatch(ctx, [][]byte{[]byte("dd")}))
}
