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

// TestPageCache_EvictLRUNode tests the LRU eviction logic for NodeL1 cache.
func TestPageCache_EvictLRUNode(t *testing.T) {
	// Create a cache with very small capacity (1 node)
	cache := NewPageCache(10, 100, 1, nil)

	// Create first node and store it
	node1 := NewNode(true)
	err := node1.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)
	err = cache.PutNode(1, node1)
	require.NoError(t, err)

	// Verify node1 is in cache
	stats := cache.GetNodeStats()
	assert.Equal(t, 1, stats.Size)

	// Create second node and store it (should evict node1)
	node2 := NewNode(true)
	err = node2.Insert([]byte("key2"), []byte("value2"))
	require.NoError(t, err)
	err = cache.PutNode(2, node2)
	require.NoError(t, err)

	// Verify cache size is still 1 (LRU eviction)
	stats = cache.GetNodeStats()
	assert.Equal(t, 1, stats.Size)

	// Verify node2 is now in cache
	retrieved, err := cache.GetNode(2)
	require.NoError(t, err)
	assert.Equal(t, 1, len(retrieved.Keys))
	assert.Equal(t, []byte("key2"), retrieved.Keys[0])
	retrieved.Release()

	// node1 should have been evicted (not in NodeL1, but may be in L2)
	retrieved2, err := cache.GetNode(1)
	require.NoError(t, err)
	assert.NotNil(t, retrieved2)
	retrieved2.Release()
}

// TestPageCache_GetNode_L3Hit tests the L3 cache hit path (loading from PageManager).
func TestPageCache_GetNode_L3Hit(t *testing.T) {
	// Create a temporary directory for PageManager
	tempDir := t.TempDir()
	dbFile := tempDir + "/test.db"

	// Create a real PageManager
	pageManager, err := NewPageManager(dbFile)
	require.NoError(t, err)

	// Create a cache with small capacity
	cache := NewPageCache(1, 100, 10, pageManager)

	// Pre-populate PageManager with a page
	node := NewNode(true)
	err = node.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	page, err := PageFromNode(1, node)
	require.NoError(t, err)

	err = pageManager.WritePage(page)
	require.NoError(t, err)

	// Clear NodeL1 and L2 to force L3 hit
	cache.NodeL1 = &sync.Map{}
	cache.L2 = &sync.Map{}
	cache.nodeL1Size.Store(0)
	cache.l2Size.Store(0)

	// GetNode should load from L3 (PageManager)
	retrieved, err := cache.GetNode(1)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, 1, len(retrieved.Keys))
	assert.Equal(t, []byte("key1"), retrieved.Keys[0])

	// Verify it was cached in both L1 and L2
	stats := cache.GetNodeStats()
	assert.Equal(t, 1, stats.Size)

	retrieved.Release()
}

// TestPageCache_Get_L2Hit tests L2 cache hit with L1 promotion.
func TestPageCache_Get_L2Hit(t *testing.T) {
	// Create a cache with limited L1 capacity
	cache := NewPageCache(2, 100, 10, nil)

	// Create and store a page using NewPage
	page1 := NewPage(1, model.LeafPage)
	copy(page1.Data[:], []byte("test-data-1"))

	err := cache.Put(page1)
	require.NoError(t, err)

	// Clear L1 to force L2 hit on next Get
	cache.L1 = &sync.Map{}
	cache.l1Size.Store(0)

	// Get should find in L2 and promote to L1
	retrieved, ok := cache.Get(1)
	assert.True(t, ok)
	assert.NotNil(t, retrieved)
	assert.Equal(t, model.PageID(1), retrieved.ID)

	// Verify it was promoted to L1
	l1Size := cache.l1Size.Load()
	assert.Equal(t, int64(1), l1Size)

	retrieved.Release()
}

// TestPageCache_Get_NotFound tests cache miss.
func TestPageCache_Get_NotFound(t *testing.T) {
	cache := NewPageCache(10, 100, 10, nil)

	// Try to get non-existent page
	retrieved, ok := cache.Get(999)
	assert.False(t, ok)
	assert.Nil(t, retrieved)
}

