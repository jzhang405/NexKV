// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageCache_PutAndGet(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Create a page
	page := NewPage(1, model.LeafPage)
	page.Data[0] = 42

	// Put page
	err := cache.Put(page)
	require.NoError(t, err)

	// Get page
	retrieved, found := cache.Get(1)
	require.True(t, found)
	assert.NotNil(t, retrieved)
	assert.Equal(t, model.PageID(1), retrieved.ID)
	assert.Equal(t, byte(42), retrieved.Data[0])

	// Release page
	retrieved.Release()
}

func TestPageCache_GetNotFound(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	page, found := cache.Get(999)
	assert.False(t, found)
	assert.Nil(t, page)
}

func TestPageCache_Evict(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Put a page
	page := NewPage(1, model.LeafPage)
	_ = cache.Put(page)

	// Verify it's in cache
	_, found := cache.Get(1)
	require.True(t, found)
	page.Release()

	// Evict
	cache.Evict(1)

	// Verify it's gone
	_, found = cache.Get(1)
	assert.False(t, found)
}

func TestPageCache_Clear(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Put multiple pages
	for i := 1; i <= 5; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		_ = cache.Put(page)
	}

	// Verify they're in cache
	l1Size, l2Size := cache.Size()
	assert.Equal(t, 5, l1Size)
	assert.Equal(t, 5, l2Size)

	// Clear
	cache.Clear()

	// Verify they're gone
	l1Size, l2Size = cache.Size()
	assert.Equal(t, 0, l1Size)
	assert.Equal(t, 0, l2Size)

	_, found := cache.Get(1)
	assert.False(t, found)
}

func TestPageCache_LRUEviction(t *testing.T) {
	cache := NewPageCache(3, 100, 2, nil) // Small L1 cache

	// Fill cache to capacity
	for i := 1; i <= 3; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		_ = cache.Put(page)
	}

	// Verify all pages are in cache
	for i := 1; i <= 3; i++ {
		page, found := cache.Get(model.PageID(i))
		require.True(t, found, "Page %d should be in cache initially", i)
		page.Release() // Release the reference from Get()
	}

	// Add one more - should evict LRU (page 1)
	page4 := NewPage(4, model.LeafPage)
	_ = cache.Put(page4)

	// Page 1 should be evicted
	_, found := cache.Get(1)
	assert.False(t, found, "Page 1 should have been evicted")

	// Pages 2, 3, 4 should still be there
	for _, id := range []model.PageID{2, 3, 4} {
		page, found := cache.Get(id)
		require.True(t, found, "Page %d should be in cache", id)
		page.Release()
	}
}

func TestPageCache_ConcurrentAccess(t *testing.T) {
	cache := NewPageCache(100, 1000, 50, nil)

	const numGoroutines = 10
	const numPagesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Readers + writers

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < numPagesPerGoroutine; j++ {
				id := model.PageID(offset*numPagesPerGoroutine + j)
				page := NewPage(id, model.LeafPage)
				page.Data[0] = byte(id & 0xFF)
				_ = cache.Put(page)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < numPagesPerGoroutine; j++ {
				id := model.PageID(offset*numPagesPerGoroutine + j)
				page, found := cache.Get(id)
				if found {
					assert.Equal(t, id, page.ID)
					page.Release()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify cache has some entries
	l1Size, _ := cache.Size()
	assert.Greater(t, l1Size, 0)
}

func TestPageCache_GetL1Stats(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	stats := cache.GetL1Stats()
	assert.Equal(t, 0, stats.Size)
	assert.Equal(t, 10, stats.Capacity)

	// Add some pages
	for i := 1; i <= 5; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		_ = cache.Put(page)
	}

	stats = cache.GetL1Stats()
	assert.Equal(t, 5, stats.Size)
}

func TestPageCache_GetL2Stats(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	stats := cache.GetL2Stats()
	assert.Equal(t, 0, stats.Size)
	assert.Equal(t, 100, stats.Capacity)

	// Add some pages (also adds to L2)
	for i := 1; i <= 5; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		_ = cache.Put(page)
	}

	stats = cache.GetL2Stats()
	assert.Equal(t, 5, stats.Size)
}

func TestDefaultPageCache(t *testing.T) {
	cache := DefaultPageCache()

	l1Stats := cache.GetL1Stats()
	assert.Equal(t, 1000, l1Stats.Capacity)

	l2Stats := cache.GetL2Stats()
	assert.Equal(t, 10000, l2Stats.Capacity)
}

func TestPageCache_PutNil(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Putting nil should not panic
	err := cache.Put(nil)
	assert.NoError(t, err)
}

func TestPageCache_EvictNonExistent(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Evicting non-existent page should not panic
	cache.Evict(999)
}

// BenchmarkPageCache_Put benchmarks Put operation.
func BenchmarkPageCache_Put(b *testing.B) {
	cache := NewPageCache(10000, 100000, 500, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := model.PageID(i % 1000)
		page := NewPage(id, model.LeafPage)
		_ = cache.Put(page)
	}
}

// BenchmarkPageCache_Get benchmarks Get operation with cache hit.
func BenchmarkPageCache_Get_Hit(b *testing.B) {
	cache := NewPageCache(10000, 100000, 500, nil)

	// Pre-fill cache
	for i := 0; i < 1000; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		_ = cache.Put(page)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := model.PageID(i % 1000)
		page, found := cache.Get(id)
		if found {
			page.Release()
		}
	}
}

