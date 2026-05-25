package btree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageDispatcher_WriteBatch(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()

	// Small batch
	n := 100
	keys := make([][]byte, n)
	values := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(string(rune('a'+i%26)) + string(rune('a'+i/26%26)))
		values[i] = []byte(string(keys[i]) + "_value")
	}

	err := bw.WriteBatch(ctx, keys, values)
	require.NoError(t, err)

	// Verify all keys were written
	for i := range n {
		got, err := tree.Get(ctx, keys[i])
		require.NoError(t, err)
		assert.NotNil(t, got, "key %s not found", keys[i])
	}
}

func TestPageDispatcher_EmptyBatch(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()
	err := bw.WriteBatch(ctx, nil, nil)
	require.NoError(t, err)
}

func TestPageDispatcher_MismatchedLen(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()
	err := bw.WriteBatch(ctx, [][]byte{[]byte("k")}, [][]byte{})
	require.Error(t, err)
}

func TestKeyToShard(t *testing.T) {
	// Same key always maps to same shard
	k := []byte("test-key")
	s1 := KeyToShard(k)
	s2 := KeyToShard(k)
	assert.Equal(t, s1, s2)
	assert.True(t, s1 >= 0 && s1 < numShards)
}

func TestPageDispatcher_SetWithRetry(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	// SetWithRetry with generous retries
	err := tree.SetWithRetry(ctx, []byte("k1"), []byte("v1"), 10)
	require.NoError(t, err)

	got, err := tree.Get(ctx, []byte("k1"))
	require.NoError(t, err)
	assert.NotNil(t, got)

	// SetWithRetry with 1 retry — single attempt, works for uncontended write
	err = tree.SetWithRetry(ctx, []byte("k2"), []byte("v2"), 1)
	require.NoError(t, err)
}

func TestResolvePageID(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	// Insert some keys first
	for i := range 10 {
		k := []byte(string(rune('a' + i)))
		require.NoError(t, tree.Set(ctx, k, k))
	}

	// ResolvePageID should return a valid page
	pid, err := tree.ResolvePageID(ctx, []byte("c"))
	require.NoError(t, err)
	assert.NotZero(t, pid)
}