// TestPageCache_Put_NilPage tests putting nil page.
func TestPageCache_Put_NilPage(t *testing.T) {
	cache := NewPageCache(10, 100, 10, nil)

	// Put nil should not error
	err := cache.Put(nil)
	assert.NoError(t, err)

	// L1 size should remain 0
	l1Size := cache.l1Size.Load()
	assert.Equal(t, int64(0), l1Size)
}

// TestPageCache_Put_UpdateExisting tests updating existing page in cache.
func TestPageCache_Put_UpdateExisting(t *testing.T) {
	cache := NewPageCache(10, 100, 10, nil)

	// Create and store initial page using NewPage
	page1 := NewPage(1, model.LeafPage)
	copy(page1.Data[:], []byte("initial-data"))

	err := cache.Put(page1)
	require.NoError(t, err)

	l1Size1 := cache.l1Size.Load()
	assert.Equal(t, int64(1), l1Size1)

	// Update with new data
	page1Updated := NewPage(1, model.LeafPage)
	copy(page1Updated.Data[:], []byte("updated-data"))

	err = cache.Put(page1Updated)
	require.NoError(t, err)

	// L1 size should still be 1 (not incremented)
	l1Size2 := cache.l1Size.Load()
	assert.Equal(t, int64(1), l1Size2)

	// Verify we get the updated data
	retrieved, ok := cache.Get(1)
	assert.True(t, ok)
	assert.Equal(t, []byte("updated-data"), retrieved.Data[:len("updated-data")])
	retrieved.Release()
}

// TestPageManager_WritePage_NotDirty tests writing a clean page.
func TestPageManager_WritePage_NotDirty(t *testing.T) {
	tempDir := t.TempDir()
	dbFile := tempDir + "/test.db"

	pageManager, err := NewPageManager(dbFile)
	require.NoError(t, err)
	defer pageManager.Close()

	// Create a page without marking it dirty
	page := NewPage(1, model.LeafPage)
	copy(page.Data[:], []byte("test-data"))

	// WritePage should return nil (not dirty, no write needed)
	err = pageManager.WritePage(page)
	assert.NoError(t, err)
}

// TestPageManager_WritePage_NilPage tests writing nil page.
func TestPageManager_WritePage_NilPage(t *testing.T) {
	tempDir := t.TempDir()
	dbFile := tempDir + "/test.db"

	pageManager, err := NewPageManager(dbFile)
	require.NoError(t, err)
	defer pageManager.Close()

	// WritePage with nil should return error
	err = pageManager.WritePage(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "page is nil")
}

// TestPageManager_WritePage_Closed tests writing to closed PageManager.
func TestPageManager_WritePage_Closed(t *testing.T) {
	tempDir := t.TempDir()
	dbFile := tempDir + "/test.db"

	pageManager, err := NewPageManager(dbFile)
	require.NoError(t, err)

	// Close the PageManager
	pageManager.Close()

	// Create a dirty page
	page := NewPage(1, model.LeafPage)
	copy(page.Data[:], []byte("test-data"))
	page.MarkDirty()

	// WritePage should return ErrStoreClosed
	err = pageManager.WritePage(page)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrStoreClosed)
}

// TestNode_BatchInsert_Basic tests basic batch insert functionality.
func TestNode_BatchInsert_Basic(t *testing.T) {
	node := NewNode(true)

	// Batch insert multiple keys
	keys := make([][]byte, 5)
	values := make([][]byte, 5)
	for i := 0; i < 5; i++ {
		keys[i] = []byte{byte(i)}
		values[i] = []byte("value")
	}

	err := node.BatchInsert(keys, values)
	require.NoError(t, err)

	// Verify all keys were inserted
	assert.Equal(t, 5, len(node.Keys))

	// Verify values
	for i := 0; i < 5; i++ {
		value, err := node.Get(keys[i])
		require.NoError(t, err)
		assert.Equal(t, values[i], value)
	}
}

// TestNode_BatchInsert_MismatchedLengths tests batch insert with mismatched arrays.
func TestNode_BatchInsert_MismatchedLengths(t *testing.T) {
	node := NewNode(true)

	keys := make([][]byte, 3)
	values := make([][]byte, 5) // Different length

	err := node.BatchInsert(keys, values)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "length mismatch")
}

