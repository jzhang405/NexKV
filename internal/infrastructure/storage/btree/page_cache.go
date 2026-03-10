// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageCache implements a three-tier cache for Page and Node objects.
//
// Cache levels:
//   - L1: Hot data - Page objects (fully deserialized, ready to use)
//   - L2: Warm data - Serialized buffers ([]byte)
//   - L3: Cold data - On disk (via PageManager)
//
// The cache uses a simple LRU eviction policy when capacity is reached.
type PageCache struct {
	// L1 cache stores PageID → *Page (hot data, deserialized)
	L1 *sync.Map

	// L2 cache stores PageID → []byte (warm data, serialized)
	L2 *sync.Map

	// NodeL1 cache stores PageID → *Node for lazy loading
	NodeL1 *sync.Map

	// l1Capacity is the maximum number of Pages in L1 cache.
	l1Capacity int

	// l2Capacity is the maximum number of buffers in L2 cache.
	l2Capacity int

	// nodeL1Capacity is the maximum number of Nodes in NodeL1 cache.
	nodeL1Capacity int

	// l1Size tracks current L1 cache size.
	l1Size atomic.Int64

	// l2Size tracks current L2 cache size.
	l2Size atomic.Int64

	// nodeL1Size tracks current NodeL1 cache size.
	nodeL1Size atomic.Int64

	// evictionQueue tracks LRU order for eviction.
	evictionLock  sync.Mutex
	evictionQueue []model.PageID

	// pageManager is the optional L3 storage (can be nil for in-memory mode).
	pageManager *PageManager
}

// NewPageCache creates a new PageCache with the given capacities.
// Optionally accepts a PageManager for L3 storage (can be nil for in-memory mode).
func NewPageCache(l1Capacity, l2Capacity, nodeL1Capacity int, pageManager *PageManager) *PageCache {
	return &PageCache{
		L1:             &sync.Map{},
		L2:             &sync.Map{},
		NodeL1:         &sync.Map{},
		l1Capacity:     l1Capacity,
		l2Capacity:     l2Capacity,
		nodeL1Capacity: nodeL1Capacity,
		evictionQueue:  make([]model.PageID, 0, l1Capacity+l2Capacity+nodeL1Capacity),
		pageManager:    pageManager,
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
	return NewPageCache(1000, 10000, 500, nil) // L1: 1000 pages, L2: 10000 buffers, NodeL1: 500 nodes
}

// DefaultPageCacheWithPersistence returns a PageCache with default capacities and L3 storage.
func DefaultPageCacheWithPersistence(pageManager *PageManager) *PageCache {
	return NewPageCache(1000, 10000, 500, pageManager) // L1: 1000 pages, L2: 10000 buffers, NodeL1: 500 nodes
}

// GetNode retrieves or loads a Node by PageID (lazy loading).
// Implements three-tier caching:
//   - L1 (NodeL1): Hot data - deserialized Node objects
//   - L2 (L2): Warm data - serialized bytes
//   - L3 (pageManager): Cold data - on-disk Pages
//
// Returns ErrPageNotFound if the page cannot be found in any cache level.
func (c *PageCache) GetNode(pageID model.PageID) (*Node, error) {
	if pageID == 0 {
		return nil, ErrPageNotFound
	}

	// L1: Check for already deserialized Node (hot data)
	if node, ok := c.NodeL1.Load(pageID); ok {
		n := node.(*Node)
		n.Acquire()
		c.trackAccess(pageID)
		return n, nil
	}

	// L2: Check for serialized bytes (warm data)
	if data, ok := c.L2.Load(pageID); ok {
		node, err := c.deserializeNode(data.([]byte))
		if err != nil {
			return nil, err
		}

		// Promote to NodeL1
		if c.nodeL1Size.Load() < int64(c.nodeL1Capacity) {
			c.NodeL1.Store(pageID, node)
			c.nodeL1Size.Add(1)
		}

		node.Acquire()
		c.trackAccess(pageID)
		return node, nil
	}

	// L3: Read from PageManager (cold data)
	if c.pageManager != nil {
		page, err := c.pageManager.ReadPage(pageID)
		if err != nil {
			return nil, err
		}

		// Deserialize Page to Node
		node, err := DeserializeNode(page)
		if err != nil {
			return nil, err
		}

		// Cache in L2 (serialized) and NodeL1 (deserialized)
		data := make([]byte, PageDataSize)
		copy(data, page.Data[:])

		c.L2.Store(pageID, data)
		c.l2Size.Add(1)

		if c.nodeL1Size.Load() < int64(c.nodeL1Capacity) {
			c.NodeL1.Store(pageID, node)
			c.nodeL1Size.Add(1)
		}

		node.Acquire()
		c.trackAccess(pageID)
		return node, nil
	}

	return nil, ErrPageNotFound
}

// PutNode stores a Node in the NodeL1 cache.
// Also creates a serialized version in L2.
func (c *PageCache) PutNode(pageID model.PageID, node *Node) error {
	if node == nil || pageID == 0 {
		return nil
	}

	// Check if node already exists in NodeL1
	_, exists := c.NodeL1.Load(pageID)

	// If not exists and cache is full, evict first
	if !exists && c.nodeL1Size.Load() >= int64(c.nodeL1Capacity) {
		c.evictLRUNode()
	}

	// Store in NodeL1
	c.NodeL1.Store(pageID, node)
	if !exists {
		c.nodeL1Size.Add(1)
	}

	// Also store serialized version in L2
	page, err := PageFromNode(pageID, node)
	if err != nil {
		return err
	}

	data := make([]byte, PageDataSize)
	copy(data, page.Data[:])

	_, l2Exists := c.L2.Load(pageID)
	c.L2.Store(pageID, data)
	if !l2Exists {
		c.l2Size.Add(1)
	}

	c.trackAccess(pageID)
	return nil
}

// deserializeNode deserializes a Node from byte data.
func (c *PageCache) deserializeNode(data []byte) (*Node, error) {
	if len(data) < PageDataSize {
		return nil, ErrBufferTooSmall
	}

	page := &Page{
		Data: [PageDataSize]byte{},
	}
	copy(page.Data[:], data)

	return DeserializeNode(page)
}

// evictLRUNode evicts the least recently used Node from NodeL1.
func (c *PageCache) evictLRUNode() {
	c.evictionLock.Lock()
	defer c.evictionLock.Unlock()

	if len(c.evictionQueue) == 0 {
		return
	}

	// Get LRU entry (first in queue)
	lruID := c.evictionQueue[0]
	c.evictionQueue = c.evictionQueue[1:]

	// Evict from NodeL1
	if node, ok := c.NodeL1.LoadAndDelete(lruID); ok {
		n := node.(*Node)
		n.Release()
		c.nodeL1Size.Add(-1)
	}

	// Note: We don't evict from L2 here as it may still be useful
}

// GetNodeStats returns statistics about NodeL1 cache.
func (c *PageCache) GetNodeStats() CacheStats {
	size := c.nodeL1Size.Load()
	return CacheStats{
		Size:     int(size),
		Capacity: c.nodeL1Capacity,
	}
}
