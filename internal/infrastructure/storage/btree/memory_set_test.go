// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryBTree_Debug debugs the pure memory BTree.
func TestMemoryBTree_Debug(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	t.Logf("Initial stats: %+v", tree.GetStats())
	t.Logf("Root is nil: %v", tree.root == nil)

	// Test Set
	err := tree.Set(ctx, []byte("key1"), []byte("value1"))
	t.Logf("Set error: %v", err)
	t.Logf("After Set, root is nil: %v", tree.root == nil)
	if tree.root != nil {
		t.Logf("Root keys length: %d", len(tree.root.keys))
		t.Logf("Root values length: %d", len(tree.root.values))
	}
}

// TestMemoryBTree_BasicSet tests basic Set operations in pure memory.
func TestMemoryBTree_BasicSet(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Test Set
	err := tree.Set(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	err = tree.Set(ctx, []byte("key2"), []byte("value2"))
	require.NoError(t, err)

	err = tree.Set(ctx, []byte("key3"), []byte("value3"))
	require.NoError(t, err)

	// Test Get
	val, err := tree.Get(ctx, []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), val)

	val, err = tree.Get(ctx, []byte("key2"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), val)

	// Test not found
	_, err = tree.Get(ctx, []byte("notexist"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestMemoryBTree_Update tests updating existing keys.
func TestMemoryBTree_Update(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Set initial value
	err := tree.Set(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// Update value
	err = tree.Set(ctx, []byte("key1"), []byte("value1-updated"))
	require.NoError(t, err)

	// Verify updated value
	val, err := tree.Get(ctx, []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1-updated"), val)
}

// TestMemoryBTree_Delete tests Delete operations.
func TestMemoryBTree_Delete(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Set and delete
	err := tree.Set(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	err = tree.Delete(ctx, []byte("key1"))
	require.NoError(t, err)

	// Verify deleted
	_, err = tree.Get(ctx, []byte("key1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// Delete non-existent key
	err = tree.Delete(ctx, []byte("notexist"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestMemoryBTree_EmptyKey tests error handling for empty keys.
func TestMemoryBTree_EmptyKey(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Set with empty key
	err := tree.Set(ctx, []byte(""), []byte("value"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key cannot be empty")

	// Get with empty key
	_, err = tree.Get(ctx, []byte(""))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key cannot be empty")

	// Delete with empty key
	err = tree.Delete(ctx, []byte(""))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key cannot be empty")
}

// TestMemoryBTree_GetStats tests statistics collection.
func TestMemoryBTree_GetStats(t *testing.T) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Empty tree stats
	stats := tree.GetStats()
	assert.Equal(t, 0, stats.NumKeys)
	assert.Equal(t, 1, stats.Depth) // Empty tree still has root, depth is 1

	// Add some keys
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// Check stats
	stats = tree.GetStats()
	assert.Equal(t, 10, stats.NumKeys)
	assert.Equal(t, 1, stats.Depth)
}

// BenchmarkMemoryBTree_Set benchmarks pure memory Set operations.
func BenchmarkMemoryBTree_Set(b *testing.B) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("test-value")

		err := tree.Set(ctx, key, value)
		if err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkMemoryBTree_Get benchmarks pure memory Get operations.
func BenchmarkMemoryBTree_Get(b *testing.B) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	// Pre-populate tree with 1000 keys
	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("test-value")
		tree.Set(ctx, key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Only query keys that were inserted (modulo numKeys)
		keyNum := i % numKeys
		key := []byte{byte(keyNum & 0xff), byte((keyNum >> 8) & 0xff)}
		_, err := tree.Get(ctx, key)
		if err != nil {
			b.Fatalf("Get failed for key %d: %v", keyNum, err)
		}
	}
}

// BenchmarkMemoryBTree_Sequential benchmarks sequential operations.
func BenchmarkMemoryBTree_Sequential(b *testing.B) {
	ctx := context.Background()
	tree := NewMemoryBTree()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Set
		key := []byte{byte(i & 0xff)}
		err := tree.Set(ctx, key, []byte("value"))
		if err != nil {
			b.Fatalf("Set failed: %v", err)
		}

		// Get
		_, err = tree.Get(ctx, key)
		if err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}
}
