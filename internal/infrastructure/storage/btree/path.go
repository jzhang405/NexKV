// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PathNode represents a node in the path from root to leaf.
type PathNode struct {
	Node   *Node       // The deserialized node at this level (cached to avoid repeated deserialization)
	PageID model.PageID // The page ID of this node
	Level  int         // The level in the tree (0 = leaf)
}

// Path represents a path from root to leaf.
type Path []*PathNode

// pathPool is a sync.Pool for Path objects.
var pathPool = sync.Pool{
	New: func() any {
		// Pre-allocate capacity for typical BTree depth
		return make(Path, 0, 10)
	},
}

// AcquirePath gets a Path from the pool or creates a new one.
func AcquirePath() Path {
	return pathPool.Get().(Path)
}

// ReleasePath returns a Path to the pool for reuse.
func ReleasePath(path Path) {
	// Reset path to zero length but keep capacity
	path = path[:0]
	pathPool.Put(path)
}

// nodeCache is a simple cache for deserialized nodes.
// This is a placeholder for future optimization.
type nodeCache struct {
	cache map[model.PageID]*Node
	mu    sync.RWMutex
}

// newNodeCache creates a new node cache.
func newNodeCache() *nodeCache {
	return &nodeCache{
		cache: make(map[model.PageID]*Node),
	}
}

// Get retrieves a node from cache or deserializes it.
func (nc *nodeCache) Get(pageID model.PageID, page *Page, deserializeFunc func(*Page) *Node) *Node {
	nc.mu.RLock()
	node, ok := nc.cache[pageID]
	nc.mu.RUnlock()

	if ok {
		return node
	}

	// Not in cache, deserialize and cache it
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Double-check after acquiring write lock
	if node, ok := nc.cache[pageID]; ok {
		return node
	}

	node = deserializeFunc(page)
	nc.cache[pageID] = node
	return node
}

// Invalidate removes a node from cache (used during CCOW operations).
func (nc *nodeCache) Invalidate(pageID model.PageID) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	delete(nc.cache, pageID)
}

// InvalidateAll clears all cached nodes.
func (nc *nodeCache) InvalidateAll() {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	nc.cache = make(map[model.PageID]*Node)
}

// FindPath finds the path from root to leaf for the given key.
// This is a read-only operation and does not require locking.
// Optimized with path pooling to reduce allocations.
func (b *BTree) FindPath(key []byte) (Path, error) {
	// Use path pool to reduce allocations
	path := AcquirePath()
	defer ReleasePath(path)

	// Start from current root
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	currentPageID := rootInfo.RootID
	currentLevel := b.maxLevels

	for currentLevel > 0 {
		currentLevel--

		// Get the page for this level
		page, err := b.pageManager.Get(currentPageID)
		if err != nil {
			return nil, fmt.Errorf("failed to get page %d: %w", currentPageID, err)
		}
		defer b.pageManager.Release(page)

		// Deserialize node from page (cached)
		node := b.nodeCache.Get(currentPageID, page, b.deserializeNode)

		// Add to path with cached node
		pathNode := &PathNode{
			Node:   node, // Cache the deserialized node for reuse in CopyPathBottomUp
			PageID: currentPageID,
			Level:  currentLevel,
		}
		path = append(path, pathNode)

		// If this is a leaf node, we're done
		if node.IsLeaf {
			break
		}

		// Find the child to descend to
		idx := node.Search(key)
		if idx >= len(node.Children) {
			// Key is greater than all keys, go to rightmost child
			currentPageID = node.Children[len(node.Children)-1]
		} else {
			currentPageID = node.Children[idx]
		}
	}

	return path, nil
}

// CopyPathBottomUp copies the path from bottom to top (leaf to root).
// Returns the new root page ID after the copy-on-write operation.
// Optimized to reuse cached nodes and avoid unnecessary Get/Release cycles.
func (b *BTree) CopyPathBottomUp(ctx context.Context, path Path, modifyFunc func(*Node) error) (model.PageID, error) {
	if len(path) == 0 {
		return 0, fmt.Errorf("empty path")
	}

	// Save old page IDs before they get modified
	oldPageIDs := make([]model.PageID, len(path))
	for i, pathNode := range path {
		oldPageIDs[i] = pathNode.PageID
	}

	// Track pages to release at the end
	pagesToRelease := make([]*Page, 0, len(path))

	// Process from leaf to root
	for i := len(path) - 1; i >= 0; i-- {
		pathNode := path[i]

		// Copy the page (optimized version returns pointer directly)
		newPage, err := b.copyPageOptimized(pathNode.PageID)
		if err != nil {
			// Clean up already allocated pages
			for _, p := range pagesToRelease {
				b.pageManager.Release(p)
			}
			return 0, fmt.Errorf("failed to copy page %d: %w", pathNode.PageID, err)
		}

		// Track for cleanup
		pagesToRelease = append(pagesToRelease, newPage)

		// Reuse cached node data instead of deserializing from new page
		// Use AcquireNode to get a pre-allocated node from pool, then copy data from cached node
		newNode := AcquireNode()
		sourceNode := pathNode.Node

		// Copy node properties
		newNode.Page = newPage
		newNode.IsLeaf = sourceNode.IsLeaf

		// Copy Keys
		if len(sourceNode.Keys) > 0 {
			newNode.Keys = append(newNode.Keys[:0], sourceNode.Keys...)
		}

		// Copy Values or Children based on node type
		if sourceNode.IsLeaf {
			if len(sourceNode.Values) > 0 {
				newNode.Values = append(newNode.Values[:0], sourceNode.Values...)
			}
		} else {
			if len(sourceNode.Children) > 0 {
				newNode.Children = append(newNode.Children[:0], sourceNode.Children...)
			}
		}

		if i == len(path)-1 {
			// This is the leaf node, apply modification
			if err := modifyFunc(newNode); err != nil {
				ReleaseNode(newNode)
				return 0, fmt.Errorf("failed to modify leaf node: %w", err)
			}
		} else {
			// This is an internal node, update child reference
			// The child was already copied in the previous iteration.
			// We need to update the parent's Children array to point from the child's old page ID
			// (which is stored in the parent's Children array) to the child's new page ID
			// (which was updated in the previous iteration and is in path[i+1].PageID).
			if err := b.updateChildReference(newNode, oldPageIDs[i+1], path[i+1].PageID); err != nil {
				ReleaseNode(newNode)
				return 0, fmt.Errorf("failed to update child reference: %w", err)
			}
		}

		// Serialize node back to page
		if err := b.serializeNodeToPage(newNode, newPage); err != nil {
			ReleaseNode(newNode)
			return 0, fmt.Errorf("failed to serialize node: %w", err)
		}

		// Release node back to pool
		ReleaseNode(newNode)

		// Mark page as dirty
		newPage.MarkDirty()

		// Invalidate old page from cache (CCOW creates new version)
		b.nodeCache.Invalidate(oldPageIDs[i])

		// Store new page ID for next iteration
		path[i].PageID = newPage.ID
	}

	// Release all pages at the end
	for _, page := range pagesToRelease {
		b.pageManager.Release(page)
	}

	// Return the new root page ID
	return path[0].PageID, nil
}