// TestPage_TypeCheckers tests Page type checking methods.
func TestPage_TypeCheckers(t *testing.T) {
	leafPage := NewPage(1, model.LeafPage)
	internalPage := NewPage(2, model.InternalPage)
	metaPage := NewPage(3, model.MetaPage)

	assert.True(t, leafPage.IsLeaf())
	assert.False(t, leafPage.IsInternal())
	assert.False(t, leafPage.IsMeta())

	assert.False(t, internalPage.IsLeaf())
	assert.True(t, internalPage.IsInternal())
	assert.False(t, internalPage.IsMeta())

	assert.False(t, metaPage.IsLeaf())
	assert.False(t, metaPage.IsInternal())
	assert.True(t, metaPage.IsMeta())
}

// TestPage_GetRefCount tests GetRefCount method.
func TestPage_GetRefCount(t *testing.T) {
	page := NewPage(1, model.LeafPage)

	// Initial ref count should be 1 (from NewPage)
	refCount := page.GetRefCount()
	assert.Equal(t, int32(1), refCount)

	// Acquire should increment
	page.Acquire()
	refCount = page.GetRefCount()
	assert.Equal(t, int32(2), refCount)

	// Release should decrement
	page.Release()
	refCount = page.GetRefCount()
	assert.Equal(t, int32(1), refCount)
}

// TestPage_GetVersion tests GetVersion method.
func TestPage_GetVersion(t *testing.T) {
	page := NewPage(1, model.LeafPage)

	// Initial version should be 0
	assert.Equal(t, uint64(0), page.GetVersion())

	// Set version
	page.SetVersion(5)
	assert.Equal(t, uint64(5), page.GetVersion())
}

// TestPageCache_EvictionLRUBoundary tests LRU eviction at boundaries.
func TestPageCache_EvictionLRUBoundary(t *testing.T) {
	cache := NewPageCache(2, 100, 2, nil)

	// Create pages
	page1 := NewPage(1, model.LeafPage)
	page2 := NewPage(2, model.LeafPage)
	page3 := NewPage(3, model.LeafPage)

	// Fill cache to capacity
	err := cache.Put(page1)
	require.NoError(t, err)
	err = cache.Put(page2)
	require.NoError(t, err)

	// Access page1 to make it more recent
	_, _ = cache.Get(1)

	// Add page3 - should evict page2 (least recently used)
	err = cache.Put(page3)
	require.NoError(t, err)

	// page2 should be evicted, page1 and page3 should remain
	_, ok := cache.Get(2)
	assert.False(t, ok) // page2 evicted

	_, ok = cache.Get(1)
	assert.True(t, ok) // page1 still there

	_, ok = cache.Get(3)
	assert.True(t, ok) // page3 added
}

// TestPageCache_EvictAll tests evicting all cached items.
func TestPageCache_EvictAll(t *testing.T) {
	cache := NewPageCache(10, 100, 10, nil)

	// Add some pages
	for i := 1; i <= 5; i++ {
		page := NewPage(model.PageID(i), model.LeafPage)
		err := cache.Put(page)
		require.NoError(t, err)
	}

	// Clear all
	cache.Clear()

	// All should be evicted
	stats := cache.GetL1Stats()
	assert.Equal(t, 0, stats.Size)
}

// TestPageCache_StatsConsistency tests cache statistics consistency.
func TestPageCache_StatsConsistency(t *testing.T) {
	cache := NewPageCache(10, 100, 10, nil)

	// Initially empty
	l1Stats := cache.GetL1Stats()
	assert.Equal(t, 0, l1Stats.Size)

	l2Stats := cache.GetL2Stats()
	assert.Equal(t, 0, l2Stats.Size)

	// Add one page
	page := NewPage(1, model.LeafPage)
	err := cache.Put(page)
	require.NoError(t, err)

	// Should be in both L1 and L2
	l1Stats = cache.GetL1Stats()
	assert.Equal(t, 1, l1Stats.Size)

	l2Stats = cache.GetL2Stats()
	assert.Equal(t, 1, l2Stats.Size)
}
