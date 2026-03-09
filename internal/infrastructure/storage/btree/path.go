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
	Node   *Node       // The node at this level
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

		// Add to path
		pathNode := &PathNode{
			Node:   node,
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
func (b *BTree) CopyPathBottomUp(ctx context.Context, path Path, modifyFunc func(*Node) error) (model.PageID, error) {
	if len(path) == 0 {
		return 0, fmt.Errorf("empty path")
	}

	// Process from leaf to root
	for i := len(path) - 1; i >= 0; i-- {
		pathNode := path[i]

		// Copy the page
		newPageID, err := b.copyPage(pathNode.PageID)
		if err != nil {
			return 0, fmt.Errorf("failed to copy page %d: %w", pathNode.PageID, err)
		}

		// Get the new page
		newPage, err := b.pageManager.Get(newPageID)
		if err != nil {
			return 0, fmt.Errorf("failed to get new page %d: %w", newPageID, err)
		}

		// Deserialize node from new page
		newNode := b.deserializeNode(newPage)

		if i == len(path)-1 {
			// This is the leaf node, apply modification
			if err := modifyFunc(newNode); err != nil {
				b.pageManager.Release(newPage)
				return 0, fmt.Errorf("failed to modify leaf node: %w", err)
			}
		} else {
			// This is an internal node, update child reference
			childPathNode := path[i+1]
			if err := b.updateChildReference(newNode, childPathNode); err != nil {
				b.pageManager.Release(newPage)
				return 0, fmt.Errorf("failed to update child reference: %w", err)
			}
		}

		// Serialize node back to page
		if err := b.serializeNodeToPage(newNode, newPage); err != nil {
			b.pageManager.Release(newPage)
			return 0, fmt.Errorf("failed to serialize node: %w", err)
		}

		// Mark page as dirty
		newPage.MarkDirty()

		// Invalidate old page from cache (CCOW creates new version)
		b.nodeCache.Invalidate(pathNode.PageID)

		// Store new page ID for next iteration
		path[i].PageID = newPageID

		b.pageManager.Release(newPage)
	}

	// Return the new root page ID
	return path[0].PageID, nil
}

// copyPage creates a copy of the given page.
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

	// Copy data from old page to new page
	copy(newPage.Data[:], page.Data[:])

	// Copy metadata
	newPage.Type = page.Type
	newPage.Version = page.Version + 1
	newPage.MarkDirty()

	// Get the page ID before releasing
	newPageID := newPage.ID

	b.pageManager.Release(newPage)

	return newPageID, nil
}

// updateChildReference updates the child reference in the parent node.
func (b *BTree) updateChildReference(parentNode *Node, childPathNode *PathNode) error {
	// Find the index to update
	// We need to find the child that points to the old page ID
	for i, childID := range parentNode.Children {
		if childID == childPathNode.PageID {
			// Update the child reference to point to the new page
			parentNode.Children[i] = childPathNode.PageID
			return nil
		}
	}

	return fmt.Errorf("child reference not found")
}

// serializeNodeToPage serializes a node to a page.
func (b *BTree) serializeNodeToPage(node *Node, page *Page) error {
	// This is a placeholder - will be implemented with serializer
	// For now, just mark the page as dirty
	page.MarkDirty()
	return nil
}

// deserializeNode deserializes a node from a page.
func (b *BTree) deserializeNode(page *Page) *Node {
	// This is a placeholder - will be implemented with serializer
	// For now, return a new node
	return NewNode(page.Type == model.LeafPage)
}

// ModifyPage modifies a page with the given key-value pair.
// This is used by the CopyPathBottomUp operation.
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

	return b.serializeNodeToPage(node, page)
}

// ModifyOperation represents the type of modification.
type ModifyOperation int

const (
	ModifyInsert ModifyOperation = iota
	ModifyUpdate
	ModifyDelete
)
