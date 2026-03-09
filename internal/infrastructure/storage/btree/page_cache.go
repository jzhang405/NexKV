// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageCache implements a three-tier cache for Page objects.
//
// Cache levels:
//   - L1: Hot data - Page objects (fully deserialized, ready to use)
//   - L2: Warm data - Serialized buffers ([]byte)
//   - L3: Cold data - On disk (future implementation)
//
// The cache uses a simple LRU eviction policy when capacity is reached.
type PageCache struct {
	// L1 cache stores PageID → *Page (hot data, deserialized)
	L1 *sync.Map

	// L2 cache stores PageID → []byte (warm data, serialized)
	L2 *sync.Map

	// l1Capacity is the maximum number of Pages in L1 cache.
	l1Capacity int

	// l2Capacity is the maximum number of buffers in L2 cache.
	l2Capacity int

	// l1Size tracks current L1 cache size.
	l1Size atomic.Int64

	// l2Size tracks current L2 cache size.
	l2Size atomic.Int64

	// evictionQueue tracks LRU order for eviction.
	evictionLock  sync.Mutex
	evictionQueue []model.PageID
}

// NewPageCache creates a new PageCache with the given capacities.
func NewPageCache(l1Capacity, l2Capacity int) *PageCache {
	return &PageCache{
		L1:            &sync.Map{},
		L2:            &sync.Map{},
		l1Capacity:    l1Capacity,
		l2Capacity:    l2Capacity,
		evictionQueue: make([]model.PageID, 0, l1Capacity+l2Capacity),
	}
}

// Get retrieves a Page from the cache.
// Returns the Page and a boolean indicating whether it was found in L1.
// If not in L1, checks L2 and deserializes.
func (c *PageCache) Get(id model.PageID) (*Page, bool) {
	// Check L1 first (hot data)
	if page, ok := c.L1.Load(id); ok {
		p := page.(*Page)
		p.Acquire() // Increment reference count
		c.trackAccess(id)
		return p, true
	}

	// Check L2 (warm data)
	if buf, ok := c.L2.Load(id); ok {
		data := buf.([]byte)

		// Create Page from buffer
		page := &Page{
			ID:   id,
			Data: [PageDataSize]byte{},
		}
		copy(page.Data[:], data)

		// Promote to L1
		if c.l1Size.Load() < int64(c.l1Capacity) {
			c.L1.Store(id, page)
			c.l1Size.Add(1)
		}

		page.Acquire()
		c.trackAccess(id)
		return page, true
	}

	return nil, false
}

// Put stores a Page in the cache (L1).
// If L1 is full, evicts the least recently used entry.
func (c *PageCache) Put(page *Page) error {
	if page == nil {
		return nil
	}

	id := page.ID

	// Check if page already exists in L1
	_, exists := c.L1.Load(id)

	// If not exists and cache is full, evict first
	if !exists && c.l1Size.Load() >= int64(c.l1Capacity) {
		c.evictLRU()
	}

	// Store in L1
	c.L1.Store(id, page)
	if !exists {
		c.l1Size.Add(1)
	}

	// Also store serialized version in L2
	data := make([]byte, PageDataSize)
	copy(data, page.Data[:])

	_, l2Exists := c.L2.Load(id)
	c.L2.Store(id, data)
	if !l2Exists {
		c.l2Size.Add(1)
	}

	c.trackAccess(id)
	return nil
}

// Evict removes a Page from all cache levels.
func (c *PageCache) Evict(id model.PageID) {
	// Remove from L1
	if page, ok := c.L1.LoadAndDelete(id); ok {
		p := page.(*Page)
		p.Release()
		c.l1Size.Add(-1)
	}

	// Remove from L2
	if _, ok := c.L2.LoadAndDelete(id); ok {
		c.l2Size.Add(-1)
	}

	// Remove from eviction queue
	c.evictionLock.Lock()
	defer c.evictionLock.Unlock()
	for i, pid := range c.evictionQueue {
		if pid == id {
			c.evictionQueue = append(c.evictionQueue[:i], c.evictionQueue[i+1:]...)
			break
		}
	}
}

// Clear removes all entries from the cache.
func (c *PageCache) Clear() {
	c.L1.Range(func(key, value any) bool {
		page := value.(*Page)
		page.Release()
		c.L1.Delete(key)
		return true
	})

	c.L2.Range(func(key, value any) bool {
		c.L2.Delete(key)
		return true
	})

	c.l1Size.Store(0)
	c.l2Size.Store(0)

	c.evictionLock.Lock()
	c.evictionQueue = c.evictionQueue[:0]
	c.evictionLock.Unlock()
}

// Size returns the total number of entries in the cache.
func (c *PageCache) Size() (l1, l2 int) {
	return int(c.l1Size.Load()), int(c.l2Size.Load())
}

// trackAccess tracks page access for LRU eviction.
func (c *PageCache) trackAccess(id model.PageID) {
	c.evictionLock.Lock()
	defer c.evictionLock.Unlock()

	// Remove from current position
	for i, pid := range c.evictionQueue {
		if pid == id {
			c.evictionQueue = append(c.evictionQueue[:i], c.evictionQueue[i+1:]...)
			break
		}
	}

	// Add to end (most recently used)
	c.evictionQueue = append(c.evictionQueue, id)
}

// evictLRU evicts the least recently used entry from L1 and L2.
func (c *PageCache) evictLRU() {
	c.evictionLock.Lock()
	defer c.evictionLock.Unlock()

	if len(c.evictionQueue) == 0 {
		return
	}

	// Get LRU entry (first in queue)
	lruID := c.evictionQueue[0]
	c.evictionQueue = c.evictionQueue[1:]

	// Evict from L1
	if page, ok := c.L1.LoadAndDelete(lruID); ok {
		p := page.(*Page)
		p.Release()
		c.l1Size.Add(-1)
	}

	// Also evict from L2 to prevent Get from recreating it in L1
	if _, ok := c.L2.LoadAndDelete(lruID); ok {
		c.l2Size.Add(-1)
	}
}

// GetL1Stats returns statistics about L1 cache.
func (c *PageCache) GetL1Stats() CacheStats {
	size := c.l1Size.Load()
	return CacheStats{
		Size:     int(size),
		Capacity: c.l1Capacity,
		HitRate:  c.calculateHitRate(),
	}
}

// GetL2Stats returns statistics about L2 cache.
func (c *PageCache) GetL2Stats() CacheStats {
	size := c.l2Size.Load()
	return CacheStats{
		Size:     int(size),
		Capacity: c.l2Capacity,
	}
}

// calculateHitRate calculates the cache hit rate.
// Placeholder for future implementation with hit/miss counters.
func (c *PageCache) calculateHitRate() float64 {
	return 0.0 // TODO: Implement hit/miss tracking
}

// CacheStats holds cache statistics.
type CacheStats struct {
	Size     int
	Capacity int
	HitRate  float64
}

// DefaultPageCache returns a PageCache with default capacities.
func DefaultPageCache() *PageCache {
	return NewPageCache(1000, 10000) // L1: 1000 pages, L2: 10000 buffers
}