// copyPage creates a copy of the given page and returns the PageID.
func (b *BTree) copyPage(pageID model.PageID) (model.PageID, error) {
	// Get the original page
	page, err := b.pageManager.Get(pageID)
	if err != nil {
		return 0, err
	}
	defer b.pageManager.Release(page)

	// Allocate a new page
	newPage, err := b.pageManager.Allocate()
	if err != nil {
		return 0, err
	}
	defer b.pageManager.Release(newPage)

	// Copy data from old page to new page
	copy(newPage.Data[:], page.Data[:])

	// Copy metadata
	newPage.Type = page.Type
	newPage.Version = page.Version + 1
	newPage.MarkDirty()

	return newPage.ID, nil
}

// copyPageOptimized creates a copy of the given page and returns the Page pointer directly.
// This avoids an extra Get/Release cycle. The caller is responsible for releasing the returned page.
func (b *BTree) copyPageOptimized(pageID model.PageID) (*Page, error) {
	// Get the original page
	oldPage, err := b.pageManager.Get(pageID)
	if err != nil {
		return nil, err
	}
	defer b.pageManager.Release(oldPage)

	// Allocate a new page
	newPage, err := b.pageManager.Allocate()
	if err != nil {
		return nil, err
	}

	// Copy data from old page to new page
	copy(newPage.Data[:], oldPage.Data[:])

	// Copy metadata
	newPage.Type = oldPage.Type
	newPage.Version = oldPage.Version + 1
	newPage.MarkDirty()

	// Return the page pointer without releasing
	// Caller is responsible for releasing the page
	return newPage, nil
}

// updateChildReference updates the child reference in the parent node.
// It finds the child with oldPageID and updates it to newPageID.
func (b *BTree) updateChildReference(parentNode *Node, oldPageID, newPageID model.PageID) error {
	// Find the index to update
	// We need to find the child that points to the old page ID
	for i, childID := range parentNode.Children {
		if childID == oldPageID {
			// Update the child reference to point to the new page
			parentNode.Children[i] = newPageID
			return nil
		}
	}

	return fmt.Errorf("child reference not found: oldPageID=%d", oldPageID)
}

// serializeNodeToPage serializes a node to a page.
func (b *BTree) serializeNodeToPage(node *Node, page *Page) error {
	// This is a placeholder - will be implemented with serializer
	// For now, just mark the page as dirty
	page.MarkDirty()
	return nil
}

// deserializeNode deserializes a node from a page.
// Optimized to use node pool to reduce allocations and GC pressure.
func (b *BTree) deserializeNode(page *Page) *Node {
	// This is a placeholder - will be implemented with serializer
	// For now, use pooled node to reduce GC pressure
	node := AcquireNode()
	node.IsLeaf = (page.Type == model.LeafPage)
	return node
}

// ModifyPage modifies a page with the given key-value pair.
// This is used by the CopyPathBottomUp operation.
// Optimized to use node pool and properly release nodes.
func (b *BTree) ModifyPage(page *Page, key, value []byte, op ModifyOperation) error {
	node := b.deserializeNode(page)

	switch op {
	case ModifyInsert:
		if err := node.Insert(key, value); err != nil {
			return err
		}
	case ModifyUpdate:
		idx := node.Search(key)
		if idx < len(node.Keys) && string(node.Keys[idx]) == string(key) {
			node.Values[idx] = value
		} else {
			return fmt.Errorf("key not found")
		}
	case ModifyDelete:
		if err := node.Delete(key); err != nil {
			return err
		}
	}

	// Serialize before releasing node
	err := b.serializeNodeToPage(node, page)

	// Release node back to pool for reuse
	ReleaseNode(node)

	return err
}

// ModifyOperation represents the type of modification.
type ModifyOperation int

const (
	ModifyInsert ModifyOperation = iota
	ModifyUpdate
	ModifyDelete
)
