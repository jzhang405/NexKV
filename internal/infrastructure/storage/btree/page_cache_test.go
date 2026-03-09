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
	cache := NewPageCache(10, 100)

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
	cache := NewPageCache(10, 100)

	page, found := cache.Get(999)
	assert.False(t, found)
	assert.Nil(t, page)
}

func TestPageCache_Evict(t *testing.T) {
	cache := NewPageCache(10, 100)

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
	cache := NewPageCache(10, 100)

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
	cache := NewPageCache(3, 100) // Small L1 cache

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
	cache := NewPageCache(100, 1000)

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
	cache := NewPageCache(10, 100)

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
	cache := NewPageCache(10, 100)

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
	cache := NewPageCache(10, 100)

	// Putting nil should not panic
	err := cache.Put(nil)
	assert.NoError(t, err)
}

func TestPageCache_EvictNonExistent(t *testing.T) {
	cache := NewPageCache(10, 100)

	// Evicting non-existent page should not panic
	cache.Evict(999)
}

// BenchmarkPageCache_Put benchmarks Put operation.
func BenchmarkPageCache_Put(b *testing.B) {
	cache := NewPageCache(10000, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := model.PageID(i % 1000)
		page := NewPage(id, model.LeafPage)
		_ = cache.Put(page)
	}
}

// BenchmarkPageCache_Get benchmarks Get operation with cache hit.
func BenchmarkPageCache_Get_Hit(b *testing.B) {
	cache := NewPageCache(10000, 100000)

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
	cache := NewPageCache(10000, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get(model.PageID(i + 100000))
	}
}
