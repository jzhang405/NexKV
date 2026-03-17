package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_NotImplementedMethods tests methods that are not implemented.
func TestBTree_NotImplementedMethods(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Note: Get and Set are partially implemented
	// They return runtime errors instead of "not implemented" because:
	// 1. ChunkManager is not integrated yet
	// 2. Root node is not properly initialized
	// TODO: Update these tests after ChunkManager integration

	// Test Delete (已实现，但需要先插入数据才能删除)
	// 删除不存在的键会返回 "key not found"
	err = btree.Delete(ctx, []byte("key"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")

	// Test GetBatch
	_, err = btree.GetBatch(ctx, [][]byte{[]byte("key1"), []byte("key2")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test SetBatch
	err = btree.SetBatch(ctx, []service.KVPair{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test DeleteBatch
	err = btree.DeleteBatch(ctx, [][]byte{[]byte("key1"), []byte("key2")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test RangeScan
	_, err = btree.RangeScan(ctx, []byte("start"), []byte("end"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test BeginTx
	_, err = btree.BeginTx(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test CreateSnapshot
	_, err = btree.CreateSnapshot(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test ReleaseSnapshot
	err = btree.ReleaseSnapshot(ctx, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	// Test Stats
	_, err = btree.Stats(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}

// TestBTree_NotImplementedMethods_Closed tests error handling when BTree is closed.
func TestBTree_NotImplementedMethods_Closed(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)

	// Close the BTree
	btree.Close()

	ctx := context.Background()

	// Note: Set is implemented, so it should return ErrClosed
	err = btree.Set(ctx, []byte("key"), []byte("value"))
	assert.ErrorIs(t, err, ErrClosed)

	// Test Delete when closed
	err = btree.Delete(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrClosed)

	// Test GetBatch when closed
	_, err = btree.GetBatch(ctx, [][]byte{[]byte("key")})
	assert.ErrorIs(t, err, ErrClosed)

	// Test SetBatch when closed
	err = btree.SetBatch(ctx, []service.KVPair{})
	assert.ErrorIs(t, err, ErrClosed)

	// Test DeleteBatch when closed
	err = btree.DeleteBatch(ctx, [][]byte{[]byte("key")})
	assert.ErrorIs(t, err, ErrClosed)

	// Test RangeScan when closed
	_, err = btree.RangeScan(ctx, []byte("start"), []byte("end"))
	assert.ErrorIs(t, err, ErrClosed)
}

// TestBTree_HeightAndPageCount tests tree height and page count methods.
func TestBTree_HeightAndPageCount(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// ✅ Test GetHeight - 已实现 (P0-5)
	height, err := btree.GetHeight(ctx)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, height, 0) // 空树高度为 0
	t.Logf("Tree height: %d", height)

	// Test GetPageCount - not implemented
	pageCount, err := btree.GetPageCount(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, 0, pageCount)
}

// TestBTree_DumpTree tests DumpTree method.
func TestBTree_DumpTree(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// DumpTree is not implemented
	treeDump, err := btree.DumpTree(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Empty(t, treeDump)
}

// TestBTree_Validate tests Validate method.
func TestBTree_Validate(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Validate is not implemented
	err = btree.Validate(ctx)
	// Should return "not implemented" error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}