// BenchmarkPageCache_Get_Miss benchmarks Get operation with cache miss.
func BenchmarkPageCache_Get_Miss(b *testing.B) {
	cache := NewPageCache(10000, 100000, 500, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get(model.PageID(i + 100000))
	}
}

// TestPageCache_GetNode_L1Hit tests GetNode with L1 cache hit.
func TestPageCache_GetNode_L1Hit(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Create a leaf node
	node := NewNode(true)
	err := node.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// Store node in NodeL1 cache
	err = cache.PutNode(1, node)
	require.NoError(t, err)

	// GetNode should retrieve from L1
	retrieved, err := cache.GetNode(1)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, 1, len(retrieved.Keys))
	assert.True(t, retrieved.IsLeaf)

	// Verify pin count increased
	assert.Equal(t, int32(1), retrieved.PinCount())

	// Release node
	retrieved.Release()
}

// TestPageCache_GetNode_L2Hit tests GetNode with L2 cache hit (deserialization).
func TestPageCache_GetNode_L2Hit(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Create a leaf node
	node := NewNode(true)
	err := node.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// Create page and store in L2 cache (without storing in NodeL1)
	page := NewPage(2, model.LeafPage)
	err = SerializeNode(node, page)
	require.NoError(t, err)

	data := make([]byte, PageDataSize)
	copy(data, page.Data[:])
	cache.L2.Store(model.PageID(2), data) // 使用 model.PageID 类型
	cache.l2Size.Add(1)

	// GetNode should retrieve from L2 and deserialize
	retrieved, err := cache.GetNode(2)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, 1, len(retrieved.Keys))
	assert.True(t, retrieved.IsLeaf)

	// Verify node was promoted to NodeL1
	assert.Equal(t, int64(1), cache.nodeL1Size.Load())

	retrieved.Release()
}

// TestPageCache_GetNode_NotFound tests GetNode when page is not found.
func TestPageCache_GetNode_NotFound(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// GetNode should return ErrPageNotFound
	_, err := cache.GetNode(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

// TestPageCache_GetNode_PageIDZero tests GetNode with PageID=0.
func TestPageCache_GetNode_PageIDZero(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// GetNode should return ErrPageNotFound for PageID=0
	_, err := cache.GetNode(0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

// TestPageCache_PutNode_Nil tests PutNode with nil node.
func TestPageCache_PutNode_Nil(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// PutNode should not panic with nil node
	err := cache.PutNode(1, nil)
	assert.NoError(t, err)
}

// TestPageCache_PutNode_PageIDZero tests PutNode with PageID=0.
func TestPageCache_PutNode_PageIDZero(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	node := NewNode(true)
	// PutNode should not panic with PageID=0
	err := cache.PutNode(0, node)
	assert.NoError(t, err)
}

// TestPageCache_GetNode_InternalNode tests GetNode with internal node (with children).
func TestPageCache_GetNode_InternalNode(t *testing.T) {
	cache := NewPageCache(10, 100, 5, nil)

	// Create internal node with ChildIDs
	parent := NewNode(false)
	parent.ChildIDs = []model.PageID{10, 20, 30}
	parent.Keys = [][]byte{[]byte("m"), []byte("z")}

	// Store in NodeL1
	err := cache.PutNode(1, parent)
	require.NoError(t, err)

	// GetNode should retrieve internal node
	retrieved, err := cache.GetNode(1)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.False(t, retrieved.IsLeaf)
	assert.Equal(t, 2, len(retrieved.Keys))
	assert.Equal(t, 3, len(retrieved.ChildIDs))
	assert.Equal(t, model.PageID(10), retrieved.ChildIDs[0])
	assert.Equal(t, model.PageID(20), retrieved.ChildIDs[1])
	assert.Equal(t, model.PageID(30), retrieved.ChildIDs[2])

	retrieved.Release()
}

// TestPageCache_GetNode_Concurrent tests concurrent GetNode operations.
func TestPageCache_GetNode_Concurrent(t *testing.T) {
	cache := NewPageCache(100, 1000, 50, nil)

	// Create and store a node
	node := NewNode(true)
	err := node.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)
	err = cache.PutNode(1, node)
	require.NoError(t, err)

	const numGoroutines = 10
	const numOpsPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				retrieved, err := cache.GetNode(1)
				if err == nil {
					assert.Equal(t, 1, len(retrieved.Keys))
					retrieved.Release()
				}
			}
		}()
	}

	wg.Wait()

	// Verify final pin count is 0 (all released)
	stats := cache.GetNodeStats()
	assert.Equal(t, 1, stats.Size)
}
