// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// Global object pools for Page and Node reuse.
// Based on Phase 0.5 validation: Node pooling provides 14.9x performance improvement.

var (
	// pagePool manages Page objects for reuse.
	// Note: Page allocation is already very fast (5.77 ns/op with zero allocs),
	// so pooling may not provide significant benefit.
	pagePool = sync.Pool{
		New: func() any {
			return &Page{
				Data: [PageDataSize]byte{},
			}
		},
	}

	// nodePool manages Node objects for reuse.
	// Based on Phase 0.5 validation: Node pooling provides 14.9x performance improvement.
	nodePool = sync.Pool{
		New: func() any {
			return &Node{
				Keys:     make([][]byte, 0, model.DefaultMaxKeys),
				Values:   make([][]byte, 0, model.DefaultMaxKeys),
				Children: make([]*Node, 0, model.DefaultMaxKeys+1),
			}
		},
	}
)

// AcquirePage acquires a Page from the pool or creates a new one.
// The page is not initialized and should be configured before use.
// For better performance, use NewPage() when you know the page ID and type.
func AcquirePage() *Page {
	return pagePool.Get().(*Page)
}

// ReleasePage returns a Page to the pool for reuse.
// The page will be reset before being returned to the pool.
func ReleasePage(page *Page) {
	if page == nil {
		return
	}

	// Reset page state
	page.ID = 0
	page.Type = 0
	page.Version = 0
	page.RefCount.Store(0)
	page.dirty = false

	// Clear data (optional, for security)
	// page.Data = [PageDataSize]byte{}

	// Return to pool
	pagePool.Put(page)
}

// AcquireNode acquires a Node from the pool or creates a new one.
// The node is initialized as a leaf node with empty keys/values/children.
func AcquireNode() *Node {
	node := nodePool.Get().(*Node)

	// Reset node state
	node.IsLeaf = true

	// Clear slices (preserve capacity)
	node.Keys = node.Keys[:0]
	node.Values = node.Values[:0]
	node.Children = node.Children[:0]

	return node
}

// AcquireInternalNode acquires an internal Node from the pool.
// The node is initialized as an internal node with empty keys/children.
func AcquireInternalNode() *Node {
	node := AcquireNode()
	node.IsLeaf = false
	return node
}

// ReleaseNode returns a Node to the pool for reuse.
// The node will be reset before being returned to the pool.
func ReleaseNode(node *Node) {
	if node == nil {
		return
	}

	// Clear slices (preserve capacity for reuse)
	node.Keys = node.Keys[:0]
	node.Values = node.Values[:0]
	node.Children = node.Children[:0]

	// Return to pool
	nodePool.Put(node)
}

// PoolStats holds object pool statistics.
type PoolStats struct {
	// PageHits is the number of successful Page acquisitions from pool.
	PageHits int64

	// PageMisses is the number of Page allocations (pool misses).
	PageMisses int64

	// NodeHits is the number of successful Node acquisitions from pool.
	NodeHits int64

	// NodeMisses is the number of Node allocations (pool misses).
	NodeMisses int64
}

// GetPoolStats returns the current pool statistics.
// Note: sync.Pool does not expose hit/miss counters, so this returns placeholder data.
// In production, you would implement custom counters if needed.
func GetPoolStats() *PoolStats {
	return &PoolStats{
		PageHits:   0,
		PageMisses: 0,
		NodeHits:   0,
		NodeMisses: 0,
	}
}
